// Package agent provides AI coding agent execution and lifecycle management.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Unit Verification for Agent Router and Strategy Registry (HND-2, COD-10).
//
// Tests verify:
// 1. Default registration of the "fake" test double runner.
// 2. Dynamic registration and dispatching to custom runners.
// 3. Typed ErrAgentUnavailable returned for unconfigured/unknown agent names.
// ==============================================================================
package agent

import (
	"context"
	"errors"
	"testing"
)

// mockRunner is a test double used to verify custom agent dispatching.
type mockRunner struct {
	invoked bool
	gotReq  AgentRequest
}

func (m *mockRunner) Run(ctx context.Context, req AgentRequest) (*AgentResult, error) {
	m.invoked = true
	m.gotReq = req
	return &AgentResult{
		ExitCode: 0,
		Summary:  "mock executed",
	}, nil
}

// TestRouter_DefaultFakeRegistration tests that NewRouter automatically configures
// the "fake" runner so testing environments work out-of-the-box.
func TestRouter_DefaultFakeRegistration(t *testing.T) {
	r := NewRouter()

	req := AgentRequest{
		Agent:  "fake",
		Prompt: "write test code",
	}

	res, err := r.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("expected fake runner to execute successfully, got error: %v", err)
	}

	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
}

// TestRouter_RegisterAndDispatch tests registering a custom runner and dispatching to it.
func TestRouter_RegisterAndDispatch(t *testing.T) {
	r := NewRouter()
	mock := &mockRunner{}
	r.Register("custom-agent", mock)

	req := AgentRequest{
		Agent:  "custom-agent",
		Role:   "coding",
		Prompt: "implement feature",
	}

	res, err := r.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mock.invoked {
		t.Errorf("expected custom runner to be invoked")
	}
	if mock.gotReq.Prompt != "implement feature" {
		t.Errorf("expected prompt 'implement feature', got %q", mock.gotReq.Prompt)
	}
	if res.Summary != "mock executed" {
		t.Errorf("unexpected summary: %s", res.Summary)
	}
}

// TestRouter_UnknownAgent_ErrAgentUnavailable tests that unknown agent names
// return a typed ErrAgentUnavailable.
func TestRouter_UnknownAgent_ErrAgentUnavailable(t *testing.T) {
	r := NewRouter()

	req := AgentRequest{
		Agent: "non-existent-agent",
	}

	_, err := r.Run(context.Background(), req)
	if err == nil {
		t.Fatalf("expected error for unknown agent, got nil")
	}

	if !errors.Is(err, ErrAgentUnavailable) {
		t.Errorf("expected ErrAgentUnavailable, got %v", err)
	}
}
