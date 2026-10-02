// Package store_test contains integration tests for all store repositories.
//
// ==============================================================================
// GO TESTING CONCEPTS:
//
//  1. `t.Cleanup()`:
//     Go 1.14 introduced `t.Cleanup(fn)`.
//     Instead of writing `defer db.Close()` in every test, a setup helper function
//     (`setupTestDB`) registers the cleanup callback directly with the test runner.
//     Equivalent to JUnit 5's `@AfterEach`.
//
//  2. Testing Database Foreign Keys & Cascades:
//     SQLite disables foreign key enforcement by default. These tests verify that
//     `PRAGMA foreign_keys=ON` is working as expected:
//     - Deleting a parent `Project` with associated jobs is RESTRICTED (fails).
//     - Deleting a `Job` CASCADE-deletes child `StepRuns`.
//
// ==============================================================================
package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/garagefab/garagefab/internal/store"
)

// setupTestDB creates an isolated SQLite database in a temporary directory and registers cleanup.
func setupTestDB(t *testing.T) *store.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	// Automatically close database when calling test completes
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

// TestProjectRepo_CRUD_And_UniqueConstraints_PRJ5 verifies requirement PRJ-5:
// Project CRUD operations and unique constraint enforcement for name and repo_path.
func TestProjectRepo_CRUD_And_UniqueConstraints_PRJ5(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	repo := db.Projects()

	p1 := &store.Project{
		Name:             "repo-alpha",
		RepoPath:         "/path/to/alpha",
		BaseRef:          "origin/main",
		EnabledWorkTypes: []string{store.WorkTypeBugFix, store.WorkTypeFeature},
	}

	if err := repo.CreateProject(ctx, p1); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	if p1.ID == 0 {
		t.Errorf("expected p1.ID > 0, got %d", p1.ID)
	}

	// Fetch by ID
	got, err := repo.GetProject(ctx, p1.ID)
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if got.Name != p1.Name || got.RepoPath != p1.RepoPath || len(got.EnabledWorkTypes) != 2 {
		t.Errorf("unexpected project fetched: %+v", got)
	}

	// Fetch by Name
	byName, err := repo.GetProjectByName(ctx, "repo-alpha")
	if err != nil {
		t.Fatalf("GetProjectByName failed: %v", err)
	}
	if byName.ID != p1.ID {
		t.Errorf("expected ID %d, got %d", p1.ID, byName.ID)
	}

	// Fetch by Path
	byPath, err := repo.GetProjectByRepoPath(ctx, "/path/to/alpha")
	if err != nil {
		t.Fatalf("GetProjectByRepoPath failed: %v", err)
	}
	if byPath.ID != p1.ID {
		t.Errorf("expected ID %d, got %d", p1.ID, byPath.ID)
	}

	// Test PRJ-5: Duplicate Name Rejected
	pDupName := &store.Project{
		Name:             "repo-alpha",
		RepoPath:         "/path/to/different",
		EnabledWorkTypes: []string{store.WorkTypeFeature},
	}
	err = repo.CreateProject(ctx, pDupName)
	if !errors.Is(err, store.ErrProjectNameExists) {
		t.Errorf("expected ErrProjectNameExists (PRJ-5), got %v", err)
	}

	// Test PRJ-5: Duplicate RepoPath Rejected
	pDupPath := &store.Project{
		Name:             "repo-beta",
		RepoPath:         "/path/to/alpha",
		EnabledWorkTypes: []string{store.WorkTypeFeature},
	}
	err = repo.CreateProject(ctx, pDupPath)
	if !errors.Is(err, store.ErrProjectRepoPathExists) {
		t.Errorf("expected ErrProjectRepoPathExists (PRJ-5), got %v", err)
	}

	// Test Archive (PRJ-8)
	if err := repo.ArchiveProject(ctx, p1.ID); err != nil {
		t.Fatalf("ArchiveProject failed: %v", err)
	}

	list, err := repo.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects failed: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 active projects after archive, got %d", len(list))
	}
}

// TestJobRepo_CRUD_And_FIFO_SCH3 tests requirements SCH-1, SCH-2, SCH-3:
// FIFO queue ordering and active/attention job counts.
func TestJobRepo_CRUD_And_FIFO_SCH3(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)

	p := &store.Project{
		Name:     "proj-jobs",
		RepoPath: "/path/to/proj-jobs",
	}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	jobRepo := db.Jobs()

	// Create three jobs in sequence
	j1 := &store.Job{
		ProjectID: p.ID,
		WorkType:  store.WorkTypeRefactor,
		Title:     "Refactor auth logic",
		Intent:    "Simplify middleware",
	}
	if err := jobRepo.CreateJob(ctx, j1); err != nil {
		t.Fatalf("CreateJob j1 failed: %v", err)
	}

	j2 := &store.Job{
		ProjectID: p.ID,
		WorkType:  store.WorkTypeFeature,
		Title:     "Add SSE stream",
		Intent:    "Implement event feed",
	}
	if err := jobRepo.CreateJob(ctx, j2); err != nil {
		t.Fatalf("CreateJob j2 failed: %v", err)
	}

	// Verify FIFO order (SCH-3): oldest queued job must be j1
	nextJob, err := jobRepo.GetNextQueuedJob(ctx)
	if err != nil {
		t.Fatalf("GetNextQueuedJob failed: %v", err)
	}
	if nextJob.ID != j1.ID {
		t.Errorf("SCH-3 violated: expected next job to be j1 (ID=%d), got ID=%d", j1.ID, nextJob.ID)
	}

	// Transition j1 to running
	if err := jobRepo.UpdateJobState(ctx, j1.ID, store.StageCoding, store.StatusRunning); err != nil {
		t.Fatalf("UpdateJobState failed: %v", err)
	}

	// Now oldest queued job should be j2
	nextJob2, err := jobRepo.GetNextQueuedJob(ctx)
	if err != nil {
		t.Fatalf("GetNextQueuedJob after j1 running failed: %v", err)
	}
	if nextJob2.ID != j2.ID {
		t.Errorf("expected next job to be j2 (ID=%d), got ID=%d", j2.ID, nextJob2.ID)
	}

	// Verify running counts (SCH-1, SCH-2)
	globalRunning, err := jobRepo.CountRunningJobs(ctx)
	if err != nil {
		t.Fatalf("CountRunningJobs failed: %v", err)
	}
	if globalRunning != 1 {
		t.Errorf("expected 1 global running job, got %d", globalRunning)
	}

	projectRunning, err := jobRepo.CountRunningJobsByProject(ctx, p.ID)
	if err != nil {
		t.Fatalf("CountRunningJobsByProject failed: %v", err)
	}
	if projectRunning != 1 {
		t.Errorf("expected 1 project running job, got %d", projectRunning)
	}

	// Verify status counts (UI-1)
	statusCounts, err := jobRepo.CountJobsByStatus(ctx)
	if err != nil {
		t.Fatalf("CountJobsByStatus failed: %v", err)
	}
	if statusCounts[store.StatusRunning] != 1 || statusCounts[store.StatusQueued] != 1 {
		t.Errorf("unexpected status counts: %+v", statusCounts)
	}

	// Mark j2 as needs_clarification and verify ListAttentionJobs (UI-1)
	if err := jobRepo.UpdateJobState(ctx, j2.ID, store.StageClarificationAndSpec, store.StatusNeedsClarification); err != nil {
		t.Fatalf("UpdateJobState to needs_clarification failed: %v", err)
	}

	attentionJobs, err := jobRepo.ListAttentionJobs(ctx)
	if err != nil {
		t.Fatalf("ListAttentionJobs failed: %v", err)
	}
	if len(attentionJobs) != 1 || attentionJobs[0].ID != j2.ID {
		t.Errorf("expected j2 in attention list, got: %+v", attentionJobs)
	}
}

// TestWithTx_AtomicStateAndEvent_PIP2 verifies requirement PIP-2:
// State updates and audit event writes are committed atomically; on error, both roll back.
func TestWithTx_AtomicStateAndEvent_PIP2(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)

	p := &store.Project{Name: "p-tx", RepoPath: "/path/to/p-tx"}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	j := &store.Job{
		ProjectID: p.ID,
		WorkType:  store.WorkTypeRefactor,
		Title:     "Tx Test Job",
	}
	if err := db.Jobs().CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	// 1. Transaction fails -> Rollback (Neither state change nor event persisted)
	simulatedErr := errors.New("simulated failure after state change")
	err := db.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.Jobs().UpdateJobState(ctx, j.ID, store.StageCoding, store.StatusRunning); err != nil {
			return err
		}
		if err := tx.Events().CreateEvent(ctx, &store.Event{
			JobID: j.ID,
			Type:  "job.status_changed",
		}); err != nil {
			return err
		}
		return simulatedErr
	})

	if !errors.Is(err, simulatedErr) {
		t.Fatalf("expected simulated error, got %v", err)
	}

	// Verify rollback (PIP-2): Job status must still be queued, and events count must be 0
	jAfterRollback, err := db.Jobs().GetJob(ctx, j.ID)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if jAfterRollback.Status != store.StatusQueued {
		t.Errorf("PIP-2 violated: state changed despite rollback: status=%s", jAfterRollback.Status)
	}

	eventsAfterRollback, err := db.Events().ListEventsByJob(ctx, j.ID)
	if err != nil {
		t.Fatalf("ListEventsByJob failed: %v", err)
	}
	if len(eventsAfterRollback) != 0 {
		t.Errorf("PIP-2 violated: events persisted despite rollback: count=%d", len(eventsAfterRollback))
	}

	// 2. Transaction succeeds -> Commit (Both state change and event persisted)
	err = db.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.Jobs().UpdateJobState(ctx, j.ID, store.StageCoding, store.StatusRunning); err != nil {
			return err
		}
		return tx.Events().CreateEvent(ctx, &store.Event{
			JobID:   j.ID,
			Type:    "job.status_changed",
			Payload: `{"status":"running"}`,
		})
	})
	if err != nil {
		t.Fatalf("WithTx commit failed: %v", err)
	}

	jAfterCommit, err := db.Jobs().GetJob(ctx, j.ID)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if jAfterCommit.Status != store.StatusRunning || jAfterCommit.Stage != store.StageCoding {
		t.Errorf("expected stage/status 04_Coding/running, got %s/%s", jAfterCommit.Stage, jAfterCommit.Status)
	}

	eventsAfterCommit, err := db.Events().ListEventsByJob(ctx, j.ID)
	if err != nil {
		t.Fatalf("ListEventsByJob failed: %v", err)
	}
	if len(eventsAfterCommit) != 1 {
		t.Errorf("expected 1 event after commit, got %d", len(eventsAfterCommit))
	}
}

// TestStepRun_And_ProcessRecord_RCV1 tests requirements LOG-1 and RCV-1:
// Creating step runs and recording active OS processes for crash recovery.
func TestStepRun_And_ProcessRecord_RCV1(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)

	p := &store.Project{Name: "p-proc", RepoPath: "/path/to/p-proc"}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	j := &store.Job{ProjectID: p.ID, WorkType: store.WorkTypeRefactor, Title: "Process Test"}
	if err := db.Jobs().CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	// Create StepRun
	step := &store.StepRun{
		JobID:    j.ID,
		Stage:    store.StageCoding,
		Kind:     store.StepKindAgent,
		Attempt:  1,
		Executor: "fakeagent",
		LogPath:  "/tmp/logs/1/step-1.log",
	}
	if err := db.StepRuns().CreateStepRun(ctx, step); err != nil {
		t.Fatalf("CreateStepRun failed: %v", err)
	}
	if step.ID == 0 {
		t.Fatal("expected step.ID > 0")
	}

	// Create ProcessRecord (RCV-1)
	proc := &store.ProcessRecord{
		StepRunID: step.ID,
		PID:       12345,
		PGID:      12345,
		StartTime: time.Now().Unix(),
		Active:    true,
	}
	if err := db.ProcessRecords().CreateProcessRecord(ctx, proc); err != nil {
		t.Fatalf("CreateProcessRecord failed: %v", err)
	}

	// List active process records (RCV-2)
	active, err := db.ProcessRecords().ListActiveProcessRecords(ctx)
	if err != nil {
		t.Fatalf("ListActiveProcessRecords failed: %v", err)
	}
	if len(active) != 1 || active[0].PID != 12345 {
		t.Errorf("unexpected active processes: %+v", active)
	}

	// Mark inactive
	if err := db.ProcessRecords().MarkProcessInactive(ctx, proc.ID); err != nil {
		t.Fatalf("MarkProcessInactive failed: %v", err)
	}

	activeAfter, err := db.ProcessRecords().ListActiveProcessRecords(ctx)
	if err != nil {
		t.Fatalf("ListActiveProcessRecords after inactive failed: %v", err)
	}
	if len(activeAfter) != 0 {
		t.Errorf("expected 0 active processes, got %d", len(activeAfter))
	}
}

// TestEventRepo_SinceAndRecent_LOG4 tests requirement LOG-4:
// Event retrieval by cursor and for dashboard recent feed.
func TestEventRepo_SinceAndRecent_LOG4(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)

	p := &store.Project{Name: "p-events", RepoPath: "/path/to/p-events"}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	j := &store.Job{ProjectID: p.ID, WorkType: store.WorkTypeRefactor, Title: "Events Test"}
	if err := db.Jobs().CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	eventRepo := db.Events()
	for i := 1; i <= 5; i++ {
		e := &store.Event{
			JobID:   j.ID,
			Type:    "step.progress",
			Payload: `{"tick":true}`,
		}
		if err := eventRepo.CreateEvent(ctx, e); err != nil {
			t.Fatalf("CreateEvent %d failed: %v", i, err)
		}
	}

	// Test ListEventsSince (LOG-4: Last-Event-ID resumption)
	since, err := eventRepo.ListEventsSince(ctx, 3, 10)
	if err != nil {
		t.Fatalf("ListEventsSince failed: %v", err)
	}
	if len(since) != 2 {
		t.Errorf("expected 2 events since ID 3, got %d", len(since))
	}
	if since[0].ID != 4 || since[1].ID != 5 {
		t.Errorf("unexpected event IDs: %d, %d", since[0].ID, since[1].ID)
	}

	// Test ListRecentEvents
	recent, err := eventRepo.ListRecentEvents(ctx, 3)
	if err != nil {
		t.Fatalf("ListRecentEvents failed: %v", err)
	}
	if len(recent) != 3 {
		t.Errorf("expected 3 recent events, got %d", len(recent))
	}
	if recent[0].ID != 5 {
		t.Errorf("expected most recent event ID to be 5, got %d", recent[0].ID)
	}
}

// TestApprovalRepo_APR5 tests requirement APR-5:
// Gate approval records and latest decision retrieval.
func TestApprovalRepo_APR5(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)

	p := &store.Project{Name: "p-appr", RepoPath: "/path/to/p-appr"}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	j := &store.Job{ProjectID: p.ID, WorkType: store.WorkTypeRefactor, Title: "Appr Test"}
	if err := db.Jobs().CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	apprRepo := db.Approvals()
	a := &store.Approval{
		JobID:    j.ID,
		Gate:     store.ApprovalGateFinal,
		Decision: store.ApprovalDecisionApprove,
		HeadSHA:  "abcdef123456",
	}
	if err := apprRepo.CreateApproval(ctx, a); err != nil {
		t.Fatalf("CreateApproval failed: %v", err)
	}

	latest, err := apprRepo.GetLatestApproval(ctx, j.ID, store.ApprovalGateFinal)
	if err != nil {
		t.Fatalf("GetLatestApproval failed: %v", err)
	}
	if latest.HeadSHA != "abcdef123456" || latest.Decision != store.ApprovalDecisionApprove {
		t.Errorf("unexpected approval record: %+v", latest)
	}
}

// TestSessionRepo_Invalidation_SEC3 tests requirement SEC-3:
// Dashboard session expiration and bulk invalidation on token rotation.
func TestSessionRepo_Invalidation_SEC3(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	sessRepo := db.Sessions()

	s1 := &store.Session{
		ID:        "sess-1",
		TokenHash: "hash-1",
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	s2 := &store.Session{
		ID:        "sess-2",
		TokenHash: "hash-1",
		ExpiresAt: time.Now().Add(-1 * time.Minute), // already expired
	}

	if err := sessRepo.CreateSession(ctx, s1); err != nil {
		t.Fatalf("CreateSession s1 failed: %v", err)
	}
	if err := sessRepo.CreateSession(ctx, s2); err != nil {
		t.Fatalf("CreateSession s2 failed: %v", err)
	}

	// Delete expired sessions
	if err := sessRepo.DeleteExpiredSessions(ctx); err != nil {
		t.Fatalf("DeleteExpiredSessions failed: %v", err)
	}

	_, err := sessRepo.GetSession(ctx, s2.ID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected s2 to be deleted by DeleteExpiredSessions, got err: %v", err)
	}

	// DeleteAllSessions on token rotation (SEC-3)
	if err := sessRepo.DeleteAllSessions(ctx); err != nil {
		t.Fatalf("DeleteAllSessions failed: %v", err)
	}
	_, err = sessRepo.GetSession(ctx, s1.ID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected s1 to be deleted by DeleteAllSessions, got err: %v", err)
	}
}

// TestForeignKeys_CascadeAndRestrict verifies that SQLite foreign key enforcement is active.
func TestForeignKeys_CascadeAndRestrict(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)

	p := &store.Project{Name: "p-fk", RepoPath: "/path/to/p-fk"}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	j := &store.Job{ProjectID: p.ID, WorkType: store.WorkTypeRefactor, Title: "FK Test"}
	if err := db.Jobs().CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	step := &store.StepRun{
		JobID:    j.ID,
		Stage:    store.StageCoding,
		Kind:     store.StepKindAgent,
		Executor: "fakeagent",
	}
	if err := db.StepRuns().CreateStepRun(ctx, step); err != nil {
		t.Fatalf("CreateStepRun failed: %v", err)
	}

	// Deleting a project that has active jobs must be RESTRICTED by foreign key constraint
	_, err := db.ExecContext(ctx, "DELETE FROM projects WHERE id = ?", p.ID)
	if err == nil {
		t.Fatal("expected foreign key RESTRICT error when deleting project with jobs, got nil")
	}

	// Deleting a job must CASCADE and delete its child step run
	_, err = db.ExecContext(ctx, "DELETE FROM jobs WHERE id = ?", j.ID)
	if err != nil {
		t.Fatalf("delete job failed: %v", err)
	}

	_, err = db.StepRuns().GetStepRun(ctx, step.ID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected step to be deleted by cascade, got %v", err)
	}
}
