// Package github provides a driven adapter communicating with GitHub via the GitHub CLI ('gh').
//
// ==============================================================================
// ARCHITECTURAL ROLE & BOUNDARIES:
// Hexagonal Driven Adapter for GitHub Integration (architecture.md §6 Rule 6).
//
// In Clean / Hexagonal Architecture:
// `internal/provider/github` is a purely driven infrastructure adapter.
// Under Architectural Rule 6 (internal/boundaries_test.go):
//   - This package NEVER imports factory, store, server, worker, or intake.
//   - It defines its own Data Transfer Objects (DTOs) and sentinel errors.
//   - cmd/garagefab acts as the composition root that bridges these DTOs to the
//     inbound and outbound ports needed by the factory engine and intake poller.
//
// ENTERPRISE / JAVA SPRING COMPARISON:
//   - Spring CLI / Process Adapter: Equivalent to an infrastructure client bean
//     calling an external CLI tool or REST client, returning decoupled DTOs
//     rather than domain models or database entities.
//   - Typed Error Hierarchy: Replaces generic RuntimeExceptions with typed sentinels
//     (ErrGHNotAuthenticated, ErrGHNotFound) that callers can inspect using errors.Is().
//
// GO IDIOMS & CONCEPTS:
//   1. Sentinel Errors:
//      Standard library errors.New constants enable explicit, idiomatic error inspection
//      via errors.Is(err, ErrGHNotAuthenticated).
//   2. Error Wrapping Context:
//      Command failures wrap the underlying exit code and masked stderr output.
// ==============================================================================
package github

import (
	"errors"
	"fmt"
)

var (
	// ErrGHNotInstalled is returned when the 'gh' binary cannot be located on system PATH.
	ErrGHNotInstalled = errors.New("github: gh cli is not installed or not in PATH")

	// ErrGHNotAuthenticated is returned when 'gh' indicates the user is not authenticated.
	ErrGHNotAuthenticated = errors.New("github: gh cli is not authenticated (run 'gh auth login')")

	// ErrGHRateLimited is returned when GitHub API rate limits are encountered.
	ErrGHRateLimited = errors.New("github: rate limit exceeded")

	// ErrGHNotFound is returned when a requested repository, issue, or pull request does not exist.
	ErrGHNotFound = errors.New("github: resource not found")
)

// CommandError represents a failed execution of the GitHub CLI with captured output.
type CommandError struct {
	Args     []string
	ExitCode int
	Stderr   string
	Err      error
}

func (e *CommandError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("gh %v failed (exit %d): %s", e.Args, e.ExitCode, e.Stderr)
	}
	return fmt.Sprintf("gh %v failed: %v", e.Args, e.Err)
}

func (e *CommandError) Unwrap() error {
	return e.Err
}
