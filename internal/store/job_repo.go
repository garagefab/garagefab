// Package store implements repository data access for pipeline jobs.
//
// ==============================================================================
// ARCHITECTURAL ROLE & REPOSITORY PATTERN:
// Job Data Access & Dynamic Query Construction (PIP-1..5, SCH-1..4, CLI-4).
//
// `JobRepo` manages SQL persistence for jobs.
//
// GO CONCEPTS & JAVA COMPARISONS:
//
//  1. Dynamic SQL Query Construction:
//     In Spring Data JPA: You would use `JpaSpecificationExecutor` and `CriteriaBuilder`.
//     In Go: Writing dynamic SQL is straightforward string manipulation:
//     var whereClauses []string
//     var args []any
//     if filter.ProjectID != nil { whereClauses = append(whereClauses, "project_id = ?"); args = append(...) }
//     query += " WHERE " + strings.Join(whereClauses, " AND ")
//     This is lightweight, explicit, and easy to inspect and debug.
//
//  2. FIFO Queue Ordering (SCH-3):
//     `GetNextQueuedJob` orders by auto-increment `id ASC LIMIT 1`. Since SQLite integer
//     primary keys monotonically increase, this guarantees strict First-In-First-Out admission.
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

// JobRepo handles database operations for pipeline jobs (PRJ-1..6, PIP-1..5, SCH-1..4).
type JobRepo struct {
	q dbtx
}

// CreateJob inserts a new job into the database.
func (r *JobRepo) CreateJob(ctx context.Context, j *Job) error {
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

	query := `
		INSERT INTO jobs (
			project_id, work_type, title, intent, source, source_ref,
			stage, status, branch_name, worktree_path, base_sha, head_sha,
			pr_url, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	res, err := r.q.ExecContext(
		ctx, query,
		j.ProjectID, j.WorkType, j.Title, j.Intent, j.Source, j.SourceRef,
		j.Stage, j.Status, j.BranchName, j.WorktreePath, j.BaseSHA, j.HeadSHA,
		j.PRURL, nowStr, nowStr,
	)
	if err != nil {
		return fmt.Errorf("store: insert job: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: get job last insert id: %w", err)
	}

	j.ID = id
	j.CreatedAt = now
	j.UpdatedAt = now
	return nil
}

// GetJob retrieves a job by its primary key ID.
func (r *JobRepo) GetJob(ctx context.Context, id int64) (*Job, error) {
	query := `
		SELECT id, project_id, work_type, title, intent, source, source_ref,
		       stage, status, branch_name, worktree_path, base_sha, head_sha,
		       pr_url, created_at, updated_at
		FROM jobs
		WHERE id = ?
	`
	row := r.q.QueryRowContext(ctx, query, id)
	return scanJob(row)
}

// UpdateJob updates general mutable fields of a job.
func (r *JobRepo) UpdateJob(ctx context.Context, j *Job) error {
	now := time.Now().UTC()
	nowStr := formatTime(now)

	query := `
		UPDATE jobs
		SET stage = ?, status = ?, branch_name = ?, worktree_path = ?,
		    base_sha = ?, head_sha = ?, pr_url = ?, updated_at = ?
		WHERE id = ?
	`

	res, err := r.q.ExecContext(
		ctx, query,
		j.Stage, j.Status, j.BranchName, j.WorktreePath,
		j.BaseSHA, j.HeadSHA, j.PRURL, nowStr, j.ID,
	)
	if err != nil {
		return fmt.Errorf("store: update job: %w", err)
	}

	rowsAff, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: get rows affected: %w", err)
	}
	if rowsAff == 0 {
		return ErrNotFound
	}

	j.UpdatedAt = now
	return nil
}

// UpdateJobState atomically updates a job's stage and status.
func (r *JobRepo) UpdateJobState(ctx context.Context, id int64, stage, status string) error {
	nowStr := formatTime(time.Now().UTC())
	query := `UPDATE jobs SET stage = ?, status = ?, updated_at = ? WHERE id = ?`
	res, err := r.q.ExecContext(ctx, query, stage, status, nowStr, id)
	if err != nil {
		return fmt.Errorf("store: update job state: %w", err)
	}
	rowsAff, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: get rows affected: %w", err)
	}
	if rowsAff == 0 {
		return ErrNotFound
	}
	return nil
}

// ListJobs queries jobs with optional filters and cursor pagination.
func (r *JobRepo) ListJobs(ctx context.Context, filter JobListFilter) ([]*Job, error) {
	var whereClauses []string
	var args []any

	// Conditionally append filter clauses
	if filter.ProjectID != nil {
		whereClauses = append(whereClauses, "project_id = ?")
		args = append(args, *filter.ProjectID)
	}
	if filter.Status != nil {
		whereClauses = append(whereClauses, "status = ?")
		args = append(args, *filter.Status)
	}
	if filter.Stage != nil {
		whereClauses = append(whereClauses, "stage = ?")
		args = append(args, *filter.Stage)
	}
	// Cursor-based pagination: fetch items with ID less than cursor
	if filter.Cursor > 0 {
		whereClauses = append(whereClauses, "id < ?")
		args = append(args, filter.Cursor)
	}

	query := `
		SELECT id, project_id, work_type, title, intent, source, source_ref,
		       stage, status, branch_name, worktree_path, base_sha, head_sha,
		       pr_url, created_at, updated_at
		FROM jobs
	`
	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}

	query += " ORDER BY id DESC"

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	query += " LIMIT ?"
	args = append(args, limit)

	rows, err := r.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list jobs rows: %w", err)
	}

	return jobs, nil
}

// GetNextQueuedJob retrieves the oldest queued job (SCH-3: FIFO ordering).
func (r *JobRepo) GetNextQueuedJob(ctx context.Context) (*Job, error) {
	query := `
		SELECT id, project_id, work_type, title, intent, source, source_ref,
		       stage, status, branch_name, worktree_path, base_sha, head_sha,
		       pr_url, created_at, updated_at
		FROM jobs
		WHERE status = ?
		ORDER BY id ASC
		LIMIT 1
	`
	row := r.q.QueryRowContext(ctx, query, StatusQueued)
	return scanJob(row)
}

// CountRunningJobs returns the total number of jobs currently in running status (SCH-1).
func (r *JobRepo) CountRunningJobs(ctx context.Context) (int, error) {
	query := `SELECT COUNT(*) FROM jobs WHERE status = ?`
	var count int
	err := r.q.QueryRowContext(ctx, query, StatusRunning).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count running jobs: %w", err)
	}
	return count, nil
}

// CountRunningJobsByProject returns the number of running jobs for a specific project (SCH-2).
func (r *JobRepo) CountRunningJobsByProject(ctx context.Context, projectID int64) (int, error) {
	query := `SELECT COUNT(*) FROM jobs WHERE project_id = ? AND status = ?`
	var count int
	err := r.q.QueryRowContext(ctx, query, projectID, StatusRunning).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count running jobs by project: %w", err)
	}
	return count, nil
}

// CountJobsByStatus returns a map of status -> count across all projects (UI-1 Overview).
func (r *JobRepo) CountJobsByStatus(ctx context.Context) (map[string]int, error) {
	query := `SELECT status, COUNT(*) FROM jobs GROUP BY status`
	rows, err := r.q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: count jobs by status: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("store: scan job count by status: %w", err)
		}
		counts[status] = count
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: count jobs rows: %w", err)
	}

	return counts, nil
}

// ListAttentionJobs returns jobs requiring developer attention (UI-1).
func (r *JobRepo) ListAttentionJobs(ctx context.Context) ([]*Job, error) {
	query := `
		SELECT id, project_id, work_type, title, intent, source, source_ref,
		       stage, status, branch_name, worktree_path, base_sha, head_sha,
		       pr_url, created_at, updated_at
		FROM jobs
		WHERE status IN (?, ?, ?, ?, ?)
		ORDER BY id DESC
	`
	rows, err := r.q.QueryContext(
		ctx, query,
		StatusNeedsClarification,
		StatusSpecReview,
		StatusAwaitingApproval,
		StatusFailed,
		StatusInterrupted,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list attention jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list attention jobs rows: %w", err)
	}

	return jobs, nil
}

// ListActiveStatusJobs returns active running jobs and attention-needing jobs with project names (CLI-4).
// It performs an SQL JOIN between jobs and projects.
func (r *JobRepo) ListActiveStatusJobs(ctx context.Context) ([]*JobStatusItem, error) {
	query := `
		SELECT j.id, COALESCE(p.name, ''), j.work_type, j.stage, j.status, j.title
		FROM jobs j
		LEFT JOIN projects p ON j.project_id = p.id
		WHERE j.status IN ('running', 'needs_clarification', 'spec_review', 'awaiting_approval', 'failed', 'interrupted')
		ORDER BY j.id ASC
	`
	rows, err := r.q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: list active status jobs: %w", err)
	}
	defer rows.Close()

	var items []*JobStatusItem
	for rows.Next() {
		var item JobStatusItem
		if err := rows.Scan(&item.ID, &item.ProjectName, &item.WorkType, &item.Stage, &item.Status, &item.Title); err != nil {
			return nil, fmt.Errorf("store: scan active status job: %w", err)
		}
		items = append(items, &item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list active status jobs rows: %w", err)
	}

	return items, nil
}

func scanJob(row rowScanner) (*Job, error) {
	var (
		j            Job
		createdAtStr string
		updatedAtStr string
	)

	err := row.Scan(
		&j.ID,
		&j.ProjectID,
		&j.WorkType,
		&j.Title,
		&j.Intent,
		&j.Source,
		&j.SourceRef,
		&j.Stage,
		&j.Status,
		&j.BranchName,
		&j.WorktreePath,
		&j.BaseSHA,
		&j.HeadSHA,
		&j.PRURL,
		&createdAtStr,
		&updatedAtStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: scan job: %w", err)
	}

	j.CreatedAt = parseTime(createdAtStr)
	j.UpdatedAt = parseTime(updatedAtStr)
	return &j, nil
}
