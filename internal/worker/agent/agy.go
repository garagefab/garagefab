// Package agent provides AI coding agent execution and lifecycle management.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Hexagonal Architecture — Infrastructure Driven Adapter for Google Antigravity `agy` CLI (D21).
//
// This adapter translates generic SDLC agent requests into concrete `agy` CLI invocations:
// 1. Invokes `agy` in headless print mode (`--print=<prompt>`) with full auto-approval (`--dangerously-skip-permissions`).
// 2. Enforces determinism by disabling interactive slash-commands (`--disable-slash-commands`).
// 3. Modulates model effort by SDLC role (`coding` -> medium, `spec`/`review`/`probe` -> high).
// 4. Parses the trailing JSON result envelope (`--output-format json`) into structured `AgentResult` models.
// 5. Never passes `--continue` or `--conversation` to guarantee completely clean, isolated sessions (D21).
//
// JAVA / ENTERPRISE BACKEND COMPARISONS:
//
//  1. Strategy Pattern Implementation:
//     In Java, this would be an `@Component public class AgyAgentRunner implements AgentRunner`
//     that uses `ProcessBuilder` to execute `agy` and Jackson `ObjectMapper` to parse stdout.
//     In Go, `AgyRunner` implicitly satisfies the `Runner` interface by implementing `Run`.
//
//  2. Output Envelope Validation:
//     Because CLI tools like `agy` return process exit code 0 even when the agent fails to complete
//     the prompt task (Spike A / Q6), the adapter cannot rely on OS exit codes alone. It parses
//     the final JSON output line to check `"status": "SUCCESS"`, translating task failures into
//     non-zero exit codes (1) so the factory pipeline triggers its automated repair loop.
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

// TestedAgyVersion is the verified and supported version of the agy CLI (Spike A, R3).
const TestedAgyVersion = "1.2.14"

// AgyRunner executes AI agent tasks via the Google Antigravity `agy` CLI tool (D21).
type AgyRunner struct {
	Binary         string   // Path or name of the agy binary (defaults to "agy")
	EnvPassthrough []string // Explicit environment variable names permitted through to agy (SEC-6)
}

// NewAgyRunner constructs a new runner for the `agy` CLI.
func NewAgyRunner(binary string, envPassthrough []string) *AgyRunner {
	if binary == "" {
		binary = "agy"
	}
	return &AgyRunner{
		Binary:         binary,
		EnvPassthrough: envPassthrough,
	}
}

// agyUsage captures model token consumption from the agy JSON result envelope.
type agyUsage struct {
	InputTokens    int `json:"input_tokens"`
	OutputTokens   int `json:"output_tokens"`
	ThinkingTokens int `json:"thinking_tokens"`
	TotalTokens    int `json:"total_tokens"`
}

// agyEnvelope represents the final JSON result structure emitted on stdout by `agy --output-format json`.
type agyEnvelope struct {
	ConversationID  string   `json:"conversation_id"`
	Status          string   `json:"status"`
	Response        string   `json:"response"`
	DurationSeconds float64  `json:"duration_seconds"`
	NumTurns        int      `json:"num_turns"`
	Usage           agyUsage `json:"usage"`
}

// Run executes a pipeline stage using `agy` inside req.WorktreePath.
func (r *AgyRunner) Run(ctx context.Context, req AgentRequest) (*AgentResult, error) {
	// Deliver prompt, spilling to ~/.garagefab/logs/<job>/prompt_*.md if prompt > 64 KiB (F9)
	promptArg, cleanup, err := deliverPrompt(req.Prompt, req.LogPath)
	if err != nil {
		return nil, fmt.Errorf("worker/agent: agy deliver prompt: %w", err)
	}
	defer cleanup()

	// Assign effort level based on SDLC role (Spike §3.3)
	effort := "high"
	if req.Role == "coding" {
		effort = "medium"
	}

	// Prepare CLI arguments:
	// - Attached form --print=<prompt> prevents flag parsing ambiguity
	// - --dangerously-skip-permissions enables unattended automation
	// - --output-format json emits structured completion metadata
	// - --disable-slash-commands prevents user global skills from altering headless pipeline steps
	// - Cwd = req.WorktreePath guarantees work is confined to the job worktree
	// - Never pass --continue or --conversation (D21 clean session invariant)
	args := []string{
		fmt.Sprintf("--print=%s", promptArg),
		"--dangerously-skip-permissions",
		"--output-format", "json",
		"--disable-slash-commands",
		"--effort", effort,
	}

	// Sanitize OS environment, allowing explicitly permitted passthrough variables (SEC-6)
	env := command.SanitizeEnvWithPassthrough(r.EnvPassthrough, req.Env)

	var (
		mu           sync.Mutex
		lastJSONLine string
	)

	// Stream hook captures the last stdout line starting with '{' (the completion envelope)
	onLine := func(stream, line string) {
		if stream == "stdout" {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "{") {
				mu.Lock()
				lastJSONLine = trimmed
				mu.Unlock()
			}
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

	// Result Mapping (per M5 specification):
	// 1. Subprocess timed out -> Exit 124 with TimedOut=true (pipeline categorizes as Blocked)
	if procRes.TimedOut {
		return &AgentResult{
			ExitCode: 124,
			Summary:  fmt.Sprintf("agent timed out after %v", req.Timeout),
			TimedOut: true,
			Duration: procRes.Duration,
		}, nil
	}

	// 2. Parent context cancelled -> Exit 130 with Cancelled=true
	if procRes.Cancelled {
		return &AgentResult{
			ExitCode:  130,
			Summary:   "agent cancelled",
			Cancelled: true,
			Duration:  procRes.Duration,
		}, nil
	}

	// 3. Process exited with non-zero status (crash, invalid flag, etc.)
	if procRes.ExitCode != 0 {
		summary := strings.TrimSpace(procRes.Tail)
		if summary == "" {
			summary = fmt.Sprintf("agy process exited with code %d", procRes.ExitCode)
		}
		return &AgentResult{
			ExitCode: procRes.ExitCode,
			Summary:  summary,
			Duration: procRes.Duration,
		}, nil
	}

	// 4. Process exited with 0: Inspect JSON completion envelope
	mu.Lock()
	jsonPayload := lastJSONLine
	mu.Unlock()

	if jsonPayload == "" {
		return &AgentResult{
			ExitCode: 1,
			Summary:  "agy produced no result envelope",
			Duration: procRes.Duration,
		}, nil
	}

	var envObj agyEnvelope
	if err := json.Unmarshal([]byte(jsonPayload), &envObj); err != nil {
		return &AgentResult{
			ExitCode: 1,
			Summary:  "agy produced no result envelope",
			Duration: procRes.Duration,
		}, nil
	}

	usage := Usage{
		PromptTokens:     envObj.Usage.InputTokens,
		CompletionTokens: envObj.Usage.OutputTokens,
		TotalTokens:      envObj.Usage.TotalTokens,
	}

	// Agent task completed but reported error/failure status -> Exit 1 (pipeline triggers repair loop)
	if envObj.Status != "SUCCESS" {
		return &AgentResult{
			ExitCode: 1,
			Summary:  fmt.Sprintf("agy reported status %s", envObj.Status),
			Duration: procRes.Duration,
			Usage:    usage,
		}, nil
	}

	// Successful execution -> Exit 0 with first 500 chars of response as summary
	summary := envObj.Response
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
