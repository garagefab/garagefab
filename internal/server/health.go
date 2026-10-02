// Package server implements HTTP API endpoints.
//
// ==============================================================================
// HEALTH CHECK ENDPOINT (CLI-1):
//
// `GET /api/health` is the unauthenticated startup readiness and liveness probe.
// It is used by:
//  1. `garagefab start`: The CLI polls this endpoint to detect when the HTTP daemon
//     has successfully booted and bound to its port before opening the browser.
//  2. Monitoring and container orchestration probes.
//
// ==============================================================================
package server

import (
	"encoding/json"
	"net/http"
)

// HealthResponse represents the payload returned by GET /api/health.
type HealthResponse struct {
	Status string `json:"status"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
}
