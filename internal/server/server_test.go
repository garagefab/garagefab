// Package server_test contains integration tests for the HTTP server, routing, and APIs.
//
// ==============================================================================
// GO TESTING CONCEPTS & HTTP INTEGRATION TESTING:
//
//  1. `httptest.NewRequest` and `httptest.NewRecorder`:
//     Equivalent to Spring's `MockMvc` or `TestRestTemplate`.
//     It executes the full Chi router and middleware chain completely in-memory
//     without binding a physical TCP socket, making test execution virtually instantaneous.
//
//  2. Testing Virtual Filesystems (`testing/fstest.MapFS`):
//     Go standard library provides `fstest.MapFS` to mock `io/fs.FS` filesystems
//     in unit tests without touching the real disk.
//
//  3. Testing Real-Time SSE Streams:
//     Using a cancellable context `context.WithCancel(context.Background())` to run
//     the SSE stream in a background goroutine, inspect the written buffer, and cleanly
//     cancel the context to terminate the streaming loop.
//
// ==============================================================================
package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/server"
	"github.com/garagefab/garagefab/internal/store"
)

type mockEngine struct {
	approvedID           int64
	approvedSHA          string
	rejectedID           int64
	rejectedNote         string
	clarificationID      int64
	clarificationAnswers []factory.ClarificationAnswer
	clarificationErr     error
}

func (m *mockEngine) Approve(ctx context.Context, jobID int64, headSHA string) error {
	m.approvedID = jobID
	m.approvedSHA = headSHA
	return nil
}

func (m *mockEngine) Reject(ctx context.Context, jobID int64, note string) error {
	m.rejectedID = jobID
	m.rejectedNote = note
	return nil
}

func (m *mockEngine) Cancel(ctx context.Context, jobID int64) error {
	return nil
}

func (m *mockEngine) Retry(ctx context.Context, jobID int64) error {
	return nil
}

func (m *mockEngine) SubmitClarification(ctx context.Context, jobID int64, answers []factory.ClarificationAnswer) error {
	m.clarificationID = jobID
	m.clarificationAnswers = answers
	return m.clarificationErr
}

func (m *mockEngine) GetEvidence(ctx context.Context, jobID int64) (*factory.EvidenceSummary, error) {
	return &factory.EvidenceSummary{
		JobID:          jobID,
		HeadSHA:        "head-evidence-123",
		ReviewDecision: "approve",
	}, nil
}

func (m *mockEngine) GetArtifact(ctx context.Context, jobID int64, name string) ([]byte, error) {
	if name == "spec" || name == "spec.md" {
		return []byte("# Spec Content"), nil
	}
	if name == "review" || name == "review.json" {
		return []byte(`{"decision":"approve"}`), nil
	}
	return []byte("artifact content"), nil
}

func (m *mockEngine) GetDiff(ctx context.Context, jobID int64) (string, error) {
	return "diff --git a/main.go b/main.go", nil
}

type mockScheduler struct {
	wakeCount int
}

func (m *mockScheduler) Wake() {
	m.wakeCount++
}

func setupTestServer(t *testing.T) (*server.Server, *store.DB, *mockEngine, *mockScheduler) {
	t.Helper()
	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:7878"
	cfg.Server.APIToken = "test-secret-token"

	dbPath := filepath.Join(t.TempDir(), "server_test.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	engine := &mockEngine{}
	scheduler := &mockScheduler{}

	srv := server.NewServer(cfg, db, engine, scheduler, nil)
	return srv, db, engine, scheduler
}

// TestHealthEndpoint_CLI1 verifies requirement CLI-1:
// GET /api/health returns status 200 and JSON {"status":"ok"}.
func TestHealthEndpoint_CLI1(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:7878"

	srv := server.NewServer(cfg, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host = "127.0.0.1:7878"
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	var data map[string]string
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}

	if data["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", data["status"])
	}
}

// TestHostHeaderCheck_SEC2 verifies requirement SEC-2:
// DNS rebinding protection rejects external Host headers with 403 Forbidden.
func TestHostHeaderCheck_SEC2(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:7878"

	srv := server.NewServer(cfg, nil, nil, nil, nil)

	// Disallowed external Host header
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host = "attacker.example.com"
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for external host, got %d", resp.StatusCode)
	}

	// Allowed localhost Host header
	req2 := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req2.Host = "localhost:7878"
	w2 := httptest.NewRecorder()

	srv.Router.ServeHTTP(w2, req2)
	resp2 := w2.Result()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for localhost, got %d", resp2.StatusCode)
	}
}

// TestSPAFallback verifies Single Page Application fallback routing:
// Non-existent routes serve index.html, assets are served directly, and unmapped API paths 404.
func TestSPAFallback(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:7878"

	// Mock embedded frontend virtual filesystem using fstest.MapFS
	mockFS := fstest.MapFS{
		"index.html":    {Data: []byte("<html><body>Dashboard</body></html>")},
		"assets/app.js": {Data: []byte("console.log('app')")},
	}

	srv := server.NewServer(cfg, nil, nil, nil, mockFS)

	// Root path (/) should serve index.html
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.Host = "127.0.0.1:7878"
	w1 := httptest.NewRecorder()
	srv.Router.ServeHTTP(w1, req1)
	if body := w1.Body.String(); body != "<html><body>Dashboard</body></html>" {
		t.Errorf("expected index.html, got %q", body)
	}

	// Unknown non-api route should fall back to index.html for React Router
	req2 := httptest.NewRequest(http.MethodGet, "/projects/1/jobs/2", nil)
	req2.Host = "127.0.0.1:7878"
	w2 := httptest.NewRecorder()
	srv.Router.ServeHTTP(w2, req2)
	if body := w2.Body.String(); body != "<html><body>Dashboard</body></html>" {
		t.Errorf("expected SPA fallback to index.html, got %q", body)
	}

	// Existing static asset (/assets/app.js)
	req3 := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	req3.Host = "127.0.0.1:7878"
	w3 := httptest.NewRecorder()
	srv.Router.ServeHTTP(w3, req3)
	if body := w3.Body.String(); body != "console.log('app')" {
		t.Errorf("expected asset app.js, got %q", body)
	}

	// Unknown API route must return 404, not SPA fallback
	req4 := httptest.NewRequest(http.MethodGet, "/api/unknown", nil)
	req4.Host = "127.0.0.1:7878"
	w4 := httptest.NewRecorder()
	srv.Router.ServeHTTP(w4, req4)
	if w4.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown api route, got %d", w4.Code)
	}
}

// TestAuth_Bearer_And_Session_SEC3 tests requirement SEC-3:
// Supports both Bearer token header and gf_session cookie authentication.
func TestAuth_Bearer_And_Session_SEC3(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	// 1. Unauthenticated request -> 401 Unauthorized
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.Host = "127.0.0.1:7878"
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated request, got %d", w.Code)
	}

	// 2. Bearer token auth -> 200 OK
	reqBearer := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	reqBearer.Host = "127.0.0.1:7878"
	reqBearer.Header.Set("Authorization", "Bearer test-secret-token")
	wBearer := httptest.NewRecorder()
	srv.Router.ServeHTTP(wBearer, reqBearer)
	if wBearer.Code != http.StatusOK {
		t.Errorf("expected 200 for bearer token auth, got %d", wBearer.Code)
	}

	// 3. Create session via POST /api/session (SEC-3)
	reqSess := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader("token=test-secret-token"))
	reqSess.Host = "127.0.0.1:7878"
	reqSess.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wSess := httptest.NewRecorder()
	srv.Router.ServeHTTP(wSess, reqSess)
	if wSess.Code != http.StatusOK {
		t.Fatalf("expected 200 for create session, got %d", wSess.Code)
	}

	cookies := wSess.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "gf_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected gf_session cookie to be set")
	}

	// 4. Request with Session cookie -> 200 OK
	reqCookie := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	reqCookie.Host = "127.0.0.1:7878"
	reqCookie.AddCookie(sessionCookie)
	wCookie := httptest.NewRecorder()
	srv.Router.ServeHTTP(wCookie, reqCookie)
	if wCookie.Code != http.StatusOK {
		t.Errorf("expected 200 for session cookie auth, got %d", wCookie.Code)
	}

	// 5. Create session via JSON payload POST /api/session (SEC-3)
	reqJSON := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(`{"token":"test-secret-token"}`))
	reqJSON.Host = "127.0.0.1:7878"
	reqJSON.Header.Set("Content-Type", "application/json")
	wJSON := httptest.NewRecorder()
	srv.Router.ServeHTTP(wJSON, reqJSON)
	if wJSON.Code != http.StatusOK {
		t.Fatalf("expected 200 for JSON create session, got %d", wJSON.Code)
	}
}

// TestApproveReject_SessionOnly_SEC4_APR7 verifies requirements SEC-4 and APR-7:
// Human approval and rejection actions strictly require a browser session cookie.
func TestApproveReject_SessionOnly_SEC4_APR7(t *testing.T) {
	srv, _, engine, _ := setupTestServer(t)

	// Create session cookie
	reqSess := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader("token=test-secret-token"))
	reqSess.Host = "127.0.0.1:7878"
	reqSess.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wSess := httptest.NewRecorder()
	srv.Router.ServeHTTP(wSess, reqSess)
	cookie := wSess.Result().Cookies()[0]

	// 1. Approve with Bearer token MUST be forbidden (SEC-4, APR-7)
	reqBearerApprove := httptest.NewRequest(http.MethodPost, "/api/jobs/1/approve", strings.NewReader(`{"head_sha":"abc"}`))
	reqBearerApprove.Host = "127.0.0.1:7878"
	reqBearerApprove.Header.Set("Authorization", "Bearer test-secret-token")
	reqBearerApprove.Header.Set("Content-Type", "application/json")
	wBearerApprove := httptest.NewRecorder()
	srv.Router.ServeHTTP(wBearerApprove, reqBearerApprove)
	if wBearerApprove.Code != http.StatusForbidden {
		t.Errorf("SEC-4 violated: expected 403 Forbidden for Bearer token on Approve, got %d", wBearerApprove.Code)
	}

	// 2. Reject with Bearer token MUST be forbidden (SEC-4, APR-7)
	reqBearerReject := httptest.NewRequest(http.MethodPost, "/api/jobs/1/reject", strings.NewReader(`{"note":"reject"}`))
	reqBearerReject.Host = "127.0.0.1:7878"
	reqBearerReject.Header.Set("Authorization", "Bearer test-secret-token")
	reqBearerReject.Header.Set("Content-Type", "application/json")
	wBearerReject := httptest.NewRecorder()
	srv.Router.ServeHTTP(wBearerReject, reqBearerReject)
	if wBearerReject.Code != http.StatusForbidden {
		t.Errorf("SEC-4 violated: expected 403 Forbidden for Bearer token on Reject, got %d", wBearerReject.Code)
	}

	// 3. Reject with Session cookie but empty note MUST return 422 (APR-6)
	reqEmptyNote := httptest.NewRequest(http.MethodPost, "/api/jobs/1/reject", strings.NewReader(`{"note":""}`))
	reqEmptyNote.Host = "127.0.0.1:7878"
	reqEmptyNote.AddCookie(cookie)
	reqEmptyNote.Header.Set("Content-Type", "application/json")
	wEmptyNote := httptest.NewRecorder()
	srv.Router.ServeHTTP(wEmptyNote, reqEmptyNote)
	if wEmptyNote.Code != http.StatusUnprocessableEntity {
		t.Errorf("APR-6 violated: expected 422 for empty rejection note, got %d", wEmptyNote.Code)
	}

	// 4. Reject with Session cookie and valid note -> 200 OK
	reqReject := httptest.NewRequest(http.MethodPost, "/api/jobs/1/reject", strings.NewReader(`{"note":"Need fix"}`))
	reqReject.Host = "127.0.0.1:7878"
	reqReject.AddCookie(cookie)
	reqReject.Header.Set("Content-Type", "application/json")
	wReject := httptest.NewRecorder()
	srv.Router.ServeHTTP(wReject, reqReject)
	if wReject.Code != http.StatusOK {
		t.Errorf("expected 200 for valid rejection, got %d", wReject.Code)
	}
	if engine.rejectedNote != "Need fix" {
		t.Errorf("expected engine to receive rejection note, got %q", engine.rejectedNote)
	}

	// 5. Approve with Session cookie -> 200 OK
	reqApprove := httptest.NewRequest(http.MethodPost, "/api/jobs/1/approve", strings.NewReader(`{"head_sha":"sha-valid"}`))
	reqApprove.Host = "127.0.0.1:7878"
	reqApprove.AddCookie(cookie)
	reqApprove.Header.Set("Content-Type", "application/json")
	wApprove := httptest.NewRecorder()
	srv.Router.ServeHTTP(wApprove, reqApprove)
	if wApprove.Code != http.StatusOK {
		t.Errorf("expected 200 for valid approval, got %d", wApprove.Code)
	}
	if engine.approvedSHA != "sha-valid" {
		t.Errorf("expected engine to receive head_sha, got %q", engine.approvedSHA)
	}
}

// TestProjectAndJobAPIs_PRJ1_INT1 tests requirements PRJ-1..5 and INT-1:
// Creating projects and creating jobs via REST APIs.
func TestProjectAndJobAPIs_PRJ1_INT1(t *testing.T) {
	srv, _, _, scheduler := setupTestServer(t)

	// Create temporary git repo on disk for PRJ-2 validation
	repoDir := filepath.Join(t.TempDir(), "git-proj")
	_ = os.MkdirAll(repoDir, 0700)
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		_ = cmd.Run()
	}
	runGit("init")

	// 1. Create project (PRJ-1..5)
	projBody := fmt.Sprintf(`{"name":"test-api-proj","repo_path":%q,"base_ref":"origin/main"}`, repoDir)
	reqProj := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(projBody))
	reqProj.Host = "127.0.0.1:7878"
	reqProj.Header.Set("Authorization", "Bearer test-secret-token")
	reqProj.Header.Set("Content-Type", "application/json")
	wProj := httptest.NewRecorder()
	srv.Router.ServeHTTP(wProj, reqProj)

	if wProj.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for project, got %d: %s", wProj.Code, wProj.Body.String())
	}

	var projResp map[string]any
	_ = json.Unmarshal(wProj.Body.Bytes(), &projResp)
	projID := int64(projResp["id"].(float64))

	// 2. Create job (INT-1)
	jobBody := fmt.Sprintf(`{"project_id":%d,"work_type":"refactor","title":"API Test Job","intent":"Simplify code"}`, projID)
	reqJob := httptest.NewRequest(http.MethodPost, "/api/jobs", strings.NewReader(jobBody))
	reqJob.Host = "127.0.0.1:7878"
	reqJob.Header.Set("Authorization", "Bearer test-secret-token")
	reqJob.Header.Set("Content-Type", "application/json")
	wJob := httptest.NewRecorder()
	srv.Router.ServeHTTP(wJob, reqJob)

	if wJob.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for job, got %d: %s", wJob.Code, wJob.Body.String())
	}

	if scheduler.wakeCount == 0 {
		t.Errorf("expected scheduler.Wake to be called on job creation")
	}

	// 3. List jobs
	reqList := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	reqList.Host = "127.0.0.1:7878"
	reqList.Header.Set("Authorization", "Bearer test-secret-token")
	wList := httptest.NewRecorder()
	srv.Router.ServeHTTP(wList, reqList)

	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for list jobs, got %d", wList.Code)
	}

	var jobs []map[string]any
	_ = json.Unmarshal(wList.Body.Bytes(), &jobs)
	if len(jobs) != 1 {
		t.Errorf("expected 1 job in list, got %d", len(jobs))
	}
}

// TestSSE_Events_LOG4 tests requirement LOG-4:
// Real-time Server-Sent Events (SSE) streaming and event emission.
func TestSSE_Events_LOG4(t *testing.T) {
	srv, db, _, _ := setupTestServer(t)

	// Pre-seed an event in the database
	p := &store.Project{Name: "p-sse", RepoPath: "/tmp/p-sse"}
	_ = db.Projects().CreateProject(context.Background(), p)
	j := &store.Job{ProjectID: p.ID, WorkType: "refactor", Title: "SSE Job"}
	_ = db.Jobs().CreateJob(context.Background(), j)
	_ = db.Events().CreateEvent(context.Background(), &store.Event{
		JobID:   j.ID,
		Type:    "job.status_changed",
		Payload: `{"status":"running"}`,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer test-secret-token")

	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Router.ServeHTTP(w, req)
	}()

	// Wait briefly for handler to flush initial events, then cancel client request
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	respBody := w.Body.String()
	if !strings.Contains(respBody, "event: job.status_changed") {
		t.Errorf("LOG-4 violated: expected SSE stream to contain job.status_changed event, got:\n%s", respBody)
	}
	if !strings.Contains(respBody, "data: {\"status\":\"running\"}") {
		t.Errorf("LOG-4 violated: expected SSE stream to contain payload, got:\n%s", respBody)
	}
}

// TestClarificationAPI_SPC3 verifies requirement SPC-3:
// Answers submitted via POST /api/jobs/{id}/clarification are validated and passed to the engine.
func TestClarificationAPI_SPC3(t *testing.T) {
	srv, _, engine, scheduler := setupTestServer(t)

	postClarification := func(token string, jobID int64, body any) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/jobs/%d/clarification", jobID), &buf)
		req.Host = "127.0.0.1:7878"
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		srv.Router.ServeHTTP(w, req)
		return w
	}

	// 1. Unauthenticated request -> 401 Unauthorized
	w := postClarification("", 10, map[string]any{"answers": []map[string]any{{"q": 1, "answer": "Yes"}}})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized without auth, got %d", w.Code)
	}

	// 2. Empty answers -> 422 Unprocessable Entity
	w = postClarification("test-secret-token", 10, map[string]any{"answers": []map[string]any{}})
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for empty answers array, got %d", w.Code)
	}

	// 3. Invalid answer (empty text) -> 422 Unprocessable Entity
	w = postClarification("test-secret-token", 10, map[string]any{"answers": []map[string]any{{"q": 1, "answer": "   "}}})
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for blank answer text, got %d", w.Code)
	}

	// 4. Invalid state from engine -> 409 Conflict
	engine.clarificationErr = factory.ErrInvalidState
	w = postClarification("test-secret-token", 10, map[string]any{
		"answers": []map[string]any{{"q": 1, "answer": "Use SQLite"}},
	})
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict when job not awaiting clarification, got %d", w.Code)
	}

	// 5. Successful submission via Bearer Token (SPC-3)
	engine.clarificationErr = nil
	w = postClarification("test-secret-token", 10, map[string]any{
		"answers": []map[string]any{
			{"q": 1, "answer": "Use SQLite"},
			{"q": 2, "answer": "Timeout is 30s"},
		},
	})
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK for valid answers, got %d", w.Code)
	}
	if engine.clarificationID != 10 {
		t.Errorf("expected engine.clarificationID = 10, got %d", engine.clarificationID)
	}
	if len(engine.clarificationAnswers) != 2 {
		t.Errorf("expected 2 answers passed to engine, got %d", len(engine.clarificationAnswers))
	}
	if scheduler.wakeCount == 0 {
		t.Errorf("expected scheduler.Wake to be invoked after clarification submission")
	}
}

// TestEvidenceAndArtifactAPIs_APR1_APR3_LOG3 tests requirement APR-1, APR-2, APR-3, and LOG-3:
// - GET /api/jobs/{id}/evidence serves structured JSON summary (APR-1, APR-3)
// - GET /api/jobs/{id}/artifacts/{name} serves raw artifacts (LOG-3)
// - GET /api/jobs/{id}/diff serves diff output (APR-2)
func TestEvidenceAndArtifactAPIs_APR1_APR3_LOG3(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	// 1. GET /api/jobs/1/evidence with Bearer token
	req := httptest.NewRequest("GET", "/api/jobs/1/evidence", nil)
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer test-secret-token")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /evidence, got %d: %s", w.Code, w.Body.String())
	}
	var evidence factory.EvidenceSummary
	if err := json.Unmarshal(w.Body.Bytes(), &evidence); err != nil {
		t.Fatalf("failed to decode evidence JSON: %v", err)
	}
	if evidence.JobID != 1 || evidence.HeadSHA != "head-evidence-123" {
		t.Errorf("unexpected evidence payload: %+v", evidence)
	}

	// 2. GET /api/jobs/1/artifacts/spec with Bearer token
	req = httptest.NewRequest("GET", "/api/jobs/1/artifacts/spec", nil)
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer test-secret-token")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /artifacts/spec, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "# Spec Content") {
		t.Errorf("expected spec artifact content, got: %s", w.Body.String())
	}
	if w.Header().Get("Content-Type") != "text/markdown; charset=utf-8" {
		t.Errorf("expected text/markdown Content-Type, got: %s", w.Header().Get("Content-Type"))
	}

	// 3. GET /api/jobs/1/artifacts/review (JSON content type)
	req = httptest.NewRequest("GET", "/api/jobs/1/artifacts/review", nil)
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer test-secret-token")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /artifacts/review, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Errorf("expected application/json Content-Type, got: %s", w.Header().Get("Content-Type"))
	}

	// 4. GET /api/jobs/1/diff
	req = httptest.NewRequest("GET", "/api/jobs/1/diff", nil)
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer test-secret-token")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /diff, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "diff --git") {
		t.Errorf("expected git diff text, got: %s", w.Body.String())
	}
}
