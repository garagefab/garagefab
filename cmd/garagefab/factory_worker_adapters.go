package main

import (
	"context"

	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/worker/agent"
	"github.com/garagefab/garagefab/internal/worker/command"
	"github.com/garagefab/garagefab/internal/worker/worktree"
)

type factoryWorktreeAdapter struct {
	mgr *worktree.Manager
}

func newFactoryWorktreeAdapter(mgr *worktree.Manager) *factoryWorktreeAdapter {
	return &factoryWorktreeAdapter{mgr: mgr}
}

func (a *factoryWorktreeAdapter) Create(ctx context.Context, repoPath, projectName string, jobID int64, baseRef string) (*factory.WorktreeInfo, error) {
	info, err := a.mgr.Create(ctx, repoPath, projectName, jobID, baseRef)
	if err != nil {
		return nil, err
	}
	return &factory.WorktreeInfo{
		Path:    info.Path,
		Branch:  info.Branch,
		BaseSHA: info.BaseSHA,
	}, nil
}

func (a *factoryWorktreeAdapter) Checkpoint(ctx context.Context, worktreePath string, jobID int64, message string) (string, error) {
	return a.mgr.Checkpoint(ctx, worktreePath, jobID, message)
}

func (a *factoryWorktreeAdapter) Reset(ctx context.Context, worktreePath, targetSHA string) error {
	return a.mgr.Reset(ctx, worktreePath, targetSHA)
}

func (a *factoryWorktreeAdapter) Remove(ctx context.Context, repoPath, worktreePath, branchName string, deleteBranch bool) error {
	return a.mgr.Remove(ctx, repoPath, worktreePath, branchName, deleteBranch)
}

func (a *factoryWorktreeAdapter) Diff(ctx context.Context, worktreePath, baseSHA string) (string, error) {
	return a.mgr.Diff(ctx, worktreePath, baseSHA)
}

func (a *factoryWorktreeAdapter) HeadSHA(ctx context.Context, worktreePath string) (string, error) {
	return a.mgr.HeadSHA(ctx, worktreePath)
}

type factoryAgentAdapter struct {
	runner agent.Runner
}

func newFactoryAgentAdapter(runner agent.Runner) *factoryAgentAdapter {
	return &factoryAgentAdapter{runner: runner}
}

func (a *factoryAgentAdapter) Run(ctx context.Context, req factory.AgentRequest) (*factory.AgentResult, error) {
	workerReq := agent.AgentRequest{
		JobID:          req.JobID,
		Stage:          req.Stage,
		WorktreePath:   req.WorktreePath,
		Prompt:         req.Prompt,
		ProjectName:    req.ProjectName,
		LogPath:        req.LogPath,
		OnProcessStart: req.OnProcessStart,
	}
	res, err := a.runner.Run(ctx, workerReq)
	if err != nil {
		return nil, err
	}
	return &factory.AgentResult{
		ExitCode:     res.ExitCode,
		ArtifactPath: res.ArtifactPath,
		Summary:      res.Summary,
	}, nil
}

type factoryCommandAdapter struct {
	runner *command.Runner
}

func newFactoryCommandAdapter(runner *command.Runner) *factoryCommandAdapter {
	return &factoryCommandAdapter{runner: runner}
}

func (a *factoryCommandAdapter) Run(ctx context.Context, opts factory.CommandOptions) (*factory.CommandResult, error) {
	workerOpts := command.RunOptions{
		WorkDir:        opts.WorkDir,
		Command:        opts.Command,
		LogPath:        opts.LogPath,
		OnProcessStart: opts.OnProcessStart,
	}
	res, err := a.runner.Run(ctx, workerOpts)
	if err != nil {
		return nil, err
	}
	return &factory.CommandResult{
		ExitCode: res.ExitCode,
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
	}, nil
}
