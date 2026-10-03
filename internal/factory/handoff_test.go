// Package factory implements the core SDLC pipeline orchestrator and state engine.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Unit Verification for Handoff Command Formatting (HND-1, HND-2).
//
// Tests verify:
// 1. POSIX shell quoting: safe paths bare, paths with spaces quoted, paths with single quotes escaped.
// 2. Exact command formatting: cd <path> ; <agent> garagefab-work <jobID>.
// 3. Status eligibility: only human intervention statuses return true.
// ==============================================================================
package factory

import (
	"testing"
)

// TestHandoffCommand_Quoting_HND1 tests requirement HND-1:
// Single-quotes paths with spaces/metacharacters, escapes embedded quotes, and leaves safe paths bare.
func TestHandoffCommand_Quoting_HND1(t *testing.T) {
	tests := []struct {
		name        string
		projectPath string
		agent       string
		jobID       int64
		expected    string
	}{
		{
			name:        "path with spaces",
			projectPath: "/Users/me/my app",
			agent:       "agy",
			jobID:       178,
			expected:    `cd '/Users/me/my app' ; agy -i "/caveman garagefab-work 178"`,
		},
		{
			name:        "path with single quotes",
			projectPath: "/Users/me/my'app",
			agent:       "agy",
			jobID:       178,
			expected:    `cd '/Users/me/my'\''app' ; agy -i "/caveman garagefab-work 178"`,
		},
		{
			name:        "safe path unquoted",
			projectPath: "/Users/me/projects/my-app",
			agent:       "opencode",
			jobID:       42,
			expected:    `cd /Users/me/projects/my-app ; opencode --prompt "/caveman garagefab-work 42"`,
		},
		{
			name:        "empty agent returns empty",
			projectPath: "/Users/me/myapp",
			agent:       "",
			jobID:       10,
			expected:    "",
		},
		{
			name:        "empty path returns empty",
			projectPath: "",
			agent:       "agy",
			jobID:       10,
			expected:    "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := HandoffCommand(tc.projectPath, tc.agent, tc.jobID)
			if actual != tc.expected {
				t.Errorf("expected:\n%s\ngot:\n%s", tc.expected, actual)
			}
		})
	}
}

// TestHandoffEligible_Statuses_HND1 tests requirement HND-1:
// Only needs_clarification, spec_review, awaiting_approval, failed, and interrupted are eligible.
func TestHandoffEligible_Statuses_HND1(t *testing.T) {
	eligible := []string{
		StatusNeedsClarification,
		StatusSpecReview,
		StatusAwaitingApproval,
		StatusFailed,
		StatusInterrupted,
	}

	for _, s := range eligible {
		if !HandoffEligible(s) {
			t.Errorf("expected status %s to be eligible for handoff", s)
		}
	}

	ineligible := []string{
		StatusQueued,
		StatusRunning,
		StatusDone,
		StatusCancelled,
		"unknown_status",
	}

	for _, s := range ineligible {
		if HandoffEligible(s) {
			t.Errorf("expected status %s to be ineligible for handoff", s)
		}
	}
}
