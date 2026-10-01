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
	WorkDir        string
	Command        string
	Env            map[string]string
	LogPath        string
	OnProcessStart ProcessStartFunc
}

// RunResult represents the execution outcome of a command.
type RunResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Combined string
	Duration time.Duration
}

// Runner executes shell commands inside worktrees with sanitized environments and process group tracking (SEC-6, LOG-2, RCV-1).
type Runner struct{}

// NewRunner creates a new command runner.
func NewRunner() *Runner {
	return &Runner{}
}

// SanitizeEnv returns a minimal, sanitized environment for subprocesses (SEC-6).
// It explicitly excludes credentials, tokens, and parent secrets.
func SanitizeEnv(customEnv map[string]string) []string {
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

		// Never pass secrets or tokens
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

	// Append custom environment variables if not forbidden
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

// Run executes a shell command via `sh -c` inside opts.WorkDir.
func (r *Runner) Run(ctx context.Context, opts RunOptions) (*RunResult, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", opts.Command)
	cmd.Dir = opts.WorkDir
	cmd.Env = SanitizeEnv(opts.Env)

	// Create new process group (SEC-6, RCV-1)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("command: stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("command: stderr pipe: %w", err)
	}

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

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("command: start: %w", err)
	}

	pid := cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		pgid = pid
	}

	// RCV-1: Persist process record before output is read
	if opts.OnProcessStart != nil {
		opts.OnProcessStart(pid, pgid, startTime.Unix())
	}

	var (
		stdoutBuf bytes.Buffer
		stderrBuf bytes.Buffer
		combBuf   bytes.Buffer
		mu        sync.Mutex
		wg        sync.WaitGroup
	)

	streamOutput := func(reader io.Reader, streamName string, buf *bytes.Buffer) {
		defer wg.Done()
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			text := scanner.Text()
			nowStr := time.Now().UTC().Format(time.RFC3339Nano)
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

	wg.Add(2)
	go streamOutput(stdoutPipe, "stdout", &stdoutBuf)
	go streamOutput(stderrPipe, "stderr", &stderrBuf)

	wg.Wait()

	waitErr := cmd.Wait()
	duration := time.Since(startTime)

	exitCode := 0
	if waitErr != nil {
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
