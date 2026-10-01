package store

import (
	"errors"
	"time"
)

var (
	// ErrNotFound is returned when a requested record does not exist.
	ErrNotFound = errors.New("store: record not found")
	// ErrProjectNameExists is returned when registering a project with a duplicate name (PRJ-5).
	ErrProjectNameExists = errors.New("store: project name already exists")
	// ErrProjectRepoPathExists is returned when registering a project with a duplicate repo path (PRJ-5).
	ErrProjectRepoPathExists = errors.New("store: project repo path already exists")
	// ErrInvalidState is returned when an action violates state transition rules (PIP-3).
	ErrInvalidState = errors.New("store: invalid state transition")
)

// Work type constants
const (
	WorkTypeBugFix   = "bug_fix"
	WorkTypeFeature  = "feature"
	WorkTypeRefactor = "refactor"
	WorkTypeDocs     = "docs"
)

// Pipeline stage constants (PIP-1)
const (
	StageIntent               = "01_Intent"
	StageClarificationAndSpec = "02_Clarification_and_Spec"
	StageFailingProbe         = "03_Failing_Probe"
	StageCoding               = "04_Coding"
	StageIndependentReview    = "05_Independent_Review"
	StageHumanApprovalGate    = "06_Human_Approval_Gate"
	StageDone                 = "07_Done"
)

// Job status constants (spec §4.3)
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

// Step run kind constants
const (
	StepKindAgent   = "agent"
	StepKindCommand = "command"
	StepKindGate    = "gate"
)

// Step run status constants
const (
	StepStatusRunning = "running"
	StepStatusSuccess = "success"
	StepStatusFail    = "fail"
	StepStatusSkipped = "skipped"
)

// Failure category constants (§9.4)
const (
	FailureFlawed  = "Flawed"
	FailureBlocked = "Blocked"
	FailureManual  = "Manual"
)

// Approval gate constants
const (
	ApprovalGateSpecReview = "spec_review"
	ApprovalGateFinal      = "final"
)

// Approval decision constants
const (
	ApprovalDecisionApprove = "approve"
	ApprovalDecisionReject  = "reject"
)

// Job source constants (INT-1..3)
const (
	SourceDashboard   = "dashboard"
	SourceIntentFile  = "intent_file"
	SourceGitHubIssue = "github_issue"
)

// Project represents a registered Git repository managed by Garagefab (PRJ-1..6).
type Project struct {
	ID               int64     `json:"id"`
	Name             string    `json:"name"`
	RepoPath         string    `json:"repo_path"`
	BaseRef          string    `json:"base_ref"`
	EnabledWorkTypes []string  `json:"enabled_work_types"`
	IsArchived       bool      `json:"is_archived"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Job represents a single unit of work moving through the pipeline (PIP-1..5).
type Job struct {
	ID           int64     `json:"id"`
	ProjectID    int64     `json:"project_id"`
	WorkType     string    `json:"work_type"`
	Title        string    `json:"title"`
	Intent       string    `json:"intent"`
	Source       string    `json:"source"`
	SourceRef    string    `json:"source_ref"`
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

// StepRun represents a single execution of an agent, command, or gate step (LOG-1).
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

// Event represents an immutable log event for live feed and SSE broadcast (LOG-4).
type Event struct {
	ID        int64     `json:"id"`
	JobID     int64     `json:"job_id"`
	Type      string    `json:"type"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

// Approval records a human decision at a review or final approval gate (APR-5..7).
type Approval struct {
	ID        int64     `json:"id"`
	JobID     int64     `json:"job_id"`
	Gate      string    `json:"gate"`
	Decision  string    `json:"decision"`
	Note      string    `json:"note"`
	HeadSHA   string    `json:"head_sha"`
	CreatedAt time.Time `json:"created_at"`
}

// Session tracks authenticated dashboard sessions (SEC-3).
type Session struct {
	ID        string    `json:"id"`
	TokenHash string    `json:"token_hash"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// ProcessRecord tracks active agent/command processes for crash recovery (RCV-1, RCV-2).
type ProcessRecord struct {
	ID        int64     `json:"id"`
	StepRunID int64     `json:"step_run_id"`
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid"`
	StartTime int64     `json:"start_time"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// JobListFilter specifies query parameters for listing jobs.
type JobListFilter struct {
	ProjectID *int64
	Status    *string
	Stage     *string
	Limit     int
	Cursor    int64
}

// JobStatusItem contains summary fields for displaying active jobs (CLI-4).
type JobStatusItem struct {
	ID          int64  `json:"id"`
	ProjectName string `json:"project_name"`
	WorkType    string `json:"work_type"`
	Stage       string `json:"stage"`
	Status      string `json:"status"`
	Title       string `json:"title"`
}
