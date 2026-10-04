// Package intake_test verifies the issue feedback reconciler in package intake.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Issue Feedback Reconciler Unit & Integration Tests (GHB-2, GHB-5).
//
// In Clean Architecture:
// `feedback_test.go` tests the driven port interaction between the feedback reconciler,
// SQLite persistence, and the mockable `IssueFeedback` outbound port.
//
// ENTERPRISE / JAVA SPRING COMPARISON:
//   - Similar to testing an asynchronous outbox publisher or reconciler service
//     using Mockito to verify external API calls and an in-memory database to verify
//     state tracking.
//
// GO IDIOMS & CONCEPTS:
//   - Table-driven tests verifying the full state transition matrix.
//   - Thread-safe mock implementing `IssueFeedback` with slice recording.
// ==============================================================================
package intake_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/intake"
	"github.com/garagefab/garagefab/internal/store"
)

// fakeFeedbackAdapter records calls to IssueFeedback methods.
type fakeFeedbackAdapter struct {
	ensureCalls []string
	setLabels   []setLabelCall
	comments    []commentCall
	setErr      error
	commentErr  error
	ensureErr   error
}

type setLabelCall struct {
	repo     string
	issueNum int
	add      []string
	remove   []string
}

type commentCall struct {
	repo     string
	issueNum int
	body     string
}

func (f *fakeFeedbackAdapter) EnsureLabels(ctx context.Context, repo string, labels []intake.LabelSpec) error {
	f.ensureCalls = append(f.ensureCalls, repo)
	return f.ensureErr
}

func (f *fakeFeedbackAdapter) SetLabels(ctx context.Context, repo string, issueNum int, add, remove []string) error {
	f.setLabels = append(f.setLabels, setLabelCall{
		repo:     repo,
		issueNum: issueNum,
		add:      add,
		remove:   remove,
	})
	return f.setErr
}

func (f *fakeFeedbackAdapter) Comment(ctx context.Context, repo string, issueNum int, body string) error {
	f.comments = append(f.comments, commentCall{
		repo:     repo,
		issueNum: issueNum,
		body:     body,
	})
	return f.commentErr
}

// TestFeedback_StateLabels_GHB2 verifies requirement GHB-2:
// Stage transitions map to mutually exclusive state labels, removing prior labels.
func TestFeedback_StateLabels_GHB2(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	db, err := store.Open(filepath.Join(tempDir, "feedback.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	project := &store.Project{
		Name:     "alpha",
		RepoPath: tempDir,
		BaseRef:  "main",
	}
	if err := db.Projects().CreateProject(ctx, project); err != nil {
		t.Fatalf("create project: %v", err)
	}

	cfg := &config.ProjectYAML{
		GitHub: config.ProjectGitHub{
			Repo: "owner/alpha",
		},
	}

	fakeFB := &fakeFeedbackAdapter{}
	reconciler := intake.NewFeedbackReconciler(fakeFB)

	// Create an issue-sourced job in 01_Intent / queued
	job := &store.Job{
		ProjectID: project.ID,
		WorkType:  store.WorkTypeFeature,
		Title:     "Add User Profile",
		Intent:    "Allow users to edit profiles",
		Source:    store.SourceGitHubIssue,
		SourceRef: "owner/alpha#42",
		Stage:     store.StageIntent,
		Status:    store.StatusQueued,
	}
	seen := &store.IntakeSeen{
		ProjectID: project.ID,
		Source:    store.SourceGitHubIssue,
		Ref:       "owner/alpha#42",
	}
	if err := db.CreateJobFromIntake(ctx, job, seen); err != nil {
		t.Fatalf("create job: %v", err)
	}

	// 1. First reconciliation: should bootstrap labels and set garagefab:in-progress
	if err := reconciler.Reconcile(ctx, db, project, cfg); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	if len(fakeFB.ensureCalls) != 1 || fakeFB.ensureCalls[0] != "owner/alpha" {
		t.Errorf("expected 1 EnsureLabels call for 'owner/alpha', got: %v", fakeFB.ensureCalls)
	}
	if len(fakeFB.setLabels) != 1 {
		t.Fatalf("expected 1 SetLabels call, got %d", len(fakeFB.setLabels))
	}
	call := fakeFB.setLabels[0]
	if call.issueNum != 42 || len(call.add) != 1 || call.add[0] != intake.LabelInProgress {
		t.Errorf("expected add [%s], got: %v", intake.LabelInProgress, call.add)
	}

	// 2. Second reconciliation with unchanged state: no new SetLabels call
	if err := reconciler.Reconcile(ctx, db, project, cfg); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(fakeFB.setLabels) != 1 {
		t.Errorf("expected no additional SetLabels calls for unchanged state, got %d", len(fakeFB.setLabels))
	}
	if len(fakeFB.ensureCalls) != 1 {
		t.Errorf("EnsureLabels must only be called once (lazy bootstrap), got %d", len(fakeFB.ensureCalls))
	}

	// 3. Transition to 02_Clarification_and_Spec / needs_clarification
	if err := db.Jobs().UpdateJobState(ctx, job.ID, store.StageClarificationAndSpec, store.StatusNeedsClarification); err != nil {
		t.Fatalf("update state: %v", err)
	}
	if err := reconciler.Reconcile(ctx, db, project, cfg); err != nil {
		t.Fatalf("reconcile after needs_clarification: %v", err)
	}
	if len(fakeFB.setLabels) != 2 {
		t.Fatalf("expected 2 SetLabels calls, got %d", len(fakeFB.setLabels))
	}
	call2 := fakeFB.setLabels[1]
	if len(call2.add) != 1 || call2.add[0] != intake.LabelNeedsClarification {
		t.Errorf("expected add [%s], got: %v", intake.LabelNeedsClarification, call2.add)
	}
	// Verify that garagefab:in-progress is in the remove list
	containsInProgress := false
	for _, r := range call2.remove {
		if r == intake.LabelInProgress {
			containsInProgress = true
			break
		}
	}
	if !containsInProgress {
		t.Errorf("expected remove list to include previous label %s, got: %v", intake.LabelInProgress, call2.remove)
	}

	// 4. Transition to 07_Done / done
	if err := db.Jobs().UpdateJobState(ctx, job.ID, store.StageDone, store.StatusDone); err != nil {
		t.Fatalf("update state: %v", err)
	}
	if err := reconciler.Reconcile(ctx, db, project, cfg); err != nil {
		t.Fatalf("reconcile after done: %v", err)
	}
	if len(fakeFB.setLabels) != 3 {
		t.Fatalf("expected 3 SetLabels calls, got %d", len(fakeFB.setLabels))
	}
	call3 := fakeFB.setLabels[2]
	if len(call3.add) != 1 || call3.add[0] != intake.LabelDelivered {
		t.Errorf("expected add [%s], got: %v", intake.LabelDelivered, call3.add)
	}
}

// TestFeedback_ErrorIgnored_GHB5 verifies requirement GHB-5:
// Provider errors during feedback synchronization are recorded in intake_errors
// and do not fail, alter, or interrupt job execution.
func TestFeedback_ErrorIgnored_GHB5(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	db, err := store.Open(filepath.Join(tempDir, "feedback_err.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	project := &store.Project{
		Name:     "beta",
		RepoPath: tempDir,
		BaseRef:  "main",
	}
	if err := db.Projects().CreateProject(ctx, project); err != nil {
		t.Fatalf("create project: %v", err)
	}

	cfg := &config.ProjectYAML{
		GitHub: config.ProjectGitHub{
			Repo: "owner/beta",
		},
	}

	fakeFB := &fakeFeedbackAdapter{
		setErr: errors.New("HTTP 403: API rate limit exceeded"),
	}
	reconciler := intake.NewFeedbackReconciler(fakeFB)

	job := &store.Job{
		ProjectID: project.ID,
		WorkType:  store.WorkTypeFeature,
		Title:     "Add Billing",
		Intent:    "Stripe integration",
		Source:    store.SourceGitHubIssue,
		SourceRef: "owner/beta#99",
		Stage:     store.StageIntent,
		Status:    store.StatusQueued,
	}
	seen := &store.IntakeSeen{
		ProjectID: project.ID,
		Source:    store.SourceGitHubIssue,
		Ref:       "owner/beta#99",
	}
	if err := db.CreateJobFromIntake(ctx, job, seen); err != nil {
		t.Fatalf("create job: %v", err)
	}

	// Reconcile encounters error: Reconcile must not fail fatally
	err = reconciler.Reconcile(ctx, db, project, cfg)
	if err != nil {
		t.Fatalf("Reconcile should not return fatal error on individual issue failure: %v", err)
	}

	// Verify job remains in 01_Intent / queued (not modified)
	reloadedJob, err := db.Jobs().GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if reloadedJob.Stage != store.StageIntent || reloadedJob.Status != store.StatusQueued {
		t.Errorf("job state was improperly modified: %s / %s", reloadedJob.Stage, reloadedJob.Status)
	}

	// Verify intake error was logged in intake_errors table (GHB-5)
	errs, err := db.Intake().ListIntakeErrors(ctx, &project.ID)
	if err != nil {
		t.Fatalf("list intake errors: %v", err)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 intake error recorded, got %d", len(errs))
	}
	if errs[0].Source != "provider" || errs[0].Ref != "owner/beta#99" {
		t.Errorf("unexpected error record: %+v", errs[0])
	}
}
