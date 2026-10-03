// Package agent provides AI coding agent execution and lifecycle management.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Unit Verification for Google Antigravity `agy` CLI Adapter (D21, COD-10, SPC-1, REV-2).
//
// Tests verify:
// 1. Contract tests against real Spike fixtures (success, agent failure, missing envelope, garbage).
// 2. Strict CLI argv construction (attached --print, permissions, disable slash commands, fresh session).
// 3. Environmental isolation and passthrough behavior (SEC-6).
// 4. Role-based effort scaling (coding -> medium, review -> high).
// ==============================================================================
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAgyRunner_Contract_Success tests requirement D21 & SPC-1:
// When agy returns a SUCCESS envelope, exit code is 0 and summary contains the response text.
func TestAgyRunner_Contract_Success(t *testing.T) {
	fakeAgy, err := filepath.Abs("testdata/agy/fake_agy.sh")
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	runner := NewAgyRunner(fakeAgy, nil)
	tmpDir := t.TempDir()

	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "Fix hello world string",
		Env: map[string]string{
			"FAKE_AGY_MODE": "success",
		},
	}

	res, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Summary, "Updated [hello.go]") {
		t.Errorf("expected summary to contain response text, got: %s", res.Summary)
	}
	if res.Usage.TotalTokens != 120271 {
		t.Errorf("expected 120271 total tokens, got %d", res.Usage.TotalTokens)
	}
	if res.Usage.PromptTokens != 116447 {
		t.Errorf("expected 116447 prompt tokens, got %d", res.Usage.PromptTokens)
	}
	if res.Usage.CompletionTokens != 3824 {
		t.Errorf("expected 3824 completion tokens, got %d", res.Usage.CompletionTokens)
	}
}

// TestAgyRunner_Contract_AgentFailure tests requirement D21:
// When agy returns an envelope with status != SUCCESS, exit code is mapped to 1
// to trigger the factory repair loop.
func TestAgyRunner_Contract_AgentFailure(t *testing.T) {
	fakeAgy, err := filepath.Abs("testdata/agy/fake_agy.sh")
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	runner := NewAgyRunner(fakeAgy, nil)
	tmpDir := t.TempDir()

	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "Impossible task",
		Env: map[string]string{
			"FAKE_AGY_MODE": "agent_failure",
		},
	}

	res, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	if res.ExitCode != 1 {
		t.Errorf("expected exit code 1 for agent failure, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Summary, "agy reported status FAILED") {
		t.Errorf("expected summary 'agy reported status FAILED', got: %s", res.Summary)
	}
	if res.Usage.TotalTokens != 1200 {
		t.Errorf("expected 1200 total tokens, got %d", res.Usage.TotalTokens)
	}
}

// TestAgyRunner_Contract_NoEnvelope tests requirement D21:
// When agy completes with exit code 0 but produces no JSON envelope, exit code is mapped to 1.
func TestAgyRunner_Contract_NoEnvelope(t *testing.T) {
	fakeAgy, err := filepath.Abs("testdata/agy/fake_agy.sh")
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	runner := NewAgyRunner(fakeAgy, nil)
	tmpDir := t.TempDir()

	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "Task without envelope",
		Env: map[string]string{
			"FAKE_AGY_MODE": "failure",
		},
	}

	res, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	if res.ExitCode != 1 {
		t.Errorf("expected exit code 1 for missing envelope, got %d", res.ExitCode)
	}
	if res.Summary != "agy produced no result envelope" {
		t.Errorf("expected 'agy produced no result envelope', got: %s", res.Summary)
	}
}

// TestAgyRunner_Contract_GarbageOutput tests requirement D21:
// When agy outputs invalid/garbage JSON, exit code is mapped to 1.
func TestAgyRunner_Contract_GarbageOutput(t *testing.T) {
	fakeAgy, err := filepath.Abs("testdata/agy/fake_agy.sh")
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	runner := NewAgyRunner(fakeAgy, nil)
	tmpDir := t.TempDir()

	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "Task with corrupt output",
		Env: map[string]string{
			"FAKE_AGY_MODE": "garbage",
		},
	}

	res, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	if res.ExitCode != 1 {
		t.Errorf("expected exit code 1 for garbage output, got %d", res.ExitCode)
	}
	if res.Summary != "agy produced no result envelope" {
		t.Errorf("expected 'agy produced no result envelope', got: %s", res.Summary)
	}
}

// TestAgyRunner_Contract_ProcessCrash tests requirement D21:
// When agy binary crashes with a non-zero exit code, that exit code and stderr tail are preserved.
func TestAgyRunner_Contract_ProcessCrash(t *testing.T) {
	fakeAgy, err := filepath.Abs("testdata/agy/fake_agy.sh")
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	runner := NewAgyRunner(fakeAgy, nil)
	tmpDir := t.TempDir()

	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "Crashing task",
		Env: map[string]string{
			"FAKE_AGY_MODE": "crash",
		},
	}

	res, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	if res.ExitCode != 2 {
		t.Errorf("expected exit code 2, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Summary, "segmentation fault") {
		t.Errorf("expected stderr in summary, got: %s", res.Summary)
	}
}

// TestAgyRunner_Contract_Timeout_COD10 tests requirement COD-10:
// When agy exceeds configured timeout, exit code is 124 and TimedOut=true.
func TestAgyRunner_Contract_Timeout_COD10(t *testing.T) {
	fakeAgy, err := filepath.Abs("testdata/agy/fake_agy.sh")
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	runner := NewAgyRunner(fakeAgy, nil)
	tmpDir := t.TempDir()

	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "Hanging task",
		Timeout:      200 * time.Millisecond,
		Env: map[string]string{
			"FAKE_AGY_MODE": "timeout",
		},
	}

	res, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	if !res.TimedOut {
		t.Errorf("expected TimedOut=true, got false")
	}
	if res.ExitCode != 124 {
		t.Errorf("expected exit code 124, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Summary, "agent timed out after 200ms") {
		t.Errorf("unexpected timeout summary: %s", res.Summary)
	}
}

// TestAgyRunner_ArgvAssertions tests CLI argument generation:
// - Attached form --print=<prompt>
// - Auto-approval: --dangerously-skip-permissions
// - Structured format: --output-format json
// - Determinism: --disable-slash-commands
// - Effort scaling: coding -> medium, review -> high
// - Clean session invariant: NO --continue or --conversation
func TestAgyRunner_ArgvAssertions(t *testing.T) {
	fakeAgy, err := filepath.Abs("testdata/agy/fake_agy.sh")
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	runner := NewAgyRunner(fakeAgy, nil)
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "recorded_args.txt")

	// 1. Role: coding -> effort medium
	reqCoding := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "Implement user login",
		Env: map[string]string{
			"FAKE_AGY_RECORD_ARGS": argsFile,
		},
	}

	_, err = runner.Run(context.Background(), reqCoding)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	content, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file failed: %v", err)
	}
	argsStr := string(content)

	if !strings.Contains(argsStr, "--print=Implement user login") {
		t.Errorf("expected attached --print=... flag, got:\n%s", argsStr)
	}
	if !strings.Contains(argsStr, "--dangerously-skip-permissions") {
		t.Errorf("expected --dangerously-skip-permissions flag")
	}
	if !strings.Contains(argsStr, "--output-format\njson") {
		t.Errorf("expected --output-format json")
	}
	if !strings.Contains(argsStr, "--disable-slash-commands") {
		t.Errorf("expected --disable-slash-commands flag")
	}
	if !strings.Contains(argsStr, "--effort\nmedium") {
		t.Errorf("expected --effort medium for coding role")
	}
	if strings.Contains(argsStr, "--continue") || strings.Contains(argsStr, "--conversation") {
		t.Errorf("D21 clean session violated: found resume flags in args:\n%s", argsStr)
	}

	// 2. Role: review -> effort high
	reqReview := AgentRequest{
		Role:         "review",
		WorktreePath: tmpDir,
		Prompt:       "Review pull request changes",
		Env: map[string]string{
			"FAKE_AGY_RECORD_ARGS": argsFile,
		},
	}

	_, err = runner.Run(context.Background(), reqReview)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	content, err = os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file failed: %v", err)
	}
	argsStr = string(content)

	if !strings.Contains(argsStr, "--effort\nhigh") {
		t.Errorf("expected --effort high for review role, got:\n%s", argsStr)
	}
}

// TestAgyRunner_EnvIsolation_SEC6 tests environment sanitization:
// Subprocess only receives allow-listed variables and explicit passthrough;
// GARAGEFAB_* variables are strictly stripped.
func TestAgyRunner_EnvIsolation_SEC6(t *testing.T) {
	fakeAgy, err := filepath.Abs("testdata/agy/fake_agy.sh")
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	os.Setenv("GARAGEFAB_GITHUB_TOKEN", "super-secret-gh-token")
	os.Setenv("GARAGEFAB_API_TOKEN", "super-secret-api-token")
	os.Setenv("GEMINI_API_KEY", "gemini-model-key")
	defer func() {
		os.Unsetenv("GARAGEFAB_GITHUB_TOKEN")
		os.Unsetenv("GARAGEFAB_API_TOKEN")
		os.Unsetenv("GEMINI_API_KEY")
	}()

	runner := NewAgyRunner(fakeAgy, []string{"GEMINI_API_KEY", "GARAGEFAB_API_TOKEN"})
	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, "recorded_env.txt")

	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "Test env",
		Env: map[string]string{
			"FAKE_AGY_RECORD_ENV": envFile,
		},
	}

	_, err = runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	content, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env file failed: %v", err)
	}
	envStr := string(content)

	if strings.Contains(envStr, "GARAGEFAB_") {
		t.Errorf("SEC-6 hard deny violated: found GARAGEFAB in child env:\n%s", envStr)
	}
	if !strings.Contains(envStr, "GEMINI_API_KEY=gemini-model-key") {
		t.Errorf("expected GEMINI_API_KEY to be passed through to child env")
	}
}
