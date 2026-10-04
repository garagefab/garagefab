// Package intake provides GitHub issue scanning and automated job creation.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// GitHub Issue Scanner & Label-Gated Ingestion (INT-3, INT-4, INT-7, GHB-3).
//
// In Clean / Hexagonal Architecture:
// `issue_scanner.go` queries open GitHub issues via the `IssueSource` outbound port.
//
// Intake Trigger Rules (INT-3, Decision U5):
//  1. Trigger Label: Issues must bear the configured intake label (default `garagefab`).
//  2. Explicit Type Label Requirement: Every candidate issue must bear EXACTLY ONE
//     valid `type:<work_type>` label (`type:bug_fix`, `type:feature`, `type:refactor`, `type:docs`).
//  3. No Defaulting / Fail-Safe: If an issue is missing a type label, has an unrecognized
//     type label, or has multiple type labels, it MUST NOT create a job or be marked as seen.
//     Instead, an intake error is recorded on the dashboard Overview page. Once the human
//     adds the proper label on GitHub, the next poll cycle ingests the issue cleanly.
//  4. Atomic Ingestion (INT-4): Job record, creation event, and idempotency seen
//     marker are written within a single database transaction (`db.CreateJobFromIntake`).
//  5. Error Resilience (INT-5): Provider network or authentication errors record
//     a provider intake error and continue to the next project without crashing.
// ==============================================================================
package intake

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/store"
)

// extractValidWorkType inspects issue labels and returns the single valid work type,
// or an error if missing, ambiguous, or invalid (INT-3, Decision U5).
func extractValidWorkType(labels []string) (string, error) {
	var foundTypes []string
	for _, l := range labels {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "type:") {
			candidate := strings.TrimPrefix(trimmed, "type:")
			if isValidWorkType(candidate) {
				foundTypes = append(foundTypes, candidate)
			} else {
				return "", fmt.Errorf("unknown type label %q (allowed: type:bug_fix, type:feature, type:refactor, type:docs)", trimmed)
			}
		}
	}

	if len(foundTypes) == 0 {
		return "", fmt.Errorf("missing required type:<work_type> label (e.g. type:bug_fix, type:feature, type:refactor, type:docs)")
	}
	if len(foundTypes) > 1 {
		return "", fmt.Errorf("multiple type labels found: %v (must have exactly one)", foundTypes)
	}

	return foundTypes[0], nil
}

// ScanProjectIssues polls GitHub issues for a registered project and creates jobs (INT-3).
func ScanProjectIssues(
	ctx context.Context,
	db *store.DB,
	project *store.Project,
	source IssueSource,
	cfg *config.ProjectYAML,
	notifier SchedulerNotifier,
) error {
	// Skip projects without github.repo configured (GHB-3)
	if cfg == nil || cfg.GitHub.Repo == "" {
		return nil
	}

	intakeLabel := cfg.GitHub.IntakeLabel
	if intakeLabel == "" {
		intakeLabel = "garagefab"
	}

	issues, err := source.ListTriggerIssues(ctx, cfg.GitHub.Repo, intakeLabel)
	if err != nil {
		errMsg := fmt.Sprintf("failed to query issues for %s: %v", cfg.GitHub.Repo, err)
		slog.Warn("intake: issue fetch failed", "project", project.Name, "repo", cfg.GitHub.Repo, "error", err)
		_ = db.Intake().UpsertIntakeError(ctx, &store.IntakeError{
			ProjectID: project.ID,
			Source:    "provider",
			Ref:       project.Name,
			Message:   errMsg,
		})
		return fmt.Errorf("intake: list issues: %w", err)
	}

	// Clear prior provider error on successful API call
	_ = db.Intake().ClearIntakeError(ctx, project.ID, "provider", project.Name)

	for _, issue := range issues {
		issueRef := fmt.Sprintf("%s#%d", cfg.GitHub.Repo, issue.Number)

		// Check if already seen (INT-4 idempotency)
		seenAlready, err := db.Intake().IsSeen(ctx, project.ID, store.SourceGitHubIssue, issueRef)
		if err != nil {
			slog.Warn("intake: check is_seen failed", "project", project.Name, "ref", issueRef, "error", err)
			continue
		}
		if seenAlready {
			continue
		}

		// Enforce single valid type label requirement (Decision U5)
		workType, err := extractValidWorkType(issue.Labels)
		if err != nil {
			errMsg := fmt.Sprintf("Issue #%d: %v", issue.Number, err)
			_ = db.Intake().UpsertIntakeError(ctx, &store.IntakeError{
				ProjectID: project.ID,
				Source:    store.SourceGitHubIssue,
				Ref:       issueRef,
				Message:   errMsg,
			})
			continue
		}

		// Valid issue with recognized type -> create job atomically
		job := &store.Job{
			ProjectID: project.ID,
			WorkType:  workType,
			Title:     issue.Title,
			Intent:    issue.Body,
			Source:    store.SourceGitHubIssue,
			SourceRef: issueRef,
		}
		seen := &store.IntakeSeen{
			ProjectID: project.ID,
			Source:    store.SourceGitHubIssue,
			Ref:       issueRef,
		}

		if err := db.CreateJobFromIntake(ctx, job, seen); err != nil {
			slog.Error("intake: create job from issue failed", "project", project.Name, "ref", issueRef, "error", err)
			continue
		}

		// Clear prior intake error for this issue
		_ = db.Intake().ClearIntakeError(ctx, project.ID, store.SourceGitHubIssue, issueRef)

		slog.Info("intake: created job from GitHub issue", "job_id", job.ID, "project", project.Name, "ref", issueRef, "work_type", job.WorkType, "title", job.Title)

		// Wake scheduler immediately (INT-7)
		if notifier != nil {
			notifier.Wake()
		}
	}

	return nil
}
