// Package github provides test doubles for GitHub CLI interactions.
//
// ==============================================================================
// ARCHITECTURAL ROLE & TESTING CONCEPTS:
// Test Double (Mock / Fake) for Subprocess GHRunner.
//
// In Clean / Hexagonal Architecture:
// `fake_runner.go` allows unit and integration tests across the codebase
// to exercise GitHub operations (intake polling, issue feedback, PR creation)
// entirely offline with zero network connectivity or external dependencies.
//
// ENTERPRISE / JAVA SPRING COMPARISON:
//   - Mockito / WireMock: Similar to mocking HTTP or CLI responses in Java tests
//     (`when(cliRunner.run(...)).thenReturn(...)`).
//   - Thread-Safe In-Memory Recording: Captures all invocations (`Calls`) for assertion.
//
// GO IDIOMS & CONCEPTS:
//   1. Mutex Protection (`sync.Mutex`):
//      Protects recorded calls and response matchers against concurrent test access.
//   2. Higher-Order Matcher Functions:
//      Allows callers to match commands by prefix, argument equality, or custom predicates.
// ==============================================================================
package github

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// GHCall records an individual invocation of the GHRunner.
type GHCall struct {
	Args  []string
	Stdin []byte
}

// CommandString returns the full command invocation as a readable string (e.g. "gh issue list --repo foo").
func (c GHCall) CommandString() string {
	return "gh " + strings.Join(c.Args, " ")
}

// FakeGHRunner is a thread-safe test double implementing GHRunner.
type FakeGHRunner struct {
	mu       sync.Mutex
	calls    []GHCall
	handlers []func(call GHCall) ([]byte, error, bool)
}

// NewFakeGHRunner constructs a fresh fake runner.
func NewFakeGHRunner() *FakeGHRunner {
	return &FakeGHRunner{}
}

// On registers a custom matching function and its canned response.
func (f *FakeGHRunner) On(matcher func(args []string) bool, response []byte, err error) *FakeGHRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers = append(f.handlers, func(call GHCall) ([]byte, error, bool) {
		if matcher(call.Args) {
			return response, err, true
		}
		return nil, nil, false
	})
	return f
}

// OnPrefix registers a response for commands matching a specific prefix of CLI arguments.
func (f *FakeGHRunner) OnPrefix(prefix []string, response []byte, err error) *FakeGHRunner {
	return f.On(func(args []string) bool {
		if len(args) < len(prefix) {
			return false
		}
		for i := range prefix {
			if args[i] != prefix[i] {
				return false
			}
		}
		return true
	}, response, err)
}

// OnCommand registers a response when args contains the given subcommand sequence (e.g. "issue list").
func (f *FakeGHRunner) OnCommand(subcommand string, response []byte, err error) *FakeGHRunner {
	parts := strings.Fields(subcommand)
	return f.OnPrefix(parts, response, err)
}

// Run records the call and returns the configured response from registered handlers.
func (f *FakeGHRunner) Run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	call := GHCall{
		Args:  args,
		Stdin: stdin,
	}
	f.calls = append(f.calls, call)

	// Evaluate handlers in reverse order (most recently added first)
	for i := len(f.handlers) - 1; i >= 0; i-- {
		resp, err, matched := f.handlers[i](call)
		if matched {
			return resp, err
		}
	}

	// Default response if no handler matched
	return []byte("{}"), nil
}

// Calls returns a copy of all recorded CLI calls.
func (f *FakeGHRunner) Calls() []GHCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]GHCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// Reset clears recorded calls and handlers.
func (f *FakeGHRunner) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
	f.handlers = nil
}

// HasCallContaining returns true if any recorded call contains the target argument substring.
func (f *FakeGHRunner) HasCallContaining(substr string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(c.CommandString(), substr) {
			return true
		}
	}
	return false
}

// FindCallsWithPrefix returns all recorded calls matching the specified argument prefix.
func (f *FakeGHRunner) FindCallsWithPrefix(prefix ...string) []GHCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var matching []GHCall
	for _, c := range f.calls {
		if len(c.Args) >= len(prefix) {
			match := true
			for i := range prefix {
				if c.Args[i] != prefix[i] {
					match = false
					break
				}
			}
			if match {
				matching = append(matching, c)
			}
		}
	}
	return matching
}

// LastCall returns the most recent call, or an error if no calls were made.
func (f *FakeGHRunner) LastCall() (GHCall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return GHCall{}, fmt.Errorf("no calls recorded")
	}
	return f.calls[len(f.calls)-1], nil
}
