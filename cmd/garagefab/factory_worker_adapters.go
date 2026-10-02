// Package main (cmd/garagefab) is the application entry point and composition root.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Hexagonal Architecture (Ports & Adapters) — Inversion of Control (IoC).
//
// In Clean/Hexagonal Architecture:
//  1. The Core Domain (`internal/factory`) defines "Driving/Inbound Ports" (e.g. Engine, Scheduler)
//     and "Driven/Outbound Ports" (interfaces for external services it requires).
//  2. The Infrastructure packages (`internal/worker/worktree`, `agent`, `command`) contain
//     the concrete OS/Git execution logic.
//  3. Strict Boundary Rule: `internal/factory` is strictly forbidden from importing `worker`.
//     Likewise, `worker` does not know anything about `factory`.
//  4. This file (`factory_worker_adapters.go`) acts as the bridge (the Adapter layer). It
//     lives in `cmd/garagefab` (the composition root) and wraps worker types so they satisfy
//     the interfaces expected by `factory`.
//
// JAVA / ENTERPRISE CONCEPT BRIDGE:
//   - Java Spring equivalent:
//     In Spring Boot, `factory` would define `WorktreePort` interface in package `domain.ports`.
//     `worker` would provide `GitWorktreeService`.
//     This file would be a `@Component` class `WorktreeAdapter implements WorktreePort`
//     that injects `GitWorktreeService` via constructor and translates DTOs between layers.
//   - In Go, there is no `implements` keyword. Any struct that implements all the methods of
//     an interface automatically satisfies that interface (Structural Subtyping / "Duck Typing").
//
// ==============================================================================
package main

import (
	"context"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/worker/agent"
	"github.com/garagefab/garagefab/internal/worker/command"
	"github.com/garagefab/garagefab/internal/worker/worktree"
)

// ------------------------------------------------------------------------------
// Worktree Adapter
// ------------------------------------------------------------------------------

// factoryWorktreeAdapter adapts the worker's *worktree.Manager to satisfy
// the factory.WorktreeManager interface.
//
// Go Concept: Composition over Inheritance.
// Instead of extending a base class, `factoryWorktreeAdapter` holds a pointer
// to `worktree.Manager` as a struct field.
type factoryWorktreeAdapter struct {
	mgr *worktree.Manager
}

// newFactoryWorktreeAdapter creates a new adapter wrapping the given worktree manager.
//
// Go Concept: Constructor idiom.
// Go has no class constructors. A package function named `new...` or `New...`
// that returns a pointer to the struct is the universal idiom.
func newFactoryWorktreeAdapter(mgr *worktree.Manager) *factoryWorktreeAdapter {
	return &factoryWorktreeAdapter{mgr: mgr}
}

// Create provisions an isolated git worktree branch for a job.
// It maps the worker's concrete `worktree.WorktreeInfo` into factory's domain `factory.WorktreeInfo`.
func (a *factoryWorktreeAdapter) Create(ctx context.Context, repoPath, projectName string, jobID int64, baseRef string) (*factory.WorktreeInfo, error) {
	// Call underlying worker method
	info, err := a.mgr.Create(ctx, repoPath, projectName, jobID, baseRef)
	if err != nil {
		return nil, err
	}
	// Translate DTO (Data Transfer Object) from worker domain to factory domain
	return &factory.WorktreeInfo{
		Path:    info.Path,
		Branch:  info.Branch,
		BaseSHA: info.BaseSHA,
	}, nil
}

// Checkpoint stages all changes and creates an automated git commit with the given message.
func (a *factoryWorktreeAdapter) Checkpoint(ctx context.Context, worktreePath string, jobID int64, message string) (string, error) {
	return a.mgr.Checkpoint(ctx, worktreePath, jobID, message)
}

// Reset discards uncommitted/committed changes in the worktree back to targetSHA.
func (a *factoryWorktreeAdapter) Reset(ctx context.Context, worktreePath, targetSHA string) error {
	return a.mgr.Reset(ctx, worktreePath, targetSHA)
}

// Remove cleanly unregisters and deletes the worktree directory from disk.
func (a *factoryWorktreeAdapter) Remove(ctx context.Context, repoPath, worktreePath, branchName string, deleteBranch bool) error {
	return a.mgr.Remove(ctx, repoPath, worktreePath, branchName, deleteBranch)
}

// Diff returns git diff output comparing the worktree state against baseSHA.
func (a *factoryWorktreeAdapter) Diff(ctx context.Context, worktreePath, baseSHA string) (string, error) {
	return a.mgr.Diff(ctx, worktreePath, baseSHA)
}

// HeadSHA returns the commit hash of the current HEAD in the worktree.
func (a *factoryWorktreeAdapter) HeadSHA(ctx context.Context, worktreePath string) (string, error) {
	return a.mgr.HeadSHA(ctx, worktreePath)
}

// WriteArtifact writes an artifact file inside the job's dedicated artifact directory (LOG-3).
func (a *factoryWorktreeAdapter) WriteArtifact(ctx context.Context, worktreePath string, jobID int64, filename string, content []byte) error {
	return a.mgr.WriteArtifact(ctx, worktreePath, jobID, filename, content)
}

// ReadArtifact reads an artifact file from the job's dedicated artifact directory (LOG-3).
func (a *factoryWorktreeAdapter) ReadArtifact(ctx context.Context, worktreePath string, jobID int64, filename string) ([]byte, error) {
	return a.mgr.ReadArtifact(ctx, worktreePath, jobID, filename)
}

// RemoveArtifact removes an artifact file from the job's dedicated artifact directory (LOG-3).
func (a *factoryWorktreeAdapter) RemoveArtifact(ctx context.Context, worktreePath string, jobID int64, filename string) error {
	return a.mgr.RemoveArtifact(ctx, worktreePath, jobID, filename)
}

// ListArtifacts returns a list of relative artifact file names in the job's artifact directory (LOG-3).
func (a *factoryWorktreeAdapter) ListArtifacts(ctx context.Context, worktreePath string, jobID int64) ([]string, error) {
	return a.mgr.ListArtifacts(ctx, worktreePath, jobID)
}

// ------------------------------------------------------------------------------
// Agent Runner Adapter
// ------------------------------------------------------------------------------

// factoryAgentAdapter adapts worker's agent.Runner to satisfy factory.AgentRunner.
//
// Go Concept: Interface wrapping.
// Notice that agent.Runner is already an interface in worker/agent. We wrap it
// here to translate the request/response structs between the two packages without
// introducing a direct dependency between factory and worker.
type factoryAgentAdapter struct {
	runner agent.Runner
}

// newFactoryAgentAdapter constructs a new agent adapter.
func newFactoryAgentAdapter(runner agent.Runner) *factoryAgentAdapter {
	return &factoryAgentAdapter{runner: runner}
}

// Run executes an AI agent prompt against the job's worktree.
// It maps factory.AgentRequest -> agent.AgentRequest and agent.AgentResult -> factory.AgentResult.
func (a *factoryAgentAdapter) Run(ctx context.Context, req factory.AgentRequest) (*factory.AgentResult, error) {
	// DTO translation: Factory -> Worker
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
	// DTO translation: Worker -> Factory
	return &factory.AgentResult{
		ExitCode:     res.ExitCode,
		ArtifactPath: res.ArtifactPath,
		Summary:      res.Summary,
	}, nil
}

// ------------------------------------------------------------------------------
// Command Runner Adapter
// ------------------------------------------------------------------------------

// factoryCommandAdapter adapts *command.Runner to satisfy factory.CommandRunner.
type factoryCommandAdapter struct {
	runner *command.Runner
}

// newFactoryCommandAdapter constructs a new command adapter.
func newFactoryCommandAdapter(runner *command.Runner) *factoryCommandAdapter {
	return &factoryCommandAdapter{runner: runner}
}

// Run executes a shell command with guardrails and captures stdout/stderr.
// It maps factory.CommandOptions -> command.RunOptions and command.RunResult -> factory.CommandResult.
func (a *factoryCommandAdapter) Run(ctx context.Context, opts factory.CommandOptions) (*factory.CommandResult, error) {
	// DTO translation: Factory -> Worker
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
	// DTO translation: Worker -> Factory
	return &factory.CommandResult{
		ExitCode: res.ExitCode,
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
	}, nil
}

// ------------------------------------------------------------------------------
// Guardrail Adapter
// ------------------------------------------------------------------------------

// factoryGuardrailAdapter adapts command package guardrail functions to satisfy factory.GuardrailRunner.
type factoryGuardrailAdapter struct{}

func newFactoryGuardrailAdapter() *factoryGuardrailAdapter {
	return &factoryGuardrailAdapter{}
}

// CheckProtectedPaths evaluates git diff status against configured protected path globs (GRD-1).
func (a *factoryGuardrailAdapter) CheckProtectedPaths(ctx context.Context, workDir, stepStartSHA string, patterns []string) ([]factory.GuardrailViolation, error) {
	violations, err := command.CheckProtectedPaths(ctx, workDir, stepStartSHA, patterns)
	if err != nil {
		return nil, err
	}
	var res []factory.GuardrailViolation
	for _, v := range violations {
		res = append(res, factory.GuardrailViolation{
			Path:   v.Path,
			Status: v.Status,
		})
	}
	return res, nil
}

// ------------------------------------------------------------------------------
// Project Config Provider Adapter
// ------------------------------------------------------------------------------

// factoryProjectConfigAdapter loads project configuration files (project.yaml)
// and maps them into factory domain ProjectConfig objects (architecture.md §14).
type factoryProjectConfigAdapter struct{}

func newFactoryProjectConfigAdapter() *factoryProjectConfigAdapter {
	return &factoryProjectConfigAdapter{}
}

func (a *factoryProjectConfigAdapter) GetProjectConfig(ctx context.Context, repoPath string) (*factory.ProjectConfig, error) {
	yamlCfg, err := config.LoadProjectConfig(repoPath)
	if err != nil {
		return nil, err
	}
	return &factory.ProjectConfig{
		BaseRef: yamlCfg.BaseRef,
		Commands: factory.ProjectCommands{
			Build: yamlCfg.Commands.Build,
			Test:  yamlCfg.Commands.Test,
			Lint:  yamlCfg.Commands.Lint,
		},
		Guardrails: factory.ProjectGuardrails{
			ProtectedPaths: yamlCfg.Guardrails.ProtectedPaths,
			Commands:       yamlCfg.Guardrails.Commands,
		},
		MaxConcurrentJobs: yamlCfg.MaxConcurrentJobs,
	}, nil
}
