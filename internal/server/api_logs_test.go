// Package server_test contains integration tests for step log streaming and retrieval.
//
// ==============================================================================
// ARCHITECTURAL ROLE & TESTING CONCEPTS:
// Step Log Streaming & Retrieval Tests (LOG-5, UI-7).
//
// Verifies:
// 1. Unauthenticated requests are rejected with 401.
// 2. Plain text requests return stored log file content and respect offset.
// 3. Accept: text/event-stream streams log events in real-time.
// ==============================================================================
package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/garagefab/garagefab/internal/store"
)

func TestStepLogs_PlainText_And_Offset_LOG5(t *testing.T) {
	srv, db, _, _ := setupTestServer(t)
	ctx := context.Background()

	// 1. Setup project, job, and step run
	p := &store.Project{Name: "p-logs", RepoPath: "/path/to/p-logs"}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	j := &store.Job{ProjectID: p.ID, WorkType: store.WorkTypeRefactor, Title: "Log Test"}
	if err := db.Jobs().CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	// Create a real temporary log file
	logFile := filepath.Join(t.TempDir(), "step_coding_1.log")
	logContent := "Line 1: Compiling source\nLine 2: Running tests\nLine 3: Build succeeded\n"
	if err := os.WriteFile(logFile, []byte(logContent), 0644); err != nil {
		t.Fatalf("write log file failed: %v", err)
	}

	ended := time.Now().UTC()
	step := &store.StepRun{
		JobID:    j.ID,
		Stage:    store.StageCoding,
		Kind:     store.StepKindAgent,
		Executor: "fake",
		Status:   store.StepStatusSuccess,
		LogPath:  logFile,
		EndedAt:  &ended,
	}
	if err := db.StepRuns().CreateStepRun(ctx, step); err != nil {
		t.Fatalf("CreateStepRun failed: %v", err)
	}

	// 2. Unauthenticated request -> 401 Unauthorized
	reqUnauth := httptest.NewRequest(http.MethodGet, "/api/jobs/1/steps/1/log", nil)
	reqUnauth.Host = "127.0.0.1:7878"
	wUnauth := httptest.NewRecorder()
	srv.Router.ServeHTTP(wUnauth, reqUnauth)
	if wUnauth.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated log fetch, got %d", wUnauth.Code)
	}

	// 3. Authenticated request -> 200 OK with full plain text log
	reqAuth := httptest.NewRequest(http.MethodGet, "/api/jobs/1/steps/1/log", nil)
	reqAuth.Host = "127.0.0.1:7878"
	reqAuth.Header.Set("Authorization", "Bearer test-secret-token")
	wAuth := httptest.NewRecorder()
	srv.Router.ServeHTTP(wAuth, reqAuth)

	if wAuth.Code != http.StatusOK {
		t.Fatalf("expected 200 for log fetch, got %d", wAuth.Code)
	}
	if wAuth.Body.String() != logContent {
		t.Errorf("expected log content %q, got %q", logContent, wAuth.Body.String())
	}

	// 4. Authenticated request with offset -> returns content from offset
	reqOffset := httptest.NewRequest(http.MethodGet, "/api/jobs/1/steps/1/log?offset=25", nil)
	reqOffset.Host = "127.0.0.1:7878"
	reqOffset.Header.Set("Authorization", "Bearer test-secret-token")
	wOffset := httptest.NewRecorder()
	srv.Router.ServeHTTP(wOffset, reqOffset)

	if wOffset.Code != http.StatusOK {
		t.Fatalf("expected 200 for offset log fetch, got %d", wOffset.Code)
	}
	if !strings.HasPrefix(wOffset.Body.String(), "Line 2:") {
		t.Errorf("expected offset log starting with 'Line 2:', got: %s", wOffset.Body.String())
	}
}

func TestStepLogs_SSE_Streaming_UI7(t *testing.T) {
	srv, db, _, _ := setupTestServer(t)
	ctx := context.Background()

	p := &store.Project{Name: "p-logs-sse", RepoPath: "/path/to/p-logs-sse"}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	j := &store.Job{ProjectID: p.ID, WorkType: store.WorkTypeRefactor, Title: "Log SSE Test"}
	if err := db.Jobs().CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	logFile := filepath.Join(t.TempDir(), "step_running.log")
	if err := os.WriteFile(logFile, []byte("Line 1: Starting agent\n"), 0644); err != nil {
		t.Fatalf("write log file failed: %v", err)
	}

	step := &store.StepRun{
		JobID:    j.ID,
		Stage:    store.StageCoding,
		Kind:     store.StepKindAgent,
		Executor: "fake",
		Status:   store.StepStatusSuccess, // Completed step sends all lines + end event
		LogPath:  logFile,
	}
	if err := db.StepRuns().CreateStepRun(ctx, step); err != nil {
		t.Fatalf("CreateStepRun failed: %v", err)
	}

	// Request with Accept: text/event-stream
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/1/steps/1/log", nil)
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer test-secret-token")
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for SSE stream, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: log") {
		t.Errorf("expected SSE body to contain 'event: log', got: %s", body)
	}
	if !strings.Contains(body, "Line 1: Starting agent") {
		t.Errorf("expected SSE body to contain log text, got: %s", body)
	}
	if !strings.Contains(body, "event: end") {
		t.Errorf("expected SSE body to contain 'event: end', got: %s", body)
	}
}
