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
	store         Store          // Persistence port for querying queue and running counts
	engine        *Engine        // Pipeline engine for executing admitted jobs
	maxConcurrent int            // Maximum concurrent active jobs (SCH-1)
	pollInterval  time.Duration  // Periodic fallback poll interval
	wakeCh        chan struct{}  // Reactive signal channel to trigger immediate scheduling
	activeWg      sync.WaitGroup // WaitGroup tracking active job goroutines for clean shutdown
	runningJobs   sync.Map       // Map[int64]context.CancelFunc for active job cancellation
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
func (s *Scheduler) scheduleNext(ctx context.Context) {
	// Query currently running jobs from database
	runningCount, err := s.store.CountRunningJobs(ctx)
	if err != nil {
		slog.Error("scheduler: count running jobs failed", "error", err)
		return
	}

	// Calculate remaining execution capacity
	availableSlots := s.maxConcurrent - runningCount
	if availableSlots <= 0 {
		return // At maximum concurrency capacity
	}

	// Admit jobs up to available slots
	for i := 0; i < availableSlots; i++ {
		job, err := s.store.GetNextQueuedJob(ctx)
		if err != nil || job == nil {
			break // No more queued jobs waiting
		}

		// Skip if already tracked as running in-memory
		if _, alreadyRunning := s.runningJobs.Load(job.ID); alreadyRunning {
			break
		}

		// Create a cancellable context dedicated to this specific job run
		jobCtx, cancel := context.WithCancel(ctx)
		s.runningJobs.Store(job.ID, cancel)
		s.activeWg.Add(1)

		// Spawn lightweight background goroutine for job execution
		go func(j *Job, cancelFn context.CancelFunc) {
			defer s.activeWg.Done()
			defer s.runningJobs.Delete(j.ID)
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
