// Package factory contains the core domain model, pipeline engine, and scheduler
// for the software factory.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Unit Test / Domain Specification (Hexagonal / Clean Architecture).
//
//  1. Architectural Role:
//     This test file verifies the pure domain logic of the Failure Categorizer
//     (COD-4, architecture.md §9.4). Because CategorizeFailure is a pure function
//     with zero external dependencies, these tests run with microsecond speed,
//     zero mocks, and complete determinism.
//
//  2. Enterprise / Java Testing Comparison:
//     Analogous to JUnit 5 `@ParameterizedTest` with `@MethodSource` in Java/Spring,
//     Go prefers table-driven tests: a slice of anonymous structs declaring test cases,
//     inputs, and expected results, executed via `t.Run(tc.name, func(t *testing.T) { ... })`.
//
// ==============================================================================
package factory_test

import (
	"testing"

	"github.com/garagefab/garagefab/internal/factory"
)

// TestFailureCategorizer_COD4 verifies deterministic classification of step failures
// into Flawed, Blocked, and Manual categories (COD-4, COD-7, COD-10, GRD-4).
func TestFailureCategorizer_COD4(t *testing.T) {
	tests := []struct {
		name     string
		input    factory.FailureInput
		expected string
	}{
		{
			name: "Test failure with non-zero exit code returns Flawed",
			input: factory.FailureInput{
				ExitCode:    1,
				Stdout:      "=== RUN TestSomething\n--- FAIL: TestSomething",
				Stderr:      "FAIL\nexit status 1",
				Attempt:     1,
				MaxAttempts: 3,
			},
			expected: factory.FailureFlawed,
		},
		{
			name: "Compiler error with non-zero exit code returns Flawed",
			input: factory.FailureInput{
				ExitCode:    2,
				Stderr:      "main.go:12:3: undefined: FooBar",
				Attempt:     1,
				MaxAttempts: 3,
			},
			expected: factory.FailureFlawed,
		},
		{
			name: "Lint error with non-zero exit code returns Flawed",
			input: factory.FailureInput{
				ExitCode:    1,
				Stderr:      "internal/service.go:5:1: exported function should have comment",
				Attempt:     2,
				MaxAttempts: 3,
			},
			expected: factory.FailureFlawed,
		},
		{
			name: "Empty diff when agent exits 0 returns Flawed (COD-7)",
			input: factory.FailureInput{
				ExitCode:    0,
				EmptyDiff:   true,
				Attempt:     1,
				MaxAttempts: 3,
			},
			expected: factory.FailureFlawed,
		},
		{
			name: "Guardrail violation returns Flawed (GRD-4)",
			input: factory.FailureInput{
				ExitCode:           0,
				GuardrailViolation: true,
				Stderr:             "guardrail violation: modified protected path auth_test.go",
				Attempt:            1,
				MaxAttempts:        3,
			},
			expected: factory.FailureFlawed,
		},
		{
			name: "Process timeout returns Blocked (COD-10)",
			input: factory.FailureInput{
				TimedOut:    true,
				ExitCode:    -1,
				Attempt:     1,
				MaxAttempts: 3,
			},
			expected: factory.FailureBlocked,
		},
		{
			name: "Command not found (exit code 127) returns Blocked",
			input: factory.FailureInput{
				ExitCode:    127,
				Stderr:      "sh: go: command not found",
				Attempt:     1,
				MaxAttempts: 3,
			},
			expected: factory.FailureBlocked,
		},
		{
			name: "Command permission denied (exit code 126) returns Blocked",
			input: factory.FailureInput{
				ExitCode:    126,
				Stderr:      "sh: ./custom-tool: Permission denied",
				Attempt:     1,
				MaxAttempts: 3,
			},
			expected: factory.FailureBlocked,
		},
		{
			name: "OS-level missing executable in stderr returns Blocked",
			input: factory.FailureInput{
				ExitCode:    1,
				Stderr:      "exec: executable file not found in $PATH",
				Attempt:     1,
				MaxAttempts: 3,
			},
			expected: factory.FailureBlocked,
		},
		{
			name: "Attempts exhausted at max_repair_attempts returns Manual",
			input: factory.FailureInput{
				ExitCode:    1,
				Stderr:      "FAIL: TestFeature",
				Attempt:     3,
				MaxAttempts: 3,
			},
			expected: factory.FailureManual,
		},
		{
			name: "Attempts exceeded beyond max_repair_attempts returns Manual",
			input: factory.FailureInput{
				ExitCode:    1,
				Stderr:      "FAIL: TestFeature",
				Attempt:     4,
				MaxAttempts: 3,
			},
			expected: factory.FailureManual,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := factory.CategorizeFailure(tc.input)
			if result != tc.expected {
				t.Fatalf("expected category %q, got %q", tc.expected, result)
			}
		})
	}
}
