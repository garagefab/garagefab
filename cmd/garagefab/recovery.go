// Package main (cmd/garagefab) is the application entry point and composition root.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Crash Recovery & Orphan Process Reclamation (RCV-1..4, WKT-6).
//
//  1. The Problem:
//     If the Garagefab server process is killed abruptly (`kill -9`, kernel panic, power loss),
//     child subprocesses (e.g. AI agent CLIs, compilers, test runners) can remain running
//     in the background as "orphans", consuming CPU, locking `.git` repositories, or
//     writing uncontrolled changes.
//
//  2. The Solution (RCV-1..3):
//     - Before reading command output, subprocess PGID, PID, and start time are saved to DB (RCV-1).
//     - On every startup, BEFORE the scheduler or web server starts, RecoverOrphanProcesses
//     scans for active process records from previous runs (RCV-2).
//     - If the process exists, it terminates the entire process group (`-pgid`) with SIGTERM/SIGKILL.
//     - The interrupted job is transitioned to `interrupted`, and its worktree is preserved
//     so the human developer can later Retry or Cancel (RCV-3, WKT-6).
//
//  3. Enterprise / Java Comparison:
//     Similar to distributed workflow orchestrators (e.g. Temporal, Camunda, Zeebe, Spring Batch)
//     recovering stranded tasks on cluster restart by querying pending lease records in the database.
//
// ==============================================================================
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"syscall"
	"time"

	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/store"
)

// IsProcessAlive checks whether an operating system process with the given PID is running.
// It uses `syscall.Kill(pid, 0)`: in POSIX, signal 0 sends no signal, but performs error checking.
// If error is nil, the process exists. If ESRCH, the process does not exist.
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.ESRCH) {
		return false
	}
	// EPERM means the process exists but is owned by another user (e.g. root)
	return errors.Is(err, syscall.EPERM)
}

// RecoverOrphanProcesses scans for active processes left behind by a crash or hard kill (RCV-2, RCV-3).
// If a process is still running, it terminates the process group, marks affected step runs
// as failed (Blocked, interrupted), marks jobs as interrupted, and deactivates the records.
func RecoverOrphanProcesses(ctx context.Context, db *store.DB) error {
	activeRecords, err := db.ProcessRecords().ListActiveProcessRecords(ctx)
	if err != nil {
		return fmt.Errorf("recovery: list active processes: %w", err)
	}

	if len(activeRecords) == 0 {
		return nil
	}

	slog.Info("crash recovery: checking orphan process records from previous runs", "count", len(activeRecords))

	for _, rec := range activeRecords {
		alive := IsProcessAlive(rec.PID)

		if alive && rec.PGID > 0 {
			slog.Warn("crash recovery: killing orphaned process group", "pid", rec.PID, "pgid", rec.PGID)
			// Send SIGTERM to negative PGID to signal the entire process group
			_ = syscall.Kill(-rec.PGID, syscall.SIGTERM)
			time.Sleep(100 * time.Millisecond)
			// Escalate to SIGKILL if still alive
			if IsProcessAlive(rec.PID) {
				_ = syscall.Kill(-rec.PGID, syscall.SIGKILL)
			}
		}

		// Deactivate process record
		_ = db.ProcessRecords().MarkProcessInactive(ctx, rec.ID)

		// Mark corresponding StepRun and Job as interrupted (RCV-3)
		if rec.StepRunID > 0 {
			step, sErr := db.StepRuns().GetStepRun(ctx, rec.StepRunID)
			if sErr == nil && step != nil {
				now := time.Now().UTC()
				step.EndedAt = &now
				step.Status = store.StepStatusFail
				step.FailureCategory = factory.FailureBlocked
				_ = db.StepRuns().UpdateStepRun(ctx, step)

				// Atomically transition job to status 'interrupted' (RCV-3)
				_ = db.WithTx(ctx, func(tx *store.Tx) error {
					if err := tx.Jobs().UpdateJobState(ctx, step.JobID, step.Stage, store.StatusInterrupted); err != nil {
						return err
					}
					return tx.Events().CreateEvent(ctx, &store.Event{
						JobID:   step.JobID,
						Type:    "job.status_changed",
						Payload: fmt.Sprintf(`{"stage":%q,"status":%q,"reason":"interrupted"}`, step.Stage, store.StatusInterrupted),
					})
				})
			}
		}
	}

	return nil
}
