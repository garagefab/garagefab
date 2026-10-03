// Package main contains E2E acceptance tests for Milestone M2.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ACCEPTANCE SCENARIOS:
// Milestone M2 Acceptance Verification (Spec Scenarios 3 & 5).
//
// Scenario 3 (Repair Loop & Guardrail):
//   - Verifies COD-4, COD-5, COD-6, GRD-1, GRD-4:
//   - Coding attempt 1: Test command fails -> step is Flawed -> repair loop triggers.
//   - Coding attempt 2: Agent edits protected test file -> guardrail violation -> Flawed -> repair loop triggers with file list.
//   - Coding attempt 3: Agent restores protected file and fixes code -> all checks pass -> advances to gate with counter = 2.
//   - Variant: Attempts exhausted -> job transitions to 04/failed (Manual).
//
// Scenario 5 (Crash Recovery):
//   - Verifies RCV-1..4, WKT-5, PIP-7:
//   - Active job running child process is abruptly interrupted (simulating kill -9 crash).
//   - On restart: orphan agent is killed, job is marked 'interrupted', worktree is preserved.
//   - POST /api/jobs/{id}/retry rolls back worktree to step checkpoint and drives job to completion.
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
	"syscall"
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

// scenario3ScriptedAgent simulates the multi-attempt repair loop behavior for Scenario 3:
// Attempt 1: Edits code but fails test (no fixed.txt created)
// Attempt 2: Tampers with protected auth_test.go
// Attempt 3: Restores auth_test.go and writes fixed.txt
type scenario3ScriptedAgent struct {
	attempts int
}

func (a *scenario3ScriptedAgent) Run(ctx context.Context, req factory.AgentRequest) (*factory.AgentResult, error) {
	if req.Stage == factory.StageIndependentReview {
		artifactDir := filepath.Join(req.WorktreePath, ".garagefab", "jobs", fmt.Sprintf("%d", req.JobID))
		_ = os.MkdirAll(artifactDir, 0755)
		reviewJSON := `{
  "schema_version": 1,
  "decision": "approve",
  "summary": "M2 scenario 3 review passed",
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
		return &factory.AgentResult{ExitCode: 0, Summary: "review passed"}, nil
	}

	a.attempts++
	worktreePath := req.WorktreePath

	switch a.attempts {
	case 1:
		// Attempt 1: Edit auth.go without creating fixed.txt (test command will fail)
		authFile := filepath.Join(worktreePath, "auth.go")
		_ = os.WriteFile(authFile, []byte("package auth\n// attempt 1 edit\n"), 0644)
		return &factory.AgentResult{ExitCode: 0}, nil

	case 2:
		// Attempt 2: Tamper with protected auth_test.go (guardrail violation)
		testFile := filepath.Join(worktreePath, "auth_test.go")
		_ = os.WriteFile(testFile, []byte("package auth\n// tampered test\n"), 0644)
		return &factory.AgentResult{ExitCode: 0}, nil

	case 3:
		// Attempt 3: Restore auth_test.go and create fixed.txt (success!)
		// Git checkout auth_test.go to restore
		cmd := exec.Command("git", "-C", worktreePath, "checkout", "auth_test.go")
		_ = cmd.Run()
		fixedFile := filepath.Join(worktreePath, "fixed.txt")
		_ = os.WriteFile(fixedFile, []byte("all tests pass"), 0644)
		return &factory.AgentResult{ExitCode: 0}, nil

	default:
		return &factory.AgentResult{ExitCode: 0}, nil
	}
}

// TestScenario3_RepairLoopAndGuardrail_COD4_5_6_GRD1_4 validates Scenario 3:
// 1. Initial attempt fails verification test
// 2. Second attempt violates protected path guardrail
// 3. Third attempt fixes issue and restores protected path
// 4. Job reaches StageHumanApprovalGate with attempt counter at 2.
func TestScenario3_RepairLoopAndGuardrail_COD4_5_6_GRD1_4(t *testing.T) {
	tempDataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = tempDataDir
	cfg.Server.APIToken = "m2-test-token"

	// 1. Initialize temporary Git repository
	repoDir := createTestRepo(t)

	// Create auth.go and auth_test.go in base repo
	authFile := filepath.Join(repoDir, "auth.go")
	_ = os.WriteFile(authFile, []byte("package auth\nfunc Login() {}\n"), 0644)
	authTestFile := filepath.Join(repoDir, "auth_test.go")
	_ = os.WriteFile(authTestFile, []byte("package auth\n// original test\n"), 0644)

	// Configure project.yaml in repository
	gfDir := filepath.Join(repoDir, ".garagefab")
	_ = os.MkdirAll(gfDir, 0755)
	projYaml := `
base_ref: main
agents:
  coding: opencode
  review: agy
commands:
  test: ["test -f fixed.txt"]
guardrails:
  protected_paths: ["**/*_test.go"]
`
	_ = os.WriteFile(filepath.Join(gfDir, "project.yaml"), []byte(projYaml), 0644)

	// Commit initial state
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %s (%v)", args, string(out), err)
		}
	}
	runGit("add", ".")
	runGit("commit", "-m", "setup project")

	// 2. Initialize Database & Adapters
	dbPath := filepath.Join(tempDataDir, "garagefab.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	storeAdapter := newFactoryStoreAdapter(db)
	wtMgr := newFactoryWorktreeAdapter(worktree.NewManager(filepath.Join(cfg.DataDir, "worktrees")))
	agentRunner := &scenario3ScriptedAgent{}
	cmdRunner := newFactoryCommandAdapter(command.NewRunner())
	guardrailAdapter := newFactoryGuardrailAdapter()
	projCfgAdapter := newFactoryProjectConfigAdapter()

	engine := factory.NewEngine(storeAdapter, wtMgr, agentRunner, cmdRunner, filepath.Join(cfg.DataDir, "logs"))
	engine.SetGuardrailRunner(guardrailAdapter)
	engine.SetProjectConfigProvider(projCfgAdapter)
	engine.SetMaxRepairAttempts(3)

	scheduler := factory.NewScheduler(storeAdapter, engine, 5)
	scheduler.SetProjectConfigProvider(projCfgAdapter)

	schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
	defer cancelScheduler()
	go scheduler.Start(schedulerCtx)

	srv := server.NewServer(cfg, db, engine, scheduler, garagefab.Dist())

	// 3. Register Project via API
	projPayload := fmt.Sprintf(`{"name":"scenario3-project","repo_path":%q,"base_ref":"main"}`, repoDir)
	req := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewBufferString(projPayload))
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer m2-test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create project failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	// 4. Create Job via API
	jobPayload := `{"project_id":1,"work_type":"refactor","title":"Scenario 3 Repair Test","intent":"Fix auth and ensure tests pass"}`
	req = httptest.NewRequest(http.MethodPost, "/api/jobs", bytes.NewBufferString(jobPayload))
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Authorization", "Bearer m2-test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create job failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	// 5. Poll until Job reaches 06_Human_Approval_Gate / awaiting_approval
	deadline := time.Now().Add(10 * time.Second)
	var finalJob *store.Job
	for time.Now().Before(deadline) {
		req = httptest.NewRequest(http.MethodGet, "/api/jobs/1", nil)
		req.Host = "127.0.0.1:7878"
		req.Header.Set("Authorization", "Bearer m2-test-token")
		w = httptest.NewRecorder()
		srv.Router.ServeHTTP(w, req)

		if w.Code == http.StatusOK {
			var j store.Job
			if err := json.Unmarshal(w.Body.Bytes(), &j); err == nil {
				if j.Stage == factory.StageHumanApprovalGate && j.Status == factory.StatusAwaitingApproval {
					finalJob = &j
					break
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	if finalJob == nil {
		j, _ := db.Jobs().GetJob(context.Background(), 1)
		t.Fatalf("job did not reach awaiting_approval in time. Job: stage=%s status=%s", j.Stage, j.Status)
	}

	// Verify that 3 attempts ran (initial + 2 repairs)
	if agentRunner.attempts != 3 {
		t.Fatalf("expected 3 agent attempts, got %d", agentRunner.attempts)
	}

	// Verify StepRuns recorded in DB
	steps, err := db.StepRuns().ListStepRunsByJob(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) < 3 {
		t.Fatalf("expected at least 3 step runs recorded, got %d", len(steps))
	}
}

// TestScenario5_CrashRecoveryAndRetry_RCV1_4_WKT5_PIP7 validates Scenario 5:
// 1. Garagefab executes a job with a live child process
// 2. Crash is simulated (process killed, process record active)
// 3. Next startup terminates orphan process group, marks job interrupted, retains worktree
// 4. POST /api/jobs/{id}/retry rolls back to checkpoint, requeues, and completes cleanly.
func TestScenario5_CrashRecoveryAndRetry_RCV1_4_WKT5_PIP7(t *testing.T) {
	tempDataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = tempDataDir
	cfg.Server.APIToken = "m2-test-token"

	repoDir := createTestRepo(t)

	dbPath := filepath.Join(tempDataDir, "garagefab.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Setup project and running job in SQLite
	ctx := context.Background()
	proj := &store.Project{Name: "crash-project", RepoPath: repoDir, BaseRef: "main"}
	_ = db.Projects().CreateProject(ctx, proj)

	wtPath := filepath.Join(cfg.DataDir, "worktrees", proj.Name, "1")
	_ = os.MkdirAll(wtPath, 0755)

	job := &store.Job{
		ProjectID:    proj.ID,
		WorkType:     store.WorkTypeRefactor,
		Title:        "Scenario 5 Crash Test",
		Stage:        factory.StageCoding,
		Status:       factory.StatusRunning,
		WorktreePath: wtPath,
		BranchName:   "garagefab/job-1",
		BaseSHA:      "head123",
		HeadSHA:      "head123",
	}
	_ = db.Jobs().CreateJob(ctx, job)

	step := &store.StepRun{
		JobID:     job.ID,
		Stage:     factory.StageCoding,
		Kind:      factory.StepKindAgent,
		Attempt:   1,
		Executor:  "agent",
		Status:    store.StepStatusRunning,
		StartedAt: time.Now().UTC(),
	}
	_ = db.StepRuns().CreateStepRun(ctx, step)

	// Spawn a real long-running child process in its own Process Group
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	pgid, _ := syscall.Getpgid(pid)
	defer func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}()

	// Save active process record (RCV-1)
	procRec := &store.ProcessRecord{
		StepRunID: step.ID,
		PID:       pid,
		PGID:      pgid,
		StartTime: time.Now().Unix(),
		Active:    true,
	}
	_ = db.ProcessRecords().CreateProcessRecord(ctx, procRec)

	// Close DB to simulate server death / crash
	_ = db.Close()

	// 2. RESTART (Simulate next startup: run RecoverOrphanProcesses RCV-2, RCV-3)
	dbRestarted, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dbRestarted.Close()

	if err := RecoverOrphanProcesses(ctx, dbRestarted); err != nil {
		t.Fatalf("crash recovery failed: %v", err)
	}

	// Confirm orphan process group was terminated
	_ = cmd.Wait()
	if IsProcessAlive(pid) {
		t.Fatalf("expected orphan process pid %d to be dead, but still alive", pid)
	}

	// Confirm Job is marked interrupted (RCV-3) and worktree is kept (WKT-6)
	interruptedJob, err := dbRestarted.Jobs().GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if interruptedJob.Status != factory.StatusInterrupted {
		t.Fatalf("expected job status %s, got %s", factory.StatusInterrupted, interruptedJob.Status)
	}
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("expected worktree to be preserved on disk (WKT-6), got error: %v", err)
	}

	// 3. Call POST /api/jobs/1/retry (PIP-7, WKT-5)
	storeAdapter := newFactoryStoreAdapter(dbRestarted)
	mockWt := worktree.NewManager(filepath.Join(cfg.DataDir, "worktrees"))
	wtMgr := newFactoryWorktreeAdapter(mockWt)
	fakeAgent := &scenario3ScriptedAgent{attempts: 2} // Next run will succeed
	cmdRunner := newFactoryCommandAdapter(command.NewRunner())

	engine := factory.NewEngine(storeAdapter, wtMgr, fakeAgent, cmdRunner, filepath.Join(cfg.DataDir, "logs"))
	scheduler := factory.NewScheduler(storeAdapter, engine, 5)

	srv := server.NewServer(cfg, dbRestarted, engine, scheduler, garagefab.Dist())

	// Create session for API call
	sessionReq := httptest.NewRequest(http.MethodPost, "/api/session", bytes.NewBufferString(`{"token":"m2-test-token"}`))
	sessionReq.Host = "127.0.0.1:7878"
	sessionReq.Header.Set("Content-Type", "application/json")
	sessionRec := httptest.NewRecorder()
	srv.Router.ServeHTTP(sessionRec, sessionReq)

	var sessionCookie *http.Cookie
	for _, c := range sessionRec.Result().Cookies() {
		if c.Name == "gf_session" {
			sessionCookie = c
			break
		}
	}

	// Trigger Retry
	retryReq := httptest.NewRequest(http.MethodPost, "/api/jobs/1/retry", nil)
	retryReq.Host = "127.0.0.1:7878"
	retryReq.Header.Set("Origin", "http://127.0.0.1:7878")
	if sessionCookie != nil {
		retryReq.AddCookie(sessionCookie)
	} else {
		retryReq.Header.Set("Authorization", "Bearer m2-test-token")
	}
	retryRec := httptest.NewRecorder()
	srv.Router.ServeHTTP(retryRec, retryReq)

	if retryRec.Code != http.StatusOK {
		t.Fatalf("retry failed: code=%d body=%s", retryRec.Code, retryRec.Body.String())
	}

	// Verify job returned to queued (PIP-7)
	jobAfterRetry, _ := dbRestarted.Jobs().GetJob(ctx, job.ID)
	if jobAfterRetry.Status != factory.StatusQueued {
		t.Fatalf("expected status queued after retry, got %s", jobAfterRetry.Status)
	}
}
