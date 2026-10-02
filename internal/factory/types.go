// Package factory contains the core domain model, pipeline engine, and scheduler
// for the software factory.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Core Domain & Ports (Hexagonal / Clean Architecture).
//
//  1. Dependency Rule:
//     `factory` represents the pure business domain of Garagefab.
//     Under the architectural rules of the project (enforced by `internal/boundaries_test.go`),
//     `factory` is strictly forbidden from importing:
//     - `database/sql` or `internal/store` (storage details)
//     - `internal/worker` (OS process / Git details)
//     - `internal/server` (HTTP / API details)
//
//  2. Driven Ports (Interfaces):
//     `factory` declares the interfaces it needs to do its job:
//     - `WorktreeManager`: Interface for Git worktree isolation and checkpoints.
//     - `AgentRunner`: Interface for executing AI coding models.
//     - `CommandRunner`: Interface for executing test/build shell commands.
//     - `Store` & `StoreTx`: Interface for transactional database persistence.
//
// JAVA / SPRING / DDD COMPARISON:
// - `types.go` is the exact equivalent of Java DDD packages:
//   - `com.garagefab.domain.model` (Job, Project, StepRun, Approval, Event)
//   - `com.garagefab.domain.port.out` (Store, WorktreeManager, AgentRunner)
//   - Go's implicit interface satisfaction allows outside adapters in `cmd/garagefab`
//     to wire concrete infrastructure beans into these domain ports without circular dependencies.
//
// ==============================================================================
package factory

import (
	"context"
	"time"
)

// Pipeline stages (PIP-1) defining the progression of work through the factory.
const (
	StageIntent               = "01_Intent"                 // Raw problem description / ticket intake
	StageClarificationAndSpec = "02_Clarification_and_Spec" // AI clarification interview & generated specification
	StageFailingProbe         = "03_Failing_Probe"          // TDD probe reproducing the bug or proving feature absence
	StageCoding               = "04_Coding"                 // AI agent writing code and tests in worktree
	StageIndependentReview    = "05_Independent_Review"     // Separate critic agent reviewing diff and risk
	StageHumanApprovalGate    = "06_Human_Approval_Gate"    // Human engineer reviews diff, logs, and evidence
	StageDone                 = "07_Done"                   // Terminal success: delivered via merge or PR
)

// Job statuses representing the lifecycle state of a job within a stage.
const (
	StatusQueued             = "queued"              // Waiting for an available concurrency slot in the scheduler
	StatusRunning            = "running"             // Actively being executed by an agent or command
	StatusNeedsClarification = "needs_clarification" // Blocked awaiting human answer to clarification questions
	StatusSpecReview         = "spec_review"         // Blocked awaiting human sign-off on generated spec
	StatusAwaitingApproval   = "awaiting_approval"   // Blocked at human gate awaiting final approve/reject
	StatusInterrupted        = "interrupted"         // Daemon shut down while job was running (needs recovery)
	StatusFailed             = "failed"              // Terminal failure (all repairs exhausted or blocked)
	StatusCancelled          = "cancelled"           // Terminated by user request
	StatusDone               = "done"                // Terminal success
)

// Step kinds and execution statuses.
const (
	StepKindAgent   = "agent"   // Step executed by an AI agent runner
	StepKindCommand = "command" // Step executed as a shell command (e.g. test runner)
	StepKindGate    = "gate"    // Human decision checkpoint

	StepStatusRunning = "running"
	StepStatusSuccess = "success"
	StepStatusFail    = "fail"
	StepStatusSkipped = "skipped"
)

// Failure categories for automated repair and diagnostic classification.
const (
	FailureFlawed  = "Flawed"  // Code error or test failure that the AI agent can attempt to repair
	FailureBlocked = "Blocked" // External dependency missing, network down, or unfixable environment issue
	FailureManual  = "Manual"  // Requires human intervention
)

// Work types categorizing incoming development tasks.
const (
	WorkTypeBugFix   = "bug_fix"
	WorkTypeFeature  = "feature"
	WorkTypeRefactor = "refactor"
	WorkTypeDocs     = "docs"
)

// Gates and decisions for human approvals.
const (
	ApprovalGateSpecReview = "spec_review"
	ApprovalGateFinal      = "final"

	ApprovalDecisionApprove = "approve"
	ApprovalDecisionReject  = "reject"
)

// ------------------------------------------------------------------------------
// Domain Entities
// ------------------------------------------------------------------------------

// Job is the central aggregate entity representing a unit of work flowing through the pipeline.
type Job struct {
	ID           int64     `json:"id"`
	ProjectID    int64     `json:"project_id"`
	WorkType     string    `json:"work_type"`
	Title        string    `json:"title"`
	Intent       string    `json:"intent"`
	Stage        string    `json:"stage"`
	Status       string    `json:"status"`
	BranchName   string    `json:"branch_name"`
	WorktreePath string    `json:"worktree_path"`
	BaseSHA      string    `json:"base_sha"`
	HeadSHA      string    `json:"head_sha"`
	PRURL        string    `json:"pr_url"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Project represents a registered Git code repository managed by Garagefab.
type Project struct {
	ID               int64    `json:"id"`
	Name             string   `json:"name"`
	RepoPath         string   `json:"repo_path"`
	BaseRef          string   `json:"base_ref"`
	EnabledWorkTypes []string `json:"enabled_work_types"`
}

// StepRun records the execution attempt of an individual pipeline step.
type StepRun struct {
	ID              int64      `json:"id"`
	JobID           int64      `json:"job_id"`
	Stage           string     `json:"stage"`
	Kind            string     `json:"kind"`
	Attempt         int        `json:"attempt"`
	Executor        string     `json:"executor"`
	Status          string     `json:"status"`
	FailureCategory string     `json:"failure_category"`
	ExitCode        *int       `json:"exit_code,omitempty"`
	LogPath         string     `json:"log_path"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
}

// Approval records a human engineer's decision (approval or rejection) at a gate.
type Approval struct {
	ID        int64     `json:"id"`
	JobID     int64     `json:"job_id"`
	Gate      string    `json:"gate"`
	Decision  string    `json:"decision"`
	Note      string    `json:"note"`
	HeadSHA   string    `json:"head_sha"`
	CreatedAt time.Time `json:"created_at"`
}

// Event represents an append-only audit log entry for state transitions and observability.
type Event struct {
	ID        int64     `json:"id"`
	JobID     int64     `json:"job_id"`
	Type      string    `json:"type"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

// ------------------------------------------------------------------------------
// Outbound Ports (Driven Interfaces)
// ------------------------------------------------------------------------------

// WorktreeInfo contains metadata returned upon Git worktree creation.
type WorktreeInfo struct {
	Path    string // Worktree filesystem path
	Branch  string // Created Git branch name
	BaseSHA string // Base commit SHA
}

// WorktreeManager defines the outbound port for Git worktree lifecycle management.
type WorktreeManager interface {
	Create(ctx context.Context, repoPath, projectName string, jobID int64, baseRef string) (*WorktreeInfo, error)
	Checkpoint(ctx context.Context, worktreePath string, jobID int64, message string) (string, error)
	Reset(ctx context.Context, worktreePath, targetSHA string) error
	Remove(ctx context.Context, repoPath, worktreePath, branchName string, deleteBranch bool) error
	Diff(ctx context.Context, worktreePath, baseSHA string) (string, error)
	HeadSHA(ctx context.Context, worktreePath string) (string, error)
}

// AgentRequest specifies parameters for invoking an AI coding agent.
type AgentRequest struct {
	JobID          int64
	Stage          string
	WorktreePath   string
	Prompt         string
	ProjectName    string
	LogPath        string
	OnProcessStart func(pid, pgid int, startTime int64)
}

// AgentResult represents the output of an AI coding agent execution.
type AgentResult struct {
	ExitCode     int
	ArtifactPath string
	Summary      string
}

// AgentRunner defines the outbound port for AI agent execution.
type AgentRunner interface {
	Run(ctx context.Context, req AgentRequest) (*AgentResult, error)
}

// CommandOptions specifies parameters for executing a verification shell command.
type CommandOptions struct {
	WorkDir        string
	Command        string
	LogPath        string
	OnProcessStart func(pid, pgid int, startTime int64)
}

// CommandResult represents the output of a shell command execution.
type CommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// CommandRunner defines the outbound port for shell command execution.
type CommandRunner interface {
	Run(ctx context.Context, opts CommandOptions) (*CommandResult, error)
}

// Store defines persistence operations required by the factory engine.
// Notice that this interface is completely decoupled from database/sql.
type Store interface {
	GetJob(ctx context.Context, id int64) (*Job, error)
	GetProject(ctx context.Context, id int64) (*Project, error)
	GetNextQueuedJob(ctx context.Context) (*Job, error)
	ListQueuedJobs(ctx context.Context, limit int) ([]*Job, error)
	CountRunningJobs(ctx context.Context) (int, error)
	CountRunningJobsByProject(ctx context.Context, projectID int64) (int, error)
	CreateStepRun(ctx context.Context, step *StepRun) error
	UpdateStepRun(ctx context.Context, step *StepRun) error
	CreateProcessRecord(ctx context.Context, stepRunID int64, pid, pgid int, startTime int64) error
	MarkProcessInactive(ctx context.Context, processRecordID int64) error

	// InTx executes atomic state mutations inside a database transaction (PIP-2).
	InTx(ctx context.Context, fn func(tx StoreTx) error) error
}

// StoreTx provides transactional write operations within an active database transaction (PIP-2).
// If `fn` returns an error, the transaction rolls back; if nil, it commits.
type StoreTx interface {
	UpdateJobState(ctx context.Context, jobID int64, stage, status string) error
	UpdateJobWorktree(ctx context.Context, jobID int64, worktreePath, branchName, baseSHA string) error
	UpdateJobHead(ctx context.Context, jobID int64, headSHA string) error
	RecordEvent(ctx context.Context, jobID int64, eventType string, payload string) error
	RecordApproval(ctx context.Context, a *Approval) error
}

// GuardrailViolation represents an existing protected file that was modified or deleted (GRD-1).
type GuardrailViolation struct {
	Path   string `json:"path"`
	Status string `json:"status"` // "M", "D", "R"
}

// GuardrailRunner defines the outbound port for validating workspace integrity and path restrictions (GRD-1..4).
type GuardrailRunner interface {
	CheckProtectedPaths(ctx context.Context, workDir, stepStartSHA string, patterns []string) ([]GuardrailViolation, error)
}

// ProjectCommands defines test/build/lint verification commands for a project (COD-2, COD-3).
type ProjectCommands struct {
	Build []string `yaml:"build"`
	Test  []string `yaml:"test"`
	Lint  []string `yaml:"lint"`
}

// ProjectGuardrails defines custom path patterns and verification scripts (GRD-1, GRD-3).
type ProjectGuardrails struct {
	ProtectedPaths []string `yaml:"protected_paths"`
	Commands       []string `yaml:"commands"`
}

// ProjectConfig defines per-project configuration loaded from `<repo>/.garagefab/project.yaml`.
type ProjectConfig struct {
	BaseRef           string            `yaml:"base_ref"`
	Commands          ProjectCommands   `yaml:"commands"`
	Guardrails        ProjectGuardrails `yaml:"guardrails"`
	MaxConcurrentJobs int               `yaml:"max_concurrent_jobs"`
}

// ProjectConfigProvider defines the outbound port to load per-project configuration.
type ProjectConfigProvider interface {
	GetProjectConfig(ctx context.Context, repoPath string) (*ProjectConfig, error)
}
