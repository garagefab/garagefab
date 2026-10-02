// Package factory manages job admission and concurrency scheduling.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Work Scheduler & Concurrency Throttling (SCH-1..4).
//
// The Scheduler is responsible for:
// 1. Enforcing global concurrency limits (`max_concurrent_jobs`, default 5).
// 2. Admitting queued jobs in FIFO order (SCH-1, SCH-3).
// 3. Spawning worker goroutines for admitted jobs and managing their lifecycle contexts.
// 4. Reactive wake-up notifications via buffered channels + fallback timer polling.
//
// GO CONCEPTS & JAVA / THREADPOOLEXECUTOR COMPARISON:
//
//  1. Goroutine Pool vs Java ThreadPoolExecutor:
//     In Java: You create an `ExecutorService executor = Executors.newFixedThreadPool(5)`.
//     Tasks are submitted as `Runnable` / `Callable`.
//     In Go: Goroutines are extremely lightweight (2KB stack vs ~1MB in Java). Instead of
//     a fixed OS thread pool, Go systems often use a scheduler loop that monitors DB state,
//     checks capacity, and launches a goroutine per job (`go func(...)`), tracking them
//     with `sync.WaitGroup` and `context.Context`.
//
//  2. Non-Blocking Event Channels (`wakeCh chan struct{}`):
//     `struct{}` is Go's zero-byte type (takes 0 bytes of memory). A channel of `struct{}`
//     is used purely as a signal/notification mechanism.
//     Using a non-blocking select:
//     select {
//     case s.wakeCh <- struct{}{}:
//     default: // If buffer is full, don't block!
//     }
//     This pattern prevents the caller from blocking while ensuring the scheduler wakes up.
//
// ==============================================================================
package factory

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Scheduler manages concurrent job execution according to global concurrency limits (SCH-1..4).
type Scheduler struct {
	store           Store                 // Persistence port for querying queue and running counts
	engine          *Engine               // Pipeline engine for executing admitted jobs
	maxConcurrent   int                   // Maximum concurrent active jobs (SCH-1)
	pollInterval    time.Duration         // Periodic fallback poll interval
	wakeCh          chan struct{}         // Reactive signal channel to trigger immediate scheduling
	activeWg        sync.WaitGroup        // WaitGroup tracking active job goroutines for clean shutdown
	runningJobs     sync.Map              // Map[int64]context.CancelFunc for active job cancellation
	jobProjectMap   sync.Map              // Map[int64]int64 (jobID -> projectID) for per-project tracking
	projCfgProvider ProjectConfigProvider // Optional project config provider for per-project limits (SCH-2)
}

// NewScheduler creates a new scheduler with concurrency limits.
func NewScheduler(store Store, engine *Engine, maxConcurrent int) *Scheduler {
	if maxConcurrent <= 0 {
		maxConcurrent = 5 // default 5 concurrent jobs (SCH-1)
	}
	s := &Scheduler{
		store:         store,
		engine:        engine,
		maxConcurrent: maxConcurrent,
		pollInterval:  500 * time.Millisecond,
		wakeCh:        make(chan struct{}, 10), // Buffered channel for wake signals
	}
	// Connect engine's wake notification directly to scheduler's Wake method
	engine.SetWakeFunc(s.Wake)
	return s
}

// SetProjectConfigProvider registers the project configuration provider for project limits (SCH-2).
func (s *Scheduler) SetProjectConfigProvider(p ProjectConfigProvider) {
	s.projCfgProvider = p
}

func (s *Scheduler) countInMemoryForProject(projectID int64) int {
	var count int
	s.jobProjectMap.Range(func(key, val any) bool {
		if pID, ok := val.(int64); ok && pID == projectID {
			count++
		}
		return true
	})
	return count
}

// Wake notifies the scheduler to inspect the queue immediately without waiting for the ticker.
// It is non-blocking: if a wake signal is already pending, it does nothing.
func (s *Scheduler) Wake() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

// Start runs the scheduler loop until ctx is cancelled.
// It uses a `time.Ticker` combined with `wakeCh` inside a `select` statement.
func (s *Scheduler) Start(ctx context.Context) {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		s.scheduleNext(ctx)

		// Wait for next scheduled poll, a wake signal, or shutdown context
		select {
		case <-ctx.Done():
			// Shutdown signal received: wait for all active job goroutines to exit
			s.activeWg.Wait()
			return
		case <-ticker.C:
		case <-s.wakeCh:
		}
	}
}

// scheduleNext checks running counts and admits queued jobs FIFO (SCH-1, SCH-3).
// It prevents queue starvation if an admitted job is still initializing its worktree before
// its database status transition commits.
func (s *Scheduler) scheduleNext(ctx context.Context) {
	// 1. Query currently running jobs from database
	runningCount, err := s.store.CountRunningJobs(ctx)
	if err != nil {
		slog.Error("scheduler: count running jobs failed", "error", err)
		return
	}

	// Also count jobs currently in-flight in memory whose DB status has not yet transitioned
	var activeInMemoryCount int
	s.runningJobs.Range(func(key, value any) bool {
		activeInMemoryCount++
		return true
	})
	if activeInMemoryCount > runningCount {
		runningCount = activeInMemoryCount
	}

	// 2. Calculate remaining execution capacity (SCH-1)
	availableSlots := s.maxConcurrent - runningCount
	if availableSlots <= 0 {
		return // At maximum concurrency capacity
	}

	// 3. Fetch candidate queued jobs in FIFO order (SCH-3)
	// We query extra candidates (availableSlots + activeInMemoryCount) in case earlier candidates
	// are already tracked as in-flight in memory.
	candidates, err := s.store.ListQueuedJobs(ctx, availableSlots+activeInMemoryCount)
	if err != nil {
		slog.Error("scheduler: list queued jobs failed", "error", err)
		return
	}

	admitted := 0
	for _, job := range candidates {
		if admitted >= availableSlots {
			break
		}

		// Skip if already tracked as running in-memory (e.g. creating worktree)
		if _, alreadyRunning := s.runningJobs.Load(job.ID); alreadyRunning {
			continue // Do NOT break: continue to admit subsequent queued jobs (SCH-3)!
		}

		// Project-level concurrency limit check (SCH-2)
		if s.projCfgProvider != nil {
			project, pErr := s.store.GetProject(ctx, job.ProjectID)
			if pErr == nil && project != nil {
				cfg, cErr := s.projCfgProvider.GetProjectConfig(ctx, project.RepoPath)
				if cErr == nil && cfg != nil && cfg.MaxConcurrentJobs > 0 {
					inMemForProj := s.countInMemoryForProject(job.ProjectID)
					dbRunningForProj, _ := s.store.CountRunningJobsByProject(ctx, job.ProjectID)
					projRunning := inMemForProj
					if dbRunningForProj > projRunning {
						projRunning = dbRunningForProj
					}
					if projRunning >= cfg.MaxConcurrentJobs {
						continue // Project limit saturated; skip to next candidate (SCH-2)
					}
				}
			}
		}

		// Create a cancellable context dedicated to this specific job run
		jobCtx, cancel := context.WithCancel(ctx)
		s.runningJobs.Store(job.ID, cancel)
		s.jobProjectMap.Store(job.ID, job.ProjectID)
		s.activeWg.Add(1)
		admitted++

		// Spawn lightweight background goroutine for job execution
		go func(j *Job, cancelFn context.CancelFunc) {
			defer s.activeWg.Done()
			defer s.runningJobs.Delete(j.ID)
			defer s.jobProjectMap.Delete(j.ID)
			defer cancelFn()

			slog.Info("scheduler: admitting job", "job_id", j.ID, "title", j.Title)

			// Run pipeline engine
			if err := s.engine.ExecuteJob(jobCtx, j.ID); err != nil {
				slog.Error("scheduler: job execution ended with error", "job_id", j.ID, "error", err)
			}
			// Trigger wake to immediately fill the vacated slot
			s.Wake()
		}(job, cancel)
	}
}

// CancelRunningJob cancels the execution context of a running job.
func (s *Scheduler) CancelRunningJob(jobID int64) bool {
	if cancelVal, ok := s.runningJobs.Load(jobID); ok {
		if cancelFn, ok := cancelVal.(context.CancelFunc); ok {
			cancelFn()
			return true
		}
	}
	return false
}

// Close waits for all active jobs to terminate cleanly before returning.
func (s *Scheduler) Close() {
	s.activeWg.Wait()
}
