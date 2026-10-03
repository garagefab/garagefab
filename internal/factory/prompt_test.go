// Package factory implements the core SDLC pipeline orchestrator and state engine.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Unit Verification for Prompt Template Engine (SPC-1/2, COD-1, COD-9, REV-1..4, PRB).
//
// Tests verify:
// 1. Byte-exact golden file matching per role and scenario (with -update flag).
// 2. COD-1: coding prompt isolation (repair contains failure output only, no prior chatter).
// 3. REV-1: review prompt isolation (contains diff and verification results, no coding agent conversation).
// 4. SPC-1/2: spec prompt rules ("never assume on ambiguity", "produce exactly one file", 7 headings).
// 5. Omission of absent sections (no empty headers).
// 6. Unknown role and template errors.
// ==============================================================================
package factory

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "update golden prompt files")

func TestRenderPrompt_GoldenFiles(t *testing.T) {
	scenarios := []struct {
		name string
		role string
		data PromptData
	}{
		{
			name: "spec_first_run",
			role: RoleSpec,
			data: PromptData{
				JobID:       101,
				WorkType:    WorkTypeFeature,
				Intent:      "Add OAuth2 Google login button to landing page.",
				ArtifactDir: ".garagefab/jobs/101",
			},
		},
		{
			name: "spec_clarification",
			role: RoleSpec,
			data: PromptData{
				JobID:         102,
				WorkType:      WorkTypeFeature,
				Intent:        "Add export to CSV.",
				ArtifactDir:   ".garagefab/jobs/102",
				Clarification: "## Q1\nWhich columns should be exported?\n**Answer:** All visible table columns.",
			},
		},
		{
			name: "spec_bugfix",
			role: RoleSpec,
			data: PromptData{
				JobID:       103,
				WorkType:    WorkTypeBugFix,
				Intent:      "Fix nil pointer dereference on empty user profile.",
				ArtifactDir: ".garagefab/jobs/103",
			},
		},
		{
			name: "coding_first_run",
			role: RoleCoding,
			data: PromptData{
				JobID:          201,
				WorkType:       WorkTypeFeature,
				Intent:         "Add OAuth2 Google login button.",
				ArtifactDir:    ".garagefab/jobs/201",
				Spec:           "# OAuth2 Login\n## Summary\nImplement Google OAuth2 flow.",
				BuildCmds:      []string{"go build ./..."},
				TestCmds:       []string{"go test ./..."},
				LintCmds:       []string{"golangci-lint run"},
				ProtectedPaths: []string{"go.mod", "internal/core/**"},
			},
		},
		{
			name: "coding_repair",
			role: RoleCoding,
			data: PromptData{
				JobID:          202,
				WorkType:       WorkTypeFeature,
				Intent:         "Add OAuth2 Google login button.",
				ArtifactDir:    ".garagefab/jobs/202",
				Spec:           "# OAuth2 Login\n## Summary\nImplement Google OAuth2 flow.",
				RepairFeedback: "--- FAIL: TestLogin (0.01s)\n    login_test.go:42: expected 200, got 500",
				BuildCmds:      []string{"go build ./..."},
				TestCmds:       []string{"go test ./..."},
			},
		},
		{
			name: "coding_rejection",
			role: RoleCoding,
			data: PromptData{
				JobID:          203,
				WorkType:       WorkTypeFeature,
				Intent:         "Add OAuth2 Google login button.",
				ArtifactDir:    ".garagefab/jobs/203",
				Spec:           "# OAuth2 Login\n## Summary\nImplement Google OAuth2 flow.",
				RejectionNotes: []string{"Button color does not match corporate branding guidelines."},
				BuildCmds:      []string{"go build ./..."},
			},
		},
		{
			name: "review_feature",
			role: RoleReview,
			data: PromptData{
				JobID:          301,
				WorkType:       WorkTypeFeature,
				Intent:         "Add OAuth2 Google login button.",
				ArtifactDir:    ".garagefab/jobs/301",
				Spec:           "# OAuth2 Login\n## Summary\nImplement Google OAuth2 flow.",
				Diff:           "--- a/login.go\n+++ b/login.go\n@@ -10,1 +10,2 @@\n+func GoogleAuth() {}\n",
				CommandSummary: []string{"Attempt 1 command (exit 0, status: passed): /logs/build.log"},
			},
		},
		{
			name: "probe_bugfix",
			role: RoleProbe,
			data: PromptData{
				JobID:       401,
				WorkType:    WorkTypeBugFix,
				Intent:      "Fix panic when user email is blank.",
				ArtifactDir: ".garagefab/jobs/401",
			},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			rendered, err := RenderPrompt(sc.role, sc.data)
			if err != nil {
				t.Fatalf("RenderPrompt failed: %v", err)
			}

			goldenPath := filepath.Join("testdata", "prompts", sc.name+".golden")
			if *updateGolden {
				if err := os.WriteFile(goldenPath, []byte(rendered), 0644); err != nil {
					t.Fatalf("failed to update golden file %s: %v", goldenPath, err)
				}
				return
			}

			expectedBytes, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("failed to read golden file %s (run go test -update): %v", goldenPath, err)
			}
			expected := string(expectedBytes)

			if rendered != expected {
				t.Errorf("rendered prompt does not match golden file %s.\nGot:\n%s\nExpected:\n%s", goldenPath, rendered, expected)
			}
		})
	}
}

// TestRenderPrompt_COD1_NoPriorAgentChatter tests requirement COD-1:
// Coding repair prompt contains the failure feedback and no prior agent conversational text.
func TestRenderPrompt_COD1_NoPriorAgentChatter(t *testing.T) {
	data := PromptData{
		JobID:          501,
		WorkType:       WorkTypeFeature,
		Intent:         "Implement feature X",
		ArtifactDir:    ".garagefab/jobs/501",
		RepairFeedback: "test failure: nil pointer at main.go:25",
	}

	rendered, err := RenderPrompt(RoleCoding, data)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	if !strings.Contains(rendered, "[Automated Repair Feedback on Previous Attempt]") {
		t.Errorf("expected automated repair feedback marker")
	}
	if !strings.Contains(rendered, "test failure: nil pointer at main.go:25") {
		t.Errorf("expected failure output in prompt")
	}
	if strings.Contains(rendered, "I have updated the code according to your instructions") {
		t.Errorf("COD-1 violated: found conversational agent chatter in repair prompt")
	}
}

// TestRenderPrompt_REV1_NoCodingAgentOutput tests requirement REV-1:
// Review prompt contains diff and command summaries, but never coding-agent output.
func TestRenderPrompt_REV1_NoCodingAgentOutput(t *testing.T) {
	data := PromptData{
		JobID:          601,
		WorkType:       WorkTypeFeature,
		Intent:         "Implement payment flow",
		ArtifactDir:    ".garagefab/jobs/601",
		Diff:           "+func ProcessPayment() {}",
		CommandSummary: []string{"Attempt 1 (exit 0): passed"},
	}

	rendered, err := RenderPrompt(RoleReview, data)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	if !strings.Contains(rendered, "+func ProcessPayment() {}") {
		t.Errorf("expected diff in review prompt")
	}
	if strings.Contains(rendered, "Coding agent response") {
		t.Errorf("REV-1 violated: found coding agent output in review prompt")
	}
}

// TestRenderPrompt_SPC1_2_RequiredRulesAndHeadings tests requirements SPC-1 & SPC-2:
// Spec prompt contains "never assume on ambiguity", "produce exactly one file", and required headings.
func TestRenderPrompt_SPC1_2_RequiredRulesAndHeadings(t *testing.T) {
	data := PromptData{
		JobID:       701,
		WorkType:    WorkTypeBugFix,
		Intent:      "Fix race condition in pool",
		ArtifactDir: ".garagefab/jobs/701",
	}

	rendered, err := RenderPrompt(RoleSpec, data)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	if !strings.Contains(rendered, "Never assume on ambiguity") {
		t.Errorf("SPC-2 violated: missing 'Never assume on ambiguity' rule")
	}
	if !strings.Contains(rendered, "EXACTLY ONE") {
		t.Errorf("SPC-1 violated: missing 'EXACTLY ONE' rule")
	}

	requiredHeadings := []string{
		"## Summary",
		"## Goals and Non-Goals",
		"## Design",
		"## Acceptance Criteria",
		"## Implementation Plan",
		"## Test Plan",
		"## Risks and Assumptions",
		"## Reproduction",
	}
	for _, h := range requiredHeadings {
		if !strings.Contains(rendered, h) {
			t.Errorf("SPC-1 violated: missing heading %s in spec prompt", h)
		}
	}
}

// TestRenderPrompt_OmittedSectionsWhenEmpty tests that empty fields do not generate empty headers.
func TestRenderPrompt_OmittedSectionsWhenEmpty(t *testing.T) {
	data := PromptData{
		JobID:       801,
		WorkType:    WorkTypeRefactor,
		Intent:      "Clean up imports",
		ArtifactDir: ".garagefab/jobs/801",
	}

	rendered, err := RenderPrompt(RoleSpec, data)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	if strings.Contains(rendered, "Clarification History") {
		t.Errorf("expected Clarification History section to be omitted when empty")
	}
	if strings.Contains(rendered, "[Automated Repair Feedback on Previous Attempt]") {
		t.Errorf("expected Automated Repair Feedback section to be omitted when empty")
	}
}

// TestRenderPrompt_UnknownRole_Error tests that rendering an unknown role returns an error.
func TestRenderPrompt_UnknownRole_Error(t *testing.T) {
	data := PromptData{
		JobID:       901,
		WorkType:    WorkTypeFeature,
		Intent:      "Test",
		ArtifactDir: ".garagefab/jobs/901",
	}

	_, err := RenderPrompt("non_existent_role", data)
	if err == nil {
		t.Fatalf("expected error for unknown role, got nil")
	}
	if !strings.Contains(err.Error(), "unknown role") {
		t.Errorf("unexpected error message: %v", err)
	}
}
