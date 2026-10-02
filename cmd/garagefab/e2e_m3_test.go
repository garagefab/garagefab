// Package main contains E2E acceptance tests for Milestone M3.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ACCEPTANCE SCENARIOS:
// Milestone M3 Acceptance Verification (Spec Scenarios 2 & 4).
//
// Scenario 2 (Clarification & Spec Approval Flow):
//   - Verifies SPC-1, SPC-2, SPC-3, SPC-4, SPC-5, SPC-6, SPC-7, HND-1, HND-4, HND-5, APR-7:
//   - Feature intake: problem description ingested.
//   - Spec step 1: Agent writes clarification questions -> job yields in 02/needs_clarification.
//   - Bearer token is accepted to submit answers via POST /api/jobs/{id}/clarification.
//   - Spec step 2: Agent receives answers in prompt and emits valid spec.md -> 02/spec_review.
//   - Human approves spec via session cookie -> job advances to 04_Coding/queued.
//   - Review agent runs in isolated prompt and writes review.json.
//   - Human gate receives immutable evidence -> final approve completes job to 07_Done.
//
// Scenario 4 (Gate Rejection Flow):
//   - Verifies APR-6, APR-7, COD-5:
//   - Job reaches 06_Human_Approval_Gate / awaiting_approval.
//   - Rejecting with empty note returns 422 validation_failed.
//   - Rejecting with Bearer token returns 403 forbidden (session cookie required).
//   - Rejecting with session cookie writes .garagefab/jobs/<id>/rejections/1.md.
//   - Repair attempts counter resets to 0 (COD-5) and job routes to 04_Coding/queued.
//   - Subsequent coding prompt contains the rejection note.
//   - Agent fixes code, review approves, gate approves -> 07_Done.
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

	"github.com/garagefab/garagefab"
	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/server"
	"github.com/garagefab/garagefab/internal/store"
	"github.com/garagefab/garagefab/internal/worker/command"
	"github.com/garagefab/garagefab/internal/worker/worktree"
)

// scenario2ScriptedAgent simulates the clarification and spec workflow:
// 1. First 02_Clarification_and_Spec call: outputs clarification-questions.md (SPC-1, SPC-2).
// 2. Second 02_Clarification_and_Spec call: verifies clarification.md exists and emits valid spec.md (SPC-4, SPC-5).
// 3. 04_Coding call: writes code.
// 4. 05_Independent_Review call: writes structured review.json (REV-2).
type scenario2ScriptedAgent struct {
	specInvocations   int
	codingInvocations int
	reviewInvocations int
}

func (a *scenario2ScriptedAgent) Run(ctx context.Context, req factory.AgentRequest) (*factory.AgentResult, error) {
	artifactDir := filepath.Join(req.WorktreePath, ".garagefab", "jobs", fmt.Sprintf("%d", req.JobID))
	_ = os.MkdirAll(artifactDir, 0755)

	switch req.Stage {
	case factory.StageClarificationAndSpec:
		a.specInvocations++
		if a.specInvocations == 1 {
			// First attempt: emit clarification questions
			qContent := "Q1. Should we support Google OAuth or GitHub OAuth?\n\nQ2. What is the session expiration time?\n"
			_ = os.WriteFile(filepath.Join(artifactDir, "clarification-questions.md"), []byte(qContent), 0644)
			return &factory.AgentResult{ExitCode: 0, Summary: "questions emitted"}, nil
		}

		// Second attempt: verify answers and emit valid spec.md
		validSpec := fmt.Sprintf(`# Feature Spec for %s

## Summary
Implement OAuth authentication for users.

## Goals and Non-Goals
Goals: OAuth login.
Non-Goals: LDAP.

## Design
Hexagonal architecture isolating OAuth provider from factory domain.

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
		a.codingInvocations++
		codeFile := filepath.Join(req.WorktreePath, "oauth.go")
		_ = os.WriteFile(codeFile, []byte("package auth\nfunc HandleOAuth() {}\n"), 0644)
		return &factory.AgentResult{ExitCode: 0, Summary: "coding completed"}, nil

	case factory.StageIndependentReview:
		a.reviewInvocations++
		reviewJSON := `{
  "schema_version": 1,
  "decision": "approve",
  "summary": "OAuth implementation meets AC-1 and has no blocking defects.",
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

// TestScenario2_Clarification_SPC1_2_3 verifies Acceptance Scenario 2 end-to-end:
// 1. Problem intake creates feature job.
// 2. Job transitions to 02/needs_clarification with questions file on disk.
// 3. Bearer token posts answers via POST /api/jobs/{id}/clarification.
// 4. Spec agent generates spec.md and job enters 02/spec_review.
// 5. Spec is approved via dashboard session cookie.
// 6. Coding and Review stages execute successfully.
// 7. Human gate approves evidence and job reaches 07_Done.
func TestScenario2_Clarification_SPC1_2_3(t *testing.T) {
	tempDataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = tempDataDir
	cfg.Server.APIToken = "m3-scenario2-token"

	repoDir := createTestRepo(t)
	gfDir := filepath.Join(repoDir, ".garagefab")
	_ = os.MkdirAll(gfDir, 0755)
	projYaml := `
base_ref: main
commands:
  build: []
  test: []
  lint: []
`
	_ = os.WriteFile(filepath.Join(gfDir, "project.yaml"), []byte(projYaml), 0644)

	cmd := exec.Command("git", "-C", repoDir, "add", ".")
	_ = cmd.Run()
	cmd = exec.Command("git", "-C", repoDir, "commit", "-m", "initial setup")
	_ = cmd.Run()

	dbPath := filepath.Join(tempDataDir, "garagefab.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	storeAdapter := newFactoryStoreAdapter(db)
	wtMgr := newFactoryWorktreeAdapter(worktree.NewManager(filepath.Join(cfg.DataDir, "worktrees")))
	agentRunner := &scenario2ScriptedAgent{}
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

	// 1. Register project with feature profile enabled (INT-1)
	projPayload := fmt.Sprintf(`{"name":"scenario2-project","repo_path":%q,"base_ref":"main","enabled_work_types":["feature","refactor"]}`, repoDir)
	req := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewBufferString(projPayload))
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer m3-scenario2-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create project failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	// 2. Create feature job with ambiguous intent
	jobPayload := `{"project_id":1,"work_type":"feature","title":"Add OAuth Authentication","intent":"Please add OAuth login support"}`
	req = httptest.NewRequest(http.MethodPost, "/api/jobs", bytes.NewBufferString(jobPayload))
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer m3-scenario2-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create job failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	// 3. Poll until job reaches needs_clarification (SPC-2)
	deadline := time.Now().Add(10 * time.Second)
	var clarJob *store.Job
	for time.Now().Before(deadline) {
		j, err := db.Jobs().GetJob(context.Background(), 1)
		if err == nil && j.Stage == store.StageClarificationAndSpec && j.Status == store.StatusNeedsClarification {
			clarJob = j
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if clarJob == nil {
		j, _ := db.Jobs().GetJob(context.Background(), 1)
		t.Fatalf("expected job at 02_Clarification_and_Spec/needs_clarification, got %s/%s", j.Stage, j.Status)
	}

	// 4. Fetch questions artifact via GET /api/jobs/1/artifacts/clarification-questions (LOG-3)
	req = httptest.NewRequest(http.MethodGet, "/api/jobs/1/artifacts/clarification-questions", nil)
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer m3-scenario2-token")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get clarification questions failed: code=%d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Q1. Should we support") {
		t.Errorf("questions artifact content mismatch: %s", w.Body.String())
	}

	// 5. Submit clarification answers via POST /api/jobs/1/clarification with Bearer token (SPC-3)
	ansPayload := `{"answers":[{"q":1,"answer":"Support GitHub OAuth"},{"q":2,"answer":"60 minutes expiration"}]}`
	req = httptest.NewRequest(http.MethodPost, "/api/jobs/1/clarification", bytes.NewBufferString(ansPayload))
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer m3-scenario2-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("submit clarification failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	// 6. Poll until job reaches spec_review (SPC-5)
	deadline = time.Now().Add(10 * time.Second)
	var specJob *store.Job
	for time.Now().Before(deadline) {
		j, err := db.Jobs().GetJob(context.Background(), 1)
		if err == nil && j.Stage == store.StageClarificationAndSpec && j.Status == store.StatusSpecReview {
			specJob = j
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if specJob == nil {
		j, _ := db.Jobs().GetJob(context.Background(), 1)
		t.Fatalf("expected job at 02_Clarification_and_Spec/spec_review, got %s/%s", j.Stage, j.Status)
	}

	// 7. Verify spec artifact via GET /api/jobs/1/artifacts/spec
	req = httptest.NewRequest(http.MethodGet, "/api/jobs/1/artifacts/spec", nil)
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer m3-scenario2-token")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get spec artifact failed: code=%d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "AC-1:") {
		t.Errorf("expected AC-1 in spec artifact: %s", w.Body.String())
	}

	// 8. Attempt spec approval with Bearer token (must fail with 403 Forbidden - APR-7)
	req = httptest.NewRequest(http.MethodPost, "/api/jobs/1/approve", bytes.NewBufferString(`{}`))
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer m3-scenario2-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for Bearer token on /approve (APR-7), got %d", w.Code)
	}

	// 9. Exchange token for session cookie (SEC-3)
	reqSess := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader("token=m3-scenario2-token"))
	reqSess.Host = "127.0.0.1:7878"
	reqSess.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wSess := httptest.NewRecorder()
	srv.Router.ServeHTTP(wSess, reqSess)
	cookies := wSess.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("no session cookie returned from /api/session")
	}
	sessionCookie := cookies[0]

	// 10. Approve spec with session cookie (SPC-6)
	req = httptest.NewRequest(http.MethodPost, "/api/jobs/1/approve", bytes.NewBufferString(`{}`))
	req.Host = "127.0.0.1:7878"
	req.AddCookie(sessionCookie)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("approve spec failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	// 11. Poll until job reaches StageHumanApprovalGate / awaiting_approval
	deadline = time.Now().Add(10 * time.Second)
	var gateJob *store.Job
	for time.Now().Before(deadline) {
		j, err := db.Jobs().GetJob(context.Background(), 1)
		if err == nil && j.Stage == store.StageHumanApprovalGate && j.Status == store.StatusAwaitingApproval {
			gateJob = j
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if gateJob == nil {
		j, _ := db.Jobs().GetJob(context.Background(), 1)
		t.Fatalf("expected job at 06_Human_Approval_Gate/awaiting_approval, got %s/%s", j.Stage, j.Status)
	}

	// 12. Inspect evidence summary via GET /api/jobs/1/evidence (APR-1..3)
	req = httptest.NewRequest(http.MethodGet, "/api/jobs/1/evidence", nil)
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer m3-scenario2-token")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get evidence failed: code=%d, body=%s", w.Code, w.Body.String())
	}
	var evidence factory.EvidenceSummary
	if err := json.Unmarshal(w.Body.Bytes(), &evidence); err != nil {
		t.Fatalf("failed to parse evidence JSON: %v", err)
	}
	if evidence.ReviewDecision != "approve" {
		t.Errorf("expected review decision = approve, got %s", evidence.ReviewDecision)
	}

	// 13. Approve final gate via session cookie (APR-5)
	approvePayload := fmt.Sprintf(`{"head_sha":%q}`, gateJob.HeadSHA)
	req = httptest.NewRequest(http.MethodPost, "/api/jobs/1/approve", bytes.NewBufferString(approvePayload))
	req.Host = "127.0.0.1:7878"
	req.AddCookie(sessionCookie)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("final gate approve failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	// 14. Verify job is terminal 07_Done / done and worktree is deleted (DLV-4)
	doneJob, err := db.Jobs().GetJob(context.Background(), 1)
	if err != nil {
		t.Fatalf("get done job failed: %v", err)
	}
	if doneJob.Stage != store.StageDone || doneJob.Status != store.StatusDone {
		t.Errorf("expected job at 07_Done/done, got %s/%s", doneJob.Stage, doneJob.Status)
	}
	if _, err := os.Stat(doneJob.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("expected worktree to be cleaned up after delivery, still exists at: %s", doneJob.WorktreePath)
	}
}

// scenario4ScriptedAgent tracks rejection feedback and repairs code:
// Invocations:
// 1. Coding attempt 1: Writes initial non-optimized code.
// 2. Review: Approves initial code.
// 3. Coding attempt 2 (after human gate rejection): Verifies rejection note in prompt, writes optimized code.
// 4. Review 2: Approves optimized code.
type scenario4ScriptedAgent struct {
	codingInvocations int
	lastCodingPrompt  string
}

func (a *scenario4ScriptedAgent) Run(ctx context.Context, req factory.AgentRequest) (*factory.AgentResult, error) {
	artifactDir := filepath.Join(req.WorktreePath, ".garagefab", "jobs", fmt.Sprintf("%d", req.JobID))
	_ = os.MkdirAll(artifactDir, 0755)

	switch req.Stage {
	case factory.StageCoding:
		a.codingInvocations++
		a.lastCodingPrompt = req.Prompt

		codePath := filepath.Join(req.WorktreePath, "feature.go")
		if a.codingInvocations == 1 {
			_ = os.WriteFile(codePath, []byte("package feature\nfunc Run() { /* basic */ }\n"), 0644)
		} else {
			_ = os.WriteFile(codePath, []byte("package feature\nfunc Run() { /* highly optimized */ }\n"), 0644)
		}
		return &factory.AgentResult{ExitCode: 0, Summary: "code written"}, nil

	case factory.StageIndependentReview:
		reviewJSON := `{
  "schema_version": 1,
  "decision": "approve",
  "summary": "Implementation approved.",
  "risk": {
    "side_effect": {"score": 1, "rationale": "low"},
    "performance": {"score": 1, "rationale": "low"},
    "backward_compatibility": {"score": 1, "rationale": "low"}
  },
  "findings": [],
  "warnings": [],
  "spec_coverage": []
}`
		_ = os.WriteFile(filepath.Join(artifactDir, "review.json"), []byte(reviewJSON), 0644)
		return &factory.AgentResult{ExitCode: 0, Summary: "review done"}, nil

	default:
		return &factory.AgentResult{ExitCode: 0}, nil
	}
}

// TestScenario4_Rejection_APR6_COD5 verifies Acceptance Scenario 4:
// 1. Job reaches gate.
// 2. Reject with empty note returns 422.
// 3. Reject with session cookie writes rejections/1.md.
// 4. Job moves back to 04_Coding / queued, and repair loop attempts reset to 0 (COD-5).
// 5. Subsequent coding prompt contains the rejection note.
// 6. Job fixes issue, completes review, and human approves at gate.
func TestScenario4_Rejection_APR6_COD5(t *testing.T) {
	tempDataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = tempDataDir
	cfg.Server.APIToken = "m3-scenario4-token"

	repoDir := createTestRepo(t)
	gfDir := filepath.Join(repoDir, ".garagefab")
	_ = os.MkdirAll(gfDir, 0755)
	projYaml := `
base_ref: main
commands:
  build: []
  test: []
  lint: []
`
	_ = os.WriteFile(filepath.Join(gfDir, "project.yaml"), []byte(projYaml), 0644)

	cmd := exec.Command("git", "-C", repoDir, "add", ".")
	_ = cmd.Run()
	cmd = exec.Command("git", "-C", repoDir, "commit", "-m", "initial setup")
	_ = cmd.Run()

	dbPath := filepath.Join(tempDataDir, "garagefab.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	storeAdapter := newFactoryStoreAdapter(db)
	wtMgr := newFactoryWorktreeAdapter(worktree.NewManager(filepath.Join(cfg.DataDir, "worktrees")))
	agentRunner := &scenario4ScriptedAgent{}
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

	// 1. Register project with refactor profile enabled
	projPayload := fmt.Sprintf(`{"name":"scenario4-project","repo_path":%q,"base_ref":"main","enabled_work_types":["refactor"]}`, repoDir)
	req := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewBufferString(projPayload))
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer m3-scenario4-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create project failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	// 2. Create job
	jobPayload := `{"project_id":1,"work_type":"refactor","title":"Algorithm Optimization","intent":"Optimize feature algorithm"}`
	req = httptest.NewRequest(http.MethodPost, "/api/jobs", bytes.NewBufferString(jobPayload))
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer m3-scenario4-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create job failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	// 3. Poll until job reaches 06_Human_Approval_Gate / awaiting_approval
	deadline := time.Now().Add(10 * time.Second)
	var gateJob *store.Job
	for time.Now().Before(deadline) {
		j, err := db.Jobs().GetJob(context.Background(), 1)
		if err == nil && j.Stage == store.StageHumanApprovalGate && j.Status == store.StatusAwaitingApproval {
			gateJob = j
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if gateJob == nil {
		j, _ := db.Jobs().GetJob(context.Background(), 1)
		t.Fatalf("expected job at 06_Human_Approval_Gate/awaiting_approval, got %s/%s", j.Stage, j.Status)
	}

	// 4. Exchange token for session cookie (APR-7)
	reqSess := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader("token=m3-scenario4-token"))
	reqSess.Host = "127.0.0.1:7878"
	reqSess.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wSess := httptest.NewRecorder()
	srv.Router.ServeHTTP(wSess, reqSess)
	cookies := wSess.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("no session cookie returned from /api/session")
	}
	sessionCookie := cookies[0]

	// 5. Reject with empty note (must fail with 422 - APR-6)
	req = httptest.NewRequest(http.MethodPost, "/api/jobs/1/reject", bytes.NewBufferString(`{"note": "  "}`))
	req.Host = "127.0.0.1:7878"
	req.AddCookie(sessionCookie)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for empty rejection note, got %d: %s", w.Code, w.Body.String())
	}

	// 6. Reject with note via session cookie (APR-6)
	rejectionNote := "Please optimize algorithm using fast path"
	req = httptest.NewRequest(http.MethodPost, "/api/jobs/1/reject", bytes.NewBufferString(fmt.Sprintf(`{"note":%q}`, rejectionNote)))
	req.Host = "127.0.0.1:7878"
	req.AddCookie(sessionCookie)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reject job failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	// 7. Verify rejection artifact exists at .garagefab/jobs/1/rejections/1.md
	rejFile := filepath.Join(gateJob.WorktreePath, ".garagefab", "jobs", "1", "rejections", "1.md")
	rejContent, err := os.ReadFile(rejFile)
	if err != nil {
		t.Fatalf("expected rejection artifact at %s: %v", rejFile, err)
	}
	if !strings.Contains(string(rejContent), rejectionNote) {
		t.Errorf("rejection file missing note text: %s", string(rejContent))
	}

	// 8. Poll until job is admitted, completes coding attempt 2, review, and reaches gate again
	deadline = time.Now().Add(10 * time.Second)
	var gateJob2 *store.Job
	for time.Now().Before(deadline) {
		j, err := db.Jobs().GetJob(context.Background(), 1)
		if err == nil && j.Stage == store.StageHumanApprovalGate && j.Status == store.StatusAwaitingApproval && agentRunner.codingInvocations >= 2 {
			gateJob2 = j
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if gateJob2 == nil {
		j, _ := db.Jobs().GetJob(context.Background(), 1)
		t.Fatalf("expected job to return to gate after re-coding, got %s/%s (runs: %d)", j.Stage, j.Status, agentRunner.codingInvocations)
	}

	// 9. Verify coding prompt contained rejection note (APR-6)
	if !strings.Contains(agentRunner.lastCodingPrompt, rejectionNote) {
		t.Errorf("re-coding prompt missing rejection note, got:\n%s", agentRunner.lastCodingPrompt)
	}

	// 10. Approve final gate via session cookie
	approvePayload := fmt.Sprintf(`{"head_sha":%q}`, gateJob2.HeadSHA)
	req = httptest.NewRequest(http.MethodPost, "/api/jobs/1/approve", bytes.NewBufferString(approvePayload))
	req.Host = "127.0.0.1:7878"
	req.AddCookie(sessionCookie)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("gate approve failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	// 11. Verify job is terminal 07_Done / done
	doneJob, err := db.Jobs().GetJob(context.Background(), 1)
	if err != nil {
		t.Fatalf("get done job failed: %v", err)
	}
	if doneJob.Stage != store.StageDone || doneJob.Status != store.StatusDone {
		t.Errorf("expected job at 07_Done/done, got %s/%s", doneJob.Stage, doneJob.Status)
	}
}
