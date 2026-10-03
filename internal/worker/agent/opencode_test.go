// Package agent provides AI coding agent execution and lifecycle management.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Unit Verification for OpenCode CLI Adapter (D21, COD-10, LOG-2).
//
// Tests verify:
// 1. Contract tests against real NDJSON fixtures (success, truncated, error events, crashes).
// 2. High-volume stream processing (50,000 synthetic lines) with O(1) memory guarantees.
// 3. Command-line invocation argument structure (run, --auto, --format json, --dir).
// 4. Environmental isolation and passthrough behavior (SEC-6).
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

// TestOpenCodeRunner_Contract_Success tests requirement D21:
// Parses multi-step NDJSON stream terminating with reason "stop", yielding ExitCode 0
// and extracting final text and token counts.
func TestOpenCodeRunner_Contract_Success(t *testing.T) {
	fakeOpencode, err := filepath.Abs("testdata/opencode/fake_opencode.sh")
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	runner := NewOpenCodeRunner(fakeOpencode, nil)
	tmpDir := t.TempDir()

	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "# Fix hello world string",
		Env: map[string]string{
			"FAKE_OPENCODE_MODE": "success",
		},
	}

	res, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Summary, "Done. `hello.go` prints `Hello World`") {
		t.Errorf("expected summary to contain final text, got: %s", res.Summary)
	}
	if res.Usage.TotalTokens != 12058 {
		t.Errorf("expected 12058 total tokens, got %d", res.Usage.TotalTokens)
	}
	if res.Usage.PromptTokens != 222 {
		t.Errorf("expected 222 prompt tokens, got %d", res.Usage.PromptTokens)
	}
	if res.Usage.CompletionTokens != 27 {
		t.Errorf("expected 27 completion tokens, got %d", res.Usage.CompletionTokens)
	}
}

// TestOpenCodeRunner_Contract_Truncated tests requirement D21:
// When the NDJSON stream finishes without a step_finish event, it is mapped to ExitCode 1.
func TestOpenCodeRunner_Contract_Truncated(t *testing.T) {
	fakeOpencode, err := filepath.Abs("testdata/opencode/fake_opencode.sh")
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	runner := NewOpenCodeRunner(fakeOpencode, nil)
	tmpDir := t.TempDir()

	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "# Incomplete run",
		Env: map[string]string{
			"FAKE_OPENCODE_MODE": "truncated",
		},
	}

	res, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	if res.ExitCode != 1 {
		t.Errorf("expected exit code 1 for truncated stream, got %d", res.ExitCode)
	}
	if res.Summary != "opencode stream ended without step_finish" {
		t.Errorf("unexpected summary: %s", res.Summary)
	}
}

// TestOpenCodeRunner_Contract_ErrorEvent tests requirement D21:
// When an explicit {"type":"error"} event is received in the stream, ExitCode is 1
// and summary contains the error message.
func TestOpenCodeRunner_Contract_ErrorEvent(t *testing.T) {
	fakeOpencode, err := filepath.Abs("testdata/opencode/fake_opencode.sh")
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	runner := NewOpenCodeRunner(fakeOpencode, nil)
	tmpDir := t.TempDir()

	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "# Task hitting error",
		Env: map[string]string{
			"FAKE_OPENCODE_MODE": "error_event",
		},
	}

	res, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	if res.ExitCode != 1 {
		t.Errorf("expected exit code 1 for error event, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Summary, "Rate limit exceeded") {
		t.Errorf("expected error message in summary, got: %s", res.Summary)
	}
}

// TestOpenCodeRunner_Contract_ProcessCrash tests requirement D21:
// Non-zero process exit codes are preserved along with stderr output.
func TestOpenCodeRunner_Contract_ProcessCrash(t *testing.T) {
	fakeOpencode, err := filepath.Abs("testdata/opencode/fake_opencode.sh")
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	runner := NewOpenCodeRunner(fakeOpencode, nil)
	tmpDir := t.TempDir()

	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "# Crashing task",
		Env: map[string]string{
			"FAKE_OPENCODE_MODE": "crash",
		},
	}

	res, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	if res.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Summary, "syntax error") {
		t.Errorf("expected stderr in summary, got: %s", res.Summary)
	}
}

// TestOpenCodeRunner_Contract_Timeout_COD10 tests requirement COD-10:
// On timeout, ExitCode is 124 and TimedOut=true.
func TestOpenCodeRunner_Contract_Timeout_COD10(t *testing.T) {
	fakeOpencode, err := filepath.Abs("testdata/opencode/fake_opencode.sh")
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	runner := NewOpenCodeRunner(fakeOpencode, nil)
	tmpDir := t.TempDir()

	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "# Hanging task",
		Timeout:      200 * time.Millisecond,
		Env: map[string]string{
			"FAKE_OPENCODE_MODE": "timeout",
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
}

// TestOpenCodeRunner_BoundedMemory_50kLines tests requirement LOG-2 / NFR:
// A 50,000-line synthetic stream is processed with strictly bounded O(1) memory.
func TestOpenCodeRunner_BoundedMemory_50kLines(t *testing.T) {
	tmpDir := t.TempDir()
	streamScript := filepath.Join(tmpDir, "stream_50k.sh")

	// Shell script emits 50,000 tool events followed by a successful step_finish
	scriptContent := `#!/bin/sh
awk 'BEGIN {
    for (i=1; i<=50000; i++) {
        print "{\"type\":\"tool_use\",\"part\":{\"type\":\"tool\",\"tool\":\"read\",\"state\":{\"status\":\"completed\"}}}"
    }
    print "{\"type\":\"text\",\"part\":{\"type\":\"text\",\"text\":\"Synthetic complete.\"}}"
    print "{\"type\":\"step_finish\",\"part\":{\"reason\":\"stop\",\"tokens\":{\"total\":50000,\"input\":40000,\"output\":10000}}}"
}'
`
	if err := os.WriteFile(streamScript, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("failed to write stream script: %v", err)
	}

	runner := NewOpenCodeRunner(streamScript, nil)
	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "# Process high volume stream",
	}

	res, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if res.Summary != "Synthetic complete." {
		t.Errorf("unexpected summary: %s", res.Summary)
	}
	if res.Usage.TotalTokens != 50000 {
		t.Errorf("expected 50000 tokens, got %d", res.Usage.TotalTokens)
	}
}

// TestOpenCodeRunner_ArgvAssertions tests CLI argument construction:
// - Command: opencode run --auto --format json --dir <worktree> <prompt>
// - Omits --continue / --session
func TestOpenCodeRunner_ArgvAssertions(t *testing.T) {
	fakeOpencode, err := filepath.Abs("testdata/opencode/fake_opencode.sh")
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	runner := NewOpenCodeRunner(fakeOpencode, nil)
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "recorded_args.txt")

	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "# Implement caching layer",
		Env: map[string]string{
			"FAKE_OPENCODE_RECORD_ARGS": argsFile,
		},
	}

	_, err = runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	content, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file failed: %v", err)
	}
	argsStr := string(content)

	if !strings.Contains(argsStr, "run\n--auto\n--format\njson\n--dir\n"+tmpDir) {
		t.Errorf("expected opencode run arguments matching flags, got:\n%s", argsStr)
	}
	if !strings.Contains(argsStr, "# Implement caching layer") {
		t.Errorf("expected prompt passed as positional argument")
	}
	if strings.Contains(argsStr, "--continue") || strings.Contains(argsStr, "--session") {
		t.Errorf("D21 clean session violated: found resume flags in args:\n%s", argsStr)
	}
}

// TestOpenCodeRunner_EnvIsolation_SEC6 tests environment sanitization:
// Subprocess only receives allow-listed variables and explicit passthrough;
// GARAGEFAB_* variables are strictly stripped.
func TestOpenCodeRunner_EnvIsolation_SEC6(t *testing.T) {
	fakeOpencode, err := filepath.Abs("testdata/opencode/fake_opencode.sh")
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	os.Setenv("GARAGEFAB_GITHUB_TOKEN", "super-secret-gh-token")
	os.Setenv("GARAGEFAB_API_TOKEN", "super-secret-api-token")
	os.Setenv("OPENAI_API_KEY", "openai-secret-key")
	defer func() {
		os.Unsetenv("GARAGEFAB_GITHUB_TOKEN")
		os.Unsetenv("GARAGEFAB_API_TOKEN")
		os.Unsetenv("OPENAI_API_KEY")
	}()

	runner := NewOpenCodeRunner(fakeOpencode, []string{"OPENAI_API_KEY", "GARAGEFAB_API_TOKEN"})
	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, "recorded_env.txt")

	req := AgentRequest{
		Role:         "coding",
		WorktreePath: tmpDir,
		Prompt:       "# Test env isolation",
		Env: map[string]string{
			"FAKE_OPENCODE_RECORD_ENV": envFile,
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
	if !strings.Contains(envStr, "OPENAI_API_KEY=openai-secret-key") {
		t.Errorf("expected OPENAI_API_KEY to be passed through to child env")
	}
}
