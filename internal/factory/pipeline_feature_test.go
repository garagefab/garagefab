// Package factory_test verifies the pipeline orchestration for Feature profile jobs (INT-1, PIP-1, SPC-1..5).
//
// ==============================================================================
// ARCHITECTURAL ROLE & TEST SPECIFICATION:
// Feature Profile State Machine & Specification Invariant Verification.
//
// Tests in this file verify:
// 1. Stage 01_Intent transitions: intent.md written, checkpoint committed, job moves to 02_Clarification_and_Spec (INT-1, PIP-1).
// 2. Stage 02 Spec agent with questions: transitions to needs_clarification and yields (SPC-2).
// 3. Stage 02 Spec agent with valid spec: validated against §6.1, checkpoint committed, moves to spec_review (SPC-4, SPC-5).
// 4. Stage 02 Spec agent with both files or invalid spec: categorized as Flawed and retried (SPC-1, SPC-4).
//
// ==============================================================================
package factory_test

import (
	"context"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/factory"
)

// TestFeature_Intent_To_SpecStage_INT1_PIP1 verifies requirement INT-1 & PIP-1:
// A feature job at 01_Intent writes intent.md, commits checkpoint, and advances to 02_Clarification_and_Spec.
func TestFeature_Intent_To_SpecStage_INT1_PIP1(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha", BaseRef: "main"}

	intentText := "Implement Single Sign-On using OAuth2"
	store.jobs[1] = &factory.Job{
		ID:        1,
		ProjectID: 1,
		WorkType:  factory.WorkTypeFeature,
		Title:     "Add SSO",
		Intent:    intentText,
		Stage:     factory.StageIntent,
		Status:    factory.StatusQueued,
	}

	wtMgr := newMockWorktreeManager()

	// Scripted agent produces a valid spec.md on stage 02
	agent := &ScriptableAgentRunner{
		results: func(call int, req factory.AgentRequest) (*factory.AgentResult, error) {
			if req.Stage == factory.StageClarificationAndSpec {
				_ = wtMgr.WriteArtifact(ctx, req.WorktreePath, req.JobID, "spec.md", []byte(validFeatureSpec))
				return &factory.AgentResult{ExitCode: 0, Summary: "spec generated"}, nil
			}
			return &factory.AgentResult{ExitCode: 0, Summary: "ok"}, nil
		},
	}

	engine := factory.NewEngine(store, wtMgr, agent, &ScriptableCommandRunner{}, t.TempDir())

	err := engine.ExecuteJob(ctx, 1)
	if err != nil {
		t.Fatalf("ExecuteJob failed: %v", err)
	}

	// 1. Verify intent.md was written to worktree (INT-1, LOG-3)
	savedIntent, err := wtMgr.ReadArtifact(ctx, "/tmp/worktrees/alpha/1", 1, "intent.md")
	if err != nil {
		t.Fatalf("expected intent.md artifact, got error: %v", err)
	}
	if string(savedIntent) != intentText {
		t.Errorf("saved intent mismatch: got %q, want %q", string(savedIntent), intentText)
	}

	// 2. Verify 01_Intent checkpoint commit was created
	foundIntentCheckpoint := false
	for _, msg := range wtMgr.checkpointHistory {
		if strings.Contains(msg, "01_Intent") {
			foundIntentCheckpoint = true
			break
		}
	}
	if !foundIntentCheckpoint {
		t.Errorf("expected 01_Intent checkpoint to be recorded in worktree manager")
	}

	// 3. Verify job advanced to spec_review (SPC-5)
	job := store.jobs[1]
	if job.Stage != factory.StageClarificationAndSpec || job.Status != factory.StatusSpecReview {
		t.Errorf("expected job at 02_Clarification_and_Spec / spec_review, got %s / %s", job.Stage, job.Status)
	}
}

// TestFeature_SpecStage_ClarificationQuestions_SPC1_SPC2 verifies requirement SPC-1 & SPC-2:
// When an agent produces clarification-questions.md, the job transitions to needs_clarification and yields.
func TestFeature_SpecStage_ClarificationQuestions_SPC1_SPC2(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha", BaseRef: "main"}

	store.jobs[2] = &factory.Job{
		ID:           2,
		ProjectID:    1,
		WorkType:     factory.WorkTypeFeature,
		Title:        "Ambiguous Job",
		Intent:       "Make the app faster",
		Stage:        factory.StageClarificationAndSpec,
		Status:       factory.StatusQueued,
		WorktreePath: "/tmp/worktrees/alpha/2",
	}

	wtMgr := newMockWorktreeManager()

	agent := &ScriptableAgentRunner{
		results: func(call int, req factory.AgentRequest) (*factory.AgentResult, error) {
			questions := "Q1. Which endpoints are slow?\n\nQ2. What is the target latency threshold?\n"
			_ = wtMgr.WriteArtifact(ctx, req.WorktreePath, req.JobID, "clarification-questions.md", []byte(questions))
			return &factory.AgentResult{ExitCode: 0, Summary: "questions asked"}, nil
		},
	}

	engine := factory.NewEngine(store, wtMgr, agent, &ScriptableCommandRunner{}, t.TempDir())

	err := engine.ExecuteJob(ctx, 2)
	if err != nil {
		t.Fatalf("ExecuteJob failed: %v", err)
	}

	// Verify job is now at 02_Clarification_and_Spec / needs_clarification (SPC-2)
	job := store.jobs[2]
	if job.Stage != factory.StageClarificationAndSpec || job.Status != factory.StatusNeedsClarification {
		t.Errorf("expected job at 02_Clarification_and_Spec / needs_clarification, got %s / %s", job.Stage, job.Status)
	}
}

// TestFeature_SpecStage_BothFiles_Flawed_SPC1 verifies requirement SPC-1:
// If an agent produces both spec.md and clarification-questions.md, it is categorized as Flawed and retried.
func TestFeature_SpecStage_BothFiles_Flawed_SPC1(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha", BaseRef: "main"}

	store.jobs[3] = &factory.Job{
		ID:           3,
		ProjectID:    1,
		WorkType:     factory.WorkTypeFeature,
		Title:        "Both Files Job",
		Intent:       "Build feature",
		Stage:        factory.StageClarificationAndSpec,
		Status:       factory.StatusQueued,
		WorktreePath: "/tmp/worktrees/alpha/3",
	}

	wtMgr := newMockWorktreeManager()
	var agentInvocations int

	agent := &ScriptableAgentRunner{
		results: func(call int, req factory.AgentRequest) (*factory.AgentResult, error) {
			agentInvocations++
			if call == 0 {
				// Violation: output BOTH files
				_ = wtMgr.WriteArtifact(ctx, req.WorktreePath, req.JobID, "clarification-questions.md", []byte("Q1. What?"))
				_ = wtMgr.WriteArtifact(ctx, req.WorktreePath, req.JobID, "spec.md", []byte(validFeatureSpec))
				return &factory.AgentResult{ExitCode: 0}, nil
			}
			// Repair attempt: output ONLY valid spec.md
			_ = wtMgr.RemoveArtifact(ctx, req.WorktreePath, req.JobID, "clarification-questions.md")
			_ = wtMgr.WriteArtifact(ctx, req.WorktreePath, req.JobID, "spec.md", []byte(validFeatureSpec))
			return &factory.AgentResult{ExitCode: 0}, nil
		},
	}

	engine := factory.NewEngine(store, wtMgr, agent, &ScriptableCommandRunner{}, t.TempDir())
	engine.SetMaxRepairAttempts(3)

	err := engine.ExecuteJob(ctx, 3)
	if err != nil {
		t.Fatalf("ExecuteJob failed: %v", err)
	}

	// Verify repair loop executed: 1 flawed attempt + 1 repaired attempt = 2 runs
	if agentInvocations != 2 {
		t.Errorf("expected 2 agent runs (1 initial + 1 repair), got %d", agentInvocations)
	}

	job := store.jobs[3]
	if job.Status != factory.StatusSpecReview {
		t.Errorf("expected job at spec_review after repair, got %s", job.Status)
	}
}

// TestFeature_SpecStage_InvalidSpec_Flawed_SPC4 verifies requirement SPC-4:
// If an agent produces a spec missing required headings or AC-<n>, it is rejected as Flawed.
func TestFeature_SpecStage_InvalidSpec_Flawed_SPC4(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha", BaseRef: "main"}

	store.jobs[4] = &factory.Job{
		ID:           4,
		ProjectID:    1,
		WorkType:     factory.WorkTypeFeature,
		Title:        "Invalid Spec Job",
		Intent:       "Build feature",
		Stage:        factory.StageClarificationAndSpec,
		Status:       factory.StatusQueued,
		WorktreePath: "/tmp/worktrees/alpha/4",
	}

	wtMgr := newMockWorktreeManager()
	var agentInvocations int

	invalidSpecMissingAC := `# Feature Title
## Summary
Some summary
## Goals and Non-Goals
Goals
## Design
Design
## Implementation Plan
1. Do work for AC-1
## Test Plan
Test
## Risks and Assumptions
Risks
`

	agent := &ScriptableAgentRunner{
		results: func(call int, req factory.AgentRequest) (*factory.AgentResult, error) {
			agentInvocations++
			if call == 0 {
				_ = wtMgr.WriteArtifact(ctx, req.WorktreePath, req.JobID, "spec.md", []byte(invalidSpecMissingAC))
				return &factory.AgentResult{ExitCode: 0}, nil
			}
			_ = wtMgr.WriteArtifact(ctx, req.WorktreePath, req.JobID, "spec.md", []byte(validFeatureSpec))
			return &factory.AgentResult{ExitCode: 0}, nil
		},
	}

	engine := factory.NewEngine(store, wtMgr, agent, &ScriptableCommandRunner{}, t.TempDir())
	engine.SetMaxRepairAttempts(3)

	err := engine.ExecuteJob(ctx, 4)
	if err != nil {
		t.Fatalf("ExecuteJob failed: %v", err)
	}

	if agentInvocations != 2 {
		t.Errorf("expected 2 agent runs, got %d", agentInvocations)
	}

	job := store.jobs[4]
	if job.Status != factory.StatusSpecReview {
		t.Errorf("expected job at spec_review after repair, got %s", job.Status)
	}
}
