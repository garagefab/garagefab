// Package server implements job management and pipeline lifecycle REST APIs.
//
// ==============================================================================
// ARCHITECTURAL ROLE & REST ENDPOINTS:
// Clarification Flow API Controller (Inbound Adapter, SPC-3).
//
// Endpoint:
// - `POST /api/jobs/{id}/clarification` : Submits answers to clarification questions.
//
// Security & Authentication:
// - Auth: `B` (Session cookie or Bearer token, accessible by web UI and CLI `garagefab-work`).
//
// ENTERPRISE & JAVA / SPRING COMPARISON:
// In Spring Boot: An `@PostMapping("/api/jobs/{id}/clarification")` controller endpoint
// validating the request body with `@Valid` and delegating to `JobService.submitClarification`.
// In Go: We parse the URL parameter with Chi, decode the JSON payload with `json.NewDecoder`,
// and map domain sentinel errors (`factory.ErrInvalidState`) to HTTP status codes (`409 Conflict`).
//
// ==============================================================================
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/garagefab/garagefab/internal/factory"
)

type clarificationRequest struct {
	Answers []factory.ClarificationAnswer `json:"answers"`
}

// handleClarification handles POST /api/jobs/{id}/clarification (SPC-3).
// Accepts clarification answers from the user or CLI and resumes the spec stage.
func (s *Server) handleClarification(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	jobID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid job ID", http.StatusBadRequest)
		return
	}

	var req clarificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if len(req.Answers) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{
				"code":    "validation_failed",
				"message": "answers array cannot be empty",
			},
		})
		return
	}

	for _, a := range req.Answers {
		if a.Q < 1 || strings.TrimSpace(a.Answer) == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{
					"code":    "validation_failed",
					"message": "each answer must have Q >= 1 and non-empty answer text",
				},
			})
			return
		}
	}

	if s.Engine == nil {
		http.Error(w, "Engine not configured", http.StatusInternalServerError)
		return
	}

	if err := s.Engine.SubmitClarification(r.Context(), jobID, req.Answers); err != nil {
		if errors.Is(err, factory.ErrInvalidState) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{
					"code":    "invalid_state",
					"message": "Job is not in needs_clarification status",
				},
			})
			return
		}
		http.Error(w, fmt.Sprintf("Failed to submit clarification: %v", err), http.StatusInternalServerError)
		return
	}

	if s.Scheduler != nil {
		s.Scheduler.Wake()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
