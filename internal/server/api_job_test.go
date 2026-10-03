// Package server_test contains integration tests for the HTTP server, routing, and APIs.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Integration Verification for Job REST API & Handoff Commands (HND-1, HND-2, HND-5, SEC-4).
//
// Tests verify:
// 1. GET /api/jobs/{id} includes a formatted handoff_command when status is eligible (HND-1).
// 2. Stage-to-agent resolution: stage 06 maps to coding agent (HND-2).
// 3. Ineligible statuses (running, done, queued, cancelled) return empty handoff_command (HND-1).
// 4. Regression guard TestApprove_BearerOnly_HND5: Bearer token cannot approve/reject gates (HND-5, SEC-4).
// ==============================================================================
package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/server"
	"github.com/garagefab/garagefab/internal/store"
)

func loginAndGetCookie(t *testing.T, srv *server.Server) *http.Cookie {
	t.Helper()
	reqSess := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader("token=test-secret-token"))
	reqSess.Host = "127.0.0.1:7878"
	reqSess.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wSess := httptest.NewRecorder()
	srv.Router.ServeHTTP(wSess, reqSess)
	if wSess.Code != http.StatusOK {
		t.Fatalf("expected 200 for create session, got %d", wSess.Code)
	}

	for _, c := range wSess.Result().Cookies() {
		if c.Name == "gf_session" {
			return c
		}
	}
	t.Fatal("expected gf_session cookie")
	return nil
}

// TestGetJob_HandoffCommand_HND1_HND2 tests requirements HND-1 and HND-2:
// - Eligible statuses (needs_clarification, awaiting_approval, etc.) include handoff_command.
// - Role/stage resolution resolves the appropriate agent from project.yaml.
// - Ineligible statuses return an empty handoff_command.
func TestGetJob_HandoffCommand_HND1_HND2(t *testing.T) {
	srv, db, _, _ := setupTestServer(t)

	// Set up temporary git project with .garagefab/project.yaml
	projectDir := filepath.Join(t.TempDir(), "my app")
	if err := os.MkdirAll(filepath.Join(projectDir, ".garagefab"), 0755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	projectYAML := `base_ref: origin/main
agents:
  spec: agy
  coding: opencode
  review: agy
`
	if err := os.WriteFile(filepath.Join(projectDir, ".garagefab", "project.yaml"), []byte(projectYAML), 0644); err != nil {
		t.Fatalf("failed to write project.yaml: %v", err)
	}

	ctx := context.Background()
	project := &store.Project{
		Name:             "test-project",
		RepoPath:         projectDir,
		BaseRef:          "origin/main",
		EnabledWorkTypes: []string{"feature"},
	}
	if err := db.Projects().CreateProject(ctx, project); err != nil {
		t.Fatalf("create project failed: %v", err)
	}

	// 1. Job in needs_clarification (stage 02 -> spec -> agy)
	job1 := &store.Job{
		ProjectID: project.ID,
		WorkType:  "feature",
		Title:     "Feature 1",
		Intent:    "Intent 1",
		Stage:     "02_Clarification_and_Spec",
		Status:    "needs_clarification",
		Source:    "dashboard",
	}
	if err := db.Jobs().CreateJob(ctx, job1); err != nil {
		t.Fatalf("create job1 failed: %v", err)
	}

	// 2. Job in awaiting_approval (stage 06 -> coding -> opencode)
	job2 := &store.Job{
		ProjectID: project.ID,
		WorkType:  "feature",
		Title:     "Feature 2",
		Intent:    "Intent 2",
		Stage:     "06_Human_Approval_Gate",
		Status:    "awaiting_approval",
		Source:    "dashboard",
	}
	if err := db.Jobs().CreateJob(ctx, job2); err != nil {
		t.Fatalf("create job2 failed: %v", err)
	}

	// 3. Job in running (ineligible -> empty handoff_command)
	job3 := &store.Job{
		ProjectID: project.ID,
		WorkType:  "feature",
		Title:     "Feature 3",
		Intent:    "Intent 3",
		Stage:     "04_Coding",
		Status:    "running",
		Source:    "dashboard",
	}
	if err := db.Jobs().CreateJob(ctx, job3); err != nil {
		t.Fatalf("create job3 failed: %v", err)
	}

	cookie := loginAndGetCookie(t, srv)

	fetchJob := func(jobID int64) map[string]interface{} {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/jobs/%d", jobID), nil)
		req.Host = "127.0.0.1:7878"
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		srv.Router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		var res map[string]interface{}
		body, _ := io.ReadAll(w.Body)
		if err := json.Unmarshal(body, &res); err != nil {
			t.Fatalf("failed to decode json: %v", err)
		}
		return res
	}

	// Check Job 1: eligible, stage 02 -> agy, quoted path with space
	res1 := fetchJob(job1.ID)
	cmd1, ok := res1["handoff_command"].(string)
	if !ok || cmd1 == "" {
		t.Fatalf("expected non-empty handoff_command for job1, got: %v", res1["handoff_command"])
	}
	expectedCmd1 := fmt.Sprintf("cd '%s' ; agy garagefab-work %d", projectDir, job1.ID)
	if cmd1 != expectedCmd1 {
		t.Errorf("job1 handoff_command mismatch.\nExpected: %s\nGot:      %s", expectedCmd1, cmd1)
	}

	// Check Job 2: eligible, stage 06 -> opencode
	res2 := fetchJob(job2.ID)
	cmd2, ok := res2["handoff_command"].(string)
	if !ok || cmd2 == "" {
		t.Fatalf("expected non-empty handoff_command for job2, got: %v", res2["handoff_command"])
	}
	expectedCmd2 := fmt.Sprintf("cd '%s' ; opencode garagefab-work %d", projectDir, job2.ID)
	if cmd2 != expectedCmd2 {
		t.Errorf("job2 handoff_command mismatch.\nExpected: %s\nGot:      %s", expectedCmd2, cmd2)
	}

	// Check Job 3: running -> empty handoff_command
	res3 := fetchJob(job3.ID)
	cmd3, _ := res3["handoff_command"].(string)
	if cmd3 != "" {
		t.Errorf("expected empty handoff_command for running job3, got: %q", cmd3)
	}
}

// TestApprove_BearerOnly_HND5 tests requirements HND-5 and SEC-4:
// Calling POST /api/jobs/{id}/approve or /reject with only Authorization: Bearer token yields 403 Forbidden.
func TestApprove_BearerOnly_HND5(t *testing.T) {
	srv, db, _, _ := setupTestServer(t)
	ctx := context.Background()

	project := &store.Project{
		Name:             "approval-project",
		RepoPath:         t.TempDir(),
		BaseRef:          "origin/main",
		EnabledWorkTypes: []string{"feature"},
	}
	if err := db.Projects().CreateProject(ctx, project); err != nil {
		t.Fatalf("create project failed: %v", err)
	}

	job := &store.Job{
		ProjectID: project.ID,
		WorkType:  "feature",
		Title:     "Feature Approval Test",
		Intent:    "Test bearer restriction",
		Stage:     "06_Human_Approval_Gate",
		Status:    "awaiting_approval",
		Source:    "dashboard",
	}
	if err := db.Jobs().CreateJob(ctx, job); err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	// 1. POST /api/jobs/{id}/approve with Bearer token only
	approveReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/jobs/%d/approve", job.ID), strings.NewReader(`{"head_sha":"abc123"}`))
	approveReq.Host = "127.0.0.1:7878"
	approveReq.Header.Set("Authorization", "Bearer test-secret-token")
	approveReq.Header.Set("Content-Type", "application/json")
	wApprove := httptest.NewRecorder()
	srv.Router.ServeHTTP(wApprove, approveReq)

	if wApprove.Code != http.StatusForbidden {
		t.Errorf("SEC-4 / HND-5 violated: expected 403 Forbidden for Bearer token on approve, got %d", wApprove.Code)
	}

	// 2. POST /api/jobs/{id}/reject with Bearer token only
	rejectReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/jobs/%d/reject", job.ID), strings.NewReader(`{"note":"Please fix typo"}`))
	rejectReq.Host = "127.0.0.1:7878"
	rejectReq.Header.Set("Authorization", "Bearer test-secret-token")
	rejectReq.Header.Set("Content-Type", "application/json")
	wReject := httptest.NewRecorder()
	srv.Router.ServeHTTP(wReject, rejectReq)

	if wReject.Code != http.StatusForbidden {
		t.Errorf("SEC-4 / HND-5 violated: expected 403 Forbidden for Bearer token on reject, got %d", wReject.Code)
	}
}
