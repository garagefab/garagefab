// Package factory_test contains isolated unit tests for the factory engine repair loop,
// guardrails, cancel, and retry actions (COD-3..7, GRD-1..4, PIP-6/7, WKT-5).
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Pipeline Engine Repair Loop & Guardrails Tests.
//
// Verifies:
//  1. Repair Loop (COD-4, COD-5, COD-6): Automated retry with feedback on Flawed failure,
//     re-running all verification command groups from the start.
//  2. Manual Escalation (COD-4): Exhaustion of max_repair_attempts marks job failed.
//  3. Empty Diff (COD-7): Agent exiting 0 without making file changes is rejected as Flawed.
//  4. Guardrails (GRD-1, GRD-4): Tampering with protected files triggers Flawed with feedback.
//  5. Cancel Action (PIP-6): Aborts active job and marks cancelled.
//  6. Retry Action (PIP-7, WKT-5): Resets worktree to checkpoint and requeues job.
//
// ==============================================================================
package factory_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/garagefab/garagefab/internal/factory"
)

// ScriptableAgentRunner allows configuring per-attempt outcomes.
type ScriptableAgentRunner struct {
	mu          sync.Mutex
	invocations []factory.AgentRequest
	// results returns the result for the n-th invocation (0-indexed)
	results func(n int, req factory.AgentRequest) (*factory.AgentResult, error)
}

func (s *ScriptableAgentRunner) Run(ctx context.Context, req factory.AgentRequest) (*factory.AgentResult, error) {
	s.mu.Lock()
	n := len(s.invocations)
	s.invocations = append(s.invocations, req)
	s.mu.Unlock()

	if req.OnProcessStart != nil {
		req.OnProcessStart(100+n, 100+n, time.Now().Unix())
	}
	if s.results != nil {
		return s.results(n, req)
	}
	return &factory.AgentResult{ExitCode: 0, Summary: "ok"}, nil
}

// ScriptableCommandRunner allows configuring per-command results.
type ScriptableCommandRunner struct {
	mu          sync.Mutex
	invocations []factory.CommandOptions
	handler     func(opts factory.CommandOptions) (*factory.CommandResult, error)
}

func (c *ScriptableCommandRunner) Run(ctx context.Context, opts factory.CommandOptions) (*factory.CommandResult, error) {
	c.mu.Lock()
	c.invocations = append(c.invocations, opts)
	c.mu.Unlock()

	if opts.OnProcessStart != nil {
		opts.OnProcessStart(200, 200, time.Now().Unix())
	}
	if c.handler != nil {
		return c.handler(opts)
	}
	return &factory.CommandResult{ExitCode: 0}, nil
}

// MockGuardrailRunner implements factory.GuardrailRunner.
type MockGuardrailRunner struct {
	violations []factory.GuardrailViolation
}

func (m *MockGuardrailRunner) CheckProtectedPaths(ctx context.Context, workDir, stepStartSHA string, patterns []string) ([]factory.GuardrailViolation, error) {
	return m.violations, nil
}

// MockProjectConfigProvider implements factory.ProjectConfigProvider.
type MockProjectConfigProvider struct {
	cfg *factory.ProjectConfig
}

func (m *MockProjectConfigProvider) GetProjectConfig(ctx context.Context, repoPath string) (*factory.ProjectConfig, error) {
	if m.cfg != nil && m.cfg.Agents == nil {
		m.cfg.Agents = map[string]string{
			factory.RoleSpec:   "fake",
			factory.RoleProbe:  "fake",
			factory.RoleCoding: "fake",
			factory.RoleReview: "fake",
		}
	}
	return m.cfg, nil
}

// TestEngine_RepairLoop_Flawed_Then_Pass_COD4_COD5_COD6 verifies that a test command failure
// causes a repair run with previous error output in the prompt (COD-1), re-runs all commands (COD-6),
// and successfully advances the job once fixed (COD-5).
func TestEngine_RepairLoop_Flawed_Then_Pass_COD4_COD5_COD6(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "test-proj", RepoPath: "/repo", BaseRef: "main"}
	store.jobs[1] = &factory.Job{ID: 1, ProjectID: 1, Stage: factory.StageIntent, Status: factory.StatusQueued, Intent: "fix bug"}

	wtMgr := newMockWorktreeManager()

	agent := &ScriptableAgentRunner{}
	cmdRunner := &ScriptableCommandRunner{}

	// Test command fails on attempt 1, passes on attempt 2
	var testRuns int
	cmdRunner.handler = func(opts factory.CommandOptions) (*factory.CommandResult, error) {
		if strings.Contains(opts.Command, "test") {
			testRuns++
			if testRuns == 1 {
				return &factory.CommandResult{
					ExitCode: 1,
					Stdout:   "--- FAIL: TestCalc (0.01s)",
					Stderr:   "assertion failed: expected 4 got 5",
				}, nil
			}
		}
		return &factory.CommandResult{ExitCode: 0}, nil
	}

	projCfg := &factory.ProjectConfig{
		Commands: factory.ProjectCommands{
			Build: []string{"go build ./..."},
			Test:  []string{"go test ./..."},
			Lint:  []string{"golangci-lint run"},
		},
	}

	engine := factory.NewEngine(store, wtMgr, agent, cmdRunner, t.TempDir())
	engine.SetProjectConfigProvider(&MockProjectConfigProvider{cfg: projCfg})
	engine.SetMaxRepairAttempts(3)

	err := engine.ExecuteJob(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify coding agent ran twice (Attempt 1 + Repair Attempt 2)
	var codingRuns int
	var prompt2 string
	agent.mu.Lock()
	for _, inv := range agent.invocations {
		if inv.Stage == factory.StageCoding {
			codingRuns++
			if codingRuns == 2 {
				prompt2 = inv.Prompt
			}
		}
	}
	agent.mu.Unlock()

	if codingRuns != 2 {
		t.Fatalf("expected 2 coding agent runs (1 initial + 1 repair), got %d", codingRuns)
	}

	// COD-1: Verify repair prompt contained previous test error feedback
	if !strings.Contains(prompt2, "assertion failed: expected 4 got 5") {
		t.Fatalf("expected repair prompt to include test error output, got: %s", prompt2)
	}

	// COD-6: All command groups ran again from the start (build ran on attempt 1 AND attempt 2)
	var buildCount int
	cmdRunner.mu.Lock()
	for _, inv := range cmdRunner.invocations {
		if strings.Contains(inv.Command, "build") {
			buildCount++
		}
	}
	cmdRunner.mu.Unlock()

	if buildCount != 2 {
		t.Fatalf("expected build command to re-run on attempt 2 (COD-6), but ran %d times", buildCount)
	}

	// Verify job reached StageHumanApprovalGate
	job, _ := store.GetJob(ctx, 1)
	if job.Stage != factory.StageHumanApprovalGate || job.Status != factory.StatusAwaitingApproval {
		t.Fatalf("expected job at 06_Human_Approval_Gate/awaiting_approval, got %s/%s", job.Stage, job.Status)
	}
}

// TestEngine_RepairLoop_Exhausted_Manual_COD4 verifies that failing all repair attempts
// escalates the job to StatusFailed with category Manual.
func TestEngine_RepairLoop_Exhausted_Manual_COD4(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "test-proj", RepoPath: "/repo", BaseRef: "main"}
	store.jobs[1] = &factory.Job{ID: 1, ProjectID: 1, Stage: factory.StageIntent, Status: factory.StatusQueued, Intent: "fix bug"}

	wtMgr := newMockWorktreeManager()
	agent := &ScriptableAgentRunner{}

	// Command always fails
	cmdRunner := &ScriptableCommandRunner{
		handler: func(opts factory.CommandOptions) (*factory.CommandResult, error) {
			return &factory.CommandResult{ExitCode: 1, Stderr: "syntax error"}, nil
		},
	}

	projCfg := &factory.ProjectConfig{
		Commands: factory.ProjectCommands{
			Build: []string{"go build ./..."},
		},
	}

	engine := factory.NewEngine(store, wtMgr, agent, cmdRunner, t.TempDir())
	engine.SetProjectConfigProvider(&MockProjectConfigProvider{cfg: projCfg})
	engine.SetMaxRepairAttempts(3)

	err := engine.ExecuteJob(ctx, 1)
	if err == nil {
		t.Fatal("expected ExecuteJob to fail when attempts exhausted, but succeeded")
	}

	job, _ := store.GetJob(ctx, 1)
	if job.Status != factory.StatusFailed {
		t.Fatalf("expected job status %s, got %s", factory.StatusFailed, job.Status)
	}

	// Verify 3 attempts ran
	agent.mu.Lock()
	runs := len(agent.invocations)
	agent.mu.Unlock()
	if runs != 3 {
		t.Fatalf("expected 3 agent attempts before Manual escalation, got %d", runs)
	}
}

// TestEngine_EmptyDiff_Flawed_COD7 verifies that an agent exiting 0 without making file changes
// is categorized as Flawed and triggers the repair loop (COD-7).
func TestEngine_EmptyDiff_Flawed_COD7(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "test-proj", RepoPath: "/repo", BaseRef: "main"}
	store.jobs[1] = &factory.Job{ID: 1, ProjectID: 1, Stage: factory.StageIntent, Status: factory.StatusQueued, Intent: "refactor code"}

	wtMgr := newMockWorktreeManager()

	var diffCalls int
	// On attempt 1 diff is empty; on attempt 2 diff is present
	agent := &ScriptableAgentRunner{}
	wtMgrDiff := func() string {
		diffCalls++
		if diffCalls == 1 {
			return "" // Empty diff (COD-7)
		}
		return "diff --git a/file.go b/file.go\n+added code"
	}

	mockWt := &customDiffWorktreeManager{
		MockWorktreeManager: wtMgr,
		diffFn:              wtMgrDiff,
	}

	cmdRunner := &ScriptableCommandRunner{}
	engine := factory.NewEngine(store, mockWt, agent, cmdRunner, t.TempDir())
	engine.SetMaxRepairAttempts(3)

	err := engine.ExecuteJob(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var codingRuns int
	var prompt2 string
	agent.mu.Lock()
	for _, inv := range agent.invocations {
		if inv.Stage == factory.StageCoding {
			codingRuns++
			if codingRuns == 2 {
				prompt2 = inv.Prompt
			}
		}
	}
	agent.mu.Unlock()

	if codingRuns != 2 {
		t.Fatalf("expected 2 coding agent runs, got %d", codingRuns)
	}

	if !strings.Contains(prompt2, "empty diff") {
		t.Fatalf("expected repair prompt to complain about empty diff, got: %s", prompt2)
	}
}

type customDiffWorktreeManager struct {
	*MockWorktreeManager
	diffFn func() string
}

func (m *customDiffWorktreeManager) Diff(ctx context.Context, worktreePath, baseSHA string) (string, error) {
	if baseSHA == "head123" || strings.HasPrefix(baseSHA, "sha-") {
		return m.MockWorktreeManager.Diff(ctx, worktreePath, baseSHA)
	}
	if m.diffFn != nil {
		return m.diffFn(), nil
	}
	return m.MockWorktreeManager.Diff(ctx, worktreePath, baseSHA)
}

// TestEngine_Guardrail_Violation_GRD1_GRD4 verifies that modifying a protected file triggers
// a guardrail violation, which is Flawed once per attempt, and prompts the agent to restore it.
func TestEngine_Guardrail_Violation_GRD1_GRD4(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "test-proj", RepoPath: "/repo", BaseRef: "main"}
	store.jobs[1] = &factory.Job{ID: 1, ProjectID: 1, Stage: factory.StageIntent, Status: factory.StatusQueued, Intent: "fix bug"}

	wtMgr := newMockWorktreeManager()
	agent := &ScriptableAgentRunner{}
	cmdRunner := &ScriptableCommandRunner{}

	var guardrailCalls int
	guardrails := &dynamicGuardrailRunner{
		checkFn: func() []factory.GuardrailViolation {
			guardrailCalls++
			if guardrailCalls == 1 {
				return []factory.GuardrailViolation{
					{Path: "auth_test.go", Status: "M"},
				}
			}
			return nil // Fixed on attempt 2
		},
	}

	projCfg := &factory.ProjectConfig{
		Guardrails: factory.ProjectGuardrails{
			ProtectedPaths: []string{"**/*_test.go"},
		},
	}

	engine := factory.NewEngine(store, wtMgr, agent, cmdRunner, t.TempDir())
	engine.SetGuardrailRunner(guardrails)
	engine.SetProjectConfigProvider(&MockProjectConfigProvider{cfg: projCfg})
	engine.SetMaxRepairAttempts(3)

	err := engine.ExecuteJob(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var codingRuns int
	var prompt2 string
	agent.mu.Lock()
	for _, inv := range agent.invocations {
		if inv.Stage == factory.StageCoding {
			codingRuns++
			if codingRuns == 2 {
				prompt2 = inv.Prompt
			}
		}
	}
	agent.mu.Unlock()

	if codingRuns != 2 {
		t.Fatalf("expected 2 coding agent runs, got %d", codingRuns)
	}

	if !strings.Contains(prompt2, "Guardrail violation: you modified existing protected file(s): auth_test.go") {
		t.Fatalf("expected guardrail violation feedback in repair prompt, got: %s", prompt2)
	}
}

type dynamicGuardrailRunner struct {
	checkFn func() []factory.GuardrailViolation
}

func (d *dynamicGuardrailRunner) CheckProtectedPaths(ctx context.Context, workDir, stepStartSHA string, patterns []string) ([]factory.GuardrailViolation, error) {
	if d.checkFn != nil {
		return d.checkFn(), nil
	}
	return nil, nil
}

// TestEngine_Cancel_TerminatesJob_PIP6 verifies that cancelling an active or queued job
// cleans up worktrees and marks status cancelled (PIP-6).
func TestEngine_Cancel_TerminatesJob_PIP6(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "test-proj", RepoPath: "/repo", BaseRef: "main"}
	store.jobs[1] = &factory.Job{
		ID:           1,
		ProjectID:    1,
		Stage:        factory.StageCoding,
		Status:       factory.StatusRunning,
		WorktreePath: "/tmp/worktree/1",
		BranchName:   "garagefab/job-1",
	}

	wtMgr := newMockWorktreeManager()
	engine := factory.NewEngine(store, wtMgr, &ScriptableAgentRunner{}, &ScriptableCommandRunner{}, t.TempDir())

	err := engine.Cancel(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	job, _ := store.GetJob(ctx, 1)
	if job.Status != factory.StatusCancelled {
		t.Fatalf("expected status %s, got %s", factory.StatusCancelled, job.Status)
	}

	// Attempting to cancel an already cancelled job returns ErrInvalidState (PIP-3)
	err = engine.Cancel(ctx, 1)
	if err == nil {
		t.Fatal("expected error cancelling already cancelled job, got nil")
	}
}

// TestEngine_Retry_ResetsCheckpoint_PIP7_WKT5 verifies that retrying a failed job
// rolls back the worktree to the checkpoint commit (WKT-5) and requeues the job (PIP-7).
func TestEngine_Retry_ResetsCheckpoint_PIP7_WKT5(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "test-proj", RepoPath: "/repo", BaseRef: "main"}
	store.jobs[1] = &factory.Job{
		ID:           1,
		ProjectID:    1,
		Stage:        factory.StageCoding,
		Status:       factory.StatusFailed,
		WorktreePath: "/tmp/worktree/1",
		HeadSHA:      "checkpoint123",
	}

	wtMgr := newMockWorktreeManager()
	engine := factory.NewEngine(store, wtMgr, &ScriptableAgentRunner{}, &ScriptableCommandRunner{}, t.TempDir())

	err := engine.Retry(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wtMgr.resetCount != 1 {
		t.Fatalf("expected 1 worktree reset call, got %d", wtMgr.resetCount)
	}

	job, _ := store.GetJob(ctx, 1)
	if job.Status != factory.StatusQueued {
		t.Fatalf("expected job status queued, got %s", job.Status)
	}
}

// TestCodingStep_AgentTimeout_COD10 verifies COD-10:
// A fake runner returning TimedOut: true makes the step failed with category Blocked
// and executes no repair attempts.
func TestCodingStep_AgentTimeout_COD10(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "test-proj", RepoPath: "/repo", BaseRef: "main"}
	store.jobs[1] = &factory.Job{
		ID:           1,
		ProjectID:    1,
		Stage:        factory.StageCoding,
		Status:       factory.StatusQueued,
		WorktreePath: "/tmp/worktree/1",
		BaseSHA:      "base123",
		HeadSHA:      "base123",
	}

	wtMgr := newMockWorktreeManager()
	agent := &ScriptableAgentRunner{
		results: func(n int, req factory.AgentRequest) (*factory.AgentResult, error) {
			return &factory.AgentResult{
				ExitCode: 1,
				TimedOut: true,
				Summary:  "timed out",
			}, nil
		},
	}
	cmdRunner := &ScriptableCommandRunner{}

	projCfg := &factory.ProjectConfig{
		BaseRef: "main",
		Agents: map[string]string{
			factory.RoleCoding: "opencode",
		},
		AgentTimeout: 10 * time.Minute,
	}

	engine := factory.NewEngine(store, wtMgr, agent, cmdRunner, t.TempDir())
	engine.SetProjectConfigProvider(&MockProjectConfigProvider{cfg: projCfg})
	engine.SetMaxRepairAttempts(3)

	err := engine.ExecuteJob(ctx, 1)
	if err == nil {
		t.Fatal("expected ExecuteJob to fail on agent timeout, but got nil")
	}

	// 1. Verify only 1 attempt was made (no repair loop on Blocked)
	if len(agent.invocations) != 1 {
		t.Fatalf("expected exactly 1 agent invocation on timeout, got %d", len(agent.invocations))
	}

	// 2. Verify request fields passed to agent
	req := agent.invocations[0]
	if req.Role != factory.RoleCoding {
		t.Errorf("expected Role %q, got %q", factory.RoleCoding, req.Role)
	}
	if req.Agent != "opencode" {
		t.Errorf("expected Agent 'opencode', got %q", req.Agent)
	}
	if req.Timeout != 10*time.Minute {
		t.Errorf("expected Timeout 10m, got %v", req.Timeout)
	}

	// 3. Verify step run recorded with FailureBlocked
	stepRuns, _ := store.ListStepRunsByJob(ctx, 1)
	if len(stepRuns) == 0 {
		t.Fatal("expected at least one step run")
	}
	lastStep := stepRuns[len(stepRuns)-1]
	if lastStep.Status != factory.StepStatusFail {
		t.Errorf("expected step status fail, got %s", lastStep.Status)
	}
	if lastStep.FailureCategory != factory.FailureBlocked {
		t.Errorf("expected failure category Blocked, got %s", lastStep.FailureCategory)
	}

	// 4. Verify job state is failed
	job, _ := store.GetJob(ctx, 1)
	if job.Status != factory.StatusFailed {
		t.Errorf("expected job status failed, got %s", job.Status)
	}
}

// TestCodingStep_MissingAgent_Blocked verifies that when no agent is configured for a role,
// the step fails immediately as Blocked with the required error message.
func TestCodingStep_MissingAgent_Blocked(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "test-proj", RepoPath: "/repo", BaseRef: "main"}
	store.jobs[1] = &factory.Job{
		ID:           1,
		ProjectID:    1,
		Stage:        factory.StageCoding,
		Status:       factory.StatusQueued,
		WorktreePath: "/tmp/worktree/1",
		BaseSHA:      "base123",
		HeadSHA:      "base123",
	}

	wtMgr := newMockWorktreeManager()
	agent := &ScriptableAgentRunner{}
	cmdRunner := &ScriptableCommandRunner{}

	// ProjectConfig with empty agents
	projCfg := &factory.ProjectConfig{
		BaseRef: "main",
		Agents:  map[string]string{}, // coding unconfigured
	}

	engine := factory.NewEngine(store, wtMgr, agent, cmdRunner, t.TempDir())
	engine.SetProjectConfigProvider(&MockProjectConfigProvider{cfg: projCfg})

	err := engine.ExecuteJob(ctx, 1)
	if err == nil {
		t.Fatal("expected ExecuteJob to fail when agent role unconfigured, got nil")
	}

	expectedMsg := `no agent configured for role "coding" (set agents.coding in .garagefab/project.yaml)`
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("expected error containing %q, got %q", expectedMsg, err.Error())
	}

	// Agent runner was never invoked
	if len(agent.invocations) != 0 {
		t.Errorf("expected 0 agent invocations, got %d", len(agent.invocations))
	}

	// Job state is failed
	job, _ := store.GetJob(ctx, 1)
	if job.Status != factory.StatusFailed {
		t.Errorf("expected job status failed, got %s", job.Status)
	}
}
