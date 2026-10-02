// Package main contains full end-to-end integration tests for Garagefab.
//
// ==============================================================================
// GO TESTING CONCEPTS & ARCHITECTURAL PATTERNS:
//
//  1. `httptest` (In-Memory HTTP Testing):
//     `net/http/httptest` is Go's standard package for testing HTTP handlers.
//     - `httptest.NewRequest`: Creates an in-memory `http.Request`.
//     - `httptest.NewRecorder`: Captures the response headers, status code, and body.
//     - `srv.Router.ServeHTTP(w, req)`: Directly invokes the HTTP router without
//     binding a real TCP port. This is fast, deterministic, and doesn't collide with
//     other processes. (Equivalent to Spring's `MockMvc`).
//
// 2. Goroutines & Context Cancellation:
//
//   - `go scheduler.Start(schedulerCtx)`: Spawns the scheduler loop on a lightweight
//     green thread (goroutine).
//
//   - `context.WithCancel`: Provides a cooperative cancellation mechanism. Calling
//     `cancelScheduler()` signals the goroutine to exit cleanly when the test ends.
//
//     3. Test Fixture Helper (`t.Helper()`):
//     Calling `t.Helper()` tells the Go test runner to ignore this function in stack
//     traces. When an error occurs inside `createTestRepo`, Go reports the line number
//     of the caller in `TestM1_WalkingSkeleton_E2E`, making failures much easier to debug.
//
// ==============================================================================
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/server"
	"github.com/garagefab/garagefab/internal/store"
	"github.com/garagefab/garagefab/internal/worker/agent"
	"github.com/garagefab/garagefab/internal/worker/command"
	"github.com/garagefab/garagefab/internal/worker/worktree"
)

// createTestRepo initializes a real Git repository in a temporary directory.
// This represents the user's local code repository that Garagefab will manage.
func createTestRepo(t *testing.T) string {
	t.Helper() // Marks this function as a test helper for accurate stack trace reporting

	repoDir := filepath.Join(t.TempDir(), "e2e-repo")
	if err := os.MkdirAll(repoDir, 0700); err != nil {
		t.Fatalf("mkdir repoDir failed: %v", err)
	}

	// Helper to execute git commands inside the temporary repository
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %s: %v", args, string(out), err)
		}
	}

	// Initialize git repository with main branch and dummy commit
	runGit("init", "-b", "main")
	runGit("config", "user.name", "Garagefab Test")
	runGit("config", "user.email", "test@garagefab.local")

	readme := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readme, []byte("# E2E Repo\n"), 0600); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "initial commit")

	return repoDir
}

// TestM1_WalkingSkeleton_E2E validates the complete Milestone 1 exit criteria:
// 1. Registers a temporary Git repository via POST /api/projects
// 2. Creates a refactor job via POST /api/jobs
// 3. Scheduler admits job, executes coding & review with FakeAgent
// 4. Verifies logs, events, and checkpoint commit are present
// 5. Verifies job reaches 06_Human_Approval_Gate / awaiting_approval
// 6. Approves job with matching head SHA via POST /api/jobs/{id}/approve
// 7. Verifies job reaches 07_Done / done and worktree is cleaned up
func TestM1_WalkingSkeleton_E2E(t *testing.T) {
	tempDataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = tempDataDir
	cfg.Server.APIToken = "e2e-secret-token"

	// 1. Initialize embedded SQLite database
	dbPath := filepath.Join(tempDataDir, "garagefab.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	defer db.Close()

	// 2. Setup worker and factory components via adapters (IoC / Dependency Injection)
	storeAdapter := newFactoryStoreAdapter(db)
	worktreeBaseDir := filepath.Join(tempDataDir, "worktrees")
	logBaseDir := filepath.Join(tempDataDir, "logs")

	wtMgr := newFactoryWorktreeAdapter(worktree.NewManager(worktreeBaseDir))
	fakeAgent := newFactoryAgentAdapter(agent.NewFakeRunner())
	cmdRunner := newFactoryCommandAdapter(command.NewRunner())

	engine := factory.NewEngine(storeAdapter, wtMgr, fakeAgent, cmdRunner, logBaseDir)
	scheduler := factory.NewScheduler(storeAdapter, engine, 2)

	// Launch background scheduler goroutine with cancellable context
	schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
	defer cancelScheduler()
	go scheduler.Start(schedulerCtx)

	// Create HTTP server instance
	srv := server.NewServer(cfg, db, engine, scheduler, nil)

	// Step 1: Register temporary Git repository via POST /api/projects (PRJ-1..5)
	repoDir := createTestRepo(t)
	projPayload := fmt.Sprintf(`{"name":"service-alpha","repo_path":%q,"base_ref":"main"}`, repoDir)

	reqProj := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(projPayload))
	reqProj.Host = "127.0.0.1:7878"
	reqProj.Header.Set("Authorization", "Bearer e2e-secret-token")
	reqProj.Header.Set("Content-Type", "application/json")
	wProj := httptest.NewRecorder()
	srv.Router.ServeHTTP(wProj, reqProj)

	if wProj.Code != http.StatusCreated {
		t.Fatalf("POST /api/projects failed: %d: %s", wProj.Code, wProj.Body.String())
	}

	var projResp map[string]any
	if err := json.Unmarshal(wProj.Body.Bytes(), &projResp); err != nil {
		t.Fatalf("parse project response failed: %v", err)
	}
	projectID := int64(projResp["id"].(float64))

	// Step 2: Create a refactor job via POST /api/jobs (INT-1)
	jobPayload := fmt.Sprintf(`{
		"project_id": %d,
		"work_type": "refactor",
		"title": "Refactor database query performance",
		"intent": "Optimize indexes and queries"
	}`, projectID)

	reqJob := httptest.NewRequest(http.MethodPost, "/api/jobs", strings.NewReader(jobPayload))
	reqJob.Host = "127.0.0.1:7878"
	reqJob.Header.Set("Authorization", "Bearer e2e-secret-token")
	reqJob.Header.Set("Content-Type", "application/json")
	wJob := httptest.NewRecorder()
	srv.Router.ServeHTTP(wJob, reqJob)

	if wJob.Code != http.StatusCreated {
		t.Fatalf("POST /api/jobs failed: %d: %s", wJob.Code, wJob.Body.String())
	}

	var jobResp map[string]any
	if err := json.Unmarshal(wJob.Body.Bytes(), &jobResp); err != nil {
		t.Fatalf("parse job response failed: %v", err)
	}
	jobID := int64(jobResp["id"].(float64))

	// Step 3: Poll database until job progresses through scheduler, coding, review,
	// and reaches the human approval gate (06_Human_Approval_Gate / awaiting_approval)
	var finalJob *store.Job
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := db.Jobs().GetJob(context.Background(), jobID)
		if err == nil && j.Stage == store.StageHumanApprovalGate && j.Status == store.StatusAwaitingApproval {
			finalJob = j
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if finalJob == nil {
		j, _ := db.Jobs().GetJob(context.Background(), jobID)
		t.Fatalf("Job did not reach awaiting_approval in time. Current state: stage=%s, status=%s", j.Stage, j.Status)
	}

	// Step 4: Verify checkpoint commit is present (WKT-5, COD-9)
	// The head commit of the worktree must be newer than the base commit.
	if finalJob.HeadSHA == "" || finalJob.HeadSHA == finalJob.BaseSHA {
		t.Errorf("expected HeadSHA to differ from BaseSHA due to checkpoint commit: HeadSHA=%s, BaseSHA=%s",
			finalJob.HeadSHA, finalJob.BaseSHA)
	}

	// Step 5: Verify stage log files exist on disk (LOG-2)
	codingLog := filepath.Join(logBaseDir, fmt.Sprintf("%d", jobID), "step_coding.log")
	if _, err := os.Stat(codingLog); os.IsNotExist(err) {
		t.Errorf("expected coding log file at %s", codingLog)
	}

	reviewLog := filepath.Join(logBaseDir, fmt.Sprintf("%d", jobID), "step_review.log")
	if _, err := os.Stat(reviewLog); os.IsNotExist(err) {
		t.Errorf("expected review log file at %s", reviewLog)
	}

	// Step 6: Verify audit event log records state transitions in DB (LOG-4)
	events, err := db.Events().ListEventsByJob(context.Background(), jobID)
	if err != nil || len(events) < 3 {
		t.Fatalf("expected at least 3 state transition events, got %d, err: %v", len(events), err)
	}

	// Step 7: Exchange API token for session cookie (SEC-3)
	// Demonstrates that approval can authenticate via HTTP cookie rather than Bearer header.
	reqSess := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader("token=e2e-secret-token"))
	reqSess.Host = "127.0.0.1:7878"
	reqSess.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wSess := httptest.NewRecorder()
	srv.Router.ServeHTTP(wSess, reqSess)

	if wSess.Code != http.StatusOK {
		t.Fatalf("POST /api/session failed: %d", wSess.Code)
	}
	cookies := wSess.Result().Cookies()
	var sessCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "gf_session" {
			sessCookie = c
			break
		}
	}
	if sessCookie == nil {
		t.Fatal("expected gf_session cookie")
	}

	// Step 8: Approve job via POST /api/jobs/{id}/approve (APR-5, APR-7)
	// Validates HeadSHA matching to prevent approving stale changes.
	approvePayload := fmt.Sprintf(`{"head_sha":%q}`, finalJob.HeadSHA)
	approveURL := fmt.Sprintf("/api/jobs/%d/approve", jobID)
	reqApprove := httptest.NewRequest(http.MethodPost, approveURL, strings.NewReader(approvePayload))
	reqApprove.Host = "127.0.0.1:7878"
	reqApprove.AddCookie(sessCookie)
	reqApprove.Header.Set("Content-Type", "application/json")
	wApprove := httptest.NewRecorder()
	srv.Router.ServeHTTP(wApprove, reqApprove)

	if wApprove.Code != http.StatusOK {
		t.Fatalf("POST %s failed: %d: %s", approveURL, wApprove.Code, wApprove.Body.String())
	}

	// Step 9: Verify job reached terminal state: 07_Done / done (DLV-4)
	doneJob, err := db.Jobs().GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if doneJob.Stage != store.StageDone || doneJob.Status != store.StatusDone {
		t.Errorf("expected 07_Done/done, got %s/%s", doneJob.Stage, doneJob.Status)
	}

	// Step 10: Verify worktree was cleanly deleted upon delivery (DLV-4, WKT-6)
	if _, err := os.Stat(finalJob.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("expected worktree to be removed after completion, got err: %v", err)
	}

	// Step 11: Verify developer's main working tree was never touched or dirtied (WKT-4)
	cmdStatus := exec.Command("git", "-C", repoDir, "status", "--porcelain")
	statusOut, err := cmdStatus.Output()
	if err != nil || len(bytes.TrimSpace(statusOut)) > 0 {
		t.Errorf("WKT-4 violated: developer checkout status modified: %s", string(statusOut))
	}
}
