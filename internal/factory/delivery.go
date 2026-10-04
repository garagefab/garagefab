// Package factory contains the core domain model, pipeline engine, and scheduler
// for the software factory.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ENTERPRISE/JAVA BRIDGE:
// Domain Core Delivery Orchestrator (Hexagonal Architecture / Stage 07_Done).
//
// In Clean Architecture:
// `delivery.go` orchestrates the final pipeline stage (`07_Done`):
//  1. Pushes the job's worktree Git branch to the remote repository (DLV-1, DLV-3)
//     via the driven port `WorktreeManager.Push`.
//  2. Discovers or creates a Pull Request via the driven port `PullRequestProvider` (DLV-1, DLV-2).
//  3. Persists the resulting PR URL and transitions status to `done` atomically
//     via `StoreTx.UpdateJobPR` and `StoreTx.UpdateJobState` (DLV-4, PIP-2).
//  4. Cleans up the ephemeral Git worktree while keeping the local branch intact (DLV-4, WKT-6).
//
// JAVA / SPRING BOOT COMPARISON:
//   - In a Spring Boot application, this corresponds to an `@Async` delivery service or
//     Spring Batch `Tasklet` that:
//   - Injects a `GitClient` and `GitHubFeignClient` (driven outbound ports).
//   - Operates inside a `@Transactional` boundary for state persistence.
//   - Invokes resource cleanup (`worktree.delete()`) in a `finally` block or upon
//     successful delivery.
//
// GO IDIOMS & CONCEPTS:
//  1. Consumer-Driven Ports:
//     Factory defines `PullRequestProvider` and `WorktreeManager.Push` without importing
//     external libraries or subprocess runners (Rule 1).
//  2. Structured Failure Classification:
//     Any delivery error (missing config, network failure, push rejection) is deterministically
//     classified as `FailureBlocked` (DLV-3, DLV-6) so that the automated repair loop is not
//     futilely invoked and the worktree is preserved for human inspection or manual retry.
//  3. String Assembly for Markdown:
//     Uses `strings.Builder` for zero-allocation formatting of the delivery PR body
//     containing intent, evidence summaries, artifact links, and closing keywords.
//
// ==============================================================================
package factory

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
)

// executeDeliveryStage executes the final delivery step in stage 07_Done (DLV-1..6, WKT-6).
//
// Rules enforced:
// 1. Transition job to 07_Done / running with an execution slot (DLV-1, PIP-2).
// 2. Project must have github.repo configured; otherwise fail as Blocked (DLV-6).
// 3. Remote is determined from base_ref: "origin/<branch>" -> "origin", or local -> "origin" (Decision P2).
// 4. Git branch is pushed to remote via wtMgr.Push; failures are Blocked (DLV-3).
// 5. PR search via prProvider.FindPullRequest (DLV-2):
//   - Open PR is reused.
//   - Closed or merged PR fails as Blocked (Decision P4, DLV-2).
//   - Missing PR triggers CreatePullRequest with evidence summary and closing keyword (DLV-1).
//
// 6. On success: DB records PR URL, status becomes done, worktree is deleted (DLV-4, WKT-6).
// 7. On failure: status becomes failed (Blocked), worktree is retained for retry (DLV-3, WKT-6).
func (e *Engine) executeDeliveryStage(ctx context.Context, job *Job, project *Project, projCfg *ProjectConfig) error {
	// Transition state to 07_Done / running
	err := e.store.InTx(ctx, func(tx StoreTx) error {
		if err := tx.UpdateJobState(ctx, job.ID, StageDone, StatusRunning); err != nil {
			return err
		}
		return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageDone, StatusRunning))
	})
	if err != nil {
		return fmt.Errorf("factory: transition to delivery running: %w", err)
	}
	job.Stage = StageDone
	job.Status = StatusRunning

	logPath := filepath.Join(e.logBaseDir, fmt.Sprintf("%d", job.ID), "step_delivery.log")
	step := &StepRun{
		JobID:     job.ID,
		Stage:     StageDone,
		Kind:      StepKindCommand,
		Attempt:   1,
		Executor:  "delivery",
		Status:    StepStatusRunning,
		LogPath:   logPath,
		StartedAt: time.Now().UTC(),
	}
	if err := e.store.CreateStepRun(ctx, step); err != nil {
		return fmt.Errorf("factory: create delivery step run: %w", err)
	}

	// 1. Validate GitHub Repository Configuration (DLV-6)
	repo := ""
	if projCfg != nil {
		repo = strings.TrimSpace(projCfg.GitHub.Repo)
	}
	if repo == "" {
		errMsg := "delivery failed: github.repo is not configured in .garagefab/project.yaml (DLV-6)"
		return e.failDelivery(ctx, job, step, errMsg)
	}

	// 2. Validate PR Provider availability (DLV-6)
	if e.prProvider == nil {
		errMsg := "delivery failed: pull request provider is not configured"
		return e.failDelivery(ctx, job, step, errMsg)
	}

	// 3. Determine Remote and Base Branch (DLV-1, Decision P2)
	remote := "origin"
	baseBranch := project.BaseRef
	if baseBranch == "" {
		baseBranch = "main"
	}
	if strings.Contains(baseBranch, "/") {
		parts := strings.SplitN(baseBranch, "/", 2)
		remote = parts[0]
		baseBranch = parts[1]
	}

	// 4. Push Branch to Remote (DLV-1, DLV-3)
	if err := e.wtMgr.Push(ctx, job.WorktreePath, remote, job.BranchName); err != nil {
		errMsg := fmt.Sprintf("delivery push failed: %v", err)
		return e.failDelivery(ctx, job, step, errMsg)
	}

	// 5. Inspect / Create Pull Request (DLV-1, DLV-2)
	existingPR, err := e.prProvider.FindPullRequest(ctx, repo, job.BranchName)
	if err != nil {
		errMsg := fmt.Sprintf("delivery query pull request failed: %v", err)
		return e.failDelivery(ctx, job, step, errMsg)
	}

	var prURL string
	if existingPR != nil {
		state := strings.ToUpper(strings.TrimSpace(existingPR.State))
		if state == "OPEN" {
			// Idempotent reuse of open PR (DLV-2)
			prURL = existingPR.URL
			slog.Info("delivery: reusing existing open pull request", "job_id", job.ID, "pr_url", prURL)
		} else {
			// Closed or Merged PR -> Fail as Blocked (Decision P4, DLV-2)
			errMsg := fmt.Sprintf("delivery failed: pull request #%d for branch %s is already %s (cannot open duplicate PR)", existingPR.Number, job.BranchName, state)
			return e.failDelivery(ctx, job, step, errMsg)
		}
	} else {
		// Assemble PR body and create PR (DLV-1)
		title := job.Title
		body := e.assemblePRBody(ctx, job, projCfg)
		createdPR, err := e.prProvider.CreatePullRequest(ctx, PullRequestRequest{
			Repo:  repo,
			Base:  baseBranch,
			Head:  job.BranchName,
			Title: title,
			Body:  body,
		})
		if err != nil {
			errMsg := fmt.Sprintf("delivery create pull request failed: %v", err)
			return e.failDelivery(ctx, job, step, errMsg)
		}
		prURL = createdPR.URL
		slog.Info("delivery: created pull request", "job_id", job.ID, "pr_url", prURL)
	}

	// 6. Record Delivery Success & Advance Job to Done (DLV-4, PIP-2)
	err = e.store.InTx(ctx, func(tx StoreTx) error {
		if err := tx.UpdateJobPR(ctx, job.ID, prURL); err != nil {
			return err
		}
		if err := tx.UpdateJobState(ctx, job.ID, StageDone, StatusDone); err != nil {
			return err
		}
		if err := tx.RecordEvent(ctx, job.ID, "job.delivered", fmt.Sprintf(`{"pr_url":%q}`, prURL)); err != nil {
			return err
		}
		return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageDone, StatusDone))
	})
	if err != nil {
		return fmt.Errorf("factory: record delivery success: %w", err)
	}

	job.PRURL = prURL
	job.Stage = StageDone
	job.Status = StatusDone

	// Update step run record
	step.Status = StepStatusSuccess
	code := 0
	step.ExitCode = &code
	now := time.Now().UTC()
	step.EndedAt = &now
	_ = e.store.UpdateStepRun(ctx, step)

	// 7. Clean up worktree on success, retaining local branch (DLV-4, WKT-6)
	if job.WorktreePath != "" {
		if err := e.wtMgr.Remove(ctx, project.RepoPath, job.WorktreePath, job.BranchName, false); err != nil {
			slog.Warn("delivery: remove worktree warning", "job_id", job.ID, "error", err)
		}
	}

	e.notifyWake()
	return nil
}

// failDelivery marks the step and job as failed with FailureBlocked, keeping the worktree (DLV-3, WKT-6).
func (e *Engine) failDelivery(ctx context.Context, job *Job, step *StepRun, errMsg string) error {
	now := time.Now().UTC()
	step.Status = StepStatusFail
	step.FailureCategory = FailureBlocked
	step.EndedAt = &now
	_ = e.store.UpdateStepRun(ctx, step)

	_ = e.store.InTx(ctx, func(tx StoreTx) error {
		if err := tx.UpdateJobState(ctx, job.ID, StageDone, StatusFailed); err != nil {
			return err
		}
		return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q,"failure_category":%q,"error":%q}`, StageDone, StatusFailed, FailureBlocked, errMsg))
	})
	job.Stage = StageDone
	job.Status = StatusFailed
	e.notifyWake()
	return fmt.Errorf("delivery: %s", errMsg)
}

// assemblePRBody constructs the markdown PR description containing intent, evidence summary,
// artifact references, and closing issue keywords (DLV-1).
func (e *Engine) assemblePRBody(ctx context.Context, job *Job, projCfg *ProjectConfig) string {
	var sb strings.Builder

	sb.WriteString("## Summary\n\n")
	if strings.TrimSpace(job.Intent) != "" {
		sb.WriteString(job.Intent)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString(job.Title)
		sb.WriteString("\n\n")
	}

	evidenceBytes, err := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, "evidence.md")
	if err == nil && len(evidenceBytes) > 0 {
		sb.WriteString("## Evidence Summary\n\n")
		sb.WriteString(string(evidenceBytes))
		sb.WriteString("\n\n")
	}

	sb.WriteString("## Artifacts\n\n")
	fmt.Fprintf(&sb, "- `.garagefab/jobs/%d/evidence.md`\n", job.ID)
	fmt.Fprintf(&sb, "- `.garagefab/jobs/%d/review.json`\n", job.ID)
	if _, err := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, "spec.md"); err == nil {
		fmt.Fprintf(&sb, "- `.garagefab/jobs/%d/spec.md`\n", job.ID)
	}
	sb.WriteString("\n")

	// Issue closing keyword for issue-sourced jobs (DLV-1)
	if issueNum := extractIssueNumber(job.SourceRef); issueNum != "" {
		kw := "Closes"
		if projCfg != nil && strings.EqualFold(projCfg.GitHub.PRIssueKeyword, "refs") {
			kw = "Refs"
		}
		fmt.Fprintf(&sb, "%s #%s\n", kw, issueNum)
	}

	return strings.TrimSpace(sb.String())
}

// extractIssueNumber extracts the numeric issue ID from a source reference (e.g. "owner/repo#42" -> "42").
func extractIssueNumber(sourceRef string) string {
	if idx := strings.LastIndex(sourceRef, "#"); idx != -1 {
		numStr := strings.TrimSpace(sourceRef[idx+1:])
		if numStr != "" {
			return numStr
		}
	}
	return ""
}
