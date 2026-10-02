// Package command_test contains unit tests for the subprocess command runner.
//
// ==============================================================================
// GO TESTING CONCEPTS:
//
//  1. Testing Process Execution:
//     Verifies that shell commands execute with stdout/stderr separation, exit code
//     capture, and timestamped streaming log outputs.
//
//  2. Manipulating and Restoring OS Environment in Tests:
//     When modifying process-wide state like environment variables with `os.Setenv`,
//     always use `defer os.Unsetenv(...)` to avoid contaminating other concurrent tests.
//
// ==============================================================================
package command_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/worker/command"
)

// TestCommandRunner_Run_And_Log_LOG2 tests requirements:
// - LOG-2: Command outputs are written to the log file with timestamped [stdout] and [stderr] tags.
// - RCV-1: Process startup callback receives valid PID and PGID values.
func TestCommandRunner_Run_And_Log_LOG2(t *testing.T) {
	runner := command.NewRunner()
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	var startedPID, startedPGID int
	var startedTime int64

	// Execute shell command that writes to both stdout and stderr
	res, err := runner.Run(context.Background(), command.RunOptions{
		WorkDir: tmpDir,
		Command: "echo 'hello stdout' && echo 'hello stderr' >&2",
		LogPath: logPath,
		OnProcessStart: func(pid, pgid int, startTime int64) {
			startedPID = pid
			startedPGID = pgid
			startedTime = startTime
		},
	})

	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "hello stdout") {
		t.Errorf("expected stdout to contain 'hello stdout', got: %s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "hello stderr") {
		t.Errorf("expected stderr to contain 'hello stderr', got: %s", res.Stderr)
	}

	// Verify RCV-1: callback received positive PID and PGID
	if startedPID <= 0 || startedPGID <= 0 || startedTime <= 0 {
		t.Errorf("expected positive pid/pgid/time, got pid=%d, pgid=%d, time=%d", startedPID, startedPGID, startedTime)
	}

	// Verify LOG-2: Log file contains timestamps and stream identifiers
	logContent, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file failed: %v", err)
	}
	logStr := string(logContent)
	if !strings.Contains(logStr, "[stdout] hello stdout") {
		t.Errorf("LOG-2 violated: expected [stdout] tag in log file, got:\n%s", logStr)
	}
	if !strings.Contains(logStr, "[stderr] hello stderr") {
		t.Errorf("LOG-2 violated: expected [stderr] tag in log file, got:\n%s", logStr)
	}
}

// TestSanitizeEnv_SEC6 verifies requirement SEC-6:
// Sensitive environment variables (containing TOKEN, SECRET, KEY, PASSWORD) are stripped.
func TestSanitizeEnv_SEC6(t *testing.T) {
	// Inject simulated sensitive environment variables into test process
	os.Setenv("GARAGEFAB_API_TOKEN", "super-secret-token")
	os.Setenv("GITHUB_TOKEN", "ghp_secret")
	os.Setenv("AWS_SECRET_KEY", "aws-secret")
	// Clean up environment variables when test finishes
	defer func() {
		os.Unsetenv("GARAGEFAB_API_TOKEN")
		os.Unsetenv("GITHUB_TOKEN")
		os.Unsetenv("AWS_SECRET_KEY")
	}()

	custom := map[string]string{
		"MY_CUSTOM_VAR":  "value123",
		"ANOTHER_SECRET": "must-be-stripped",
	}

	sanitized := command.SanitizeEnv(custom)

	// Verify no secrets leaked into sanitized environment slice
	for _, entry := range sanitized {
		if strings.Contains(entry, "super-secret-token") ||
			strings.Contains(entry, "GARAGEFAB") ||
			strings.Contains(entry, "GITHUB_TOKEN") ||
			strings.Contains(entry, "AWS_SECRET_KEY") ||
			strings.Contains(entry, "must-be-stripped") {
			t.Errorf("SEC-6 violated: forbidden secret leaked into environment: %s", entry)
		}
	}

	// Verify benign custom variables are retained
	foundCustom := false
	for _, entry := range sanitized {
		if entry == "MY_CUSTOM_VAR=value123" {
			foundCustom = true
			break
		}
	}
	if !foundCustom {
		t.Errorf("expected MY_CUSTOM_VAR to be present in sanitized env")
	}
}
