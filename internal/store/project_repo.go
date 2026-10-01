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
	q dbtx
}

// CreateProject inserts a new project into the database.
// It returns ErrProjectNameExists or ErrProjectRepoPathExists if unique constraints are violated (PRJ-5).
func (r *ProjectRepo) CreateProject(ctx context.Context, p *Project) error {
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

	res, err := r.q.ExecContext(ctx, query, p.Name, p.RepoPath, baseRef, string(workTypesJSON), isArchivedInt, nowStr, nowStr)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "UNIQUE constraint failed: projects.name") {
			return ErrProjectNameExists
		}
		if strings.Contains(errStr, "UNIQUE constraint failed: projects.repo_path") {
			return ErrProjectRepoPathExists
		}
		return fmt.Errorf("store: insert project: %w", err)
	}

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

// GetProject retrieves a project by ID.
func (r *ProjectRepo) GetProject(ctx context.Context, id int64) (*Project, error) {
	query := `
		SELECT id, name, repo_path, base_ref, enabled_work_types, is_archived, created_at, updated_at
		FROM projects
		WHERE id = ?
	`
	row := r.q.QueryRowContext(ctx, query, id)
	return scanProject(row)
}

// GetProjectByName retrieves a project by name.
func (r *ProjectRepo) GetProjectByName(ctx context.Context, name string) (*Project, error) {
	query := `
		SELECT id, name, repo_path, base_ref, enabled_work_types, is_archived, created_at, updated_at
		FROM projects
		WHERE name = ?
	`
	row := r.q.QueryRowContext(ctx, query, name)
	return scanProject(row)
}

// GetProjectByRepoPath retrieves a project by repo path.
func (r *ProjectRepo) GetProjectByRepoPath(ctx context.Context, repoPath string) (*Project, error) {
	query := `
		SELECT id, name, repo_path, base_ref, enabled_work_types, is_archived, created_at, updated_at
		FROM projects
		WHERE repo_path = ?
	`
	row := r.q.QueryRowContext(ctx, query, repoPath)
	return scanProject(row)
}

// ListProjects returns all non-archived projects.
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
	defer rows.Close()

	var projects []*Project
	for rows.Next() {
		p, err := scanProjectRows(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list projects rows: %w", err)
	}

	return projects, nil
}

// UpdateProject updates mutable fields of a project.
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

// ArchiveProject marks a project as archived (PRJ-8).
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
