// Package intake_test provides unit and integration tests for the intake subsystem.
//
// ==============================================================================
// ARCHITECTURAL ROLE & TEST STRATEGY:
// Intake Pipeline Integration Tests (INT-2..7, GHB-3).
//
// All tests execute 100% offline with an in-memory or temporary SQLite database
// and mock implementations of `IssueSource` and `SchedulerNotifier`.
//
// Test Matrix:
//  1. INT-2: Scans *-intent.md files, parses YAML front matter, admits valid jobs.
//  2. INT-3: Polls GitHub candidate issues, checks required type:<type> labels.
//  3. Decision U5: Issues missing a valid type label record an intake error and do NOT create a job.
//  4. INT-4: Subsequent sweeps do not re-ingest already processed files or issues.
//  5. INT-5: API errors during issue fetching do not crash poller or prevent other projects.
//  6. INT-6: Non-overlapping sweep guarantee.
//  7. INT-7: SchedulerNotifier.Wake() is invoked when new jobs are created.
//  8. GHB-3: Projects without github.repo are cleanly skipped without issue polling.
//
// ==============================================================================
package intake_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/garagefab/garagefab/internal/intake"
	"github.com/garagefab/garagefab/internal/store"
)

// fakeIssueSource is a mock implementing intake.IssueSource.
type fakeIssueSource struct {
	mu     sync.Mutex
	issues map[string][]intake.IssueDTO // key: "repo:label"
	err    error
	calls  int
}

func newFakeIssueSource() *fakeIssueSource {
	return &fakeIssueSource{
		issues: make(map[string][]intake.IssueDTO),
	}
}

func (f *fakeIssueSource) ListTriggerIssues(ctx context.Context, repo, label string) ([]intake.IssueDTO, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	key := repo + ":" + label
	return f.issues[key], nil
}

// fakeNotifier is a mock implementing intake.SchedulerNotifier.
type fakeNotifier struct {
	wakeCount atomic.Int32
}

func (f *fakeNotifier) Wake() {
	f.wakeCount.Add(1)
}

// setupDB creates an isolated SQLite database for intake tests.
func setupDB(t *testing.T) *store.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "intake_test.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

// TestIntake_IntentFile_INT2 tests requirement INT-2, INT-4, INT-7:
// Scans intent files, validates YAML front matter, creates jobs, and wakes scheduler.
func TestIntake_IntentFile_INT2(t *testing.T) {
	ctx := context.Background()
	db := setupDB(t)

	repoDir := t.TempDir()
	intentsDir := filepath.Join(repoDir, ".garagefab", "intents")
	if err := os.MkdirAll(intentsDir, 0755); err != nil {
		t.Fatal(err)
	}

	p := &store.Project{
		Name:     "intent-project",
		RepoPath: repoDir,
	}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	// 1. Create a valid intent file
	validIntent := `---
type: feature
title: User Profile Management
---
Implement user profile page with avatar upload.
`
	validFile := filepath.Join(intentsDir, "user-profile-intent.md")
	if err := os.WriteFile(validFile, []byte(validIntent), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Create an invalid intent file (missing type)
	invalidIntent := `---
title: Broken Intent
---
Missing type front matter.
`
	invalidFile := filepath.Join(intentsDir, "broken-intent.md")
	if err := os.WriteFile(invalidFile, []byte(invalidIntent), 0644); err != nil {
		t.Fatal(err)
	}

	notifier := &fakeNotifier{}
	poller := intake.NewPoller(db, nil, notifier, 100*time.Millisecond)

	// Execute poll sweep
	poller.PollOnce(ctx)

	// Verify valid job was created
	jobs, err := db.Jobs().ListJobs(ctx, store.JobListFilter{ProjectID: &p.ID})
	if err != nil {
		t.Fatalf("ListJobs failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected exactly 1 job, got %d", len(jobs))
	}
	if jobs[0].WorkType != store.WorkTypeFeature || jobs[0].Title != "User Profile Management" {
		t.Errorf("unexpected job: %+v", jobs[0])
	}
	if jobs[0].Source != store.SourceIntentFile {
		t.Errorf("expected source %q, got %q", store.SourceIntentFile, jobs[0].Source)
	}

	// Verify scheduler was notified (INT-7)
	if notifier.wakeCount.Load() != 1 {
		t.Errorf("expected 1 wake call, got %d", notifier.wakeCount.Load())
	}

	// Verify invalid intent produced an intake error
	intakeErrs, err := db.Intake().ListIntakeErrors(ctx, &p.ID)
	if err != nil {
		t.Fatalf("ListIntakeErrors failed: %v", err)
	}
	if len(intakeErrs) != 1 {
		t.Fatalf("expected 1 intake error for broken intent, got %d", len(intakeErrs))
	}

	// 3. Re-poll does not create duplicate job (INT-4)
	poller.PollOnce(ctx)
	jobs, _ = db.Jobs().ListJobs(ctx, store.JobListFilter{ProjectID: &p.ID})
	if len(jobs) != 1 {
		t.Fatalf("expected still 1 job after second sweep (INT-4), got %d", len(jobs))
	}
}

// TestIntake_GitHubIssue_INT3 tests requirement INT-3, INT-4, INT-7:
// Ingests issues bearing the intake label and exactly one valid type label.
func TestIntake_GitHubIssue_INT3(t *testing.T) {
	ctx := context.Background()
	db := setupDB(t)

	repoDir := t.TempDir()
	gfDir := filepath.Join(repoDir, ".garagefab")
	if err := os.MkdirAll(gfDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gfDir, "project.yaml"), []byte("github:\n  repo: owner/auth-service\n"), 0644); err != nil {
		t.Fatal(err)
	}

	p := &store.Project{
		Name:     "auth-service",
		RepoPath: repoDir,
	}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	source := newFakeIssueSource()
	source.issues["owner/auth-service:garagefab"] = []intake.IssueDTO{
		{
			Number: 42,
			Title:  "Fix password reset token expiration",
			Body:   "Reset tokens currently never expire.",
			Labels: []string{"garagefab", "type:bug_fix", "priority:high"},
			URL:    "https://github.com/owner/auth-service/issues/42",
		},
	}

	notifier := &fakeNotifier{}
	poller := intake.NewPoller(db, source, notifier, 100*time.Millisecond)

	// First sweep -> creates job
	poller.PollOnce(ctx)

	jobs, err := db.Jobs().ListJobs(ctx, store.JobListFilter{ProjectID: &p.ID})
	if err != nil {
		t.Fatalf("ListJobs failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].WorkType != store.WorkTypeBugFix || jobs[0].SourceRef != "owner/auth-service#42" {
		t.Errorf("unexpected job: %+v", jobs[0])
	}
	if notifier.wakeCount.Load() != 1 {
		t.Errorf("expected 1 wake call, got %d", notifier.wakeCount.Load())
	}

	// Second sweep -> does not duplicate (INT-4)
	poller.PollOnce(ctx)
	jobs, _ = db.Jobs().ListJobs(ctx, store.JobListFilter{ProjectID: &p.ID})
	if len(jobs) != 1 {
		t.Fatalf("expected still 1 job after second sweep (INT-4), got %d", len(jobs))
	}
}

// TestIntake_MissingTypeLabel_INT3 tests Decision U5:
// Issues with missing, invalid, or multiple type labels record intake errors
// and do NOT create jobs until corrected.
func TestIntake_MissingTypeLabel_INT3(t *testing.T) {
	ctx := context.Background()
	db := setupDB(t)

	repoDir := t.TempDir()
	gfDir := filepath.Join(repoDir, ".garagefab")
	if err := os.MkdirAll(gfDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gfDir, "project.yaml"), []byte("github:\n  repo: owner/repo\n"), 0644); err != nil {
		t.Fatal(err)
	}

	p := &store.Project{
		Name:     "type-label-project",
		RepoPath: repoDir,
	}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	source := newFakeIssueSource()
	// Candidate issues:
	// Issue 1: missing type label
	// Issue 2: unknown type label (type:epic)
	// Issue 3: multiple type labels (type:feature AND type:bug_fix)
	source.issues["owner/repo:garagefab"] = []intake.IssueDTO{
		{
			Number: 1,
			Title:  "Issue with no type",
			Labels: []string{"garagefab"},
		},
		{
			Number: 2,
			Title:  "Issue with unknown type",
			Labels: []string{"garagefab", "type:epic"},
		},
		{
			Number: 3,
			Title:  "Issue with duplicate types",
			Labels: []string{"garagefab", "type:feature", "type:bug_fix"},
		},
	}

	notifier := &fakeNotifier{}
	poller := intake.NewPoller(db, source, notifier, 100*time.Millisecond)

	// Poll sweep
	poller.PollOnce(ctx)

	// Verify ZERO jobs created
	jobs, err := db.Jobs().ListJobs(ctx, store.JobListFilter{ProjectID: &p.ID})
	if err != nil {
		t.Fatalf("ListJobs failed: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs created for invalid type labels (Decision U5), got %d", len(jobs))
	}

	// Verify 3 intake errors recorded
	intakeErrs, err := db.Intake().ListIntakeErrors(ctx, &p.ID)
	if err != nil {
		t.Fatalf("ListIntakeErrors failed: %v", err)
	}
	if len(intakeErrs) != 3 {
		t.Fatalf("expected 3 intake errors, got %d", len(intakeErrs))
	}

	// Now fix Issue 1 by adding type:refactor on GitHub
	source.issues["owner/repo:garagefab"] = []intake.IssueDTO{
		{
			Number: 1,
			Title:  "Issue with no type (now fixed)",
			Labels: []string{"garagefab", "type:refactor"},
		},
	}

	// Next poll sweep -> successfully creates job for Issue 1 and clears its error
	poller.PollOnce(ctx)

	jobs, err = db.Jobs().ListJobs(ctx, store.JobListFilter{ProjectID: &p.ID})
	if err != nil {
		t.Fatalf("ListJobs failed: %v", err)
	}
	if len(jobs) != 1 || jobs[0].WorkType != store.WorkTypeRefactor {
		t.Fatalf("expected 1 refactor job after fixing label, got: %+v", jobs)
	}

	// Intake error for #1 is cleared, remaining 2 remain
	intakeErrs, err = db.Intake().ListIntakeErrors(ctx, &p.ID)
	if err != nil {
		t.Fatalf("ListIntakeErrors failed: %v", err)
	}
	if len(intakeErrs) != 2 {
		t.Fatalf("expected 2 remaining intake errors, got %d", len(intakeErrs))
	}
}

// TestIntake_ErrorResilience_INT5 verifies requirement INT-5:
// Provider errors do not crash poller or interrupt polling for other projects.
func TestIntake_ErrorResilience_INT5(t *testing.T) {
	ctx := context.Background()
	db := setupDB(t)

	// Project 1: Failing provider
	repo1 := t.TempDir()
	gf1 := filepath.Join(repo1, ".garagefab")
	_ = os.MkdirAll(gf1, 0755)
	_ = os.WriteFile(filepath.Join(gf1, "project.yaml"), []byte("github:\n  repo: owner/failing\n"), 0644)
	p1 := &store.Project{Name: "p1-failing", RepoPath: repo1}
	_ = db.Projects().CreateProject(ctx, p1)

	// Project 2: Healthy local intents project
	repo2 := t.TempDir()
	intents2 := filepath.Join(repo2, ".garagefab", "intents")
	_ = os.MkdirAll(intents2, 0755)
	_ = os.WriteFile(filepath.Join(intents2, "healthy-intent.md"), []byte("---\ntype: feature\ntitle: Healthy\n---\nBody\n"), 0644)
	p2 := &store.Project{Name: "p2-healthy", RepoPath: repo2}
	_ = db.Projects().CreateProject(ctx, p2)

	source := newFakeIssueSource()
	source.err = errors.New("simulated GitHub 503 Service Unavailable")

	poller := intake.NewPoller(db, source, nil, 100*time.Millisecond)

	// Sweep should NOT panic or fail
	poller.PollOnce(ctx)

	// p1 recorded a provider intake error
	errs1, _ := db.Intake().ListIntakeErrors(ctx, &p1.ID)
	if len(errs1) != 1 {
		t.Fatalf("expected 1 provider error for p1, got %d", len(errs1))
	}

	// p2 still successfully created its job!
	jobs2, _ := db.Jobs().ListJobs(ctx, store.JobListFilter{ProjectID: &p2.ID})
	if len(jobs2) != 1 {
		t.Fatalf("expected 1 job created for p2 despite p1 failure, got %d", len(jobs2))
	}
}

// TestIntake_NoRepo_GHB3 verifies requirement GHB-3:
// Projects without github.repo configured are skipped by issue scanner.
func TestIntake_NoRepo_GHB3(t *testing.T) {
	ctx := context.Background()
	db := setupDB(t)

	repo := t.TempDir()
	p := &store.Project{Name: "local-only", RepoPath: repo}
	_ = db.Projects().CreateProject(ctx, p)

	source := newFakeIssueSource()
	poller := intake.NewPoller(db, source, nil, 100*time.Millisecond)

	poller.PollOnce(ctx)

	if source.calls != 0 {
		t.Errorf("expected 0 calls to issue source for project without github.repo, got %d", source.calls)
	}
}
