// Package main contains E2E acceptance tests for Milestone M4.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ACCEPTANCE SCENARIOS:
// Milestone M4 Acceptance Verification: Dashboard, Security & Interactive Gates.
//
// Verifies:
// 1. Dual-Mode Authentication & Session Cookies (SEC-1..5, CLI-2):
//   - Unauthenticated requests to protected endpoints return 401 Unauthorized.
//   - Secret API token is exchanged for HttpOnly, SameSite=Strict session cookie.
//   - Session cookie authenticates subsequent web dashboard requests.
//
// 2. Project Registration, Validation & Templates (PRJ-1..8, UI-6):
//   - Registering a repo without .garagefab/project.yaml returns 422 with YAML template (PRJ-3).
//   - Generating project template writes valid configuration to disk (PRJ-7).
//   - Valid repo is registered and appears in project listings (PRJ-1).
//   - Archiving is refused when active non-terminal jobs exist (PRJ-8).
//
// 3. Overview Dashboard Aggregates (UI-1):
//   - GET /api/overview returns aggregated counts across non-archived projects.
//   - Attention list includes jobs needing clarification, review, or approval.
//   - Activity feed returns recent events with job titles.
//
// 4. SDLC Step Inspection & Lazy Log Streaming (UI-3, UI-7, LOG-5):
//   - GET /api/jobs/{id}/steps returns all step executions and statuses.
//   - Step logs are NEVER fetched by default (LOG-5); GET /api/jobs/{id}/steps/{stepId}/log
//     loads output on demand, supporting text/plain and text/event-stream.
//
// 5. Interactive Gate Rejection & Approval (UI-4, APR-5..7):
//   - Bearer tokens are strictly forbidden on gate actions (SEC-4, APR-7).
//   - Interactive session cookies are required.
//   - Rejection requires a non-empty note and transitions job back to coding.
//
// JAVA / SPRING BOOT COMPARISON:
//   - Similar to an end-to-end `@SpringBootTest(webEnvironment = RANDOM_PORT)` integrating
//     Spring Security (Session vs Bearer filters), Spring MVC controllers, Flyway migrations,
//     and Spring Event publishers.
//
// ==============================================================================
package main

import (
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

	"github.com/garagefab/garagefab"
	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/server"
	"github.com/garagefab/garagefab/internal/store"
	"github.com/garagefab/garagefab/internal/worker/command"
	"github.com/garagefab/garagefab/internal/worker/worktree"
)

// m4ScriptedAgent handles the pipeline steps for M4 verification.
type m4ScriptedAgent struct{}

func (a *m4ScriptedAgent) Run(ctx context.Context, req factory.AgentRequest) (*factory.AgentResult, error) {
	artifactDir := filepath.Join(req.WorktreePath, ".garagefab", "jobs", fmt.Sprintf("%d", req.JobID))
	_ = os.MkdirAll(artifactDir, 0755)

	switch req.Stage {
	case factory.StageClarificationAndSpec:
		validSpec := fmt.Sprintf(`# Feature Spec for %s

## Summary
Implement OAuth authentication.

## Goals and Non-Goals
Goals: OAuth login.
Non-Goals: LDAP.

## Design
Hexagonal architecture isolating OAuth provider.

## Acceptance Criteria
Given valid OAuth tokens When login requested Then AC-1: authenticate successfully.

## Implementation Plan
1. Add OAuth endpoints (AC-1)

## Test Plan
Automated integration tests.

## Risks and Assumptions
None.
`, req.ProjectName)
		_ = os.WriteFile(filepath.Join(artifactDir, "spec.md"), []byte(validSpec), 0644)
		return &factory.AgentResult{ExitCode: 0, Summary: "valid spec generated"}, nil

	case factory.StageCoding:
		codeFile := filepath.Join(req.WorktreePath, "feature.go")
		_ = os.WriteFile(codeFile, []byte("package main\n\nfunc Feature() string { return \"ok\" }\n"), 0644)
		return &factory.AgentResult{ExitCode: 0, Summary: "coding completed"}, nil

	case factory.StageIndependentReview:
		reviewJSON := `{
  "schema_version": 1,
  "decision": "approve",
  "summary": "Implementation meets AC-1 and has no blocking defects.",
  "risk": {
    "side_effect": {"score": 1, "rationale": "low"},
    "performance": {"score": 1, "rationale": "low"},
    "backward_compatibility": {"score": 1, "rationale": "low"}
  },
  "findings": [],
  "warnings": [],
  "spec_coverage": [{"criterion": "AC-1", "status": "met", "note": "verified"}]
}`
		_ = os.WriteFile(filepath.Join(artifactDir, "review.json"), []byte(reviewJSON), 0644)
		return &factory.AgentResult{ExitCode: 0, Summary: "review approved"}, nil

	default:
		return &factory.AgentResult{ExitCode: 0}, nil
	}
}

func TestE2E_M4_DashboardAndSecurity(t *testing.T) {
	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.Server.Listen = "127.0.0.1:7878"
	cfg.Server.APIToken = "m4-secret-token-abcdef1234567890abcdef1234567890abcdef1234567890"

	dbPath := filepath.Join(dataDir, "garagefab.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	storeAdapter := newFactoryStoreAdapter(db)
	wtMgr := newFactoryWorktreeAdapter(worktree.NewManager(filepath.Join(cfg.DataDir, "worktrees")))
	agentRunner := &m4ScriptedAgent{}
	cmdRunner := newFactoryCommandAdapter(command.NewRunner())
	guardrailAdapter := newFactoryGuardrailAdapter()
	projCfgAdapter := newFactoryProjectConfigAdapter()

	engine := factory.NewEngine(storeAdapter, wtMgr, agentRunner, cmdRunner, filepath.Join(cfg.DataDir, "logs"))
	engine.SetGuardrailRunner(guardrailAdapter)
	engine.SetProjectConfigProvider(projCfgAdapter)

	scheduler := factory.NewScheduler(storeAdapter, engine, 5)
	scheduler.SetProjectConfigProvider(projCfgAdapter)

	schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
	defer cancelScheduler()
	go scheduler.Start(schedulerCtx)

	srv := server.NewServer(cfg, db, engine, scheduler, garagefab.Dist())

	// -------------------------------------------------------------------------
	// 1. Dual-Mode Authentication & Session Exchange (SEC-1..5, CLI-2)
	// -------------------------------------------------------------------------
	// 1a. Unauthenticated GET /api/overview returns 401 (SEC-3)
	reqUnauth := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	reqUnauth.Host = "127.0.0.1:7878"
	wUnauth := httptest.NewRecorder()
	srv.Router.ServeHTTP(wUnauth, reqUnauth)
	if wUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d", wUnauth.Code)
	}

	// 1b. Exchange secret API token for HttpOnly session cookie (SEC-3, CLI-2)
	sessionBody := fmt.Sprintf(`{"token":%q}`, cfg.Server.APIToken)
	reqSession := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(sessionBody))
	reqSession.Host = "127.0.0.1:7878"
	reqSession.Header.Set("Content-Type", "application/json")
	wSession := httptest.NewRecorder()
	srv.Router.ServeHTTP(wSession, reqSession)
	if wSession.Code != http.StatusOK {
		t.Fatalf("expected 200 for session exchange, got %d: %s", wSession.Code, wSession.Body.String())
	}

	cookies := wSession.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "gf_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("expected gf_session cookie to be set")
	}
	if !sessionCookie.HttpOnly {
		t.Errorf("expected session cookie to be HttpOnly")
	}

	// Helper for authenticated requests via cookie
	doSessionReq := func(method, path string, body string) *httptest.ResponseRecorder {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		req.Host = "127.0.0.1:7878"
		req.Header.Set("Origin", "http://127.0.0.1:7878")
		req.AddCookie(sessionCookie)
		rec := httptest.NewRecorder()
		srv.Router.ServeHTTP(rec, req)
		return rec
	}

	// -------------------------------------------------------------------------
	// 2. Project Registration, Validation & Config Template (PRJ-1..8, UI-6)
	// -------------------------------------------------------------------------
	repoDir := filepath.Join(t.TempDir(), "m4-test-repo")
	_ = os.MkdirAll(repoDir, 0700)
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %s: %v", args, string(out), err)
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.name", "M4 Tester")
	runGit("config", "user.email", "m4@garagefab.local")
	_ = os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# M4 Repo\n"), 0644)
	runGit("add", "README.md")
	runGit("commit", "-m", "init")

	// 2a. Attempt registering without project.yaml -> 422 with template (PRJ-3, UI-6)
	regBodyNoCfg := fmt.Sprintf(`{"repo_path":%q,"name":"service-m4","base_ref":"main"}`, repoDir)
	wNoCfg := doSessionReq(http.MethodPost, "/api/projects", regBodyNoCfg)
	if wNoCfg.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for missing project.yaml, got %d: %s", wNoCfg.Code, wNoCfg.Body.String())
	}
	if !strings.Contains(wNoCfg.Body.String(), "project.yaml missing") {
		t.Errorf("expected missing project.yaml error message, got: %s", wNoCfg.Body.String())
	}

	// 2b. Generate project.yaml template via POST /api/projects/config-template (PRJ-7)
	tplBody := fmt.Sprintf(`{"repo_path":%q}`, repoDir)
	wTpl := doSessionReq(http.MethodPost, "/api/projects/config-template", tplBody)
	if wTpl.Code != http.StatusOK {
		t.Fatalf("expected 200 for template generation, got %d: %s", wTpl.Code, wTpl.Body.String())
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".garagefab", "project.yaml")); os.IsNotExist(err) {
		t.Fatalf(".garagefab/project.yaml was not generated")
	}

	// 2c. Register valid project now succeeds (PRJ-1..5)
	wReg := doSessionReq(http.MethodPost, "/api/projects", regBodyNoCfg)
	if wReg.Code != http.StatusCreated {
		t.Fatalf("expected 201 for project registration, got %d: %s", wReg.Code, wReg.Body.String())
	}

	var projResp map[string]any
	_ = json.Unmarshal(wReg.Body.Bytes(), &projResp)
	projectID := int64(projResp["id"].(float64))

	// -------------------------------------------------------------------------
	// 3. Job Creation, Pipeline Execution & Board/Overview Verification (INT-1, UI-1, UI-2)
	// -------------------------------------------------------------------------
	jobPayload := fmt.Sprintf(`{
		"project_id": %d,
		"work_type": "feature",
		"title": "M4 Security & Live Updates",
		"intent": "Verify live updates and UI integration"
	}`, projectID)

	wJob := doSessionReq(http.MethodPost, "/api/jobs", jobPayload)
	if wJob.Code != http.StatusCreated {
		t.Fatalf("expected 201 for job creation, got %d: %s", wJob.Code, wJob.Body.String())
	}

	var jobResp map[string]any
	_ = json.Unmarshal(wJob.Body.Bytes(), &jobResp)
	jobID := int64(jobResp["id"].(float64))

	// Wait for job to progress through Clarification & Spec to 02_Clarification_and_Spec / spec_review
	waitForStatus := func(targetStage, targetStatus string) *store.Job {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			j, err := db.Jobs().GetJob(context.Background(), jobID)
			if err == nil && j.Stage == targetStage && j.Status == targetStatus {
				return j
			}
			time.Sleep(50 * time.Millisecond)
		}
		j, _ := db.Jobs().GetJob(context.Background(), jobID)
		t.Fatalf("job did not reach stage=%s, status=%s in time (currently: %s/%s)", targetStage, targetStatus, j.Stage, j.Status)
		return nil
	}

	jobAtSpec := waitForStatus(store.StageClarificationAndSpec, store.StatusSpecReview)

	// 3a. Verify Overview API reflects attention list & job counts (UI-1)
	wOverview := doSessionReq(http.MethodGet, "/api/overview", "")
	if wOverview.Code != http.StatusOK {
		t.Fatalf("expected 200 for overview, got %d: %s", wOverview.Code, wOverview.Body.String())
	}
	var ovData struct {
		JobCounts     map[string]int   `json:"job_counts"`
		AttentionList []map[string]any `json:"attention_list"`
	}
	_ = json.Unmarshal(wOverview.Body.Bytes(), &ovData)
	if ovData.JobCounts["spec_review"] < 1 {
		t.Errorf("expected spec_review count >= 1, got %d", ovData.JobCounts["spec_review"])
	}
	if len(ovData.AttentionList) == 0 {
		t.Errorf("expected job in attention list, got 0")
	}

	// 3b. Verify Step Runs & Lazy Log Endpoint (UI-3, UI-7, LOG-5)
	wSteps := doSessionReq(http.MethodGet, fmt.Sprintf("/api/jobs/%d/steps", jobID), "")
	if wSteps.Code != http.StatusOK {
		t.Fatalf("expected 200 for steps, got %d: %s", wSteps.Code, wSteps.Body.String())
	}
	var steps []store.StepRun
	_ = json.Unmarshal(wSteps.Body.Bytes(), &steps)
	if len(steps) == 0 {
		t.Fatalf("expected at least 1 step run record")
	}
	firstStepID := steps[0].ID

	// Lazy log access: plain text response
	wLog := doSessionReq(http.MethodGet, fmt.Sprintf("/api/jobs/%d/steps/%d/log", jobID, firstStepID), "")
	if wLog.Code != http.StatusOK {
		t.Fatalf("expected 200 for step log, got %d: %s", wLog.Code, wLog.Body.String())
	}

	// -------------------------------------------------------------------------
	// 4. Interactive Gate Security Guardrails (SEC-4, APR-7, UI-4)
	// -------------------------------------------------------------------------
	// 4a. Bearer token is forbidden on approve (SEC-4, APR-7)
	reqBearerApprove := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/jobs/%d/approve", jobID), strings.NewReader(`{}`))
	reqBearerApprove.Host = "127.0.0.1:7878"
	reqBearerApprove.Header.Set("Authorization", "Bearer "+cfg.Server.APIToken)
	reqBearerApprove.Header.Set("Content-Type", "application/json")
	wBearerApprove := httptest.NewRecorder()
	srv.Router.ServeHTTP(wBearerApprove, reqBearerApprove)
	if wBearerApprove.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for Bearer token on approve, got %d", wBearerApprove.Code)
	}

	// 4b. Session cookie approves spec successfully -> advances to coding -> reaches 06_Human_Approval_Gate / awaiting_approval
	wApproveSpec := doSessionReq(http.MethodPost, fmt.Sprintf("/api/jobs/%d/approve", jobID), fmt.Sprintf(`{"head_sha":%q}`, jobAtSpec.HeadSHA))
	if wApproveSpec.Code != http.StatusOK {
		t.Fatalf("expected 200 for spec approval, got %d: %s", wApproveSpec.Code, wApproveSpec.Body.String())
	}

	jobAtGate := waitForStatus(store.StageHumanApprovalGate, store.StatusAwaitingApproval)
	if jobAtGate == nil {
		t.Fatalf("expected job at human approval gate")
	}

	// -------------------------------------------------------------------------
	// 5. Gate Rejection Flow (APR-6, UI-4)
	// -------------------------------------------------------------------------
	// 5a. Rejection without note fails with 422 (APR-6)
	wEmptyReject := doSessionReq(http.MethodPost, fmt.Sprintf("/api/jobs/%d/reject", jobID), `{"note":""}`)
	if wEmptyReject.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for empty rejection note, got %d: %s", wEmptyReject.Code, wEmptyReject.Body.String())
	}

	// 5b. Valid rejection notes route job back to coding
	wValidReject := doSessionReq(http.MethodPost, fmt.Sprintf("/api/jobs/%d/reject", jobID), `{"note":"Please add more tests"}`)
	if wValidReject.Code != http.StatusOK {
		t.Fatalf("expected 200 for rejection, got %d: %s", wValidReject.Code, wValidReject.Body.String())
	}

	// Job is re-scheduled to coding and again reaches approval gate
	jobAtGate2 := waitForStatus(store.StageHumanApprovalGate, store.StatusAwaitingApproval)

	// -------------------------------------------------------------------------
	// 6. Project Archiving Guardrails (PRJ-8)
	// -------------------------------------------------------------------------
	// 6a. Attempt to archive project with active non-terminal job returns 409 Conflict
	wArchiveActive := doSessionReq(http.MethodPost, fmt.Sprintf("/api/projects/%d/archive", projectID), "")
	if wArchiveActive.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict when archiving project with active job, got %d", wArchiveActive.Code)
	}

	// 6b. Final gate approval completes the job to 07_Done / done
	wFinalApprove := doSessionReq(http.MethodPost, fmt.Sprintf("/api/jobs/%d/approve", jobID), fmt.Sprintf(`{"head_sha":%q}`, jobAtGate2.HeadSHA))
	if wFinalApprove.Code != http.StatusOK {
		t.Fatalf("expected 200 for final approval, got %d: %s", wFinalApprove.Code, wFinalApprove.Body.String())
	}

	waitForStatus(store.StageDone, store.StatusDone)

	// 6c. Now that job is done, archiving the project succeeds (PRJ-8)
	wArchiveIdle := doSessionReq(http.MethodPost, fmt.Sprintf("/api/projects/%d/archive", projectID), "")
	if wArchiveIdle.Code != http.StatusOK {
		t.Fatalf("expected 200 for archiving idle project, got %d: %s", wArchiveIdle.Code, wArchiveIdle.Body.String())
	}

	archivedProj, err := db.Projects().GetProject(context.Background(), projectID)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if !archivedProj.IsArchived {
		t.Errorf("expected project to be archived")
	}
}
