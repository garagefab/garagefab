// Package worktree manages the lifecycle of isolated Git worktrees for parallel jobs.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Git Worktree Isolation & Per-Project Concurrency Serialization (WKT-1..9).
//
// What is a Git Worktree?
// In standard Git, a repository has a single working directory linked to `.git`. You can
// only checkout one branch at a time.
// `git worktree add` allows checking out multiple branches simultaneously into separate,
// isolated directories while sharing the same underlying `.git` object store and history.
//
// Why is this fundamental to Garagefab?
// 1. Isolation: Coding agents run in their own worktree (`~/.garagefab/worktrees/<project>/<job-id>`).
// 2. Zero Contamination: The developer's primary checkout and branch are NEVER modified (WKT-4).
// 3. Parallelism: Multiple jobs can execute concurrently on different branches of the same repo.
//
// GO CONCEPTS & CONCURRENCY CONTROLS:
//
//  1. Per-Project Mutex Map (`sync.Mutex`):
//     Although worktrees are isolated directories, Git's internal index and ref updates
//     (e.g. `git worktree add` or `git branch`) share `.git/index.lock`.
//     If two goroutines run `git worktree add` simultaneously on the same repository,
//     Git crashes with "index.lock already exists".
//     We solve this with a thread-safe mutex map:
//     - `m.mu` (master mutex) protects the map of project locks.
//     - `m.projectLocks[repoPath]` serializes operations on that specific repository (WKT-9).
//
// ==============================================================================
package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	// ErrBranchAlreadyExists is returned when a leftover job branch already exists (WKT-7).
	ErrBranchAlreadyExists = errors.New("worktree: branch already exists")
	// ErrBaseRefNotFound is returned when the specified base ref cannot be resolved locally or remotely.
	ErrBaseRefNotFound = errors.New("worktree: base ref not found")
)

// WorktreeInfo contains metadata about an active worktree.
type WorktreeInfo struct {
	Path      string `json:"path"`       // Absolute path to the isolated worktree directory on disk
	Branch    string `json:"branch"`     // Dedicated git branch name (e.g. "garagefab/job-42")
	BaseSHA   string `json:"base_sha"`   // Starting Git commit SHA that the branch was forked from
	FetchWarn bool   `json:"fetch_warn"` // True if remote fetch timed out or failed, using local fallback
}

// Manager manages git worktrees for jobs with per-project mutex serialization (WKT-1..9).
type Manager struct {
	worktreeBaseDir string                 // Base directory where worktrees are provisioned (~/.garagefab/worktrees)
	mu              sync.Mutex             // Master mutex protecting the projectLocks and worktreeLocks maps
	projectLocks    map[string]*sync.Mutex // Map from repoPath -> Mutex for per-project serialization
	worktreeLocks   map[string]*sync.Mutex // Map from worktreePath -> Mutex for per-worktree serialization
}

// NewManager creates a worktree manager storing worktrees under worktreeBaseDir.
func NewManager(worktreeBaseDir string) *Manager {
	return &Manager{
		worktreeBaseDir: worktreeBaseDir,
		projectLocks:    make(map[string]*sync.Mutex),
		worktreeLocks:   make(map[string]*sync.Mutex),
	}
}

// getProjectLock returns the synchronization mutex for the specified repository path.
// It uses `m.mu` to safely read or insert into the map.
func (m *Manager) getProjectLock(repoPath string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleanPath := filepath.Clean(repoPath)
	lock, ok := m.projectLocks[cleanPath]
	if !ok {
		lock = &sync.Mutex{}
		m.projectLocks[cleanPath] = lock
	}
	return lock
}

// getWorktreeLock returns the synchronization mutex for the specified worktree path.
// It uses `m.mu` to safely read or insert into the map.
//
// Concurrency & Git Index Isolation:
// While `getProjectLock` serializes worktree additions and branch deletions on the main repo,
// individual worktrees have their own `.git/worktrees/<id>/index` file. Operations that modify
// or read the worktree index (such as `Diff` with `git add -N .`, `Checkpoint` with `git add -A / commit`,
// or `Reset`) will conflict on `index.lock` if called concurrently (for instance, when the web dashboard
// or an SSE listener calls `/api/jobs/:id/diff` while the pipeline engine creates a checkpoint commit).
// Serializing access per worktree path completely eliminates transient `index.lock` collisions.
func (m *Manager) getWorktreeLock(worktreePath string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleanPath := filepath.Clean(worktreePath)
	lock, ok := m.worktreeLocks[cleanPath]
	if !ok {
		lock = &sync.Mutex{}
		m.worktreeLocks[cleanPath] = lock
	}
	return lock
}

// Create creates a new worktree for jobID branched from baseRef (WKT-1, WKT-2, WKT-3, WKT-9).
// All operations targeting the same repository are serialized to prevent `.git/index.lock` collisions.
func (m *Manager) Create(ctx context.Context, repoPath, projectName string, jobID int64, baseRef string) (*WorktreeInfo, error) {
	// Acquire per-repository mutex (WKT-9)
	lock := m.getProjectLock(repoPath)
	lock.Lock()
	defer lock.Unlock()

	branchName := fmt.Sprintf("garagefab/job-%d", jobID)

	// Check if branch already exists from a previous crash or duplicate intake (WKT-7)
	checkBranchCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", "refs/heads/"+branchName)
	if err := checkBranchCmd.Run(); err == nil {
		return nil, fmt.Errorf("%w: %s in %s", ErrBranchAlreadyExists, branchName, repoPath)
	}

	if baseRef == "" {
		baseRef = "origin/main"
	}

	// Fetch baseRef from remote with a 10s timeout (WKT-1, WKT-2)
	fetchWarn := false
	remote := "origin"
	if parts := strings.Split(baseRef, "/"); len(parts) > 1 {
		remote = parts[0]
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	fetchCmd := exec.CommandContext(fetchCtx, "git", "-C", repoPath, "fetch", remote)
	if err := fetchCmd.Run(); err != nil {
		fetchWarn = true
		// Verify local fallback ref exists if offline or fetch failed (WKT-2)
		localRef := baseRef
		if strings.HasPrefix(baseRef, remote+"/") {
			localRef = strings.TrimPrefix(baseRef, remote+"/")
		}

		checkLocal := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", localRef)
		if err := checkLocal.Run(); err != nil {
			// Also check if remote tracking branch is cached locally in refs/remotes
			checkRemoteLocal := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", baseRef)
			if err := checkRemoteLocal.Run(); err != nil {
				return nil, fmt.Errorf("%w: fetch failed and local ref %q not found: %v", ErrBaseRefNotFound, baseRef, err)
			}
		} else {
			baseRef = localRef
		}
	}

	// Target worktree directory: ~/.garagefab/worktrees/<project>/<job-id> (WKT-1)
	worktreePath := filepath.Join(m.worktreeBaseDir, projectName, fmt.Sprintf("%d", jobID))
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0700); err != nil {
		return nil, fmt.Errorf("worktree: create parent dir: %w", err)
	}

	// Clean any dangling directory at target path
	_ = os.RemoveAll(worktreePath)

	// Add git worktree: git worktree add -b garagefab/job-<id> <path> <base_ref> (WKT-1)
	addCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "add", "-b", branchName, worktreePath, baseRef)
	if out, err := addCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("worktree: git worktree add failed: %s: %w", string(out), err)
	}

	// Capture the exact base commit SHA in the newly created worktree (WKT-3)
	shaCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "rev-parse", "HEAD")
	baseSHABytes, err := shaCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("worktree: rev-parse HEAD: %w", err)
	}
	baseSHA := strings.TrimSpace(string(baseSHABytes))

	return &WorktreeInfo{
		Path:      worktreePath,
		Branch:    branchName,
		BaseSHA:   baseSHA,
		FetchWarn: fetchWarn,
	}, nil
}

// Checkpoint stages all changes and creates an automated git commit in the worktree (WKT-5, COD-9).
// It returns the newly created commit SHA.
func (m *Manager) Checkpoint(ctx context.Context, worktreePath string, jobID int64, message string) (string, error) {
	// Serialize operations on this worktree to prevent concurrent index.lock collisions (e.g. with Diff)
	lock := m.getWorktreeLock(worktreePath)
	lock.Lock()
	defer lock.Unlock()

	// Stage all tracked and untracked files
	addCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "add", "-A")
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("worktree: checkpoint git add: %s: %w", string(out), err)
	}

	// Create commit with --no-verify (bypassing pre-commit hooks) and --allow-empty
	commitMsg := fmt.Sprintf("garagefab(job-%d): %s", jobID, message)
	commitCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "commit", "--no-verify", "--allow-empty", "-m", commitMsg)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("worktree: checkpoint git commit: %s: %w", string(out), err)
	}

	// Retrieve the commit SHA of the checkpoint
	shaCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "rev-parse", "HEAD")
	shaBytes, err := shaCmd.Output()
	if err != nil {
		return "", fmt.Errorf("worktree: checkpoint rev-parse HEAD: %w", err)
	}

	return strings.TrimSpace(string(shaBytes)), nil
}

// Reset resets the worktree to a previous target checkpoint SHA (WKT-5).
// It rolls back both tracked modifications (`git reset --hard`) and untracked files (`git clean -fd`).
func (m *Manager) Reset(ctx context.Context, worktreePath, targetSHA string) error {
	// Serialize worktree access to ensure reset and clean operations do not race with other commands
	lock := m.getWorktreeLock(worktreePath)
	lock.Lock()
	defer lock.Unlock()

	if targetSHA == "" {
		targetSHA = "HEAD"
	}

	// Discard tracked changes back to target commit
	resetCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "reset", "--hard", targetSHA)
	if out, err := resetCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("worktree: reset --hard: %s: %w", string(out), err)
	}

	// Delete untracked files and directories created during the failed attempt
	cleanCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "clean", "-fd")
	if out, err := cleanCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("worktree: clean -fd: %s: %w", string(out), err)
	}

	return nil
}

// Remove cleanly unregisters and deletes the worktree from disk (WKT-4, WKT-6, WKT-9).
// It also prunes stale worktree administrative metadata from `.git/worktrees/`.
func (m *Manager) Remove(ctx context.Context, repoPath, worktreePath, branchName string, deleteBranch bool) error {
	// Serialize against other worktree operations on the same repository
	repoLock := m.getProjectLock(repoPath)
	repoLock.Lock()
	defer repoLock.Unlock()

	wtLock := m.getWorktreeLock(worktreePath)
	wtLock.Lock()
	defer wtLock.Unlock()

	// Clean up worktree mutex after removal
	defer func() {
		m.mu.Lock()
		delete(m.worktreeLocks, filepath.Clean(worktreePath))
		m.mu.Unlock()
	}()

	// 1. Unregister and delete worktree directory
	if _, err := os.Stat(worktreePath); err == nil {
		removeCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "remove", "--force", worktreePath)
		_ = removeCmd.Run()
		_ = os.RemoveAll(worktreePath)
	}

	// 2. Prune git internal worktree tracking records
	pruneCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "prune")
	_ = pruneCmd.Run()

	// 3. Optionally delete the job branch
	if deleteBranch && branchName != "" {
		delBranchCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "branch", "-D", branchName)
		_ = delBranchCmd.Run()
	}

	return nil
}

// Diff returns the git diff output from the merge-base of baseSHA and the current worktree (WKT-3, COD-9).
// This accurately reflects all modifications introduced by the job, including uncommitted edits and newly added files.
func (m *Manager) Diff(ctx context.Context, worktreePath, baseSHA string) (string, error) {
	// Serialize operations on this worktree to prevent concurrent index mutations (git add -N vs commit)
	lock := m.getWorktreeLock(worktreePath)
	lock.Lock()
	defer lock.Unlock()

	// Find the common ancestor commit
	mbCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "merge-base", baseSHA, "HEAD")
	mbOut, err := mbCmd.Output()
	if err != nil {
		return "", fmt.Errorf("worktree: merge-base: %w", err)
	}
	mergeBase := strings.TrimSpace(string(mbOut))

	// Intent-to-add untracked files so new files appear in the diff (COD-7, COD-9)
	addNCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "add", "-N", ".")
	_ = addNCmd.Run()

	// Compute diff between merge base and current worktree
	diffCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "diff", mergeBase)
	diffOut, err := diffCmd.Output()
	if err != nil {
		return "", fmt.Errorf("worktree: diff: %w", err)
	}

	return string(diffOut), nil
}

// HeadSHA returns the commit hash of the current HEAD in the worktree.
func (m *Manager) HeadSHA(ctx context.Context, worktreePath string) (string, error) {
	// Serialize access for consistency
	lock := m.getWorktreeLock(worktreePath)
	lock.Lock()
	defer lock.Unlock()

	shaCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "rev-parse", "HEAD")
	out, err := shaCmd.Output()
	if err != nil {
		return "", fmt.Errorf("worktree: rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// JobArtifactDir returns the absolute path to a job's artifact directory (.garagefab/jobs/<jobID>)
// inside the given worktree directory (LOG-3, spec §6.7).
func JobArtifactDir(worktreePath string, jobID int64) string {
	return filepath.Join(worktreePath, ".garagefab", "jobs", fmt.Sprintf("%d", jobID))
}

// WriteArtifact writes an artifact file inside the job's dedicated artifact directory (LOG-3).
// If parent directories do not exist, they are created automatically.
func (m *Manager) WriteArtifact(ctx context.Context, worktreePath string, jobID int64, filename string, content []byte) error {
	dir := JobArtifactDir(worktreePath, jobID)
	fullPath := filepath.Join(dir, filename)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("worktree: create artifact dir: %w", err)
	}
	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		return fmt.Errorf("worktree: write artifact %s: %w", filename, err)
	}
	return nil
}

// ReadArtifact reads the specified artifact file from the job's artifact directory (LOG-3).
func (m *Manager) ReadArtifact(ctx context.Context, worktreePath string, jobID int64, filename string) ([]byte, error) {
	dir := JobArtifactDir(worktreePath, jobID)
	fullPath := filepath.Join(dir, filename)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("worktree: read artifact %s: %w", filename, err)
	}
	return data, nil
}

// RemoveArtifact deletes the specified artifact file from the job's artifact directory (LOG-3).
// If the file does not exist, no error is returned (idempotent).
func (m *Manager) RemoveArtifact(ctx context.Context, worktreePath string, jobID int64, filename string) error {
	dir := JobArtifactDir(worktreePath, jobID)
	fullPath := filepath.Join(dir, filename)
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("worktree: remove artifact %s: %w", filename, err)
	}
	return nil
}

// ListArtifacts returns all relative file paths residing under the job's artifact directory (LOG-3).
func (m *Manager) ListArtifacts(ctx context.Context, worktreePath string, jobID int64) ([]string, error) {
	dir := JobArtifactDir(worktreePath, jobID)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}
	var list []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, rErr := filepath.Rel(dir, path)
			if rErr == nil {
				list = append(list, rel)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("worktree: list artifacts: %w", err)
	}
	return list, nil
}

// Push pushes the specified job branch from the worktree to the remote repository (DLV-1, DLV-3).
// It acquires the per-worktree mutex lock to prevent concurrent Git operations.
func (m *Manager) Push(ctx context.Context, worktreePath, remote, branch string) error {
	lock := m.getWorktreeLock(worktreePath)
	lock.Lock()
	defer lock.Unlock()

	if remote == "" {
		remote = "origin"
	}

	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "push", "-u", remote, branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("worktree: git push %s %s: %s: %w", remote, branch, strings.TrimSpace(string(out)), err)
	}
	return nil
}
