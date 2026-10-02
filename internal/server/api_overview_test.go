// Package server implements HTTP routing, middleware, and tests for REST APIs.
//
// ==============================================================================
// ARCHITECTURAL ROLE & TESTING CONCEPTS:
// Integration Test: Overview REST API (UI-1, SEC-3).
//
// Verifies that:
//  1. Unauthenticated requests to /api/overview are rejected with 401.
//  2. Authenticated requests return accurate job counts, attention items,
//     and recent events.
//
// ==============================================================================
package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/garagefab/garagefab/internal/store"
)

func TestOverviewAPI_UI1(t *testing.T) {
	srv, db, _, _ := setupTestServer(t)
	ctx := context.Background()

	// 1. Unauthenticated request -> 401 Unauthorized
	reqUnauth := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	reqUnauth.Host = "127.0.0.1:7878"
	wUnauth := httptest.NewRecorder()
	srv.Router.ServeHTTP(wUnauth, reqUnauth)
	if wUnauth.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated /api/overview, got %d", wUnauth.Code)
	}

	// 2. Seed data
	p := &store.Project{Name: "overview-proj", RepoPath: "/path/to/overview-proj"}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	jRunning := &store.Job{
		ProjectID: p.ID,
		WorkType:  store.WorkTypeRefactor,
		Title:     "Running Job",
		Stage:     store.StageCoding,
		Status:    store.StatusRunning,
	}
	if err := db.Jobs().CreateJob(ctx, jRunning); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	jClarify := &store.Job{
		ProjectID: p.ID,
		WorkType:  store.WorkTypeFeature,
		Title:     "Clarify Job",
		Stage:     store.StageClarificationAndSpec,
		Status:    store.StatusNeedsClarification,
	}
	if err := db.Jobs().CreateJob(ctx, jClarify); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	// 3. Authenticated request -> 200 OK
	reqAuth := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	reqAuth.Host = "127.0.0.1:7878"
	reqAuth.Header.Set("Authorization", "Bearer test-secret-token")
	wAuth := httptest.NewRecorder()
	srv.Router.ServeHTTP(wAuth, reqAuth)

	if wAuth.Code != http.StatusOK {
		t.Fatalf("expected 200 for authenticated /api/overview, got %d", wAuth.Code)
	}

	var data store.OverviewData
	if err := json.NewDecoder(wAuth.Body).Decode(&data); err != nil {
		t.Fatalf("failed to decode overview response: %v", err)
	}

	// Verify counts (UI-1)
	if data.JobCounts[store.StatusRunning] != 1 {
		t.Errorf("expected 1 running job, got %d", data.JobCounts[store.StatusRunning])
	}
	if data.JobCounts[store.StatusNeedsClarification] != 1 {
		t.Errorf("expected 1 needs_clarification job, got %d", data.JobCounts[store.StatusNeedsClarification])
	}

	// Verify attention list (UI-1)
	if len(data.AttentionList) != 1 {
		t.Fatalf("expected 1 attention item, got %d", len(data.AttentionList))
	}
	if data.AttentionList[0].ID != jClarify.ID {
		t.Errorf("expected attention job ID %d, got %d", jClarify.ID, data.AttentionList[0].ID)
	}
}
