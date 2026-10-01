package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// FakeRunner simulates agent executions for testing and M1 walking skeleton (COD-1/2/9, REV).
type FakeRunner struct {
	FailCoding     bool
	FailReview     bool
	ReviewDecision string
	CustomExitCode int
	SleepDuration  time.Duration
}

// NewFakeRunner returns a new FakeRunner with default passing behaviors.
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{
		ReviewDecision: "approve",
	}
}

// Run executes the fake agent step according to the stage.
func (f *FakeRunner) Run(ctx context.Context, req AgentRequest) (*AgentResult, error) {
	if req.OnProcessStart != nil {
		req.OnProcessStart(os.Getpid(), os.Getpid(), time.Now().Unix())
	}

	if f.SleepDuration > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.SleepDuration):
		}
	}

	// Write log entries if LogPath is provided (LOG-2)
	if req.LogPath != "" {
		_ = os.MkdirAll(filepath.Dir(req.LogPath), 0700)
		logLine := fmt.Sprintf("[%s] [stdout] Fake agent started for job %d at stage %s\n",
			time.Now().UTC().Format(time.RFC3339Nano), req.JobID, req.Stage)
		_ = os.WriteFile(req.LogPath, []byte(logLine), 0600)
	}

	if f.CustomExitCode != 0 {
		return &AgentResult{
			ExitCode: f.CustomExitCode,
			Summary:  fmt.Sprintf("Fake agent exited with code %d", f.CustomExitCode),
		}, nil
	}

	switch req.Stage {
	case "04_Coding":
		if f.FailCoding {
			return &AgentResult{
				ExitCode: 1,
				Summary:  "Fake coding step failed",
			}, nil
		}

		// Simulate writing code to worktree (e.g. creating/modifying a file)
		if req.WorktreePath != "" {
			artifactDir := filepath.Join(req.WorktreePath, ".garagefab", "jobs", fmt.Sprintf("%d", req.JobID))
			_ = os.MkdirAll(artifactDir, 0700)

			codeFile := filepath.Join(req.WorktreePath, "refactor_output.txt")
			_ = os.WriteFile(codeFile, []byte("refactored code output"), 0600)
		}

		return &AgentResult{
			ExitCode: 0,
			Summary:  "Fake agent refactored code successfully",
		}, nil

	case "05_Independent_Review":
		decision := f.ReviewDecision
		if decision == "" {
			decision = "approve"
		}
		if f.FailReview {
			decision = "request_changes"
		}

		// Write review.json in .garagefab/jobs/<id>/ (spec §6.2)
		var reviewPath string
		if req.WorktreePath != "" {
			artifactDir := filepath.Join(req.WorktreePath, ".garagefab", "jobs", fmt.Sprintf("%d", req.JobID))
			_ = os.MkdirAll(artifactDir, 0700)

			reviewJSON := fmt.Sprintf(`{
  "schema_version": 1,
  "decision": %q,
  "summary": "Fake independent review completed.",
  "risk": {
    "side_effect": {"score": 1, "rationale": "No unexpected side effects detected"},
    "performance": {"score": 1, "rationale": "No performance impact"},
    "backward_compatibility": {"score": 1, "rationale": "Fully backward compatible"}
  },
  "findings": [],
  "warnings": [],
  "spec_coverage": []
}`, decision)

			reviewPath = filepath.Join(artifactDir, "review.json")
			if err := os.WriteFile(reviewPath, []byte(reviewJSON), 0600); err != nil {
				return nil, fmt.Errorf("fake agent: write review.json: %w", err)
			}
		}

		return &AgentResult{
			ExitCode:     0,
			ArtifactPath: reviewPath,
			Summary:      fmt.Sprintf("Fake review finished with decision: %s", decision),
		}, nil

	default:
		return &AgentResult{
			ExitCode: 0,
			Summary:  fmt.Sprintf("Fake agent completed stage %s", req.Stage),
		}, nil
	}
}
