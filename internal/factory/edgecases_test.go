// Package factory_test contains edge-case tests from spec §8 (worktree failure, categorizer).
package factory_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/factory"
)

// failingWorktreeManager forces worktree creation to fail (spec §8).
type failingWorktreeManager struct {
	*MockWorktreeManager
}

func (f *failingWorktreeManager) Create(ctx context.Context, repoPath, projectName string, jobID int64, baseRef string) (*factory.WorktreeInfo, error) {
	return nil, fmt.Errorf("git worktree add failed: disk full")
}

// TestEngine_WorktreeCreateFailure_Blocked verifies that a worktree creation failure fails the
// job with git's message (spec §8).
func TestEngine_WorktreeCreateFailure_Blocked(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	wtMgr := &failingWorktreeManager{newMockWorktreeManager()}
	engine := factory.NewEngine(store, wtMgr, &MockAgentRunner{}, nil, t.TempDir())

	store.projects[1] = &factory.Project{ID: 1, Name: "p", RepoPath: "/repos/p", BaseRef: "main"}
	store.jobs[1] = &factory.Job{
		ID: 1, ProjectID: 1, WorkType: factory.WorkTypeRefactor,
		Title: "t", Intent: "x", Stage: factory.StageIntent, Status: factory.StatusQueued,
	}

	if err := engine.ExecuteJob(ctx, 1); err == nil {
		t.Fatal("expected ExecuteJob to fail when worktree creation fails")
	}

	job, _ := store.GetJob(ctx, 1)
	if job.Status != factory.StatusFailed {
		t.Fatalf("expected job failed, got %s/%s", job.Stage, job.Status)
	}

	var surfaced bool
	for _, e := range store.events {
		if strings.Contains(e.Payload, "git worktree add failed") {
			surfaced = true
		}
	}
	if !surfaced {
		t.Fatalf("expected git's message in the failure event, got %+v", store.events)
	}
}

// TestCategorizeFailure_CommandNotFound_Blocked verifies exit 127 is categorized as Blocked
// (spec §8: command not found / permission denied).
func TestCategorizeFailure_CommandNotFound_Blocked(t *testing.T) {
	got := factory.CategorizeFailure(factory.FailureInput{ExitCode: 127, Attempt: 1, MaxAttempts: 3})
	if got != factory.FailureBlocked {
		t.Fatalf("expected Blocked for exit 127, got %q", got)
	}
}
