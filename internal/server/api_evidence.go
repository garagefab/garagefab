// Package server implements HTTP routing, middleware, and REST API handlers.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ENTERPRISE / JAVA SPRING COMPARISON:
// Primary/Driving Adapter: REST Controllers for Evidence, Artifacts, and Diffs (APR-1..3, LOG-3).
//
// In Clean/Hexagonal Architecture:
// `api_evidence.go` serves as a Driving Adapter that maps incoming HTTP GET requests
// to domain queries on the `JobEngine` inbound port. It handles query parameter parsing,
// URL path deserialization, MIME content negotiation, and maps domain errors to standard HTTP status codes.
//
// Enterprise / Spring Boot Comparison:
// In Spring Boot: Analogous to `@RestController` endpoints:
//   - `@GetMapping("/api/jobs/{id}/evidence")` returning `ResponseEntity<EvidenceSummaryDTO>`
//   - `@GetMapping("/api/jobs/{id}/diff")` returning `ResponseEntity<String>`
//   - `@GetMapping("/api/jobs/{id}/artifacts/{name}")` returning `ResponseEntity<Resource>`
//
// In Go: We write explicit `http.HandlerFunc` functions using `net/http` primitives and Chi URL routers.
//
// Go Idiom Bridges:
//   - `chi.URLParam(r, "id")`: Extracts wildcard path parameters from the routing context.
//   - Content Negotiation: Inspects file extensions / artifact names to serve appropriate Content-Type
//     headers (`application/json` vs `text/markdown` vs `text/plain`).
//
// ==============================================================================
package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// handleGetEvidence handles GET /api/jobs/{id}/evidence (APR-1, APR-3).
// Serves the structured chain of evidence summary for human inspection.
// This is a pure read operation: zero subprocesses or agent runs are executed.
func (s *Server) handleGetEvidence(w http.ResponseWriter, r *http.Request) {
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

	evidence, err := s.Engine.GetEvidence(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(evidence)
}

// handleGetArtifact handles GET /api/jobs/{id}/artifacts/{name} (LOG-3).
// Serves raw artifact files (.garagefab/jobs/<id>/<name>) from the worktree.
func (s *Server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid job id", http.StatusBadRequest)
		return
	}

	name := chi.URLParam(r, "name")
	if strings.TrimSpace(name) == "" {
		http.Error(w, "Artifact name cannot be empty", http.StatusBadRequest)
		return
	}

	if s.Engine == nil {
		http.Error(w, "Engine not available", http.StatusInternalServerError)
		return
	}

	content, err := s.Engine.GetArtifact(r.Context(), id, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Content-Type determination based on artifact name
	contentType := "text/markdown; charset=utf-8"
	if name == "review" || strings.HasSuffix(name, ".json") {
		contentType = "application/json; charset=utf-8"
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

// handleGetDiff handles GET /api/jobs/{id}/diff (WKT-3, APR-2).
// Serves the unified diff from the merge base for inspection in dashboard or CLI.
func (s *Server) handleGetDiff(w http.ResponseWriter, r *http.Request) {
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

	diffText, err := s.Engine.GetDiff(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(diffText))
}
