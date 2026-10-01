package factory

import (
	"context"
	"time"
)

// Pipeline stages (PIP-1)
const (
	StageIntent               = "01_Intent"
	StageClarificationAndSpec = "02_Clarification_and_Spec"
	StageFailingProbe         = "03_Failing_Probe"
	StageCoding               = "04_Coding"
	StageIndependentReview    = "05_Independent_Review"
	StageHumanApprovalGate    = "06_Human_Approval_Gate"
	StageDone                 = "07_Done"
)

// Job statuses
const (
	StatusQueued             = "queued"
	StatusRunning            = "running"
	StatusNeedsClarification = "needs_clarification"
	StatusSpecReview         = "spec_review"
	StatusAwaitingApproval   = "awaiting_approval"
	StatusInterrupted        = "interrupted"
	StatusFailed             = "failed"
	StatusCancelled          = "cancelled"
	StatusDone               = "done"
)

// Step kinds and statuses
const (
	StepKindAgent   = "agent"
	StepKindCommand = "command"
	StepKindGate    = "gate"

	StepStatusRunning = "running"
	StepStatusSuccess = "success"
	StepStatusFail    = "fail"
	StepStatusSkipped = "skipped"
)

// Failure categories
const (
	FailureFlawed  = "Flawed"
	FailureBlocked = "Blocked"
	FailureManual  = "Manual"
)

// Work types
const (
	WorkTypeBugFix   = "bug_fix"
	WorkTypeFeature  = "feature"
	WorkTypeRefactor = "refactor"
	WorkTypeDocs     = "docs"
)

// Gates and decisions
const (
	ApprovalGateSpecReview = "spec_review"
	ApprovalGateFinal      = "final"

	ApprovalDecisionApprove = "approve"
	ApprovalDecisionReject  = "reject"
)

// Job domain model for factory engine.
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

// Project domain model for factory engine.
type Project struct {
	ID               int64    `json:"id"`
	Name             string   `json:"name"`
	RepoPath         string   `json:"repo_path"`
	BaseRef          string   `json:"base_ref"`
	EnabledWorkTypes []string `json:"enabled_work_types"`
}

// StepRun domain model for factory engine.
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

// Approval domain model for factory engine.
type Approval struct {
	ID        int64     `json:"id"`
	JobID     int64     `json:"job_id"`
	Gate      string    `json:"gate"`
	Decision  string    `json:"decision"`
	Note      string    `json:"note"`
	HeadSHA   string    `json:"head_sha"`
	CreatedAt time.Time `json:"created_at"`
}

// Event domain model for factory engine.
type Event struct {
	ID        int64     `json:"id"`
	JobID     int64     `json:"job_id"`
	Type      string    `json:"type"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

// WorktreeInfo contains worktree creation results.
type WorktreeInfo struct {
	Path    string
	Branch  string
	BaseSHA string
}

// WorktreeManager defines worktree operations required by the factory.
type WorktreeManager interface {
	Create(ctx context.Context, repoPath, projectName string, jobID int64, baseRef string) (*WorktreeInfo, error)
	Checkpoint(ctx context.Context, worktreePath string, jobID int64, message string) (string, error)
	Reset(ctx context.Context, worktreePath, targetSHA string) error
	Remove(ctx context.Context, repoPath, worktreePath, branchName string, deleteBranch bool) error
	Diff(ctx context.Context, worktreePath, baseSHA string) (string, error)
	HeadSHA(ctx context.Context, worktreePath string) (string, error)
}

// AgentRequest specifies parameters for agent execution.
type AgentRequest struct {
	JobID          int64
	Stage          string
	WorktreePath   string
	Prompt         string
	ProjectName    string
	LogPath        string
	OnProcessStart func(pid, pgid int, startTime int64)
}

// AgentResult represents the result of agent execution.
type AgentResult struct {
	ExitCode     int
	ArtifactPath string
	Summary      string
}

// AgentRunner executes agent steps.
type AgentRunner interface {
	Run(ctx context.Context, req AgentRequest) (*AgentResult, error)
}

// CommandOptions specifies parameters for command execution.
type CommandOptions struct {
	WorkDir        string
	Command        string
	LogPath        string
	OnProcessStart func(pid, pgid int, startTime int64)
}

// CommandResult represents the result of command execution.
type CommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// CommandRunner executes shell command steps.
type CommandRunner interface {
	Run(ctx context.Context, opts CommandOptions) (*CommandResult, error)
}

// Store defines persistence operations required by factory without depending on database/sql or store package.
type Store interface {
	GetJob(ctx context.Context, id int64) (*Job, error)
	GetProject(ctx context.Context, id int64) (*Project, error)
	GetNextQueuedJob(ctx context.Context) (*Job, error)
	CountRunningJobs(ctx context.Context) (int, error)
	CountRunningJobsByProject(ctx context.Context, projectID int64) (int, error)
	CreateStepRun(ctx context.Context, step *StepRun) error
	UpdateStepRun(ctx context.Context, step *StepRun) error
	CreateProcessRecord(ctx context.Context, stepRunID int64, pid, pgid int, startTime int64) error
	MarkProcessInactive(ctx context.Context, processRecordID int64) error

	// InTx executes atomic state mutations with event logging (PIP-2).
	InTx(ctx context.Context, fn func(tx StoreTx) error) error
}

// StoreTx provides operations within a single database transaction (PIP-2).
type StoreTx interface {
	UpdateJobState(ctx context.Context, jobID int64, stage, status string) error
	UpdateJobWorktree(ctx context.Context, jobID int64, worktreePath, branchName, baseSHA string) error
	UpdateJobHead(ctx context.Context, jobID int64, headSHA string) error
	RecordEvent(ctx context.Context, jobID int64, eventType string, payload string) error
	RecordApproval(ctx context.Context, a *Approval) error
}
