// Package main is the entry point and dependency wiring root for Garagefab.
//
// factory_store.go implements the Hexagonal Architecture Adapter pattern.
// In our architecture rules, 'internal/factory' (the domain layer) is strictly
// forbidden from importing 'internal/store' (the SQL persistence layer).
// Instead, factory defines abstract interfaces:
//   - factory.JobStore
//   - factory.StoreTx
//
// This file provides concrete implementations (factoryStoreAdapter and factoryStoreTxAdapter)
// that bridge store.DB and store.Tx to those factory interfaces, performing model mappings.
package main

import (
	"context"

	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/store"
)

// factoryStoreAdapter implements factory.JobStore by wrapping a concrete *store.DB instance.
type factoryStoreAdapter struct {
	db *store.DB
}

// newFactoryStoreAdapter constructs a new adapter instance.
func newFactoryStoreAdapter(db *store.DB) *factoryStoreAdapter {
	return &factoryStoreAdapter{db: db}
}

// GetJob fetches a job by ID from SQLite and maps it to the domain model factory.Job.
func (a *factoryStoreAdapter) GetJob(ctx context.Context, id int64) (*factory.Job, error) {
	j, err := a.db.Jobs().GetJob(ctx, id)
	if err != nil {
		return nil, err
	}
	return toFactoryJob(j), nil
}

// GetProject fetches a project by ID and maps it to factory.Project.
func (a *factoryStoreAdapter) GetProject(ctx context.Context, id int64) (*factory.Project, error) {
	p, err := a.db.Projects().GetProject(ctx, id)
	if err != nil {
		return nil, err
	}
	return toFactoryProject(p), nil
}

// GetNextQueuedJob retrieves the oldest queued job (FIFO scheduling, spec SCH-3).
func (a *factoryStoreAdapter) GetNextQueuedJob(ctx context.Context) (*factory.Job, error) {
	j, err := a.db.Jobs().GetNextQueuedJob(ctx)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, nil // No jobs waiting in queue.
	}
	return toFactoryJob(j), nil
}

// ListQueuedJobs retrieves queued jobs up to limit (FIFO scheduling, spec SCH-3).
func (a *factoryStoreAdapter) ListQueuedJobs(ctx context.Context, limit int) ([]*factory.Job, error) {
	storeJobs, err := a.db.Jobs().ListQueuedJobs(ctx, limit)
	if err != nil {
		return nil, err
	}
	var jobs []*factory.Job
	for _, sj := range storeJobs {
		jobs = append(jobs, toFactoryJob(sj))
	}
	return jobs, nil
}

// CountRunningJobs returns the total number of currently running jobs across all projects.
func (a *factoryStoreAdapter) CountRunningJobs(ctx context.Context) (int, error) {
	return a.db.Jobs().CountRunningJobs(ctx)
}

// CountRunningJobsByProject returns running jobs count for a specific project.
func (a *factoryStoreAdapter) CountRunningJobsByProject(ctx context.Context, projectID int64) (int, error) {
	return a.db.Jobs().CountRunningJobsByProject(ctx, projectID)
}

// CreateStepRun inserts a new step execution record into the database.
func (a *factoryStoreAdapter) CreateStepRun(ctx context.Context, step *factory.StepRun) error {
	storeStep := &store.StepRun{
		JobID:           step.JobID,
		Stage:           step.Stage,
		Kind:            step.Kind,
		Attempt:         step.Attempt,
		Executor:        step.Executor,
		Status:          step.Status,
		FailureCategory: step.FailureCategory,
		ExitCode:        step.ExitCode,
		LogPath:         step.LogPath,
		StartedAt:       step.StartedAt,
		EndedAt:         step.EndedAt,
	}
	if err := a.db.StepRuns().CreateStepRun(ctx, storeStep); err != nil {
		return err
	}
	step.ID = storeStep.ID
	return nil
}

// UpdateStepRun updates the outcome, exit code, and timestamps of a completed step run.
func (a *factoryStoreAdapter) UpdateStepRun(ctx context.Context, step *factory.StepRun) error {
	storeStep := &store.StepRun{
		ID:              step.ID,
		JobID:           step.JobID,
		Stage:           step.Stage,
		Kind:            step.Kind,
		Attempt:         step.Attempt,
		Executor:        step.Executor,
		Status:          step.Status,
		FailureCategory: step.FailureCategory,
		ExitCode:        step.ExitCode,
		LogPath:         step.LogPath,
		StartedAt:       step.StartedAt,
		EndedAt:         step.EndedAt,
	}
	return a.db.StepRuns().UpdateStepRun(ctx, storeStep)
}

// CreateProcessRecord tracks an active OS process (PID/PGID) for crash recovery (spec RCV-1).
func (a *factoryStoreAdapter) CreateProcessRecord(ctx context.Context, stepRunID int64, pid, pgid int, startTime int64) error {
	rec := &store.ProcessRecord{
		StepRunID: stepRunID,
		PID:       pid,
		PGID:      pgid,
		StartTime: startTime,
		Active:    true,
	}
	return a.db.ProcessRecords().CreateProcessRecord(ctx, rec)
}

// MarkProcessInactive flags a process record as inactive when its process completes.
func (a *factoryStoreAdapter) MarkProcessInactive(ctx context.Context, processRecordID int64) error {
	return a.db.ProcessRecords().MarkProcessInactive(ctx, processRecordID)
}

// InTx executes the provided callback within an atomic SQLite database transaction.
// Implements spec PIP-2: State transition and event log are written in one atomic commit.
func (a *factoryStoreAdapter) InTx(ctx context.Context, fn func(tx factory.StoreTx) error) error {
	return a.db.WithTx(ctx, func(stx *store.Tx) error {
		txAdapter := &factoryStoreTxAdapter{stx: stx}
		return fn(txAdapter)
	})
}

// factoryStoreTxAdapter wraps a transaction handle (*store.Tx) to fulfill factory.StoreTx.
type factoryStoreTxAdapter struct {
	stx *store.Tx
}

// UpdateJobState atomically modifies a job's stage and status.
func (t *factoryStoreTxAdapter) UpdateJobState(ctx context.Context, jobID int64, stage, status string) error {
	return t.stx.Jobs().UpdateJobState(ctx, jobID, stage, status)
}

// UpdateJobWorktree updates the Git worktree path, branch name, and base commit SHA.
func (t *factoryStoreTxAdapter) UpdateJobWorktree(ctx context.Context, jobID int64, worktreePath, branchName, baseSHA string) error {
	j, err := t.stx.Jobs().GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	j.WorktreePath = worktreePath
	j.BranchName = branchName
	j.BaseSHA = baseSHA
	return t.stx.Jobs().UpdateJob(ctx, j)
}

// UpdateJobHead updates the head commit SHA on the job record.
func (t *factoryStoreTxAdapter) UpdateJobHead(ctx context.Context, jobID int64, headSHA string) error {
	j, err := t.stx.Jobs().GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	j.HeadSHA = headSHA
	return t.stx.Jobs().UpdateJob(ctx, j)
}

// RecordEvent appends an audit/lifecycle event to the events table within the transaction.
func (t *factoryStoreTxAdapter) RecordEvent(ctx context.Context, jobID int64, eventType string, payload string) error {
	e := &store.Event{
		JobID:   jobID,
		Type:    eventType,
		Payload: payload,
	}
	return t.stx.Events().CreateEvent(ctx, e)
}

// RecordApproval inserts a human approval gate decision record into the database.
func (t *factoryStoreTxAdapter) RecordApproval(ctx context.Context, a *factory.Approval) error {
	storeApproval := &store.Approval{
		JobID:    a.JobID,
		Gate:     a.Gate,
		Decision: a.Decision,
		Note:     a.Note,
		HeadSHA:  a.HeadSHA,
	}
	return t.stx.Approvals().CreateApproval(ctx, storeApproval)
}

// toFactoryJob maps a persistence model (store.Job) to the domain model (factory.Job).
func toFactoryJob(j *store.Job) *factory.Job {
	return &factory.Job{
		ID:           j.ID,
		ProjectID:    j.ProjectID,
		WorkType:     j.WorkType,
		Title:        j.Title,
		Intent:       j.Intent,
		Stage:        j.Stage,
		Status:       j.Status,
		BranchName:   j.BranchName,
		WorktreePath: j.WorktreePath,
		BaseSHA:      j.BaseSHA,
		HeadSHA:      j.HeadSHA,
		PRURL:        j.PRURL,
		CreatedAt:    j.CreatedAt,
		UpdatedAt:    j.UpdatedAt,
	}
}

// toFactoryProject maps a persistence model (store.Project) to the domain model (factory.Project).
func toFactoryProject(p *store.Project) *factory.Project {
	return &factory.Project{
		ID:               p.ID,
		Name:             p.Name,
		RepoPath:         p.RepoPath,
		BaseRef:          p.BaseRef,
		EnabledWorkTypes: p.EnabledWorkTypes,
	}
}
