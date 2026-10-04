// Package store implements repository data access for intake tracking and errors.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Intake Idempotency & Error Journal Repository (Hexagonal Driven Adapter).
//
// In Clean / Hexagonal Architecture:
// `intake_repo.go` manages database persistence for the intake subsystem:
//   - `intake_seen`: Idempotency registry preventing duplicate jobs from being
//     created for the same intent file or GitHub issue (INT-4).
//   - `intake_errors`: Error journal for reporting validation or communication
//     failures on the dashboard Overview page (INT-5, GHB-5).
//
// ENTERPRISE / JAVA SPRING COMPARISON:
//   - Spring Data JPA: In Java, idempotent ingestion is typically handled by
//     an `@Entity` with `@Table(uniqueConstraints = ...)` or an idempotent consumer
//     table. Error reporting corresponds to an active error registry / dead-letter
//     monitoring table.
//   - Spring `@Transactional`: In Go, atomic multi-table operations (creating
//     the job, recording the creation event, and marking intake seen) are executed
//     either within `db.WithTx(ctx, ...)` or directly via `db.CreateJobFromIntake`.
//
// GO IDIOMS & CONCEPTS:
//  1. Unified Executor (`dbtx`):
//     Methods execute against `dbtx`, allowing queries to run transparently on
//     either `*sql.DB` or `*sql.Tx`.
//  2. Native SQLite UPSERT:
//     Instead of an expensive SELECT-then-INSERT/UPDATE race condition,
//     `UpsertIntakeError` leverages SQLite 3.24+ `ON CONFLICT (...) DO UPDATE`
//     for atomic, lock-free error upserts.
//  3. Constraint Error Translation:
//     Catches raw SQLite constraint strings and translates them into domain
//     sentinel errors (`ErrIntakeAlreadySeen`) so callers never parse SQL error strings.
//
// ==============================================================================
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// IntakeRepo handles persistence for intake idempotency and error tracking.
type IntakeRepo struct {
	q dbtx
}

// CreateJobFromIntake inserts a new job, emits a 'job.created' event, and records
// the source reference in 'intake_seen' within the current query executor (INT-4).
// If the (project_id, source, ref) tuple already exists in intake_seen, the call fails
// with ErrIntakeAlreadySeen.
func (r *IntakeRepo) CreateJobFromIntake(ctx context.Context, j *Job, seen *IntakeSeen) error {
	now := time.Now().UTC()
	nowStr := formatTime(now)

	// Set initial defaults if omitted
	if j.Stage == "" {
		j.Stage = StageIntent
	}
	if j.Status == "" {
		j.Status = StatusQueued
	}
	if j.Source == "" {
		j.Source = SourceDashboard
	}

	// 1. Insert job record
	insertJobSQL := `
		INSERT INTO jobs (
			project_id, work_type, title, intent, source, source_ref,
			stage, status, branch_name, worktree_path, base_sha, head_sha,
			pr_url, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	jobRes, err := r.q.ExecContext(
		ctx, insertJobSQL,
		j.ProjectID, j.WorkType, j.Title, j.Intent, j.Source, j.SourceRef,
		j.Stage, j.Status, j.BranchName, j.WorktreePath, j.BaseSHA, j.HeadSHA,
		j.PRURL, nowStr, nowStr,
	)
	if err != nil {
		return fmt.Errorf("store: insert intake job: %w", err)
	}

	jobID, err := jobRes.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: get intake job last insert id: %w", err)
	}
	j.ID = jobID
	j.CreatedAt = now
	j.UpdatedAt = now

	// 2. Insert job.created audit event
	insertEventSQL := `INSERT INTO events (job_id, type, payload, created_at) VALUES (?, ?, ?, ?)`
	if _, err := r.q.ExecContext(ctx, insertEventSQL, j.ID, "job.created", "{}", nowStr); err != nil {
		return fmt.Errorf("store: insert intake job event: %w", err)
	}

	// 3. Insert intake_seen record (enforces INT-4 idempotency)
	seen.JobID = j.ID
	seen.CreatedAt = now
	insertSeenSQL := `
		INSERT INTO intake_seen (project_id, source, ref, content_hash, job_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	seenRes, err := r.q.ExecContext(ctx, insertSeenSQL, seen.ProjectID, seen.Source, seen.Ref, seen.ContentHash, seen.JobID, nowStr)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: intake_seen") {
			return ErrIntakeAlreadySeen
		}
		return fmt.Errorf("store: insert intake_seen: %w", err)
	}

	seenID, err := seenRes.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: get intake_seen last insert id: %w", err)
	}
	seen.ID = seenID

	return nil
}

// IsSeen checks whether an intake source reference has already produced a job (INT-4).
func (r *IntakeRepo) IsSeen(ctx context.Context, projectID int64, source, ref string) (bool, error) {
	query := `SELECT 1 FROM intake_seen WHERE project_id = ? AND source = ? AND ref = ? LIMIT 1`
	var exists int
	err := r.q.QueryRowContext(ctx, query, projectID, source, ref).Scan(&exists)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("store: query is_seen: %w", err)
	}
	return true, nil
}

// GetSeen retrieves an existing intake_seen record by unique composite key.
func (r *IntakeRepo) GetSeen(ctx context.Context, projectID int64, source, ref string) (*IntakeSeen, error) {
	query := `
		SELECT id, project_id, source, ref, content_hash, job_id, created_at
		FROM intake_seen
		WHERE project_id = ? AND source = ? AND ref = ?
	`
	var (
		seen       IntakeSeen
		createdStr string
	)
	err := r.q.QueryRowContext(ctx, query, projectID, source, ref).Scan(
		&seen.ID,
		&seen.ProjectID,
		&seen.Source,
		&seen.Ref,
		&seen.ContentHash,
		&seen.JobID,
		&createdStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get intake_seen: %w", err)
	}

	seen.CreatedAt = parseTime(createdStr)
	return &seen, nil
}

// UpsertIntakeError records or updates an active intake error for an item (INT-5, GHB-5).
func (r *IntakeRepo) UpsertIntakeError(ctx context.Context, e *IntakeError) error {
	now := time.Now().UTC()
	nowStr := formatTime(now)

	query := `
		INSERT INTO intake_errors (project_id, source, ref, message, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(project_id, source, ref) DO UPDATE SET
			message = excluded.message,
			updated_at = excluded.updated_at
	`
	_, err := r.q.ExecContext(ctx, query, e.ProjectID, e.Source, e.Ref, e.Message, nowStr)
	if err != nil {
		return fmt.Errorf("store: upsert intake error: %w", err)
	}
	e.UpdatedAt = now
	return nil
}

// ClearIntakeError removes an intake error when the issue or file is successfully resolved (INT-3, INT-5).
func (r *IntakeRepo) ClearIntakeError(ctx context.Context, projectID int64, source, ref string) error {
	query := `DELETE FROM intake_errors WHERE project_id = ? AND source = ? AND ref = ?`
	if _, err := r.q.ExecContext(ctx, query, projectID, source, ref); err != nil {
		return fmt.Errorf("store: clear intake error: %w", err)
	}
	return nil
}

// ListIntakeErrors retrieves intake errors, optionally filtered by project ID.
func (r *IntakeRepo) ListIntakeErrors(ctx context.Context, projectID *int64) ([]*IntakeError, error) {
	var (
		query string
		args  []any
	)

	if projectID != nil {
		query = `
			SELECT project_id, source, ref, message, updated_at
			FROM intake_errors
			WHERE project_id = ?
			ORDER BY updated_at DESC
		`
		args = append(args, *projectID)
	} else {
		query = `
			SELECT project_id, source, ref, message, updated_at
			FROM intake_errors
			ORDER BY updated_at DESC
		`
	}

	rows, err := r.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list intake errors: %w", err)
	}
	defer rows.Close()

	var errorsList []*IntakeError
	for rows.Next() {
		var (
			item       IntakeError
			updatedStr string
		)
		if err := rows.Scan(&item.ProjectID, &item.Source, &item.Ref, &item.Message, &updatedStr); err != nil {
			return nil, fmt.Errorf("store: scan intake error: %w", err)
		}
		item.UpdatedAt = parseTime(updatedStr)
		errorsList = append(errorsList, &item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: rows intake errors: %w", err)
	}

	return errorsList, nil
}
