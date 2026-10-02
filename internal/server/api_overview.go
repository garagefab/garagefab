// Package server implements HTTP routing, middleware, and REST API handlers.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ENTERPRISE / JAVA SPRING COMPARISON:
// Driving Adapter: Dashboard Overview Controller (UI-1).
//
// In Clean / Hexagonal Architecture:
// `api_overview.go` is an Inbound / Driving Adapter that exposes aggregated dashboard
// state via `GET /api/overview`. It delegates query execution to the `store.OverviewRepo`
// and encodes the resulting `OverviewData` DTO into JSON.
//
// Enterprise / Spring Boot Comparison:
// Equivalent to a Spring `@RestController` endpoint:
//
//	@GetMapping("/api/overview")
//	public ResponseEntity<OverviewDTO> getOverview() { ... }
//
// ==============================================================================
package server

import (
	"encoding/json"
	"net/http"
)

// handleGetOverview handles GET /api/overview (UI-1).
// Returns aggregated status counts, attention items, recent activity, and health indicators.
func (s *Server) handleGetOverview(w http.ResponseWriter, r *http.Request) {
	data, err := s.DB.Overview().GetOverviewData(r.Context())
	if err != nil {
		http.Error(w, "Failed to retrieve overview data: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(data)
}
