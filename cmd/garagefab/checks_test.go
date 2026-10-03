// Package main_test or main contains unit tests for CLI checks.
//
// ==============================================================================
// GO TESTING CONCEPTS & JAVA / JUNIT COMPARISON:
//
//  1. Table-Driven Tests:
//     The slice of structs (`tests := []struct { ... }`) is the idiomatic Go way to write
//     parameterized tests.
//     In Java/JUnit 5, you would use `@ParameterizedTest` with `@MethodSource` or `@CsvSource`.
//     In Go, tests are plain code: you iterate over a slice with `for _, tt := range tests`.
//
// 2. Error Reporting (`t.Errorf` vs `t.Fatalf`):
//
//   - `t.Errorf`: Marks the test as failed but CONTINUES running subsequent test cases.
//     This is like `assertAll` or soft assertions in JUnit.
//
//   - `t.Fatalf`: Fails and STOPS the test execution immediately (like JUnit's `fail()`).
//
//     3. No Separate Assertion Library:
//     Standard Go does not use assertion libraries like AssertJ or Hamcrest. Plain `if`
//     checks with `t.Errorf` are preferred for clarity and zero external dependencies.
//
// ==============================================================================
package main

import (
	"errors"
	"strings"
	"testing"
)

// TestParseGitVersion verifies that validateGitVersionOutput correctly parses
// git version strings and enforces the minimum version requirement (git 2.30.0+).
func TestParseGitVersion(t *testing.T) {
	// Table-driven test cases defining input and expected outcome
	tests := []struct {
		output string // Mocked output from `git --version`
		valid  bool   // Expected validity (true if >= 2.30.0, false otherwise)
	}{
		{"git version 2.30.0", true},                 // Minimum supported version
		{"git version 2.34.1 (Apple Git-147)", true}, // Common macOS Xcode git string
		{"git version 2.54.0", true},                 // Higher minor version
		{"git version 3.0.0", true},                  // Future major version
		{"git version 2.29.9", false},                // Unsupported older version
		{"git version 1.9.5", false},                 // Very old version
		{"invalid output", false},                    // Corrupted or unrecognized output
	}

	// Iterate through each test case in the table
	for _, tt := range tests {
		err := validateGitVersionOutput(tt.output)
		// (err == nil) evaluates to true if validation succeeded
		if (err == nil) != tt.valid {
			t.Errorf("validateGitVersionOutput(%q): expected valid=%v, got err=%v", tt.output, tt.valid, err)
		}
	}
}

// TestCheckAgent_Preflight_R3 tests requirement R3:
// Exact version match produces no warning; mismatch produces a warning only (never a hard error);
// missing binary produces a clear missing warning.
func TestCheckAgent_Preflight_R3(t *testing.T) {
	tests := []struct {
		name                 string
		agentName            string
		lookPathErr          error
		versionOutput        string
		versionErr           error
		wantInstalled        bool
		wantVersionMismatch  bool
		wantWarningSubstring string
	}{
		{
			name:                 "agy exact tested version match",
			agentName:            "agy",
			lookPathErr:          nil,
			versionOutput:        "1.2.14\n",
			wantInstalled:        true,
			wantVersionMismatch:  false,
			wantWarningSubstring: "",
		},
		{
			name:                 "opencode exact tested version match",
			agentName:            "opencode",
			lookPathErr:          nil,
			versionOutput:        "opencode 1.18.34 (arm64)\n",
			wantInstalled:        true,
			wantVersionMismatch:  false,
			wantWarningSubstring: "",
		},
		{
			name:                 "agy version mismatch warning only (R3)",
			agentName:            "agy",
			lookPathErr:          nil,
			versionOutput:        "1.3.0\n",
			wantInstalled:        true,
			wantVersionMismatch:  true,
			wantWarningSubstring: "differs from tested version 1.2.14 (R3)",
		},
		{
			name:                 "opencode version mismatch warning only (R3)",
			agentName:            "opencode",
			lookPathErr:          nil,
			versionOutput:        "2.0.0\n",
			wantInstalled:        true,
			wantVersionMismatch:  true,
			wantWarningSubstring: "differs from tested version 1.18.34 (R3)",
		},
		{
			name:                 "missing binary on PATH",
			agentName:            "agy",
			lookPathErr:          errors.New("executable file not found in $PATH"),
			wantInstalled:        false,
			wantVersionMismatch:  false,
			wantWarningSubstring: "not found on PATH. Jobs requiring it will fail Blocked.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := CheckAgent(
				tt.agentName,
				func(bin string) (string, error) {
					return "/mock/bin/" + bin, tt.lookPathErr
				},
				func(bin string) (string, error) {
					return tt.versionOutput, tt.versionErr
				},
			)

			if res.Installed != tt.wantInstalled {
				t.Errorf("Installed = %v, want %v", res.Installed, tt.wantInstalled)
			}
			if res.VersionMismatch != tt.wantVersionMismatch {
				t.Errorf("VersionMismatch = %v, want %v", res.VersionMismatch, tt.wantVersionMismatch)
			}
			if tt.wantWarningSubstring == "" {
				if res.Warning != "" {
					t.Errorf("expected no warning, got: %q", res.Warning)
				}
			} else {
				if !strings.Contains(res.Warning, tt.wantWarningSubstring) {
					t.Errorf("warning %q does not contain %q", res.Warning, tt.wantWarningSubstring)
				}
			}
		})
	}
}
