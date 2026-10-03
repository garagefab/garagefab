//go:build realagent

// Package main contains integration tests executing live agy and opencode CLI binaries.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ENTERPRISE / JAVA SPRING COMPARISON:
// Live Process Verification Tests behind Build Tag `realagent`.
//
// In Clean Architecture & SDLC:
// These tests exercise real `agy` and `opencode` CLI subprocesses on the host machine.
// Because they make real LLM invocations and consume API tokens/quotas, they are isolated
// behind the `//go:build realagent` compiler tag and excluded from standard `make ci` / `go test ./...`.
//
// Execution:
//
//	go test -v -tags realagent -run TestRealAgent ./cmd/garagefab
//
// ==============================================================================
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/store"
	"github.com/garagefab/garagefab/internal/worker/agent"
	"github.com/garagefab/garagefab/internal/worker/command"
	"github.com/garagefab/garagefab/internal/worker/worktree"
)

// TestRealAgent_AgyRefactor exercises a live agy CLI refactor job on a scratch repository.
func TestRealAgent_AgyRefactor(t *testing.T) {
	if _, err := exec.LookPath("agy"); err != nil {
		t.Skip("agy binary not found on PATH; skipping realagent test")
	}

	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "repo")
	_ = os.MkdirAll(repoDir, 0755)
	setupGitRepo(t, repoDir)

	// Configure project with agy for all roles
	projectYAML := `base_ref: origin/main
agents:
  spec: agy
  coding: agy
  review: agy
commands:
  build: ["go build ./..."]
  test: ["go test ./..."]
`
	_ = os.WriteFile(filepath.Join(repoDir, ".garagefab", "project.yaml"), []byte(projectYAML), 0644)

	cfgDir := filepath.Join(tempDir, "data")
	cfg, err := config.Load(cfgDir)
	if err != nil {
		t.Fatalf("config load: %v", err)
	}

	dbPath := filepath.Join(cfg.DataDir, "garagefab.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer db.Close()

	proj := &store.Project{
		Name:             "real-agy-project",
		RepoPath:         repoDir,
		BaseRef:          "origin/main",
		EnabledWorkTypes: []string{store.WorkTypeRefactor},
	}
	if err := db.Projects().CreateProject(context.Background(), proj); err != nil {
		t.Fatalf("create project: %v", err)
	}

	agentRouter := agent.NewRouter()
	agentRouter.Register("agy", agent.NewAgyRunner("agy", cfg.Engine.EnvPassthrough))
	agentAdapter := newFactoryAgentAdapter(agentRouter)

	storeAdapter := newFactoryStoreAdapter(db)
	wtMgr := newFactoryWorktreeAdapter(worktree.NewManager(filepath.Join(cfg.DataDir, "worktrees")))
	cmdRunner := newFactoryCommandAdapter(command.NewRunner())
	guardrailAdapter := newFactoryGuardrailAdapter()
	projCfgAdapter := newFactoryProjectConfigAdapter()

	engine := factory.NewEngine(storeAdapter, wtMgr, agentAdapter, cmdRunner, filepath.Join(cfg.DataDir, "logs"))
	engine.SetGuardrailRunner(guardrailAdapter)
	engine.SetProjectConfigProvider(projCfgAdapter)
	engine.SetMaxRepairAttempts(1)
	engine.SetDefaultAgentTimeout(15 * time.Minute)

	job := &store.Job{
		ProjectID: proj.ID,
		WorkType:  store.WorkTypeRefactor,
		Title:     "Refactor Main",
		Intent:    "Add a helper function to main.go that returns a greeting string and test it.",
		Stage:     store.StageCoding,
		Status:    store.StatusQueued,
	}
	if err := db.Jobs().CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	// Refactor work types skip Spec and begin in Stage 04 (Coding)
	if err := engine.ExecuteJob(context.Background(), job.ID); err != nil {
		t.Fatalf("execute refactor job with agy failed: %v", err)
	}

	updatedJob, err := db.Jobs().GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if updatedJob.Stage != store.StageHumanApprovalGate || updatedJob.Status != store.StatusAwaitingApproval {
		t.Errorf("job stage/status = %s/%s, want %s/%s", updatedJob.Stage, updatedJob.Status, store.StageHumanApprovalGate, store.StatusAwaitingApproval)
	}
}

// TestRealAgent_OpenCodeRefactor exercises a live opencode CLI refactor job on a scratch repository.
func TestRealAgent_OpenCodeRefactor(t *testing.T) {
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode binary not found on PATH; skipping realagent test")
	}

	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "repo")
	_ = os.MkdirAll(repoDir, 0755)
	setupGitRepo(t, repoDir)

	// Configure project with opencode for coding and review
	projectYAML := `base_ref: origin/main
agents:
  spec: opencode
  coding: opencode
  review: opencode
commands:
  build: ["go build ./..."]
  test: ["go test ./..."]
`
	_ = os.WriteFile(filepath.Join(repoDir, ".garagefab", "project.yaml"), []byte(projectYAML), 0644)

	cfgDir := filepath.Join(tempDir, "data")
	cfg, err := config.Load(cfgDir)
	if err != nil {
		t.Fatalf("config load: %v", err)
	}

	dbPath := filepath.Join(cfg.DataDir, "garagefab.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer db.Close()

	proj := &store.Project{
		Name:             "real-opencode-project",
		RepoPath:         repoDir,
		BaseRef:          "origin/main",
		EnabledWorkTypes: []string{store.WorkTypeRefactor},
	}
	if err := db.Projects().CreateProject(context.Background(), proj); err != nil {
		t.Fatalf("create project: %v", err)
	}

	agentRouter := agent.NewRouter()
	agentRouter.Register("opencode", agent.NewOpenCodeRunner("opencode", cfg.Engine.EnvPassthrough))
	agentAdapter := newFactoryAgentAdapter(agentRouter)

	storeAdapter := newFactoryStoreAdapter(db)
	wtMgr := newFactoryWorktreeAdapter(worktree.NewManager(filepath.Join(cfg.DataDir, "worktrees")))
	cmdRunner := newFactoryCommandAdapter(command.NewRunner())
	guardrailAdapter := newFactoryGuardrailAdapter()
	projCfgAdapter := newFactoryProjectConfigAdapter()

	engine := factory.NewEngine(storeAdapter, wtMgr, agentAdapter, cmdRunner, filepath.Join(cfg.DataDir, "logs"))
	engine.SetGuardrailRunner(guardrailAdapter)
	engine.SetProjectConfigProvider(projCfgAdapter)
	engine.SetMaxRepairAttempts(1)
	engine.SetDefaultAgentTimeout(15 * time.Minute)

	job := &store.Job{
		ProjectID: proj.ID,
		WorkType:  store.WorkTypeRefactor,
		Title:     "Refactor Main With OpenCode",
		Intent:    "Add a helper function to main.go that returns a greeting string.",
		Stage:     store.StageCoding,
		Status:    store.StatusQueued,
	}
	if err := db.Jobs().CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	if err := engine.ExecuteJob(context.Background(), job.ID); err != nil {
		t.Fatalf("execute refactor job with opencode failed: %v", err)
	}

	updatedJob, err := db.Jobs().GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if updatedJob.Stage != store.StageHumanApprovalGate || updatedJob.Status != store.StatusAwaitingApproval {
		t.Errorf("job stage/status = %s/%s, want %s/%s", updatedJob.Stage, updatedJob.Status, store.StageHumanApprovalGate, store.StatusAwaitingApproval)
	}
}
