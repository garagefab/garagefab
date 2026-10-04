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

func (m *MockGuardrailRunner) CheckProbeScope(ctx context.Context, workDir, stepStartSHA string, testPatterns []string, artifactGlob string) ([]factory.GuardrailViolation, error) {
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

	// GRD-4: the guardrail-failed attempt must be persisted as fail/Flawed, not success,
	// so the repair attempt has a discoverable cause in step_runs.
	stepRuns, _ := store.ListStepRunsByJob(ctx, 1)
	var codingAgentSteps []*factory.StepRun
	for _, s := range stepRuns {
		if s.Stage == factory.StageCoding && s.Kind == factory.StepKindAgent {
			codingAgentSteps = append(codingAgentSteps, s)
		}
	}
	if len(codingAgentSteps) != 2 {
		t.Fatalf("expected 2 coding agent step runs, got %d", len(codingAgentSteps))
	}
	if codingAgentSteps[0].Status != factory.StepStatusFail || codingAgentSteps[0].FailureCategory != factory.FailureFlawed {
		t.Errorf("attempt 1 guardrail violation must be recorded fail/Flawed, got status=%s category=%s",
			codingAgentSteps[0].Status, codingAgentSteps[0].FailureCategory)
	}
	if codingAgentSteps[1].Status != factory.StepStatusSuccess {
		t.Errorf("attempt 2 must be recorded success, got status=%s", codingAgentSteps[1].Status)
	}
}

// TestEngine_GuardrailViolation_Terminal_RecordsFail_GRD4 verifies that when a guardrail
// violation is terminal (no repair budget left), the coding agent step run is persisted as
// failed with a category instead of success, matching the job's failed state (GRD-4).
func TestEngine_GuardrailViolation_Terminal_RecordsFail_GRD4(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.projects[1] = &factory.Project{ID: 1, Name: "test-proj", RepoPath: "/repo", BaseRef: "main"}
	store.jobs[1] = &factory.Job{ID: 1, ProjectID: 1, Stage: factory.StageIntent, Status: factory.StatusQueued, Intent: "fix bug"}

	wtMgr := newMockWorktreeManager()
	agent := &ScriptableAgentRunner{}
	cmdRunner := &ScriptableCommandRunner{}

	// Always violates: no attempt can satisfy the guardrail, so it terminates after one try.
	// Count consultations so the test proves the guardrail runner (not some other signal) is
	// what failed the attempt.
	var guardrailCalls int
	guardrails := &dynamicGuardrailRunner{
		checkFn: func() []factory.GuardrailViolation {
			guardrailCalls++
			return []factory.GuardrailViolation{{Path: "auth_test.go", Status: "M"}}
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
	engine.SetMaxRepairAttempts(1)

	if err := engine.ExecuteJob(ctx, 1); err == nil {
		t.Fatal("expected ExecuteJob to fail on terminal guardrail violation, got nil")
	}

	// The failure must come from the guardrail runner actually being consulted, not from an
	// unrelated signal (e.g. a non-empty diff).
	if guardrailCalls == 0 {
		t.Error("expected the guardrail runner to be consulted at least once")
	}

	stepRuns, _ := store.ListStepRunsByJob(ctx, 1)
	var codingAgent *factory.StepRun
	for _, s := range stepRuns {
		if s.Stage == factory.StageCoding && s.Kind == factory.StepKindAgent {
			codingAgent = s
		}
	}
	if codingAgent == nil {
		t.Fatal("expected a coding agent step run")
	}
	if codingAgent.Status != factory.StepStatusFail {
		t.Errorf("terminal guardrail violation must record step status fail, got %s", codingAgent.Status)
	}
	if codingAgent.FailureCategory != factory.FailureManual {
		t.Errorf("expected failure category Manual on exhausted attempts, got %s", codingAgent.FailureCategory)
	}

	job, _ := store.GetJob(ctx, 1)
	if job.Status != factory.StatusFailed {
		t.Errorf("expected job status failed, got %s", job.Status)
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

func (d *dynamicGuardrailRunner) CheckProbeScope(ctx context.Context, workDir, stepStartSHA string, testPatterns []string, artifactGlob string) ([]factory.GuardrailViolation, error) {
	if d.checkFn != nil {
		return d.checkFn(), nil
	}
	return nil, nil
}

// TestEngine_CustomGuardrailCommand_RecordsFail_GRD3 verifies that a failing project-configured
// guardrail command (guardrails.commands, GRD-3) is recorded on the coding agent step run as a
// failure with a category — never left as success — and that the repair loop categorizes it
// Flawed while budget remains and Manual once the budget is exhausted (GRD-4).
//
// Why this matters: in this scenario the agent process itself exits 0; it is the separate
// guardrail command that fails. Without explicitly overwriting the success status recorded when
// the agent passed, step_runs would show a passing coding attempt next to a failed job, hiding
// the very cause the repair loop is reacting to.
//
// Sub-case structure mirrors the guardrail tests above: a repairable violation (command fails
// once, then passes) must advance the job, while a terminal violation (command always fails and
// no repair budget remains) must fail the job with category Manual.
func TestEngine_CustomGuardrailCommand_RecordsFail_GRD3(t *testing.T) {
	const guardrailCmd = "bash scripts/check-guardrail.sh"

	tests := []struct {
		name           string
		maxAttempts    int
		alwaysFail     bool
		wantJobFailed  bool
		wantAttempts   int
		wantFirstCat   string
		wantLastStatus string
		wantRepairMsg  string
	}{
		{
			name:           "repairable command failure records Flawed",
			maxAttempts:    3,
			wantAttempts:   2,
			wantFirstCat:   factory.FailureFlawed,
			wantLastStatus: factory.StepStatusSuccess,
			wantRepairMsg:  "Custom guardrail command failed",
		},
		{
			name:           "exhausted attempts records Manual",
			maxAttempts:    1,
			alwaysFail:     true,
			wantJobFailed:  true,
			wantAttempts:   1,
			wantFirstCat:   factory.FailureManual,
			wantLastStatus: factory.StepStatusFail,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := newMockStore()
			store.projects[1] = &factory.Project{ID: 1, Name: "test-proj", RepoPath: "/repo", BaseRef: "main"}
			store.jobs[1] = &factory.Job{
				ID:        1,
				ProjectID: 1,
				WorkType:  factory.WorkTypeRefactor,
				Stage:     factory.StageIntent,
				Status:    factory.StatusQueued,
				Intent:    "refactor code",
			}

			wtMgr := newMockWorktreeManager()
			agent := &ScriptableAgentRunner{}

			// The scripted guardrail-command failure drives the GRD-3 path, so counting
			// invocations also proves the command really ran rather than failing elsewhere.
			var guardrailRuns int
			cmdRunner := &ScriptableCommandRunner{}
			cmdRunner.handler = func(opts factory.CommandOptions) (*factory.CommandResult, error) {
				if opts.Command != guardrailCmd {
					return &factory.CommandResult{ExitCode: 0}, nil
				}
				guardrailRuns++
				if tc.alwaysFail || guardrailRuns == 1 {
					return &factory.CommandResult{
						ExitCode: 1,
						Stdout:   "checking imports",
						Stderr:   "guardrail: disallowed import",
					}, nil
				}
				return &factory.CommandResult{ExitCode: 0}, nil
			}

			projCfg := &factory.ProjectConfig{
				Guardrails: factory.ProjectGuardrails{
					Commands: []string{guardrailCmd},
				},
			}

			engine := factory.NewEngine(store, wtMgr, agent, cmdRunner, t.TempDir())
			engine.SetProjectConfigProvider(&MockProjectConfigProvider{cfg: projCfg})
			engine.SetMaxRepairAttempts(tc.maxAttempts)

			err := engine.ExecuteJob(ctx, 1)
			if tc.wantJobFailed && err == nil {
				t.Fatal("expected ExecuteJob to fail on terminal guardrail-command violation, got nil")
			}
			if !tc.wantJobFailed && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if guardrailRuns == 0 {
				t.Fatal("expected the custom guardrail command to be executed at least once")
			}

			stepRuns, _ := store.ListStepRunsByJob(ctx, 1)
			var codingAgentSteps []*factory.StepRun
			for _, s := range stepRuns {
				if s.Stage == factory.StageCoding && s.Kind == factory.StepKindAgent {
					codingAgentSteps = append(codingAgentSteps, s)
				}
			}
			if len(codingAgentSteps) != tc.wantAttempts {
				t.Fatalf("expected %d coding agent step runs, got %d", tc.wantAttempts, len(codingAgentSteps))
			}

			// The command-failed attempt must be persisted as fail with a category: the agent
			// exited 0, but the guardrail command did not pass, so recording success would be a lie.
			first := codingAgentSteps[0]
			if first.Status != factory.StepStatusFail {
				t.Errorf("guardrail-command failure must record step status fail, got %s", first.Status)
			}
			if first.FailureCategory != tc.wantFirstCat {
				t.Errorf("expected attempt 1 failure category %s, got %s", tc.wantFirstCat, first.FailureCategory)
			}

			last := codingAgentSteps[len(codingAgentSteps)-1]
			if last.Status != tc.wantLastStatus {
				t.Errorf("expected final coding agent status %s, got %s", tc.wantLastStatus, last.Status)
			}

			if tc.wantRepairMsg != "" {
				var repairPrompt string
				agent.mu.Lock()
				for _, inv := range agent.invocations {
					if inv.Stage == factory.StageCoding && strings.Contains(inv.Prompt, tc.wantRepairMsg) {
						repairPrompt = inv.Prompt
					}
				}
				agent.mu.Unlock()
				if repairPrompt == "" {
					t.Errorf("expected the repair prompt to contain %q", tc.wantRepairMsg)
				}
			}

			job, _ := store.GetJob(ctx, 1)
			if tc.wantJobFailed {
				if job.Status != factory.StatusFailed {
					t.Errorf("expected job status failed, got %s", job.Status)
				}
				return
			}
			if job.Stage != factory.StageHumanApprovalGate || job.Status != factory.StatusAwaitingApproval {
				t.Errorf("expected job at %s/%s after successful repair, got %s/%s",
					factory.StageHumanApprovalGate, factory.StatusAwaitingApproval, job.Stage, job.Status)
			}
		})
	}
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

// TestSpecPrompt_IncludesProtectedPaths_GRD1 verifies that the spec stage forwards the project's
// guardrails.protected_paths into the rendered spec prompt, and omits the section when unset.
//
// Why this matters (GRD-1 alignment): the spec agent plans the implementation before any coding
// step runs. If it is unaware of protected paths it can legitimately plan an edit to a protected
// file (e.g. go.mod or an existing *_test.go); the coding agent then follows the spec and the
// guardrail check fails the step as Flawed/terminal. Telling the spec agent up-front, rather than
// relying on a failure-and-repair loop, is what keeps specs guardrail-consistent.
//
// Sub-case structure mirrors a table-driven test: with patterns the prompt must name them, without
// patterns the conditional section must disappear entirely (no dangling empty header).
func TestSpecPrompt_IncludesProtectedPaths_GRD1(t *testing.T) {
	tests := []struct {
		name           string
		protectedPaths []string
		wantContains   []string
		wantAbsent     string
	}{
		{
			name:           "configured patterns are rendered",
			protectedPaths: []string{"**/*_test.go"},
			wantContains:   []string{"### Protected Paths", "**/*_test.go"},
		},
		{
			name:           "no patterns means no section",
			protectedPaths: nil,
			wantAbsent:     "### Protected Paths",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := newMockStore()
			store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha", BaseRef: "main"}
			store.jobs[1] = &factory.Job{
				ID:           1,
				ProjectID:    1,
				WorkType:     factory.WorkTypeFeature,
				Title:        "Subtract function",
				Intent:       "Add a Subtract function with tests.",
				Stage:        factory.StageClarificationAndSpec,
				Status:       factory.StatusQueued,
				WorktreePath: "/tmp/worktrees/alpha/1",
			}

			wtMgr := newMockWorktreeManager()

			// The scripted spec agent emits a valid spec.md so the stage completes and the job
			// advances to spec_review; we only care about the prompt it was handed.
			agent := &ScriptableAgentRunner{
				results: func(call int, req factory.AgentRequest) (*factory.AgentResult, error) {
					_ = wtMgr.WriteArtifact(ctx, req.WorktreePath, req.JobID, "spec.md", []byte(validFeatureSpec))
					return &factory.AgentResult{ExitCode: 0, Summary: "spec generated"}, nil
				},
			}

			projCfg := &factory.ProjectConfig{
				Guardrails: factory.ProjectGuardrails{ProtectedPaths: tc.protectedPaths},
			}

			engine := factory.NewEngine(store, wtMgr, agent, &ScriptableCommandRunner{}, t.TempDir())
			engine.SetProjectConfigProvider(&MockProjectConfigProvider{cfg: projCfg})

			if err := engine.ExecuteJob(ctx, 1); err != nil {
				t.Fatalf("ExecuteJob failed: %v", err)
			}

			agent.mu.Lock()
			invocations := append([]factory.AgentRequest(nil), agent.invocations...)
			agent.mu.Unlock()

			if len(invocations) == 0 {
				t.Fatal("expected at least 1 agent invocation, got 0")
			}
			first := invocations[0]
			if first.Role != factory.RoleSpec {
				t.Fatalf("expected first agent invocation role %q, got %q", factory.RoleSpec, first.Role)
			}

			for _, want := range tc.wantContains {
				if !strings.Contains(first.Prompt, want) {
					t.Errorf("expected spec prompt to contain %q, got:\n%s", want, first.Prompt)
				}
			}
			if tc.wantAbsent != "" && strings.Contains(first.Prompt, tc.wantAbsent) {
				t.Errorf("expected spec prompt NOT to contain %q, got:\n%s", tc.wantAbsent, first.Prompt)
			}
		})
	}
}
