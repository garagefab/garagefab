package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ApprovalRepo handles gate approval records (APR-5..7).
type ApprovalRepo struct {
	q dbtx
}

// CreateApproval inserts an approval record into the database.
func (r *ApprovalRepo) CreateApproval(ctx context.Context, a *Approval) error {
	now := time.Now().UTC()
	nowStr := formatTime(now)

	query := `
		INSERT INTO approvals (job_id, gate, decision, note, head_sha, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	res, err := r.q.ExecContext(ctx, query, a.JobID, a.Gate, a.Decision, a.Note, a.HeadSHA, nowStr)
	if err != nil {
		return fmt.Errorf("store: insert approval: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: get approval last insert id: %w", err)
	}

	a.ID = id
	a.CreatedAt = now
	return nil
}

// ListApprovalsByJob retrieves all approval records for a given job.
func (r *ApprovalRepo) ListApprovalsByJob(ctx context.Context, jobID int64) ([]*Approval, error) {
	query := `
		SELECT id, job_id, gate, decision, note, head_sha, created_at
		FROM approvals
		WHERE job_id = ?
		ORDER BY id ASC
	`
	rows, err := r.q.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("store: list approvals by job: %w", err)
	}
	defer rows.Close()

	var approvals []*Approval
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list approvals rows: %w", err)
	}

	return approvals, nil
}

// GetLatestApproval retrieves the latest decision for a job at a specific gate.
func (r *ApprovalRepo) GetLatestApproval(ctx context.Context, jobID int64, gate string) (*Approval, error) {
	query := `
		SELECT id, job_id, gate, decision, note, head_sha, created_at
		FROM approvals
		WHERE job_id = ? AND gate = ?
		ORDER BY id DESC
		LIMIT 1
	`
	row := r.q.QueryRowContext(ctx, query, jobID, gate)
	return scanApproval(row)
}

func scanApproval(row rowScanner) (*Approval, error) {
	var (
		a            Approval
		createdAtStr string
	)

	err := row.Scan(&a.ID, &a.JobID, &a.Gate, &a.Decision, &a.Note, &a.HeadSHA, &createdAtStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: scan approval: %w", err)
	}

	a.CreatedAt = parseTime(createdAtStr)
	return &a, nil
}
