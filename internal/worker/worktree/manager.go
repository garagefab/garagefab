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
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	BaseSHA   string `json:"base_sha"`
	FetchWarn bool   `json:"fetch_warn"`
}

// Manager manages git worktrees for jobs with per-project mutex serialization (WKT-1..9).
type Manager struct {
	worktreeBaseDir string
	mu              sync.Mutex
	projectLocks    map[string]*sync.Mutex
}

// NewManager creates a worktree manager storing worktrees under worktreeBaseDir.
func NewManager(worktreeBaseDir string) *Manager {
	return &Manager{
		worktreeBaseDir: worktreeBaseDir,
		projectLocks:    make(map[string]*sync.Mutex),
	}
}

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

// Create creates a new worktree for jobID branched from baseRef (WKT-1, WKT-2, WKT-3, WKT-9).
// Operations on the same repository are serialized (WKT-9).
func (m *Manager) Create(ctx context.Context, repoPath, projectName string, jobID int64, baseRef string) (*WorktreeInfo, error) {
	lock := m.getProjectLock(repoPath)
	lock.Lock()
	defer lock.Unlock()

	branchName := fmt.Sprintf("garagefab/job-%d", jobID)

	// Check if branch already exists (WKT-7)
	checkBranchCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", "refs/heads/"+branchName)
	if err := checkBranchCmd.Run(); err == nil {
		return nil, fmt.Errorf("%w: %s in %s", ErrBranchAlreadyExists, branchName, repoPath)
	}

	if baseRef == "" {
		baseRef = "origin/main"
	}

	// Fetch baseRef remote with timeout (WKT-1, WKT-2)
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
		// Verify local fallback exists (WKT-2)
		localRef := baseRef
		if strings.HasPrefix(baseRef, remote+"/") {
			localRef = strings.TrimPrefix(baseRef, remote+"/")
		}

		checkLocal := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", localRef)
		if err := checkLocal.Run(); err != nil {
			// Also check if remote tracking branch is cached locally
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

	// Ensure worktreePath is clean before adding
	_ = os.RemoveAll(worktreePath)

	// Add git worktree: git worktree add -b garagefab/job-<id> <path> <base_ref> (WKT-1)
	addCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "add", "-b", branchName, worktreePath, baseRef)
	if out, err := addCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("worktree: git worktree add failed: %s: %w", string(out), err)
	}

	// Get base SHA in the worktree (WKT-3)
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

// Checkpoint creates a checkpoint commit with all current changes in the worktree (WKT-5, COD-9).
func (m *Manager) Checkpoint(ctx context.Context, worktreePath string, jobID int64, message string) (string, error) {
	addCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "add", "-A")
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("worktree: checkpoint git add: %s: %w", string(out), err)
	}

	commitMsg := fmt.Sprintf("garagefab(job-%d): %s", jobID, message)
	commitCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "commit", "--no-verify", "--allow-empty", "-m", commitMsg)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("worktree: checkpoint git commit: %s: %w", string(out), err)
	}

	shaCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "rev-parse", "HEAD")
	shaBytes, err := shaCmd.Output()
	if err != nil {
		return "", fmt.Errorf("worktree: checkpoint rev-parse HEAD: %w", err)
	}

	return strings.TrimSpace(string(shaBytes)), nil
}

// Reset resets worktree to the specified checkpoint SHA (WKT-5).
func (m *Manager) Reset(ctx context.Context, worktreePath, targetSHA string) error {
	if targetSHA == "" {
		targetSHA = "HEAD"
	}

	resetCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "reset", "--hard", targetSHA)
	if out, err := resetCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("worktree: reset --hard: %s: %w", string(out), err)
	}

	cleanCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "clean", "-fd")
	if out, err := cleanCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("worktree: clean -fd: %s: %w", string(out), err)
	}

	return nil
}

// Remove removes the worktree and prunes worktree metadata (WKT-4, WKT-6, WKT-9).
func (m *Manager) Remove(ctx context.Context, repoPath, worktreePath, branchName string, deleteBranch bool) error {
	lock := m.getProjectLock(repoPath)
	lock.Lock()
	defer lock.Unlock()

	// Remove worktree
	if _, err := os.Stat(worktreePath); err == nil {
		removeCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "remove", "--force", worktreePath)
		_ = removeCmd.Run()
		_ = os.RemoveAll(worktreePath)
	}

	pruneCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "prune")
	_ = pruneCmd.Run()

	if deleteBranch && branchName != "" {
		delBranchCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "branch", "-D", branchName)
		_ = delBranchCmd.Run()
	}

	return nil
}

// Diff returns the git diff from the merge-base of baseSHA and HEAD (WKT-3).
func (m *Manager) Diff(ctx context.Context, worktreePath, baseSHA string) (string, error) {
	mbCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "merge-base", baseSHA, "HEAD")
	mbOut, err := mbCmd.Output()
	if err != nil {
		return "", fmt.Errorf("worktree: merge-base: %w", err)
	}
	mergeBase := strings.TrimSpace(string(mbOut))

	diffCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "diff", mergeBase, "HEAD")
	diffOut, err := diffCmd.Output()
	if err != nil {
		return "", fmt.Errorf("worktree: diff: %w", err)
	}

	return string(diffOut), nil
}

// HeadSHA returns the current HEAD SHA of the worktree.
func (m *Manager) HeadSHA(ctx context.Context, worktreePath string) (string, error) {
	shaCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "rev-parse", "HEAD")
	out, err := shaCmd.Output()
	if err != nil {
		return "", fmt.Errorf("worktree: rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
