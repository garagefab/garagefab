// Package factory_test verifies the stage 05 Independent Review, evidence generation,
// and gate approval/rejection behavior according to specification requirements (REV-1..6, APR-1..7, SPC-6..7).
//
// ==============================================================================
// ARCHITECTURAL ROLE & ENTERPRISE / JAVA COMPARISON:
// Domain Service Integration Test Suite (Hexagonal Architecture Core).
//
// Enterprise Comparison:
// In Spring Boot: Similar to testing an approval workflow service using Mockito mocks for
// Git clients and subprocess executors, verifying state machine transitions (@Transactional).
// In Go: We wire pure in-memory test doubles (MockStore, MockWorktreeManager, ScriptableAgentRunner)
// to verify deterministic state transitions without touching the real disk or spinning up OS processes.
//
// ==============================================================================
package factory_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/factory"
)

// TestReviewStage_Success_REV1_REV2_REV5_APR4 verifies the happy path for Stage 05:
// - Review agent receives isolated prompt (REV-1).
// - Valid review.json is parsed and checked (REV-2).
// - Checkpoint commits for review.json (REV-5) and evidence.md (APR-4) are created.
// - Job transitions to StageHumanApprovalGate / awaiting_approval.
func TestReviewStage_Success_REV1_REV2_REV5_APR4(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha", BaseRef: "main"}
	store.jobs[1] = &factory.Job{
		ID:           1,
		ProjectID:    1,
		WorkType:     factory.WorkTypeFeature,
		Title:        "Add OAuth",
		Intent:       "Implement OAuth login",
		Stage:        factory.StageCoding,
		Status:       factory.StatusRunning,
		WorktreePath: "/tmp/worktrees/alpha/1",
		BaseSHA:      "base123",
		HeadSHA:      "head123",
	}

	wtMgr := newMockWorktreeManager()
	_ = wtMgr.WriteArtifact(ctx, "/tmp/worktrees/alpha/1", 1, "spec.md", []byte("# Spec\n## Acceptance Criteria\nAC-1: Valid\n"))

	var reviewPromptReceived string
	agent := &ScriptableAgentRunner{
		results: func(call int, req factory.AgentRequest) (*factory.AgentResult, error) {
			if req.Stage == factory.StageIndependentReview {
				reviewPromptReceived = req.Prompt
				reviewJSON := `{
  "schema_version": 1,
  "decision": "approve",
  "summary": "LGTM",
  "risk": {
    "side_effect": {"score": 1, "rationale": "none"},
    "performance": {"score": 1, "rationale": "low"},
    "backward_compatibility": {"score": 1, "rationale": "compatible"}
  },
  "findings": [],
  "warnings": [{"file": "old.go", "description": "legacy notice"}],
  "spec_coverage": [{"criterion": "AC-1", "status": "met", "note": "verified"}]
}`
				_ = wtMgr.WriteArtifact(ctx, req.WorktreePath, req.JobID, "review.json", []byte(reviewJSON))
				return &factory.AgentResult{ExitCode: 0, Summary: "review done"}, nil
			}
			return &factory.AgentResult{ExitCode: 0, Summary: "ok"}, nil
		},
	}

	engine := factory.NewEngine(store, wtMgr, agent, &ScriptableCommandRunner{}, t.TempDir())

	err := engine.ExecuteJob(ctx, 1)
	if err != nil {
		t.Fatalf("ExecuteJob failed: %v", err)
	}

	// 1. Verify isolated prompt received intent, spec, and diff (REV-1)
	if !strings.Contains(reviewPromptReceived, "Implement OAuth login") {
		t.Errorf("review prompt missing intent: %s", reviewPromptReceived)
	}
	if !strings.Contains(reviewPromptReceived, "AC-1: Valid") {
		t.Errorf("review prompt missing spec: %s", reviewPromptReceived)
	}
	if !strings.Contains(reviewPromptReceived, "DO NOT modify any code") {
		t.Errorf("review prompt missing read-only instruction: %s", reviewPromptReceived)
	}

	// 2. Verify checkpoints created (REV-5, APR-4)
	foundReviewCheckpoint := false
	foundEvidenceCheckpoint := false
	for _, cp := range wtMgr.checkpointHistory {
		if strings.Contains(cp, "05_Independent_Review review.json") {
			foundReviewCheckpoint = true
		}
		if strings.Contains(cp, "06_Human_Approval_Gate evidence.md") {
			foundEvidenceCheckpoint = true
		}
	}
	if !foundReviewCheckpoint {
		t.Errorf("expected 05_Independent_Review review.json checkpoint")
	}
	if !foundEvidenceCheckpoint {
		t.Errorf("expected 06_Human_Approval_Gate evidence.md checkpoint")
	}

	// 3. Verify job state reached StageHumanApprovalGate / awaiting_approval
	job := store.jobs[1]
	if job.Stage != factory.StageHumanApprovalGate || job.Status != factory.StatusAwaitingApproval {
		t.Errorf("expected job at 06_Human_Approval_Gate/awaiting_approval, got %s/%s", job.Stage, job.Status)
	}

	// 4. Verify evidence.md artifact was created
	evidenceData, err := wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, "evidence.md")
	if err != nil {
		t.Fatalf("expected evidence.md artifact to exist: %v", err)
	}
	if !strings.Contains(string(evidenceData), "Evidence Summary for Job 1") {
		t.Errorf("evidence.md content missing title: %s", string(evidenceData))
	}
}

// TestReviewStage_TamperingDetected_Fails_REV4 verifies requirement REV-4:
// If the review agent modifies source code files, the step fails as Flawed and worktree is reset.
func TestReviewStage_TamperingDetected_Fails_REV4(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha", BaseRef: "main"}
	store.jobs[2] = &factory.Job{
		ID:           2,
		ProjectID:    1,
		WorkType:     factory.WorkTypeFeature,
		Title:        "Tamper Job",
		Intent:       "Check tampering",
		Stage:        factory.StageCoding,
		Status:       factory.StatusRunning,
		WorktreePath: "/tmp/worktrees/alpha/2",
		BaseSHA:      "base123",
		HeadSHA:      "head123",
	}

	wtMgr := newMockWorktreeManager()
	// Set tampered diff touching code file (tampered.go)
	wtMgr.tamperedDiff = "diff --git a/tampered.go b/tampered.go\n+package main"

	agent := &ScriptableAgentRunner{
		results: func(call int, req factory.AgentRequest) (*factory.AgentResult, error) {
			return &factory.AgentResult{ExitCode: 0, Summary: "tampered"}, nil
		},
	}

	engine := factory.NewEngine(store, wtMgr, agent, &ScriptableCommandRunner{}, t.TempDir())

	err := engine.ExecuteJob(ctx, 2)
	if err == nil {
		t.Fatalf("expected ExecuteJob to fail due to code modifications during review (REV-4)")
	}

	// Verify worktree reset was called
	if wtMgr.resetCount == 0 {
		t.Errorf("expected Reset to be invoked to revert unauthorized changes (REV-4)")
	}

	// Verify job state is failed
	job := store.jobs[2]
	if job.Stage != factory.StageIndependentReview || job.Status != factory.StatusFailed {
		t.Errorf("expected job failed at 05_Independent_Review, got %s/%s", job.Stage, job.Status)
	}
}

// TestReviewStage_InvalidReviewJSON_Fails_REV2_REV6 verifies requirements REV-2 and REV-6:
// If review agent produces invalid JSON or crashes, step fails as FailureFlawed (no repair loop).
func TestReviewStage_InvalidReviewJSON_Fails_REV2_REV6(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha", BaseRef: "main"}
	store.jobs[3] = &factory.Job{
		ID:           3,
		ProjectID:    1,
		WorkType:     factory.WorkTypeRefactor,
		Title:        "Bad Review Job",
		Intent:       "Test bad review",
		Stage:        factory.StageCoding,
		Status:       factory.StatusRunning,
		WorktreePath: "/tmp/worktrees/alpha/3",
		BaseSHA:      "base123",
		HeadSHA:      "head123",
	}

	wtMgr := newMockWorktreeManager()

	agent := &ScriptableAgentRunner{
		results: func(call int, req factory.AgentRequest) (*factory.AgentResult, error) {
			if req.Stage == factory.StageIndependentReview {
				// Write review with blocking finding while deciding "approve" (violates REV-2)
				badReview := `{
  "schema_version": 1,
  "decision": "approve",
  "summary": "Bad review",
  "risk": {
    "side_effect": {"score": 1, "rationale": "ok"},
    "performance": {"score": 1, "rationale": "ok"},
    "backward_compatibility": {"score": 1, "rationale": "ok"}
  },
  "findings": [{"severity": "blocking", "file": "main.go", "line": 10, "description": "critical bug"}]
}`
				_ = wtMgr.WriteArtifact(ctx, req.WorktreePath, req.JobID, "review.json", []byte(badReview))
				return &factory.AgentResult{ExitCode: 0, Summary: "review done"}, nil
			}
			return &factory.AgentResult{ExitCode: 0, Summary: "ok"}, nil
		},
	}

	engine := factory.NewEngine(store, wtMgr, agent, &ScriptableCommandRunner{}, t.TempDir())

	err := engine.ExecuteJob(ctx, 3)
	if err == nil {
		t.Fatalf("expected review stage to fail on blocking finding with approve decision (REV-2)")
	}

	// Verify step failed as Flawed
	job := store.jobs[3]
	if job.Status != factory.StatusFailed {
		t.Errorf("expected job status = failed, got %s", job.Status)
	}

	// Verify only 1 review agent invocation occurred (REV-6: no repair loop)
	reviewRuns := 0
	agent.mu.Lock()
	for _, inv := range agent.invocations {
		if inv.Stage == factory.StageIndependentReview {
			reviewRuns++
		}
	}
	agent.mu.Unlock()

	if reviewRuns != 1 {
		t.Errorf("expected exactly 1 review agent run (REV-6), got %d", reviewRuns)
	}
}

// TestApprove_SpecReview_SPC6_SPC7 verifies requirement SPC-6 & SPC-7:
// Approving at 02_Clarification_and_Spec / spec_review re-validates the spec, commits the checkpoint,
// records the spec SHA-256 hash in approvals, and advances to 04_Coding / queued.
func TestApprove_SpecReview_SPC6_SPC7(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha", BaseRef: "main"}
	store.jobs[4] = &factory.Job{
		ID:           4,
		ProjectID:    1,
		WorkType:     factory.WorkTypeFeature,
		Title:        "Feature Job",
		Intent:       "Add auth",
		Stage:        factory.StageClarificationAndSpec,
		Status:       factory.StatusSpecReview,
		WorktreePath: "/tmp/worktrees/alpha/4",
	}

	wtMgr := newMockWorktreeManager()
	validSpec := `# Feature Spec

## Summary
Authentication feature.

## Goals and Non-Goals
Goals: Auth.
Non-Goals: None.

## Design
Hexagonal architecture.

## Acceptance Criteria
Given user When credentials valid Then AC-1: login succeeds.

## Implementation Plan
1. Implement auth (AC-1)

## Test Plan
Unit tests.

## Risks and Assumptions
None.
`
	_ = wtMgr.WriteArtifact(ctx, "/tmp/worktrees/alpha/4", 4, "spec.md", []byte(validSpec))

	var wakeCalled bool
	engine := factory.NewEngine(store, wtMgr, &ScriptableAgentRunner{}, &ScriptableCommandRunner{}, t.TempDir())
	engine.SetWakeFunc(func() { wakeCalled = true })

	err := engine.Approve(ctx, 4, "")
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	// Verify job moved to 04_Coding / queued
	job := store.jobs[4]
	if job.Stage != factory.StageCoding || job.Status != factory.StatusQueued {
		t.Errorf("expected job at 04_Coding/queued, got %s/%s", job.Stage, job.Status)
	}

	// Verify approval record has spec_hash and gate spec_review
	if len(store.approvals) == 0 {
		t.Fatalf("expected approval record in store")
	}
	appr := store.approvals[0]
	if appr.Gate != factory.ApprovalGateSpecReview || appr.Decision != factory.ApprovalDecisionApprove {
		t.Errorf("approval mismatch: %+v", appr)
	}
	if !strings.HasPrefix(appr.Note, "spec_hash:") {
		t.Errorf("expected spec_hash in note, got %s", appr.Note)
	}

	// Verify checkpoint commit
	foundCheckpoint := false
	for _, msg := range wtMgr.checkpointHistory {
		if strings.Contains(msg, "approved spec.md") {
			foundCheckpoint = true
			break
		}
	}
	if !foundCheckpoint {
		t.Errorf("expected approved spec.md checkpoint commit")
	}

	if !wakeCalled {
		t.Errorf("expected scheduler wake notification")
	}
}

// TestApprove_SpecReview_InvalidSpec_ReturnsError_SPC6 verifies that if the developer edited
// the spec into an invalid state, Approve returns ErrSpecInvalid (SPC-6).
func TestApprove_SpecReview_InvalidSpec_ReturnsError_SPC6(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha", BaseRef: "main"}
	store.jobs[5] = &factory.Job{
		ID:           5,
		ProjectID:    1,
		WorkType:     factory.WorkTypeFeature,
		Title:        "Feature Job",
		Intent:       "Add auth",
		Stage:        factory.StageClarificationAndSpec,
		Status:       factory.StatusSpecReview,
		WorktreePath: "/tmp/worktrees/alpha/5",
	}

	wtMgr := newMockWorktreeManager()
	// Invalid spec missing Acceptance Criteria
	invalidSpec := "# Feature Spec\n## Summary\nIncomplete."
	_ = wtMgr.WriteArtifact(ctx, "/tmp/worktrees/alpha/5", 5, "spec.md", []byte(invalidSpec))

	engine := factory.NewEngine(store, wtMgr, &ScriptableAgentRunner{}, &ScriptableCommandRunner{}, t.TempDir())

	err := engine.Approve(ctx, 5, "")
	if !errors.Is(err, factory.ErrSpecInvalid) {
		t.Errorf("expected ErrSpecInvalid, got: %v", err)
	}
}

// TestReject_GateRejection_RoutingAndArtifact_APR6_COD5 verifies requirement APR-6 & COD-5:
// Rejecting at 06_Human_Approval_Gate requires a note, writes rejections/1.md, and routes to 04_Coding / queued.
func TestReject_GateRejection_RoutingAndArtifact_APR6_COD5(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha", BaseRef: "main"}
	store.jobs[6] = &factory.Job{
		ID:           6,
		ProjectID:    1,
		WorkType:     factory.WorkTypeFeature,
		Title:        "Feature Gate Job",
		Intent:       "Add auth",
		Stage:        factory.StageHumanApprovalGate,
		Status:       factory.StatusAwaitingApproval,
		WorktreePath: "/tmp/worktrees/alpha/6",
		HeadSHA:      "head123",
	}

	wtMgr := newMockWorktreeManager()
	var wakeCalled bool
	engine := factory.NewEngine(store, wtMgr, &ScriptableAgentRunner{}, &ScriptableCommandRunner{}, t.TempDir())
	engine.SetWakeFunc(func() { wakeCalled = true })

	// Empty note should fail (APR-6)
	err := engine.Reject(ctx, 6, "   ")
	if !errors.Is(err, factory.ErrEmptyRejectionNote) {
		t.Errorf("expected ErrEmptyRejectionNote, got: %v", err)
	}

	// Reject with valid note
	rejectionNote := "Please optimize database query in login handler"
	err = engine.Reject(ctx, 6, rejectionNote)
	if err != nil {
		t.Fatalf("Reject failed: %v", err)
	}

	// 1. Verify rejection artifact was written to rejections/1.md
	rejData, err := wtMgr.ReadArtifact(ctx, "/tmp/worktrees/alpha/6", 6, "rejections/1.md")
	if err != nil {
		t.Fatalf("expected rejections/1.md artifact to exist: %v", err)
	}
	if !strings.Contains(string(rejData), rejectionNote) {
		t.Errorf("rejection note artifact missing text: %s", string(rejData))
	}

	// 2. Verify job routed back to 04_Coding / queued (APR-6, COD-5)
	job := store.jobs[6]
	if job.Stage != factory.StageCoding || job.Status != factory.StatusQueued {
		t.Errorf("expected job at 04_Coding/queued, got %s/%s", job.Stage, job.Status)
	}

	// 3. Verify approval rejection recorded
	if len(store.approvals) == 0 {
		t.Fatalf("expected approval record in store")
	}
	appr := store.approvals[0]
	if appr.Gate != factory.ApprovalGateFinal || appr.Decision != factory.ApprovalDecisionReject || appr.Note != rejectionNote {
		t.Errorf("approval rejection mismatch: %+v", appr)
	}

	if !wakeCalled {
		t.Errorf("expected scheduler wake notification")
	}
}

// TestApprove_FinalGate_StaleEvidence_APR5 verifies requirement APR-5:
// If HEAD SHA differs from stored evidence HEAD SHA, Approve fails with ErrStaleEvidence.
func TestApprove_FinalGate_StaleEvidence_APR5(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha", BaseRef: "main"}
	store.jobs[7] = &factory.Job{
		ID:           7,
		ProjectID:    1,
		WorkType:     factory.WorkTypeFeature,
		Title:        "Feature Job",
		Intent:       "Add auth",
		Stage:        factory.StageHumanApprovalGate,
		Status:       factory.StatusAwaitingApproval,
		WorktreePath: "/tmp/worktrees/alpha/7",
		HeadSHA:      "evidence-sha-123",
	}

	wtMgr := newMockWorktreeManager()
	wtMgr.currentHead = "evidence-sha-123"

	engine := factory.NewEngine(store, wtMgr, &ScriptableAgentRunner{}, &ScriptableCommandRunner{}, t.TempDir())

	// Calling approve with outdated client-provided SHA
	err := engine.Approve(ctx, 7, "outdated-sha-000")
	if !errors.Is(err, factory.ErrStaleEvidence) {
		t.Errorf("expected ErrStaleEvidence on client SHA mismatch, got: %v", err)
	}

	// Worktree HEAD advanced past evidence SHA
	wtMgr.currentHead = "newer-commit-456"
	err = engine.Approve(ctx, 7, "evidence-sha-123")
	if !errors.Is(err, factory.ErrStaleEvidence) {
		t.Errorf("expected ErrStaleEvidence on worktree HEAD mismatch, got: %v", err)
	}
}
