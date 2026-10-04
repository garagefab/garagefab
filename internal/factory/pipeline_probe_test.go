// Package factory_test contains isolated unit tests for the failing-probe stage (PRB-1..4, GRD-5).
package factory_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/garagefab/garagefab/internal/factory"
)

// probeScriptRunner is a scripted agent runner: on each probe-stage call it writes the next
// probe.json payload into the mock worktree and records the rendered prompt.
type probeScriptRunner struct {
	wtMgr *MockWorktreeManager

	mu      sync.Mutex
	calls   int
	probes  []string
	prompts []string
}

func (r *probeScriptRunner) Run(ctx context.Context, req factory.AgentRequest) (*factory.AgentResult, error) {
	r.mu.Lock()
	i := r.calls
	r.calls++
	r.prompts = append(r.prompts, req.Prompt)
	r.mu.Unlock()

	if req.OnProcessStart != nil {
		req.OnProcessStart(1, 1, time.Now().Unix())
	}

	if req.Stage == factory.StageFailingProbe && i < len(r.probes) && r.probes[i] != "" {
		_ = r.wtMgr.WriteArtifact(ctx, req.WorktreePath, req.JobID, "probe.json", []byte(r.probes[i]))
	}
	return &factory.AgentResult{ExitCode: 0, Summary: "ok"}, nil
}

func (r *probeScriptRunner) promptAt(i int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < len(r.prompts) {
		return r.prompts[i]
	}
	return ""
}

func probeJSON(command string) string {
	return fmt.Sprintf(`{"schema_version":1,"command":%q,"files":["bug_repro_test.go"],"description":"repro"}`, command)
}

// newProbeTestEngine wires a bug_fix job at 03_Failing_Probe/queued with the given probe script.
func newProbeTestEngine(t *testing.T, probes []string) (*factory.Engine, *MockStore, *MockWorktreeManager, *probeScriptRunner, *ScriptableCommandRunner) {
	t.Helper()
	store := newMockStore()
	wtMgr := newMockWorktreeManager()
	agent := &probeScriptRunner{wtMgr: wtMgr, probes: probes}
	cmdRunner := &ScriptableCommandRunner{handler: func(opts factory.CommandOptions) (*factory.CommandResult, error) {
		switch opts.Command {
		case "pass-cmd":
			return &factory.CommandResult{ExitCode: 0}, nil
		case "fail-cmd":
			return &factory.CommandResult{ExitCode: 1, Stderr: "boom"}, nil
		default:
			return &factory.CommandResult{ExitCode: 0}, nil
		}
	}}
	engine := factory.NewEngine(store, wtMgr, agent, cmdRunner, t.TempDir())

	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha", BaseRef: "main"}
	store.jobs[1] = &factory.Job{
		ID:           1,
		ProjectID:    1,
		WorkType:     factory.WorkTypeBugFix,
		Title:        "Fix login bug",
		Intent:       "Login fails for empty passwords",
		Stage:        factory.StageFailingProbe,
		Status:       factory.StatusQueued,
		WorktreePath: "/tmp/worktrees/alpha/1",
		BaseSHA:      "base-sha",
		HeadSHA:      "base-sha",
	}
	return engine, store, wtMgr, agent, cmdRunner
}

// TestProbe_PassingProbe_Rejected_PRB2: a probe that exits 0 is rejected with
// "probe passed; it must fail" and the agent is re-run (PRB-2).
func TestProbe_PassingProbe_Rejected_PRB2(t *testing.T) {
	ctx := context.Background()
	engine, store, wtMgr, agent, _ := newProbeTestEngine(t, []string{probeJSON("pass-cmd"), probeJSON("fail-cmd")})

	if err := engine.ExecuteJob(ctx, 1); err != nil {
		t.Fatalf("ExecuteJob failed: %v", err)
	}

	if p := agent.promptAt(1); !strings.Contains(p, "probe passed; it must fail") {
		t.Fatalf("expected repair feedback 'probe passed; it must fail' in second probe prompt, got: %s", p)
	}
	if !containsString(wtMgr.checkpointHistory, "probe") {
		t.Fatalf("expected a 'probe' checkpoint, got %v", wtMgr.checkpointHistory)
	}

	job, _ := store.GetJob(ctx, 1)
	if job.Stage != factory.StageHumanApprovalGate {
		t.Fatalf("expected job to progress past coding to the gate, got %s/%s", job.Stage, job.Status)
	}
}

// TestProbe_InvalidSpec_Flawed_PRB1: a missing/invalid probe.json is Flawed and re-run (PRB-1).
func TestProbe_InvalidSpec_Flawed_PRB1(t *testing.T) {
	ctx := context.Background()
	engine, _, wtMgr, agent, _ := newProbeTestEngine(t, []string{
		`{"schema_version":1,"command":"","files":[]}`,
		probeJSON("fail-cmd"),
	})

	if err := engine.ExecuteJob(ctx, 1); err != nil {
		t.Fatalf("ExecuteJob failed: %v", err)
	}

	if p := agent.promptAt(1); !strings.Contains(p, "probe.json invalid") {
		t.Fatalf("expected 'probe.json invalid' repair feedback, got: %s", p)
	}
	if !containsString(wtMgr.checkpointHistory, "probe") {
		t.Fatalf("expected a 'probe' checkpoint after recovery, got %v", wtMgr.checkpointHistory)
	}
}

// TestProbe_ExhaustedAttempts_Manual_PRB3: three passing probes fail the job at 03 with
// category Manual (PRB-3).
func TestProbe_ExhaustedAttempts_Manual_PRB3(t *testing.T) {
	ctx := context.Background()
	engine, store, _, _, _ := newProbeTestEngine(t, []string{
		probeJSON("pass-cmd"), probeJSON("pass-cmd"), probeJSON("pass-cmd"),
	})

	if err := engine.ExecuteJob(ctx, 1); err == nil {
		t.Fatal("expected ExecuteJob to fail after exhausted probe attempts")
	}

	job, _ := store.GetJob(ctx, 1)
	if job.Stage != factory.StageFailingProbe || job.Status != factory.StatusFailed {
		t.Fatalf("expected 03_Failing_Probe/failed, got %s/%s", job.Stage, job.Status)
	}

	var manual bool
	for _, e := range store.events {
		if strings.Contains(e.Payload, `"failure_category":"Manual"`) {
			manual = true
		}
	}
	if !manual {
		t.Fatalf("expected a Manual failure_category event, got %+v", store.events)
	}
}

// TestProbe_FailingProbe_MovesToCoding_PRB4: a valid non-zero-exit probe is checkpointed and
// the job proceeds to 04_Coding (PRB-4).
func TestProbe_FailingProbe_MovesToCoding_PRB4(t *testing.T) {
	ctx := context.Background()
	engine, store, wtMgr, _, _ := newProbeTestEngine(t, []string{probeJSON("fail-cmd")})

	if err := engine.ExecuteJob(ctx, 1); err != nil {
		t.Fatalf("ExecuteJob failed: %v", err)
	}

	if len(wtMgr.checkpointHistory) == 0 || wtMgr.checkpointHistory[0] != "probe" {
		t.Fatalf("expected the first checkpoint to be 'probe', got %v", wtMgr.checkpointHistory)
	}

	steps, _ := store.ListStepRunsByJob(ctx, 1)
	var probeCmd *factory.StepRun
	for _, s := range steps {
		if s.Stage == factory.StageFailingProbe && s.Kind == factory.StepKindCommand && s.Executor == "probe" {
			probeCmd = s
		}
	}
	if probeCmd == nil {
		t.Fatal("expected a probe command StepRun")
	}
	if probeCmd.Status != factory.StepStatusSuccess {
		t.Fatalf("expected probe command step success, got %s", probeCmd.Status)
	}
	if probeCmd.ExitCode == nil || *probeCmd.ExitCode == 0 {
		t.Fatalf("expected a non-zero probe exit code, got %v", probeCmd.ExitCode)
	}
}

// TestProbe_NonTestFile_Flawed_GRD5: with test_paths configured, a probe-scope violation is
// Flawed and re-run (GRD-5).
func TestProbe_NonTestFile_Flawed_GRD5(t *testing.T) {
	ctx := context.Background()
	engine, store, _, agent, _ := newProbeTestEngine(t, []string{
		probeJSON("fail-cmd"), probeJSON("fail-cmd"), probeJSON("fail-cmd"),
	})
	engine.SetProjectConfigProvider(&MockProjectConfigProvider{cfg: &factory.ProjectConfig{
		Guardrails: factory.ProjectGuardrails{TestPaths: []string{"**/*_test.go"}},
	}})
	engine.SetGuardrailRunner(&MockGuardrailRunner{violations: []factory.GuardrailViolation{{Path: "src/main.go", Status: "M"}}})

	if err := engine.ExecuteJob(ctx, 1); err == nil {
		t.Fatal("expected ExecuteJob to fail on guardrail violations")
	}

	if p := agent.promptAt(1); !strings.Contains(p, "modified non-test file") {
		t.Fatalf("expected guardrail repair feedback, got: %s", p)
	}

	job, _ := store.GetJob(ctx, 1)
	if job.Stage != factory.StageFailingProbe || job.Status != factory.StatusFailed {
		t.Fatalf("expected 03_Failing_Probe/failed, got %s/%s", job.Stage, job.Status)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
