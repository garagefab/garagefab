package factory

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Scheduler manages concurrent job execution according to global concurrency limits (SCH-1..4).
type Scheduler struct {
	store         Store
	engine        *Engine
	maxConcurrent int
	pollInterval  time.Duration
	wakeCh        chan struct{}
	activeWg      sync.WaitGroup
	runningJobs   sync.Map // jobID -> context.CancelFunc
}

// NewScheduler creates a new scheduler with concurrency limits.
func NewScheduler(store Store, engine *Engine, maxConcurrent int) *Scheduler {
	if maxConcurrent <= 0 {
		maxConcurrent = 5 // default 5 (SCH-1)
	}
	s := &Scheduler{
		store:         store,
		engine:        engine,
		maxConcurrent: maxConcurrent,
		pollInterval:  500 * time.Millisecond,
		wakeCh:        make(chan struct{}, 10),
	}
	engine.SetWakeFunc(s.Wake)
	return s
}

// Wake notifies the scheduler to check for queued jobs immediately.
func (s *Scheduler) Wake() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

// Start runs the scheduler loop until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		s.scheduleNext(ctx)

		select {
		case <-ctx.Done():
			s.activeWg.Wait()
			return
		case <-ticker.C:
		case <-s.wakeCh:
		}
	}
}

// scheduleNext checks running counts and admits queued jobs FIFO (SCH-1, SCH-3).
func (s *Scheduler) scheduleNext(ctx context.Context) {
	runningCount, err := s.store.CountRunningJobs(ctx)
	if err != nil {
		slog.Error("scheduler: count running jobs failed", "error", err)
		return
	}

	availableSlots := s.maxConcurrent - runningCount
	if availableSlots <= 0 {
		return
	}

	for i := 0; i < availableSlots; i++ {
		job, err := s.store.GetNextQueuedJob(ctx)
		if err != nil || job == nil {
			break
		}
		if _, alreadyRunning := s.runningJobs.Load(job.ID); alreadyRunning {
			break
		}

		jobCtx, cancel := context.WithCancel(ctx)
		s.runningJobs.Store(job.ID, cancel)
		s.activeWg.Add(1)

		go func(j *Job, cancelFn context.CancelFunc) {
			defer s.activeWg.Done()
			defer s.runningJobs.Delete(j.ID)
			defer cancelFn()

			slog.Info("scheduler: admitting job", "job_id", j.ID, "title", j.Title)

			if err := s.engine.ExecuteJob(jobCtx, j.ID); err != nil {
				slog.Error("scheduler: job execution ended with error", "job_id", j.ID, "error", err)
			}
			s.Wake()
		}(job, cancel)
	}
}

// CancelRunningJob cancels the context of a currently running job if any.
func (s *Scheduler) CancelRunningJob(jobID int64) bool {
	if cancelVal, ok := s.runningJobs.Load(jobID); ok {
		if cancelFn, ok := cancelVal.(context.CancelFunc); ok {
			cancelFn()
			return true
		}
	}
	return false
}

// Close waits for all active jobs to terminate.
func (s *Scheduler) Close() {
	s.activeWg.Wait()
}
