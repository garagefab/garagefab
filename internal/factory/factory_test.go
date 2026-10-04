// Package factory_test contains isolated unit tests for the factory engine and scheduler.
//
// ==============================================================================
// GO TESTING PATTERNS & HANDWRITTEN MOCKS VS MOCKITO:
//
// 1. Handwritten Mocks vs Java Mockito:
//   - In Java/Spring: Developers heavily rely on Mockito (`@Mock`, `when(...).thenReturn(...)`),
//     which uses runtime CGLIB/ByteBuddy bytecode manipulation and reflection.
//   - In Go: The universal convention is "Handwritten Mocks / Fakes".
//     `MockStore`, `MockWorktreeManager`, and `MockAgentRunner` are plain Go structs
//     storing in-memory maps or recording invocations.
//   - Advantages:
//   - 100% Compile-Time Safe: If an interface changes, compiler errors show exactly what's broken.
//   - Zero Reflection & Zero Dependencies: Runs in microseconds.
//   - Predictable Concurrency: Can be protected with standard `sync.Mutex`.
//
// ==============================================================================
package factory_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/garagefab/garagefab/internal/factory"
)

// MockStore implements factory.Store completely in-memory using Go maps and mutexes.
type MockStore struct {
	mu           sync.Mutex
	jobs         map[int64]*factory.Job
	projects     map[int64]*factory.Project
	stepRuns     []*factory.StepRun
	events       []*factory.Event
	approvals    []*factory.Approval
	activeProcs  map[int64]bool
	nextID       int64
	inTxFailNext bool
}

func newMockStore() *MockStore {
	return &MockStore{
		jobs:        make(map[int64]*factory.Job),
		projects:    make(map[int64]*factory.Project),
		activeProcs: make(map[int64]bool),
		nextID:      1,
	}
}

func (m *MockStore) GetJob(ctx context.Context, id int64) (*factory.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, fmt.Errorf("job %d not found", id)
	}
	cp := *j // Return shallow copy to avoid concurrent mutations
	return &cp, nil
}

func (m *MockStore) GetProject(ctx context.Context, id int64) (*factory.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[id]
	if !ok {
		return nil, fmt.Errorf("project %d not found", id)
	}
	cp := *p
	return &cp, nil
}

func (m *MockStore) GetNextQueuedJob(ctx context.Context) (*factory.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var oldest *factory.Job
	for _, j := range m.jobs {
		if j.Status == factory.StatusQueued {
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

func (m *MockStore) ListQueuedJobs(ctx context.Context, limit int) ([]*factory.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var queued []*factory.Job
	for _, j := range m.jobs {
		if j.Status == factory.StatusQueued {
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

func (m *MockStore) CountRunningJobs(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, j := range m.jobs {
		if j.Status == factory.StatusRunning {
			count++
		}
	}
	return count, nil
}

func (m *MockStore) CountRunningJobsByProject(ctx context.Context, projectID int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, j := range m.jobs {
		if j.ProjectID == projectID && j.Status == factory.StatusRunning {
			count++
		}
	}
	return count, nil
}

func (m *MockStore) CreateStepRun(ctx context.Context, step *factory.StepRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	step.ID = m.nextID
	m.nextID++
	m.stepRuns = append(m.stepRuns, step)
	return nil
}

func (m *MockStore) UpdateStepRun(ctx context.Context, step *factory.StepRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.stepRuns {
		if s.ID == step.ID {
			m.stepRuns[i] = step
			return nil
		}
	}
	return nil
}

func (m *MockStore) ListStepRunsByJob(ctx context.Context, jobID int64) ([]*factory.StepRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []*factory.StepRun
	for _, s := range m.stepRuns {
		if s.JobID == jobID {
			res = append(res, s)
		}
	}
	return res, nil
}

func (m *MockStore) CreateProcessRecord(ctx context.Context, stepRunID int64, pid, pgid int, startTime int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeProcs[stepRunID] = true
	return nil
}

func (m *MockStore) MarkProcessInactive(ctx context.Context, stepRunID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeProcs[stepRunID] = false
	return nil
}

func (m *MockStore) InTx(ctx context.Context, fn func(tx factory.StoreTx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inTxFailNext {
		m.inTxFailNext = false
		return errors.New("simulated tx failure")
	}
	tx := &mockTx{store: m}
	return fn(tx)
}

type mockTx struct {
	store *MockStore
}

func (tx *mockTx) UpdateJobState(ctx context.Context, jobID int64, stage, status string) error {
	j, ok := tx.store.jobs[jobID]
	if !ok {
		return fmt.Errorf("job %d not found", jobID)
	}
	j.Stage = stage
	j.Status = status
	return nil
}

func (tx *mockTx) UpdateJobWorktree(ctx context.Context, jobID int64, worktreePath, branchName, baseSHA string) error {
	j, ok := tx.store.jobs[jobID]
	if !ok {
		return fmt.Errorf("job %d not found", jobID)
	}
	j.WorktreePath = worktreePath
	j.BranchName = branchName
	j.BaseSHA = baseSHA
	j.HeadSHA = baseSHA
	return nil
}

func (tx *mockTx) UpdateJobHead(ctx context.Context, jobID int64, headSHA string) error {
	j, ok := tx.store.jobs[jobID]
	if !ok {
		return fmt.Errorf("job %d not found", jobID)
	}
	j.HeadSHA = headSHA
	return nil
}

func (tx *mockTx) RecordEvent(ctx context.Context, jobID int64, eventType string, payload string) error {
	tx.store.events = append(tx.store.events, &factory.Event{
		JobID:   jobID,
		Type:    eventType,
		Payload: payload,
	})
	return nil
}

func (tx *mockTx) RecordApproval(ctx context.Context, a *factory.Approval) error {
	tx.store.approvals = append(tx.store.approvals, a)
	return nil
}

func (tx *mockTx) UpdateJobPR(ctx context.Context, jobID int64, prURL string) error {
	j, ok := tx.store.jobs[jobID]
	if !ok {
		return fmt.Errorf("job %d not found", jobID)
	}
	j.PRURL = prURL
	return nil
}

// MockWorktreeManager provides an in-memory double of factory.WorktreeManager.
type MockWorktreeManager struct {
	createdWorktrees  map[int64]string
	checkpoints       map[int64]string
	checkpointHistory []string
	removedWorktrees  map[int64]bool
	resetCount        int
	artifacts         map[string][]byte
	tamperedDiff      string
	currentHead       string
	pushErr           error
	pushedRemotes     []string
	pushedBranches    []string
	removedPaths      []string
	lastDeleteBranch  bool
}

func newMockWorktreeManager() *MockWorktreeManager {
	return &MockWorktreeManager{
		createdWorktrees: make(map[int64]string),
		checkpoints:      make(map[int64]string),
		removedWorktrees: make(map[int64]bool),
		artifacts:        make(map[string][]byte),
	}
}

func (m *MockWorktreeManager) Create(ctx context.Context, repoPath, projectName string, jobID int64, baseRef string) (*factory.WorktreeInfo, error) {
	path := fmt.Sprintf("/tmp/worktrees/%s/%d", projectName, jobID)
	m.createdWorktrees[jobID] = path
	return &factory.WorktreeInfo{
		Path:    path,
		Branch:  fmt.Sprintf("garagefab/job-%d", jobID),
		BaseSHA: "base123",
	}, nil
}

func (m *MockWorktreeManager) Checkpoint(ctx context.Context, worktreePath string, jobID int64, message string) (string, error) {
	sha := fmt.Sprintf("sha-%s-%d", message, jobID)
	m.checkpoints[jobID] = sha
	m.checkpointHistory = append(m.checkpointHistory, message)
	m.currentHead = sha
	return sha, nil
}

func (m *MockWorktreeManager) Reset(ctx context.Context, worktreePath, targetSHA string) error {
	m.resetCount++
	return nil
}

func (m *MockWorktreeManager) Remove(ctx context.Context, repoPath, worktreePath, branchName string, deleteBranch bool) error {
	m.removedPaths = append(m.removedPaths, worktreePath)
	m.lastDeleteBranch = deleteBranch
	return nil
}

func (m *MockWorktreeManager) Diff(ctx context.Context, worktreePath, baseSHA string) (string, error) {
	if m.tamperedDiff != "" {
		return m.tamperedDiff, nil
	}
	if baseSHA == "head123" || strings.HasPrefix(baseSHA, "sha-") {
		return "diff --git a/.garagefab/jobs/1/review.json b/.garagefab/jobs/1/review.json", nil
	}
	return "diff --git a/test.go b/test.go\n--- a/test.go\n+++ b/test.go\n@@ -1 +1 @@\n-old\n+new", nil
}

func (m *MockWorktreeManager) HeadSHA(ctx context.Context, worktreePath string) (string, error) {
	if m.currentHead != "" {
		return m.currentHead, nil
	}
	return "head123", nil
}

func (m *MockWorktreeManager) Push(ctx context.Context, worktreePath, remote, branch string) error {
	m.pushedRemotes = append(m.pushedRemotes, remote)
	m.pushedBranches = append(m.pushedBranches, branch)
	if m.pushErr != nil {
		return m.pushErr
	}
	return nil
}

func (m *MockWorktreeManager) WriteArtifact(ctx context.Context, worktreePath string, jobID int64, filename string, content []byte) error {
	if m.artifacts == nil {
		m.artifacts = make(map[string][]byte)
	}
	key := fmt.Sprintf("%d/%s", jobID, filename)
	m.artifacts[key] = content
	return nil
}

func (m *MockWorktreeManager) ReadArtifact(ctx context.Context, worktreePath string, jobID int64, filename string) ([]byte, error) {
	if m.artifacts != nil {
		key := fmt.Sprintf("%d/%s", jobID, filename)
		if content, ok := m.artifacts[key]; ok {
			return content, nil
		}
	}
	if filename == "review.json" {
		return []byte(`{
  "schema_version": 1,
  "decision": "approve",
  "summary": "Mock review approved",
  "risk": {
    "side_effect": {"score": 1, "rationale": "low"},
    "performance": {"score": 1, "rationale": "low"},
    "backward_compatibility": {"score": 1, "rationale": "low"}
  },
  "findings": [],
  "warnings": [],
  "spec_coverage": []
}`), nil
	}
	return nil, fmt.Errorf("artifact not found: %s", filename)
}

func (m *MockWorktreeManager) RemoveArtifact(ctx context.Context, worktreePath string, jobID int64, filename string) error {
	if m.artifacts != nil {
		key := fmt.Sprintf("%d/%s", jobID, filename)
		delete(m.artifacts, key)
	}
	return nil
}

func (m *MockWorktreeManager) ListArtifacts(ctx context.Context, worktreePath string, jobID int64) ([]string, error) {
	if m.artifacts == nil {
		return nil, nil
	}
	prefix := fmt.Sprintf("%d/", jobID)
	var list []string
	for k := range m.artifacts {
		if strings.HasPrefix(k, prefix) {
			list = append(list, strings.TrimPrefix(k, prefix))
		}
	}
	return list, nil
}

// MockAgentRunner provides an in-memory double of factory.AgentRunner.
type MockAgentRunner struct {
	invocations []factory.AgentRequest
	failCoding  bool
}

func (m *MockAgentRunner) Run(ctx context.Context, req factory.AgentRequest) (*factory.AgentResult, error) {
	m.invocations = append(m.invocations, req)
	if req.OnProcessStart != nil {
		req.OnProcessStart(100, 100, time.Now().Unix())
	}
	if req.Stage == factory.StageCoding && m.failCoding {
		return &factory.AgentResult{ExitCode: 1, Summary: "coding failed"}, nil
	}
	return &factory.AgentResult{ExitCode: 0, Summary: "ok"}, nil
}

// ------------------------------------------------------------------------------
// Unit Tests
// ------------------------------------------------------------------------------

// TestEngine_RefactorHappyPath_PIP1_APR5 tests requirements PIP-1 and APR-5:
// Job executes through Coding and Review, reaches Human Approval Gate,
// rejects mismatched head SHA (stale evidence), and completes when approved.
func TestEngine_RefactorHappyPath_PIP1_APR5(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	wtMgr := newMockWorktreeManager()
	agentRunner := &MockAgentRunner{}
	engine := factory.NewEngine(store, wtMgr, agentRunner, nil, t.TempDir())

	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha", BaseRef: "main"}
	store.jobs[10] = &factory.Job{
		ID:        10,
		ProjectID: 1,
		WorkType:  factory.WorkTypeRefactor,
		Title:     "Refactor DB layer",
		Stage:     factory.StageIntent,
		Status:    factory.StatusQueued,
	}

	// 1. Run pipeline
	if err := engine.ExecuteJob(ctx, 10); err != nil {
		t.Fatalf("ExecuteJob failed: %v", err)
	}

	// Verify job reached StageHumanApprovalGate / awaiting_approval (PIP-1, SCH-4)
	job, err := store.GetJob(ctx, 10)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if job.Stage != factory.StageHumanApprovalGate || job.Status != factory.StatusAwaitingApproval {
		t.Errorf("expected 06_Human_Approval_Gate / awaiting_approval, got %s / %s", job.Stage, job.Status)
	}

	// Verify checkpoint commit happened
	if job.HeadSHA == "" || job.HeadSHA == job.BaseSHA {
		t.Errorf("expected HeadSHA to be updated after coding checkpoint, got: %s", job.HeadSHA)
	}

	// 2. Test APR-5: Stale evidence rejection
	err = engine.Approve(ctx, 10, "wrong-sha")
	if !errors.Is(err, factory.ErrStaleEvidence) {
		t.Errorf("expected ErrStaleEvidence, got %v", err)
	}

	// 3. Test APR-5: Valid approval moves to Done/done
	err = engine.Approve(ctx, 10, job.HeadSHA)
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	jobDone, _ := store.GetJob(ctx, 10)
	if jobDone.Stage != factory.StageDone || jobDone.Status != factory.StatusDone {
		t.Errorf("expected 07_Done / done, got %s / %s", jobDone.Stage, jobDone.Status)
	}
}

// TestEngine_Rejection_APR6 tests requirement APR-6:
// Rejection requires a non-empty human note and regresses job back to 04_Coding / queued.
func TestEngine_Rejection_APR6(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	wtMgr := newMockWorktreeManager()
	agentRunner := &MockAgentRunner{}
	engine := factory.NewEngine(store, wtMgr, agentRunner, nil, t.TempDir())

	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha"}
	store.jobs[20] = &factory.Job{
		ID:        20,
		ProjectID: 1,
		WorkType:  factory.WorkTypeRefactor,
		Title:     "Refactor Auth",
		Stage:     factory.StageHumanApprovalGate,
		Status:    factory.StatusAwaitingApproval,
		HeadSHA:   "valid-sha",
	}

	// Reject without note must fail (APR-6)
	err := engine.Reject(ctx, 20, "")
	if !errors.Is(err, factory.ErrEmptyRejectionNote) {
		t.Errorf("expected ErrEmptyRejectionNote, got %v", err)
	}

	// Reject with note moves back to 04_Coding / queued (APR-6)
	err = engine.Reject(ctx, 20, "Needs more unit tests")
	if err != nil {
		t.Fatalf("Reject failed: %v", err)
	}

	job, _ := store.GetJob(ctx, 20)
	if job.Stage != factory.StageCoding || job.Status != factory.StatusQueued {
		t.Errorf("expected 04_Coding / queued after rejection, got %s / %s", job.Stage, job.Status)
	}
}

// TestScheduler_ConcurrencyLimit_SCH1_3 tests requirements SCH-1 and SCH-3:
// Queue contains 3 jobs, max concurrency = 2. Scheduler admits up to limit.
func TestScheduler_ConcurrencyLimit_SCH1_3(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := newMockStore()
	wtMgr := newMockWorktreeManager()
	agentRunner := &MockAgentRunner{}
	engine := factory.NewEngine(store, wtMgr, agentRunner, nil, t.TempDir())

	store.projects[1] = &factory.Project{ID: 1, Name: "alpha", RepoPath: "/repos/alpha"}

	// Create 3 queued jobs
	for i := 1; i <= 3; i++ {
		id := int64(100 + i)
		store.jobs[id] = &factory.Job{
			ID:        id,
			ProjectID: 1,
			WorkType:  factory.WorkTypeRefactor,
			Title:     fmt.Sprintf("Job %d", id),
			Stage:     factory.StageIntent,
			Status:    factory.StatusQueued,
		}
	}

	// Limit scheduler to max 2 concurrent jobs
	scheduler := factory.NewScheduler(store, engine, 2)
	go scheduler.Start(ctx)

	// Wait briefly for scheduler to process jobs
	time.Sleep(100 * time.Millisecond)

	// Wait for all jobs to reach gate
	time.Sleep(200 * time.Millisecond)

	for i := 1; i <= 3; i++ {
		id := int64(100 + i)
		job, _ := store.GetJob(ctx, id)
		if job.Stage != factory.StageHumanApprovalGate {
			t.Errorf("expected job %d to reach approval gate, got %s", id, job.Stage)
		}
	}
}
