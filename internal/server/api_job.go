// Package server implements job management and pipeline lifecycle REST APIs.
//
// ==============================================================================
// ARCHITECTURAL ROLE & REST ENDPOINTS:
// Job Intake, Lifecycle Control & Human Gate Operations (INT-1, PIP-6, PIP-7, APR-5..7).
//
// Endpoints:
// - `GET  /api/jobs`              : Lists jobs with query filters (status, stage, project).
// - `POST /api/jobs`              : Submits a new job into the queue (INT-1).
// - `GET  /api/jobs/{id}`         : Fetches job state and metadata.
// - `POST /api/jobs/{id}/approve` : Human sign-off at gate (APR-5, APR-7).
// - `POST /api/jobs/{id}/reject`  : Human rejection with mandatory note (APR-6, APR-7).
// - `POST /api/jobs/{id}/cancel`  : Cancels an active job (PIP-6).
// - `POST /api/jobs/{id}/retry`   : Retries a failed/interrupted job (PIP-7).
// ==============================================================================
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/store"
)

type createJobRequest struct {
	ProjectID int64  `json:"project_id"`
	WorkType  string `json:"work_type"`
	Title     string `json:"title"`
	Intent    string `json:"intent"`
}

type approveJobRequest struct {
	HeadSHA string `json:"head_sha"`
}

type rejectJobRequest struct {
	Note string `json:"note"`
}

// handleListJobs handles GET /api/jobs with optional query parameter filters.
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	var filter store.JobListFilter

	// Parse query parameters
	if pIDStr := r.URL.Query().Get("project_id"); pIDStr != "" {
		if pID, err := strconv.ParseInt(pIDStr, 10, 64); err == nil {
			filter.ProjectID = &pID
		}
	}
	if status := r.URL.Query().Get("status"); status != "" {
		filter.Status = &status
	}
	if stage := r.URL.Query().Get("stage"); stage != "" {
		filter.Stage = &stage
	}
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil {
			filter.Limit = limit
		}
	}

	jobs, err := s.DB.Jobs().ListJobs(r.Context(), filter)
	if err != nil {
		http.Error(w, "Failed to list jobs", http.StatusInternalServerError)
		return
	}
	if jobs == nil {
		jobs = []*store.Job{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jobs)
}

// handleCreateJob handles POST /api/jobs (INT-1).
// Submits a new job into the queue and notifies the scheduler.
func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var req createJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.ProjectID <= 0 {
		http.Error(w, "project_id is required", http.StatusBadRequest)
		return
	}
	if req.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	if req.WorkType == "" {
		req.WorkType = store.WorkTypeRefactor
	}

	// Verify target project exists
	project, err := s.DB.Projects().GetProject(r.Context(), req.ProjectID)
	if err != nil || project == nil {
		http.Error(w, "Project not found", http.StatusBadRequest)
		return
	}

	// Verify work type is enabled for this project (PRJ-4)
	if len(project.EnabledWorkTypes) > 0 {
		enabled := false
		for _, wt := range project.EnabledWorkTypes {
			if wt == req.WorkType {
				enabled = true
				break
			}
		}
		if !enabled {
			http.Error(w, "Work type not enabled for project", http.StatusBadRequest)
			return
		}
	}

	job := &store.Job{
		ProjectID: req.ProjectID,
		WorkType:  req.WorkType,
		Title:     req.Title,
		Intent:    req.Intent,
		Stage:     store.StageIntent,
		Status:    store.StatusQueued,
		Source:    store.SourceDashboard,
	}

	if err := s.DB.Jobs().CreateJob(r.Context(), job); err != nil {
		http.Error(w, "Failed to create job", http.StatusInternalServerError)
		return
	}

	// Reactively wake scheduler to admit job immediately if concurrency slots are open
	if s.Scheduler != nil {
		s.Scheduler.Wake()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(job)
}

type jobResponse struct {
	*store.Job
	HandoffCommand string `json:"handoff_command"`
}

// handleGetJob handles GET /api/jobs/{id}.
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid job id", http.StatusBadRequest)
		return
	}

	job, err := s.DB.Jobs().GetJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "Job not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to get job", http.StatusInternalServerError)
		return
	}

	var handoffCmd string
	if factory.HandoffEligible(job.Status) {
		project, pErr := s.DB.Projects().GetProject(r.Context(), job.ProjectID)
		if pErr == nil && project != nil {
			if projCfg, cfgErr := config.LoadProjectConfig(project.RepoPath); cfgErr == nil && projCfg != nil {
				if role, ok := factory.RoleForStage(job.Stage); ok {
					agent := projCfg.AgentForRole(role)
					if agent != "" {
						handoffCmd = factory.HandoffCommand(project.RepoPath, agent, job.ID)
					}
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jobResponse{
		Job:            job,
		HandoffCommand: handoffCmd,
	})
}

// handleGetJobSteps handles GET /api/jobs/{id}/steps.
func (s *Server) handleGetJobSteps(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid job id", http.StatusBadRequest)
		return
	}

	steps, err := s.DB.StepRuns().ListStepRunsByJob(r.Context(), id)
	if err != nil {
		http.Error(w, "Failed to retrieve step runs: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if steps == nil {
		steps = []*store.StepRun{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(steps)
}

// handleApproveJob handles POST /api/jobs/{id}/approve (APR-5, APR-7, SEC-4).
// Restricted to interactive browser session cookies.
func (s *Server) handleApproveJob(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid job id", http.StatusBadRequest)
		return
	}

	var req approveJobRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	if s.Engine == nil {
		http.Error(w, "Engine not available", http.StatusInternalServerError)
		return
	}

	// Execute engine approval with stale evidence verification
	if err := s.Engine.Approve(r.Context(), id, req.HeadSHA); err != nil {
		if errors.Is(err, factory.ErrSpecInvalid) {
			http.Error(w, "422 validation_failed", http.StatusUnprocessableEntity) // 422 Unprocessable Entity (SPC-6)
			return
		}
		if errors.Is(err, factory.ErrStaleEvidence) {
			http.Error(w, "409 stale_evidence", http.StatusConflict) // 409 Conflict (APR-5)
			return
		}
		if errors.Is(err, factory.ErrInvalidState) {
			http.Error(w, "409 invalid_state", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"approved"}`))
}

// handleRejectJob handles POST /api/jobs/{id}/reject (APR-6, APR-7, SEC-4).
// Rejection note is mandatory and returns 422 if omitted.
func (s *Server) handleRejectJob(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid job id", http.StatusBadRequest)
		return
	}

	var req rejectJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	// Validate rejection note presence (APR-6)
	if strings.TrimSpace(req.Note) == "" {
		http.Error(w, "Rejection note cannot be empty", http.StatusUnprocessableEntity) // 422 Unprocessable Entity
		return
	}

	if s.Engine == nil {
		http.Error(w, "Engine not available", http.StatusInternalServerError)
		return
	}

	if err := s.Engine.Reject(r.Context(), id, req.Note); err != nil {
		if errors.Is(err, factory.ErrEmptyRejectionNote) {
			http.Error(w, "422 validation_failed", http.StatusUnprocessableEntity)
			return
		}
		if errors.Is(err, factory.ErrInvalidState) {
			http.Error(w, "409 invalid_state", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"rejected"}`))
}

// handleCancelJob handles POST /api/jobs/{id}/cancel (PIP-6).
func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid job id", http.StatusBadRequest)
		return
	}

	if s.Engine == nil {
		http.Error(w, "Engine not available", http.StatusInternalServerError)
		return
	}

	if err := s.Engine.Cancel(r.Context(), id); err != nil {
		if errors.Is(err, factory.ErrInvalidState) {
			http.Error(w, "409 invalid_state", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"cancelled"}`))
}

// handleRetryJob handles POST /api/jobs/{id}/retry (PIP-7).
func (s *Server) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid job id", http.StatusBadRequest)
		return
	}

	if s.Engine == nil {
		http.Error(w, "Engine not available", http.StatusInternalServerError)
		return
	}

	if err := s.Engine.Retry(r.Context(), id); err != nil {
		if errors.Is(err, factory.ErrInvalidState) {
			http.Error(w, "409 invalid_state", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"queued"}`))
}
