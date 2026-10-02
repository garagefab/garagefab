// Package worktree_test contains integration tests for Git worktree management.
//
// ==============================================================================
// GO TESTING CONCEPTS:
//
//  1. Concurrency Testing with Channels (`chan error`):
//     When testing concurrent code (spawning multiple goroutines), asserting directly
//     with `t.Fatalf` inside a child goroutine is dangerous because `Fatalf` terminates
//     only the running goroutine, not the test itself, causing deadlocks or missed errors.
//     Idiomatic Go Solution:
//     - Create a buffered error channel: `errCh := make(chan error, N)`.
//     - Send errors from goroutines into `errCh <- err`.
//     - Wait for all goroutines to finish with `wg.Wait()`.
//     - Close the channel: `close(errCh)`.
//     - Drain and assert errors on the main test goroutine: `for err := range errCh`.
//
//  2. Error Inspection with `errors.Is`:
//     Go 1.13 introduced `errors.Is` to check whether an error wraps a specific sentinel
//     error (like `worktree.ErrBranchAlreadyExists`), similar to checking exception types
//     with `instanceof` in Java.
//
// ==============================================================================
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

// createTestGitRepo initializes a physical Git repository with an initial commit in a temp folder.
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

	// Commit initial file so HEAD points to a valid commit
	initFile := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(initFile, []byte("# Test Repo\n"), 0600); err != nil {
		t.Fatalf("write init file failed: %v", err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "initial commit")

	return repoDir
}

// TestWorktree_Lifecycle_WKT1_3_4_5_6 exercises the complete worktree lifecycle:
// Create -> Check developer checkout cleanliness -> Checkpoint -> Diff -> Reset -> Remove.
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

	// 3. Make change in worktree and create checkpoint commit (WKT-5, COD-9)
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

	// 4. Verify Diff generation (WKT-3)
	diff, err := mgr.Diff(ctx, info.Path, info.BaseSHA)
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}
	if diff == "" {
		t.Errorf("expected non-empty diff")
	}

	// 5. Modify file again and test rollback with Reset (WKT-5)
	if err := os.WriteFile(worktreeFile, []byte("broken edit"), 0600); err != nil {
		t.Fatalf("write broken edit failed: %v", err)
	}
	if err := mgr.Reset(ctx, info.Path, headSHA); err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Verify content was restored to the checkpoint commit
	content, err := os.ReadFile(worktreeFile)
	if err != nil || string(content) != "new feature" {
		t.Fatalf("Reset failed to restore checkpoint content, got: %s", string(content))
	}

	// 6. Remove worktree cleanly from disk (WKT-6)
	if err := mgr.Remove(ctx, repoDir, info.Path, info.Branch, false); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if _, err := os.Stat(info.Path); !os.IsNotExist(err) {
		t.Errorf("expected worktree path to be removed, got err: %v", err)
	}
}

// TestWorktree_DuplicateBranch_WKT7 verifies requirement WKT-7:
// Attempting to create a worktree on an existing branch returns ErrBranchAlreadyExists.
func TestWorktree_DuplicateBranch_WKT7(t *testing.T) {
	ctx := context.Background()
	repoDir := createTestGitRepo(t)
	baseDir := filepath.Join(t.TempDir(), "worktrees")
	mgr := worktree.NewManager(baseDir)

	// Create initial worktree for job 201
	info, err := mgr.Create(ctx, repoDir, "test-proj", 201, "main")
	if err != nil {
		t.Fatalf("first Create failed: %v", err)
	}

	// Attempt second creation with same jobID (branch garagefab/job-201 already exists)
	_, err = mgr.Create(ctx, repoDir, "test-proj", 201, "main")
	if !errors.Is(err, worktree.ErrBranchAlreadyExists) {
		t.Fatalf("WKT-7 violated: expected ErrBranchAlreadyExists, got %v", err)
	}

	_ = mgr.Remove(ctx, repoDir, info.Path, info.Branch, true)
}

// TestWorktree_Serialization_WKT9 verifies requirement WKT-9:
// Concurrent worktree creations on the same repository are serialized without crashing Git.
func TestWorktree_Serialization_WKT9(t *testing.T) {
	ctx := context.Background()
	repoDir := createTestGitRepo(t)
	baseDir := filepath.Join(t.TempDir(), "worktrees")
	mgr := worktree.NewManager(baseDir)

	var wg sync.WaitGroup
	errCh := make(chan error, 5) // Channel to safely collect errors from concurrent goroutines

	// Launch 5 parallel goroutines attempting worktree operations on the same repository
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
			// Clean up after creation
			_ = mgr.Remove(ctx, repoDir, info.Path, info.Branch, true)
		}(jobID)
	}

	wg.Wait()
	close(errCh)

	// Check if any goroutine encountered an error (such as an index.lock collision)
	for err := range errCh {
		t.Fatalf("WKT-9 serialization failed with concurrent worktrees: %v", err)
	}
}
