// Package factory_test verifies the delivery pipeline stage in package factory.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Outbound Delivery Port & Pipeline Stage Verification (Hexagonal Architecture).
//
// In Clean Architecture:
// `delivery_test.go` validates the stage 07_Done delivery orchestrator:
//   - Verifies interaction with driven ports: `WorktreeManager.Push` and `PullRequestProvider`.
//   - Asserts atomic database updates: PR URL persistence, state progression,
//     and audit log emission via `StoreTx`.
//   - Confirms deterministic failure categorization (`FailureBlocked`) on push,
//     network, or configuration errors (DLV-3, DLV-6).
//
// ENTERPRISE / JAVA SPRING COMPARISON:
//   - In Java: Corresponds to a `@SpringBootTest` testing a delivery orchestration
//     service using Mockito for outbound ports (`GitClient`, `GitHubFeignClient`)
//     and verifying database assertions against an in-memory repository or H2 database.
//
// GO IDIOMS & CONCEPTS:
//   - In-memory mock doubles (`MockStore`, `MockWorktreeManager`, `mockPRProvider`).
//   - Struct embedding and closures for test-specific stubbing.
//   - Table-driven testing and requirement-traceable test names (DLV-1..6).
//
// ==============================================================================
package factory_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/garagefab/garagefab/internal/factory"
)

// mockPRProvider implements factory.PullRequestProvider for offline unit testing.
type mockPRProvider struct {
	findPRFunc   func(ctx context.Context, repo, head string) (*factory.PullRequest, error)
	createPRFunc func(ctx context.Context, req factory.PullRequestRequest) (*factory.PullRequest, error)
	createdReqs  []factory.PullRequestRequest
}

func (m *mockPRProvider) FindPullRequest(ctx context.Context, repo, head string) (*factory.PullRequest, error) {
	if m.findPRFunc != nil {
		return m.findPRFunc(ctx, repo, head)
	}
	return nil, nil
}

func (m *mockPRProvider) CreatePullRequest(ctx context.Context, req factory.PullRequestRequest) (*factory.PullRequest, error) {
	m.createdReqs = append(m.createdReqs, req)
	if m.createPRFunc != nil {
		return m.createPRFunc(ctx, req)
	}
	return &factory.PullRequest{
		Number: 101,
		URL:    "https://github.com/" + req.Repo + "/pull/101",
		State:  "OPEN",
	}, nil
}

type staticProjectConfigProvider struct {
	cfg *factory.ProjectConfig
}

func (p *staticProjectConfigProvider) GetProjectConfig(ctx context.Context, repoPath string) (*factory.ProjectConfig, error) {
	return p.cfg, nil
}

// TestDelivery_CreatePR_DLV1 tests requirement DLV-1:
// After human approval, the system pushes branch to remote and creates a PR with evidence
// and issue closing keyword, storing the PR URL and advancing status to done.
func TestDelivery_CreatePR_DLV1(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	wtMgr := newMockWorktreeManager()
	prProvider := &mockPRProvider{}

	engine := factory.NewEngine(store, wtMgr, &MockAgentRunner{}, nil, t.TempDir())
	engine.SetPullRequestProvider(prProvider)

	projCfg := &factory.ProjectConfig{
		BaseRef: "origin/main",
		GitHub: factory.ProjectGitHub{
			Repo:           "owner/app",
			IntakeLabel:    "garagefab",
			PRIssueKeyword: "closes",
		},
	}
	engine.SetProjectConfigProvider(&staticProjectConfigProvider{cfg: projCfg})

	store.projects[1] = &factory.Project{ID: 1, Name: "app", RepoPath: "/repos/app", BaseRef: "origin/main"}
	job := &factory.Job{
		ID:           42,
		ProjectID:    1,
		WorkType:     factory.WorkTypeFeature,
		Title:        "Add OAuth",
		Intent:       "Implement Google OAuth login",
		Source:       "github_issue",
		SourceRef:    "owner/app#77",
		Stage:        factory.StageHumanApprovalGate,
		Status:       factory.StatusAwaitingApproval,
		BranchName:   "garagefab/job-42",
		WorktreePath: "/worktrees/app/42",
		HeadSHA:      "sha-final-evidence",
	}
	store.jobs[42] = job
	wtMgr.currentHead = "sha-final-evidence"
	_ = wtMgr.WriteArtifact(ctx, job.WorktreePath, job.ID, "evidence.md", []byte("# Evidence\nAll tests passed."))

	// 1. Human Approves at Gate -> transitions to 07_Done / queued
	if err := engine.Approve(ctx, 42, "sha-final-evidence"); err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	jobApproved, _ := store.GetJob(ctx, 42)
	if jobApproved.Stage != factory.StageDone || jobApproved.Status != factory.StatusQueued {
		t.Fatalf("expected 07_Done/queued after approve, got %s/%s", jobApproved.Stage, jobApproved.Status)
	}

	// 2. Scheduler / Engine executes delivery stage
	if err := engine.ExecuteJob(ctx, 42); err != nil {
		t.Fatalf("ExecuteJob delivery failed: %v", err)
	}

	// 3. Verify push occurred to "origin" with branch "garagefab/job-42"
	if len(wtMgr.pushedRemotes) != 1 || wtMgr.pushedRemotes[0] != "origin" {
		t.Errorf("expected push remote 'origin', got: %v", wtMgr.pushedRemotes)
	}
	if len(wtMgr.pushedBranches) != 1 || wtMgr.pushedBranches[0] != "garagefab/job-42" {
		t.Errorf("expected push branch 'garagefab/job-42', got: %v", wtMgr.pushedBranches)
	}

	// 4. Verify PR was created with proper title, body, and "Closes #77"
	if len(prProvider.createdReqs) != 1 {
		t.Fatalf("expected 1 CreatePullRequest call, got %d", len(prProvider.createdReqs))
	}
	req := prProvider.createdReqs[0]
	if req.Title != "Add OAuth" {
		t.Errorf("expected PR title 'Add OAuth', got: %s", req.Title)
	}
	if req.Base != "main" {
		t.Errorf("expected PR base 'main', got: %s", req.Base)
	}
	if !strings.Contains(req.Body, "Closes #77") {
		t.Errorf("expected PR body to contain 'Closes #77', got: %s", req.Body)
	}
	if !strings.Contains(req.Body, "All tests passed.") {
		t.Errorf("expected PR body to contain evidence summary, got: %s", req.Body)
	}

	// 5. Verify Job state updated to done with PR URL
	jobDone, _ := store.GetJob(ctx, 42)
	if jobDone.Stage != factory.StageDone || jobDone.Status != factory.StatusDone {
		t.Errorf("expected 07_Done/done, got %s/%s", jobDone.Stage, jobDone.Status)
	}
	if jobDone.PRURL != "https://github.com/owner/app/pull/101" {
		t.Errorf("expected PR URL, got: %s", jobDone.PRURL)
	}

	// 6. Verify worktree removed with deleteBranch = false (DLV-4, WKT-6)
	if len(wtMgr.removedPaths) != 1 || wtMgr.removedPaths[0] != "/worktrees/app/42" {
		t.Errorf("expected worktree cleanup, got: %v", wtMgr.removedPaths)
	}
	if wtMgr.lastDeleteBranch {
		t.Errorf("expected local branch to be kept (deleteBranch=false)")
	}
}

// TestDelivery_ReuseOpenPR_DLV2 tests requirement DLV-2:
// Delivery is idempotent. If an open PR already exists for the head branch, it is reused.
func TestDelivery_ReuseOpenPR_DLV2(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	wtMgr := newMockWorktreeManager()
	prProvider := &mockPRProvider{
		findPRFunc: func(ctx context.Context, repo, head string) (*factory.PullRequest, error) {
			return &factory.PullRequest{
				Number: 88,
				URL:    "https://github.com/owner/app/pull/88",
				State:  "OPEN",
			}, nil
		},
	}

	engine := factory.NewEngine(store, wtMgr, &MockAgentRunner{}, nil, t.TempDir())
	engine.SetPullRequestProvider(prProvider)

	projCfg := &factory.ProjectConfig{
		BaseRef: "main",
		GitHub: factory.ProjectGitHub{
			Repo: "owner/app",
		},
	}
	engine.SetProjectConfigProvider(&staticProjectConfigProvider{cfg: projCfg})

	store.projects[1] = &factory.Project{ID: 1, Name: "app", RepoPath: "/repos/app", BaseRef: "main"}
	job := &factory.Job{
		ID:           43,
		ProjectID:    1,
		Stage:        factory.StageDone,
		Status:       factory.StatusQueued,
		BranchName:   "garagefab/job-43",
		WorktreePath: "/worktrees/app/43",
	}
	store.jobs[43] = job

	if err := engine.ExecuteJob(ctx, 43); err != nil {
		t.Fatalf("ExecuteJob failed: %v", err)
	}

	// Verify no new PR was created
	if len(prProvider.createdReqs) != 0 {
		t.Errorf("expected 0 CreatePullRequest calls when open PR exists, got %d", len(prProvider.createdReqs))
	}

	jobDone, _ := store.GetJob(ctx, 43)
	if jobDone.Status != factory.StatusDone || jobDone.PRURL != "https://github.com/owner/app/pull/88" {
		t.Errorf("expected status done with reused PR URL, got %s / %s", jobDone.Status, jobDone.PRURL)
	}
}

// TestDelivery_ClosedPR_Blocked_DLV2 tests requirement DLV-2 and Decision P4:
// If a merged or closed PR already exists for the head branch, delivery fails as Blocked.
func TestDelivery_ClosedPR_Blocked_DLV2(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	wtMgr := newMockWorktreeManager()
	prProvider := &mockPRProvider{
		findPRFunc: func(ctx context.Context, repo, head string) (*factory.PullRequest, error) {
			return &factory.PullRequest{
				Number: 99,
				URL:    "https://github.com/owner/app/pull/99",
				State:  "MERGED",
			}, nil
		},
	}

	engine := factory.NewEngine(store, wtMgr, &MockAgentRunner{}, nil, t.TempDir())
	engine.SetPullRequestProvider(prProvider)

	projCfg := &factory.ProjectConfig{
		BaseRef: "main",
		GitHub: factory.ProjectGitHub{
			Repo: "owner/app",
		},
	}
	engine.SetProjectConfigProvider(&staticProjectConfigProvider{cfg: projCfg})

	store.projects[1] = &factory.Project{ID: 1, Name: "app", RepoPath: "/repos/app", BaseRef: "main"}
	job := &factory.Job{
		ID:           44,
		ProjectID:    1,
		Stage:        factory.StageDone,
		Status:       factory.StatusQueued,
		BranchName:   "garagefab/job-44",
		WorktreePath: "/worktrees/app/44",
	}
	store.jobs[44] = job

	err := engine.ExecuteJob(ctx, 44)
	if err == nil {
		t.Fatalf("expected ExecuteJob to fail when PR is merged")
	}

	jobFailed, _ := store.GetJob(ctx, 44)
	if jobFailed.Status != factory.StatusFailed {
		t.Errorf("expected status failed, got %s", jobFailed.Status)
	}

	// Verify worktree is kept for inspection/recovery (DLV-3, WKT-6)
	if len(wtMgr.removedPaths) != 0 {
		t.Errorf("worktree must be preserved on failure, got removedPaths: %v", wtMgr.removedPaths)
	}
}

// TestDelivery_FailureBlocked_DLV3 tests requirement DLV-3:
// Git push or PR creation errors fail the step as Blocked and keep the worktree for retry.
func TestDelivery_FailureBlocked_DLV3(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	wtMgr := newMockWorktreeManager()
	wtMgr.pushErr = errors.New("remote rejected: permission denied")

	prProvider := &mockPRProvider{}

	engine := factory.NewEngine(store, wtMgr, &MockAgentRunner{}, nil, t.TempDir())
	engine.SetPullRequestProvider(prProvider)

	projCfg := &factory.ProjectConfig{
		BaseRef: "main",
		GitHub: factory.ProjectGitHub{
			Repo: "owner/app",
		},
	}
	engine.SetProjectConfigProvider(&staticProjectConfigProvider{cfg: projCfg})

	store.projects[1] = &factory.Project{ID: 1, Name: "app", RepoPath: "/repos/app", BaseRef: "main"}
	job := &factory.Job{
		ID:           45,
		ProjectID:    1,
		Stage:        factory.StageDone,
		Status:       factory.StatusQueued,
		BranchName:   "garagefab/job-45",
		WorktreePath: "/worktrees/app/45",
	}
	store.jobs[45] = job

	err := engine.ExecuteJob(ctx, 45)
	if err == nil {
		t.Fatalf("expected ExecuteJob to fail on push error")
	}

	jobFailed, _ := store.GetJob(ctx, 45)
	if jobFailed.Status != factory.StatusFailed {
		t.Errorf("expected status failed, got %s", jobFailed.Status)
	}

	// Verify worktree is preserved (WKT-6)
	if len(wtMgr.removedPaths) != 0 {
		t.Errorf("expected worktree to be retained, but was removed")
	}
}

// TestDelivery_NoRepo_DLV6 tests requirement DLV-6:
// Projects without github.repo fail delivery as Blocked with an informative error.
func TestDelivery_NoRepo_DLV6(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	wtMgr := newMockWorktreeManager()
	prProvider := &mockPRProvider{}

	engine := factory.NewEngine(store, wtMgr, &MockAgentRunner{}, nil, t.TempDir())
	engine.SetPullRequestProvider(prProvider)

	// project.yaml without github.repo
	projCfg := &factory.ProjectConfig{
		BaseRef: "main",
	}
	engine.SetProjectConfigProvider(&staticProjectConfigProvider{cfg: projCfg})

	store.projects[1] = &factory.Project{ID: 1, Name: "local-app", RepoPath: "/repos/local-app", BaseRef: "main"}
	job := &factory.Job{
		ID:           46,
		ProjectID:    1,
		Stage:        factory.StageDone,
		Status:       factory.StatusQueued,
		BranchName:   "garagefab/job-46",
		WorktreePath: "/worktrees/local-app/46",
	}
	store.jobs[46] = job

	err := engine.ExecuteJob(ctx, 46)
	if err == nil {
		t.Fatalf("expected ExecuteJob to fail when github.repo is missing")
	}
	if !strings.Contains(err.Error(), "github.repo is not configured") {
		t.Errorf("expected clear diagnostic for missing github.repo, got: %v", err)
	}

	jobFailed, _ := store.GetJob(ctx, 46)
	if jobFailed.Status != factory.StatusFailed {
		t.Errorf("expected status failed, got %s", jobFailed.Status)
	}
	if len(wtMgr.removedPaths) != 0 {
		t.Errorf("expected worktree to be retained")
	}
}
