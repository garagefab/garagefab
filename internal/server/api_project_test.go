// Package server_test contains integration tests for the project management REST APIs.
//
// ==============================================================================
// ARCHITECTURAL ROLE & TESTING CONCEPTS:
// Project Registration, Validation, Template Generation & Archiving Tests (PRJ-1..8).
//
// Verifies:
// 1. Missing project.yaml returns 422 with template (PRJ-3).
// 2. Generating template writes .garagefab/project.yaml (PRJ-7).
// 3. Registering valid repository succeeds (PRJ-1..5).
// 4. Archiving refused when active jobs exist; succeeds when idle (PRJ-8).
// ==============================================================================
package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/server"
	"github.com/garagefab/garagefab/internal/store"
)

func TestProjectRegistration_ValidationAndTemplate_PRJ1_PRJ3_PRJ7(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	// Create a temporary git repo folder
	repoDir := t.TempDir()
	gitDir := filepath.Join(repoDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("mkdir .git failed: %v", err)
	}

	// 1. Register without project.yaml -> 422 Unprocessable Entity with template (PRJ-3)
	bodyMissing := fmtJSON(map[string]any{
		"repo_path": repoDir,
	})
	reqMissing := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(bodyMissing))
	reqMissing.Host = "127.0.0.1:7878"
	reqMissing.Header.Set("Authorization", "Bearer test-secret-token")
	wMissing := httptest.NewRecorder()
	srv.Router.ServeHTTP(wMissing, reqMissing)

	if wMissing.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for missing project.yaml, got %d", wMissing.Code)
	}

	var resMissing struct {
		Template string `json:"template"`
		Error    struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(wMissing.Body).Decode(&resMissing); err != nil {
		t.Fatalf("decode 422 response failed: %v", err)
	}
	if !strings.Contains(resMissing.Template, "base_ref: origin/main") {
		t.Errorf("expected template in 422 response, got %s", resMissing.Template)
	}

	// 2. Generate config-template via POST /api/projects/config-template (PRJ-7)
	bodyTmpl := fmtJSON(map[string]any{
		"repo_path": repoDir,
	})
	reqTmpl := httptest.NewRequest(http.MethodPost, "/api/projects/config-template", strings.NewReader(bodyTmpl))
	reqTmpl.Host = "127.0.0.1:7878"
	reqTmpl.Header.Set("Authorization", "Bearer test-secret-token")
	wTmpl := httptest.NewRecorder()
	srv.Router.ServeHTTP(wTmpl, reqTmpl)

	if wTmpl.Code != http.StatusOK {
		t.Fatalf("expected 200 for config-template, got %d", wTmpl.Code)
	}

	// Check file was written to disk
	expectedFile := filepath.Join(repoDir, ".garagefab", "project.yaml")
	if _, err := os.Stat(expectedFile); err != nil {
		t.Fatalf("expected project.yaml to be created at %s: %v", expectedFile, err)
	}

	// 3. Re-submit registration now that template is present -> 201 Created (PRJ-1, PRJ-2)
	reqValid := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(bodyMissing))
	reqValid.Host = "127.0.0.1:7878"
	reqValid.Header.Set("Authorization", "Bearer test-secret-token")
	wValid := httptest.NewRecorder()
	srv.Router.ServeHTTP(wValid, reqValid)

	if wValid.Code != http.StatusCreated {
		t.Fatalf("expected 201 for valid project registration, got %d (body: %s)", wValid.Code, wValid.Body.String())
	}

	var p store.Project
	if err := json.NewDecoder(wValid.Body).Decode(&p); err != nil {
		t.Fatalf("decode created project failed: %v", err)
	}
	if p.ID == 0 || p.RepoPath != repoDir {
		t.Errorf("unexpected project: %+v", p)
	}
}

func TestProjectArchive_RefusesActiveJobs_PRJ8(t *testing.T) {
	srv, db, _, _ := setupTestServer(t)
	ctx := context.Background()

	// 1. Create session cookie (since archive requires session cookie)
	sessionCookie := createTestSessionCookie(t, srv)

	// 2. Setup project with an active job
	p := &store.Project{Name: "archive-test", RepoPath: "/path/to/archive-test"}
	if err := db.Projects().CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	j := &store.Job{
		ProjectID: p.ID,
		WorkType:  store.WorkTypeFeature,
		Title:     "Active job",
		Stage:     store.StageCoding,
		Status:    store.StatusRunning,
	}
	if err := db.Jobs().CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	// 3. Attempt to archive -> 409 Conflict (PRJ-8)
	reqArchive := httptest.NewRequest(http.MethodPost, "/api/projects/1/archive", nil)
	reqArchive.Host = "127.0.0.1:7878"
	reqArchive.AddCookie(sessionCookie)
	wArchive := httptest.NewRecorder()
	srv.Router.ServeHTTP(wArchive, reqArchive)

	if wArchive.Code != http.StatusConflict {
		t.Fatalf("expected 409 for archiving project with active job, got %d", wArchive.Code)
	}

	// 4. Mark job done -> Archiving now succeeds (PRJ-8)
	if err := db.Jobs().UpdateJobState(ctx, j.ID, store.StageDone, store.StatusDone); err != nil {
		t.Fatalf("UpdateJobState failed: %v", err)
	}

	wArchiveSuccess := httptest.NewRecorder()
	srv.Router.ServeHTTP(wArchiveSuccess, reqArchive)

	if wArchiveSuccess.Code != http.StatusOK {
		t.Fatalf("expected 200 for archiving idle project, got %d", wArchiveSuccess.Code)
	}

	// 5. Verify project is archived in DB
	archivedP, err := db.Projects().GetProject(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if !archivedP.IsArchived {
		t.Error("expected project.IsArchived to be true")
	}
}

// Helpers
func fmtJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func createTestSessionCookie(t *testing.T, srv *server.Server) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(`{"token":"test-secret-token"}`))
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("failed to create session cookie: code %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "gf_session" {
			return c
		}
	}
	t.Fatal("session cookie gf_session not found in response")
	return nil
}
