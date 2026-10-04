// Package github provides an execution runner for the GitHub CLI ('gh').
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Subprocess Runner & Mockable CLI Port (Hexagonal Architecture).
//
// In Clean / Hexagonal Architecture:
// `runner.go` abstracts subprocess execution behind the `GHRunner` interface.
// This decouples high-level GitHub client operations (ListIssues, CreatePR)
// from the low-level OS process execution (`os/exec`).
//
// Benefits:
//  1. Offline Unit Testing: Tests inject `FakeGHRunner` to simulate GitHub responses
//     with zero network calls, satisfying the offline testing invariant.
//  2. Safe Child Execution: Subprocesses execute non-interactively with prompt
//     and update notifiers disabled.
//  3. Credential Redaction: Stderr output is sanitized before error wrapping.
//
// ENTERPRISE / JAVA SPRING COMPARISON:
//   - Interface Segregation: Analogous to defining a `CommandExecutor` interface
//     in Java with a `DefaultProcessExecutor` for production and a mock bean for tests.
//   - Process Isolation: Similar to `java.lang.ProcessBuilder` configured with custom
//     environment variables and redirected IO.
//
// GO IDIOMS & CONCEPTS:
//  1. Context Cancellation (`context.Context`):
//     Subprocesses are executed with `exec.CommandContext`, ensuring that if a job
//     is cancelled or times out, the child `gh` process is terminated immediately.
//  2. Variadic Arguments (`...string`):
//     Permits passing variable numbers of CLI flags cleanly without slice allocation.
//
// ==============================================================================
package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// GHRunner defines the low-level execution port for invoking the GitHub CLI.
type GHRunner interface {
	Run(ctx context.Context, stdin []byte, args ...string) ([]byte, error)
}

// DefaultGHRunner executes real GitHub CLI subprocesses via os/exec.
type DefaultGHRunner struct {
	Binary string // Executable path or name (defaults to "gh")
}

// NewDefaultGHRunner creates a new production runner.
func NewDefaultGHRunner(binary string) *DefaultGHRunner {
	if binary == "" {
		binary = "gh"
	}
	return &DefaultGHRunner{Binary: binary}
}

// Run executes 'gh' with the specified arguments and stdin input, returning stdout bytes.
func (r *DefaultGHRunner) Run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	binary := r.Binary
	if binary == "" {
		binary = "gh"
	}

	cmd := exec.CommandContext(ctx, binary, args...)

	// Configure non-interactive CLI environment
	cmd.Env = append(os.Environ(),
		"GH_PROMPT_DISABLED=1",
		"GH_NO_UPDATE_NOTIFIER=1",
		"NO_COLOR=1",
	)

	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	stdoutBytes := stdoutBuf.Bytes()
	stderrStr := sanitizeOutput(stderrBuf.String())

	if err != nil {
		exitCode := 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}

		// Translate known error scenarios into typed sentinel errors
		lowerStderr := strings.ToLower(stderrStr)
		if errors.Is(err, exec.ErrNotFound) || strings.Contains(lowerStderr, "executable file not found") {
			return nil, fmt.Errorf("%w: %v", ErrGHNotInstalled, err)
		}
		if strings.Contains(lowerStderr, "not logged into any github hosts") ||
			strings.Contains(lowerStderr, "gh auth login") ||
			strings.Contains(lowerStderr, "authentication required") {
			return nil, fmt.Errorf("%w: %s", ErrGHNotAuthenticated, stderrStr)
		}
		if strings.Contains(lowerStderr, "rate limit exceeded") ||
			strings.Contains(lowerStderr, "api rate limit") {
			return nil, fmt.Errorf("%w: %s", ErrGHRateLimited, stderrStr)
		}
		if strings.Contains(lowerStderr, "could not resolve to a repository") ||
			strings.Contains(lowerStderr, "not found") ||
			strings.Contains(lowerStderr, "could not find") {
			return nil, fmt.Errorf("%w: %s", ErrGHNotFound, stderrStr)
		}

		return nil, &CommandError{
			Args:     args,
			ExitCode: exitCode,
			Stderr:   stderrStr,
			Err:      err,
		}
	}

	return stdoutBytes, nil
}

// sanitizeOutput redacts sensitive tokens (GH_TOKEN, GITHUB_TOKEN) from captured CLI stderr.
func sanitizeOutput(text string) string {
	if tok := os.Getenv("GH_TOKEN"); tok != "" {
		text = strings.ReplaceAll(text, tok, "***")
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		text = strings.ReplaceAll(text, tok, "***")
	}
	return text
}
