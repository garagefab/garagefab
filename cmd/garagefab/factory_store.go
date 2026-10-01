package main

import (
	"context"

	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/store"
)

type factoryStoreAdapter struct {
	db *store.DB
}

func newFactoryStoreAdapter(db *store.DB) *factoryStoreAdapter {
	return &factoryStoreAdapter{db: db}
}

func (a *factoryStoreAdapter) GetJob(ctx context.Context, id int64) (*factory.Job, error) {
	j, err := a.db.Jobs().GetJob(ctx, id)
	if err != nil {
		return nil, err
	}
	return toFactoryJob(j), nil
}

func (a *factoryStoreAdapter) GetProject(ctx context.Context, id int64) (*factory.Project, error) {
	p, err := a.db.Projects().GetProject(ctx, id)
	if err != nil {
		return nil, err
	}
	return toFactoryProject(p), nil
}

func (a *factoryStoreAdapter) GetNextQueuedJob(ctx context.Context) (*factory.Job, error) {
	j, err := a.db.Jobs().GetNextQueuedJob(ctx)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, nil
	}
	return toFactoryJob(j), nil
}

func (a *factoryStoreAdapter) CountRunningJobs(ctx context.Context) (int, error) {
	return a.db.Jobs().CountRunningJobs(ctx)
}

func (a *factoryStoreAdapter) CountRunningJobsByProject(ctx context.Context, projectID int64) (int, error) {
	return a.db.Jobs().CountRunningJobsByProject(ctx, projectID)
}

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

func (a *factoryStoreAdapter) MarkProcessInactive(ctx context.Context, processRecordID int64) error {
	return a.db.ProcessRecords().MarkProcessInactive(ctx, processRecordID)
}

func (a *factoryStoreAdapter) InTx(ctx context.Context, fn func(tx factory.StoreTx) error) error {
	return a.db.WithTx(ctx, func(stx *store.Tx) error {
		txAdapter := &factoryStoreTxAdapter{stx: stx}
		return fn(txAdapter)
	})
}

type factoryStoreTxAdapter struct {
	stx *store.Tx
}

func (t *factoryStoreTxAdapter) UpdateJobState(ctx context.Context, jobID int64, stage, status string) error {
	return t.stx.Jobs().UpdateJobState(ctx, jobID, stage, status)
}

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

func (t *factoryStoreTxAdapter) UpdateJobHead(ctx context.Context, jobID int64, headSHA string) error {
	j, err := t.stx.Jobs().GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	j.HeadSHA = headSHA
	return t.stx.Jobs().UpdateJob(ctx, j)
}

func (t *factoryStoreTxAdapter) RecordEvent(ctx context.Context, jobID int64, eventType string, payload string) error {
	e := &store.Event{
		JobID:   jobID,
		Type:    eventType,
		Payload: payload,
	}
	return t.stx.Events().CreateEvent(ctx, e)
}

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

func toFactoryProject(p *store.Project) *factory.Project {
	return &factory.Project{
		ID:               p.ID,
		Name:             p.Name,
		RepoPath:         p.RepoPath,
		BaseRef:          p.BaseRef,
		EnabledWorkTypes: p.EnabledWorkTypes,
	}
}
