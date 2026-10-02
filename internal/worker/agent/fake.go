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
	FailCoding             bool          // If true, simulates a coding failure (exit code 1)
	FailReview             bool          // If true, produces a "request_changes" review decision
	ReviewDecision         string        // Overrides default review decision ("approve" vs "request_changes")
	SpecBehavior           string        // "spec" (default), "questions", "both", "neither", "invalid" (SPC-1, SPC-2)
	CustomSpecContent      string        // Overrides default valid spec content
	CustomQuestionsContent string        // Overrides default clarification questions content
	CustomExitCode         int           // Simulates arbitrary non-zero process exit codes
	SleepDuration          time.Duration // Simulates long-running agent work for concurrency testing
}

// NewFakeRunner returns a FakeRunner configured for the happy path (approves reviews, valid spec).
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{
		ReviewDecision: "approve",
		SpecBehavior:   "spec",
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
	case "02_Clarification_and_Spec":
		artifactDir := filepath.Join(req.WorktreePath, ".garagefab", "jobs", fmt.Sprintf("%d", req.JobID))
		_ = os.MkdirAll(artifactDir, 0700)

		behavior := f.SpecBehavior
		if behavior == "" {
			behavior = "spec"
		}

		switch behavior {
		case "questions":
			qContent := f.CustomQuestionsContent
			if qContent == "" {
				qContent = "Q1. Should we support SQLite or PostgreSQL?\n\nQ2. What is the required session timeout duration?\n"
			}
			_ = os.WriteFile(filepath.Join(artifactDir, "clarification-questions.md"), []byte(qContent), 0644)
			return &AgentResult{ExitCode: 0, Summary: "Fake agent output clarification questions"}, nil

		case "invalid":
			// Output invalid spec missing Acceptance Criteria
			invalidSpec := fmt.Sprintf("# %s\n\n## Summary\nInvalid spec.\n\n## Goals and Non-Goals\nNone.\n\n## Design\nNone.\n\n## Implementation Plan\n1. Do something.\n\n## Test Plan\nNone.\n\n## Risks and Assumptions\nNone.\n", req.ProjectName)
			_ = os.WriteFile(filepath.Join(artifactDir, "spec.md"), []byte(invalidSpec), 0644)
			return &AgentResult{ExitCode: 0, Summary: "Fake agent output invalid spec"}, nil

		case "both":
			// Write both files (violates SPC-1)
			_ = os.WriteFile(filepath.Join(artifactDir, "clarification-questions.md"), []byte("Q1. Ambiguous question?\n"), 0644)
			validSpec := "# Feature Spec\n\n## Summary\nSummary.\n\n## Goals and Non-Goals\nGoals.\n\n## Design\nDesign.\n\n## Acceptance Criteria\nGiven A When B Then AC-1: Pass.\n\n## Implementation Plan\n1. Work (AC-1)\n\n## Test Plan\nTests.\n\n## Risks and Assumptions\nNone.\n"
			_ = os.WriteFile(filepath.Join(artifactDir, "spec.md"), []byte(validSpec), 0644)
			return &AgentResult{ExitCode: 0, Summary: "Fake agent output both files"}, nil

		case "neither":
			// Output nothing
			return &AgentResult{ExitCode: 0, Summary: "Fake agent output nothing"}, nil

		default: // "spec"
			specContent := f.CustomSpecContent
			if specContent == "" {
				specContent = fmt.Sprintf(`# Feature Spec for %s

## Summary
Complete specification for %s feature.

## Goals and Non-Goals
Goals:
- Implement required functionality
Non-Goals:
- Out-of-scope refactoring

## Design
Modular hexagonal design isolating factory from worker and server.

## Acceptance Criteria
Given valid input When processed Then AC-1: returns expected output.

## Implementation Plan
1. Create core domain logic (AC-1)
2. Add end-to-end integration tests (AC-1)

## Test Plan
Automated unit tests and CI integration verification.

## Risks and Assumptions
Assumes standard execution environment.
`, req.ProjectName, req.ProjectName)
			}
			_ = os.WriteFile(filepath.Join(artifactDir, "spec.md"), []byte(specContent), 0644)
			return &AgentResult{ExitCode: 0, Summary: "Fake agent output valid spec"}, nil
		}

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
