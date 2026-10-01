package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/garagefab/garagefab/internal/worker/agent"
)

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

	// Verify code file was created in worktree
	codeFile := filepath.Join(tmpDir, "refactor_output.txt")
	if _, err := os.Stat(codeFile); os.IsNotExist(err) {
		t.Errorf("expected refactor_output.txt to be created in worktree")
	}

	// Verify log file was written
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("expected log file to be created at %s", logPath)
	}

	if startedPID <= 0 || startedTime <= 0 {
		t.Errorf("expected valid pid and time, got pid=%d, time=%d", startedPID, startedTime)
	}
	_ = startedPGID
}

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

	reviewFile := filepath.Join(tmpDir, ".garagefab", "jobs", "42", "review.json")
	data, err := os.ReadFile(reviewFile)
	if err != nil {
		t.Fatalf("read review.json failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("review.json is not valid JSON: %v", err)
	}
	if parsed["decision"] != "approve" {
		t.Errorf("expected decision approve, got %v", parsed["decision"])
	}
}
