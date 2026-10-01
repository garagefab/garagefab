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

	// Validate repository path exists (PRJ-2)
	fi, err := os.Stat(req.RepoPath)
	if err != nil || !fi.IsDir() {
		http.Error(w, "Repository path does not exist", http.StatusBadRequest)
		return
	}

	// Validate it is a git repository (PRJ-2)
	gitDir := filepath.Join(req.RepoPath, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		http.Error(w, "Path is not a valid Git repository (.git not found)", http.StatusBadRequest)
		return
	}

	name := req.Name
	if name == "" {
		name = filepath.Base(req.RepoPath)
	}

	baseRef := req.BaseRef
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
			http.Error(w, "Project with this name already exists", http.StatusConflict)
			return
		}
		if errors.Is(err, store.ErrProjectRepoPathExists) {
			http.Error(w, "Project with this repository path already exists", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to create project", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
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
