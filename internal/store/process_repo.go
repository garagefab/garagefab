// Package store implements repository data access for operating system process records.
//
// ==============================================================================
// ARCHITECTURAL ROLE & CRASH RECOVERY:
// Process Tracking & Orphan Termination (RCV-1, RCV-2).
//
// When Garagefab executes an external agent or shell command subprocess:
//  1. Immediately after OS process fork (`cmd.Start()`), the process PID, PGID, and
//     start timestamp are saved to `process_records` with `active = 1`.
//  2. If Garagefab terminates normally, it marks the record `active = 0`.
//  3. If Garagefab crashes, loses power, or is killed via SIGKILL:
//     Upon the next startup, the recovery system calls `ListActiveProcessRecords()`
//     and sends `SIGTERM/SIGKILL` to the entire process group (`-pgid`), ensuring no
//     orphaned compiler, test runner, or LLM processes linger in the background.
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

// ProcessRecordRepo handles process records for process group tracking and crash recovery (RCV-1, RCV-2).
type ProcessRecordRepo struct {
	q dbtx
}

// CreateProcessRecord stores a newly spawned agent or command process.
func (r *ProcessRecordRepo) CreateProcessRecord(ctx context.Context, rec *ProcessRecord) error {
	now := time.Now().UTC()
	nowStr := formatTime(now)

	activeInt := 0
	if rec.Active {
		activeInt = 1
	}

	query := `
		INSERT INTO process_records (step_run_id, pid, pgid, start_time, active, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	res, err := r.q.ExecContext(ctx, query, rec.StepRunID, rec.PID, rec.PGID, rec.StartTime, activeInt, nowStr)
	if err != nil {
		return fmt.Errorf("store: insert process record: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: get process record last insert id: %w", err)
	}

	rec.ID = id
	rec.CreatedAt = now
	return nil
}

// GetProcessRecord retrieves a process record by primary key ID.
func (r *ProcessRecordRepo) GetProcessRecord(ctx context.Context, id int64) (*ProcessRecord, error) {
	query := `SELECT id, step_run_id, pid, pgid, start_time, active, created_at FROM process_records WHERE id = ?`
	row := r.q.QueryRowContext(ctx, query, id)
	return scanProcessRecord(row)
}

// ListActiveProcessRecords returns all process records currently marked as active (RCV-2).
// Used on daemon startup to detect and kill processes orphaned by an unexpected crash.
func (r *ProcessRecordRepo) ListActiveProcessRecords(ctx context.Context) ([]*ProcessRecord, error) {
	query := `
		SELECT id, step_run_id, pid, pgid, start_time, active, created_at
		FROM process_records
		WHERE active = 1
		ORDER BY id ASC
	`
	rows, err := r.q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: list active process records: %w", err)
	}
	defer rows.Close()

	var records []*ProcessRecord
	for rows.Next() {
		rec, err := scanProcessRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list active process records rows: %w", err)
	}

	return records, nil
}

// MarkProcessInactive updates a process record to inactive upon normal process exit.
func (r *ProcessRecordRepo) MarkProcessInactive(ctx context.Context, id int64) error {
	query := `UPDATE process_records SET active = 0 WHERE id = ?`
	res, err := r.q.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("store: mark process inactive: %w", err)
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

// MarkProcessesInactiveByStepRun updates all process records for a given step run to inactive.
func (r *ProcessRecordRepo) MarkProcessesInactiveByStepRun(ctx context.Context, stepRunID int64) error {
	query := `UPDATE process_records SET active = 0 WHERE step_run_id = ?`
	_, err := r.q.ExecContext(ctx, query, stepRunID)
	if err != nil {
		return fmt.Errorf("store: mark processes inactive by step run: %w", err)
	}
	return nil
}

func scanProcessRecord(row rowScanner) (*ProcessRecord, error) {
	var (
		r            ProcessRecord
		activeInt    int
		createdAtStr string
	)

	err := row.Scan(&r.ID, &r.StepRunID, &r.PID, &r.PGID, &r.StartTime, &activeInt, &createdAtStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: scan process record: %w", err)
	}

	r.Active = activeInt == 1
	r.CreatedAt = parseTime(createdAtStr)
	return &r, nil
}
