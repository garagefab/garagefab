// Package main contains E2E acceptance tests for Milestone M5.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ENTERPRISE / JAVA SPRING COMPARISON:
// End-to-End System Acceptance Verification (Milestone M5: Real Agents & Skill).
//
// In Clean / Hexagonal Architecture:
// This suite tests the end-to-end integration of all M5 components:
//  1. Agent Strategy Router (worker/agent/router.go) coordinating distinct agent CLIs (HND-2).
//  2. Agy CLI Adapter (worker/agent/agy.go) parsing trailing JSON envelopes (D21).
//  3. OpenCode CLI Adapter (worker/agent/opencode.go) consuming NDJSON event streams (D21).
//  4. Handoff command generation (HND-1, HND-2).
//  5. Interactive Clarification Flow via `gf-api.sh` helper using Bearer auth (HND-4, HND-5, SPC-3).
//  6. Human interactive approval gate enforcing session cookie exclusivity (APR-7, SEC-4).
//  7. Timeout protection killing hanging agent process groups cleanly (COD-10).
//
// Enterprise / Spring Boot Comparison:
// In Spring Boot enterprise applications, comprehensive `@SpringBootTest(webEnvironment = RANDOM_PORT)`
// tests exercise the entire slice from HTTP Controller through Service Layer, Persistence,
// and Mock ProcessExecutors. Here, we instantiate the real HTTP server, SQLite database,
// background scheduler, and git worktrees, pointing PATH to deterministic stub CLI executables.
//
// Go Idiom & Language Concept Bridges:
//   - Isolated Subprocess Environments: `exec.Command` prepends a custom stub directory
//     to `PATH` ensuring tests run deterministically in CI without requiring live cloud LLM APIs.
//   - Graceful Goroutine Teardown: Uses `context.WithCancel` and `scheduler.Close()`
//     to await in-flight worker goroutines before tearing down SQLite pools.
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
	"github.com/garagefab/garagefab/internal/worker/agent"
	"github.com/garagefab/garagefab/internal/worker/command"
	"github.com/garagefab/garagefab/internal/worker/worktree"
)

// createStubAgentBinaries writes executable POSIX shell scripts simulating agy and opencode.
func createStubAgentBinaries(t *testing.T, binDir string) {
	t.Helper()
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir stub bin dir: %v", err)
	}

	// Stub agy CLI:
	// - Simulates agy --print=<prompt> --dangerously-skip-permissions --output-format json
	// - Supports hanging mode when HANG_AGENT is passed
	// - Emits clarification-questions.md on first spec turn
	// - Emits valid spec.md on second spec turn (when clarification history is present)
	// - Emits valid review.json on review turn
	agyScript := `#!/bin/sh
PROMPT=""
for arg in "$@"; do
  case "$arg" in
    --print=*)
      PROMPT="${arg#--print=}"
      ;;
    --version)
      echo "1.2.14"
      exit 0
      ;;
  esac
done

# Timeout simulation mode
case "$PROMPT" in
  *HANG_AGENT*)
    sleep 30
    exit 0
    ;;
esac

# Locate worktree directory from prompt or current working directory
WORKTREE="$(pwd)"
ARTIFACT_DIR=""

# Extract job ID from prompt
JOB_ID=$(echo "$PROMPT" | grep -o 'Job ID: [0-9]*' | awk '{print $3}')
if [ -z "$JOB_ID" ]; then
  JOB_ID="1"
fi
ARTIFACT_DIR="$WORKTREE/.garagefab/jobs/$JOB_ID"
mkdir -p "$ARTIFACT_DIR"

case "$PROMPT" in
  *"spec Task"*|*"Spec Task"*)
    if echo "$PROMPT" | grep -q "Use SQLite"; then
      # Turn 2: clarification provided -> write valid spec.md
      cat << 'SPECEOF' > "$ARTIFACT_DIR/spec.md"
# Specification: User Auth

## Summary
Implement authentication for users.

## Goals and Non-Goals
Goals: Auth login.
Non-Goals: LDAP.

## Design
Hexagonal architecture isolating auth provider.

## Acceptance Criteria
Given valid credentials When login requested Then AC-1: authenticate successfully.

## Implementation Plan
1. Add auth endpoints (AC-1)

## Test Plan
Automated tests.

## Risks and Assumptions
None.
SPECEOF
      echo '{"conversation_id":"stub-agy-spec2","status":"SUCCESS","response":"Spec generated successfully.","duration_seconds":1,"usage":{"total_tokens":150}}'
      exit 0
    else
      # Turn 1: write clarification questions
      cat << 'QEOF' > "$ARTIFACT_DIR/clarification-questions.md"
Q1. What database engine should be used for user storage?
QEOF
      echo '{"conversation_id":"stub-agy-spec1","status":"SUCCESS","response":"Questions emitted.","duration_seconds":1,"usage":{"total_tokens":120}}'
      exit 0
    fi
    ;;
  *"review Task"*|*"Review Task"*)
    cat << 'REVEOF' > "$ARTIFACT_DIR/review.json"
{
  "schema_version": 1,
  "decision": "approve",
  "summary": "Implementation satisfies all requirements cleanly.",
  "risk": {
    "side_effect": {"score": 1, "rationale": "No unintended side effects observed."},
    "performance": {"score": 1, "rationale": "Negligible memory and CPU impact."},
    "backward_compatibility": {"score": 1, "rationale": "Existing APIs preserved."}
  },
  "findings": [],
  "warnings": [],
  "spec_coverage": [
    {"criterion": "AC-1", "status": "met", "note": "Authentication verified."}
  ]
}
REVEOF
    echo '{"conversation_id":"stub-agy-rev","status":"SUCCESS","response":"Review completed and approved.","duration_seconds":1,"usage":{"total_tokens":200}}'
    exit 0
    ;;
  *)
    echo '{"conversation_id":"stub-agy-generic","status":"SUCCESS","response":"Done.","duration_seconds":1,"usage":{"total_tokens":50}}'
    exit 0
    ;;
esac
`

	// Stub opencode CLI:
	// - Simulates opencode run --auto --format json --dir <worktree> <prompt>
	// - Modifies codebase in worktree and emits valid NDJSON events
	opencodeScript := `#!/bin/sh
DIR=""
IS_NEXT=0
for arg in "$@"; do
  if [ "$arg" = "--version" ]; then
    echo "1.18.34"
    exit 0
  fi
  if [ "$IS_NEXT" = "1" ]; then
    DIR="$arg"
    IS_NEXT=0
    continue
  fi
  if [ "$arg" = "--dir" ]; then
    IS_NEXT=1
  fi
done

if [ -z "$DIR" ]; then
  DIR="$(pwd)"
fi

# Implement a valid code change in the target worktree
cat << 'CODEEOF' > "$DIR/auth.go"
package main

// AuthenticateUser verifies dummy credentials.
func AuthenticateUser(user, pass string) bool {
	return user != "" && pass != ""
}
CODEEOF

# Emit real-time NDJSON event stream
echo '{"type":"step_start"}'
echo '{"type":"text","part":{"text":"Created auth.go implementation."}}'
echo '{"type":"step_finish","part":{"reason":"stop","tokens":{"total":220,"input":170}}}'
exit 0
`

	agyPath := filepath.Join(binDir, "agy")
	if err := os.WriteFile(agyPath, []byte(agyScript), 0755); err != nil {
		t.Fatalf("write stub agy: %v", err)
	}

	opencodePath := filepath.Join(binDir, "opencode")
	if err := os.WriteFile(opencodePath, []byte(opencodeScript), 0755); err != nil {
		t.Fatalf("write stub opencode: %v", err)
	}
}

// setupGitRepo creates a valid test Git repository with origin/main and project.yaml.
func setupGitRepo(t *testing.T, repoDir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v, out: %s", args, err, string(out))
		}
	}

	run("init")
	run("config", "user.name", "Garagefab Stub Test")
	run("config", "user.email", "test@garagefab.dev")
	run("config", "commit.gpgSign", "false")

	// Create dummy project files
	goMod := "module example.com/testproject\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	mainGo := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	mainTestGo := "package main\n\nimport \"testing\"\n\nfunc TestMainFunc(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(repoDir, "main_test.go"), []byte(mainTestGo), 0644); err != nil {
		t.Fatalf("write main_test.go: %v", err)
	}

	// Create .garagefab/project.yaml designating agy for spec & review, opencode for coding
	gfDir := filepath.Join(repoDir, ".garagefab")
	if err := os.MkdirAll(gfDir, 0755); err != nil {
		t.Fatalf("mkdir .garagefab: %v", err)
	}

	projectYAML := `base_ref: origin/main
agents:
  spec: agy
  coding: opencode
  review: agy
commands:
  build: ["go build ./..."]
  test: ["go test ./..."]
`
	if err := os.WriteFile(filepath.Join(gfDir, "project.yaml"), []byte(projectYAML), 0644); err != nil {
		t.Fatalf("write project.yaml: %v", err)
	}

	run("add", ".")
	run("commit", "-m", "Initial commit")
	run("update-ref", "refs/remotes/origin/main", "HEAD")
}

// TestE2E_M5_MultiAgentPipeline_HND1_HND6 verifies the full multi-agent SDLC workflow:
// Spec (agy) -> Clarification Questions -> POST clarification via gf-api.sh -> Spec (agy) -> Spec Review ->
// Approve Spec -> Coding (opencode) -> Review (agy) -> Gate Awaiting Approval -> Approve -> Done.
func TestE2E_M5_MultiAgentPipeline_HND1_HND6(t *testing.T) {
	tempDir := t.TempDir()
	stubBinDir := filepath.Join(tempDir, "stub-bin")
	createStubAgentBinaries(t, stubBinDir)

	// Prepend stub binaries directory to PATH for this test execution
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", stubBinDir+string(filepath.ListSeparator)+origPath)

	repoDir := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	setupGitRepo(t, repoDir)

	// Set up Garagefab environment
	cfgDir := filepath.Join(tempDir, "data")
	cfg, err := config.Load(cfgDir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	t.Setenv("GARAGEFAB_HOME", cfgDir)

	dbPath := filepath.Join(cfg.DataDir, "garagefab.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	// Register project in DB
	proj := &store.Project{
		Name:             "m5-multi-agent-project",
		RepoPath:         repoDir,
		BaseRef:          "origin/main",
		EnabledWorkTypes: []string{store.WorkTypeFeature},
	}
	if err := db.Projects().CreateProject(context.Background(), proj); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Wire multi-agent router with agy and opencode adapters
	agentRouter := agent.NewRouter()
	agentRouter.Register("agy", agent.NewAgyRunner("agy", cfg.Engine.EnvPassthrough))
	agentRouter.Register("opencode", agent.NewOpenCodeRunner("opencode", cfg.Engine.EnvPassthrough))
	agentAdapter := newFactoryAgentAdapter(agentRouter)

	storeAdapter := newFactoryStoreAdapter(db)
	wtMgr := newFactoryWorktreeAdapter(worktree.NewManager(filepath.Join(cfg.DataDir, "worktrees")))
	cmdRunner := newFactoryCommandAdapter(command.NewRunner())
	guardrailAdapter := newFactoryGuardrailAdapter()
	projCfgAdapter := newFactoryProjectConfigAdapter()

	engine := factory.NewEngine(storeAdapter, wtMgr, agentAdapter, cmdRunner, filepath.Join(cfg.DataDir, "logs"))
	engine.SetGuardrailRunner(guardrailAdapter)
	engine.SetProjectConfigProvider(projCfgAdapter)
	engine.SetMaxRepairAttempts(1)
	engine.SetDefaultAgentTimeout(cfg.Engine.StepTimeouts.Agent)

	scheduler := factory.NewScheduler(storeAdapter, engine, 1)
	scheduler.SetProjectConfigProvider(projCfgAdapter)

	schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
	defer func() {
		cancelScheduler()
		scheduler.Close()
	}()
	go scheduler.Start(schedulerCtx)

	srv := server.NewServer(cfg, db, engine, scheduler, garagefab.Dist())
	ts := httptest.NewServer(srv.Router)
	defer ts.Close()

	// Update config.yaml with real test server listen address so gf-api.sh targets ts.URL
	listenAddr := strings.TrimPrefix(ts.URL, "http://")
	cfgContent := fmt.Sprintf("server:\n  listen: %s\n  api_token: %s\n", listenAddr, cfg.Server.APIToken)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfgContent), 0600); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}

	// Create authenticated HTTP client with session cookie
	cookieReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/session", strings.NewReader(fmt.Sprintf(`{"token":"%s"}`, cfg.Server.APIToken)))
	cookieReq.Header.Set("Content-Type", "application/json")
	cookieResp, err := http.DefaultClient.Do(cookieReq)
	if err != nil || cookieResp.StatusCode != http.StatusOK {
		t.Fatalf("session login failed: %v", err)
	}
	var sessionCookie *http.Cookie
	for _, c := range cookieResp.Cookies() {
		if c.Name == "gf_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("session cookie not returned")
	}

	sessionClient := &http.Client{}

	// 1. Submit Feature Job via HTTP API
	jobPayload := fmt.Sprintf(`{"project_id":%d,"work_type":"feature","title":"Add Auth","intent":"Add user auth module"}`, proj.ID)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/jobs", strings.NewReader(jobPayload))
	req.Header.Set("Authorization", "Bearer "+cfg.Server.APIToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || (resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated) {
		t.Fatalf("create job failed: %v, status: %d", err, resp.StatusCode)
	}
	var createdJob struct {
		ID int64 `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&createdJob)
	resp.Body.Close()
	jobID := createdJob.ID

	// 2. Await stage 02 / needs_clarification (HND-1, HND-2)
	var jobRecord *store.Job
	for i := 0; i < 60; i++ {
		time.Sleep(100 * time.Millisecond)
		j, err := db.Jobs().GetJob(context.Background(), jobID)
		if err == nil && j.Status == store.StatusNeedsClarification {
			jobRecord = j
			break
		}
	}
	if jobRecord == nil {
		t.Fatalf("job did not enter needs_clarification status")
	}

	// Verify handoff command formatting from API (HND-1, HND-2)
	jobReq, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/jobs/%d", ts.URL, jobID), nil)
	jobReq.Header.Set("Authorization", "Bearer "+cfg.Server.APIToken)
	jobResp, err := http.DefaultClient.Do(jobReq)
	if err != nil || jobResp.StatusCode != http.StatusOK {
		t.Fatalf("get job failed: %v", err)
	}
	var jobDTO struct {
		HandoffCommand string `json:"handoff_command"`
	}
	_ = json.NewDecoder(jobResp.Body).Decode(&jobDTO)
	jobResp.Body.Close()

	expectedHandoff := fmt.Sprintf("cd %s ; agy -i \"Activate caveman mode. garagefab-work %d\"", repoDir, jobID)
	if jobDTO.HandoffCommand != expectedHandoff {
		t.Errorf("handoff_command = %q, want %q", jobDTO.HandoffCommand, expectedHandoff)
	}

	// 3. Submit Clarification Answers via gf-api.sh script using Bearer token (HND-4, HND-5, SPC-3)
	scriptPath, err := filepath.Abs("skills/garagefab-work/scripts/gf-api.sh")
	if err != nil {
		t.Fatalf("abs scriptPath: %v", err)
	}

	answersBody := `{"answers":[{"q":1,"answer":"Use SQLite"}]}`
	apiCmd := exec.CommandContext(context.Background(), scriptPath, "POST", fmt.Sprintf("/api/jobs/%d/clarification", jobID), answersBody)
	apiCmd.Env = append(os.Environ(), "HOME="+tempDir, "GARAGEFAB_HOME="+cfgDir)
	var apiOut, apiErr bytes.Buffer
	apiCmd.Stdout = &apiOut
	apiCmd.Stderr = &apiErr

	if err := apiCmd.Run(); err != nil {
		t.Fatalf("gf-api.sh failed: %v, stderr: %s", err, apiErr.String())
	}

	// 4. Await stage 02 / spec_review
	var specReviewJob *store.Job
	for i := 0; i < 60; i++ {
		time.Sleep(100 * time.Millisecond)
		j, err := db.Jobs().GetJob(context.Background(), jobID)
		if err == nil && j.Status == store.StatusSpecReview {
			specReviewJob = j
			break
		}
	}
	if specReviewJob == nil {
		t.Fatalf("job did not enter spec_review status after clarification")
	}

	// 5. Approve Spec using Session Cookie (SPC-6)
	approveSpecReq, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/jobs/%d/approve", ts.URL, jobID), strings.NewReader("{}"))
	approveSpecReq.Header.Set("Content-Type", "application/json")
	approveSpecReq.AddCookie(sessionCookie)
	approveSpecResp, err := sessionClient.Do(approveSpecReq)
	if err != nil || approveSpecResp.StatusCode != http.StatusOK {
		t.Fatalf("approve spec failed: %v, status: %d", err, approveSpecResp.StatusCode)
	}
	approveSpecResp.Body.Close()

	// 6. Await stage 06 / awaiting_approval
	// Job executes Coding (opencode) -> Build/Test -> Review (agy) -> Gate
	var gateJob *store.Job
	for i := 0; i < 80; i++ {
		time.Sleep(100 * time.Millisecond)
		j, err := db.Jobs().GetJob(context.Background(), jobID)
		if err == nil && j.Stage == store.StageHumanApprovalGate && j.Status == store.StatusAwaitingApproval {
			gateJob = j
			break
		}
	}
	if gateJob == nil {
		t.Fatalf("job did not reach human approval gate")
	}

	// 7. Verify gate handoff command: uses stage 06 review/coding agent (HND-2)
	gateJobReq, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/jobs/%d", ts.URL, jobID), nil)
	gateJobReq.Header.Set("Authorization", "Bearer "+cfg.Server.APIToken)
	gateJobResp, _ := http.DefaultClient.Do(gateJobReq)
	var gateJobDTO struct {
		HandoffCommand string `json:"handoff_command"`
	}
	_ = json.NewDecoder(gateJobResp.Body).Decode(&gateJobDTO)
	gateJobResp.Body.Close()
	if !strings.Contains(gateJobDTO.HandoffCommand, "garagefab-work") {
		t.Errorf("gate handoff missing command: %s", gateJobDTO.HandoffCommand)
	}

	// 8. Final Approval via Session Cookie
	finalApproveReq, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/jobs/%d/approve", ts.URL, jobID), strings.NewReader("{}"))
	finalApproveReq.Header.Set("Content-Type", "application/json")
	finalApproveReq.AddCookie(sessionCookie)
	finalApproveResp, err := sessionClient.Do(finalApproveReq)
	if err != nil || finalApproveResp.StatusCode != http.StatusOK {
		t.Fatalf("final approve failed: %v", err)
	}
	finalApproveResp.Body.Close()

	// 9. Await stage 07 / done
	var doneJob *store.Job
	for i := 0; i < 60; i++ {
		time.Sleep(100 * time.Millisecond)
		j, err := db.Jobs().GetJob(context.Background(), jobID)
		if err == nil && j.Stage == store.StageDone && j.Status == store.StatusDone {
			doneJob = j
			break
		}
	}
	if doneJob == nil {
		t.Fatalf("job did not complete to 07_Done")
	}
}

// TestE2E_M5_Timeout_COD10 verifies requirement COD-10:
// When an agent process hangs or exceeds the configured timeout boundary,
// the process group is terminated cleanly and the job transitions to Blocked.
func TestE2E_M5_Timeout_COD10(t *testing.T) {
	tempDir := t.TempDir()
	stubBinDir := filepath.Join(tempDir, "stub-bin")
	createStubAgentBinaries(t, stubBinDir)

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", stubBinDir+string(filepath.ListSeparator)+origPath)

	repoDir := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	setupGitRepo(t, repoDir)

	cfgDir := filepath.Join(tempDir, "data")
	cfg, err := config.Load(cfgDir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	dbPath := filepath.Join(cfg.DataDir, "garagefab.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	proj := &store.Project{
		Name:             "m5-timeout-project",
		RepoPath:         repoDir,
		BaseRef:          "origin/main",
		EnabledWorkTypes: []string{store.WorkTypeFeature},
	}
	if err := db.Projects().CreateProject(context.Background(), proj); err != nil {
		t.Fatalf("create project: %v", err)
	}

	agentRouter := agent.NewRouter()
	agentRouter.Register("agy", agent.NewAgyRunner("agy", cfg.Engine.EnvPassthrough))
	agentAdapter := newFactoryAgentAdapter(agentRouter)

	storeAdapter := newFactoryStoreAdapter(db)
	wtMgr := newFactoryWorktreeAdapter(worktree.NewManager(filepath.Join(cfg.DataDir, "worktrees")))
	cmdRunner := newFactoryCommandAdapter(command.NewRunner())
	guardrailAdapter := newFactoryGuardrailAdapter()
	projCfgAdapter := newFactoryProjectConfigAdapter()

	engine := factory.NewEngine(storeAdapter, wtMgr, agentAdapter, cmdRunner, filepath.Join(cfg.DataDir, "logs"))
	engine.SetGuardrailRunner(guardrailAdapter)
	engine.SetProjectConfigProvider(projCfgAdapter)
	engine.SetDefaultAgentTimeout(500 * time.Millisecond) // Short timeout to test COD-10 boundary

	scheduler := factory.NewScheduler(storeAdapter, engine, 1)
	scheduler.SetProjectConfigProvider(projCfgAdapter)

	schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
	defer func() {
		cancelScheduler()
		scheduler.Close()
	}()
	go scheduler.Start(schedulerCtx)

	srv := server.NewServer(cfg, db, engine, scheduler, garagefab.Dist())
	ts := httptest.NewServer(srv.Router)
	defer ts.Close()

	// Create job with HANG_AGENT in the intent to trigger stub hanging
	jobPayload := fmt.Sprintf(`{"project_id":%d,"work_type":"feature","title":"Hang Job","intent":"HANG_AGENT simulation"}`, proj.ID)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/jobs", strings.NewReader(jobPayload))
	req.Header.Set("Authorization", "Bearer "+cfg.Server.APIToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || (resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated) {
		t.Fatalf("create job failed: %v, status: %d", err, resp.StatusCode)
	}
	var createdJob struct {
		ID int64 `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&createdJob)
	resp.Body.Close()
	jobID := createdJob.ID

	// Await job reaching Failed status due to agent timeout (FailureBlocked)
	var failedJob *store.Job
	for i := 0; i < 60; i++ {
		time.Sleep(100 * time.Millisecond)
		j, err := db.Jobs().GetJob(context.Background(), jobID)
		if err == nil && j.Status == store.StatusFailed {
			failedJob = j
			break
		}
	}

	if failedJob == nil {
		t.Fatalf("job did not enter Failed status upon agent timeout")
	}

	// Verify step run recorded with FailureBlocked
	steps, err := db.StepRuns().ListStepRunsByJob(context.Background(), jobID)
	if err != nil || len(steps) == 0 {
		t.Fatalf("fetch steps: %v", err)
	}
	lastStep := steps[len(steps)-1]
	if lastStep.FailureCategory != store.FailureBlocked {
		t.Errorf("step failure category = %s, want %s", lastStep.FailureCategory, store.FailureBlocked)
	}
}
