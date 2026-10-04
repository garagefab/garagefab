// Package main contains end-to-end acceptance tests for Garagefab.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ENTERPRISE/JAVA BRIDGE:
// Shared "guided walkthrough" test support (the pause hook).
//
// This file provides a reusable, env-gated pause hook that turns any E2E test
// into an interactive, guided debug tour: the test parks at named checkpoints and
// waits for a human to inspect the live filesystem / SQLite DB / git worktree
// before resuming.
//
// Design contract:
//   - E2E_WALKTHROUGH=1 enables the tour. Otherwise every helper is a no-op, so
//     normal `go test` and CI runs stay fast and deterministic.
//   - E2E_WORKSPACE=<dir> selects a persistent workspace (default
//     $TMPDIR/garagefab-walkthrough). It is reset once at the start and NOT
//     cleaned up, so the operator can inspect artifacts after the run.
//   - Resuming is driven by a sentinel file: `touch <workspace>/continue`.
//
// Enterprise / Spring Boot comparison:
//   - Analogous to an interactive `@SpringBootTest` with sleep-based
//     "breakpoints", or a Postman/Newman collection paused between requests.
//
// Go idiom notes:
//   - A blocking poll loop (time.Sleep) is used instead of a debugger breakpoint
//     so the hook works in any environment without extra tooling. Unlike a
//     debugger, the whole process is NOT frozen: place checkpoints at pipeline
//     idle points (human gates, terminal states) to observe stable snapshots.
//
// ==============================================================================
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// walkthroughEnabled reports whether the interactive step-by-step tour is active.
func walkthroughEnabled() bool { return os.Getenv("E2E_WALKTHROUGH") == "1" }

// walkthroughDir is the persistent workspace root used during the tour.
// Controlled by E2E_WORKSPACE (defaults to $TMPDIR/garagefab-walkthrough).
func walkthroughDir() string {
	if d := os.Getenv("E2E_WORKSPACE"); d != "" {
		return d
	}
	return filepath.Join(os.TempDir(), "garagefab-walkthrough")
}

// walkthroughWorkspace returns a persistent workspace when the tour is active,
// or a t.TempDir() otherwise. In tour mode the directory is deliberately reused
// (reset once) and NOT cleaned up, so the operator can inspect it after the run.
func walkthroughWorkspace(t *testing.T) string {
	t.Helper()
	if !walkthroughEnabled() {
		return t.TempDir()
	}
	dir := walkthroughDir()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("reset walkthrough workspace: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create walkthrough workspace: %v", err)
	}
	return dir
}

// pause blocks the test at a named checkpoint until the operator creates the
// "<workspace>/continue" sentinel file (e.g. `touch <workspace>/continue`).
// It is a no-op unless E2E_WALKTHROUGH=1.
func pause(t *testing.T, step string, lines ...string) {
	t.Helper()
	if !walkthroughEnabled() {
		return
	}
	continueFile := filepath.Join(walkthroughDir(), "continue")

	fmt.Printf("\n============================================================\n")
	fmt.Printf("CHECKPOINT %s\n", step)
	fmt.Printf("------------------------------------------------------------\n")
	for _, l := range lines {
		fmt.Println(l)
	}
	fmt.Printf("------------------------------------------------------------\n")
	fmt.Printf(">>> Inspect. To continue:  touch %s\n", continueFile)
	fmt.Printf("============================================================\n\n")

	for {
		if _, err := os.Stat(continueFile); err == nil {
			_ = os.Remove(continueFile)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
