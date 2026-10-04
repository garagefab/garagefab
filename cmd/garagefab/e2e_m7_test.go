// Package main contains the M7 end-to-end scenario (Scenario 6: bug-fix probe).
//
// ==============================================================================
// ARCHITECTURAL ROLE:
// Composition-root E2E test wiring the real store, worktree manager, command runner,
// guardrail adapter, and project-config adapter with a scripted probe/coding agent.
//
// Scenario 6 flow:
//
//	01_Intent -> 02_Clarification_and_Spec -> (approve) -> 03_Failing_Probe
//	          -> 04_Coding (fix makes the probe pass) -> 05_Independent_Review
//	          -> 06_Human_Approval_Gate -> (approve) -> 07_Done.
//
// ==============================================================================
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/store"
	"github.com/garagefab/garagefab/internal/worker/command"
	"github.com/garagefab/garagefab/internal/worker/worktree"
)

const scenario6BugFixSpec = `# Fix login bug

## Summary
Login fails for empty passwords.

## Goals and Non-Goals
Goals: reject empty passwords. Non-Goals: password policy changes.

## Reproduction
Call Login with an empty password; it panics today.

## Design
Validate the password before hashing.

## Acceptance Criteria
Given an empty password When Login is called Then AC-1: it returns an error.

## Implementation Plan
1. Add validation (AC-1)

## Test Plan
Unit test the empty-password case.

## Risks and Assumptions
None.
`

// scenario6Agent is a scripted agent that produces a failing probe and then fixes it.
// The probe command is `test -f fixed.marker`, so it fails during stage 03 and passes once
// coding writes fixed.marker.
type scenario6Agent struct{}

func (a *scenario6Agent) Run(ctx context.Context, req factory.AgentRequest) (*factory.AgentResult, error) {
	if req.OnProcessStart != nil {
		req.OnProcessStart(1, 1, time.Now().Unix())
	}
	artDir := filepath.Join(req.WorktreePath, ".garagefab", "jobs", fmt.Sprintf("%d", req.JobID))
	_ = os.MkdirAll(artDir, 0700)

	switch req.Stage {
	case factory.StageClarificationAndSpec:
		_ = os.WriteFile(filepath.Join(artDir, "spec.md"), []byte(scenario6BugFixSpec), 0644)
	case factory.StageFailingProbe:
		_ = os.WriteFile(filepath.Join(req.WorktreePath, "bug_repro_test.go"),
			[]byte("package main\n\nimport \"testing\"\n\nfunc TestBugRepro(t *testing.T) {}\n"), 0644)
		_ = os.WriteFile(filepath.Join(artDir, "probe.json"),
			[]byte(`{"schema_version":1,"command":"test -f fixed.marker","files":["bug_repro_test.go"],"description":"repro"}`), 0644)
	case factory.StageCoding:
		_ = os.WriteFile(filepath.Join(req.WorktreePath, "fixed.marker"), []byte("fixed\n"), 0644)
		_ = os.WriteFile(filepath.Join(req.WorktreePath, "fix.go"), []byte("package main\n\nvar fixed = true\n"), 0644)
	case factory.StageIndependentReview:
		review := `{"schema_version":1,"decision":"approve","summary":"fixed","risk":{"side_effect":{"score":1,"rationale":"low"},"performance":{"score":1,"rationale":"low"},"backward_compatibility":{"score":1,"rationale":"low"}},"findings":[],"warnings":[],"spec_coverage":[]}`
		_ = os.WriteFile(filepath.Join(artDir, "review.json"), []byte(review), 0600)
	}
	return &factory.AgentResult{ExitCode: 0, Summary: "ok"}, nil
}

// TestScenario6_BugFixProbe_PRB1_4_COD8 verifies the full bug-fix pipeline end to end (PRB-1..4, COD-8).
func TestScenario6_BugFixProbe_PRB1_4_COD8(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	repoDir := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	setupGitRepo(t, repoDir)

	cfg, err := config.Load(filepath.Join(tempDir, "data"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	db, err := store.Open(filepath.Join(cfg.DataDir, "garagefab.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	proj := &store.Project{
		Name:             "m7-bugfix",
		RepoPath:         repoDir,
		BaseRef:          "origin/main",
		EnabledWorkTypes: []string{store.WorkTypeBugFix},
	}
	if err := db.Projects().CreateProject(ctx, proj); err != nil {
		t.Fatalf("create project: %v", err)
	}

	storeAdapter := newFactoryStoreAdapter(db)
	wtMgr := newFactoryWorktreeAdapter(worktree.NewManager(filepath.Join(cfg.DataDir, "worktrees")))
	cmdRunner := newFactoryCommandAdapter(command.NewRunner())

	engine := factory.NewEngine(storeAdapter, wtMgr, &scenario6Agent{}, cmdRunner, filepath.Join(cfg.DataDir, "logs"))
	engine.SetGuardrailRunner(newFactoryGuardrailAdapter())
	engine.SetProjectConfigProvider(newFactoryProjectConfigAdapter())
	engine.SetMaxRepairAttempts(2)

	job := &store.Job{
		ProjectID: proj.ID,
		WorkType:  store.WorkTypeBugFix,
		Title:     "Fix login bug",
		Intent:    "Login fails for empty passwords",
		Stage:     store.StageIntent,
		Status:    store.StatusQueued,
	}
	if err := db.Jobs().CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	// 01 -> 02 -> spec_review
	if err := engine.ExecuteJob(ctx, job.ID); err != nil {
		t.Fatalf("spec stage failed: %v", err)
	}
	job, _ = db.Jobs().GetJob(ctx, job.ID)
	if job.Stage != store.StageClarificationAndSpec || job.Status != store.StatusSpecReview {
		t.Fatalf("expected 02/spec_review, got %s/%s", job.Stage, job.Status)
	}

	// Approve spec -> bug_fix routes to 03_Failing_Probe
	if err := engine.Approve(ctx, job.ID, ""); err != nil {
		t.Fatalf("approve spec failed: %v", err)
	}
	job, _ = db.Jobs().GetJob(ctx, job.ID)
	if job.Stage != store.StageFailingProbe || job.Status != store.StatusQueued {
		t.Fatalf("expected 03/queued after spec approval, got %s/%s", job.Stage, job.Status)
	}

	pause(t, "probe-and-coding", "Probe stage is about to write a failing repro and checkpoint it.")

	// 03 -> 04 -> 05 -> 06
	if err := engine.ExecuteJob(ctx, job.ID); err != nil {
		t.Fatalf("probe/coding/review failed: %v", err)
	}
	job, _ = db.Jobs().GetJob(ctx, job.ID)
	if job.Stage != store.StageHumanApprovalGate || job.Status != store.StatusAwaitingApproval {
		t.Fatalf("expected 06/awaiting_approval, got %s/%s", job.Stage, job.Status)
	}

	// Evidence shows the probe result (PRB-5).
	ev, err := engine.GetEvidence(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetEvidence failed: %v", err)
	}
	if ev.Probe.Status != "pass" {
		t.Fatalf("expected the probe evidence to pass, got %q", ev.Probe.Status)
	}

	pause(t, "gate", "Job reached the human approval gate with evidence.")

	// Approve the gate -> delivery (no PR provider) -> done.
	if err := engine.Approve(ctx, job.ID, job.HeadSHA); err != nil {
		t.Fatalf("approve gate failed: %v", err)
	}
	job, _ = db.Jobs().GetJob(ctx, job.ID)
	if job.Stage != store.StageDone || job.Status != store.StatusDone {
		t.Fatalf("expected 07_Done/done, got %s/%s", job.Stage, job.Status)
	}
}
