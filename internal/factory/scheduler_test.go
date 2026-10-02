// Package factory contains whitebox tests for internal scheduler logic.
//
// ==============================================================================
// ARCHITECTURAL ROLE & WHITEBOX TESTING:
// Scheduler Admission & Concurrency Throttling Tests (SCH-1, SCH-3).
//
// Whitebox testing allows inspecting package-internal state (`runningJobs` sync.Map)
// without exposing internal implementation details to outside packages.
// ==============================================================================
package factory

import (
	"context"
	"sort"
	"sync"
	"testing"
)

// fakeStoreForScheduler provides a focused in-memory store for scheduler whitebox tests.
type fakeStoreForScheduler struct {
	mu          sync.Mutex
	jobs        map[int64]*Job
	runningJobs int
}

func (f *fakeStoreForScheduler) GetJob(ctx context.Context, id int64) (*Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return nil, nil
	}
	cp := *j
	return &cp, nil
}

func (f *fakeStoreForScheduler) GetProject(ctx context.Context, id int64) (*Project, error) {
	return &Project{ID: 1, Name: "p"}, nil
}

func (f *fakeStoreForScheduler) GetNextQueuedJob(ctx context.Context) (*Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var oldest *Job
	for _, j := range f.jobs {
		if j.Status == StatusQueued {
			if oldest == nil || j.ID < oldest.ID {
				oldest = j
			}
		}
	}
	if oldest == nil {
		return nil, nil
	}
	cp := *oldest
	return &cp, nil
}

func (f *fakeStoreForScheduler) ListQueuedJobs(ctx context.Context, limit int) ([]*Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var queued []*Job
	for _, j := range f.jobs {
		if j.Status == StatusQueued {
			cp := *j
			queued = append(queued, &cp)
		}
	}
	sort.Slice(queued, func(i, j int) bool {
		return queued[i].ID < queued[j].ID
	})
	if limit > 0 && len(queued) > limit {
		queued = queued[:limit]
	}
	return queued, nil
}

func (f *fakeStoreForScheduler) CountRunningJobs(ctx context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runningJobs, nil
}

func (f *fakeStoreForScheduler) CountRunningJobsByProject(ctx context.Context, projectID int64) (int, error) {
	return 0, nil
}

func (f *fakeStoreForScheduler) CreateStepRun(ctx context.Context, step *StepRun) error {
	return nil
}

func (f *fakeStoreForScheduler) UpdateStepRun(ctx context.Context, step *StepRun) error {
	return nil
}

func (f *fakeStoreForScheduler) CreateProcessRecord(ctx context.Context, stepRunID int64, pid, pgid int, startTime int64) error {
	return nil
}

func (f *fakeStoreForScheduler) MarkProcessInactive(ctx context.Context, processRecordID int64) error {
	return nil
}

func (f *fakeStoreForScheduler) InTx(ctx context.Context, fn func(tx StoreTx) error) error {
	return fn(&fakeTxForScheduler{})
}

type fakeTxForScheduler struct{}

func (t *fakeTxForScheduler) UpdateJobState(ctx context.Context, jobID int64, stage, status string) error {
	return nil
}
func (t *fakeTxForScheduler) UpdateJobWorktree(ctx context.Context, jobID int64, worktreePath, branchName, baseSHA string) error {
	return nil
}
func (t *fakeTxForScheduler) UpdateJobHead(ctx context.Context, jobID int64, headSHA string) error {
	return nil
}
func (t *fakeTxForScheduler) RecordEvent(ctx context.Context, jobID int64, eventType string, payload string) error {
	return nil
}
func (t *fakeTxForScheduler) RecordApproval(ctx context.Context, a *Approval) error {
	return nil
}

type fakeWorktreeManager struct{}

func (f *fakeWorktreeManager) Create(ctx context.Context, repoPath, projectName string, jobID int64, baseRef string) (*WorktreeInfo, error) {
	return &WorktreeInfo{Path: "/tmp/wt", Branch: "b", BaseSHA: "sha"}, nil
}
func (f *fakeWorktreeManager) Checkpoint(ctx context.Context, worktreePath string, jobID int64, message string) (string, error) {
	return "sha", nil
}
func (f *fakeWorktreeManager) Reset(ctx context.Context, worktreePath, targetSHA string) error {
	return nil
}
func (f *fakeWorktreeManager) Remove(ctx context.Context, repoPath, worktreePath, branchName string, deleteBranch bool) error {
	return nil
}
func (f *fakeWorktreeManager) Diff(ctx context.Context, worktreePath, baseSHA string) (string, error) {
	return "", nil
}
func (f *fakeWorktreeManager) HeadSHA(ctx context.Context, worktreePath string) (string, error) {
	return "sha", nil
}

type fakeAgentRunner struct{}

func (f *fakeAgentRunner) Run(ctx context.Context, req AgentRequest) (*AgentResult, error) {
	return &AgentResult{ExitCode: 0}, nil
}

// TestScheduler_MultiAdmit_NoStarvation_SCH3 verifies requirement SCH-3:
// When available capacity > 0, an in-flight job whose DB status is still 'queued'
// does NOT block the admission of subsequent queued candidates in the same cycle.
func TestScheduler_MultiAdmit_NoStarvation_SCH3(t *testing.T) {
	ctx := context.Background()

	store := &fakeStoreForScheduler{
		jobs: map[int64]*Job{
			201: {ID: 201, Status: StatusQueued, Title: "Job 201"},
			202: {ID: 202, Status: StatusQueued, Title: "Job 202"},
		},
	}

	engine := NewEngine(store, &fakeWorktreeManager{}, &fakeAgentRunner{}, nil, t.TempDir())
	scheduler := NewScheduler(store, engine, 2)

	// Simulate Job 201 already in-flight in memory (e.g. creating worktree before DB status transition)
	scheduler.runningJobs.Store(int64(201), func() {})

	// Trigger one schedule admission cycle
	scheduler.scheduleNext(ctx)

	// In the old implementation (which breaks on the first alreadyRunning job), Job 202 is never admitted.
	// With the fix, Job 202 must be loaded into runningJobs!
	if _, ok := scheduler.runningJobs.Load(int64(202)); !ok {
		t.Errorf("Job 202 was starved: scheduler broke loop instead of admitting subsequent candidate")
	}
}
