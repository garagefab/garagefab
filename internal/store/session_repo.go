// Package store implements repository data access for web dashboard sessions.
//
// ==============================================================================
// ARCHITECTURAL ROLE & SECURITY:
// Dashboard Session Management & Token Exchange (SEC-3).
//
// To allow seamless dashboard access without exposing raw API tokens in frontend JavaScript:
// 1. The dashboard exchanges the API token via `POST /api/session` for a secure session cookie (`gf_session`).
// 2. The session record stores:
//   - `id`: Cryptographically secure random session ID (UUID or random hex string).
//   - `token_hash`: SHA-256 hash of the API token used to mint the session.
//   - `expires_at`: Expiration timestamp.
//     3. When the API token is rotated in `config.yaml`, `DeleteAllSessions()` invalidates all
//     active web sessions immediately (SEC-3).
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

// GetSession retrieves a session by its unique session ID.
func (r *SessionRepo) GetSession(ctx context.Context, id string) (*Session, error) {
	query := `SELECT id, token_hash, expires_at, created_at FROM sessions WHERE id = ?`
	row := r.q.QueryRowContext(ctx, query, id)
	return scanSession(row)
}

// DeleteSession deletes a single session by ID (e.g. user logout).
func (r *SessionRepo) DeleteSession(ctx context.Context, id string) error {
	query := `DELETE FROM sessions WHERE id = ?`
	_, err := r.q.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("store: delete session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions cleans up sessions whose expires_at is in the past.
func (r *SessionRepo) DeleteExpiredSessions(ctx context.Context) error {
	nowStr := formatTime(time.Now().UTC())
	query := `DELETE FROM sessions WHERE expires_at < ?`
	_, err := r.q.ExecContext(ctx, query, nowStr)
	if err != nil {
		return fmt.Errorf("store: delete expired sessions: %w", err)
	}
	return nil
}

// DeleteAllSessions invalidates all sessions across the database when the API token is rotated (SEC-3).
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
