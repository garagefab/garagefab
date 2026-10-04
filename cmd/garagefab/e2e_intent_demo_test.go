// Package main contains end-to-end acceptance tests for Garagefab.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ENTERPRISE/JAVA BRIDGE:
// Full-pipeline acceptance verification for the intent-file intake path.
//
// In Clean / Hexagonal Architecture (Root Composition Root):
// This test exercises the SAME wiring as `cmd/garagefab/main.go`, but in-process:
//  1. The real SQLite `store` (temp file DB, embedded goose migrations).
//  2. The real `factory.Engine` + `factory.Scheduler` (background goroutine).
//  3. The real `intake.Poller` scanning a `*-intent.md` file on disk (INT-2).
//  4. The real HTTP `server` router via `httptest.NewServer`.
//  5. The stub `gh` CLI on `PATH` driving the DLV delivery stage (DLV-1, DLV-4).
//
// Enterprise / Spring Boot comparison:
//   - This is the equivalent of a `@SpringBootTest(webEnvironment = RANDOM_PORT)`
//     that starts the full application context, posts a file to a watched folder,
//     and asserts the saga reaches its terminal state through two mandatory
//     human approval gates.
//
// Go idiom / language concept bridges:
//   - Hermetic subprocesses: a stub `gh` shell script is written to a temp dir and
//     prepended to `PATH` via `t.Setenv`, so the delivery stage needs no network.
//   - Bare remote: a local bare Git repository plays the role of `origin`, so the
//     delivery push (DLV-1/DLV-3) is exercised without touching any real remote.
//   - Goroutine lifecycle: `context.WithCancel` + `scheduler.Close()` drain the
//     scheduler before the deferred `db.Close()` runs (SQLite pools + worker teardown).
//
// The flow asserted here:
//
//	intent file on disk -> intake poller -> job (feature)
//	  -> 02 spec_review gate (approve, APR-5)
//	  -> 04 coding / 05 review
//	  -> 06 final gate (approve, APR-5)
//	  -> 07 delivery (push + PR) -> done (DLV-4)
//
// The test also performs the "additional write" step: because the in-process
// FakeRunner does not honour the intent body, the deliverable `isimler.txt`
// (10 random Turkish names) is written into the job worktree at the final gate
// and verified before approval.
// ==============================================================================
package main

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/intake"
	"github.com/garagefab/garagefab/internal/provider/github"
	"github.com/garagefab/garagefab/internal/server"
	"github.com/garagefab/garagefab/internal/store"
	"github.com/garagefab/garagefab/internal/worker/agent"
	"github.com/garagefab/garagefab/internal/worker/command"
	"github.com/garagefab/garagefab/internal/worker/worktree"
)

// createStubGHBinary writes a minimal, offline `gh` CLI stub to binDir.
// It answers exactly the calls made by the DLV delivery stage:
//   - `gh pr list ...` -> empty array (no existing PR)
//   - `gh pr create ...` -> a PR URL
//   - `gh issue list ...` -> empty array (intake issue scan is disabled here)
func createStubGHBinary(t *testing.T, binDir string) {
	t.Helper()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir stub gh dir: %v", err)
	}
	script := `#!/bin/sh
case "$1 $2" in
  "pr list")     echo "[]" ;;
  "pr create")   echo "https://github.com/demo/garagefab-demo-2/pull/1" ;;
  "issue list")  echo "[]" ;;
  "auth status") exit 0 ;;
esac
case "$1" in
  --version) echo "gh version 2.0.0 (2024-01-01)" ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatalf("write stub gh: %v", err)
	}
}

// setupIntentDemoRepo creates the demo project repository "garagefab-demo-2"
// together with a local bare `origin` remote, so the delivery stage can push.
// All three SDLC roles are configured to use the `opencode` agent.
func setupIntentDemoRepo(t *testing.T, repoDir, originDir string) {
	t.Helper()

	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}

	runGit("init", "-b", "main")
	runGit("config", "user.name", "Garagefab Demo")
	runGit("config", "user.email", "demo@garagefab.local")
	runGit("config", "commit.gpgSign", "false")

	if err := os.MkdirAll(filepath.Join(repoDir, ".garagefab", "intents"), 0o755); err != nil {
		t.Fatalf("mkdir .garagefab/intents: %v", err)
	}

	files := map[string]string{
		"README.md": "# garagefab-demo-2\n\nDemo project for the intent-file e2e flow.\n",
		"notes.txt": "hello from the demo project\n",
		".garagefab/project.yaml": `base_ref: origin/main
github:
  repo: demo/garagefab-demo-2
agents:
  spec: opencode
  coding: opencode
  review: opencode
work_types:
  - bug_fix
  - feature
  - refactor
  - docs
commands:
  build: []
  test: []
  lint: []
guardrails:
  protected_paths:
    - "**/*_test.go"
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(repoDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	runGit("add", ".")
	runGit("commit", "-m", "chore: initial demo project")

	// Local bare repository acting as `origin` for the delivery push (DLV-1, DLV-3).
	if out, err := exec.Command("git", "init", "-q", "--bare", originDir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare failed: %v\n%s", err, string(out))
	}
	runGit("remote", "add", "origin", originDir)
	runGit("push", "-q", "-u", "origin", "main")
}

// writeIsimlerNames writes 10 random Turkish names into isimler.txt inside the
// job worktree and verifies the result. This is the demo's "additional write"
// step: the FakeRunner does not honour the intent body, so the deliverable is
// produced here to mirror what a real coding agent would have committed.
func writeIsimlerNames(t *testing.T, worktreePath string) {
	t.Helper()

	pool := []string{
		"Ahmet", "Mehmet", "Ayse", "Fatma", "Mustafa",
		"Emine", "Ali", "Zeynep", "Huseyin", "Elif",
		"Can", "Deniz", "Ece", "Burak", "Selin",
		"Kaan", "Melis", "Emre", "Ceren", "Ozan",
	}
	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	picked := pool[:10]

	path := filepath.Join(worktreePath, "isimler.txt")
	if err := os.WriteFile(path, []byte(strings.Join(picked, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write isimler.txt: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read isimler.txt: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) != 10 {
		t.Fatalf("isimler.txt has %d names, want 10", len(lines))
	}
}

// approveJob posts a human approval through the interactive session cookie.
// Approvals are session-only by design (SEC-4, APR-7); a Bearer token is rejected.
func approveJob(t *testing.T, baseURL string, jobID int64, headSHA string, cookie *http.Cookie) {
	t.Helper()

	body := "{}"
	if headSHA != "" {
		body = fmt.Sprintf(`{"head_sha":%q}`, headSHA)
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/jobs/%d/approve", baseURL, jobID), strings.NewReader(body))
	if err != nil {
		t.Fatalf("build approve request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("approve job %d: %v", jobID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve job %d: HTTP %d", jobID, resp.StatusCode)
	}
}

// TestE2E_IntentFile_To_Done_INT2_APR7_DLV4 verifies the intent-file intake path
// end to end, through both human approval gates, up to delivery completion.
func TestE2E_IntentFile_To_Done_INT2_APR7_DLV4(t *testing.T) {
	const (
		projectName = "garagefab-demo-2"
		apiToken    = "intent-demo-token"
	)

	tempDir := walkthroughWorkspace(t)
	dbPath := filepath.Join(tempDir, "data", "garagefab.db")

	// Stub `gh` on PATH so the DLV delivery stage runs offline.
	stubBinDir := filepath.Join(tempDir, "stub-bin")
	createStubGHBinary(t, stubBinDir)
	t.Setenv("PATH", stubBinDir+string(filepath.ListSeparator)+os.Getenv("PATH"))

	// Demo project repository + local bare origin remote.
	repoDir := filepath.Join(tempDir, projectName)
	originDir := filepath.Join(tempDir, "origin.git")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	setupIntentDemoRepo(t, repoDir, originDir)

	// Garagefab global config + SQLite store.
	cfg := config.Default()
	cfg.DataDir = filepath.Join(tempDir, "data")
	cfg.Server.APIToken = apiToken

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Register the project directly against the store (mirrors POST /api/projects).
	proj := &store.Project{
		Name:             projectName,
		RepoPath:         repoDir,
		BaseRef:          "origin/main",
		EnabledWorkTypes: []string{store.WorkTypeFeature},
	}
	if err := db.Projects().CreateProject(ctx, proj); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// CHECKPOINT A - infrastructure ready, scheduler not started yet.
	pause(t, "A - infrastructure ready (repo + origin + DB + project row)",
		"What happened: demo repo + bare origin created, SQLite opened, project registered.",
		"               The scheduler is NOT started yet -> the system is fully static.",
		"Look: git -C "+repoDir+" log --oneline --decorate",
		"      git -C "+repoDir+" remote -v",
		"      git --git-dir="+originDir+" for-each-ref",
		"      sqlite3 "+dbPath+" 'select id,name,repo_path,base_ref from projects;'",
	)

	// Wire the engine with the real adapters and the FakeRunner (all agents = opencode).
	storeAdapter := newFactoryStoreAdapter(db)
	wtMgr := newFactoryWorktreeAdapter(worktree.NewManager(filepath.Join(cfg.DataDir, "worktrees")))

	agentRouter := agent.NewRouter()
	agentRouter.Register("opencode", agent.NewFakeRunner())
	agentAdapter := newFactoryAgentAdapter(agentRouter)

	cmdRunner := newFactoryCommandAdapter(command.NewRunner())
	guardrailAdapter := newFactoryGuardrailAdapter()
	projCfgAdapter := newFactoryProjectConfigAdapter()

	engine := factory.NewEngine(storeAdapter, wtMgr, agentAdapter, cmdRunner, filepath.Join(cfg.DataDir, "logs"))
	engine.SetGuardrailRunner(guardrailAdapter)
	engine.SetProjectConfigProvider(projCfgAdapter)
	engine.SetMaxRepairAttempts(1)
	engine.SetDefaultAgentTimeout(cfg.Engine.StepTimeouts.Agent)
	engine.SetPullRequestProvider(newFactoryPullRequestAdapter(github.NewClient(github.NewDefaultGHRunner(""))))

	scheduler := factory.NewScheduler(storeAdapter, engine, 1)
	scheduler.SetProjectConfigProvider(projCfgAdapter)

	schedCtx, cancelScheduler := context.WithCancel(context.Background())
	defer func() {
		cancelScheduler()
		scheduler.Close()
	}()
	// NOTE: the scheduler is deliberately NOT started yet. It is started after
	// DURAK B so the freshly created job stays observably queued (01_Intent/queued).

	// Real HTTP server (no UI filesystem needed for the API-only flow).
	srv := server.NewServer(cfg, db, engine, scheduler, nil)
	ts := httptest.NewServer(srv.Router)
	defer ts.Close()

	// Exchange the API token for an interactive session cookie (SEC-3).
	sessReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/session", strings.NewReader(fmt.Sprintf(`{"token":"%s"}`, apiToken)))
	if err != nil {
		t.Fatalf("build session request: %v", err)
	}
	sessReq.Header.Set("Content-Type", "application/json")
	sessResp, err := http.DefaultClient.Do(sessReq)
	if err != nil {
		t.Fatalf("session login: %v", err)
	}
	defer sessResp.Body.Close()
	if sessResp.StatusCode != http.StatusOK {
		t.Fatalf("session login: HTTP %d", sessResp.StatusCode)
	}
	var sessionCookie *http.Cookie
	for _, c := range sessResp.Cookies() {
		if c.Name == "gf_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("gf_session cookie not returned")
	}

	// Drop the intent file and let the poller ingest it (INT-2).
	intent := "---\ntype: feature\ntitle: Rastgele Turkce isimler\n---\n" +
		"isimler.txt dosyasina 10 adet random Turkce isim kaydet\n"
	intentPath := filepath.Join(repoDir, ".garagefab", "intents", "isimler-intent.md")
	if err := os.WriteFile(intentPath, []byte(intent), 0o644); err != nil {
		t.Fatalf("write intent file: %v", err)
	}

	poller := intake.NewPoller(db, nil, scheduler, 50*time.Millisecond)
	poller.PollOnce(ctx)

	// The poller should have created exactly one job, sourced from the intent file.
	projectID := proj.ID
	var jobID int64
	{
		var jobs []*store.Job
		for i := 0; i < 40; i++ {
			jobs, err = db.Jobs().ListJobs(ctx, store.JobListFilter{ProjectID: &projectID})
			if err != nil {
				t.Fatalf("list jobs: %v", err)
			}
			if len(jobs) > 0 {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if len(jobs) != 1 {
			t.Fatalf("expected 1 job from intent file, got %d", len(jobs))
		}
		if jobs[0].Source != store.SourceIntentFile {
			t.Fatalf("job source = %q, want %q", jobs[0].Source, store.SourceIntentFile)
		}
		if jobs[0].WorkType != store.WorkTypeFeature {
			t.Fatalf("job work_type = %q, want %q", jobs[0].WorkType, store.WorkTypeFeature)
		}
		jobID = jobs[0].ID
	}

	// CHECKPOINT B - intent file ingested, job queued (scheduler still stopped).
	pause(t, "B - intent file ingested, job queued (01_Intent/queued)",
		fmt.Sprintf("What happened: %q was read from disk and the poller created a job. Scheduler still stopped.", intentPath),
		"Look: sqlite3 "+dbPath+" 'select id,work_type,source,source_ref,stage,status from jobs;'",
		"      sqlite3 "+dbPath+" 'select project_id,source,ref from intake_seen;'",
		"      sqlite3 "+dbPath+" 'select type,payload from events order by id;'",
		"      cat "+intentPath,
	)

	// Now let the scheduler admit and run the job.
	go scheduler.Start(schedCtx)

	// Drive the job through both human gates until delivery completes.
	specApproved, finalApproved := false, false
	var gateJob *store.Job
	var finalJob *store.Job
	// A long deadline in tour mode so the operator can inspect at a pause without
	// the loop's wall-clock window expiring while the process is parked.
	deadlineWindow := 30 * time.Second
	if walkthroughEnabled() {
		deadlineWindow = 30 * time.Minute
	}
	deadline := time.Now().Add(deadlineWindow)
	for time.Now().Before(deadline) {
		j, err := db.Jobs().GetJob(ctx, jobID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}

		if j.Stage == store.StageDone && j.Status == store.StatusDone {
			finalJob = j
			break
		}
		if j.Status == store.StatusFailed || j.Status == store.StatusCancelled {
			t.Fatalf("job ended early: stage=%s status=%s", j.Stage, j.Status)
		}

		jobArtifactDir := filepath.Join(j.WorktreePath, ".garagefab", "jobs", fmt.Sprintf("%d", jobID))
		switch {
		case j.Status == store.StatusSpecReview && !specApproved:
			// CHECKPOINT C - spec generated, waiting for the human spec gate (idle).
			pause(t, "C - spec_review (spec generated, awaiting approval)",
				"What happened: 02_Clarification_and_Spec finished, spec.md generated; job waits at spec_review.",
				"Look: sqlite3 "+dbPath+" 'select stage,status from jobs;'",
				"      sqlite3 "+dbPath+" 'select stage,kind,executor,status from step_runs order by id;'",
				"      cat "+filepath.Join(jobArtifactDir, "spec.md"),
				"      ls -R "+filepath.Join(cfg.DataDir, "logs"),
			)
			approveJob(t, ts.URL, jobID, "", sessionCookie)
			specApproved = true
		case j.Stage == store.StageHumanApprovalGate && j.Status == store.StatusAwaitingApproval && !finalApproved:
			writeIsimlerNames(t, j.WorktreePath)
			// CHECKPOINT D - coding + review done, final gate waiting (idle).
			pause(t, "D - awaiting_approval (coding+review done; isimler.txt written)",
				"What happened: 04 coding + 05 review finished (review.json/evidence.md); waiting at the final gate.",
				"               Extra write: isimler.txt (10 Turkish names) was written to the worktree and verified.",
				"Look: cat "+filepath.Join(j.WorktreePath, "isimler.txt"),
				"      cat "+filepath.Join(jobArtifactDir, "review.json"),
				"      cat "+filepath.Join(jobArtifactDir, "evidence.md"),
				"      git -C "+j.WorktreePath+" status --short",
				"      git -C "+j.WorktreePath+" log --oneline",
			)
			gateJob = j
			approveJob(t, ts.URL, jobID, j.HeadSHA, sessionCookie)
			finalApproved = true
		}
		time.Sleep(25 * time.Millisecond)
	}

	if finalJob == nil {
		j, _ := db.Jobs().GetJob(ctx, jobID)
		t.Fatalf("job did not reach 07_Done/done (stage=%s status=%s)", j.Stage, j.Status)
	}
	if !specApproved || !finalApproved {
		t.Fatalf("both human gates must be approved: spec=%v final=%v", specApproved, finalApproved)
	}

	// CHECKPOINT E - delivery complete.
	pause(t, "E - done (delivery complete)",
		"What happened: 07 delivery finished -> 07_Done/done; branch pushed to origin, PR recorded, worktree removed.",
		"Look: sqlite3 "+dbPath+" 'select id,stage,status,pr_url,branch_name from jobs;'",
		"      sqlite3 "+dbPath+" 'select gate,decision,head_sha from approvals;'",
		"      git --git-dir="+originDir+" for-each-ref refs/heads",
		"      ls "+filepath.Join(tempDir, "data", "worktrees")+" 2>/dev/null || echo 'worktree gone (removed)'",
	)

	// Both human approvals must be recorded (APR-5, APR-7).
	approvals, err := db.Approvals().ListApprovalsByJob(ctx, jobID)
	if err != nil {
		t.Fatalf("list approvals: %v", err)
	}
	gates := map[string]bool{}
	for _, a := range approvals {
		gates[a.Gate] = a.Decision == store.ApprovalDecisionApprove
	}
	if !gates[store.ApprovalGateSpecReview] || !gates[store.ApprovalGateFinal] {
		t.Fatalf("expected approved spec_review + final gates, got %+v", gates)
	}

	// Delivery must have pushed the job branch to origin and recorded a PR URL (DLV-1, DLV-4).
	if finalJob.PRURL == "" {
		t.Error("expected a PR URL to be recorded after delivery")
	}
	if out, err := exec.Command("git", "--git-dir", originDir, "rev-parse", "--verify", "refs/heads/"+finalJob.BranchName).CombinedOutput(); err != nil {
		t.Errorf("job branch %q was not pushed to origin: %v\n%s", finalJob.BranchName, err, string(out))
	}

	// The ephemeral worktree is removed asynchronously right AFTER the delivery
	// DB commit (DLV-4, WKT-6), so allow a brief moment for it to disappear.
	if gateJob != nil {
		removed := false
		for i := 0; i < 60; i++ {
			if _, err := os.Stat(gateJob.WorktreePath); os.IsNotExist(err) {
				removed = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !removed {
			t.Errorf("expected worktree %q to be removed after delivery", gateJob.WorktreePath)
		}
	}
}
