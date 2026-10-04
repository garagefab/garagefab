// Package agent provides AI coding agent execution and lifecycle management.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Hexagonal Architecture — Infrastructure Adapter for Subprocess Execution (SEC-6, LOG-2, RCV-1, COD-10).
//
// This file implements low-level OS process orchestration shared by all external agent
// CLI runners (Google Antigravity `agy`, OpenCode, etc.):
//  1. Process Group Isolation (Setpgid: true) to eliminate orphaned child processes (RCV-1).
//  2. Real-time Output Streaming with 2 MiB scanner buffers and RFC3339Nano log formatting (LOG-2).
//  3. Graceful Termination Lifecycle (SIGTERM -> 10s grace -> SIGKILL) on timeout or cancel (COD-10).
//  4. Bounded 64 KiB Tail Buffer to prevent memory exhaustion during long agent runs.
//  5. Prompt Spilling (F9) to avoid OS ARG_MAX limits when prompts exceed 64 KiB.
//
// GO CONCEPTS & JAVA / ENTERPRISE BACKEND COMPARISONS:
//
//  1. Subprocess Lifecycle Management (Java `ProcessBuilder` & `ProcessHandle`):
//     In modern Java (Java 9+), `ProcessHandle.descendants()` is used to find and destroy
//     child processes. In Unix/Go, setting `Setpgid: true` creates a Process Group ID (PGID)
//     matching the child PID. Signaling `-pgid` atomically broadcasts signals to the process
//     and all its descendants, preventing background daemon leaks.
//
//  2. Output Streaming & Memory Protection:
//     In enterprise Java, pipe deadlocks are prevented using separate threads or reactive
//     publishers with backpressure. In Go, two concurrent goroutines drain stdout and stderr
//     independently using buffered scanners, writing to disk while keeping a fixed-size ring
//     buffer in memory for error context.
//
//  3. Graceful Shutdown Cascades:
//     Similar to Spring Boot's graceful shutdown (`server.shutdown=graceful`) where an HTTP
//     service stops accepting traffic, waits for in-flight requests, and then aborts, `runProc`
//     signals `SIGTERM`, grants a 10-second grace window, and escalates to `SIGKILL` only
//     if the process fails to terminate.
//
// ==============================================================================
package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/garagefab/garagefab/internal/worker/command"
	"github.com/garagefab/garagefab/internal/worker/logbuf"
)

// defaultGracePeriod is the maximum time granted to a process group after SIGTERM before SIGKILL (COD-10).
const defaultGracePeriod = 10 * time.Second

// maxPromptArgBytes is the maximum prompt length passed directly via command-line arguments (64 KiB).
// Prompts exceeding this threshold are spilled to a temporary markdown file to avoid OS ARG_MAX limits (F9).
const maxPromptArgBytes = 64 * 1024

// maxTailBufferBytes is the maximum size of the in-memory output buffer kept for error reporting (64 KiB).
const maxTailBufferBytes = logbuf.DefaultMaxBytes

// procSpec defines the configuration required to execute an external agent subprocess.
type procSpec struct {
	Binary      string                    // Executable name or absolute path (e.g. "agy", "opencode")
	Args        []string                  // Command-line arguments
	Dir         string                    // Working directory (job worktree)
	Env         []string                  // Sanitized environment variables
	LogPath     string                    // Destination file path for streaming log output (LOG-2)
	Timeout     time.Duration             // Timeout duration before process group termination (COD-10)
	GracePeriod time.Duration             // Grace duration between SIGTERM and SIGKILL (defaults to 10s)
	OnStart     ProcessStartFunc          // Hook invoked immediately after OS process fork (RCV-1)
	OnLine      func(stream, line string) // Hook invoked for each line of stdout/stderr (e.g. envelope parser)
}

// procResult encapsulates the execution outcome of an agent subprocess.
type procResult struct {
	ExitCode  int           // Process exit status (0 = success)
	Tail      string        // Bounded 64 KiB tail of interleaved stdout/stderr for error summaries
	TimedOut  bool          // True if the process group was killed due to timeout expiration
	Cancelled bool          // True if the parent context was cancelled
	Duration  time.Duration // Wall-clock execution time
}

// deliverPrompt ensures prompt arguments do not exceed OS argument length limits (F9).
// If prompt is <= 64 KiB, it is returned verbatim.
// If prompt is > 64 KiB, it is written to a prompt_<timestamp>.md file (0600) next to logPath,
// outside the git worktree, and an instruction directing the agent to read the file is returned.
func deliverPrompt(prompt, logPath string) (string, func(), error) {
	if len(prompt) <= maxPromptArgBytes {
		return prompt, func() {}, nil
	}

	var dir string
	if logPath != "" {
		dir = filepath.Dir(logPath)
	} else {
		dir = os.TempDir()
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", nil, fmt.Errorf("worker/agent: deliverPrompt mkdir: %w", err)
	}

	filename := fmt.Sprintf("prompt_%d.md", time.Now().UnixNano())
	promptFile := filepath.Join(dir, filename)
	absPath, err := filepath.Abs(promptFile)
	if err != nil {
		absPath = promptFile
	}

	if err := os.WriteFile(absPath, []byte(prompt), 0600); err != nil {
		return "", nil, fmt.Errorf("worker/agent: deliverPrompt write: %w", err)
	}

	// The spilled file is retained for audit/retention (LOG-3 style).
	// Cleanup is a no-op, but provided in case callers want hook customization.
	cleanup := func() {}

	msg := fmt.Sprintf("Read the full task instructions from %s and follow them exactly.", absPath)
	return msg, cleanup, nil
}

// runProc executes an agent CLI command as an isolated subprocess with streaming logs,
// process group isolation, and graceful termination handling (SEC-6, LOG-2, RCV-1, COD-10).
func runProc(ctx context.Context, p procSpec) (procResult, error) {
	if err := ctx.Err(); err != nil {
		return procResult{Cancelled: true}, err
	}

	// Open destination log file if requested before spawning the process
	var logFile *os.File
	if p.LogPath != "" {
		if err := os.MkdirAll(filepath.Dir(p.LogPath), 0700); err != nil {
			return procResult{}, fmt.Errorf("worker/agent: create log dir: %w", err)
		}
		f, err := os.OpenFile(p.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return procResult{}, fmt.Errorf("worker/agent: open log file: %w", err)
		}
		defer f.Close()
		logFile = f
	}

	// Stdin is /dev/null so agents never block waiting for interactive terminal input
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return procResult{}, fmt.Errorf("worker/agent: open dev/null: %w", err)
	}
	defer devNull.Close()

	// Derive timeout context if configured
	procCtx := ctx
	var procCancel context.CancelFunc
	if p.Timeout > 0 {
		procCtx, procCancel = context.WithTimeout(ctx, p.Timeout)
		defer procCancel()
	}

	// Prepare exec command
	cmd := exec.CommandContext(procCtx, p.Binary, p.Args...)
	cmd.Dir = p.Dir
	cmd.Env = p.Env
	cmd.Stdin = devNull

	// Override standard library Cancel func to prevent default immediate SIGKILL.
	// We handle graceful escalation (SIGTERM -> 10s grace -> SIGKILL) via a dedicated monitor.
	cmd.Cancel = func() error {
		return nil
	}

	// Isolate process and its future child processes into a dedicated Process Group (RCV-1, SEC-6)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return procResult{}, fmt.Errorf("worker/agent: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return procResult{}, fmt.Errorf("worker/agent: stderr pipe: %w", err)
	}

	startTime := time.Now()

	// Launch subprocess
	if err := cmd.Start(); err != nil {
		// Start failure (binary not found, permission denied, etc.) returns typed ErrAgentUnavailable
		return procResult{}, fmt.Errorf("%w: failed to start %q: %v", ErrAgentUnavailable, p.Binary, err)
	}

	pid := cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		pgid = pid
	}

	// Notify caller that process has started BEFORE processing any output lines (RCV-1)
	if p.OnStart != nil {
		p.OnStart(pid, pgid, startTime.Unix())
	}

	// Configure graceful termination monitor
	grace := defaultGracePeriod
	if p.GracePeriod > 0 {
		grace = p.GracePeriod
	}

	doneCh := make(chan struct{})
	go func() {
		select {
		case <-procCtx.Done():
			// Context expired (timeout or cancellation) -> send SIGTERM to process group (-pgid)
			if pgid > 0 {
				_ = syscall.Kill(-pgid, syscall.SIGTERM)
			}
			// Wait for process to exit gracefully within grace period, or escalate to SIGKILL
			select {
			case <-doneCh:
				return
			case <-time.After(grace):
				if pgid > 0 {
					_ = syscall.Kill(-pgid, syscall.SIGKILL)
				}
			}
		case <-doneCh:
			return
		}
	}()

	// Buffers and synchronization primitives for concurrent stream capture
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		tailBuf = logbuf.NewTailBuffer(maxTailBufferBytes)
	)

	streamOutput := func(reader io.Reader, streamName string) {
		defer wg.Done()
		scanner := bufio.NewScanner(reader)
		// Expand scanner buffer to 2 MiB to handle large JSON envelopes or tokens without truncation (LOG-2)
		scannerBuf := make([]byte, 64*1024)
		scanner.Buffer(scannerBuf, 2*1024*1024)

		for scanner.Scan() {
			text := command.MaskTokens(scanner.Text())
			nowStr := time.Now().UTC().Format(time.RFC3339Nano)
			// Format: [<RFC3339Nano>] [stdout|stderr] <text> (LOG-2)
			logLine := fmt.Sprintf("[%s] [%s] %s\n", nowStr, streamName, text)

			mu.Lock()
			if logFile != nil {
				_, _ = logFile.WriteString(logLine)
			}
			tailBuf.WriteLine(text)
			if p.OnLine != nil {
				p.OnLine(streamName, text)
			}
			mu.Unlock()
		}
	}

	wg.Add(2)
	go streamOutput(stdoutPipe, "stdout")
	go streamOutput(stderrPipe, "stderr")

	// Wait for pipe readers to reach EOF
	wg.Wait()

	// Wait for OS process to exit
	waitErr := cmd.Wait()
	close(doneCh)
	duration := time.Since(startTime)

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	timedOut := false
	cancelled := false
	if ctx.Err() != nil {
		cancelled = true
	} else if procCtx.Err() != nil && errors.Is(procCtx.Err(), context.DeadlineExceeded) {
		timedOut = true
	}

	return procResult{
		ExitCode:  exitCode,
		Tail:      tailBuf.String(),
		TimedOut:  timedOut,
		Cancelled: cancelled,
		Duration:  duration,
	}, nil
}
