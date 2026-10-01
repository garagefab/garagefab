package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SessionRepo handles web dashboard authentication sessions (SEC-3).
type SessionRepo struct {
	q dbtx
}

// CreateSession stores a new dashboard session.
func (r *SessionRepo) CreateSession(ctx context.Context, s *Session) error {
	now := time.Now().UTC()
	nowStr := formatTime(now)
	expiresAtStr := formatTime(s.ExpiresAt)

	query := `INSERT INTO sessions (id, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?)`
	_, err := r.q.ExecContext(ctx, query, s.ID, s.TokenHash, expiresAtStr, nowStr)
	if err != nil {
		return fmt.Errorf("store: insert session: %w", err)
	}

	s.CreatedAt = now
	return nil
}

// GetSession retrieves a session by ID.
func (r *SessionRepo) GetSession(ctx context.Context, id string) (*Session, error) {
	query := `SELECT id, token_hash, expires_at, created_at FROM sessions WHERE id = ?`
	row := r.q.QueryRowContext(ctx, query, id)
	return scanSession(row)
}

// DeleteSession deletes a session by ID (e.g. logout).
func (r *SessionRepo) DeleteSession(ctx context.Context, id string) error {
	query := `DELETE FROM sessions WHERE id = ?`
	_, err := r.q.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("store: delete session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions cleans up sessions whose expires_at is before now.
func (r *SessionRepo) DeleteExpiredSessions(ctx context.Context) error {
	nowStr := formatTime(time.Now().UTC())
	query := `DELETE FROM sessions WHERE expires_at < ?`
	_, err := r.q.ExecContext(ctx, query, nowStr)
	if err != nil {
		return fmt.Errorf("store: delete expired sessions: %w", err)
	}
	return nil
}

// DeleteAllSessions invalidates all sessions when the API token is rotated (SEC-3).
func (r *SessionRepo) DeleteAllSessions(ctx context.Context) error {
	query := `DELETE FROM sessions`
	_, err := r.q.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("store: delete all sessions: %w", err)
	}
	return nil
}

func scanSession(row rowScanner) (*Session, error) {
	var (
		s            Session
		expiresAtStr string
		createdAtStr string
	)

	err := row.Scan(&s.ID, &s.TokenHash, &expiresAtStr, &createdAtStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: scan session: %w", err)
	}

	s.ExpiresAt = parseTime(expiresAtStr)
	s.CreatedAt = parseTime(createdAtStr)
	return &s, nil
}
