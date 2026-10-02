// Package main contains recovery and CLI integration tests.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Crash Recovery Integration Tests (RCV-1, RCV-2, RCV-3).
//
// Tests that orphaned background processes from simulated crashes are detected,
// killed via process group signaling, and marked interrupted in SQLite.
// ==============================================================================
package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/store"
)

// TestCrashRecovery_OrphanProcessTerminated_RCV2_RCV3 tests that an active process record
// surviving from a crash is killed on startup and its job is marked interrupted.
func TestCrashRecovery_OrphanProcessTerminated_RCV2_RCV3(t *testing.T) {
	ctx := context.Background()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test_recovery.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// Create project and job
	proj := &store.Project{Name: "recovery-proj", RepoPath: "/repo", BaseRef: "main"}
	if err := db.Projects().CreateProject(ctx, proj); err != nil {
		t.Fatal(err)
	}

	job := &store.Job{
		ProjectID: proj.ID,
		WorkType:  store.WorkTypeRefactor,
		Title:     "Crash test job",
		Stage:     factory.StageCoding,
		Status:    factory.StatusRunning,
	}
	if err := db.Jobs().CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}

	step := &store.StepRun{
		JobID:     job.ID,
		Stage:     factory.StageCoding,
		Kind:      factory.StepKindAgent,
		Attempt:   1,
		Executor:  "agent",
		Status:    store.StepStatusRunning,
		StartedAt: time.Now().UTC(),
	}
	if err := db.StepRuns().CreateStepRun(ctx, step); err != nil {
		t.Fatal(err)
	}

	// Spawn a real long-running child process in its own Process Group
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // Place in new process group
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start test process: %v", err)
	}
	pid := cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("failed to get pgid: %v", err)
	}
	defer func() {
		// Cleanup safety guard
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}()

	// Verify process is initially running
	if !IsProcessAlive(pid) {
		t.Fatalf("expected test process pid %d to be alive", pid)
	}

	// Record the process in the DB as active (RCV-1)
	procRec := &store.ProcessRecord{
		StepRunID: step.ID,
		PID:       pid,
		PGID:      pgid,
		StartTime: time.Now().Unix(),
		Active:    true,
	}
	if err := db.ProcessRecords().CreateProcessRecord(ctx, procRec); err != nil {
		t.Fatal(err)
	}

	// Now run Crash Recovery (RCV-2, RCV-3)
	if err := RecoverOrphanProcesses(ctx, db); err != nil {
		t.Fatalf("RecoverOrphanProcesses failed: %v", err)
	}

	// Wait for process to exit and confirm it was terminated by signal
	waitErr := cmd.Wait()
	if waitErr == nil {
		t.Fatalf("expected sleep process to be terminated by signal, but exited cleanly with code 0")
	}
	if IsProcessAlive(pid) {
		t.Fatalf("expected test process pid %d to be dead after wait, but still alive", pid)
	}

	// Verify Job is marked interrupted (RCV-3)
	recoveredJob, err := db.Jobs().GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredJob.Status != factory.StatusInterrupted {
		t.Fatalf("expected job status %s, got %s", factory.StatusInterrupted, recoveredJob.Status)
	}

	// Verify StepRun is marked fail (Blocked)
	recoveredStep, err := db.StepRuns().GetStepRun(ctx, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredStep.Status != store.StepStatusFail {
		t.Fatalf("expected step status %s, got %s", store.StepStatusFail, recoveredStep.Status)
	}
	if recoveredStep.FailureCategory != factory.FailureBlocked {
		t.Fatalf("expected failure category %s, got %s", factory.FailureBlocked, recoveredStep.FailureCategory)
	}

	// Verify process record is marked inactive
	recAfter, err := db.ProcessRecords().GetProcessRecord(ctx, procRec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recAfter.Active {
		t.Fatalf("expected process record to be marked inactive (active=false)")
	}
}
