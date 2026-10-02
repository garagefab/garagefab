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
	"strings"
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
	store             Store                 // Persistence port
	wtMgr             WorktreeManager       // Worktree lifecycle port
	agentRunner       AgentRunner           // AI agent execution port
	cmdRunner         CommandRunner         // Command runner port
	guardrailRunner   GuardrailRunner       // Guardrail runner port (GRD-1..4)
	projCfgProvider   ProjectConfigProvider // Project configuration loader (architecture.md §14)
	maxRepairAttempts int                   // Maximum automated repair loop attempts (default 3, COD-4)
	logBaseDir        string                // Base path on disk for step logs (~/.garagefab/logs)

	executingJobs    sync.Map // Thread-safe set tracking actively executing job IDs (PIP-5)
	activeJobCancels sync.Map // Map[int64]context.CancelFunc for terminating active jobs (PIP-6)
	wakeFn           func()   // Callback notifying scheduler when a job finishes or yields
}

// NewEngine creates a new pipeline engine instance with injected port dependencies.
func NewEngine(store Store, wtMgr WorktreeManager, agentRunner AgentRunner, cmdRunner CommandRunner, logBaseDir string) *Engine {
	return &Engine{
		store:             store,
		wtMgr:             wtMgr,
		agentRunner:       agentRunner,
		cmdRunner:         cmdRunner,
		maxRepairAttempts: 3,
		logBaseDir:        logBaseDir,
	}
}

// SetGuardrailRunner registers the guardrail verification runner (GRD-1..4).
func (e *Engine) SetGuardrailRunner(g GuardrailRunner) {
	e.guardrailRunner = g
}

// SetProjectConfigProvider registers the provider for loading repo project.yaml configs.
func (e *Engine) SetProjectConfigProvider(p ProjectConfigProvider) {
	e.projCfgProvider = p
}

// SetMaxRepairAttempts sets the maximum repair loop iterations (default 3, COD-4).
func (e *Engine) SetMaxRepairAttempts(n int) {
	if n > 0 {
		e.maxRepairAttempts = n
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

	jobCtx, jobCancel := context.WithCancel(ctx)
	defer jobCancel()
	e.activeJobCancels.Store(jobID, jobCancel)
	defer e.activeJobCancels.Delete(jobID)

	job, err := e.store.GetJob(jobCtx, jobID)
	if err != nil {
		return fmt.Errorf("factory: get job %d: %w", jobID, err)
	}

	project, err := e.store.GetProject(jobCtx, job.ProjectID)
	if err != nil {
		return fmt.Errorf("factory: get project %d: %w", job.ProjectID, err)
	}

	// Load per-project configuration if available (architecture.md §14)
	var projCfg *ProjectConfig
	if e.projCfgProvider != nil {
		cfg, pErr := e.projCfgProvider.GetProjectConfig(jobCtx, project.RepoPath)
		if pErr == nil && cfg != nil {
			projCfg = cfg
		}
	}
	if projCfg == nil {
		projCfg = &ProjectConfig{
			BaseRef: project.BaseRef,
		}
		projCfg.Guardrails.ProtectedPaths = []string{"**/*_test.go"}
	}

	// 1. Ensure Git Worktree exists (WKT-1)
	if job.WorktreePath == "" {
		wtInfo, err := e.wtMgr.Create(jobCtx, project.RepoPath, project.Name, job.ID, project.BaseRef)
		if err != nil {
			// Persist failure state and audit log on worktree creation error
			_ = e.store.InTx(jobCtx, func(tx StoreTx) error {
				_ = tx.UpdateJobState(jobCtx, job.ID, job.Stage, StatusFailed)
				_ = tx.RecordEvent(jobCtx, job.ID, "job.status_changed", fmt.Sprintf(`{"status":%q,"error":%q}`, StatusFailed, err.Error()))
				return nil
			})
			return fmt.Errorf("factory: create worktree: %w", err)
		}

		job.WorktreePath = wtInfo.Path
		job.BranchName = wtInfo.Branch
		job.BaseSHA = wtInfo.BaseSHA
		job.HeadSHA = wtInfo.BaseSHA

		// Persist worktree coordinates inside a transaction
		err = e.store.InTx(jobCtx, func(tx StoreTx) error {
			return tx.UpdateJobWorktree(jobCtx, job.ID, wtInfo.Path, wtInfo.Branch, wtInfo.BaseSHA)
		})
		if err != nil {
			return fmt.Errorf("factory: persist worktree info: %w", err)
		}
	}

	// 2. Stage 01_Intent -> Transition to 02_Clarification_and_Spec for Feature profile (INT-1, PIP-1)
	if job.WorkType == WorkTypeFeature && job.Stage == StageIntent {
		if err := e.wtMgr.WriteArtifact(jobCtx, job.WorktreePath, job.ID, "intent.md", []byte(job.Intent)); err != nil {
			return fmt.Errorf("factory: write intent.md: %w", err)
		}

		headSHA, err := e.wtMgr.Checkpoint(jobCtx, job.WorktreePath, job.ID, "01_Intent")
		if err != nil {
			return fmt.Errorf("factory: intent checkpoint: %w", err)
		}
		job.HeadSHA = headSHA

		err = e.store.InTx(jobCtx, func(tx StoreTx) error {
			if err := tx.UpdateJobHead(jobCtx, job.ID, headSHA); err != nil {
				return err
			}
			if err := tx.UpdateJobState(jobCtx, job.ID, StageClarificationAndSpec, StatusQueued); err != nil {
				return err
			}
			if err := tx.RecordEvent(jobCtx, job.ID, "job.stage_changed", fmt.Sprintf(`{"stage":%q}`, StageClarificationAndSpec)); err != nil {
				return err
			}
			return tx.RecordEvent(jobCtx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageClarificationAndSpec, StatusQueued))
		})
		if err != nil {
			return fmt.Errorf("factory: transition to 02_Clarification_and_Spec: %w", err)
		}
		job.Stage = StageClarificationAndSpec
		job.Status = StatusQueued
	}

	// 3. Stage 02_Clarification_and_Spec (Feature Profile, SPC-1..5)
	if job.Stage == StageClarificationAndSpec {
		if job.Status == StatusQueued {
			if err := e.executeSpecStage(jobCtx, job, project, projCfg); err != nil {
				return err
			}
		}
		// If job entered needs_clarification or spec_review, yield execution (SPC-2, SPC-5, SPC-7)
		if job.Status == StatusNeedsClarification || job.Status == StatusSpecReview {
			e.notifyWake()
			return nil
		}
	}

	// 4. Stage 04_Coding: AI Agent writes code and undergoes automated repair loop (COD-1..7, GRD-1..4)
	if ((job.WorkType == "" || job.WorkType == WorkTypeRefactor) && (job.Stage == StageIntent || job.Stage == StageCoding)) ||
		(job.WorkType == WorkTypeFeature && job.Stage == StageCoding) {
		if err := e.executeCodingStage(jobCtx, job, project, projCfg); err != nil {
			return err
		}
	}

	// 3. Stage 05_Independent_Review: Review agent inspects diff and risk
	err = e.store.InTx(jobCtx, func(tx StoreTx) error {
		if err := tx.UpdateJobState(jobCtx, job.ID, StageIndependentReview, StatusRunning); err != nil {
			return err
		}
		return tx.RecordEvent(jobCtx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageIndependentReview, StatusRunning))
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
	if err := e.store.CreateStepRun(jobCtx, stepReview); err != nil {
		return fmt.Errorf("factory: create review step run: %w", err)
	}

	// Execute Review Agent
	resReview, err := e.agentRunner.Run(jobCtx, AgentRequest{
		JobID:        job.ID,
		Stage:        StageIndependentReview,
		WorktreePath: job.WorktreePath,
		ProjectName:  project.Name,
		LogPath:      logPathReview,
		OnProcessStart: func(pid, pgid int, startTime int64) {
			_ = e.store.CreateProcessRecord(jobCtx, stepReview.ID, pid, pgid, startTime)
		},
	})

	nowReview := time.Now().UTC()
	stepReview.EndedAt = &nowReview

	if err != nil || (resReview != nil && resReview.ExitCode != 0) {
		stepReview.Status = StepStatusFail
		stepReview.FailureCategory = FailureFlawed
		if resReview != nil {
			code := resReview.ExitCode
			stepReview.ExitCode = &code
		}
		_ = e.store.UpdateStepRun(jobCtx, stepReview)

		_ = e.store.InTx(jobCtx, func(tx StoreTx) error {
			_ = tx.UpdateJobState(jobCtx, job.ID, StageIndependentReview, StatusFailed)
			_ = tx.RecordEvent(jobCtx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageIndependentReview, StatusFailed))
			return nil
		})
		return fmt.Errorf("factory: review step failed: %w", err)
	}

	stepReview.Status = StepStatusSuccess
	codeReview := 0
	stepReview.ExitCode = &codeReview
	_ = e.store.UpdateStepRun(jobCtx, stepReview)

	// Create Checkpoint commit for review
	headSHAReview, err := e.wtMgr.Checkpoint(jobCtx, job.WorktreePath, job.ID, "review")
	if err != nil {
		return fmt.Errorf("factory: review checkpoint: %w", err)
	}
	job.HeadSHA = headSHAReview

	_ = e.store.InTx(jobCtx, func(tx StoreTx) error {
		return tx.UpdateJobHead(jobCtx, job.ID, headSHAReview)
	})

	// 4. Stage 06_Human_Approval_Gate
	err = e.store.InTx(jobCtx, func(tx StoreTx) error {
		if err := tx.UpdateJobState(jobCtx, job.ID, StageHumanApprovalGate, StatusAwaitingApproval); err != nil {
			return err
		}
		return tx.RecordEvent(jobCtx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageHumanApprovalGate, StatusAwaitingApproval))
	})
	if err != nil {
		return fmt.Errorf("factory: transition to gate: %w", err)
	}

	job.Stage = StageHumanApprovalGate
	job.Status = StatusAwaitingApproval
	e.notifyWake()
	return nil
}

// executeSpecStage executes the AI specification generation step and draft validation (SPC-1..5, LOG-3).
//
// Rules enforced:
// 1. Fresh session with intent and prior clarification.md (SPC-1).
// 2. Exactly one of clarification-questions.md or spec.md must be produced (SPC-1).
// 3. Ambiguous intent with questions transitions to 02/needs_clarification (SPC-2).
// 4. Draft spec.md is strictly validated against §6.1 invariants (SPC-4).
// 5. Valid draft spec is committed as a checkpoint and transitions to 02/spec_review (SPC-5).
func (e *Engine) executeSpecStage(ctx context.Context, job *Job, project *Project, projCfg *ProjectConfig) error {
	// Transition state to 02_Clarification_and_Spec / running
	err := e.store.InTx(ctx, func(tx StoreTx) error {
		if err := tx.UpdateJobState(ctx, job.ID, StageClarificationAndSpec, StatusRunning); err != nil {
			return err
		}
		return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageClarificationAndSpec, StatusRunning))
	})
	if err != nil {
		return fmt.Errorf("factory: transition to spec running: %w", err)
	}
	job.Stage = StageClarificationAndSpec
	job.Status = StatusRunning

	maxAttempts := e.maxRepairAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	var repairFeedback string
	attempt := 0

	for attempt < maxAttempts {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		currentAttempt := attempt + 1
		logFilename := "step_spec.log"
		if currentAttempt > 1 {
			logFilename = fmt.Sprintf("step_spec_attempt_%d.log", currentAttempt)
		}
		logPath := filepath.Join(e.logBaseDir, fmt.Sprintf("%d", job.ID), logFilename)

		step := &StepRun{
			JobID:     job.ID,
			Stage:     StageClarificationAndSpec,
			Kind:      StepKindAgent,
			Attempt:   currentAttempt,
			Executor:  "agent",
			Status:    StepStatusRunning,
			LogPath:   logPath,
			StartedAt: time.Now().UTC(),
		}
		if err := e.store.CreateStepRun(ctx, step); err != nil {
			return fmt.Errorf("factory: create spec step run: %w", err)
		}

		// Prompt construction (SPC-1): Intent + any clarification.md
		prompt := fmt.Sprintf("# Intent\n%s", job.Intent)
		if clarData, err := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, "clarification.md"); err == nil && len(clarData) > 0 {
			prompt = fmt.Sprintf("%s\n\n# Clarification History\n%s", prompt, string(clarData))
		}
		if repairFeedback != "" {
			prompt = fmt.Sprintf("%s\n\n[Automated Repair Feedback on Previous Attempt]\n%s", prompt, repairFeedback)
		}

		res, err := e.agentRunner.Run(ctx, AgentRequest{
			JobID:        job.ID,
			Stage:        StageClarificationAndSpec,
			WorktreePath: job.WorktreePath,
			Prompt:       prompt,
			ProjectName:  project.Name,
			LogPath:      logPath,
			OnProcessStart: func(pid, pgid int, startTime int64) {
				_ = e.store.CreateProcessRecord(ctx, step.ID, pid, pgid, startTime)
			},
		})

		now := time.Now().UTC()
		step.EndedAt = &now

		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err != nil || (res != nil && res.ExitCode != 0) {
			step.Status = StepStatusFail
			step.FailureCategory = FailureFlawed
			if res != nil {
				code := res.ExitCode
				step.ExitCode = &code
			}
			_ = e.store.UpdateStepRun(ctx, step)
			repairFeedback = fmt.Sprintf("Agent exited with error: %v", err)
			attempt++
			continue
		}

		// Inspect outputs produced under .garagefab/jobs/<id>/ (SPC-1)
		specBytes, errSpec := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, "spec.md")
		hasSpec := errSpec == nil && len(strings.TrimSpace(string(specBytes))) > 0

		qBytes, errQ := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, "clarification-questions.md")
		hasQuestions := errQ == nil && len(strings.TrimSpace(string(qBytes))) > 0

		// Rule SPC-1: Exactly one file produced
		if hasSpec && hasQuestions {
			step.Status = StepStatusFail
			step.FailureCategory = FailureFlawed
			_ = e.store.UpdateStepRun(ctx, step)
			repairFeedback = "Agent produced both spec.md and clarification-questions.md. Exactly one must be produced."
			attempt++
			continue
		}

		if !hasSpec && !hasQuestions {
			step.Status = StepStatusFail
			step.FailureCategory = FailureFlawed
			_ = e.store.UpdateStepRun(ctx, step)
			repairFeedback = "Agent produced neither spec.md nor clarification-questions.md. Exactly one must be produced."
			attempt++
			continue
		}

		// Case A: clarification-questions.md produced (SPC-2)
		if hasQuestions {
			step.Status = StepStatusSuccess
			code := 0
			step.ExitCode = &code
			_ = e.store.UpdateStepRun(ctx, step)

			err = e.store.InTx(ctx, func(tx StoreTx) error {
				if err := tx.UpdateJobState(ctx, job.ID, StageClarificationAndSpec, StatusNeedsClarification); err != nil {
					return err
				}
				return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageClarificationAndSpec, StatusNeedsClarification))
			})
			if err != nil {
				return fmt.Errorf("factory: transition to needs_clarification: %w", err)
			}
			job.Stage = StageClarificationAndSpec
			job.Status = StatusNeedsClarification
			return nil
		}

		// Case B: spec.md produced -> validate against §6.1 (SPC-4)
		if errVal := ValidateSpec(string(specBytes), job.WorkType); errVal != nil {
			step.Status = StepStatusFail
			step.FailureCategory = FailureFlawed
			_ = e.store.UpdateStepRun(ctx, step)
			repairFeedback = fmt.Sprintf("Specification validation failed: %v", errVal)
			attempt++
			continue
		}

		// Valid draft spec produced! (SPC-5)
		step.Status = StepStatusSuccess
		code := 0
		step.ExitCode = &code
		_ = e.store.UpdateStepRun(ctx, step)

		// Create Git checkpoint commit for draft spec
		headSHA, err := e.wtMgr.Checkpoint(ctx, job.WorktreePath, job.ID, "02_Clarification_and_Spec draft spec")
		if err != nil {
			return fmt.Errorf("factory: spec checkpoint: %w", err)
		}
		job.HeadSHA = headSHA

		err = e.store.InTx(ctx, func(tx StoreTx) error {
			if err := tx.UpdateJobHead(ctx, job.ID, headSHA); err != nil {
				return err
			}
			if err := tx.UpdateJobState(ctx, job.ID, StageClarificationAndSpec, StatusSpecReview); err != nil {
				return err
			}
			return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageClarificationAndSpec, StatusSpecReview))
		})
		if err != nil {
			return fmt.Errorf("factory: transition to spec_review: %w", err)
		}
		job.Stage = StageClarificationAndSpec
		job.Status = StatusSpecReview
		return nil
	}

	// All repair attempts exhausted -> transition to 02/failed (Manual)
	err = e.store.InTx(ctx, func(tx StoreTx) error {
		if err := tx.UpdateJobState(ctx, job.ID, StageClarificationAndSpec, StatusFailed); err != nil {
			return err
		}
		return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q,"failure_category":%q}`, StageClarificationAndSpec, StatusFailed, FailureManual))
	})
	if err != nil {
		return fmt.Errorf("factory: transition to spec failed: %w", err)
	}
	job.Stage = StageClarificationAndSpec
	job.Status = StatusFailed
	return fmt.Errorf("factory: spec generation failed after %d attempts: %s", maxAttempts, repairFeedback)
}

// executeCodingStage orchestrates the AI coding agent and the automated repair loop (COD-1..7, GRD-1..4).
// It re-runs all verification command groups (build -> test -> lint) upon repair (COD-6).
func (e *Engine) executeCodingStage(ctx context.Context, job *Job, project *Project, projCfg *ProjectConfig) error {
	// Transition state to 04_Coding / running
	err := e.store.InTx(ctx, func(tx StoreTx) error {
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

	maxAttempts := e.maxRepairAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	stepStartSHA := job.HeadSHA
	if stepStartSHA == "" {
		stepStartSHA = job.BaseSHA
	}

	var repairFeedback string
	attempt := 0

	for attempt < maxAttempts {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		currentAttempt := attempt + 1
		logFilename := "step_coding.log"
		if currentAttempt > 1 {
			logFilename = fmt.Sprintf("step_coding_attempt_%d.log", currentAttempt)
		}
		logPath := filepath.Join(e.logBaseDir, fmt.Sprintf("%d", job.ID), logFilename)

		step := &StepRun{
			JobID:     job.ID,
			Stage:     StageCoding,
			Kind:      StepKindAgent,
			Attempt:   currentAttempt,
			Executor:  "agent",
			Status:    StepStatusRunning,
			LogPath:   logPath,
			StartedAt: time.Now().UTC(),
		}
		if err := e.store.CreateStepRun(ctx, step); err != nil {
			return fmt.Errorf("factory: create coding step run: %w", err)
		}

		prompt := job.Intent
		if specBytes, err := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, "spec.md"); err == nil && len(specBytes) > 0 {
			prompt = string(specBytes)
		}
		// If rejection notes exist, append them to the coding prompt (APR-6)
		rejections, _ := e.wtMgr.ListArtifacts(ctx, job.WorktreePath, job.ID)
		for _, rej := range rejections {
			if strings.HasPrefix(rej, "rejections/") {
				if rData, err := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, rej); err == nil {
					prompt = fmt.Sprintf("%s\n\n[Rejection Note from Gate]\n%s", prompt, string(rData))
				}
			}
		}
		if repairFeedback != "" {
			prompt = fmt.Sprintf("%s\n\n[Automated Repair Feedback on Previous Attempt]\n%s", prompt, repairFeedback)
		}

		res, err := e.agentRunner.Run(ctx, AgentRequest{
			JobID:        job.ID,
			Stage:        StageCoding,
			WorktreePath: job.WorktreePath,
			Prompt:       prompt,
			ProjectName:  project.Name,
			LogPath:      logPath,
			OnProcessStart: func(pid, pgid int, startTime int64) {
				_ = e.store.CreateProcessRecord(ctx, step.ID, pid, pgid, startTime)
			},
		})

		now := time.Now().UTC()
		step.EndedAt = &now

		if ctx.Err() != nil {
			return ctx.Err()
		}

		var agentFailed bool
		var failureInput FailureInput
		failureInput.Attempt = currentAttempt
		failureInput.MaxAttempts = maxAttempts

		if err != nil || (res != nil && res.ExitCode != 0) {
			agentFailed = true
			code := 1
			if res != nil {
				code = res.ExitCode
			}
			step.ExitCode = &code
			failureInput.ExitCode = code
			if err != nil {
				failureInput.Stderr = err.Error()
			}
		} else {
			// Check empty diff against stepStartSHA (COD-7)
			diff, diffErr := e.wtMgr.Diff(ctx, job.WorktreePath, stepStartSHA)
			if diffErr == nil && strings.TrimSpace(diff) == "" {
				agentFailed = true
				code := 0
				step.ExitCode = &code
				failureInput.EmptyDiff = true
			}
		}

		if agentFailed {
			category := CategorizeFailure(failureInput)
			step.Status = StepStatusFail
			step.FailureCategory = category
			_ = e.store.UpdateStepRun(ctx, step)

			if category == FailureFlawed && currentAttempt < maxAttempts {
				if failureInput.EmptyDiff {
					repairFeedback = "Agent completed with exit code 0 but made no changes to files (empty diff). Please implement the requested code changes."
				} else if err != nil {
					repairFeedback = fmt.Sprintf("Agent process failed: %s", err.Error())
				} else {
					repairFeedback = fmt.Sprintf("Agent exited with non-zero exit code %d", failureInput.ExitCode)
				}
				attempt++
				continue
			}

			_ = e.store.InTx(ctx, func(tx StoreTx) error {
				_ = tx.UpdateJobState(ctx, job.ID, StageCoding, StatusFailed)
				_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q,"category":%q}`, StageCoding, StatusFailed, category))
				return nil
			})
			return fmt.Errorf("factory: coding step failed with category %s", category)
		}

		// Agent passed
		step.Status = StepStatusSuccess
		code := 0
		step.ExitCode = &code
		_ = e.store.UpdateStepRun(ctx, step)

		// Guardrail check: protected_paths (GRD-1)
		var guardrailFailed bool
		if e.guardrailRunner != nil && len(projCfg.Guardrails.ProtectedPaths) > 0 {
			violations, gErr := e.guardrailRunner.CheckProtectedPaths(ctx, job.WorktreePath, stepStartSHA, projCfg.Guardrails.ProtectedPaths)
			if gErr == nil && len(violations) > 0 {
				guardrailFailed = true
				var paths []string
				for _, v := range violations {
					paths = append(paths, v.Path)
				}
				category := CategorizeFailure(FailureInput{
					GuardrailViolation: true,
					Attempt:            currentAttempt,
					MaxAttempts:        maxAttempts,
				})
				if category == FailureFlawed && currentAttempt < maxAttempts {
					repairFeedback = fmt.Sprintf("Guardrail violation: you modified existing protected file(s): %s. You must restore them.", strings.Join(paths, ", "))
					attempt++
					continue
				}
				_ = e.store.InTx(ctx, func(tx StoreTx) error {
					_ = tx.UpdateJobState(ctx, job.ID, StageCoding, StatusFailed)
					_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q,"category":%q}`, StageCoding, StatusFailed, category))
					return nil
				})
				return fmt.Errorf("factory: guardrail violation: %v", paths)
			}
		}

		// Custom guardrail commands (GRD-3)
		if !guardrailFailed && len(projCfg.Guardrails.Commands) > 0 && e.cmdRunner != nil {
			var cmdFailed bool
			for _, gCmd := range projCfg.Guardrails.Commands {
				cRes, cErr := e.cmdRunner.Run(ctx, CommandOptions{
					WorkDir: job.WorktreePath,
					Command: gCmd,
				})
				if cErr != nil || (cRes != nil && cRes.ExitCode != 0) {
					cmdFailed = true
					exitCode := 1
					var stderr, stdout string
					if cRes != nil {
						exitCode = cRes.ExitCode
						stderr = cRes.Stderr
						stdout = cRes.Stdout
					}
					category := CategorizeFailure(FailureInput{
						ExitCode:    exitCode,
						Stdout:      stdout,
						Stderr:      stderr,
						Attempt:     currentAttempt,
						MaxAttempts: maxAttempts,
					})
					if category == FailureFlawed && currentAttempt < maxAttempts {
						repairFeedback = fmt.Sprintf("Custom guardrail command failed (%s):\n%s\n%s", gCmd, stdout, stderr)
						attempt++
						break
					}
					_ = e.store.InTx(ctx, func(tx StoreTx) error {
						_ = tx.UpdateJobState(ctx, job.ID, StageCoding, StatusFailed)
						_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q,"category":%q}`, StageCoding, StatusFailed, category))
						return nil
					})
					return fmt.Errorf("factory: custom guardrail command failed: %s", gCmd)
				}
			}
			if cmdFailed {
				continue
			}
		}

		// Verification Commands: Build -> Test -> Lint (COD-2, COD-3, COD-6)
		var commandFailed bool
		commandGroups := []struct {
			name string
			cmds []string
		}{
			{"build", projCfg.Commands.Build},
			{"test", projCfg.Commands.Test},
			{"lint", projCfg.Commands.Lint},
		}

		for _, grp := range commandGroups {
			if len(grp.cmds) == 0 {
				skipStep := &StepRun{
					JobID:     job.ID,
					Stage:     StageCoding,
					Kind:      StepKindCommand,
					Attempt:   currentAttempt,
					Executor:  grp.name,
					Status:    StepStatusSkipped,
					StartedAt: time.Now().UTC(),
				}
				_ = e.store.CreateStepRun(ctx, skipStep)
				now := time.Now().UTC()
				skipStep.EndedAt = &now
				_ = e.store.UpdateStepRun(ctx, skipStep)
				continue
			}

			for _, cmdStr := range grp.cmds {
				cmdLogPath := filepath.Join(e.logBaseDir, fmt.Sprintf("%d", job.ID), fmt.Sprintf("step_cmd_%s_%d.log", grp.name, currentAttempt))
				cmdStep := &StepRun{
					JobID:     job.ID,
					Stage:     StageCoding,
					Kind:      StepKindCommand,
					Attempt:   currentAttempt,
					Executor:  grp.name,
					Status:    StepStatusRunning,
					LogPath:   cmdLogPath,
					StartedAt: time.Now().UTC(),
				}
				_ = e.store.CreateStepRun(ctx, cmdStep)

				cRes, cErr := e.cmdRunner.Run(ctx, CommandOptions{
					WorkDir: job.WorktreePath,
					Command: cmdStr,
					LogPath: cmdLogPath,
					OnProcessStart: func(pid, pgid int, startTime int64) {
						_ = e.store.CreateProcessRecord(ctx, cmdStep.ID, pid, pgid, startTime)
					},
				})

				now := time.Now().UTC()
				cmdStep.EndedAt = &now

				if cErr != nil || (cRes != nil && cRes.ExitCode != 0) {
					commandFailed = true
					exitCode := 1
					var stderr, stdout string
					if cRes != nil {
						exitCode = cRes.ExitCode
						stderr = cRes.Stderr
						stdout = cRes.Stdout
					}
					cmdStep.ExitCode = &exitCode
					category := CategorizeFailure(FailureInput{
						ExitCode:    exitCode,
						Stdout:      stdout,
						Stderr:      stderr,
						Attempt:     currentAttempt,
						MaxAttempts: maxAttempts,
					})
					cmdStep.Status = StepStatusFail
					cmdStep.FailureCategory = category
					_ = e.store.UpdateStepRun(ctx, cmdStep)

					if category == FailureFlawed && currentAttempt < maxAttempts {
						repairFeedback = fmt.Sprintf("Command '%s' failed with exit code %d:\n%s\n%s", cmdStr, exitCode, stdout, stderr)
						attempt++
						break
					}

					_ = e.store.InTx(ctx, func(tx StoreTx) error {
						_ = tx.UpdateJobState(ctx, job.ID, StageCoding, StatusFailed)
						_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q,"category":%q}`, StageCoding, StatusFailed, category))
						return nil
					})
					return fmt.Errorf("factory: command '%s' failed with category %s", cmdStr, category)
				}

				code := 0
				cmdStep.ExitCode = &code
				cmdStep.Status = StepStatusSuccess
				_ = e.store.UpdateStepRun(ctx, cmdStep)
			}

			if commandFailed {
				break
			}
		}

		if commandFailed {
			continue
		}

		// All checks passed!
		break
	}

	// Create Checkpoint commit (WKT-5, COD-9)
	headSHA, err := e.wtMgr.Checkpoint(ctx, job.WorktreePath, job.ID, "coding")
	if err != nil {
		return fmt.Errorf("factory: coding checkpoint: %w", err)
	}
	job.HeadSHA = headSHA

	return e.store.InTx(ctx, func(tx StoreTx) error {
		return tx.UpdateJobHead(ctx, job.ID, headSHA)
	})
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

	// Signal and abort active execution context if job is currently running (PIP-6)
	if cancelVal, ok := e.activeJobCancels.Load(jobID); ok {
		if cancelFn, isCancel := cancelVal.(context.CancelFunc); isCancel {
			cancelFn()
		}
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
