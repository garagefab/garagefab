// Package agent provides AI coding agent execution and lifecycle management.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Worker Abstraction Layer — Dynamic Strategy Registry & Dispatcher (HND-2, COD-10).
//
// The SDLC pipeline designates different agent tools per role (e.g. `agy` for coding,
// `opencode` for review, or `fake` in testing). The Router decouples the factory orchestrator
// from the set of registered agent adapters by acting as an Inversion-of-Control (IoC) registry.
//
// JAVA / SPRING BOOT COMPARISONS:
//
//  1. Strategy Pattern via Spring Dependency Injection:
//     In Spring Boot, you might autowire `Map<String, AgentRunner> runners` into an
//     `AgentRoutingService`. At runtime, `runners.get(req.getAgent())` looks up the bean by name.
//     If missing, it throws `NoSuchBeanDefinitionException`.
//     In Go, the `Router` struct holds a `map[string]Runner` protected by `sync.RWMutex`,
//     dynamically dispatching to the configured runner or returning `ErrAgentUnavailable`.
//
//  2. Interface Subtyping:
//     In Java, `FakeRunner implements Runner`. In Go, any struct defining
//     `Run(context.Context, AgentRequest) (*AgentResult, error)` automatically satisfies `Runner`.
//
// ==============================================================================
package agent

import (
	"context"
	"fmt"
	"sync"
)

// Router coordinates multiple named agent execution runners (e.g. "agy", "opencode", "fake").
type Router struct {
	mu      sync.RWMutex
	runners map[string]Runner
}

// NewRouter creates a new agent router pre-registered with the default "fake" runner for testing.
func NewRouter() *Router {
	r := &Router{
		runners: make(map[string]Runner),
	}
	r.Register("fake", NewFakeRunner())
	return r
}

// Register registers or replaces an agent runner under the given name.
func (r *Router) Register(name string, runner Runner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runners[name] = runner
}

// Run looks up the agent specified in req.Agent and delegates execution.
// If no runner is registered for req.Agent, it returns ErrAgentUnavailable.
func (r *Router) Run(ctx context.Context, req AgentRequest) (*AgentResult, error) {
	r.mu.RLock()
	runner, ok := r.runners[req.Agent]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: unknown agent %q", ErrAgentUnavailable, req.Agent)
	}

	return runner.Run(ctx, req)
}
