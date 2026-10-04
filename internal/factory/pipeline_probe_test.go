// Package factory_test contains isolated unit tests for the failing-probe stage (PRB-1..4, GRD-5).
package factory_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/garagefab/garagefab/internal/factory"
)

// erroringProbeGuardrail makes the GRD-5 scope check fail (fail-closed test).
type erroringProbeGuardrail struct{}

func (erroringProbeGuardrail) CheckProtectedPaths(context.Context, string, string, []string) ([]factory.GuardrailViolation, error) {
	return nil, nil
}

func (erroringProbeGuardrail) CheckProbeScope(context.Context, string, string, []string, string) ([]factory.GuardrailViolation, error) {
	return nil, errors.New("git diff failed")
}

// TestProbe_ScopeCheckError_Blocked verifies that a failing GRD-5 scope check fails closed
// (Blocked) instead of silently passing (GRD-5).
func TestProbe_ScopeCheckError_Blocked(t *testing.T) {
	ctx := context.Background()
	engine, store, _, _, _ := newProbeTestEngine(t, []string{probeJSON("fail-then-pass-cmd")})
	engine.SetProjectConfigProvider(&MockProjectConfigProvider{cfg: &factory.ProjectConfig{
		Guardrails: factory.ProjectGuardrails{TestPaths: []string{"**/*_test.go"}},
	}})
	engine.SetGuardrailRunner(erroringProbeGuardrail{})

	if err := engine.ExecuteJob(ctx, 1); err == nil {
		t.Fatal("expected ExecuteJob to fail when the probe scope check errors")
	}
	job, _ := store.GetJob(ctx, 1)
	if job.Stage != factory.StageFailingProbe || job.Status != factory.StatusFailed {
		t.Fatalf("expected 03/failed, got %s/%s", job.Stage, job.Status)
	}
}

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
	var failThenPassCalls int
	cmdRunner := &ScriptableCommandRunner{handler: func(opts factory.CommandOptions) (*factory.CommandResult, error) {
		switch opts.Command {
		case "pass-cmd":
			return &factory.CommandResult{ExitCode: 0}, nil
		case "fail-cmd":
			// Always fails (used by coding-only tests).
			return &factory.CommandResult{ExitCode: 1, Stderr: "boom"}, nil
		case "fail-then-pass-cmd":
			// Fails while reproducing the bug (stage 03), passes once coding fixes it.
			failThenPassCalls++
			if failThenPassCalls == 1 {
				return &factory.CommandResult{ExitCode: 1, Stderr: "repro"}, nil
			}
			return &factory.CommandResult{ExitCode: 0}, nil
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
	engine, store, wtMgr, agent, _ := newProbeTestEngine(t, []string{probeJSON("pass-cmd"), probeJSON("fail-then-pass-cmd")})

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
		probeJSON("fail-then-pass-cmd"),
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
	engine, store, wtMgr, _, _ := newProbeTestEngine(t, []string{probeJSON("fail-then-pass-cmd")})

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

// recordingGuardrail records the patterns passed to CheckProtectedPaths (COD-8).
type recordingGuardrail struct {
	protectedPatterns   [][]string
	protectedViolations []factory.GuardrailViolation
}

func (g *recordingGuardrail) CheckProtectedPaths(_ context.Context, _, _ string, patterns []string) ([]factory.GuardrailViolation, error) {
	g.protectedPatterns = append(g.protectedPatterns, append([]string{}, patterns...))
	return g.protectedViolations, nil
}

func (g *recordingGuardrail) CheckProbeScope(_ context.Context, _, _ string, _ []string, _ string) ([]factory.GuardrailViolation, error) {
	return nil, nil
}

// startCodingJob moves the bug_fix test job to 04_Coding and seeds probe.json.
func startCodingJob(t *testing.T, store *MockStore, wtMgr *MockWorktreeManager, probe string) {
	t.Helper()
	store.jobs[1].Stage = factory.StageCoding
	store.jobs[1].Status = factory.StatusQueued
	if probe != "" {
		_ = wtMgr.WriteArtifact(context.Background(), store.jobs[1].WorktreePath, 1, "probe.json", []byte(probe))
	}
}

// TestCoding_ProtectsProbeFiles_COD8: the coding step protects the probe files, so editing one
// is a GRD-1/COD-8 violation (Flawed).
func TestCoding_ProtectsProbeFiles_COD8(t *testing.T) {
	ctx := context.Background()
	engine, store, wtMgr, agent, _ := newProbeTestEngine(t, nil)
	startCodingJob(t, store, wtMgr, probeJSON("fail-cmd"))

	gr := &recordingGuardrail{protectedViolations: []factory.GuardrailViolation{{Path: "bug_repro_test.go", Status: "M"}}}
	engine.SetGuardrailRunner(gr)

	if err := engine.ExecuteJob(ctx, 1); err == nil {
		t.Fatal("expected ExecuteJob to fail on a probe-file guardrail violation")
	}

	var protected bool
	for _, pats := range gr.protectedPatterns {
		if containsString(pats, "bug_repro_test.go") {
			protected = true
		}
	}
	if !protected {
		t.Fatalf("expected the probe file to be added to the protected patterns, got %v", gr.protectedPatterns)
	}

	if p := agent.promptAt(0); !strings.Contains(p, "Probe files (do not modify)") || !strings.Contains(p, "bug_repro_test.go") {
		t.Fatalf("expected the coding prompt to list probe files, got: %s", p)
	}

	job, _ := store.GetJob(ctx, 1)
	if job.Status != factory.StatusFailed {
		t.Fatalf("expected job failed, got %s/%s", job.Stage, job.Status)
	}
}

// TestCoding_ProbeStillFails_COD8: if the probe still fails after coding, the step is Flawed and
// re-run (COD-8).
func TestCoding_ProbeStillFails_COD8(t *testing.T) {
	ctx := context.Background()
	engine, store, wtMgr, agent, _ := newProbeTestEngine(t, nil)
	startCodingJob(t, store, wtMgr, probeJSON("fail-cmd"))

	if err := engine.ExecuteJob(ctx, 1); err == nil {
		t.Fatal("expected ExecuteJob to fail when the probe still fails after coding")
	}
	if p := agent.promptAt(1); !strings.Contains(p, "probe still fails after coding") {
		t.Fatalf("expected 'probe still fails after coding' repair feedback, got: %s", p)
	}
}

// TestCoding_ProbePassesAfterCoding_COD8: a probe that passes after coding lets the job proceed
// to review (COD-8).
func TestCoding_ProbePassesAfterCoding_COD8(t *testing.T) {
	ctx := context.Background()
	engine, store, wtMgr, _, _ := newProbeTestEngine(t, nil)
	startCodingJob(t, store, wtMgr, probeJSON("pass-cmd"))

	if err := engine.ExecuteJob(ctx, 1); err != nil {
		t.Fatalf("ExecuteJob failed: %v", err)
	}

	job, _ := store.GetJob(ctx, 1)
	if job.Stage != factory.StageHumanApprovalGate {
		t.Fatalf("expected job to reach the approval gate, got %s/%s", job.Stage, job.Status)
	}
}

// TestEvidence_IncludesProbeResult_PRB5: the evidence summary and evidence.md include the probe
// result next to the review (PRB-5).
func TestEvidence_IncludesProbeResult_PRB5(t *testing.T) {
	steps := []*factory.StepRun{
		{ID: 1, JobID: 1, Stage: factory.StageFailingProbe, Kind: factory.StepKindCommand, Executor: "probe", Status: factory.StepStatusSuccess, LogPath: "/logs/1/step_probe_cmd_1.log"},
		{ID: 2, JobID: 1, Stage: factory.StageCoding, Kind: factory.StepKindCommand, Executor: "probe", Status: factory.StepStatusSuccess, LogPath: "/logs/1/step_coding_probe_1.log"},
	}
	review := &factory.ReviewReport{Decision: "approve", Summary: "ok"}

	summary, md := factory.BuildEvidence(&factory.Job{ID: 1, WorkType: factory.WorkTypeBugFix, Title: "t", HeadSHA: "abc"}, steps, review, nil)
	if summary.Probe.Status != "pass" {
		t.Fatalf("expected probe status pass, got %q", summary.Probe.Status)
	}
	if !strings.Contains(md, "## Failing Probe") {
		t.Fatalf("expected evidence.md to contain a Failing Probe section, got: %s", md)
	}
	if _, ok := summary.DrillDowns["probe"]; !ok {
		t.Fatalf("expected a probe drill-down link, got %v", summary.DrillDowns)
	}
}
