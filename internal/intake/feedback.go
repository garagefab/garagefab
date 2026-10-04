// Package intake coordinates background ingestion and issue status feedback synchronization.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Issue Feedback Reconciler & Outbound Status Synchronizer (GHB-2, GHB-5).
//
// In Clean / Hexagonal Architecture:
// `feedback.go` is an asynchronous reconciler operating as part of the intake layer.
// It inspects active jobs in the database and ensures that external GitHub issues
// have labels and progress comments accurately reflecting their internal SDLC pipeline state.
//
// Operational Invariants:
//  1. Mutually Exclusive State Labels (GHB-2): Exactly one `garagefab:*` label is applied
//     at any time to represent the current SDLC state. Previous status labels are cleared.
//  2. Decoupled Fault Tolerance (GHB-5): Any GitHub communication or rate limit error
//     is recorded as a provider intake error on the Overview dashboard. It NEVER fails,
//     rolls back, or stalls internal software factory job execution.
//  3. Lazy Bootstrap: Ensures all required `garagefab:*` labels exist on the repository
//     exactly once per daemon run before applying state changes.
//
// ENTERPRISE / JAVA SPRING COMPARISON:
//   - Transactional Outbox / Asynchronous Reconciler: In enterprise architectures,
//     directly coupling external HTTP calls (GitHub REST API) into the core state machine
//     creates distributed transaction hazards and network fragility. Instead, state is
//     mutated locally in SQLite, and a background reconciler periodically ensures the
//     external representation converges to the desired state.
//   - Spring Batch / Scheduled Service: Corresponds to a scheduled `@Component` executing
//     `reconcile()` against a `GitHubClient` driven adapter.
//
// GO IDIOMS & CONCEPTS:
//   1. Concurrent Map (`sync.Map`):
//      Used for lock-free tracking of repositories whose labels have already been bootstrapped
//      during this daemon session.
//   2. Atomic Label Updates:
//      GitHub CLI `issue edit --add-label ... --remove-label ...` executes atomically
//      in a single command invocation, avoiding race conditions on GitHub's side.
// ==============================================================================
package intake

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/store"
)

// Label names for mutually exclusive SDLC state tracking (GHB-2).
const (
	LabelInProgress         = "garagefab:in-progress"
	LabelNeedsClarification = "garagefab:needs-clarification"
	LabelSpecReview         = "garagefab:spec-review"
	LabelNeedsApproval      = "garagefab:needs-approval"
	LabelBlocked            = "garagefab:blocked"
	LabelDelivered          = "garagefab:delivered"
)

// AllGaragefabLabels enumerates all managed status labels for removal during state transitions.
var AllGaragefabLabels = []string{
	LabelInProgress,
	LabelNeedsClarification,
	LabelSpecReview,
	LabelNeedsApproval,
	LabelBlocked,
	LabelDelivered,
}

// DefaultLabelSpecs defines the color codes and descriptions for bootstrapping repo labels.
var DefaultLabelSpecs = []LabelSpec{
	{Name: LabelInProgress, Color: "0E8A16", Description: "Work in progress in Garagefab"},
	{Name: LabelNeedsClarification, Color: "D93F0B", Description: "Awaiting developer answers to clarification questions"},
	{Name: LabelSpecReview, Color: "FBCA04", Description: "Awaiting developer sign-off on spec"},
	{Name: LabelNeedsApproval, Color: "1D76DB", Description: "Awaiting developer final sign-off at gate"},
	{Name: LabelBlocked, Color: "B60205", Description: "Job execution is blocked or failed"},
	{Name: LabelDelivered, Color: "0E8A16", Description: "Job completed and delivered via Pull Request"},
}

// FeedbackReconciler reconciles internal job states with GitHub issue labels and comments (GHB-2, GHB-5).
type FeedbackReconciler struct {
	feedback     IssueFeedback
	bootstrapped sync.Map // map[string]bool: repo -> true
}

// NewFeedbackReconciler constructs a new FeedbackReconciler with the injected IssueFeedback port.
func NewFeedbackReconciler(feedback IssueFeedback) *FeedbackReconciler {
	return &FeedbackReconciler{
		feedback: feedback,
	}
}

// Reconcile executes label synchronization and progress commenting for all issue-sourced jobs in a project.
func (r *FeedbackReconciler) Reconcile(ctx context.Context, db *store.DB, project *store.Project, cfg *config.ProjectYAML) error {
	if r.feedback == nil || db == nil || project == nil || cfg == nil {
		return nil
	}
	repo := strings.TrimSpace(cfg.GitHub.Repo)
	if repo == "" {
		return nil
	}

	// 1. Lazy Label Bootstrap: Ensure managed labels exist on GitHub once per repo per run (GHB-2)
	if _, loaded := r.bootstrapped.LoadOrStore(repo, true); !loaded {
		if err := r.feedback.EnsureLabels(ctx, repo, DefaultLabelSpecs); err != nil {
			r.bootstrapped.Delete(repo) // Retry next sweep if bootstrap fails
			_ = db.Intake().UpsertIntakeError(ctx, &store.IntakeError{
				ProjectID: project.ID,
				Source:    "provider",
				Ref:       repo,
				Message:   fmt.Sprintf("failed to ensure repo labels: %v", err),
			})
			slog.Warn("intake: ensure labels failed", "repo", repo, "error", err)
			return err
		}
	}

	// 2. Fetch jobs for this project
	jobs, err := db.Jobs().ListJobs(ctx, store.JobListFilter{
		ProjectID: &project.ID,
		Limit:     1000,
	})
	if err != nil {
		return fmt.Errorf("feedback: list jobs for project %d: %w", project.ID, err)
	}

	// 3. Process each issue-sourced job
	for _, job := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if job.Source != store.SourceGitHubIssue {
			continue // Skip intent-file and dashboard jobs
		}

		issueNum, err := parseIssueNumFromRef(job.SourceRef)
		if err != nil || issueNum <= 0 {
			continue
		}

		desiredLabel, commentText := MapJobToDesiredFeedback(job)

		appliedState, err := db.Feedback().GetFeedbackState(ctx, job.ID)
		if err != nil {
			slog.Warn("feedback: get applied state failed", "job_id", job.ID, "error", err)
			continue
		}

		// If external state already matches desired state, no sync needed
		if appliedState == desiredLabel {
			continue
		}

		// Calculate label additions and removals (GHB-2)
		var add []string
		if desiredLabel != "" {
			add = []string{desiredLabel}
		}
		var remove []string
		for _, l := range AllGaragefabLabels {
			if l != desiredLabel {
				remove = append(remove, l)
			}
		}

		// Atomically update labels on GitHub
		if err := r.feedback.SetLabels(ctx, repo, issueNum, add, remove); err != nil {
			// Record error on overview but do NOT interrupt job execution (GHB-5)
			_ = db.Intake().UpsertIntakeError(ctx, &store.IntakeError{
				ProjectID: project.ID,
				Source:    "provider",
				Ref:       fmt.Sprintf("%s#%d", repo, issueNum),
				Message:   fmt.Sprintf("failed to set issue labels: %v", err),
			})
			slog.Warn("feedback: set labels error", "repo", repo, "issue", issueNum, "error", err)
			continue
		}

		// Post progress comment if applicable
		if commentText != "" {
			if err := r.feedback.Comment(ctx, repo, issueNum, commentText); err != nil {
				slog.Warn("feedback: comment error", "repo", repo, "issue", issueNum, "error", err)
			}
		}

		// Persist successfully applied state in SQLite
		if err := db.Feedback().SetFeedbackState(ctx, job.ID, desiredLabel); err != nil {
			slog.Warn("feedback: save applied state error", "job_id", job.ID, "error", err)
		} else {
			// Clear any prior provider error for this issue
			_ = db.Intake().ClearIntakeError(ctx, project.ID, "provider", fmt.Sprintf("%s#%d", repo, issueNum))
		}
	}

	return nil
}

// MapJobToDesiredFeedback computes the target label and progress comment for a job (GHB-2).
func MapJobToDesiredFeedback(job *store.Job) (desiredLabel string, commentText string) {
	switch {
	case job.Status == store.StatusCancelled:
		return "", "🛑 Job was cancelled in Garagefab."

	case job.Status == store.StatusFailed || job.Status == store.StatusInterrupted:
		return LabelBlocked, fmt.Sprintf("⚠️ Job #%d execution is currently blocked or failed. Inspect details in the dashboard.", job.ID)

	case job.Stage == store.StageClarificationAndSpec && job.Status == store.StatusNeedsClarification:
		return LabelNeedsClarification, fmt.Sprintf("❓ Garagefab requires clarification to proceed on Job #%d. Please run:\n```sh\ngaragefab-work %d\n```", job.ID, job.ID)

	case job.Stage == store.StageClarificationAndSpec && job.Status == store.StatusSpecReview:
		return LabelSpecReview, fmt.Sprintf("📋 Draft specification for Job #%d is ready for review in the dashboard.", job.ID)

	case job.Stage == store.StageHumanApprovalGate && job.Status == store.StatusAwaitingApproval:
		return LabelNeedsApproval, fmt.Sprintf("🔍 Job #%d implementation is complete and awaiting final human approval in the dashboard.", job.ID)

	case job.Stage == store.StageDone && job.Status == store.StatusDone:
		body := fmt.Sprintf("✅ Job #%d delivered successfully!", job.ID)
		if job.PRURL != "" {
			body += fmt.Sprintf("\nPull Request: %s", job.PRURL)
		}
		return LabelDelivered, body

	case job.Status == store.StatusQueued || job.Status == store.StatusRunning:
		return LabelInProgress, fmt.Sprintf("🚀 Garagefab started work on this issue (Job #%d).", job.ID)

	default:
		return LabelInProgress, ""
	}
}

// parseIssueNumFromRef parses an issue number from a source reference string like "owner/repo#42" or "#42".
func parseIssueNumFromRef(ref string) (int, error) {
	idx := strings.LastIndex(ref, "#")
	if idx == -1 {
		return 0, fmt.Errorf("no # found in ref %q", ref)
	}
	numStr := strings.TrimSpace(ref[idx+1:])
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("invalid issue number %q in ref %q: %w", numStr, ref, err)
	}
	return num, nil
}
