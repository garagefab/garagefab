// Package server implements project management REST APIs.
//
// ==============================================================================
// ARCHITECTURAL ROLE & REST ENDPOINTS:
// Project Registration & Repository Validation (PRJ-1..5).
//
// Endpoints:
// - `GET  /api/projects`     : Lists all active registered projects.
// - `POST /api/projects`     : Registers a new local Git repository (PRJ-1..5).
// - `GET  /api/projects/{id}`: Retrieves details for a specific project.
//
// GO CONCEPTS & JAVA / SPRING COMPARISONS:
//
//  1. JSON Request Decoding & Response Streaming:
//     In Spring Boot: `@RequestBody CreateProjectDTO dto` is deserialized automatically by Jackson.
//     In Go: We stream directly from the HTTP request body via `json.NewDecoder(r.Body).Decode(&req)`.
//     Responses are written directly via `json.NewEncoder(w).Encode(p)`.
//
//  2. URL Path Parameter Extraction:
//     Chi provides `chi.URLParam(r, "id")` (equivalent to Spring's `@PathVariable("id")`).
//     Since all path parameters are strings in HTTP, `strconv.ParseInt(idStr, 10, 64)` converts
//     it to an int64 with explicit error handling.
//
// ==============================================================================
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/garagefab/garagefab/internal/store"
)

// createProjectRequest defines the JSON payload for registering a project.
type createProjectRequest struct {
	Name             string   `json:"name"`
	RepoPath         string   `json:"repo_path"`
	BaseRef          string   `json:"base_ref"`
	EnabledWorkTypes []string `json:"enabled_work_types"`
}

// handleListProjects handles GET /api/projects.
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.DB.Projects().ListProjects(r.Context())
	if err != nil {
		http.Error(w, "Failed to list projects", http.StatusInternalServerError)
		return
	}
	// Guarantee JSON array `[]` rather than `null` if empty
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

	if req.RepoPath == "" {
		http.Error(w, "repo_path is required", http.StatusBadRequest)
		return
	}

	// Validate repository path exists on disk (PRJ-2)
	fi, err := os.Stat(req.RepoPath)
	if err != nil || !fi.IsDir() {
		http.Error(w, "Repository path does not exist", http.StatusBadRequest)
		return
	}

	// Validate path is a valid Git repository containing .git (PRJ-2)
	gitDir := filepath.Join(req.RepoPath, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		http.Error(w, "Path is not a valid Git repository (.git not found)", http.StatusBadRequest)
		return
	}

	// Default project name to folder basename if omitted
	name := req.Name
	if name == "" {
		name = filepath.Base(req.RepoPath)
	}

	// Default base branch to origin/main if omitted
	baseRef := req.BaseRef
	if baseRef == "" {
		baseRef = "origin/main"
	}

	// Default to enabling all 4 work types if none specified
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
			http.Error(w, "Project with this name already exists", http.StatusConflict) // 409 Conflict
			return
		}
		if errors.Is(err, store.ErrProjectRepoPathExists) {
			http.Error(w, "Project with this repository path already exists", http.StatusConflict) // 409 Conflict
			return
		}
		http.Error(w, "Failed to create project", http.StatusInternalServerError)
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
