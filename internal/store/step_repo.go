package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// StepRunRepo handles database operations for step runs (LOG-1, LOG-2).
type StepRunRepo struct {
	q dbtx
}

// CreateStepRun inserts a new step run into the database.
func (r *StepRunRepo) CreateStepRun(ctx context.Context, s *StepRun) error {
	now := time.Now().UTC()
	nowStr := formatTime(now)

	if s.Status == "" {
		s.Status = StepStatusRunning
	}
	if s.Attempt == 0 {
		s.Attempt = 1
	}

	query := `
		INSERT INTO step_runs (
			job_id, stage, kind, attempt, executor, status,
			failure_category, exit_code, log_path, started_at, ended_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	var endedAtStr *string
	if s.EndedAt != nil {
		str := formatTime(*s.EndedAt)
		endedAtStr = &str
	}

	res, err := r.q.ExecContext(
		ctx, query,
		s.JobID, s.Stage, s.Kind, s.Attempt, s.Executor, s.Status,
		s.FailureCategory, s.ExitCode, s.LogPath, nowStr, endedAtStr,
	)
	if err != nil {
		return fmt.Errorf("store: insert step run: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: get step run last insert id: %w", err)
	}

	s.ID = id
	s.StartedAt = now
	return nil
}

// GetStepRun retrieves a step run by ID.
func (r *StepRunRepo) GetStepRun(ctx context.Context, id int64) (*StepRun, error) {
	query := `
		SELECT id, job_id, stage, kind, attempt, executor, status,
		       failure_category, exit_code, log_path, started_at, ended_at
		FROM step_runs
		WHERE id = ?
	`
	row := r.q.QueryRowContext(ctx, query, id)
	return scanStepRun(row)
}

// UpdateStepRun updates status, failure_category, exit_code, and ended_at of a step run.
func (r *StepRunRepo) UpdateStepRun(ctx context.Context, s *StepRun) error {
	query := `
		UPDATE step_runs
		SET status = ?, failure_category = ?, exit_code = ?, log_path = ?, ended_at = ?
		WHERE id = ?
	`
	var endedAtStr *string
	if s.EndedAt != nil {
		str := formatTime(*s.EndedAt)
		endedAtStr = &str
	}

	res, err := r.q.ExecContext(
		ctx, query,
		s.Status, s.FailureCategory, s.ExitCode, s.LogPath, endedAtStr, s.ID,
	)
	if err != nil {
		return fmt.Errorf("store: update step run: %w", err)
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

// ListStepRunsByJob retrieves all step runs for a job, ordered by attempt and start time.
func (r *StepRunRepo) ListStepRunsByJob(ctx context.Context, jobID int64) ([]*StepRun, error) {
	query := `
		SELECT id, job_id, stage, kind, attempt, executor, status,
		       failure_category, exit_code, log_path, started_at, ended_at
		FROM step_runs
		WHERE job_id = ?
		ORDER BY id ASC
	`
	rows, err := r.q.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("store: list step runs by job: %w", err)
	}
	defer rows.Close()

	var steps []*StepRun
	for rows.Next() {
		s, err := scanStepRun(rows)
		if err != nil {
			return nil, err
		}
		steps = append(steps, s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list step runs rows: %w", err)
	}

	return steps, nil
}

// GetLatestStepRun retrieves the most recent step run for a job.
func (r *StepRunRepo) GetLatestStepRun(ctx context.Context, jobID int64) (*StepRun, error) {
	query := `
		SELECT id, job_id, stage, kind, attempt, executor, status,
		       failure_category, exit_code, log_path, started_at, ended_at
		FROM step_runs
		WHERE job_id = ?
		ORDER BY id DESC
		LIMIT 1
	`
	row := r.q.QueryRowContext(ctx, query, jobID)
	return scanStepRun(row)
}

func scanStepRun(row rowScanner) (*StepRun, error) {
	var (
		s            StepRun
		startedAtStr string
		endedAtStr   sql.NullString
	)

	err := row.Scan(
		&s.ID,
		&s.JobID,
		&s.Stage,
		&s.Kind,
		&s.Attempt,
		&s.Executor,
		&s.Status,
		&s.FailureCategory,
		&s.ExitCode,
		&s.LogPath,
		&startedAtStr,
		&endedAtStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: scan step run: %w", err)
	}

	s.StartedAt = parseTime(startedAtStr)
	if endedAtStr.Valid && endedAtStr.String != "" {
		t := parseTime(endedAtStr.String)
		s.EndedAt = &t
	}

	return &s, nil
}
