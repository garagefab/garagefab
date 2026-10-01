package command_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/worker/command"
)

func TestCommandRunner_Run_And_Log_LOG2(t *testing.T) {
	runner := command.NewRunner()
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	var startedPID, startedPGID int
	var startedTime int64

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

	// Verify RCV-1 callback values
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

func TestSanitizeEnv_SEC6(t *testing.T) {
	// Set mock environment variables
	os.Setenv("GARAGEFAB_API_TOKEN", "super-secret-token")
	os.Setenv("GITHUB_TOKEN", "ghp_secret")
	os.Setenv("AWS_SECRET_KEY", "aws-secret")
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

	for _, entry := range sanitized {
		if strings.Contains(entry, "super-secret-token") ||
			strings.Contains(entry, "GARAGEFAB") ||
			strings.Contains(entry, "GITHUB_TOKEN") ||
			strings.Contains(entry, "AWS_SECRET_KEY") ||
			strings.Contains(entry, "must-be-stripped") {
			t.Errorf("SEC-6 violated: forbidden secret leaked into environment: %s", entry)
		}
	}

	// Custom non-secret var should be preserved
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
