// Package store_test contains integration and unit tests for the intake repository.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PERSISTENCE VERIFICATION:
// Intake Idempotency & Error Journal Integration Tests (INT-4, INT-5, GHB-5).
//
// This test suite validates:
//  1. Atomic Ingestion: `CreateJobFromIntake` creates the job, records the audit event,
//     and writes the `intake_seen` idempotency marker in a single atomic transaction.
//  2. Idempotency Invariant (INT-4): Subsequent intake attempts with the same
//     (project_id, source, ref) tuple fail with `ErrIntakeAlreadySeen` and leave
//     the database completely untouched.
//  3. Error Journal Lifecycle: Upserting, listing, updating, and clearing errors
//     for files, issues, or providers.
//
// ==============================================================================
package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/garagefab/garagefab/internal/store"
)

// TestIntakeRepo_CreateJobFromIntake_Idempotency_INT4 tests requirement INT-4:
// Ingesting an issue or intent file creates a job and prevents duplicate jobs.
func TestIntakeRepo_CreateJobFromIntake_Idempotency_INT4(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "intake_test.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	// 1. Create a project
	p := &store.Project{
		Name:             "test-project",
		RepoPath:         "/tmp/test-repo",
		BaseRef:          "origin/main",
		EnabledWorkTypes: []string{store.WorkTypeBugFix, store.WorkTypeFeature},
	}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	// 2. Ingest first job from GitHub issue
	j := &store.Job{
		ProjectID: p.ID,
		WorkType:  store.WorkTypeFeature,
		Title:     "Add user authentication",
		Intent:    "Implement OAuth2 flow",
		Source:    store.SourceGitHubIssue,
		SourceRef: "owner/repo#42",
	}
	seen := &store.IntakeSeen{
		ProjectID:   p.ID,
		Source:      store.SourceGitHubIssue,
		Ref:         "owner/repo#42",
		ContentHash: "hash-42",
	}

	if err := db.CreateJobFromIntake(ctx, j, seen); err != nil {
		t.Fatalf("CreateJobFromIntake failed: %v", err)
	}

	if j.ID == 0 {
		t.Fatal("expected non-zero Job.ID after CreateJobFromIntake")
	}
	if seen.JobID != j.ID {
		t.Fatalf("expected seen.JobID to be %d, got %d", j.ID, seen.JobID)
	}

	// Verify job.created event was recorded
	events, err := db.Events().ListEventsByJob(ctx, j.ID)
	if err != nil {
		t.Fatalf("ListEventsByJob failed: %v", err)
	}
	if len(events) != 1 || events[0].Type != "job.created" {
		t.Fatalf("expected 1 'job.created' event, got %v", events)
	}

	// Verify IsSeen returns true
	isSeen, err := db.Intake().IsSeen(ctx, p.ID, store.SourceGitHubIssue, "owner/repo#42")
	if err != nil {
		t.Fatalf("IsSeen failed: %v", err)
	}
	if !isSeen {
		t.Fatal("expected IsSeen to return true")
	}

	// Verify GetSeen returns matching record
	fetchedSeen, err := db.Intake().GetSeen(ctx, p.ID, store.SourceGitHubIssue, "owner/repo#42")
	if err != nil {
		t.Fatalf("GetSeen failed: %v", err)
	}
	if fetchedSeen.JobID != j.ID || fetchedSeen.ContentHash != "hash-42" {
		t.Fatalf("unexpected fetchedSeen content: %+v", fetchedSeen)
	}

	// 3. Attempt duplicate ingestion with same (project_id, source, ref)
	dupJob := &store.Job{
		ProjectID: p.ID,
		WorkType:  store.WorkTypeFeature,
		Title:     "Duplicate user authentication",
		Intent:    "Implement OAuth2 flow again",
		Source:    store.SourceGitHubIssue,
		SourceRef: "owner/repo#42",
	}
	dupSeen := &store.IntakeSeen{
		ProjectID:   p.ID,
		Source:      store.SourceGitHubIssue,
		Ref:         "owner/repo#42",
		ContentHash: "hash-42-dup",
	}

	err = db.CreateJobFromIntake(ctx, dupJob, dupSeen)
	if err == nil {
		t.Fatal("expected error on duplicate CreateJobFromIntake, got nil")
	}
	if !errors.Is(err, store.ErrIntakeAlreadySeen) {
		t.Fatalf("expected ErrIntakeAlreadySeen, got: %v", err)
	}

	// Verify total jobs count remains 1 (duplicate was completely rolled back)
	jobs, err := db.Jobs().ListJobs(ctx, store.JobListFilter{ProjectID: &p.ID})
	if err != nil {
		t.Fatalf("ListJobs failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected exactly 1 job after rolled-back duplicate, got %d", len(jobs))
	}
}

// TestIntakeRepo_IntakeErrors_INT5 verifies intake error logging and clearance (INT-5, GHB-5).
func TestIntakeRepo_IntakeErrors_INT5(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "intake_errors_test.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	p := &store.Project{
		Name:     "err-project",
		RepoPath: "/tmp/err-repo",
	}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	// 1. Initially no errors
	errs, err := db.Intake().ListIntakeErrors(ctx, nil)
	if err != nil {
		t.Fatalf("ListIntakeErrors failed: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d", len(errs))
	}

	// 2. Upsert error for missing type label
	e := &store.IntakeError{
		ProjectID: p.ID,
		Source:    store.SourceGitHubIssue,
		Ref:       "owner/repo#100",
		Message:   "Issue missing required type:<type> label",
	}
	if err := db.Intake().UpsertIntakeError(ctx, e); err != nil {
		t.Fatalf("UpsertIntakeError failed: %v", err)
	}

	errs, err = db.Intake().ListIntakeErrors(ctx, &p.ID)
	if err != nil {
		t.Fatalf("ListIntakeErrors failed: %v", err)
	}
	if len(errs) != 1 || errs[0].Message != "Issue missing required type:<type> label" {
		t.Fatalf("unexpected intake errors: %+v", errs)
	}

	// 3. Upsert update (message change for same key)
	e.Message = "Issue label updated but still invalid"
	if err := db.Intake().UpsertIntakeError(ctx, e); err != nil {
		t.Fatalf("UpsertIntakeError update failed: %v", err)
	}

	errs, err = db.Intake().ListIntakeErrors(ctx, nil)
	if err != nil {
		t.Fatalf("ListIntakeErrors failed: %v", err)
	}
	if len(errs) != 1 || errs[0].Message != "Issue label updated but still invalid" {
		t.Fatalf("expected updated message, got %+v", errs)
	}

	// 4. Clear error
	if err := db.Intake().ClearIntakeError(ctx, p.ID, store.SourceGitHubIssue, "owner/repo#100"); err != nil {
		t.Fatalf("ClearIntakeError failed: %v", err)
	}

	errs, err = db.Intake().ListIntakeErrors(ctx, nil)
	if err != nil {
		t.Fatalf("ListIntakeErrors failed: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors after clear, got %d", len(errs))
	}
}
