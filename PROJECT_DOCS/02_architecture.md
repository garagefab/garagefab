# Garagefab — Architecture

> Status: Draft v2.2 · Audience: the developer, and coding agents that write `spec.md` and implement features.
> Inputs: `intent.md` (what and why), `techstack.md` (initial stack notes, superseded by this document).
> Next document: `spec.md` (observable behavior and acceptance criteria).

## 1. Purpose and Scope

This document defines **how Garagefab is built**: the technology stack, the major components, their boundaries, the data and state model, concurrency, failure handling, and security posture.

It deliberately does **not** define:

| Topic | Where it lives |
|-------|----------------|
| Why the product exists, pipeline stages, work types, MVP scenarios | `intent.md` |
| Exact API endpoints, screens, field-level schemas, acceptance criteria | `spec.md` |
| Rules for coding agents working on this repo (build commands, style) | `AGENTS.md` |
| Task order, milestones | `plan.md` / issue tracker |

Phase 1 scope follows `intent.md`. Anything marked **Phase 2** is only an extension point here.

## 2. Architectural Principles

Derived from `intent.md`; each one has a concrete architectural consequence.

1. **Single static binary.** Orchestrator, SQLite, and web UI ship as one executable built with `CGO_ENABLED=0`. No runtimes, no external services, no Docker for core execution.
2. **Artifacts are the audit trail.** Anything that matters to the human (spec, review report, evidence summary) is a file committed to Git. The database holds operational state and indexes, never the only copy of a decision.
3. **Determinism over prompts.** Prompts are advisory. Rules that must hold (protected files, test status, retry limits, failure categories) are enforced by Go code and command steps, never by an LLM.
4. **Fresh session per agent step.** Every agent invocation is a new process with no conversation resume. Coding and review never share context.
5. **Scope discipline.** Agent prompts and review criteria are bounded by the approved spec.
6. **Tool agnosticism.** Agents and issue trackers sit behind interfaces. Adding one must not touch the engine.
7. **Human control at gates.** Spec Review and Final Approval are mandatory in Phase 1. A job waiting on a human never consumes an execution slot.

## 3. Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Go 1.22+, single module, `CGO_ENABLED=0` | Static binary, goroutines for supervision, easy cross-compilation |
| D2 | `modernc.org/sqlite` in WAL mode | Pure Go; DBeaver/TablePlus can read live |
| D3 | `pressly/goose` with embedded SQL migrations | Test-proven; takes our `*sql.DB`, so no driver/CGO conflict |
| D4 | `spf13/cobra` for CLI, `go-chi/chi/v5` for HTTP | Standard, `net/http`-compatible, minimal |
| D5 | **SSE** for realtime, plain `POST` for actions | Traffic is server→client; stdlib only; built-in reconnect with `Last-Event-ID` |
| D6 | Artifacts in the Git worktree; operational state in SQLite; raw logs on disk | Preserves "Git is the audit trail" and keeps the DB small |
| D7 | Job state = **stage** × **status** | Kanban follows stage, badges follow status |
| D8 | One poller loop (30 s) for GitHub and intent files; **no fsnotify** | Fewer moving parts; avoids rename/partial-write/debounce edge cases |
| D9 | ~~GitHub via **Personal Access Token** and REST/GraphQL; no `gh` CLI dependency~~ **Superseded by D22** | ~~Zero external tool requirement~~ |
| D10 | Global concurrency limit, default **5** running jobs, configurable; same-project parallelism allowed | Per `intent.md`: jobs run concurrently in isolated worktrees |
| D11 | Crash recovery marks running jobs `interrupted`; the human retries | Never auto-resume a possibly half-applied change |
| D12 | HTTP bound to `127.0.0.1`, API token required | Agents execute local commands; Approve must not be callable by any local process |
| D13 | Agents return results through **files with a schema**, not by parsing stdout | Robust to CLI output changes |
| D14 | Project settings split: commands/guardrails/agent choice in `.garagefab/project.yaml` (repo); project registration in DB | Portable, versioned config without losing dashboard onboarding |
| D15 | Phase 1 platforms: **macOS and Linux**. Windows is unsupported | Process-group handling and `sh -c` command steps; revisit later |
| D16 | Factory Brain / FTS5 is **not** in Phase 1 | Deferred by `intent.md` |
| D17 | Worktrees are created at a job's **first agent step** from the **latest remote base** (`git fetch`, then `origin/<base>`); configurable via `base_ref`; fetch failure falls back to the local base branch with a warning event | Agents work on current code and PRs conflict less; works offline; `intent.md` updated accordingly |
| D18 | ~~Both **classic** and **fine-grained** PATs are supported; the GitHub Project link is optional~~ **Superseded by D22 and D23** | ~~Classic is the easiest path for solo developers on personal accounts; fine-grained cannot reach user-owned Projects~~ |
| D19 | Dashboard auth: one-time token URL → `HttpOnly` `SameSite=Strict` session cookie; skill and CLI use the bearer token | No friction after first open; JS never holds the token |
| D20 | `gopkg.in/yaml.v3` for YAML parsing | Pure Go, standard YAML parser for global and project configuration files |
| D21 | Agent CLI invocations: headless flags (`agy --print <prompt> --dangerously-skip-permissions --output-format json`, `opencode run --auto --format json <prompt>`), fresh session default, prompt via CLI args. Exit code 0 does not imply task success; adapters must inspect JSON status and verify artifacts/diff | Spike A findings (`PROJECT_DOCS/spikes/agent-clis.md`); agent CLIs exit 0 even on task-level failure |
| D22 | All GitHub operations (issues, labels, comments, pull requests) use the **GitHub CLI `gh`** through `os/exec`, behind a `GHRunner` interface in `provider/github`. Authentication comes from `gh auth login` or `GH_TOKEN`/`GITHUB_TOKEN`; Garagefab stores, reads, and logs no GitHub token. `git push` stays on system `git` with the user's credentials (`gh auth setup-git` is recommended). `gh` is required only for projects that set `github.repo` | No token configuration, no HTTP/GraphQL client to maintain, structured `--json` output, same execution model as `git`. Replaces D9 |
| D23 | **GitHub Projects v2 is not used.** Issue intake is triggered by an issue label (`github.intake_label`, default `garagefab`) plus exactly one `type:<work_type>` label. Stage feedback is written to the issue as one mutually exclusive `garagefab:*` state label plus comments, applied asynchronously by the poller | The dashboard already is the board; Projects v2 needs fragile GraphQL node IDs, broad classic tokens on personal accounts, and a manual board setup. Replaces the Project parts of D18. Spike B is cancelled |
| D24 | API-token rotation (`garagefab token rotate`) runs against a **stopped** daemon: it acquires the data-dir lock, refuses with a clear message if a server is running (override with `--force`), rewrites `api_token` atomically (`0600`), and deletes all sessions. The new bearer token takes effect on the next `garagefab start` (the running server keeps the in-memory token until then); sessions die immediately via row deletion | Bearer validation compares the in-memory token and Phase 1 has no config hot reload, so refusing by default prevents a half-rotated state where the config file and the running server disagree |
| D25 | Release artifacts are produced by **GoReleaser** in a tag-triggered GitHub Actions workflow: four targets (`darwin`/`linux` × `amd64`/`arm64`), `CGO_ENABLED=0`, version injected via the existing `internal/version` ldflags, and the UI built in a `before` hook so the embedded dashboard is present. Example launchd and systemd units ship at both user and system level; the README documents tmux, launchd, and systemd | GoReleaser is a build-time CLI with no Go dependency or runtime footprint, and cross-compiling four pure-Go targets is trivial with `CGO_ENABLED=0`. Closes plan P-7 |
| D26 | A `garagefab start --port N` override is **persisted** to `config.yaml` (after the pre-flight checks and single-instance lock succeed). Local clients that read the config — `garagefab open` and the `garagefab-work` skill (`gf-api.sh`) — therefore agree with the port the server actually binds. The flag is "sticky" until changed again | `config.yaml` is the single source of matching for local tools; without persistence, `--port` silently desynchronized `open` and the skill. Fixes an M8 run finding |

## 4. System Overview

```
┌──────────────────────────── garagefab (single binary) ────────────────────────────┐
│                                                                                   │
│  ┌──────────────┐   HTTP + SSE   ┌─────────────────────────────────────────────┐  │
│  │ Web UI (SPA) │◄──────────────►│ server (adapter)                            │  │
│  │ embedded     │                │  chi router · auth middleware · SSE hub     │  │
│  └──────────────┘                └──────────────────┬──────────────────────────┘  │
│                                                     │ calls                       │
│  ┌──────────────┐  creates jobs  ┌──────────────────▼──────────────────────────┐  │
│  │ intake       │───────────────►│ factory (business logic)                    │  │
│  │ poller 30 s  │                │  scheduler · pipeline state machine         │  │
│  │ GitHub+files │                │  repair loop · failure categorizer          │  │
│  └──────┬───────┘                │  evidence builder                           │  │
│         │                        └───────┬─────────────────────────┬───────────┘  │
│         │ uses                           │ interfaces              │ interfaces   │
│  ┌──────▼───────┐                ┌───────▼───────────┐     ┌───────▼───────────┐  │
│  │ provider/    │                │ store             │     │ worker            │  │
│  │ github       │                │ SQLite (WAL)      │     │ agent · command · │  │
│  └──────────────┘                │ goose migrations  │     │ worktree          │  │
│                                  └───────────────────┘     └───────┬───────────┘  │
└────────────────────────────────────────────────────────────────────┼──────────────┘
                                                                     │ os/exec
                         ┌───────────────────────────────────────────▼────────────┐
                         │ agy · opencode · git · gh · test/build/lint commands   │
                         │ running inside ~/.garagefab/worktrees/<project>/<job>  │
                         └────────────────────────────────────────────────────────┘
```

`provider/github` runs `gh` through `os/exec` (D22). `intake` uses it for issue intake and issue feedback; `factory` reaches it only through the `PullRequestProvider` port, wired in `cmd/garagefab`.

On disk:

```
~/.garagefab/
├── config.yaml                  # global settings (see §14)
├── garagefab.db                 # SQLite (WAL)
├── logs/<job-id>/<step-id>.log  # raw agent/command output
└── worktrees/<project>/<job-id> # isolated git worktrees
<project repo>/
└── .garagefab/
    ├── project.yaml             # commands, guardrails, agent selection
    └── intents/*-intent.md      # file-based job entry
<job worktree>/
└── .garagefab/jobs/<job-id>/    # committed artifacts (spec, reviews, evidence)
```

## 5. Technology Stack

| Layer | Choice | Notes |
|-------|--------|-------|
| Language | Go 1.22+ | |
| CLI | `spf13/cobra` | `start`, `status`, `open`, `install-skills`, `token rotate`, `version` |
| HTTP | `go-chi/chi/v5` | `RequestID`, `Logger`, `Recoverer`; `Timeout` **not** applied to SSE routes |
| Database | SQLite via `modernc.org/sqlite` | WAL, `busy_timeout=5000`, `synchronous=NORMAL`, `foreign_keys=ON` |
| Migrations | `pressly/goose` | SQL files embedded with `//go:embed` |
| Config | YAML | Global file and per-project file |
| Subprocesses | `os/exec` + `context` | Own process group per child |
| Git | System `git` via `os/exec` | No libgit2 or pure-Go git |
| GitHub | GitHub CLI `gh` via `os/exec` | Issues, labels, comments, PRs; `--json` output; minimum version pinned in M6 (D22) |
| UI | React 19, TypeScript, Vite, Tailwind CSS 4, shadcn/ui | Built to `ui/dist`, embedded with `//go:embed` |

Rule: adding a dependency requires a decision-log entry. `CGO_ENABLED=0 go build ./...` must always pass.

## 6. Package Layout and Dependency Rules

```
cmd/garagefab/            main.go — wiring only (constructs everything, injects interfaces)
internal/
  factory/                engine, scheduler, pipeline, repair_loop, failure, evidence
  worker/
    agent/                Agent interface + adapters: agy, opencode
    command/              command runner + built-in guardrails
    worktree/             git worktree lifecycle
  store/                  db, migrations/, repositories (jobs, steps, events, intake, processes)
  intake/                 poller: GitHub issues + intent-file scan → jobs; issue feedback reconciler
  provider/github/        gh CLI adapter: issues, labels, comments, PR creation
  server/                 router, auth, api_*, sse hub, embed
  config/                 global + project config loading and validation
ui/                       SPA source
```

**Dependency rules (enforced by review, ideally by an import-lint check in CI):**

1. `factory` defines the interfaces it needs (`Store`, `AgentRunner`, `CommandRunner`, `WorktreeManager`, `PullRequestProvider`). It imports none of `store`, `worker`, `provider`, `server`, or `intake`. It never runs a binary and never contains SQL.
2. `store` owns all SQL, transactions, and migrations. Nothing else imports `database/sql`.
3. `worker` owns process execution and git worktrees. It returns structured results and never touches `store`.
4. `server` and `intake` are adapters: they call `factory` (and read and write through `store`) but contain no pipeline logic. `intake` declares its own narrow GitHub interfaces (`IssueSource`, `IssueFeedback`).
5. `cmd/garagefab` is the only place that knows concrete types.
6. `provider/github` runs the `gh` CLI and returns its own DTOs. It imports none of `factory`, `store`, `server`, `worker`, or `intake`; `cmd/garagefab` adapts its types to the `factory` and `intake` ports.

## 7. Source of Truth

| Data | Lives in | Why |
|------|----------|-----|
| Intent text, `spec.md`, clarification Q&A, review report, evidence summary, human rejection notes | **Git worktree**, `.garagefab/jobs/<id>/`, committed on the job branch and included in the PR | Human-readable, reviewable, permanent |
| Job, step runs, approvals, events, process records, poller bookkeeping | **SQLite** | Fast queries, state transitions, dashboard |
| Raw stdout/stderr | **Files** in `~/.garagefab/logs/`, path stored in DB | Large, append-heavy; keeps the DB small and DBeaver-friendly |
| Global settings | `~/.garagefab/config.yaml` | |
| Commands, guardrails, agent selection, GitHub repository settings per project | `<repo>/.garagefab/project.yaml` | Versioned with the code |

If the DB and an artifact disagree, the artifact wins for content; the DB wins for current state.

## 8. Domain and State Model

### 8.1 Entities (field-level schemas belong to `spec.md`)

- **Project**: id, name, repo path, base ref, enabled work types.
- **Job**: id (integer, global), project, work type (`bug_fix|feature|refactor|docs`), title, source (`github_issue|intent_file|dashboard`) and source reference, **stage**, **status**, branch name, worktree path, timestamps.
- **StepRun**: job, stage, kind (`agent|command|gate`), attempt number, executor (agent or command name), started/ended, exit code, result (`success|fail|skipped`), failure category, log path.
- **Event**: autoincrement id, job, type, JSON payload, time. Source for the dashboard feed and SSE.
- **Approval**: job, gate (`spec_review|final`), decision, note, time.
- **ProcessRecord**: step run, pid, pgid, process start time, state. Used for crash recovery.
- **IntakeSeen**: project, source, external ref (`owner/repo#<n>` or intent-file path), content hash (informational only), created job. Unique per (project, source, ref), so it prevents duplicate jobs even when content changes (`spec.md` INT-4).
- **IntakeError**: project, source, ref, message, updated time. One row per failing item or provider; cleared when resolved. Shown on the Overview.
- **GitHubFeedback**: job, last applied feedback state. Lets the poller apply issue labels and comments idempotently (D23).
- **Session**: dashboard login session id, created/expires. Invalidated when the API token is rotated.

### 8.2 Stage × Status

**Stage** (from `intent.md`): `01_Intent`, `02_Clarification_and_Spec`, `03_Failing_Probe`, `04_Coding`, `05_Independent_Review`, `06_Human_Approval_Gate`, `07_Done`. A work type's profile selects which stages apply.

**Status** within the current stage:

| Status | Meaning | Uses an execution slot |
|--------|---------|------------------------|
| `queued` | Ready, waiting for a slot | no |
| `running` | An agent or command step is executing | **yes** |
| `needs_clarification` | Spec agent found the intent not actionable | no |
| `spec_review` | Draft spec awaiting human approval | no |
| `awaiting_approval` | Final gate, evidence ready | no |
| `interrupted` | Garagefab stopped while the job was running | no |
| `failed` | Escalated to the human (Blocked or Manual) | no |
| `cancelled` | Cancelled by the human; terminal | no |
| `done` | PR created; terminal | no |

### 8.3 Transitions (happy path, Feature profile)

| From | Event | To |
|------|-------|----|
| 01 / `queued` | slot available | 02 / `running` |
| 02 / `running` | intent not actionable | 02 / `needs_clarification` |
| 02 / `needs_clarification` | clarification supplied | 02 / `queued` |
| 02 / `running` | draft spec produced | 02 / `spec_review` |
| 02 / `spec_review` | human approves | 04 / `queued` |
| 04 / `running` | agent done, commands and guardrails pass | 05 / `queued` |
| 04 / `running` | command fails, category Flawed, retries left | 04 / `running` (repair attempt) |
| 04 / `running` | Blocked, or retries exhausted | 04 / `failed` |
| 05 / `running` | review report produced | 06 / `awaiting_approval` |
| 06 / `awaiting_approval` | human rejects (with note) | 04 / `queued` |
| 06 / `awaiting_approval` | human approves | 07 / `queued` |
| 07 / `queued` | slot available | 07 / `running` (delivery step: push + PR) |
| 07 / `running` | PR created or reused, worktree cleaned | 07 / `done` |
| 07 / `running` | delivery fails (Blocked) | 07 / `failed` (worktree kept; Retry re-runs delivery) |

> Other profiles follow the same transitions for their active stages; skipped stages are simply absent. Profile-specific differences: `bug_fix` adds stage 03 (probe) between 02 and 04; `refactor` starts at 04 (no 02/03); `docs` goes from 05 directly to 07 unless the review decision is `request_changes`, in which case it routes through 06 (see `spec.md` REV-6).

Rules:

- Only the factory mutates stage/status, inside a store transaction that also appends an `Event`.
- Every human action (Approve, Reject, Retry, Cancel) is validated against the current stage/status. Invalid actions are rejected with a conflict error.
- `failed` and `interrupted` jobs can be **Retry**ed (re-runs the current step) or **Cancelled**.
- The Review step does not reject: it reports risk scores and warnings. Rejection happens only at the human gate.
- Docs profile skips the gates the profile excludes; Phase 1 gate configuration is otherwise fixed as in `intent.md`.

## 9. Pipeline Engine

### 9.1 Profiles
Four fixed profiles define the active stages per work type (`spec.md` §4.3): `bug_fix` 01→02→03→04→05→06→07, `feature` 01→02→04→05→06→07, `refactor` 01→04→05→06→07, `docs` 01→04→05→07 (a review `request_changes` routes through 06). They are fixed, not config; Phase 1 enforces them imperatively in `Engine.ExecuteJob` (Phase 2 may load them from configuration).

### 9.2 Step execution
A step is one of:

- **Agent step**: the factory builds a prompt (from the step template, the approved spec, and — for repair or rejection — the error output or human note), then asks the `AgentRunner` to execute it in the worktree.
- **Command step**: ordered shell commands from `project.yaml`; exit codes decide pass/fail.
- **Gate step**: sets a waiting status and ends execution until a human action arrives.

### 9.3 Validate-and-repair loop
After the coding agent, guardrails run first (cheap and decisive), then command steps in order (build → test → lint). On failure:

1. Capture output and exit code.
2. Categorize deterministically (§9.4).
3. If **Flawed** and attempts < `max_repair_attempts` (default 3): invoke the coding agent again as a new session with the failing output; then re-run **all** command steps.
4. Otherwise set status `failed` with the category. The dashboard shows it as needing attention.

### 9.4 Failure categorization
A pure function in `factory`, no I/O, fully table-tested.

| Category | Signals | Action |
|----------|---------|--------|
| Flawed | Non-zero exit from test/build/lint commands with assertion or compile/lint output | Repair attempt |
| Blocked | Timeout, permission denied, connection refused, command not found, missing dependency, agent process failed to start | Escalate |
| Manual | Repair attempts exhausted | Escalate |

Guardrail violations (e.g., a protected file was modified) are **Flawed** once with a precise message (the agent can undo it), then count toward the attempt limit.

### 9.5 Guardrails
Guardrails are command steps that run after every agent step that changes code. The built-in guardrail `protected_paths` is implemented in Go in `worker/command`: it compares `git diff --name-status <step-start-sha>` against glob patterns from `project.yaml` and fails if an **existing** file matching a pattern was modified, deleted, or renamed. New files are allowed, so agents can still add tests while existing tests stay protected. Users may add their own shell guardrails. A second built-in guardrail, `test_paths`, applies only to the probe step (`bug_fix`, `GRD-5`): when set, every file the probe adds or changes must match one of those globs (the probe's own `probe.json` artifact is always allowed); an unmatched change is a violation. Empty `test_paths` means no restriction.

### 9.6 Evidence
The factory assembles the chain of evidence from existing records: step results, test summary, review report, diff stats, warnings count, the probe result for `bug_fix` jobs (`PRB-5`), and links to artifacts and logs. Nothing new is collected. The evidence summary is also written to `.garagefab/jobs/<id>/evidence.md` and committed, so it travels with the PR.

## 10. Concurrency Model

- **Scheduler:** a single goroutine selects `queued` jobs when `running` count < `max_concurrent_jobs` (global, default **5**). Each admitted job runs in its own goroutine under a cancellable context. Optionally, a project may set a lower `max_concurrent_jobs` in its `project.yaml`.
- **Slots:** only `running` jobs hold slots. Jobs waiting on a human hold none, but keep their worktree.
- **Same-project parallelism** is allowed; each job has its own worktree and branch (`garagefab/job-<id>`). Conflicts between concurrent jobs are not resolved by Garagefab; they surface as PR merge conflicts for the human.
- **Git serialization:** `git worktree add/remove` and branch operations on the same repository are serialized with a per-project mutex, because they take locks inside `.git`.
- **SQLite:** one dedicated write connection (serialized writes, short transactions) plus a read pool. `busy_timeout` covers external readers like DBeaver. No long-running transaction may wrap a subprocess.
- **Shutdown:** on SIGINT/SIGTERM the engine stops admitting jobs, signals agent process groups, waits briefly, then escalates to SIGKILL and marks the jobs `interrupted`.

## 11. Worker Layer

### 11.1 Agent adapter contract

```go
// Defined in factory; implemented in worker/agent.
type AgentRunner interface {
    Run(ctx context.Context, req AgentRequest, logs LogSink) (AgentResult, error)
}

type AgentRequest struct {
    Agent       string        // "agy", "opencode"
    Role        string        // "spec", "probe", "coding", "review"
    WorkDir     string        // job worktree
    Prompt      string
    Expect      []Artifact    // files the agent must produce, with schema id
    Timeout     time.Duration
}

type AgentResult struct {
    ExitCode  int
    TimedOut  bool
    Cancelled bool
    Duration  time.Duration
    LogPath   string
    Produced  []ArtifactRef // validated against schema
}

// Used by the dashboard's copy-paste helper.
type Handoff interface {
    Command(projectPath string, jobID int64) string // e.g. "cd … ; agy garagefab-work 178"
}
```

Adapter obligations:

1. Start a **fresh session**; never pass a "continue/resume" flag.
2. Run the child in its own process group; record pid, pgid, and start time before output is read.
3. Stream stdout/stderr line by line to the `LogSink`, which writes the log file and publishes SSE events. Memory use must not grow with output size.
4. Honor `ctx` cancellation and `Timeout`: SIGTERM to the group, then SIGKILL after a grace period.
5. Do not interpret agent output beyond the exit code; results come from validated artifact files.

Adapters never inject `AGENTS.md`/`GEMINI.md`; the agent reads project instructions from the repo itself.

### 11.2 Output contract (D13)
For each agent role the engine defines the expected artifact and a schema, for example `spec.md` (required headings), `review.json` (decision, risk scores, warnings). After the process exits the engine validates the files. A zero exit code with a missing or invalid artifact is a **Flawed** result for coding-adjacent steps, or **Blocked** if it repeats.

### 11.3 Command runner
Runs `sh -c <command>` in the worktree with timeout, own process group, streamed logs, and a captured exit code. The test/build/lint commands come from `project.yaml`.

### 11.4 Worktree lifecycle
1. **Create** at the job's first agent step (spec step for Feature and Bug Fix, coding step for Refactor and Docs):
   1. `git fetch <remote>` for the remote named in `base_ref` (default `origin`), with a timeout.
   2. `git worktree add -b garagefab/job-<id> <path> <base_ref>` (default `origin/main`).
   3. If the fetch fails (offline, auth, VPN), append a **warning event** and fall back to the local branch of the same name. The fallback is visible on the job in the dashboard. A missing remote or branch is `Blocked`, not a silent fallback.
2. **Use:** all agent and command steps run there; artifacts are committed on the job branch.
3. **Publish:** after final approval, push the branch and create the PR (§12).
4. **Clean up:** `git worktree remove --force` after PR creation or cancellation. Failed or interrupted jobs keep their worktree until retried or cancelled.

## 12. Intake and GitHub Integration

### 12.1 Single poller (every 30 s, configurable)
One loop, per registered project:

1. **GitHub issues** (only for projects with `github.repo`): list open issues carrying `github.intake_label` (default `garagefab`) with `gh issue list --json`. The work type comes from exactly one `type:<bug_fix|feature|refactor|docs>` label. **A missing, unknown, or duplicate `type:` label creates no job and records an intake error; the issue is not marked seen, so fixing its labels creates the job on a later cycle.** A valid issue creates a job if `owner/repo#<n>` is not in `IntakeSeen`.
2. **Intent files:** scan `<repo>/.garagefab/intents/*-intent.md` in the project's main checkout. A file creates a job if its path is not in `IntakeSeen` (the content hash is stored for information only). Garagefab only reads these files; it never edits or moves them.
3. **Issue feedback reconcile** (D23): for each issue-sourced job whose (stage, status) maps to a feedback state different from the last applied one (`GitHubFeedback`), set exactly one `garagefab:*` state label on the issue (removing the others) and post a comment where the state needs human attention or is terminal. Required labels are created idempotently (`gh label create --force`). Feedback runs here, asynchronously, so a slow or failing `gh` call never blocks the scheduler or a store transaction; it never changes job state. Skipped for projects without `github.repo`.

Dashboard-created jobs go straight to the factory. Poller errors are logged and surfaced on the Overview page (`IntakeError`); they never crash the service.

### 12.2 Publishing
Delivery is a scheduled step in stage 07, after the Final Approval gate:

1. Push the job branch with system `git` and the user's own git credentials (`gh auth setup-git` is recommended). The remote is the remote part of `base_ref` (`origin/main` → `origin`); for a local `base_ref` (e.g. `main`) it is `origin`.
2. Look for a PR for the branch with `gh pr list --head <branch> --state all`. An open PR is reused. A merged or closed PR fails delivery as Blocked; Garagefab never opens a duplicate.
3. Otherwise create the PR with `gh pr create --body-file -`.
4. Any failure (no `github.repo`, `gh` missing or not authenticated, push rejected, network) is Blocked: `07/failed`, worktree kept, Retry re-runs the whole delivery idempotently.

`git push` runs in `worker/worktree`; PR calls run in `provider/github`. The factory only orchestrates through ports.

### 12.3 Authentication

- Garagefab never reads, stores, or logs a GitHub token. `gh` uses its own login (`gh auth login`, OS keyring) or `GH_TOKEN` / `GITHUB_TOKEN` from Garagefab's environment.
- The `gh` subprocess inherits Garagefab's environment (it needs `HOME` and keyring access), plus `GH_PROMPT_DISABLED=1`, `GH_NO_UPDATE_NOTIFIER=1`, and `NO_COLOR=1`.
- Agent and command subprocesses get the allow-listed environment (`spec.md` SEC-6). `GH_TOKEN` and `GITHUB_TOKEN` are not on it and are refused in `engine.env_passthrough`.
- Agents run on the same machine and could call `gh` themselves with the user's login. This is covered by the existing Phase 1 scope limit (no agent sandboxing, `spec.md` §9).
- Startup: if any registered project sets `github.repo`, Garagefab checks `gh --version` against the pinned minimum and runs `gh auth status`. A failure is a **warning plus an Overview provider error, not a fatal exit**, so one project's GitHub setup cannot stop local-only work.
- Without `github.repo`, GitHub intake and feedback are disabled for that project; intent files and dashboard entry still work, and delivery fails as Blocked with a message naming the missing setting.

## 13. Human Interaction and the Skill

- The dashboard builds the copy-paste command via the configured agent's `Handoff` (e.g. `cd /path/to/project ; agy garagefab-work 178`).
- `garagefab install-skills` writes the `garagefab-work` skill into each supported agent's global config directory. The skill reads `GET /api/jobs/{id}` (intent, draft spec, error logs, worktree path and status) and writes back through the same API.
- The API token is created at first run and stored in `~/.garagefab/config.yaml`; the skill reads it from there.
- Clarification answers and spec edits made in the agent session are saved as artifacts in the job's worktree and referenced via an API call that moves the job on (`needs_clarification → queued`, or spec edits before approval).
- Rejection notes entered in the dashboard are saved as an artifact and included in the next coding prompt.

## 14. Configuration

Global `~/.garagefab/config.yaml`:

```yaml
server:
  listen: 127.0.0.1:7878        # loopback only; non-loopback values are refused
  api_token: "<generated>"
engine:
  max_concurrent_jobs: 5
  max_repair_attempts: 3
  poll_interval: 30s
  step_timeouts: { agent: 30m, command: 10m }
  env_passthrough: []           # extra env var names passed to agents (allow-list)
```

There is no global `github` section: `gh` owns GitHub authentication (§12.3).

Per project `<repo>/.garagefab/project.yaml`:

```yaml
base_ref: origin/main            # fetched before each job; use "main" to stay local
agents:                          # agent per role
  spec: agy
  probe: opencode               # optional, defaults to coding
  coding: opencode
  review: agy
commands:
  build: ["go build ./..."]
  test:  ["go test ./..."]
  lint:  ["golangci-lint run"]
guardrails:
  protected_paths: ["**/*_test.go"]   # existing matching files cannot be modified; new files are allowed
  test_paths: []                       # probe step (bug_fix) may only add/change files matching these globs; empty = no restriction
github:                          # optional; enables GitHub intake, issue feedback, and delivery
  repo: owner/name
  intake_label: garagefab        # issues with this label are picked up
  pr_issue_keyword: closes      # closes | refs
max_concurrent_jobs: 3           # optional, lower than the global limit
```

Validation runs at startup and when a project is added; errors name the file and key.

## 15. HTTP Server, Realtime, Embedding

- **Binding and auth:** loopback only. Requests with a non-local `Host` header are rejected (DNS-rebinding defense). All `/api` routes require authentication except the login endpoint and static UI shell.
  - **Dashboard:** `garagefab start` (and `garagefab open`) opens `http://127.0.0.1:<port>/login#token=<api_token>`. The login page posts the token to `POST /api/session`, removes the fragment with `history.replaceState`, and the server sets a session cookie (`HttpOnly`, `SameSite=Strict`, 30-day expiry stored in the `Session` table). JavaScript never holds the token afterwards.
  - **Skill and CLI:** `Authorization: Bearer <api_token>`.
  - **CSRF:** cookie-authenticated `POST/PUT/DELETE` requests must carry an `Origin` header equal to the server's own origin.
  - Rotating the API token invalidates all sessions (row deletion) and rewrites `api_token`; the new bearer token takes effect on the next `garagefab start` (see §3 D24).
- **Routes:** `/` serves embedded `ui/dist` with SPA fallback; `/api/...` is JSON; endpoint list lives in `spec.md`.
- **SSE:**
  - One global event stream (`/api/events`) carries state changes, from the `events` table; each message `id` is the event row id, so `Last-Event-ID` replays missed events.
  - One log stream per open step (`/api/jobs/{id}/steps/{stepId}/log`) tails the log file; `id` is the line number so a reconnect resumes at the right line.
  - Heartbeat comment every 15 s. Flush after every write. `middleware.Timeout` and response compression are not applied to SSE routes.
- **Embedding:** `//go:embed all:ui/dist` in `internal/server`. The Makefile builds the UI before `go build`.

## 16. Persistence Details

- Location: `~/.garagefab/garagefab.db`, outside project repos.
- Migrations: `internal/store/migrations/NNNN_name.sql`, forward-only, applied at startup before any other component runs.
- Log files: `~/.garagefab/logs/<job-id>/<step-id>.log`, line format `RFC3339 stream text`. Steps that finish may be gzip-compressed. Retention policy: Phase 2.
- Startup integrity: the process refuses to run if the DB schema is newer than the binary.

## 17. Crash Recovery

An **orphan agent** is a child process still running after Garagefab died. On every startup, before the scheduler starts:

1. Load `ProcessRecord`s still marked active.
2. For each, check whether the pid exists **and** its start time matches the record (guards against PID reuse). If so, terminate the whole process group.
3. Mark the affected steps `fail` with category `Blocked` and reason `interrupted`, set their jobs to `interrupted`, append events.
4. Do **not** touch worktrees or restart anything. The human chooses Retry or Cancel in the dashboard.
5. Reconcile orphaned worktrees: worktrees on disk without a live job are listed on the Overview page for manual cleanup, not deleted automatically.

## 18. Security and Trust Model

- **Threat model:** a local, single-user tool whose agents execute arbitrary commands with the user's privileges. Garagefab does not sandbox agents in Phase 1 (security sandboxes are Phase 2). Users must only register repositories they trust.
- Loopback binding + API token + Host check protect the control plane.
- Agents receive an allow-listed environment; `GH_TOKEN`, `GITHUB_TOKEN`, and the API token are not included.
- Agent processes run in the job worktree; the base repo checkout is never their working directory.
- Guardrails and the human gates are the safety net against unintended changes; Garagefab never merges.

## 19. Observability

- Structured application logs via `log/slog` (JSON to stderr; file rotation is Phase 2).
- `Event` rows give the per-job timeline; step records give timing and results.
- `garagefab status` prints running jobs and attention items from the DB without needing the UI.

## 20. Testing Strategy

- **Unit tests:** pipeline transitions, failure categorizer, config validation, prompt builders (pure Go, no I/O).
- **Store tests:** real SQLite on a temp directory, migrations applied for real.
- **Worker tests:** a **fake agent** (a tiny test binary or script) that can succeed, write bad artifacts, hang, ignore SIGTERM, crash, or spawn children. This exercises timeouts, process groups, and orphan handling without real LLMs.
- **Engine tests:** fake `AgentRunner` and fake `CommandRunner`; assert state transitions and events.
- **Provider tests:** `provider/github` unit and integration tests run against a thread-safe `FakeGHRunner` with zero network access.
- **End-to-end:** the two MVP scenarios from `intent.md` against a temp git repo, a fake agent, and `FakeGHRunner`.
- CI runs `CGO_ENABLED=0 go build ./...`, `go vet`, `go test ./...`, the UI build, and the import-boundary check.

## 21. Build and Distribution

- `make build`: build UI → `go build` with `CGO_ENABLED=0` → `./bin/garagefab`.
- Release artifacts are built by **GoReleaser** on a `v*` tag (D25); `make release-snapshot` builds them locally.
- Release targets: `darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`.
- Paths use `filepath.Join`/`Clean` everywhere; no hardcoded separators.
- Prerequisites on the user's machine: `git` and the chosen agent CLIs only.
- Phase 1 runs in the foreground. The README documents running it under tmux, launchd, and systemd, with example files; built-in daemonization is Phase 2.

## 22. Phase 2 Extension Points

| Future feature | Where it plugs in |
|----------------|-------------------|
| Additional agents / API-based agents | New `AgentRunner` adapter |
| Additional issue trackers | `IssueProvider` interface beside `provider/github` |
| Factory Brain (FTS5) | New `store` repository and a prompt-builder hook in `factory` |
| Token/cost tracking | Optional fields on `AgentResult` and `StepRun` |
| Custom work types / step config | Profiles loaded from config instead of Go values |
| AI failure categorization | Second implementation of the categorizer interface |
| OS notifications | Subscriber on the event stream |
| Per-test result counts | Test report parsers in `worker/command`, shown in the evidence |
| Background mode / service installation | New `cmd` subcommands (`install-service` for launchd/systemd) |
| Security sandboxes | Alternate process launcher behind the worker interfaces |
| Per-stage reasoning effort | Profile configuration and `worker/agent` adapter flags (`--effort`, `--variant`) |
| ReAct loop context injection | Prompt builder in `factory` injecting target files and error diffs |
| Live agent tool streaming | NDJSON event parser in `worker/agent` forwarded to `server` SSE hub |
| Token accounting & smart abort | `step_runs` table schema extension and scheduler anomaly timeout |

## 23. Open Questions

| # | Question | Needed by |
|---|----------|-----------|
| O5 | Exact schemas for `review.json` and the required `spec.md` headings. To be researched and decided when the spec stage is detailed. | `spec.md` |

Resolved: O1 (worktree at first agent step; `intent.md` updated), O2 (D21; headless flags, prompt args, fresh session in `PROJECT_DOCS/spikes/agent-clis.md`), O3 (D17), O4 (D18), O6 (D19).
