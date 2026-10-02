// Package command provides secure, monitored subprocess execution inside worktrees.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Infrastructure Process Execution & Security Sandbox Guardrails (SEC-6, RCV-1, LOG-2).
//
// When the factory runs verification commands (e.g. `npm test`, `go test`, `pytest`)
// or AI agent commands, it uses this runner to:
// 1. Sanitize OS environment variables to prevent leaking tokens or secrets (SEC-6).
// 2. Isolate subprocesses in their own Process Group (PGID) to prevent orphaned processes (RCV-1).
// 3. Stream stdout and stderr concurrently into line-prefixed log files and memory buffers (LOG-2).
//
// GO CONCEPTS & JAVA / PROCESSBUILDER COMPARISONS:
//
//  1. Process Execution (`os/exec.CommandContext` vs Java `ProcessBuilder`):
//     In Java, `ProcessBuilder.start()` creates a `Process`. Managing timeouts requires
//     `process.waitFor(timeout, unit)` and manual destruction via `process.destroyForcibly()`.
//     In Go, `exec.CommandContext(ctx, ...)` integrates with Go's `context.Context`. If the
//     context expires or is cancelled, Go automatically sends a termination signal (SIGKILL)
//     to the process.
//
//  2. Process Groups (`Setpgid: true`):
//     In Unix, if a process spawns child subprocesses, standard `kill(pid)` only kills the parent,
//     leaving orphan child processes running. By setting `Setpgid: true`, the OS places the
//     process and all its children into a new Process Group. Killing `-pgid` kills the entire tree.
//
//  3. Concurrency (`sync.WaitGroup`, `sync.Mutex`, Goroutines):
//     To capture stdout and stderr concurrently without deadlocking OS pipe buffers:
//     - Two goroutines read stdoutPipe and stderrPipe simultaneously.
//     - `sync.WaitGroup` coordinates waiting until both pipe readers hit EOF.
//     - `sync.Mutex` protects the shared log file and combined buffer from race conditions.
//
// ==============================================================================
package command

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ProcessStartFunc is called right after process startup to persist process records (RCV-1).
type ProcessStartFunc func(pid, pgid int, startTime int64)

// RunOptions configures the execution of a command step.
type RunOptions struct {
	WorkDir        string            // Working directory for command execution (the job worktree)
	Command        string            // Shell command string to execute (passed to `sh -c`)
	Env            map[string]string // Additional environment variables (will be sanitized)
	LogPath        string            // Optional log file path for real-time output logging
	OnProcessStart ProcessStartFunc  // Callback invoked immediately after OS process fork
}

// RunResult represents the execution outcome of a command.
type RunResult struct {
	ExitCode int           // Process exit status (0 = success)
	Stdout   string        // Captured standard output
	Stderr   string        // Captured standard error
	Combined string        // Interleaved chronological stdout and stderr
	Duration time.Duration // Wall-clock execution time
}

// Runner executes shell commands inside worktrees with sanitized environments and process group tracking (SEC-6, LOG-2, RCV-1).
type Runner struct{}

// NewRunner creates a new command runner instance.
func NewRunner() *Runner {
	return &Runner{}
}

// SanitizeEnv returns a minimal, sanitized environment for subprocesses (SEC-6).
// It strips sensitive environment variables containing tokens, passwords, or secrets.
func SanitizeEnv(customEnv map[string]string) []string {
	// Whitelist of benign OS environment variables allowed through to subprocesses
	allowedKeys := map[string]bool{
		"PATH":    true,
		"HOME":    true,
		"USER":    true,
		"LOGNAME": true,
		"TMPDIR":  true,
		"SHELL":   true,
		"LANG":    true,
		"LC_ALL":  true,
		"TERM":    true,
	}

	var env []string
	for _, entry := range os.Environ() {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 0 {
			continue
		}
		key := parts[0]
		upperKey := strings.ToUpper(key)

		// Never leak secrets or tokens to untrusted subprocesses
		if strings.Contains(upperKey, "TOKEN") ||
			strings.Contains(upperKey, "SECRET") ||
			strings.Contains(upperKey, "KEY") ||
			strings.Contains(upperKey, "PASSWORD") ||
			strings.Contains(upperKey, "GARAGEFAB") ||
			strings.Contains(upperKey, "GITHUB") ||
			strings.Contains(upperKey, "AUTH") {
			continue
		}

		if allowedKeys[key] {
			env = append(env, entry)
		}
	}

	// Append custom environment variables if they don't violate secret blacklists
	for k, v := range customEnv {
		upperKey := strings.ToUpper(k)
		if strings.Contains(upperKey, "TOKEN") ||
			strings.Contains(upperKey, "SECRET") ||
			strings.Contains(upperKey, "KEY") ||
			strings.Contains(upperKey, "PASSWORD") {
			continue
		}
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	return env
}

// Run executes a shell command via `sh -c` inside opts.WorkDir with full output capture.
func (r *Runner) Run(ctx context.Context, opts RunOptions) (*RunResult, error) {
	// Construct command wrapped in POSIX shell
	cmd := exec.CommandContext(ctx, "sh", "-c", opts.Command)
	cmd.Dir = opts.WorkDir
	cmd.Env = SanitizeEnv(opts.Env)

	// Create new process group: child becomes leader of its own PGID (SEC-6, RCV-1)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Set up pipes for stdout and stderr
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("command: stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("command: stderr pipe: %w", err)
	}

	// Open destination log file if requested
	var logFile *os.File
	if opts.LogPath != "" {
		if err := os.MkdirAll(filepath.Dir(opts.LogPath), 0700); err != nil {
			return nil, fmt.Errorf("command: create log dir: %w", err)
		}
		f, err := os.OpenFile(opts.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return nil, fmt.Errorf("command: open log file: %w", err)
		}
		defer f.Close()
		logFile = f
	}

	startTime := time.Now()

	// Launch subprocess asynchronously
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("command: start: %w", err)
	}

	pid := cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		pgid = pid
	}

	// Notify caller that process has started (RCV-1 crash recovery persistence)
	if opts.OnProcessStart != nil {
		opts.OnProcessStart(pid, pgid, startTime.Unix())
	}

	// Buffers and synchronization primitives for concurrent stream capture
	var (
		stdoutBuf bytes.Buffer
		stderrBuf bytes.Buffer
		combBuf   bytes.Buffer
		mu        sync.Mutex     // Mutex protects logFile and combBuf from concurrent writes
		wg        sync.WaitGroup // WaitGroup ensures both readers finish before cmd.Wait()
	)

	// streamOutput reads lines from a pipe, timestamps them, and appends to buffers and disk
	streamOutput := func(reader io.Reader, streamName string, buf *bytes.Buffer) {
		defer wg.Done()
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			text := scanner.Text()
			nowStr := time.Now().UTC().Format(time.RFC3339Nano)
			// Format: [2026-10-02T12:00:00Z] [stdout] Line contents (LOG-2)
			logLine := fmt.Sprintf("[%s] [%s] %s\n", nowStr, streamName, text)

			mu.Lock()
			buf.WriteString(text + "\n")
			combBuf.WriteString(text + "\n")
			if logFile != nil {
				_, _ = logFile.WriteString(logLine)
			}
			mu.Unlock()
		}
	}

	// Spawn two background goroutines to drain stdout and stderr pipes concurrently
	wg.Add(2)
	go streamOutput(stdoutPipe, "stdout", &stdoutBuf)
	go streamOutput(stderrPipe, "stderr", &stderrBuf)

	// Wait for pipe readers to reach EOF
	wg.Wait()

	// Wait for OS process to exit
	waitErr := cmd.Wait()
	duration := time.Since(startTime)

	// Extract exit code
	exitCode := 0
	if waitErr != nil {
		// In Go, non-zero exits are returned as *exec.ExitError
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	return &RunResult{
		ExitCode: exitCode,
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		Combined: combBuf.String(),
		Duration: duration,
	}, nil
}
