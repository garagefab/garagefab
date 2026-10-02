// Package server implements project management REST APIs.
//
// ==============================================================================
// ARCHITECTURAL ROLE & REST ENDPOINTS:
// Project Registration & Repository Validation (PRJ-1..8).
//
// Endpoints:
// - `GET  /api/projects`                     : Lists all active registered projects.
// - `POST /api/projects`                     : Registers a new local Git repository (PRJ-1..5).
// - `GET  /api/projects/{id}`                : Retrieves details for a specific project.
// - `POST /api/projects/{id}/archive`        : Soft-archives a project if no active jobs exist (PRJ-8).
// - `POST /api/projects/{id}/config-template`: Writes default project.yaml template to checkout (PRJ-7).
// - `POST /api/projects/config-template`     : Writes template given repo_path payload (PRJ-7).
//
// ==============================================================================
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/store"
)

// DefaultProjectYAMLTemplate is the canonical project configuration template (PRJ-3, PRJ-7).
const DefaultProjectYAMLTemplate = `# .garagefab/project.yaml
base_ref: origin/main
agents:
  spec: agy
  coding: agy
  review: agy
work_types:
  - bug_fix
  - feature
  - refactor
  - docs
commands:
  build: []
  test: []
  lint: []
guardrails:
  protected_paths:
    - "**/*_test.go"
`

// createProjectRequest defines the JSON payload for registering a project.
type createProjectRequest struct {
	Name             string   `json:"name"`
	RepoPath         string   `json:"repo_path"`
	BaseRef          string   `json:"base_ref"`
	EnabledWorkTypes []string `json:"enabled_work_types"`
}

type configTemplateRequest struct {
	RepoPath string `json:"repo_path"`
}

// handleListProjects handles GET /api/projects.
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.DB.Projects().ListProjects(r.Context())
	if err != nil {
		http.Error(w, "Failed to list projects", http.StatusInternalServerError)
		return
	}
	if projects == nil {
		projects = []*store.Project{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(projects)
}

// handleCreateProject handles POST /api/projects (PRJ-1..5).
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.RepoPath) == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{
				"code":    "validation_failed",
				"message": "repo_path is required",
			},
		})
		return
	}

	// Validate repository path exists on disk (PRJ-2)
	fi, err := os.Stat(req.RepoPath)
	if err != nil || !fi.IsDir() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "validation_failed",
				"message": "Repository path does not exist",
				"details": map[string]string{
					"repo_path": "Path does not exist on disk",
				},
			},
		})
		return
	}

	// Validate path is a valid Git repository containing .git (PRJ-2)
	gitDir := filepath.Join(req.RepoPath, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "validation_failed",
				"message": "Path is not a valid Git repository (.git not found)",
				"details": map[string]string{
					"repo_path": ".git directory not found",
				},
			},
		})
		return
	}

	// Read and validate <repo>/.garagefab/project.yaml (PRJ-3, PRJ-4)
	projectYAMLPath := filepath.Join(req.RepoPath, ".garagefab", "project.yaml")
	if _, err := os.Stat(projectYAMLPath); err != nil {
		// Missing project.yaml: reject with 422 and return template (PRJ-3)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "validation_failed",
				"message": "project.yaml missing from repository",
				"details": map[string]string{
					"repo_path": "Missing .garagefab/project.yaml configuration",
				},
			},
			"template": DefaultProjectYAMLTemplate,
		})
		return
	}

	// Validate project.yaml content (PRJ-4)
	if _, err := config.LoadProjectConfig(req.RepoPath); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "validation_failed",
				"message": fmt.Sprintf("project.yaml validation error: %v", err),
				"details": map[string]string{
					"project_yaml": err.Error(),
				},
			},
		})
		return
	}

	// Default project name to folder basename if omitted
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = filepath.Base(req.RepoPath)
	}

	baseRef := strings.TrimSpace(req.BaseRef)
	if baseRef == "" {
		baseRef = "origin/main"
	}

	workTypes := req.EnabledWorkTypes
	if len(workTypes) == 0 {
		workTypes = []string{store.WorkTypeBugFix, store.WorkTypeFeature, store.WorkTypeRefactor, store.WorkTypeDocs}
	}

	p := &store.Project{
		Name:             name,
		RepoPath:         req.RepoPath,
		BaseRef:          baseRef,
		EnabledWorkTypes: workTypes,
	}

	if err := s.DB.Projects().CreateProject(r.Context(), p); err != nil {
		if errors.Is(err, store.ErrProjectNameExists) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{
					"code":    "conflict",
					"message": "Project with this name already exists",
				},
			})
			return
		}
		if errors.Is(err, store.ErrProjectRepoPathExists) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{
					"code":    "conflict",
					"message": "Project with this repository path already exists",
				},
			})
			return
		}
		http.Error(w, "Failed to create project: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated) // 201 Created
	_ = json.NewEncoder(w).Encode(p)
}

// handleGetProject handles GET /api/projects/{id}.
func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid project id", http.StatusBadRequest)
		return
	}

	p, err := s.DB.Projects().GetProject(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "Project not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to get project", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}

// handleArchiveProject handles POST /api/projects/{id}/archive (PRJ-8).
// Refuses archiving if non-terminal jobs are active; otherwise sets is_archived = 1.
func (s *Server) handleArchiveProject(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid project id", http.StatusBadRequest)
		return
	}

	// Verify project exists
	_, err = s.DB.Projects().GetProject(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "Project not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to retrieve project", http.StatusInternalServerError)
		return
	}

	// Check if active non-terminal jobs exist for this project (PRJ-8)
	activeCount, err := s.DB.Jobs().CountNonTerminalJobsByProject(r.Context(), id)
	if err != nil {
		http.Error(w, "Failed to check project job status", http.StatusInternalServerError)
		return
	}

	if activeCount > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict) // 409 Conflict
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{
				"code":    "invalid_state",
				"message": fmt.Sprintf("Project has %d active jobs; archiving is refused.", activeCount),
			},
		})
		return
	}

	if err := s.DB.Projects().ArchiveProject(r.Context(), id); err != nil {
		http.Error(w, "Failed to archive project", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "archived"})
}

// handleCreateProjectConfigTemplate handles POST /api/projects/{id}/config-template
// and POST /api/projects/config-template (PRJ-7).
// Writes default project.yaml to checkout (.garagefab/project.yaml).
func (s *Server) handleCreateProjectConfigTemplate(w http.ResponseWriter, r *http.Request) {
	var repoPath string

	idStr := chi.URLParam(r, "id")
	if idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err == nil {
			p, err := s.DB.Projects().GetProject(r.Context(), id)
			if err == nil && p != nil {
				repoPath = p.RepoPath
			}
		}
	}

	// If no valid project ID was in path, decode from JSON body
	if repoPath == "" {
		var req configTemplateRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		repoPath = req.RepoPath
	}

	if strings.TrimSpace(repoPath) == "" {
		http.Error(w, "repo_path is required", http.StatusBadRequest)
		return
	}

	gfDir := filepath.Join(repoPath, ".garagefab")
	if err := os.MkdirAll(gfDir, 0755); err != nil {
		http.Error(w, "Failed to create .garagefab directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	templatePath := filepath.Join(gfDir, "project.yaml")
	if err := os.WriteFile(templatePath, []byte(DefaultProjectYAMLTemplate), 0644); err != nil {
		http.Error(w, "Failed to write project.yaml template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":   "created",
		"path":     templatePath,
		"template": DefaultProjectYAMLTemplate,
	})
}
