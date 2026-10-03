// Package agent provides AI coding agent execution and lifecycle management.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Unit Verification for Low-Level Subprocess Orchestration (SEC-6, LOG-2, RCV-1, COD-10).
//
// These tests verify that:
// 1. Process group isolation (Setpgid) cleanly kills spawned children on timeout/cancellation (COD-10).
// 2. Real-time logging handles high volume (10k lines) with bounded memory (LOG-2).
// 3. Process startup callbacks execute strictly before reading output (RCV-1).
// 4. Large prompts (>64 KiB) spill safely to disk with 0600 permissions (F9).
// 5. Missing agent binaries return typed ErrAgentUnavailable errors.
// ==============================================================================
package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestRunProc_OnStart_BeforeOutput_RCV1 tests requirement RCV-1:
// OnStart must be invoked strictly before any stdout or stderr lines are read or processed.
func TestRunProc_OnStart_BeforeOutput_RCV1(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	var (
		mu     sync.Mutex
		events []string
		gotPID int
	)

	spec := procSpec{
		Binary:  "sh",
		Args:    []string{"-c", "sleep 0.05; echo 'first output line'"},
		Dir:     tmpDir,
		LogPath: logPath,
		OnStart: func(pid, pgid int, startTime int64) {
			mu.Lock()
			events = append(events, "on_start")
			gotPID = pid
			mu.Unlock()
		},
		OnLine: func(stream, line string) {
			mu.Lock()
			events = append(events, "on_line:"+line)
			mu.Unlock()
		},
	}

	res, err := runProc(context.Background(), spec)
	if err != nil {
		t.Fatalf("runProc failed: %v", err)
	}

	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if gotPID <= 0 {
		t.Errorf("expected positive PID, got %d", gotPID)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %v", events)
	}
	if events[0] != "on_start" {
		t.Errorf("RCV-1 violated: first event must be on_start, got %s", events[0])
	}
	if !strings.HasPrefix(events[1], "on_line:first output line") {
		t.Errorf("expected second event to be output line, got %s", events[1])
	}
}

// TestRunProc_Timeout_KillsProcessGroup_COD10 tests requirement COD-10:
// On timeout expiry, the entire process group (including child processes) is terminated.
func TestRunProc_Timeout_KillsProcessGroup_COD10(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "timeout.log")

	var (
		capturedPGID int
		mu           sync.Mutex
	)

	// Subprocess spawns a background grandchild sleep and waits
	script := "(sleep 60) & wait"

	spec := procSpec{
		Binary:      "sh",
		Args:        []string{"-c", script},
		Dir:         tmpDir,
		LogPath:     logPath,
		Timeout:     300 * time.Millisecond,
		GracePeriod: 200 * time.Millisecond,
		OnStart: func(pid, pgid int, startTime int64) {
			mu.Lock()
			capturedPGID = pgid
			mu.Unlock()
		},
	}

	res, err := runProc(context.Background(), spec)
	if err != nil {
		t.Fatalf("runProc failed: %v", err)
	}

	if !res.TimedOut {
		t.Errorf("COD-10 violated: expected TimedOut=true, got false")
	}
	if res.Cancelled {
		t.Errorf("expected Cancelled=false for timeout, got true")
	}
	if res.ExitCode == 0 {
		t.Errorf("expected non-zero exit code on timeout termination, got 0")
	}

	// Verify the process group is completely dead (no orphans surviving)
	mu.Lock()
	pgid := capturedPGID
	mu.Unlock()

	if pgid <= 0 {
		t.Fatalf("expected positive PGID captured, got %d", pgid)
	}

	// Wait up to 1 second for OS kernel process table reaping
	time.Sleep(100 * time.Millisecond)
	err = syscall.Kill(-pgid, 0)
	if err == nil {
		t.Errorf("COD-10 violated: process group %d is still alive after timeout termination", pgid)
	} else if !errors.Is(err, syscall.ESRCH) {
		t.Logf("syscall.Kill returned expected error: %v", err)
	}
}

// TestRunProc_ParentContextCancel_RCV1 tests that cancelling the parent context
// prompts immediate process group cancellation with Cancelled=true.
func TestRunProc_ParentContextCancel_RCV1(t *testing.T) {
	tmpDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	var capturedPGID int
	spec := procSpec{
		Binary:      "sh",
		Args:        []string{"-c", "sleep 60"},
		Dir:         tmpDir,
		Timeout:     30 * time.Second,
		GracePeriod: 200 * time.Millisecond,
		OnStart: func(pid, pgid int, startTime int64) {
			capturedPGID = pgid
		},
	}

	doneCh := make(chan procResult, 1)
	go func() {
		res, _ := runProc(ctx, spec)
		doneCh <- res
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case res := <-doneCh:
		if !res.Cancelled {
			t.Errorf("expected Cancelled=true, got false")
		}
		if res.TimedOut {
			t.Errorf("expected TimedOut=false, got true")
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("process group did not terminate within deadline after context cancellation")
	}

	time.Sleep(100 * time.Millisecond)
	if capturedPGID > 0 {
		err := syscall.Kill(-capturedPGID, 0)
		if err == nil {
			t.Errorf("process group %d still alive after cancellation", capturedPGID)
		}
	}
}

// TestRunProc_OutputStreaming_And_BoundedBuffer_LOG2 tests requirement LOG-2 & NFR:
// 10,000 output lines are streamed in order to the log file and OnLine, while the
// in-memory tail buffer stays strictly bounded to <= 64 KiB.
func TestRunProc_OutputStreaming_And_BoundedBuffer_LOG2(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "streaming.log")

	lineCount := 0
	var mu sync.Mutex

	spec := procSpec{
		Binary:  "awk",
		Args:    []string{"BEGIN { for (i=1; i<=10000; i++) print i }"},
		Dir:     tmpDir,
		LogPath: logPath,
		OnLine: func(stream, line string) {
			mu.Lock()
			lineCount++
			mu.Unlock()
		},
	}

	res, err := runProc(context.Background(), spec)
	if err != nil {
		t.Fatalf("runProc failed: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}

	mu.Lock()
	count := lineCount
	mu.Unlock()
	if count != 10000 {
		t.Errorf("expected 10000 lines processed, got %d", count)
	}

	// Verify in-memory tail buffer is bounded to <= 64 KiB
	if len(res.Tail) > maxTailBufferBytes {
		t.Errorf("LOG-2 memory bound violated: tail buffer size %d exceeds %d bytes", len(res.Tail), maxTailBufferBytes)
	}
	if !strings.HasSuffix(res.Tail, "10000\n") {
		t.Errorf("expected tail buffer to end with 10000, got: %s", res.Tail[len(res.Tail)-50:])
	}

	// Verify log file on disk has full output with timestamps
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file failed: %v", err)
	}
	logStr := string(logBytes)
	if !strings.Contains(logStr, "[stdout] 1\n") {
		t.Errorf("log missing first line: %s", logStr[:200])
	}
	if !strings.Contains(logStr, "[stdout] 10000\n") {
		t.Errorf("log missing last line")
	}
}

// TestDeliverPrompt_Spill_F9 tests requirement F9:
//   - Prompts <= 64 KiB are delivered verbatim with no disk file created.
//   - Prompts > 64 KiB spill to a prompt_<timestamp>.md file (0600) next to logPath,
//     returning an instruction of length < 1 KiB.
func TestDeliverPrompt_Spill_F9(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "logs", "job1", "step.log")

	// 1. Small prompt: verbatim
	smallPrompt := "Write a quick unit test for math addition."
	arg, cleanup, err := deliverPrompt(smallPrompt, logPath)
	if err != nil {
		t.Fatalf("deliverPrompt failed: %v", err)
	}
	if arg != smallPrompt {
		t.Errorf("expected small prompt verbatim, got %q", arg)
	}
	if cleanup != nil {
		cleanup()
	}

	// 2. Large prompt (200 KiB): spills to file
	largePrompt := strings.Repeat("X", 200*1024)
	arg, cleanup, err = deliverPrompt(largePrompt, logPath)
	if err != nil {
		t.Fatalf("deliverPrompt failed for 200 KiB prompt: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	if len(arg) >= 1024 {
		t.Errorf("expected instruction argument < 1 KiB, got %d bytes", len(arg))
	}
	prefix := "Read the full task instructions from "
	suffix := " and follow them exactly."
	if !strings.HasPrefix(arg, prefix) || !strings.HasSuffix(arg, suffix) {
		t.Fatalf("unexpected instruction format: %s", arg)
	}

	// Extract spilled file path
	spilledPath := strings.TrimSuffix(strings.TrimPrefix(arg, prefix), suffix)
	fi, err := os.Stat(spilledPath)
	if err != nil {
		t.Fatalf("spilled prompt file does not exist at %s: %v", spilledPath, err)
	}

	// File mode must be 0600
	if fi.Mode().Perm() != 0600 {
		t.Errorf("expected spilled prompt permissions 0600, got %o", fi.Mode().Perm())
	}

	// Verify content matches exactly
	content, err := os.ReadFile(spilledPath)
	if err != nil {
		t.Fatalf("failed to read spilled prompt: %v", err)
	}
	if string(content) != largePrompt {
		t.Errorf("spilled prompt content did not match original prompt")
	}
}

// TestRunProc_MissingBinary_ErrAgentUnavailable tests that nonexistent binaries
// return a typed ErrAgentUnavailable so the factory can mark the step as Blocked.
func TestRunProc_MissingBinary_ErrAgentUnavailable(t *testing.T) {
	spec := procSpec{
		Binary: "non_existent_binary_xyz_98765",
		Args:   []string{"--version"},
		Dir:    t.TempDir(),
	}

	_, err := runProc(context.Background(), spec)
	if err == nil {
		t.Fatalf("expected error for missing binary, got nil")
	}

	if !errors.Is(err, ErrAgentUnavailable) {
		t.Errorf("expected ErrAgentUnavailable, got: %v", err)
	}
}

// TestRunProc_StdinDevNull_SpikeQ11 tests that stdin is /dev/null
// so agents attempting to read interactive input immediately receive EOF.
func TestRunProc_StdinDevNull_SpikeQ11(t *testing.T) {
	tmpDir := t.TempDir()
	spec := procSpec{
		Binary: "sh",
		Args:   []string{"-c", `read line; if [ -z "$line" ]; then echo "EOF_CONFIRMED"; else echo "INPUT"; fi`},
		Dir:    tmpDir,
	}

	res, err := runProc(context.Background(), spec)
	if err != nil {
		t.Fatalf("runProc failed: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Tail, "EOF_CONFIRMED") {
		t.Errorf("expected EOF_CONFIRMED on stdin read, got tail: %s", res.Tail)
	}
}
