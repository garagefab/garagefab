// Package agent provides AI coding agent execution and lifecycle management.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Hexagonal Architecture — Infrastructure Driven Adapter for OpenCode CLI (D21, COD-10, LOG-2).
//
// This adapter translates generic SDLC agent requests into concrete `opencode` CLI invocations:
// 1. Invokes `opencode` in unattended mode (`opencode run --auto --format json --dir <worktree> <prompt>`).
// 2. Streams and parses NDJSON event lines in O(1) memory, tracking step starts, finishes, and tool use.
// 3. Detects stream errors (`{"type":"error"}`) and premature stream truncation without `step_finish`.
// 4. Maps completion reasons and tokens to unified `AgentResult` models.
// 5. Enforces fresh sessions by omitting `--continue` or `--session` (D21 clean session invariant).
//
// JAVA / ENTERPRISE BACKEND COMPARISONS:
//
//  1. Reactive NDJSON Stream Processing:
//     In enterprise Java (e.g. Spring WebFlux or Project Reactor), consuming line-delimited JSON
//     is done using reactive streams (`Flux<Event>`) to avoid buffering unbounded output in memory.
//     In Go, `onLine` processes each line as it is read by the 2 MiB buffered scanner, updating
//     a compact, thread-safe state struct with O(1) memory consumption.
//
//  2. State Machine Accumulator:
//     Instead of accumulating a list of all historical events, the parser accumulates only
//     summary metrics: step counters, the latest text response, and the terminal reason.
//     Even if the agent emits 50,000 tool events, memory remains constant.
//
// ==============================================================================
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/garagefab/garagefab/internal/worker/command"
)

// TestedOpenCodeVersion is the verified and supported version of the opencode CLI (Spike A, R3).
const TestedOpenCodeVersion = "1.18.34"

// OpenCodeRunner executes AI agent tasks via the OpenCode CLI tool (D21).
type OpenCodeRunner struct {
	Binary         string   // Path or name of the opencode binary (defaults to "opencode")
	EnvPassthrough []string // Explicit environment variable names permitted through to opencode (SEC-6)
}

// NewOpenCodeRunner constructs a new runner for the `opencode` CLI.
func NewOpenCodeRunner(binary string, envPassthrough []string) *OpenCodeRunner {
	if binary == "" {
		binary = "opencode"
	}
	return &OpenCodeRunner{
		Binary:         binary,
		EnvPassthrough: envPassthrough,
	}
}

// opencodeTokens records token usage inside step_finish parts.
type opencodeTokens struct {
	Total  int `json:"total"`
	Input  int `json:"input"`
	Output int `json:"output"`
}

// opencodePart represents event sub-payloads in OpenCode NDJSON streams.
type opencodePart struct {
	Type   string         `json:"type"`
	Text   string         `json:"text"`
	Reason string         `json:"reason"`
	Tokens opencodeTokens `json:"tokens"`
}

// opencodeEvent represents individual line events in the OpenCode NDJSON format.
type opencodeEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
	Part opencodePart `json:"part"`
}

// opencodeStreamState accumulates O(1) metrics from the streaming NDJSON events.
type opencodeStreamState struct {
	mu           sync.Mutex
	stepStarts   int
	stepFinishes int
	lastReason   string
	lastText     string
	errorMessage string
	totalTokens  int
	inputTokens  int
	outputTokens int
}

// Run executes a pipeline stage using `opencode` inside req.WorktreePath.
func (r *OpenCodeRunner) Run(ctx context.Context, req AgentRequest) (*AgentResult, error) {
	// Deliver prompt, spilling to file if prompt > 64 KiB (F9)
	promptArg, cleanup, err := deliverPrompt(req.Prompt, req.LogPath)
	if err != nil {
		return nil, fmt.Errorf("worker/agent: opencode deliver prompt: %w", err)
	}
	defer cleanup()

	// Prepare CLI arguments:
	// - run subcommand executes non-interactively
	// - --auto grants permission approval for tools
	// - --format json emits NDJSON stream
	// - --dir points to the worktree
	// - Positional message argument is the prompt
	// - Never pass --continue or --session (D21 fresh session invariant)
	args := []string{
		"run",
		"--auto",
		"--format", "json",
		"--dir", req.WorktreePath,
		promptArg,
	}

	env := command.SanitizeEnvWithPassthrough(r.EnvPassthrough, req.Env)
	state := &opencodeStreamState{}

	// Stream hook parses NDJSON lines as they arrive (O(1) memory)
	onLine := func(stream, line string) {
		if stream != "stdout" {
			return
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "{") {
			return
		}

		var ev opencodeEvent
		if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
			return
		}

		state.mu.Lock()
		defer state.mu.Unlock()

		switch ev.Type {
		case "step_start":
			state.stepStarts++
		case "step_finish":
			state.stepFinishes++
			state.lastReason = ev.Part.Reason
			if ev.Part.Tokens.Total > 0 {
				state.totalTokens = ev.Part.Tokens.Total
				state.inputTokens = ev.Part.Tokens.Input
				state.outputTokens = ev.Part.Tokens.Output
			}
		case "text":
			if ev.Part.Text != "" {
				state.lastText = ev.Part.Text
			}
		case "error":
			msg := ev.Message
			if msg == "" && ev.Error != nil {
				msg = ev.Error.Message
			}
			if msg == "" {
				msg = "opencode error event encountered"
			}
			state.errorMessage = msg
		}
	}

	spec := procSpec{
		Binary:  r.Binary,
		Args:    args,
		Dir:     req.WorktreePath,
		Env:     env,
		LogPath: req.LogPath,
		Timeout: req.Timeout,
		OnStart: req.OnProcessStart,
		OnLine:  onLine,
	}

	procRes, err := runProc(ctx, spec)
	if err != nil {
		return nil, err
	}

	// 1. Timeout -> Exit 124 with TimedOut=true (COD-10)
	if procRes.TimedOut {
		return &AgentResult{
			ExitCode: 124,
			Summary:  fmt.Sprintf("agent timed out after %v", req.Timeout),
			TimedOut: true,
			Duration: procRes.Duration,
		}, nil
	}

	// 2. Cancellation
	if procRes.Cancelled {
		return &AgentResult{
			ExitCode:  130,
			Summary:   "agent cancelled",
			Cancelled: true,
			Duration:  procRes.Duration,
		}, nil
	}

	// 3. Subprocess exited non-zero
	if procRes.ExitCode != 0 {
		summary := strings.TrimSpace(procRes.Tail)
		if summary == "" {
			summary = fmt.Sprintf("opencode process exited with code %d", procRes.ExitCode)
		}
		return &AgentResult{
			ExitCode: procRes.ExitCode,
			Summary:  summary,
			Duration: procRes.Duration,
		}, nil
	}

	// 4. Subprocess exited 0: evaluate NDJSON stream state
	state.mu.Lock()
	defer state.mu.Unlock()

	usage := Usage{
		PromptTokens:     state.inputTokens,
		CompletionTokens: state.outputTokens,
		TotalTokens:      state.totalTokens,
	}

	// Case A: Error event encountered in stream -> Exit 1
	if state.errorMessage != "" {
		return &AgentResult{
			ExitCode: 1,
			Summary:  state.errorMessage,
			Duration: procRes.Duration,
			Usage:    usage,
		}, nil
	}

	// Case B: No step_finish received (stream truncated) -> Exit 1
	if state.stepFinishes == 0 {
		return &AgentResult{
			ExitCode: 1,
			Summary:  "opencode stream ended without step_finish",
			Duration: procRes.Duration,
		}, nil
	}

	// Case C: step_finish ended with reason other than "stop" -> Exit 1
	if state.lastReason != "stop" {
		return &AgentResult{
			ExitCode: 1,
			Summary:  fmt.Sprintf("opencode stream ended with reason %s", state.lastReason),
			Duration: procRes.Duration,
			Usage:    usage,
		}, nil
	}

	// Case D: Success
	summary := state.lastText
	if len(summary) > 500 {
		summary = summary[:500]
	}

	return &AgentResult{
		ExitCode: 0,
		Summary:  summary,
		Duration: procRes.Duration,
		Usage:    usage,
	}, nil
}
