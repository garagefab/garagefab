// Package agent_test contains unit tests for agent runner implementations.
//
// ==============================================================================
// GO TESTING CONCEPTS:
//
//  1. Testing Side Effects (Filesystem & Artifacts):
//     Unlike pure mathematical functions, agent runners produce filesystem changes
//     (code edits, log entries, review artifacts). Tests verify these side effects
//     using `os.Stat` and `os.ReadFile`.
//
//  2. Anonymous Functions as Callbacks:
//     Notice the inline closure `OnProcessStart: func(pid, pgid int, startTime int64) { ... }`.
//     The closure captures local variables (`startedPID`, `startedTime`) from the outer scope,
//     allowing the test to verify that the runner correctly triggered the callback.
//
// ==============================================================================
package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/garagefab/garagefab/internal/worker/agent"
)

// TestFakeRunner_Coding_COD1 verifies the Coding stage simulation:
// It creates a refactor output file, generates log records, and fires the process start callback.
func TestFakeRunner_Coding_COD1(t *testing.T) {
	runner := agent.NewFakeRunner()
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "coding.log")

	var startedPID, startedPGID int
	var startedTime int64

	res, err := runner.Run(context.Background(), agent.AgentRequest{
		JobID:        42,
		Stage:        "04_Coding",
		WorktreePath: tmpDir,
		LogPath:      logPath,
		// Callback captures process lifecycle data
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

	// Verify code file was created in worktree (simulating agent code edits)
	codeFile := filepath.Join(tmpDir, "refactor_output.txt")
	if _, err := os.Stat(codeFile); os.IsNotExist(err) {
		t.Errorf("expected refactor_output.txt to be created in worktree")
	}

	// Verify log file was written (LOG-2)
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("expected log file to be created at %s", logPath)
	}

	// Verify process callback captured non-zero PID and timestamp
	if startedPID <= 0 || startedTime <= 0 {
		t.Errorf("expected valid pid and time, got pid=%d, time=%d", startedPID, startedTime)
	}
	_ = startedPGID
}

// TestFakeRunner_Review_REV verifies the Independent Review stage simulation:
// It generates a schema-compliant review.json artifact with decision="approve".
func TestFakeRunner_Review_REV(t *testing.T) {
	runner := agent.NewFakeRunner()
	tmpDir := t.TempDir()

	res, err := runner.Run(context.Background(), agent.AgentRequest{
		JobID:        42,
		Stage:        "05_Independent_Review",
		WorktreePath: tmpDir,
	})

	if err != nil {
		t.Fatalf("Run review failed: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}

	// Verify review.json was written to .garagefab/jobs/<id>/
	reviewFile := filepath.Join(tmpDir, ".garagefab", "jobs", "42", "review.json")
	data, err := os.ReadFile(reviewFile)
	if err != nil {
		t.Fatalf("read review.json failed: %v", err)
	}

	// Parse review JSON into generic map to validate schema and decision
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("review.json is not valid JSON: %v", err)
	}
	if parsed["decision"] != "approve" {
		t.Errorf("expected decision approve, got %v", parsed["decision"])
	}
}
