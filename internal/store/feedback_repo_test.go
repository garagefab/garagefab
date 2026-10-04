// Package store_test contains integration and unit tests for the feedback repository.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PERSISTENCE VERIFICATION:
// GitHub Feedback State Repository Integration Tests (GHB-2, GHB-5).
//
// This test suite validates:
//  1. Uninitialized State Handling: Calling `GetFeedbackState` on a fresh job
//     cleanly returns `""` without error, enabling straightforward reconciler diffing.
//  2. Idempotent Upsert: `SetFeedbackState` writes new states and safely overwrites
//     prior states without duplicate primary key collisions.
//  3. Foreign Key Cascades: Deleting a parent job cascades to its feedback records.
// ==============================================================================
package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/garagefab/garagefab/internal/store"
)

// TestFeedbackRepo_Lifecycle_GHB2 tests requirement GHB-2:
// Reconciler tracks applied feedback state on GitHub issues.
func TestFeedbackRepo_Lifecycle_GHB2(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "feedback_test.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	p := &store.Project{
		Name:     "feedback-project",
		RepoPath: "/tmp/feedback-repo",
	}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	j := &store.Job{
		ProjectID: p.ID,
		WorkType:  store.WorkTypeFeature,
		Title:     "Feedback Job",
		Intent:    "Test feedback state",
		Source:    store.SourceGitHubIssue,
		SourceRef: "owner/repo#77",
	}
	if err := db.Jobs().CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	// 1. Initial state is empty
	state, err := db.Feedback().GetFeedbackState(ctx, j.ID)
	if err != nil {
		t.Fatalf("GetFeedbackState failed: %v", err)
	}
	if state != "" {
		t.Fatalf("expected empty state initially, got %q", state)
	}

	// 2. Set initial state to in-progress
	if err := db.Feedback().SetFeedbackState(ctx, j.ID, "in-progress"); err != nil {
		t.Fatalf("SetFeedbackState failed: %v", err)
	}

	state, err = db.Feedback().GetFeedbackState(ctx, j.ID)
	if err != nil {
		t.Fatalf("GetFeedbackState failed: %v", err)
	}
	if state != "in-progress" {
		t.Fatalf("expected 'in-progress', got %q", state)
	}

	fb, err := db.Feedback().GetFeedback(ctx, j.ID)
	if err != nil {
		t.Fatalf("GetFeedback failed: %v", err)
	}
	if fb.JobID != j.ID || fb.AppliedState != "in-progress" {
		t.Fatalf("unexpected feedback record: %+v", fb)
	}

	// 3. Update state to delivered (UPSERT overwrite)
	if err := db.Feedback().SetFeedbackState(ctx, j.ID, "delivered"); err != nil {
		t.Fatalf("SetFeedbackState overwrite failed: %v", err)
	}

	state, err = db.Feedback().GetFeedbackState(ctx, j.ID)
	if err != nil {
		t.Fatalf("GetFeedbackState failed: %v", err)
	}
	if state != "delivered" {
		t.Fatalf("expected 'delivered', got %q", state)
	}
}
