// Package factory_test contains isolated unit tests for the docs profile routing (PIP-1, REV-6, OQ-2).
package factory_test

import (
	"context"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/factory"
)

func newDocsJob() (*MockStore, *MockWorktreeManager) {
	store := newMockStore()
	wtMgr := newMockWorktreeManager()
	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha", BaseRef: "main"}
	store.jobs[1] = &factory.Job{
		ID:        1,
		ProjectID: 1,
		WorkType:  factory.WorkTypeDocs,
		Title:     "Update README",
		Intent:    "Document the public API",
		Stage:     factory.StageIntent,
		Status:    factory.StatusQueued,
	}
	return store, wtMgr
}

// TestDocsProfile_SkipsGatesAndCommands_PIP1: a docs job runs 01 -> 04 -> 05 -> 07, never enters
// 02/03/06, and runs no build/test/lint commands (PIP-1).
func TestDocsProfile_SkipsGatesAndCommands_PIP1(t *testing.T) {
	ctx := context.Background()
	store, wtMgr := newDocsJob()
	engine := factory.NewEngine(store, wtMgr, &MockAgentRunner{}, nil, t.TempDir())

	if err := engine.ExecuteJob(ctx, 1); err != nil {
		t.Fatalf("ExecuteJob failed: %v", err)
	}

	job, _ := store.GetJob(ctx, 1)
	if job.Stage != factory.StageDone || job.Status != factory.StatusDone {
		t.Fatalf("expected 07_Done/done, got %s/%s", job.Stage, job.Status)
	}

	steps, _ := store.ListStepRunsByJob(ctx, 1)
	for _, s := range steps {
		if s.Stage == factory.StageClarificationAndSpec || s.Stage == factory.StageFailingProbe {
			t.Fatalf("docs job must not enter 02/03, got step in %s", s.Stage)
		}
		if s.Stage == factory.StageCoding && s.Kind == factory.StepKindCommand {
			t.Fatalf("docs job must not run build/test/lint commands, got %+v", s)
		}
	}
	for _, e := range store.events {
		if strings.Contains(e.Payload, factory.StageHumanApprovalGate) {
			t.Fatalf("docs job must not enter the approval gate, got event %s", e.Payload)
		}
	}
}

// TestDocsProfile_RequestChanges_GoesToGate_REV6: when the review requests changes, a docs job
// stops at 06/awaiting_approval (REV-6).
func TestDocsProfile_RequestChanges_GoesToGate_REV6(t *testing.T) {
	ctx := context.Background()
	store, wtMgr := newDocsJob()
	engine := factory.NewEngine(store, wtMgr, &MockAgentRunner{}, nil, t.TempDir())

	requestChanges := `{
  "schema_version": 1,
  "decision": "request_changes",
  "summary": "Docs need more detail",
  "risk": {
    "side_effect": {"score": 1, "rationale": "low"},
    "performance": {"score": 1, "rationale": "low"},
    "backward_compatibility": {"score": 1, "rationale": "low"}
  },
  "findings": [],
  "warnings": [],
  "spec_coverage": []
}`
	_ = wtMgr.WriteArtifact(ctx, "", 1, "review.json", []byte(requestChanges))

	if err := engine.ExecuteJob(ctx, 1); err != nil {
		t.Fatalf("ExecuteJob failed: %v", err)
	}

	job, _ := store.GetJob(ctx, 1)
	if job.Stage != factory.StageHumanApprovalGate || job.Status != factory.StatusAwaitingApproval {
		t.Fatalf("expected 06/awaiting_approval for request_changes, got %s/%s", job.Stage, job.Status)
	}
}

// TestDocsProfile_Approved_CreatesPR_OQ2: an approved docs job goes straight to delivery and
// creates a PR without a human gate (OQ-2).
func TestDocsProfile_Approved_CreatesPR_OQ2(t *testing.T) {
	ctx := context.Background()
	store, wtMgr := newDocsJob()
	prProvider := &mockPRProvider{}
	engine := factory.NewEngine(store, wtMgr, &MockAgentRunner{}, nil, t.TempDir())
	engine.SetPullRequestProvider(prProvider)
	engine.SetProjectConfigProvider(&MockProjectConfigProvider{cfg: &factory.ProjectConfig{
		BaseRef: "origin/main",
		GitHub:  factory.ProjectGitHub{Repo: "owner/app", PRIssueKeyword: "closes"},
	}})

	if err := engine.ExecuteJob(ctx, 1); err != nil {
		t.Fatalf("ExecuteJob failed: %v", err)
	}

	if len(prProvider.createdReqs) != 1 {
		t.Fatalf("expected 1 CreatePullRequest call, got %d", len(prProvider.createdReqs))
	}
	job, _ := store.GetJob(ctx, 1)
	if job.Stage != factory.StageDone || job.Status != factory.StatusDone {
		t.Fatalf("expected 07_Done/done, got %s/%s", job.Stage, job.Status)
	}
	if job.PRURL == "" {
		t.Fatal("expected a persisted PR URL")
	}
	// The evidence checkpoint head must be persisted, not only held in memory.
	if job.HeadSHA != wtMgr.checkpoints[1] {
		t.Fatalf("expected the persisted head SHA to match the last checkpoint %q, got %q", wtMgr.checkpoints[1], job.HeadSHA)
	}
}
