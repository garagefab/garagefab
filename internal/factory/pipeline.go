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
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
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
	// ErrSpecInvalid is returned when a spec fails validation upon approval (SPC-6).
	ErrSpecInvalid = errors.New("factory: spec validation failed")
	// ErrJobAlreadyExecuting is returned if a job is already running in this process (PIP-5).
	ErrJobAlreadyExecuting = errors.New("factory: job is already executing")
)

// Engine orchestrates pipeline execution across stages (PIP-1..5).
type Engine struct {
	store               Store                 // Persistence port
	wtMgr               WorktreeManager       // Worktree lifecycle port
	agentRunner         AgentRunner           // AI agent execution port
	cmdRunner           CommandRunner         // Command runner port
	guardrailRunner     GuardrailRunner       // Guardrail runner port (GRD-1..4)
	projCfgProvider     ProjectConfigProvider // Project configuration loader (architecture.md §14)
	maxRepairAttempts   int                   // Maximum automated repair loop attempts (default 3, COD-4)
	defaultAgentTimeout time.Duration         // Default timeout boundary for AI agent steps (COD-10)
	logBaseDir          string                // Base path on disk for step logs (~/.garagefab/logs)

	executingJobs    sync.Map // Thread-safe set tracking actively executing job IDs (PIP-5)
	activeJobCancels sync.Map // Map[int64]context.CancelFunc for terminating active jobs (PIP-6)
	wakeFn           func()   // Callback notifying scheduler when a job finishes or yields
}

// NewEngine creates a new pipeline engine instance with injected port dependencies.
func NewEngine(store Store, wtMgr WorktreeManager, agentRunner AgentRunner, cmdRunner CommandRunner, logBaseDir string) *Engine {
	return &Engine{
		store:               store,
		wtMgr:               wtMgr,
		agentRunner:         agentRunner,
		cmdRunner:           cmdRunner,
		maxRepairAttempts:   3,
		defaultAgentTimeout: 30 * time.Minute,
		logBaseDir:          logBaseDir,
	}
}

// SetDefaultAgentTimeout configures the fallback timeout boundary for AI agent steps (COD-10).
func (e *Engine) SetDefaultAgentTimeout(d time.Duration) {
	if d > 0 {
		e.defaultAgentTimeout = d
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
			Agents: map[string]string{
				RoleSpec:   "fake",
				RoleProbe:  "fake",
				RoleCoding: "fake",
				RoleReview: "fake",
			},
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

	// 5. Stage 05_Independent_Review: Review agent inspects diff and risk (REV-1..6, APR-1..4)
	if job.Stage == StageCoding || job.Stage == StageIndependentReview {
		if err := e.executeReviewStage(jobCtx, job, project, projCfg); err != nil {
			return err
		}
	}

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

		// Prompt construction (SPC-1, SPC-2): Render standardized spec prompt
		var clarificationContent string
		if clarData, err := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, "clarification.md"); err == nil && len(clarData) > 0 {
			clarificationContent = string(clarData)
		}

		promptData := PromptData{
			JobID:          job.ID,
			WorkType:       job.WorkType,
			Intent:         job.Intent,
			ArtifactDir:    fmt.Sprintf(".garagefab/jobs/%d", job.ID),
			Clarification:  clarificationContent,
			RepairFeedback: repairFeedback,
		}
		prompt, err := RenderPrompt(RoleSpec, promptData)
		if err != nil {
			return fmt.Errorf("factory: render spec prompt: %w", err)
		}

		// Resolve role, agent, and step timeout (HND-2, COD-10)
		role, _ := RoleForStage(StageClarificationAndSpec)
		stepTimeout := e.defaultAgentTimeout
		if projCfg != nil && projCfg.AgentTimeout > 0 {
			stepTimeout = projCfg.AgentTimeout
		}
		agentName := ""
		if projCfg != nil {
			agentName = projCfg.AgentForRole(role)
		}
		if agentName == "" && os.Getenv("GARAGEFAB_FAKE_AGENT") == "1" {
			agentName = "fake"
		}
		if agentName == "" {
			step.Status = StepStatusFail
			step.FailureCategory = FailureBlocked
			now := time.Now().UTC()
			step.EndedAt = &now
			_ = e.store.UpdateStepRun(ctx, step)
			errMsg := fmt.Sprintf("no agent configured for role %q (set agents.%s in .garagefab/project.yaml)", role, role)
			_ = e.store.InTx(ctx, func(tx StoreTx) error {
				_ = tx.UpdateJobState(ctx, job.ID, StageClarificationAndSpec, StatusFailed)
				_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q,"category":%q,"error":%q}`, StageClarificationAndSpec, StatusFailed, FailureBlocked, errMsg))
				return nil
			})
			return fmt.Errorf("factory: %s", errMsg)
		}

		res, err := e.agentRunner.Run(ctx, AgentRequest{
			JobID:        job.ID,
			Stage:        StageClarificationAndSpec,
			Role:         role,
			Agent:        agentName,
			WorktreePath: job.WorktreePath,
			Prompt:       prompt,
			ProjectName:  project.Name,
			LogPath:      logPath,
			Timeout:      stepTimeout,
			OnProcessStart: func(pid, pgid int, startTime int64) {
				_ = e.store.CreateProcessRecord(ctx, step.ID, pid, pgid, startTime)
			},
		})

		now := time.Now().UTC()
		step.EndedAt = &now

		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Handle agent process timeout (COD-10)
		if res != nil && res.TimedOut {
			step.Status = StepStatusFail
			step.FailureCategory = FailureBlocked
			code := 1
			if res != nil {
				code = res.ExitCode
			}
			step.ExitCode = &code
			_ = e.store.UpdateStepRun(ctx, step)
			_ = e.store.InTx(ctx, func(tx StoreTx) error {
				_ = tx.UpdateJobState(ctx, job.ID, StageClarificationAndSpec, StatusFailed)
				_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q,"category":%q}`, StageClarificationAndSpec, StatusFailed, FailureBlocked))
				return nil
			})
			return fmt.Errorf("factory: spec agent timed out: %s", FailureBlocked)
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

		var specContent string
		if specBytes, err := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, "spec.md"); err == nil && len(specBytes) > 0 {
			specContent = string(specBytes)
		}

		var rejectionNotes []string
		rejections, _ := e.wtMgr.ListArtifacts(ctx, job.WorktreePath, job.ID)
		for _, rej := range rejections {
			if strings.HasPrefix(rej, "rejections/") {
				if rData, err := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, rej); err == nil {
					rejectionNotes = append(rejectionNotes, string(rData))
				}
			}
		}

		var buildCmds, testCmds, lintCmds []string
		var protectedPaths []string
		if projCfg != nil {
			buildCmds = projCfg.Commands.Build
			testCmds = projCfg.Commands.Test
			lintCmds = projCfg.Commands.Lint
			protectedPaths = projCfg.Guardrails.ProtectedPaths
		}

		promptData := PromptData{
			JobID:          job.ID,
			WorkType:       job.WorkType,
			Intent:         job.Intent,
			ArtifactDir:    fmt.Sprintf(".garagefab/jobs/%d", job.ID),
			Spec:           specContent,
			RejectionNotes: rejectionNotes,
			RepairFeedback: repairFeedback,
			BuildCmds:      buildCmds,
			TestCmds:       testCmds,
			LintCmds:       lintCmds,
			ProtectedPaths: protectedPaths,
		}
		prompt, err := RenderPrompt(RoleCoding, promptData)
		if err != nil {
			return fmt.Errorf("factory: render coding prompt: %w", err)
		}

		// Resolve role, agent, and step timeout (HND-2, COD-10)
		role, _ := RoleForStage(StageCoding)
		stepTimeout := e.defaultAgentTimeout
		if projCfg != nil && projCfg.AgentTimeout > 0 {
			stepTimeout = projCfg.AgentTimeout
		}
		agentName := ""
		if projCfg != nil {
			agentName = projCfg.AgentForRole(role)
		}
		if agentName == "" && os.Getenv("GARAGEFAB_FAKE_AGENT") == "1" {
			agentName = "fake"
		}
		if agentName == "" {
			step.Status = StepStatusFail
			step.FailureCategory = FailureBlocked
			now := time.Now().UTC()
			step.EndedAt = &now
			_ = e.store.UpdateStepRun(ctx, step)
			errMsg := fmt.Sprintf("no agent configured for role %q (set agents.%s in .garagefab/project.yaml)", role, role)
			_ = e.store.InTx(ctx, func(tx StoreTx) error {
				_ = tx.UpdateJobState(ctx, job.ID, StageCoding, StatusFailed)
				_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q,"category":%q,"error":%q}`, StageCoding, StatusFailed, FailureBlocked, errMsg))
				return nil
			})
			return fmt.Errorf("factory: %s", errMsg)
		}

		res, err := e.agentRunner.Run(ctx, AgentRequest{
			JobID:        job.ID,
			Stage:        StageCoding,
			Role:         role,
			Agent:        agentName,
			WorktreePath: job.WorktreePath,
			Prompt:       prompt,
			ProjectName:  project.Name,
			LogPath:      logPath,
			Timeout:      stepTimeout,
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

		if (res != nil && res.TimedOut) || err != nil || (res != nil && res.ExitCode != 0) {
			agentFailed = true
			code := 1
			if res != nil {
				code = res.ExitCode
				if res.TimedOut {
					failureInput.TimedOut = true
				}
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

// executeReviewStage executes the independent review agent, validates critique results,
// checks for unauthorized repository modifications, commits the review checkpoint,
// and compiles the immutable audit evidence for the human gate (REV-1..6, APR-1..4).
//
// Role in Hexagonal Architecture:
// Acts as the Orchestration Service for stage 05_Independent_Review.
// Java / Spring Comparison: Similar to an automated SonarQube/Checkstyle quality gate step in a Jenkins/GitLab pipeline,
// but with an autonomous LLM critic producing structured JSON risk and defect assessments.
func (e *Engine) executeReviewStage(ctx context.Context, job *Job, project *Project, projCfg *ProjectConfig) error {
	// 1. Transition state to 05_Independent_Review / running
	err := e.store.InTx(ctx, func(tx StoreTx) error {
		if err := tx.UpdateJobState(ctx, job.ID, StageIndependentReview, StatusRunning); err != nil {
			return err
		}
		if err := tx.RecordEvent(ctx, job.ID, "job.stage_changed", fmt.Sprintf(`{"stage":%q}`, StageIndependentReview)); err != nil {
			return err
		}
		return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageIndependentReview, StatusRunning))
	})
	if err != nil {
		return fmt.Errorf("factory: transition to review: %w", err)
	}
	job.Stage = StageIndependentReview
	job.Status = StatusRunning

	// Capture worktree HEAD SHA before review agent execution to verify no code is tampered (REV-4)
	stepStartSHA, err := e.wtMgr.HeadSHA(ctx, job.WorktreePath)
	if err != nil || stepStartSHA == "" {
		stepStartSHA = job.HeadSHA
	}

	// 2. Prepare isolated prompt without prior coding agent conversation history (REV-1)
	var specContent string
	if specBytes, err := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, "spec.md"); err == nil && len(specBytes) > 0 {
		specContent = string(specBytes)
	}

	diffOutput, _ := e.wtMgr.Diff(ctx, job.WorktreePath, job.BaseSHA)

	// Gather prior test/build command results
	stepRuns, _ := e.store.ListStepRunsByJob(ctx, job.ID)
	var cmdSummary []string
	for _, sr := range stepRuns {
		if sr.Stage == StageCoding && sr.Kind == StepKindCommand {
			exit := 0
			if sr.ExitCode != nil {
				exit = *sr.ExitCode
			}
			cmdSummary = append(cmdSummary, fmt.Sprintf("Attempt %d command (exit %d, status: %s): %s", sr.Attempt, exit, sr.Status, sr.LogPath))
		}
	}

	promptDataReview := PromptData{
		JobID:          job.ID,
		WorkType:       job.WorkType,
		Intent:         job.Intent,
		ArtifactDir:    fmt.Sprintf(".garagefab/jobs/%d", job.ID),
		Spec:           specContent,
		Diff:           diffOutput,
		CommandSummary: cmdSummary,
	}
	reviewPrompt, err := RenderPrompt(RoleReview, promptDataReview)
	if err != nil {
		return fmt.Errorf("factory: render review prompt: %w", err)
	}

	// 3. Initialize StepRun for Review (REV-6: single attempt, no repair loop)
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

	// 4. Execute Review Agent
	roleReview, _ := RoleForStage(StageIndependentReview)
	stepTimeout := e.defaultAgentTimeout
	if projCfg != nil && projCfg.AgentTimeout > 0 {
		stepTimeout = projCfg.AgentTimeout
	}
	agentNameReview := ""
	if projCfg != nil {
		agentNameReview = projCfg.AgentForRole(roleReview)
	}
	if agentNameReview == "" && os.Getenv("GARAGEFAB_FAKE_AGENT") == "1" {
		agentNameReview = "fake"
	}
	if agentNameReview == "" {
		stepReview.Status = StepStatusFail
		stepReview.FailureCategory = FailureBlocked
		now := time.Now().UTC()
		stepReview.EndedAt = &now
		_ = e.store.UpdateStepRun(ctx, stepReview)
		errMsg := fmt.Sprintf("no agent configured for role %q (set agents.%s in .garagefab/project.yaml)", roleReview, roleReview)
		_ = e.store.InTx(ctx, func(tx StoreTx) error {
			_ = tx.UpdateJobState(ctx, job.ID, StageIndependentReview, StatusFailed)
			_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q,"category":%q,"error":%q}`, StageIndependentReview, StatusFailed, FailureBlocked, errMsg))
			return nil
		})
		return fmt.Errorf("factory: %s", errMsg)
	}

	resReview, runErr := e.agentRunner.Run(ctx, AgentRequest{
		JobID:        job.ID,
		Stage:        StageIndependentReview,
		Role:         roleReview,
		Agent:        agentNameReview,
		WorktreePath: job.WorktreePath,
		Prompt:       reviewPrompt,
		ProjectName:  project.Name,
		LogPath:      logPathReview,
		Timeout:      stepTimeout,
		OnProcessStart: func(pid, pgid int, startTime int64) {
			_ = e.store.CreateProcessRecord(ctx, stepReview.ID, pid, pgid, startTime)
		},
	})

	nowReview := time.Now().UTC()
	stepReview.EndedAt = &nowReview

	// Review agent process timeout (COD-10)
	if resReview != nil && resReview.TimedOut {
		stepReview.Status = StepStatusFail
		stepReview.FailureCategory = FailureBlocked
		code := 1
		stepReview.ExitCode = &code
		_ = e.store.UpdateStepRun(ctx, stepReview)
		_ = e.store.InTx(ctx, func(tx StoreTx) error {
			_ = tx.UpdateJobState(ctx, job.ID, StageIndependentReview, StatusFailed)
			_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q,"category":%q}`, StageIndependentReview, StatusFailed, FailureBlocked))
			return nil
		})
		return fmt.Errorf("factory: review agent timed out: %s", FailureBlocked)
	}

	// Review agent crash or execution error (REV-6)
	if runErr != nil || (resReview != nil && resReview.ExitCode != 0) {
		stepReview.Status = StepStatusFail
		stepReview.FailureCategory = FailureFlawed
		if resReview != nil {
			code := resReview.ExitCode
			stepReview.ExitCode = &code
		}
		_ = e.store.UpdateStepRun(ctx, stepReview)
		_ = e.store.InTx(ctx, func(tx StoreTx) error {
			_ = tx.UpdateJobState(ctx, job.ID, StageIndependentReview, StatusFailed)
			_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q,"category":%q}`, StageIndependentReview, StatusFailed, FailureFlawed))
			return nil
		})
		return fmt.Errorf("factory: review agent failed: %w", runErr)
	}

	// 5. Detect Repository Tampering (REV-4)
	// Any code changes made outside .garagefab/ during review fail the step as Flawed
	// and revert the modifications to stepStartSHA.
	tamperDiff, _ := e.wtMgr.Diff(ctx, job.WorktreePath, stepStartSHA)
	if hasCodeModifications(tamperDiff) {
		_ = e.wtMgr.Reset(ctx, job.WorktreePath, stepStartSHA)
		stepReview.Status = StepStatusFail
		stepReview.FailureCategory = FailureFlawed
		code := 1
		stepReview.ExitCode = &code
		_ = e.store.UpdateStepRun(ctx, stepReview)
		_ = e.store.InTx(ctx, func(tx StoreTx) error {
			_ = tx.UpdateJobState(ctx, job.ID, StageIndependentReview, StatusFailed)
			_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q,"category":%q}`, StageIndependentReview, StatusFailed, FailureFlawed))
			return nil
		})
		return fmt.Errorf("factory: review agent modified code outside review artifacts (REV-4)")
	}

	// 6. Read and Validate review.json (REV-2, REV-6)
	reviewBytes, readErr := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, "review.json")
	if readErr != nil {
		stepReview.Status = StepStatusFail
		stepReview.FailureCategory = FailureFlawed
		_ = e.store.UpdateStepRun(ctx, stepReview)
		_ = e.store.InTx(ctx, func(tx StoreTx) error {
			_ = tx.UpdateJobState(ctx, job.ID, StageIndependentReview, StatusFailed)
			_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q,"category":%q}`, StageIndependentReview, StatusFailed, FailureFlawed))
			return nil
		})
		return fmt.Errorf("factory: review.json missing: %w", readErr)
	}

	reviewReport, valErr := ValidateReviewJSON(reviewBytes)
	if valErr != nil {
		stepReview.Status = StepStatusFail
		stepReview.FailureCategory = FailureFlawed
		_ = e.store.UpdateStepRun(ctx, stepReview)
		_ = e.store.InTx(ctx, func(tx StoreTx) error {
			_ = tx.UpdateJobState(ctx, job.ID, StageIndependentReview, StatusFailed)
			_ = tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q,"category":%q}`, StageIndependentReview, StatusFailed, FailureFlawed))
			return nil
		})
		return fmt.Errorf("factory: validate review.json: %w", valErr)
	}

	// Review step succeeded
	stepReview.Status = StepStatusSuccess
	codeReview := 0
	stepReview.ExitCode = &codeReview
	_ = e.store.UpdateStepRun(ctx, stepReview)

	// 7. Checkpoint Review Artifact (REV-5)
	headSHAReview, err := e.wtMgr.Checkpoint(ctx, job.WorktreePath, job.ID, "05_Independent_Review review.json")
	if err != nil {
		return fmt.Errorf("factory: review checkpoint: %w", err)
	}
	job.HeadSHA = headSHAReview

	// 8. Build and Commit Evidence Summary (APR-1..4)
	diffStat := ParseDiffStat(diffOutput)
	// Refresh step runs including review step
	allSteps, _ := e.store.ListStepRunsByJob(ctx, job.ID)
	_, evidenceMD := BuildEvidence(job, allSteps, reviewReport, diffStat)
	if err := e.wtMgr.WriteArtifact(ctx, job.WorktreePath, job.ID, "evidence.md", []byte(evidenceMD)); err != nil {
		return fmt.Errorf("factory: write evidence.md: %w", err)
	}

	headSHAGate, err := e.wtMgr.Checkpoint(ctx, job.WorktreePath, job.ID, "06_Human_Approval_Gate evidence.md")
	if err != nil {
		return fmt.Errorf("factory: evidence checkpoint: %w", err)
	}
	job.HeadSHA = headSHAGate

	// Update Job Head and transition to 06_Human_Approval_Gate / awaiting_approval
	err = e.store.InTx(ctx, func(tx StoreTx) error {
		if err := tx.UpdateJobHead(ctx, job.ID, headSHAGate); err != nil {
			return err
		}
		if err := tx.UpdateJobState(ctx, job.ID, StageHumanApprovalGate, StatusAwaitingApproval); err != nil {
			return err
		}
		if err := tx.RecordEvent(ctx, job.ID, "job.stage_changed", fmt.Sprintf(`{"stage":%q}`, StageHumanApprovalGate)); err != nil {
			return err
		}
		return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageHumanApprovalGate, StatusAwaitingApproval))
	})
	if err != nil {
		return fmt.Errorf("factory: transition to gate: %w", err)
	}

	job.Stage = StageHumanApprovalGate
	job.Status = StatusAwaitingApproval
	e.notifyWake()
	return nil
}

// hasCodeModifications checks whether any files outside the metadata directory (.garagefab/)
// were added, changed, or deleted in the worktree (REV-4).
func hasCodeModifications(diff string) bool {
	lines := strings.Split(diff, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git a/") {
			parts := strings.Split(line, " ")
			if len(parts) >= 3 {
				path := strings.TrimPrefix(parts[2], "a/")
				if !strings.HasPrefix(path, ".garagefab/") {
					return true
				}
			}
		}
	}
	return false
}

// Approve records human approval and advances the job (SPC-6, SPC-7, APR-5, APR-7, DLV-4).
// - At 02_Clarification_and_Spec / spec_review: re-validates spec.md, records SHA-256 hash, and advances to 04_Coding / queued (SPC-6).
// - At 06_Human_Approval_Gate / awaiting_approval: verifies evidence HEAD SHA matches current worktree HEAD (APR-5) and advances to 07_Done.
func (e *Engine) Approve(ctx context.Context, jobID int64, headSHA string) error {
	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("factory: get job %d: %w", jobID, err)
	}

	// Case 1: Spec Review Gate (SPC-6, SPC-7)
	if job.Stage == StageClarificationAndSpec && job.Status == StatusSpecReview {
		specBytes, err := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, "spec.md")
		if err != nil {
			return fmt.Errorf("factory: read spec.md: %w", err)
		}

		// Re-validate spec as it is currently in the worktree
		if err := ValidateSpec(string(specBytes), job.WorkType); err != nil {
			return fmt.Errorf("%w: %v", ErrSpecInvalid, err)
		}

		specHash := fmt.Sprintf("%x", sha256.Sum256(specBytes))

		// Commit approved spec to worktree checkpoint (SPC-6)
		newHeadSHA, err := e.wtMgr.Checkpoint(ctx, job.WorktreePath, job.ID, "approved spec.md")
		if err != nil {
			return fmt.Errorf("factory: checkpoint approved spec: %w", err)
		}

		err = e.store.InTx(ctx, func(tx StoreTx) error {
			approval := &Approval{
				JobID:    job.ID,
				Gate:     ApprovalGateSpecReview,
				Decision: ApprovalDecisionApprove,
				Note:     fmt.Sprintf("spec_hash:%s", specHash),
				HeadSHA:  newHeadSHA,
			}
			if err := tx.RecordApproval(ctx, approval); err != nil {
				return err
			}
			if err := tx.UpdateJobHead(ctx, job.ID, newHeadSHA); err != nil {
				return err
			}
			if err := tx.UpdateJobState(ctx, job.ID, StageCoding, StatusQueued); err != nil {
				return err
			}
			if err := tx.RecordEvent(ctx, job.ID, "job.stage_changed", fmt.Sprintf(`{"stage":%q}`, StageCoding)); err != nil {
				return err
			}
			return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageCoding, StatusQueued))
		})
		if err != nil {
			return fmt.Errorf("factory: record spec approval: %w", err)
		}

		job.Stage = StageCoding
		job.Status = StatusQueued
		job.HeadSHA = newHeadSHA
		e.notifyWake()
		return nil
	}

	// Case 2: Final Gate (APR-5, APR-7)
	if job.Stage == StageHumanApprovalGate && job.Status == StatusAwaitingApproval {
		// Stale Evidence Check (APR-5): Reject approval if user approved outdated commit SHA
		if headSHA != "" && job.HeadSHA != "" && headSHA != job.HeadSHA {
			return fmt.Errorf("%w: provided %s, current %s", ErrStaleEvidence, headSHA, job.HeadSHA)
		}
		// Also verify worktree HEAD has not advanced past the evidence commit
		if job.WorktreePath != "" {
			currentHead, err := e.wtMgr.HeadSHA(ctx, job.WorktreePath)
			if err == nil && currentHead != "" && job.HeadSHA != "" && currentHead != job.HeadSHA {
				return fmt.Errorf("%w: worktree HEAD %s, evidence HEAD %s", ErrStaleEvidence, currentHead, job.HeadSHA)
			}
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
			if err := tx.RecordEvent(ctx, job.ID, "job.stage_changed", fmt.Sprintf(`{"stage":%q}`, StageDone)); err != nil {
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

		job.Stage = StageDone
		job.Status = StatusDone
		e.notifyWake()
		return nil
	}

	return fmt.Errorf("%w: job %d is in %s/%s", ErrInvalidState, jobID, job.Stage, job.Status)
}

// Reject records a human rejection note and routes the job back for repair/re-spec (APR-6, COD-5, SPC-7).
func (e *Engine) Reject(ctx context.Context, jobID int64, note string) error {
	// Rejection note is mandatory (APR-6)
	if strings.TrimSpace(note) == "" {
		return ErrEmptyRejectionNote
	}

	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("factory: get job %d: %w", jobID, err)
	}

	// Count existing rejections to determine next rejection file number
	rejectionNum := 1
	if job.WorktreePath != "" {
		artifacts, _ := e.wtMgr.ListArtifacts(ctx, job.WorktreePath, job.ID)
		for _, art := range artifacts {
			if strings.HasPrefix(art, "rejections/") {
				rejectionNum++
			}
		}
		// Write rejection artifact: .garagefab/jobs/<id>/rejections/<n>.md
		rejContent := fmt.Sprintf("# Rejection Note %d\nDate: %s\n\n%s\n", rejectionNum, time.Now().UTC().Format(time.RFC3339), note)
		_ = e.wtMgr.WriteArtifact(ctx, job.WorktreePath, job.ID, fmt.Sprintf("rejections/%d.md", rejectionNum), []byte(rejContent))
	}

	// Case 1: Spec Review Rejection (SPC-7)
	if job.Stage == StageClarificationAndSpec && job.Status == StatusSpecReview {
		if job.WorktreePath != "" {
			_, _ = e.wtMgr.Checkpoint(ctx, job.WorktreePath, job.ID, "spec rejected")
		}
		err = e.store.InTx(ctx, func(tx StoreTx) error {
			approval := &Approval{
				JobID:    job.ID,
				Gate:     ApprovalGateSpecReview,
				Decision: ApprovalDecisionReject,
				Note:     note,
				HeadSHA:  job.HeadSHA,
			}
			if err := tx.RecordApproval(ctx, approval); err != nil {
				return err
			}
			if err := tx.UpdateJobState(ctx, job.ID, StageClarificationAndSpec, StatusQueued); err != nil {
				return err
			}
			return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageClarificationAndSpec, StatusQueued))
		})
		if err != nil {
			return fmt.Errorf("factory: record spec rejection: %w", err)
		}
		job.Stage = StageClarificationAndSpec
		job.Status = StatusQueued
		e.notifyWake()
		return nil
	}

	// Case 2: Final Gate Rejection (APR-6, COD-5)
	if job.Stage == StageHumanApprovalGate && job.Status == StatusAwaitingApproval {
		if job.WorktreePath != "" {
			_, _ = e.wtMgr.Checkpoint(ctx, job.WorktreePath, job.ID, "rejected at gate")
		}
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
			// Reset repair attempts counter to 0 (COD-5) and route to 04_Coding / queued
			if err := tx.UpdateJobState(ctx, job.ID, StageCoding, StatusQueued); err != nil {
				return err
			}
			if err := tx.RecordEvent(ctx, job.ID, "job.stage_changed", fmt.Sprintf(`{"stage":%q}`, StageCoding)); err != nil {
				return err
			}
			return tx.RecordEvent(ctx, job.ID, "job.status_changed", fmt.Sprintf(`{"stage":%q,"status":%q}`, StageCoding, StatusQueued))
		})
		if err != nil {
			return fmt.Errorf("factory: record rejection: %w", err)
		}
		job.Stage = StageCoding
		job.Status = StatusQueued
		e.notifyWake()
		return nil
	}

	return fmt.Errorf("%w: job %d is in %s/%s", ErrInvalidState, jobID, job.Stage, job.Status)
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

// GetArtifact retrieves a named job artifact from the worktree (LOG-3).
// Supported names: intent, clarification-questions, clarification, spec, probe, review, evidence.
func (e *Engine) GetArtifact(ctx context.Context, jobID int64, name string) ([]byte, error) {
	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("factory: get job %d: %w", jobID, err)
	}
	if job.WorktreePath == "" {
		return nil, fmt.Errorf("factory: worktree not found for job %d", jobID)
	}

	filename := name
	switch name {
	case "intent":
		filename = "intent.md"
	case "clarification-questions":
		filename = "clarification-questions.md"
	case "clarification":
		filename = "clarification.md"
	case "spec":
		filename = "spec.md"
	case "probe":
		filename = "probe.md"
	case "review":
		filename = "review.json"
	case "evidence":
		filename = "evidence.md"
	}

	return e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, filename)
}

// GetDiff retrieves the unified diff from the merge base for a job (WKT-3, APR-2).
func (e *Engine) GetDiff(ctx context.Context, jobID int64) (string, error) {
	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return "", fmt.Errorf("factory: get job %d: %w", jobID, err)
	}
	if job.WorktreePath == "" {
		return "", fmt.Errorf("factory: worktree not found for job %d", jobID)
	}

	return e.wtMgr.Diff(ctx, job.WorktreePath, job.BaseSHA)
}

// GetEvidence compiles and returns the structured chain of evidence summary for a job (APR-1..3).
// Pure read: synthesized only from stored artifacts and step records without running any subprocesses.
func (e *Engine) GetEvidence(ctx context.Context, jobID int64) (*EvidenceSummary, error) {
	job, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("factory: get job %d: %w", jobID, err)
	}

	steps, err := e.store.ListStepRunsByJob(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("factory: get step runs for job %d: %w", jobID, err)
	}

	var reviewReport *ReviewReport
	if job.WorktreePath != "" {
		if reviewBytes, err := e.wtMgr.ReadArtifact(ctx, job.WorktreePath, job.ID, "review.json"); err == nil {
			reviewReport, _ = ValidateReviewJSON(reviewBytes)
		}
	}

	var diffStat *DiffStat
	if job.WorktreePath != "" {
		if diffText, err := e.wtMgr.Diff(ctx, job.WorktreePath, job.BaseSHA); err == nil {
			diffStat = ParseDiffStat(diffText)
		}
	}

	summary, _ := BuildEvidence(job, steps, reviewReport, diffStat)
	return summary, nil
}
