// Package intake coordinates background ingestion across registered projects.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Intake Supervisor & Background Poller (INT-2..7, GHB-3).
//
// In Clean / Hexagonal Architecture:
// `poller.go` is the driving supervisor that periodically wakes to ingest work
// from both local intent files and remote GitHub issues across all registered projects.
//
// Concurrency & Operational Invariants:
//  1. Non-Overlapping Execution Guarantee (INT-6): If a poll sweep takes longer
//     than `poll_interval` (e.g. slow network or hundreds of files), subsequent
//     ticks are skipped. Two sweeps never run concurrently.
//  2. Multi-Tenant Project Isolation (INT-5): An error scanning one project
//     does not prevent subsequent projects from being scanned.
//  3. Decoupled Ports: Uses `IssueSource` for issue discovery and `SchedulerNotifier`
//     for reactive factory wake-up (`notifier.Wake()`, INT-7).
//
// ENTERPRISE / JAVA SPRING COMPARISON:
//   - Spring `@Scheduled(fixedDelay = 30000)`: Similar to a Spring scheduled task
//     with fixed delay, where the next run starts only after the previous cycle ends.
//
// GO IDIOMS & CONCEPTS:
//   1. Atomic Compare-And-Swap (`atomic.Bool`):
//      `isPolling.CompareAndSwap(false, true)` provides lock-free, race-free single-flight
//      execution enforcement without lock contention.
//   2. Ticker Select Loop:
//      Standard `time.NewTicker` + `select { case <-ticker.C: ... case <-ctx.Done(): ... }`
//      ensuring graceful cancellation on shutdown.
// ==============================================================================
package intake

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/store"
)

// Poller coordinates background periodic ingestion sweeps across all projects.
type Poller struct {
	db        *store.DB
	source    IssueSource
	feedback  IssueFeedbackReconciler
	notifier  SchedulerNotifier
	interval  time.Duration
	isPolling atomic.Bool
}

// IssueFeedbackReconciler defines an optional hook to synchronize issue state after polling (GHB-2).
type IssueFeedbackReconciler interface {
	Reconcile(ctx context.Context, db *store.DB, project *store.Project, cfg *config.ProjectYAML) error
}

// NewPoller constructs a new background intake supervisor.
func NewPoller(db *store.DB, source IssueSource, notifier SchedulerNotifier, interval time.Duration) *Poller {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Poller{
		db:       db,
		source:   source,
		notifier: notifier,
		interval: interval,
	}
}

// SetFeedbackReconciler attaches the issue feedback reconciler executed at the end of each sweep (GHB-2).
func (p *Poller) SetFeedbackReconciler(r IssueFeedbackReconciler) {
	p.feedback = r
}

// Start begins periodic polling in the foreground until ctx is cancelled.
// Callers typically invoke this in a dedicated goroutine: `go poller.Start(ctx)`.
func (p *Poller) Start(ctx context.Context) {
	// Execute first sweep immediately on startup
	p.PollOnce(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.PollOnce(ctx)
		}
	}
}

// PollOnce executes a single ingestion sweep across all registered projects (INT-6).
func (p *Poller) PollOnce(ctx context.Context) {
	// Non-overlapping guarantee (INT-6): skip tick if previous sweep is still executing
	if !p.isPolling.CompareAndSwap(false, true) {
		slog.Debug("intake: poll sweep already in progress, skipping tick")
		return
	}
	defer p.isPolling.Store(false)

	if p.db == nil {
		return
	}

	projects, err := p.db.Projects().ListProjects(ctx)
	if err != nil {
		slog.Warn("intake: list projects failed", "error", err)
		return
	}

	for _, project := range projects {
		if ctx.Err() != nil {
			return
		}
		if project.IsArchived {
			continue
		}

		p.pollProject(ctx, project)
	}
}

// pollProject executes intent file scanning, issue polling, and feedback reconciliation for a project.
func (p *Poller) pollProject(ctx context.Context, project *store.Project) {
	cfg, err := config.LoadProjectConfig(project.RepoPath)
	if err != nil {
		slog.Warn("intake: load project config failed", "project", project.Name, "error", err)
		return
	}

	// 1. Scan intent files (*-intent.md) (INT-2)
	if err := ScanProjectIntents(ctx, p.db, project, p.notifier); err != nil {
		slog.Warn("intake: scan intents error", "project", project.Name, "error", err)
	}

	// 2. Scan GitHub trigger issues (INT-3, GHB-3)
	if p.source != nil && cfg.GitHub.Repo != "" {
		if err := ScanProjectIssues(ctx, p.db, project, p.source, cfg, p.notifier); err != nil {
			slog.Warn("intake: scan issues error", "project", project.Name, "repo", cfg.GitHub.Repo, "error", err)
		}
	}

	// 3. Reconcile issue status feedback (GHB-2, GHB-5)
	if p.feedback != nil && cfg.GitHub.Repo != "" {
		if err := p.feedback.Reconcile(ctx, p.db, project, cfg); err != nil {
			slog.Warn("intake: feedback reconcile error", "project", project.Name, "repo", cfg.GitHub.Repo, "error", err)
		}
	}
}
