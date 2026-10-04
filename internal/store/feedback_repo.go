// Package store implements repository data access for issue feedback synchronization.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Issue Feedback State Repository (Hexagonal Driven Adapter).
//
// In Clean / Hexagonal Architecture:
// `feedback_repo.go` tracks the last external GitHub issue feedback state applied
// by the intake reconciler (GHB-2, GHB-5).
//
// Because GitHub API calls are network operations, they must never block the factory
// engine or run inside the core SQLite transaction. Instead, the intake reconciler
// asynchronously inspects job states and compares them against `github_feedback.applied_state`.
//
// ENTERPRISE / JAVA SPRING COMPARISON:
//   - Outbox / Integration State Pattern: In Spring Boot enterprise applications,
//     integrations with external platforms (like GitHub or Jira) maintain a sync state
//     table to track what has been pushed to external APIs.
//   - Decoupled Reconciler: Equivalent to a Spring `@Scheduled` background worker
//     that queries dirty entities and publishes updates idempotently.
//
// GO IDIOMS & CONCEPTS:
//  1. Unified Executor (`dbtx`):
//     Methods execute against `dbtx`, allowing queries to run transparently on
//     either `*sql.DB` or `*sql.Tx`.
//  2. Native SQLite UPSERT:
//     `SetFeedbackState` leverages SQLite 3.24+ `ON CONFLICT (job_id) DO UPDATE`
//     to insert or overwrite the applied state atomically.
//
// ==============================================================================
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// FeedbackRepo handles persistence for GitHub issue feedback synchronization state.
type FeedbackRepo struct {
	q dbtx
}

// GetFeedbackState retrieves the last applied issue feedback state for a job (GHB-2).
// If no feedback has ever been applied for this job, it returns ("", nil) so callers
// can immediately detect that feedback is uninitialized without checking ErrNotFound.
func (r *FeedbackRepo) GetFeedbackState(ctx context.Context, jobID int64) (string, error) {
	query := `SELECT applied_state FROM github_feedback WHERE job_id = ?`
	var state string
	err := r.q.QueryRowContext(ctx, query, jobID).Scan(&state)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("store: get feedback state: %w", err)
	}
	return state, nil
}

// GetFeedback retrieves the complete feedback record for a job, returning ErrNotFound
// if no record exists.
func (r *FeedbackRepo) GetFeedback(ctx context.Context, jobID int64) (*GitHubFeedback, error) {
	query := `SELECT job_id, applied_state, updated_at FROM github_feedback WHERE job_id = ?`
	var (
		fb         GitHubFeedback
		updatedStr string
	)
	err := r.q.QueryRowContext(ctx, query, jobID).Scan(&fb.JobID, &fb.AppliedState, &updatedStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get feedback: %w", err)
	}
	fb.UpdatedAt = parseTime(updatedStr)
	return &fb, nil
}

// SetFeedbackState records or updates the applied feedback state for a job (GHB-2).
func (r *FeedbackRepo) SetFeedbackState(ctx context.Context, jobID int64, state string) error {
	now := time.Now().UTC()
	nowStr := formatTime(now)

	query := `
		INSERT INTO github_feedback (job_id, applied_state, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(job_id) DO UPDATE SET
			applied_state = excluded.applied_state,
			updated_at = excluded.updated_at
	`
	_, err := r.q.ExecContext(ctx, query, jobID, state, nowStr)
	if err != nil {
		return fmt.Errorf("store: set feedback state: %w", err)
	}
	return nil
}
