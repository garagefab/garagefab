package agent

import "context"

// ProcessStartFunc is called right after process startup to persist process records (RCV-1).
type ProcessStartFunc func(pid, pgid int, startTime int64)

// AgentRequest specifies parameters for executing an agent step.
type AgentRequest struct {
	JobID          int64
	Stage          string
	WorktreePath   string
	Prompt         string
	ProjectName    string
	Env            map[string]string
	LogPath        string
	OnProcessStart ProcessStartFunc
}

// AgentResult represents the outcome of an agent invocation.
type AgentResult struct {
	ExitCode     int
	ArtifactPath string
	Summary      string
}

// Runner is the interface for executing AI coding agents.
type Runner interface {
	Run(ctx context.Context, req AgentRequest) (*AgentResult, error)
}
