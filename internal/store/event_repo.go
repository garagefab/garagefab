package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// EventRepo handles append-only events for SSE broadcast and dashboard activity feeds (LOG-4).
type EventRepo struct {
	q dbtx
}

// CreateEvent appends an event to the events table.
func (r *EventRepo) CreateEvent(ctx context.Context, e *Event) error {
	now := time.Now().UTC()
	nowStr := formatTime(now)

	if e.Payload == "" {
		e.Payload = "{}"
	}

	query := `INSERT INTO events (job_id, type, payload, created_at) VALUES (?, ?, ?, ?)`
	res, err := r.q.ExecContext(ctx, query, e.JobID, e.Type, e.Payload, nowStr)
	if err != nil {
		return fmt.Errorf("store: insert event: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: get event last insert id: %w", err)
	}

	e.ID = id
	e.CreatedAt = now
	return nil
}

// ListEventsByJob retrieves all events for a given job ordered by event id.
func (r *EventRepo) ListEventsByJob(ctx context.Context, jobID int64) ([]*Event, error) {
	query := `
		SELECT id, job_id, type, payload, created_at
		FROM events
		WHERE job_id = ?
		ORDER BY id ASC
	`
	rows, err := r.q.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("store: list events by job: %w", err)
	}
	defer rows.Close()

	var events []*Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list events rows: %w", err)
	}

	return events, nil
}

// ListEventsSince retrieves events with ID > sinceID up to limit (used by SSE stream with Last-Event-ID).
func (r *EventRepo) ListEventsSince(ctx context.Context, sinceID int64, limit int) ([]*Event, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT id, job_id, type, payload, created_at
		FROM events
		WHERE id > ?
		ORDER BY id ASC
		LIMIT ?
	`
	rows, err := r.q.QueryContext(ctx, query, sinceID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list events since %d: %w", sinceID, err)
	}
	defer rows.Close()

	var events []*Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list events since rows: %w", err)
	}

	return events, nil
}

// ListRecentEvents retrieves the most recent events across all jobs up to limit (used by Overview feed).
func (r *EventRepo) ListRecentEvents(ctx context.Context, limit int) ([]*Event, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id, job_id, type, payload, created_at
		FROM events
		ORDER BY id DESC
		LIMIT ?
	`
	rows, err := r.q.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list recent events: %w", err)
	}
	defer rows.Close()

	var events []*Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list recent events rows: %w", err)
	}

	return events, nil
}

func scanEvent(row rowScanner) (*Event, error) {
	var (
		e            Event
		createdAtStr string
	)

	err := row.Scan(&e.ID, &e.JobID, &e.Type, &e.Payload, &createdAtStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: scan event: %w", err)
	}

	e.CreatedAt = parseTime(createdAtStr)
	return &e, nil
}
