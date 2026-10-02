// Package store implements repository data access for projects.
//
// ==============================================================================
// ARCHITECTURAL ROLE & REPOSITORY PATTERN:
// Repository Pattern (PRJ-1..6, PRJ-8).
//
// `ProjectRepo` provides CRUD and query methods for registered Git projects.
//
// GO CONCEPTS & JAVA COMPARISONS:
//
//  1. Parametrized SQL Queries (`?` placeholders):
//     In Java/JDBC: You use `PreparedStatement` with `?`.
//     In Go: `r.q.ExecContext(ctx, query, args...)` automatically prepares the query
//     and binds parameters, preventing SQL injection vulnerabilities.
//
//  2. Polymorphic Row Scanning (`rowScanner` Interface):
//     `*sql.Row` (from `QueryRowContext`) and `*sql.Rows` (from `QueryContext`) both
//     implement the method `Scan(dest ...any) error`.
//     By defining a private interface `type rowScanner interface { Scan(...any) error }`,
//     the same `scanProject` helper function can parse single rows or loop over row sets!
//
//  3. JSON Columns in SQLite:
//     Go slices like `EnabledWorkTypes []string` are marshaled to JSON strings before
//     insertion and unmarshaled on scan.
//
// ==============================================================================
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ProjectRepo handles database operations for projects (PRJ-1..6).
type ProjectRepo struct {
	q dbtx // Injected query executor (either *DB or *Tx)
}

// CreateProject inserts a new project into the database.
// It returns ErrProjectNameExists or ErrProjectRepoPathExists if unique constraints are violated (PRJ-5).
func (r *ProjectRepo) CreateProject(ctx context.Context, p *Project) error {
	// Serialize work types slice to JSON text
	workTypesJSON, err := json.Marshal(p.EnabledWorkTypes)
	if err != nil {
		return fmt.Errorf("store: marshal enabled_work_types: %w", err)
	}

	now := time.Now().UTC()
	nowStr := formatTime(now)

	baseRef := p.BaseRef
	if baseRef == "" {
		baseRef = "origin/main"
	}

	query := `
		INSERT INTO projects (name, repo_path, base_ref, enabled_work_types, is_archived, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	isArchivedInt := 0
	if p.IsArchived {
		isArchivedInt = 1
	}

	// Execute parameterized insert
	res, err := r.q.ExecContext(ctx, query, p.Name, p.RepoPath, baseRef, string(workTypesJSON), isArchivedInt, nowStr, nowStr)
	if err != nil {
		errStr := err.Error()
		// Translate SQLite unique constraint violations into domain errors (PRJ-5)
		if strings.Contains(errStr, "UNIQUE constraint failed: projects.name") {
			return ErrProjectNameExists
		}
		if strings.Contains(errStr, "UNIQUE constraint failed: projects.repo_path") {
			return ErrProjectRepoPathExists
		}
		return fmt.Errorf("store: insert project: %w", err)
	}

	// Retrieve auto-generated SQLite rowid
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: get project last insert id: %w", err)
	}

	p.ID = id
	p.BaseRef = baseRef
	p.CreatedAt = now
	p.UpdatedAt = now
	return nil
}

// GetProject retrieves a project by its primary key ID.
func (r *ProjectRepo) GetProject(ctx context.Context, id int64) (*Project, error) {
	query := `
		SELECT id, name, repo_path, base_ref, enabled_work_types, is_archived, created_at, updated_at
		FROM projects
		WHERE id = ?
	`
	row := r.q.QueryRowContext(ctx, query, id)
	return scanProject(row)
}

// GetProjectByName retrieves a project by its unique name.
func (r *ProjectRepo) GetProjectByName(ctx context.Context, name string) (*Project, error) {
	query := `
		SELECT id, name, repo_path, base_ref, enabled_work_types, is_archived, created_at, updated_at
		FROM projects
		WHERE name = ?
	`
	row := r.q.QueryRowContext(ctx, query, name)
	return scanProject(row)
}

// GetProjectByRepoPath retrieves a project by its repository filesystem path.
func (r *ProjectRepo) GetProjectByRepoPath(ctx context.Context, repoPath string) (*Project, error) {
	query := `
		SELECT id, name, repo_path, base_ref, enabled_work_types, is_archived, created_at, updated_at
		FROM projects
		WHERE repo_path = ?
	`
	row := r.q.QueryRowContext(ctx, query, repoPath)
	return scanProject(row)
}

// ListProjects returns all active (non-archived) projects ordered by ID.
func (r *ProjectRepo) ListProjects(ctx context.Context) ([]*Project, error) {
	query := `
		SELECT id, name, repo_path, base_ref, enabled_work_types, is_archived, created_at, updated_at
		FROM projects
		WHERE is_archived = 0
		ORDER BY id ASC
	`
	rows, err := r.q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	defer rows.Close() // Guarantee rows cursor is closed when function returns

	var projects []*Project
	for rows.Next() {
		p, err := scanProjectRows(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}

	// Check if any error occurred during iteration
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list projects rows: %w", err)
	}

	return projects, nil
}

// UpdateProject updates mutable configuration properties of a project.
func (r *ProjectRepo) UpdateProject(ctx context.Context, p *Project) error {
	workTypesJSON, err := json.Marshal(p.EnabledWorkTypes)
	if err != nil {
		return fmt.Errorf("store: marshal enabled_work_types: %w", err)
	}

	now := time.Now().UTC()
	nowStr := formatTime(now)

	query := `
		UPDATE projects
		SET name = ?, repo_path = ?, base_ref = ?, enabled_work_types = ?, updated_at = ?
		WHERE id = ?
	`
	res, err := r.q.ExecContext(ctx, query, p.Name, p.RepoPath, p.BaseRef, string(workTypesJSON), nowStr, p.ID)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "UNIQUE constraint failed: projects.name") {
			return ErrProjectNameExists
		}
		if strings.Contains(errStr, "UNIQUE constraint failed: projects.repo_path") {
			return ErrProjectRepoPathExists
		}
		return fmt.Errorf("store: update project: %w", err)
	}

	rowsAff, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: get rows affected: %w", err)
	}
	if rowsAff == 0 {
		return ErrNotFound
	}

	p.UpdatedAt = now
	return nil
}

// ArchiveProject soft-deletes a project by marking is_archived = 1 (PRJ-8).
func (r *ProjectRepo) ArchiveProject(ctx context.Context, id int64) error {
	nowStr := formatTime(time.Now().UTC())
	query := `UPDATE projects SET is_archived = 1, updated_at = ? WHERE id = ?`
	res, err := r.q.ExecContext(ctx, query, nowStr, id)
	if err != nil {
		return fmt.Errorf("store: archive project: %w", err)
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

// rowScanner abstracts *sql.Row and *sql.Rows for unified column scanning.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanProject(row rowScanner) (*Project, error) {
	var (
		p             Project
		workTypesJSON string
		isArchivedInt int
		createdAtStr  string
		updatedAtStr  string
	)

	// Scan maps database columns by reference into local variables
	err := row.Scan(
		&p.ID,
		&p.Name,
		&p.RepoPath,
		&p.BaseRef,
		&workTypesJSON,
		&isArchivedInt,
		&createdAtStr,
		&updatedAtStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: scan project: %w", err)
	}

	p.IsArchived = isArchivedInt == 1
	p.CreatedAt = parseTime(createdAtStr)
	p.UpdatedAt = parseTime(updatedAtStr)

	if workTypesJSON != "" {
		if err := json.Unmarshal([]byte(workTypesJSON), &p.EnabledWorkTypes); err != nil {
			p.EnabledWorkTypes = nil
		}
	}

	return &p, nil
}

func scanProjectRows(rows *sql.Rows) (*Project, error) {
	return scanProject(rows)
}
