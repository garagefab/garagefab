// Package factory contains the core domain model, pipeline engine, and scheduler
// for the software factory.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Pure Domain Logic / Value Object Policy (Hexagonal / Clean Architecture).
//
//  1. Architectural Role:
//     This file implements the deterministic failure categorization logic required
//     by COD-4 and architecture.md §9.4. Under Clean Architecture, this is a pure
//     domain rule: it contains no side effects, performs zero I/O, does not import
//     infrastructure packages (no database/sql, no exec, no network), and operates
//     exclusively on pure input data structures.
//
//  2. Enterprise / Java DDD Comparison:
//     In Java/Spring enterprise architectures, this is equivalent to a Domain Service
//     or Policy Specification pattern:
//     com.garagefab.domain.service.FailureCategorizationPolicy
//     Rather than scattering if/else statements across controller or service handlers,
//     all failure classification rules are centralized in an immutable, deterministic,
//     and easily unit-testable domain component.
//
//  3. Go Idioms:
//     - Pure Functions: CategorizeFailure is a pure function (same inputs always yield
//     the exact same output).
//     - Value Receivers & Structs: FailureInput is passed by value or reference without
//     retaining pointers or mutating caller state.
//
// ==============================================================================
package factory

import (
	"strings"
)

// FailureInput encapsulates all signals and telemetry extracted from an agent or command
// execution to decide whether the failure can be repaired or must be escalated.
type FailureInput struct {
	ExitCode           int    // Process exit status (0 = success, non-zero = error)
	TimedOut           bool   // Whether the process exceeded its allotted timeout window
	EmptyDiff          bool   // True if the coding agent exited 0 without editing/creating files (COD-7)
	GuardrailViolation bool   // True if protected paths or custom guardrail checks failed (GRD-1..4)
	Stdout             string // Captured standard output
	Stderr             string // Captured standard error
	Attempt            int    // Current attempt count (0-indexed or 1-indexed)
	MaxAttempts        int    // Maximum allowed repair attempts (default 3)
}

// CategorizeFailure deterministically classifies an execution failure into one of three
// fundamental categories (COD-4, architecture.md §9.4):
//
//   - FailureFlawed: A code- or artifact-level defect that an agent can potentially fix
//     (e.g., test failure, compile error, lint violation, empty changes, guardrail violation).
//     Triggers an automated repair loop while attempts remain.
//   - FailureBlocked: An environmental or systemic failure that agent retries cannot resolve
//     (e.g., process timeout, command not found, permission denied, missing tool binary).
//     Immediately halts the job and marks it failed.
//   - FailureManual: The maximum allowed repair attempts have been exhausted. Halts the job
//     and requests human attention.
func CategorizeFailure(input FailureInput) string {
	// Rule 1: Exhaustion of repair attempts always escalates to Manual intervention.
	if input.MaxAttempts > 0 && input.Attempt >= input.MaxAttempts {
		return FailureManual
	}

	// Rule 2: Timeouts (COD-10) represent hung processes or infinite loops.
	// Retrying with the same inputs is unlikely to succeed without human tuning.
	if input.TimedOut {
		return FailureBlocked
	}

	// Rule 3: POSIX exit codes 126 (Command cannot execute / permission denied)
	// and 127 (Command not found) represent environmental and configuration defects.
	if input.ExitCode == 126 || input.ExitCode == 127 {
		return FailureBlocked
	}

	// Rule 4: Systemic environment/permission failures detected in stderr.
	combined := strings.ToLower(input.Stderr + "\n" + input.Stdout)
	if hasSystemicFailure(combined) {
		return FailureBlocked
	}

	// Rule 5: Empty diff (COD-7) — Agent claimed success (exit 0) but modified no files.
	// Classified as Flawed so the repair loop prompts the agent to actually make changes.
	if input.EmptyDiff {
		return FailureFlawed
	}

	// Rule 6: Guardrail violations (GRD-4) — e.g. modified protected test files.
	// Classified as Flawed once per attempt, prompting the agent to undo changes.
	if input.GuardrailViolation {
		return FailureFlawed
	}

	// Rule 7: Non-zero exit codes from build, test, or lint tools indicate code defects.
	if input.ExitCode != 0 {
		return FailureFlawed
	}

	return FailureFlawed
}

// hasSystemicFailure checks for fatal OS-level or environment indicators that
// mean the agent environment itself is broken rather than user code having a bug.
func hasSystemicFailure(output string) bool {
	systemicMarkers := []string{
		"permission denied (os error",
		"executable file not found in $path",
		"no such file or directory: /bin/",
		"no such file or directory: /usr/",
		"disk quota exceeded",
		"no space left on device",
		"connection refused",
	}

	for _, marker := range systemicMarkers {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}
