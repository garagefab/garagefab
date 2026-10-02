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
	"fmt"
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
	return &Project{ID: id, Name: fmt.Sprintf("p%d", id), RepoPath: fmt.Sprintf("/repo/%d", id)}, nil
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
func (f *fakeWorktreeManager) WriteArtifact(ctx context.Context, worktreePath string, jobID int64, filename string, content []byte) error {
	return nil
}
func (f *fakeWorktreeManager) ReadArtifact(ctx context.Context, worktreePath string, jobID int64, filename string) ([]byte, error) {
	return nil, nil
}
func (f *fakeWorktreeManager) RemoveArtifact(ctx context.Context, worktreePath string, jobID int64, filename string) error {
	return nil
}
func (f *fakeWorktreeManager) ListArtifacts(ctx context.Context, worktreePath string, jobID int64) ([]string, error) {
	return nil, nil
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

type fakeProjectConfigProvider struct {
	configs map[string]*ProjectConfig
}

func (f *fakeProjectConfigProvider) GetProjectConfig(ctx context.Context, repoPath string) (*ProjectConfig, error) {
	if cfg, ok := f.configs[repoPath]; ok {
		return cfg, nil
	}
	return nil, nil
}

// TestScheduler_ProjectConcurrencyLimit_SCH2 verifies requirement SCH-2:
// An optional project-level limits.max_concurrent_jobs further limits that project
// without blocking other projects from using free global slots.
func TestScheduler_ProjectConcurrencyLimit_SCH2(t *testing.T) {
	ctx := context.Background()

	store := &fakeStoreForScheduler{
		jobs: map[int64]*Job{
			301: {ID: 301, ProjectID: 1, Status: StatusQueued, Title: "Job 301 - Proj 1"},
			302: {ID: 302, ProjectID: 1, Status: StatusQueued, Title: "Job 302 - Proj 1"},
			303: {ID: 303, ProjectID: 2, Status: StatusQueued, Title: "Job 303 - Proj 2"},
		},
	}

	engine := NewEngine(store, &fakeWorktreeManager{}, &fakeAgentRunner{}, nil, t.TempDir())
	scheduler := NewScheduler(store, engine, 5) // Global limit is 5

	// Configure Project 1 with limit 1, Project 2 with no limit
	projProvider := &fakeProjectConfigProvider{
		configs: map[string]*ProjectConfig{
			"/repo/1": {MaxConcurrentJobs: 1},
			"/repo/2": {MaxConcurrentJobs: 0},
		},
	}
	scheduler.SetProjectConfigProvider(projProvider)

	// Trigger admission cycle
	scheduler.scheduleNext(ctx)

	// Job 301 (Proj 1) should be admitted
	if _, ok := scheduler.runningJobs.Load(int64(301)); !ok {
		t.Errorf("expected Job 301 to be admitted")
	}

	// Job 302 (Proj 1) should NOT be admitted because Proj 1 limit is 1
	if _, ok := scheduler.runningJobs.Load(int64(302)); ok {
		t.Errorf("Job 302 should have been deferred due to project limit (SCH-2)")
	}

	// Job 303 (Proj 2) SHOULD be admitted (not blocked by Proj 1's limit)
	if _, ok := scheduler.runningJobs.Load(int64(303)); !ok {
		t.Errorf("expected Job 303 (Proj 2) to be admitted while Proj 1 is capped")
	}
}
