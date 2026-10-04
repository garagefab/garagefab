// Package server_test contains NFR-4 verification for the job list endpoint.
package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/garagefab/garagefab/internal/store"
)

// TestListEndpoints_Cursor_NFR4 verifies that GET /api/jobs is cursor-paginated and returns an
// X-Next-Cursor header (NFR-4).
func TestListEndpoints_Cursor_NFR4(t *testing.T) {
	srv, db, _, _ := setupTestServer(t)
	ctx := context.Background()

	proj := &store.Project{Name: "p", RepoPath: "/tmp/p", BaseRef: "main"}
	if err := db.Projects().CreateProject(ctx, proj); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	for i := 0; i < 5; i++ {
		j := &store.Job{
			ProjectID: proj.ID,
			WorkType:  store.WorkTypeRefactor,
			Title:     fmt.Sprintf("job-%d", i),
			Intent:    "x",
			Stage:     store.StageIntent,
			Status:    store.StatusQueued,
		}
		if err := db.Jobs().CreateJob(ctx, j); err != nil {
			t.Fatalf("CreateJob failed: %v", err)
		}
	}

	list := func(query string) ([]*store.Job, string) {
		req := httptest.NewRequest(http.MethodGet, "/api/jobs"+query, nil)
		req.Host = "127.0.0.1:7878"
		req.Header.Set("Authorization", "Bearer test-secret-token")
		rec := httptest.NewRecorder()
		srv.Router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/jobs%s returned %d", query, rec.Code)
		}
		var jobs []*store.Job
		if err := json.Unmarshal(rec.Body.Bytes(), &jobs); err != nil {
			t.Fatalf("decode jobs: %v", err)
		}
		return jobs, rec.Header().Get("X-Next-Cursor")
	}

	page1, next := list("?limit=2")
	if len(page1) != 2 {
		t.Fatalf("expected 2 jobs on page 1, got %d", len(page1))
	}
	if next == "" {
		t.Fatal("expected an X-Next-Cursor header on a full page")
	}

	page2, _ := list("?limit=2&cursor=" + next)
	if len(page2) != 2 {
		t.Fatalf("expected 2 jobs on page 2, got %d", len(page2))
	}
	if page2[0].ID >= page1[len(page1)-1].ID {
		t.Fatalf("cursor did not advance: page1 last=%d page2 first=%d", page1[len(page1)-1].ID, page2[0].ID)
	}
}
