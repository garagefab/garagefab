// Package agent provides concrete and test implementations of AI agent runners.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Test Double (Fake Object Pattern) — Walking Skeleton Enabler.
//
// `FakeRunner` simulates realistic AI agent executions for testing and for the
// Walking Skeleton (Milestone 1). Without needing real LLM API keys or CLI installations,
// `FakeRunner` exercises the entire pipeline: creating code files, generating structured
// `review.json` artifacts, handling log files, and triggering callbacks.
//
// GO CONCEPTS & JAVA COMPARISONS:
//
//  1. Channel Multiplexing with `select` vs Java Thread.sleep:
//     In Java: Pausing an asynchronous task requires `Thread.sleep(ms)` and catching
//     `InterruptedException`.
//     In Go: We use a `select` statement listening to two channels simultaneously:
//     select {
//     case <-ctx.Done():                // Wakes up if context is cancelled/timed out
//     return nil, ctx.Err()
//     case <-time.After(sleepDuration): // Wakes up when timer channel emits a timestamp
//     }
//     This guarantees instantaneous, cooperative cancellation without blocking operating system threads.
//
// ==============================================================================
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// FakeRunner simulates agent executions for unit tests and the M1 walking skeleton (COD-1/2/9, REV).
// It can be configured to succeed, fail, delay, or return specific review decisions.
type FakeRunner struct {
	FailCoding     bool          // If true, simulates a coding failure (exit code 1)
	FailReview     bool          // If true, produces a "request_changes" review decision
	ReviewDecision string        // Overrides default review decision ("approve" vs "request_changes")
	CustomExitCode int           // Simulates arbitrary non-zero process exit codes
	SleepDuration  time.Duration // Simulates long-running agent work for concurrency testing
}

// NewFakeRunner returns a FakeRunner configured for the happy path (approves reviews).
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{
		ReviewDecision: "approve",
	}
}

// Run executes the fake agent step according to the stage specified in req.
func (f *FakeRunner) Run(ctx context.Context, req AgentRequest) (*AgentResult, error) {
	// Trigger the startup callback so callers can record the simulated PID (RCV-1)
	if req.OnProcessStart != nil {
		req.OnProcessStart(os.Getpid(), os.Getpid(), time.Now().Unix())
	}

	// If configured with a delay, wait while respecting context cancellation
	if f.SleepDuration > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.SleepDuration):
		}
	}

	// Write log entries if a LogPath is specified (LOG-2)
	if req.LogPath != "" {
		_ = os.MkdirAll(filepath.Dir(req.LogPath), 0700)
		logLine := fmt.Sprintf("[%s] [stdout] Fake agent started for job %d at stage %s\n",
			time.Now().UTC().Format(time.RFC3339Nano), req.JobID, req.Stage)
		_ = os.WriteFile(req.LogPath, []byte(logLine), 0600)
	}

	// Return custom exit code if set for failure testing
	if f.CustomExitCode != 0 {
		return &AgentResult{
			ExitCode: f.CustomExitCode,
			Summary:  fmt.Sprintf("Fake agent exited with code %d", f.CustomExitCode),
		}, nil
	}

	// Stage-dependent simulation
	switch req.Stage {
	case "04_Coding":
		if f.FailCoding {
			return &AgentResult{
				ExitCode: 1,
				Summary:  "Fake coding step failed",
			}, nil
		}

		// Simulate the AI agent modifying code in the worktree
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

		// Write structured review.json adhering to spec §6.2
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
		// Default fallback for any other stages
		return &AgentResult{
			ExitCode: 0,
			Summary:  fmt.Sprintf("Fake agent completed stage %s", req.Stage),
		}, nil
	}
}
