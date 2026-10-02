// Package factory orchestrates SDLC pipeline stage execution and job state transitions.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Application Service / Pipeline Orchestrator (PIP-1..7, APR-5..7).
//
// `Engine` serves as the Application Service in Clean/Hexagonal Architecture. It:
//  1. Manages state transitions across the 7 SDLC stages (`StageIntent` -> `StageDone`).
//  2. Enforces concurrency rule PIP-5: a job runs at most one step at a time (`sync.Map`).
//  3. Guarantees transactional consistency PIP-2: every stage/status change is accompanied
//     by an immutable audit event in the same DB transaction (`store.InTx`).
//  4. Coordinates Git worktree isolation, checkpoint commits, and automatic cleanup on delivery.
//
// GO CONCEPTS & JAVA / SPRING COMPARISONS:
//
//  1. Concurrent Execution Guardrail (`sync.Map.LoadOrStore`):
//     In Java: You might use `ConcurrentHashMap.putIfAbsent(jobId, true)` or distributed locks.
//     In Go: `sync.Map.LoadOrStore(key, value)` is an atomic compare-and-swap primitive.
//     If `loaded` is true, the job is already executing and the request is rejected with `ErrJobAlreadyExecuting`.
//
//  2. Functional Transaction Boundaries (`store.InTx(ctx, func(tx StoreTx) error)`):
//     In Spring Boot: Methods are annotated with `@Transactional`.
//     In Go: Functional transaction closures (Unit of Work pattern) are preferred.
//     The engine passes a closure to `InTx`. If the closure returns an error, the store
//     adapter executes a rollback; otherwise, it commits automatically.
//
// ==============================================================================
package factory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

var (
	// ErrInvalidState is returned when an action is attempted on an incompatible job stage/status (PIP-3).
	ErrInvalidState = errors.New("factory: invalid job state for action")
	// ErrStaleEvidence is returned when approving with a head SHA differing from stored head (APR-5).
	ErrStaleEvidence = errors.New("factory: stale evidence head sha mismatch")
	// ErrEmptyRejectionNote is returned when rejecting without a note (APR-6).
	ErrEmptyRejectionNote = errors.New("factory: rejection note cannot be empty")
	// ErrJobAlreadyExecuting is returned if a job is already running in this process (PIP-5).
	ErrJobAlreadyExecuting = errors.New("factory: job is already executing")
)

// Engine orchestrates pipeline execution across stages (PIP-1..5).
type Engine struct {
	store       Store           // Persistence port
	wtMgr       WorktreeManager // Worktree lifecycle port
	agentRunner AgentRunner     // AI agent execution port
	cmdRunner   CommandRunner   // Command runner port
	logBaseDir  string          // Base path on disk for step logs (~/.garagefab/logs)

	executingJobs sync.Map // Thread-safe set tracking actively executing job IDs (PIP-5)
	wakeFn        func()   // Callback notifying scheduler when a job finishes or yields
}

// NewEngine creates a new pipeline engine instance with injected port dependencies.
func NewEngine(store Store, wtMgr WorktreeManager, agentRunner AgentRunner, cmdRunner CommandRunner, logBaseDir string) *Engine {
	return &Engine{
		store:       store,
		wtMgr:       wtMgr,
		agentRunner: agentRunner,
		cmdRunner:   cmdRunner,
		logBaseDir:  logBaseDir,
	}
}

// SetWakeFunc registers a callback to notify the scheduler of state changes (e.g. slot freed).
func (e *Engine) SetWakeFunc(fn func()) {
	e.wakeFn = fn
}

func (e *Engine) notifyWake() {
	if e.wakeFn != nil {
		e.wakeFn()
	}
}

// ExecuteJob runs a job through its active pipeline stages (PIP-1..5).
// Enforces PIP-5: a job runs at most one step at a time.
func (e *Engine) ExecuteJob(ctx context.Context, jobID int64) error {
	// Guardrail PIP-5: Ensure this job is not already executing concurrently
	if _, loaded := e.executingJobs.LoadOrStore(jobID, true); loaded {
		return ErrJobAlreadyExecuting
	}
	defer e.executingJobs.Delete(jobID) // Ensure job lock is cleared when function exits

	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("factory: get job %d: %w", jobID, err)
	}

	project, err := e.store.GetProject(ctx, job.ProjectID)
	if err != nil {
		return fmt.Errorf("factory: get project %d: %w", job.ProjectID, err)
	}

	// 1. Ensure Git Worktree exists (WKT-1)
	if job.WorktreePath == "" {
		wtInfo, err := e.wtMgr.Create(ctx, project.RepoPath, project.Name, job.ID, project.BaseRef)
		if err != nil {
			// Persist failure state and audit log on worktree creation error
			_ = e.store.InTx(ctx, func(tx StoreTx) error {
				_ = tx.UpdateJobState(ctx, job.ID, job.Stage, StatusFailed)
				_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"status":%q,"error":%q}`, StatusFailed, err.Error()))
				return nil
			})
			return fmt.Errorf("factory: create worktree: %w", err)
		}

		job.WorktreePath = wtInfo.Path
		job.BranchName = wtInfo.Branch
		job.BaseSHA = wtInfo.BaseSHA
		job.HeadSHA = wtInfo.BaseSHA

		// Persist worktree coordinates inside a transaction
		err = e.store.InTx(ctx, func(tx StoreTx) error {
			return tx.UpdateJobWorktree(ctx, job.ID, wtInfo.Path, wtInfo.Branch, wtInfo.BaseSHA)
		})
		if err != nil {
			return fmt.Errorf("factory: persist worktree info: %w", err)
		}
	}

	// 2. Stage 04_Coding: AI Agent writes code
	if job.Stage == StageIntent || job.Stage == StageCoding {
		// Transition state to 04_Coding / running
		err = e.store.InTx(ctx, func(tx StoreTx) error {
			if err := tx.UpdateJobState(ctx, job.ID, StageCoding, StatusRunning); err != nil {
				return err
			}
			return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageCoding, StatusRunning))
		})
		if err != nil {
			return fmt.Errorf("factory: transition to coding: %w", err)
		}
		job.Stage = StageCoding
		job.Status = StatusRunning

		// Initialize StepRun record
		logPath := filepath.Join(e.logBaseDir, fmt.Sprintf("%d", job.ID), "step_coding.log")
		step := &StepRun{
			JobID:     job.ID,
			Stage:     StageCoding,
			Kind:      StepKindAgent,
			Attempt:   1,
			Executor:  "agent",
			Status:    StepStatusRunning,
			LogPath:   logPath,
			StartedAt: time.Now().UTC(),
		}
		if err := e.store.CreateStepRun(ctx, step); err != nil {
			return fmt.Errorf("factory: create coding step run: %w", err)
		}

		// Execute Coding Agent
		res, err := e.agentRunner.Run(ctx, AgentRequest{
			JobID:        job.ID,
			Stage:        StageCoding,
			WorktreePath: job.WorktreePath,
			Prompt:       job.Intent,
			ProjectName:  project.Name,
			LogPath:      logPath,
			OnProcessStart: func(pid, pgid int, startTime int64) {
				_ = e.store.CreateProcessRecord(ctx, step.ID, pid, pgid, startTime)
			},
		})

		now := time.Now().UTC()
		step.EndedAt = &now

		// Handle Coding failure
		if err != nil || (res != nil && res.ExitCode != 0) {
			step.Status = StepStatusFail
			step.FailureCategory = FailureFlawed
			if res != nil {
				code := res.ExitCode
				step.ExitCode = &code
			}
			_ = e.store.UpdateStepRun(ctx, step)

			_ = e.store.InTx(ctx, func(tx StoreTx) error {
				_ = tx.UpdateJobState(ctx, job.ID, StageCoding, StatusFailed)
				_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageCoding, StatusFailed))
				return nil
			})
			return fmt.Errorf("factory: coding step failed: %w", err)
		}

		// Record successful coding step
		step.Status = StepStatusSuccess
		code := 0
		step.ExitCode = &code
		_ = e.store.UpdateStepRun(ctx, step)

		// Create Checkpoint commit (WKT-5, COD-9)
		headSHA, err := e.wtMgr.Checkpoint(ctx, job.WorktreePath, job.ID, "coding")
		if err != nil {
			return fmt.Errorf("factory: coding checkpoint: %w", err)
		}
		job.HeadSHA = headSHA

		_ = e.store.InTx(ctx, func(tx StoreTx) error {
			return tx.UpdateJobHead(ctx, job.ID, headSHA)
		})
	}

	// 3. Stage 05_Independent_Review: Review agent inspects diff and risk
	err = e.store.InTx(ctx, func(tx StoreTx) error {
		if err := tx.UpdateJobState(ctx, job.ID, StageIndependentReview, StatusRunning); err != nil {
			return err
		}
		return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageIndependentReview, StatusRunning))
	})
	if err != nil {
		return fmt.Errorf("factory: transition to review: %w", err)
	}
	job.Stage = StageIndependentReview
	job.Status = StatusRunning

	// Initialize StepRun for Review
	logPathReview := filepath.Join(e.logBaseDir, fmt.Sprintf("%d", job.ID), "step_review.log")
	stepReview := &StepRun{
		JobID:     job.ID,
		Stage:     StageIndependentReview,
		Kind:      StepKindAgent,
		Attempt:   1,
		Executor:  "agent",
		Status:    StepStatusRunning,
		LogPath:   logPathReview,
		StartedAt: time.Now().UTC(),
	}
	if err := e.store.CreateStepRun(ctx, stepReview); err != nil {
		return fmt.Errorf("factory: create review step run: %w", err)
	}

	// Execute Review Agent
	resReview, err := e.agentRunner.Run(ctx, AgentRequest{
		JobID:        job.ID,
		Stage:        StageIndependentReview,
		WorktreePath: job.WorktreePath,
		ProjectName:  project.Name,
		LogPath:      logPathReview,
		OnProcessStart: func(pid, pgid int, startTime int64) {
			_ = e.store.CreateProcessRecord(ctx, stepReview.ID, pid, pgid, startTime)
		},
	})

	nowReview := time.Now().UTC()
	stepReview.EndedAt = &nowReview

	if err != nil || (resReview != nil && resReview.ExitCode != 0) {
		stepReview.Status = StepStatusFail
		stepReview.FailureCategory = FailureFlawed
		_ = e.store.UpdateStepRun(ctx, stepReview)

		_ = e.store.InTx(ctx, func(tx StoreTx) error {
			_ = tx.UpdateJobState(ctx, job.ID, StageIndependentReview, StatusFailed)
			_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageIndependentReview, StatusFailed))
			return nil
		})
		return fmt.Errorf("factory: review step failed: %w", err)
	}

	stepReview.Status = StepStatusSuccess
	codeReview := 0
	stepReview.ExitCode = &codeReview
	_ = e.store.UpdateStepRun(ctx, stepReview)

	// 4. Transition to Human Approval Gate (Stage 06_Human_Approval_Gate / awaiting_approval) (SCH-4)
	err = e.store.InTx(ctx, func(tx StoreTx) error {
		if err := tx.UpdateJobState(ctx, job.ID, StageHumanApprovalGate, StatusAwaitingApproval); err != nil {
			return err
		}
		return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageHumanApprovalGate, StatusAwaitingApproval))
	})
	if err != nil {
		return fmt.Errorf("factory: transition to approval gate: %w", err)
	}

	// Notify scheduler that this job is now waiting at a gate, freeing a concurrency slot
	e.notifyWake()
	return nil
}

// Approve records human approval and completes the refactor job (APR-5, APR-7, DLV-4).
// It verifies that the reviewed head SHA matches the database to prevent stale approvals.
func (e *Engine) Approve(ctx context.Context, jobID int64, headSHA string) error {
	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("factory: get job %d: %w", jobID, err)
	}

	if job.Stage != StageHumanApprovalGate || job.Status != StatusAwaitingApproval {
		return fmt.Errorf("%w: job %d is in %s/%s", ErrInvalidState, jobID, job.Stage, job.Status)
	}

	// Stale Evidence Check (APR-5): Reject approval if user approved outdated commit SHA
	if headSHA != "" && job.HeadSHA != "" && headSHA != job.HeadSHA {
		return fmt.Errorf("%w: provided %s, current %s", ErrStaleEvidence, headSHA, job.HeadSHA)
	}

	project, err := e.store.GetProject(ctx, job.ProjectID)
	if err != nil {
		return fmt.Errorf("factory: get project %d: %w", job.ProjectID, err)
	}

	// Persist approval and transition to StageDone/done (PIP-2)
	err = e.store.InTx(ctx, func(tx StoreTx) error {
		approval := &Approval{
			JobID:    job.ID,
			Gate:     ApprovalGateFinal,
			Decision: ApprovalDecisionApprove,
			HeadSHA:  job.HeadSHA,
		}
		if err := tx.RecordApproval(ctx, approval); err != nil {
			return err
		}
		if err := tx.UpdateJobState(ctx, job.ID, StageDone, StatusDone); err != nil {
			return err
		}
		return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageDone, StatusDone))
	})
	if err != nil {
		return fmt.Errorf("factory: record approval: %w", err)
	}

	// Clean up worktree on delivery completion (DLV-4, WKT-6)
	if job.WorktreePath != "" {
		_ = e.wtMgr.Remove(ctx, project.RepoPath, job.WorktreePath, job.BranchName, false)
	}

	e.notifyWake()
	return nil
}

// Reject records a human rejection note and moves the job back to 04_Coding/queued (APR-6).
func (e *Engine) Reject(ctx context.Context, jobID int64, note string) error {
	// Rejection note is mandatory (APR-6)
	if note == "" {
		return ErrEmptyRejectionNote
	}

	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("factory: get job %d: %w", jobID, err)
	}

	if job.Stage != StageHumanApprovalGate || job.Status != StatusAwaitingApproval {
		return fmt.Errorf("%w: job %d is in %s/%s", ErrInvalidState, jobID, job.Stage, job.Status)
	}

	// Atomically record rejection and requeue the job back to Coding stage
	err = e.store.InTx(ctx, func(tx StoreTx) error {
		approval := &Approval{
			JobID:    job.ID,
			Gate:     ApprovalGateFinal,
			Decision: ApprovalDecisionReject,
			Note:     note,
			HeadSHA:  job.HeadSHA,
		}
		if err := tx.RecordApproval(ctx, approval); err != nil {
			return err
		}
		if err := tx.UpdateJobState(ctx, job.ID, StageCoding, StatusQueued); err != nil {
			return err
		}
		return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageCoding, StatusQueued))
	})
	if err != nil {
		return fmt.Errorf("factory: record rejection: %w", err)
	}

	e.notifyWake()
	return nil
}

// Cancel terminates a job in any non-terminal state and cleans up worktrees (PIP-6).
func (e *Engine) Cancel(ctx context.Context, jobID int64) error {
	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("factory: get job %d: %w", jobID, err)
	}

	if job.Status == StatusDone || job.Status == StatusCancelled {
		return fmt.Errorf("%w: cannot cancel terminal job in status %s", ErrInvalidState, job.Status)
	}

	project, err := e.store.GetProject(ctx, job.ProjectID)
	if err != nil {
		return fmt.Errorf("factory: get project %d: %w", job.ProjectID, err)
	}

	// Update job state to cancelled
	err = e.store.InTx(ctx, func(tx StoreTx) error {
		if err := tx.UpdateJobState(ctx, job.ID, job.Stage, StatusCancelled); err != nil {
			return err
		}
		return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, job.Stage, StatusCancelled))
	})
	if err != nil {
		return fmt.Errorf("factory: cancel job: %w", err)
	}

	// Remove worktree and delete branch (PIP-6)
	if job.WorktreePath != "" {
		_ = e.wtMgr.Remove(ctx, project.RepoPath, job.WorktreePath, job.BranchName, true)
	}

	e.notifyWake()
	return nil
}

// Retry resets a failed or interrupted job to queued to re-run from checkpoint (PIP-7, WKT-5).
func (e *Engine) Retry(ctx context.Context, jobID int64) error {
	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("factory: get job %d: %w", jobID, err)
	}

	if job.Status != StatusFailed && job.Status != StatusInterrupted {
		return fmt.Errorf("%w: retry only allowed on failed or interrupted jobs, got %s", ErrInvalidState, job.Status)
	}

	// Roll back worktree to the last successful checkpoint commit (WKT-5)
	if job.WorktreePath != "" {
		targetSHA := job.HeadSHA
		if targetSHA == "" {
			targetSHA = job.BaseSHA
		}
		_ = e.wtMgr.Reset(ctx, job.WorktreePath, targetSHA)
	}

	// Re-queue the job in its current stage
	err = e.store.InTx(ctx, func(tx StoreTx) error {
		if err := tx.UpdateJobState(ctx, job.ID, job.Stage, StatusQueued); err != nil {
			return err
		}
		return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, job.Stage, StatusQueued))
	})
	if err != nil {
		return fmt.Errorf("factory: retry job: %w", err)
	}

	e.notifyWake()
	return nil
}
