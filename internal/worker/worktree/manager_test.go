package worktree_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/garagefab/garagefab/internal/worker/worktree"
)

func createTestGitRepo(t *testing.T) string {
	t.Helper()
	repoDir := filepath.Join(t.TempDir(), "test-repo")
	if err := os.MkdirAll(repoDir, 0700); err != nil {
		t.Fatalf("mkdir repoDir failed: %v", err)
	}

	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %s: %v", args, string(out), err)
		}
	}

	runGit("init", "-b", "main")
	runGit("config", "user.name", "Test User")
	runGit("config", "user.email", "test@example.com")

	// Commit initial file
	initFile := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(initFile, []byte("# Test Repo\n"), 0600); err != nil {
		t.Fatalf("write init file failed: %v", err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "initial commit")

	return repoDir
}

func TestWorktree_Lifecycle_WKT1_3_4_5_6(t *testing.T) {
	ctx := context.Background()
	repoDir := createTestGitRepo(t)
	baseDir := filepath.Join(t.TempDir(), "worktrees")
	mgr := worktree.NewManager(baseDir)

	// 1. Create worktree (WKT-1, WKT-3)
	info, err := mgr.Create(ctx, repoDir, "test-proj", 101, "main")
	if err != nil {
		t.Fatalf("Create worktree failed: %v", err)
	}
	if info.Path == "" || info.BaseSHA == "" || info.Branch != "garagefab/job-101" {
		t.Fatalf("unexpected WorktreeInfo: %+v", info)
	}

	// 2. Check developer's own checkout is unmodified (WKT-4)
	gitStatusCmd := exec.Command("git", "-C", repoDir, "status", "--porcelain")
	statusOut, err := gitStatusCmd.Output()
	if err != nil || len(statusOut) > 0 {
		t.Fatalf("WKT-4 violated: main repo status modified: %s", string(statusOut))
	}

	// 3. Make change in worktree and checkpoint (WKT-5, COD-9)
	worktreeFile := filepath.Join(info.Path, "feature.txt")
	if err := os.WriteFile(worktreeFile, []byte("new feature"), 0600); err != nil {
		t.Fatalf("write worktree file failed: %v", err)
	}

	headSHA, err := mgr.Checkpoint(ctx, info.Path, 101, "add feature file")
	if err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}
	if headSHA == info.BaseSHA {
		t.Errorf("expected headSHA to differ from baseSHA after checkpoint")
	}

	// 4. Verify Diff (WKT-3)
	diff, err := mgr.Diff(ctx, info.Path, info.BaseSHA)
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}
	if diff == "" {
		t.Errorf("expected non-empty diff")
	}

	// 5. Modify file again and test Reset (WKT-5)
	if err := os.WriteFile(worktreeFile, []byte("broken edit"), 0600); err != nil {
		t.Fatalf("write broken edit failed: %v", err)
	}
	if err := mgr.Reset(ctx, info.Path, headSHA); err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	content, err := os.ReadFile(worktreeFile)
	if err != nil || string(content) != "new feature" {
		t.Fatalf("Reset failed to restore checkpoint content, got: %s", string(content))
	}

	// 6. Remove worktree (WKT-6)
	if err := mgr.Remove(ctx, repoDir, info.Path, info.Branch, false); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if _, err := os.Stat(info.Path); !os.IsNotExist(err) {
		t.Errorf("expected worktree path to be removed, got err: %v", err)
	}
}

func TestWorktree_DuplicateBranch_WKT7(t *testing.T) {
	ctx := context.Background()
	repoDir := createTestGitRepo(t)
	baseDir := filepath.Join(t.TempDir(), "worktrees")
	mgr := worktree.NewManager(baseDir)

	// Create job 201
	info, err := mgr.Create(ctx, repoDir, "test-proj", 201, "main")
	if err != nil {
		t.Fatalf("first Create failed: %v", err)
	}

	// Try creating again with same jobID (branch garagefab/job-201 exists)
	_, err = mgr.Create(ctx, repoDir, "test-proj", 201, "main")
	if !errors.Is(err, worktree.ErrBranchAlreadyExists) {
		t.Fatalf("WKT-7 violated: expected ErrBranchAlreadyExists, got %v", err)
	}

	_ = mgr.Remove(ctx, repoDir, info.Path, info.Branch, true)
}

func TestWorktree_Serialization_WKT9(t *testing.T) {
	ctx := context.Background()
	repoDir := createTestGitRepo(t)
	baseDir := filepath.Join(t.TempDir(), "worktrees")
	mgr := worktree.NewManager(baseDir)

	var wg sync.WaitGroup
	errCh := make(chan error, 5)

	// Start 5 concurrent worktree creations on the same repository (WKT-9)
	for i := 1; i <= 5; i++ {
		wg.Add(1)
		jobID := int64(300 + i)
		go func(id int64) {
			defer wg.Done()
			info, err := mgr.Create(ctx, repoDir, "test-proj", id, "main")
			if err != nil {
				errCh <- err
				return
			}
			// Clean up
			_ = mgr.Remove(ctx, repoDir, info.Path, info.Branch, true)
		}(jobID)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("WKT-9 serialization failed with concurrent worktrees: %v", err)
	}
}
