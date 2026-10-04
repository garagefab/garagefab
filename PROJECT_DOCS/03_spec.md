# Garagefab — Specification

> Version: 0.2 (draft) · Phase 1 (MVP)
> Inputs: `intent.md` (what and why), `architecture.md` (how it is built).
> This document defines **observable behavior and acceptance criteria**. It does not define task order or schedule (see `plan.md`).

## 0. About This Document

### 0.1 Naming

- **"The Spec"** means this document.
- **"Job spec"** means the per-job `spec.md` that the spec agent produces at `.garagefab/jobs/<job-id>/spec.md` inside a job's worktree. Do not confuse the two.

### 0.2 Conventions

- **Requirement IDs** are stable: `AREA-n` (e.g., `COD-3`). Tests, plan items, and commits reference them.
- **Priority:** **M** = required for the MVP (the two scenarios in `intent.md`) or to keep the pipeline safe; **S** = should; **C** = could.
- **Acceptance criteria** are written as *Given / When / Then* and must be testable with a fake agent (see `architecture.md` §20).
- "The system" is the Garagefab binary. Timestamps are stored in UTC and shown in local time. Agent roles are `spec`, `probe`, `coding`, `review`.
- Stage, status, and entity names follow `architecture.md` §8.

### 0.3 How to read the requirement tables

Each requirement row has: **ID · Priority · Requirement · Acceptance criteria**. Where a requirement depends on an open decision, it references an item in §11.

## 1. Overview

Garagefab is a local, single-binary AI software factory. A developer registers local Git projects, submits **jobs** (intent + work type), and Garagefab runs each job through a deterministic pipeline of agent steps, command steps, and human gates inside an isolated Git worktree, producing a Pull Request with a chain of evidence. See `intent.md` for vision and `architecture.md` for structure.

## 2. Glossary

| Term | Meaning |
|------|---------|
| Project | A registered local Git repository with a `.garagefab/project.yaml` |
| Job | One unit of work with an intent, a work type, a stage, and a status |
| Work type | `bug_fix`, `feature`, `refactor`, `docs` — selects the pipeline profile |
| Stage | One of `01_Intent` … `07_Done` |
| Status | State within a stage: `queued`, `running`, `needs_clarification`, `spec_review`, `awaiting_approval`, `interrupted`, `failed`, `cancelled`, `done` |
| Step | One execution inside a stage: an agent step, a command step, or a gate |
| Agent step | A fresh AI agent CLI process run in the worktree |
| Command step | Deterministic shell commands whose exit codes decide pass/fail |
| Gate | A point where the job waits for a human action |
| Checkpoint commit | A commit Garagefab makes on the job branch after each successful agent step |
| Artifact | A file produced by a step, stored under `.garagefab/jobs/<job-id>/` in the worktree |
| Chain of evidence | The summary and drill-down shown at the final gate, built from existing artifacts |
| Handoff command | The copy-paste terminal command that opens a coding agent on a job |
| Slot | One unit of the global concurrency limit; only `running` jobs hold one |

## 3. Actors and Key Flows

**Actors:** the **Developer** (the only human), **Garagefab**, **Agent CLIs** (`agy`, `opencode`), **GitHub** (issues, pull requests).

### F1. Happy path (Feature, from a GitHub issue)

1. Developer creates an issue with labels `garagefab` and `type:feature`.
2. Poller creates a job (`01_Intent`/`queued`). A slot frees; the worktree is created; the spec agent runs (`02`/`running`).
3. Spec agent finds the intent actionable and writes a draft job spec → `02`/`spec_review`.
4. Developer approves in the dashboard → `04`/`queued` → `running`.
5. Coding agent runs; guardrails and commands pass; checkpoint commit → `05`/`running`.
6. Review agent writes `review.json` → `06`/`awaiting_approval` with evidence.
7. Developer approves → `07`/`running`: push, create PR, clean up worktree → `07`/`done`.

### F2. Clarification

At step 3 the spec agent writes questions instead of a spec → `02`/`needs_clarification`. The developer copies the handoff command, answers in the agent session (the skill posts the answers), the job returns to `02`/`queued`, and the spec agent runs again in a fresh session with the answers.

### F3. Validate-and-repair

A command fails after coding. Category **Flawed** → coding agent re-run with the error output, up to `max_repair_attempts`. Exhausted → `04`/`failed` (category Manual). **Blocked** → `04`/`failed` immediately.

### F4. Rejection

At the final gate the developer rejects with a note → `04`/`queued`; the note is given to the next coding run.

### F5. Intent file

A file `*-intent.md` appears in `<repo>/.garagefab/intents/`; the next poll creates a job.

### F6. Dashboard creation

The developer picks a project and work type, enters a title and intent text; a job is created immediately.

### F7. Interrupted job

Garagefab stops while a job runs. On the next start the job is `interrupted`; the developer chooses Retry or Cancel.

## 4. Functional Requirements

### 4.1 Projects (PRJ)

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| PRJ-1 | M | The developer can register a project in the dashboard by giving a repository path. | Given a path to a Git repository, when "Add Project" is submitted, then the project appears in the project list with name (default: directory name), path, and enabled work types. |
| PRJ-2 | M | Registration validates the repository. | Given a path that does not exist, is not a Git repository, or whose `base_ref` cannot be resolved locally, when submitted, then it is rejected with a message naming the failed check and nothing is stored. |
| PRJ-3 | M | Registration reads `<repo>/.garagefab/project.yaml`. | Given the file is missing, when submitted, then registration is rejected and the response includes a template. |
| PRJ-4 | M | `project.yaml` is validated; errors name the file and key. `github.repo` must match `owner/name`; `github.pr_issue_keyword` must be `closes` or `refs`. A missing or unauthenticated `gh` binary is reported as a warning/intake error, not a project registration rejection. | Given `agents.coding: foo`, then the error is `project.yaml: agents.coding: unknown agent "foo" (supported: agy, opencode)`. Given `github.repo: invalid`, then the error names the key. |
| PRJ-5 | M | Project name and repository path are unique. | Given a second registration with the same path or name, then it is rejected. |
| PRJ-6 | M | A job uses a snapshot of the project configuration taken when the job is created (read from the main checkout). | Given `project.yaml` is edited while job 7 runs, then job 7 keeps its snapshot and jobs created afterwards use the new file. |
| PRJ-7 | S | The dashboard can create `project.yaml` from the template. | Given no file, when "Create template" is clicked, then the template is written to the main checkout and the form re-validates. |
| PRJ-8 | S | A project can be archived. | Given a project with non-terminal jobs, then archiving is refused; otherwise the project is hidden from the board and its job history is kept. |

### 4.2 Job Intake (INT)

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| INT-1 | M | **Dashboard entry:** the developer creates a job with project, work type, title, and intent text. | Given valid input, when submitted, then a job exists at `01_Intent`/`queued` and the intent text is stored; it is written to `.garagefab/jobs/<id>/intent.md` when the worktree is created. |
| INT-2 | M | **Intent-file entry:** each poll scans `<repo>/.garagefab/intents/*-intent.md` (`intents_dir` configurable). The file has YAML front matter with `type` (`bug_fix\|feature\|refactor\|docs`) and optional `title`; the body is the intent. Without `title`, the first `# ` heading, then the file name, is used. | Given a new valid file, when the next poll runs, then exactly one job is created. Given a missing or invalid `type`, then no job is created and an intake error naming the file appears on the Overview. |
| INT-3 | M | **GitHub entry:** each poll lists open issues carrying `github.intake_label` (default `garagefab`) via `gh issue list --json`. An issue with exactly one valid `type:<bug_fix\|feature\|refactor\|docs>` label creates a job from its title and body; `source=github_issue`, `source_ref=owner/repo#<n>`. **Missing, unknown, or multiple `type:` labels → no job, and an intake error naming the issue appears on the Overview. The issue is not marked seen, so fixing the labels creates the job on a later cycle.** | Given a new issue with `garagefab` and `type:feature`, when the next poll runs, then one job is created and linked to `owner/repo#<n>`. Given missing or multiple `type:` labels, then no job is created and an intake error is shown. |
| INT-4 | M | Intake is idempotent. At most one job is created per intent file path and per GitHub issue reference (`owner/repo#<n>`). | Given the same file path or issue ref is seen again (even if its content changed), then no second job is created and a log event notes it. |
| INT-5 | M | Intake problems never stop the service and are visible. | Given `gh` is missing, not authenticated, rate-limited, or network fails, then the poller records an intake error on the Overview, keeps running, and retries on the next cycle. |
| INT-6 | M | One poller loop (default every 30 s, configurable) serves GitHub and intent files for all projects. | Given two projects, then both are scanned in each cycle; a cycle never overlaps the previous one. |
| INT-7 | S | A newly created job is visible in the dashboard within one cycle plus one second. | Given a new intent file, then the board shows the card at most `poll_interval + 1 s` later. |

### 4.3 Pipeline and Profiles (PIP)

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| PIP-1 | M | Four fixed profiles define active stages (see table below). | Given a `docs` job, then stages `02`, `03`, and `06` never occur. |
| PIP-2 | M | Stage and status change only through the state machine in `architecture.md` §8.3, in a single transaction that also records an event. | Given a crash between a state change and its event, then neither is visible after restart. |
| PIP-3 | M | Every human action is validated against the current stage and status. | Given Approve on a `running` job, then the response is `409 invalid_state` and nothing changes. |
| PIP-4 | M | Actions are idempotent against double submission. | Given Approve is sent twice quickly, then the first succeeds and the second returns `409`; exactly one approval is recorded. |
| PIP-5 | M | A job runs at most one step at a time. | Given a job in `04`/`running`, then no second agent or command step for that job runs concurrently. |
| PIP-6 | M | Cancel is allowed in any non-terminal state. | Given a `running` job, when cancelled, then its process group receives SIGTERM then SIGKILL after the grace period, the status becomes `cancelled`, and the worktree is removed. |
| PIP-7 | M | Retry is allowed in `failed` and `interrupted` and re-runs the current step from its last checkpoint (see WKT-5). | Given `04`/`failed`, when Retry is clicked, then the job returns to `queued` and the step runs again as a fresh session. |

**Profiles**

| Work type | Stages | Notes |
|-----------|--------|-------|
| `bug_fix` | 01 → 02 → 03 → 04 → 05 → 06 → 07 | Probe before coding |
| `feature` | 01 → 02 → 04 → 05 → 06 → 07 | |
| `refactor` | 01 → 04 → 05 → 06 → 07 | No spec stage; review reference is the intent (OQ-5) |
| `docs` | 01 → 04 → 05 → 07 | Only guardrails run in `04` (no build/test/lint); no approval gate (OQ-2) |

### 4.4 Clarification and Spec (SPC) — stage 02

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| SPC-1 | M | The spec agent runs in a fresh session with the intent and any prior clarification record, and produces **exactly one** of: `clarification-questions.md` or `spec.md` under `.garagefab/jobs/<id>/`. | Given both files, neither, or an invalid file, then the step fails as Flawed and is re-run (counts toward `max_repair_attempts`). |
| SPC-2 | M | The agent must never assume on ambiguity; the step prompt states this, and the questions file is the only allowed outcome for non-actionable intent. | Given a fake agent that writes questions, then the job becomes `02`/`needs_clarification` and the questions are shown on the job. |
| SPC-3 | M | The clarification answers are accepted via the API (used by the skill) and stored as `clarification.md` (question and answer pairs), committed as a checkpoint. | Given answers posted for a `needs_clarification` job, then `clarification.md` exists, the job returns to `02`/`queued`, and the next spec run receives it. |
| SPC-4 | M | A draft job spec must have the required structure (§6.1). | Given a spec missing `Acceptance Criteria`, then validation fails and the step is Flawed. |
| SPC-5 | M | A valid draft is committed as a checkpoint and the job moves to `02`/`spec_review`. | Given a valid draft, then `spec_review` is set and the draft is viewable in the dashboard. |
| SPC-6 | M | Approve at `spec_review` re-reads and re-validates the spec file **as it is in the worktree** (the developer may have refined it), commits it, records its SHA-256, and moves to the next stage in the profile. | Given the developer edited the spec through the agent, when Approve is clicked, then the approval records the hash of the edited file. Given the edited file is invalid, then Approve returns `422 validation_failed` with the reasons. |
| SPC-7 | M | The pipeline never proceeds past stage 02 without an explicit Approve. | Given a valid draft and no action, then the job stays in `spec_review` indefinitely. |
| SPC-8 | S | The dashboard shows the spec rendered as Markdown. | Given a job in `spec_review`, then the job page renders the draft spec. |

### 4.5 Failing Probe (PRB) — stage 03, bug fix only

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| PRB-1 | M | The probe agent writes one or more failing tests in the worktree and `probe.json` (§6.3) giving the exact command to run them and the test files added. | Given a missing or invalid `probe.json`, then the step is Flawed and re-run. |
| PRB-2 | M | The system runs the probe command itself, records stdout/stderr and exit code as evidence, and requires a **non-zero** exit. | Given the probe command exits 0 (bug not reproduced), then the step is Flawed with the message "probe passed; it must fail", and the agent is re-run with that feedback. |
| PRB-3 | M | After `max_repair_attempts` failed probes the job becomes `03`/`failed` (category Manual). | Given three probes that pass, then the job is `failed` and visible in "attention". |
| PRB-4 | M | A valid probe is committed as a checkpoint and the job moves to `04`. | Given a failing probe, then stage `04` follows. |
| PRB-5 | S | Whether the probe fails "for the right reason" is not decided by the system; it is part of the review input and evidence (§4.8). | Given the evidence view, then the probe's output is shown next to the review. |

### 4.6 Coding and Validate-and-Repair (COD) — stage 04

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| COD-1 | M | The coding agent runs in a fresh session with: the approved job spec (or the intent for refactor/docs), repair feedback if any, and the latest human rejection note if any. | Given a repair run, then the prompt contains the previous failure output and no conversation from the earlier session. |
| COD-2 | M | After each coding run the system executes, in order: guardrails (§4.7), then `commands.build`, `commands.test`, `commands.lint` (those configured), stopping at the first failure. Commands run via `sh -c` in the worktree. | Given build fails, then test and lint are not run and the failure output is stored. |
| COD-3 | M | Unconfigured command groups are `skipped` and the evidence shows "no tests configured" when `test` is absent. | Given no `commands.test`, then the step result for test is `skipped` and a warning appears in the evidence. |
| COD-4 | M | Failures are categorized deterministically (`architecture.md` §9.4). **Flawed** triggers a repair run while attempts remain (default 3); **Blocked** and **Manual** escalate to `04`/`failed`. | Given a failing test and attempts left, then the coding agent is re-run; given a command timeout, then the job is `failed` immediately with category Blocked. |
| COD-5 | M | The attempt counter counts repair runs of the current coding phase (guardrail violations and invalid outputs included). It resets after a human rejection or a manual Retry. | Given two failures then a rejection at the gate, then the next coding phase starts with a counter of 0. |
| COD-6 | M | After a repair run all command groups run again from the start. | Given lint failed and was repaired, then build and test run again before the step passes. |
| COD-7 | M | A coding run that produces no change against the step-start SHA is Flawed ("no changes made"). | Given an agent that exits 0 without editing files, then the step is Flawed. |
| COD-8 | M | For `bug_fix`, after the normal commands pass, the probe command must now exit 0, and the probe files are protected from modification during coding. | Given the coding agent edits a probe file, then the step is a guardrail violation (Flawed). Given the probe still fails after coding, then the step is Flawed. |
| COD-9 | M | When all checks pass, the system makes a checkpoint commit of all changes (`garagefab(job-<id>): coding`) and moves to `05`. Agents are told not to commit; if one did, diffs are still computed from the step-start SHA. | Given an agent that committed itself, then guardrail and diff results are identical to the uncommitted case. |
| COD-10 | M | Step timeouts apply (`timeouts.agent`, `timeouts.command`). | Given an agent that hangs, then after the timeout the process group is terminated and the failure is categorized Blocked. |

### 4.7 Guardrails (GRD)

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| GRD-1 | M | `guardrails.protected_paths` globs: an **existing** file (at step-start SHA) matching a pattern must not be modified, deleted, or renamed. New files are allowed. | Given pattern `**/*_test.go` and an agent that edits `a_test.go`, then the guardrail fails and lists the file. Given a new `b_test.go`, then it passes. |
| GRD-2 | M | Guardrails run after every agent step that is allowed to change code (probe, coding). | Given a probe step that modifies a non-test file, then it is reported (see GRD-4). |
| GRD-3 | S | `guardrails.commands`: extra shell checks run after the protected-path check; non-zero exit is a violation. | Given a custom command that exits 1, then the step is Flawed with its output. |
| GRD-4 | M | A violation is Flawed once per attempt (the agent can undo it) with a message listing the offending files, and counts toward the attempt limit. | Given a violation, then the repair prompt names the files and instructs the agent to restore them. |
| GRD-5 | M | The probe step may only add or change test files and `probe.json`; changes elsewhere are a violation. How "test file" is determined is configured by `guardrails.test_paths` (default: none, meaning no restriction). | Given `test_paths` is set and the probe step modifies `src/main.go`, then the step is Flawed. |

### 4.8 Independent Review (REV) — stage 05

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| REV-1 | M | The review agent runs in a fresh session with no input from the coding session. Its inputs are the job spec (or intent), the diff from the merge base, the command results summary, and, for bug fixes, the probe result. | Given the prompt, then it contains no coding-agent output text. |
| REV-2 | M | The agent writes `review.json` (§6.2), validated against the schema. | Given invalid JSON, a missing field, or a score outside 1–5, then the step is Flawed and re-run (counts toward attempts; exhausted → `05`/`failed`, Manual). |
| REV-3 | M | The review is scope-bound: findings about the change itself go in `findings`; pre-existing debt and issues in untouched code go in `warnings` and never block. | Given a fake review with a warning, then the job still reaches the next stage and the warning is counted in the evidence. |
| REV-4 | M | The review step must not change the repository. After it, the working tree must be unchanged except for `review.json`. | Given a review agent that edits a source file, then the changes are reverted to the step-start SHA and the step is Flawed. |
| REV-5 | M | On success, the system writes a checkpoint commit and proceeds: to `06`/`awaiting_approval` (profiles with the gate), or, for `docs`, to `07` (see OQ-2). | Given a feature job, then it becomes `awaiting_approval`. |
| REV-6 | M | A review decision of `request_changes` does not loop automatically in Phase 1. It is shown prominently in the evidence. For `docs` (no gate), `request_changes` routes the job to `06`/`awaiting_approval` instead of `07`. | Given `request_changes` on a feature job, then the job is `awaiting_approval` with the decision displayed. Given it on a docs job, then the job is `06`/`awaiting_approval`. |

### 4.9 Final Approval and Evidence (APR) — stage 06

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| APR-1 | M | The summary view shows: build status, test results (pass/fail; per-test counts are Phase 2), review decision and risk scores, files changed, and the warnings count. | Given a job whose commands passed, then "tests passed" is shown; given a failed run, then "tests failed" is shown with a link to the command output. |
| APR-2 | M | The drill-down offers on demand: full diff, command output, the full review report with warnings, agent logs, the job spec, and the probe result (bug fix). | Given each item, then it can be opened from the job page and none is loaded by default. |
| APR-3 | M | Evidence is assembled only from stored artifacts and step records. | Given the evidence endpoint, then no new agent or command is run. |
| APR-4 | M | The evidence summary is also written to `.garagefab/jobs/<id>/evidence.md` and committed. | Given an approved job, then the PR contains `evidence.md`. |
| APR-5 | M | Approve records the worktree `HEAD` SHA shown in the evidence. If `HEAD` differs at approval time, Approve returns `409 stale_evidence`. | Given someone commits in the worktree after the evidence was built, when Approve is clicked, then it is rejected. |
| APR-6 | M | Reject requires a non-empty note, stores it as `rejections/<n>.md`, and sends the job to `04`/`queued`. | Given Reject with an empty note, then `422`; with a note, then the next coding prompt contains it. |
| APR-7 | M | Approve and Reject are accepted only from the dashboard session, not from the bearer token (SEC-4). | Given a request with only the bearer token, then `403 forbidden`. |

### 4.10 Delivery (DLV) — stage 07

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| DLV-1 | M | After approval, the system pushes `garagefab/job-<id>` to the remote named in `base_ref` (for a local `base_ref` such as `main`, it pushes to `origin`, P2) and opens a PR against the base branch using `gh pr create --body-file -`. Title = job title; body = summary, evidence summary, and links to artifacts; issue-sourced jobs add `<keyword> #<n>` where the keyword is `github.pr_issue_keyword` (`closes` by default, or `refs`). Push uses system `git` with the user's git credentials (`gh auth setup-git` is recommended). | Given an approved job, then a PR exists with that title and body and the job stores its URL. Given an issue-sourced job with the default setting, then the body contains `Closes #<n>`; with `refs`, it contains `Refs #<n>`. |
| DLV-2 | M | Delivery is idempotent. PR detection queries `gh pr list --head <branch> --state all`. An existing **open** PR is reused. A merged or closed PR fails delivery as `Blocked` (never open duplicate PRs). | Given Retry after a failure between push and PR creation, then exactly one PR exists. Given a closed PR, Retry fails Blocked. |
| DLV-3 | M | Delivery failures (`gh` missing, unauthenticated, rate limit, push rejected, network error) are categorized Blocked → `07`/`failed` with a clear message. Worktree is kept for retry. | Given `gh` auth failure or network down, then the job is `failed` and the message explains the cause. |
| DLV-4 | M | On success the worktree is removed, the local branch is kept, the status becomes `done`, and the issue (if any) gets label `garagefab:delivered` plus a PR link comment (best effort, GHB-5). | Given a delivered job, then the worktree path no longer exists, the PR link is shown, and the issue feedback is recorded. |
| DLV-5 | M | The system never merges. | Given any job state, then no code path calls a merge API. |
| DLV-6 | M | Jobs of projects without `github.repo` or when `gh` is not available/authenticated cannot deliver; the failure message tells the developer what to configure. | See DLV-3; see OQ-11. |

### 4.11 Worktrees (WKT)

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| WKT-1 | M | The worktree is created at the job's first agent step: `git fetch` for the remote of `base_ref` (with timeout), then `git worktree add -b garagefab/job-<id> <path> <base_ref>`. Path: `~/.garagefab/worktrees/<project>/<job-id>`. | Given a feature job, then the worktree exists before the spec agent starts; given a refactor job, before the coding agent starts. |
| WKT-2 | M | If the fetch fails, a warning event is recorded and the local branch of the same name is used; a missing remote or branch is `Blocked`. | Given the network is down, then the job proceeds from the local branch and the dashboard shows the warning. |
| WKT-3 | M | The job records `base_sha`. All diffs and statistics are relative to the merge base of `base_sha` and `HEAD`. | Given a finished job, then "files changed" counts only the job's changes. |
| WKT-4 | M | The developer's own checkout is never modified by Garagefab (except by the explicit "Create template" action, PRJ-7). | Given a job run, then `git status` in the main checkout is unchanged. |
| WKT-5 | M | After every successful agent step Garagefab makes a checkpoint commit. Retrying a failed or interrupted step first resets the worktree to the step-start checkpoint (`git reset --hard` + `git clean -fd`). | Given an interrupted coding step with half-written files, when Retry runs, then the worktree starts from the last checkpoint. |
| WKT-6 | M | Worktrees of failed or interrupted jobs are kept until Retry or Cancel; cancelled and delivered jobs are removed. | Given a failed job, then its worktree exists; after Cancel, it does not. |
| WKT-7 | M | A leftover branch `garagefab/job-<id>` (for example after a database reset) is `Blocked` with a message; it is never silently overwritten. | Given the branch exists, then the job fails with a clear message. |
| WKT-8 | S | Worktrees on disk that belong to no live job are listed on the Overview for manual cleanup, never auto-deleted. | Given an orphan directory, then it appears in the list with its path. |
| WKT-9 | M | `git worktree add/remove` and branch operations on the same repository are serialized. | Given five jobs starting at once on one project, then all worktrees are created without git lock errors. |

### 4.12 Scheduling and Concurrency (SCH)

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| SCH-1 | M | `max_concurrent_jobs` (global, default 5, read at start) limits jobs in `running`. | Given limit 2 and 4 queued jobs, then 2 run and 2 wait. |
| SCH-2 | M | An optional project-level `limits.max_concurrent_jobs` further limits that project; it cannot exceed the global limit. | Given project limit 1, then two jobs of that project never run together while another project's job can. |
| SCH-3 | M | Queued jobs are admitted oldest-first. | Given jobs queued at t1 < t2 < t3 and one free slot, then the job from t1 runs. |
| SCH-4 | M | Jobs waiting for a human (`needs_clarification`, `spec_review`, `awaiting_approval`) and `failed`/`interrupted` jobs hold no slot. | Given limit 1 and one job in `spec_review`, then another job can run. |
| SCH-5 | M | Same-project jobs may run in parallel in separate worktrees. Garagefab does not resolve conflicts between them. | Given two jobs touching the same file, then both complete; the second PR shows the merge conflict on GitHub. |

### 4.13 Recovery and Shutdown (RCV)

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| RCV-1 | M | Every agent and command process is started in its own process group; pid, pgid, and process start time are persisted before output is read. | Given a started step, then a process record exists. |
| RCV-2 | M | On startup, before scheduling, active process records are checked. A live process whose start time matches is terminated (whole group). | Given a surviving fake agent after a simulated crash, then it is killed at the next start. A reused PID with a different start time is not touched. |
| RCV-3 | M | Affected steps are marked failed (Blocked, reason `interrupted`), their jobs become `interrupted`, and nothing is restarted automatically. | Given a crash during coding, then after restart the job is `interrupted` and waits for Retry or Cancel. |
| RCV-4 | M | Graceful shutdown (SIGINT, SIGTERM) stops admitting jobs, signals all process groups, waits a grace period, then escalates to SIGKILL, and marks the jobs `interrupted`. | Given Ctrl+C with two running jobs, then no child processes remain and both jobs are `interrupted`. |
| RCV-5 | M | Only one Garagefab instance runs per data directory (lock file). | Given a second `garagefab start`, then it exits with a message naming the running instance. |

### 4.14 GitHub Integration (GHB)

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| GHB-1 | M | GitHub access uses the GitHub CLI (`gh`) and its own authentication (`gh auth login`, `GH_TOKEN`, or `GITHUB_TOKEN`). Garagefab stores no GitHub token. | Given a running system, no GitHub token is stored in `config.yaml` or SQLite. |
| GHB-2 | M | For issue-sourced jobs, within one poll cycle of a (stage, status) change, the issue carries exactly one `garagefab:*` state label for that state (`architecture.md` §12.1), and comments are posted for states requiring human attention or upon completion. State labels are created idempotently via `gh label create --force`. | Given a job moving to `04_Coding`/`running`, then the issue shows `garagefab:in-progress` after the next cycle. |
| GHB-3 | M | Without `github.repo`, GitHub intake and feedback are skipped for that project without an error. Intent files and dashboard jobs still work. | Given no `github.repo`, then the poller skips GitHub intake and feedback for that project without an error. |
| GHB-4 | M | `GH_TOKEN` and `GITHUB_TOKEN` are never logged, never passed to agent subprocesses, and refused in `env_passthrough`. | Given a full run, then no token string appears in log files and not in the agents' environment. |
| GHB-5 | S | A feedback (label or comment) failure never changes job state; it appears as an intake/provider error and is retried next cycle. | Given a `gh issue edit` or `comment` failure, then the job continues unaffected. |

### 4.15 Human Handoff and the Skill (HND)

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| HND-1 | M | Jobs in `needs_clarification`, `spec_review`, `awaiting_approval`, `failed`, and `interrupted` show a copy button for the handoff command: for agy `cd <project path> ; agy -i "/caveman garagefab-work <job-id>"`, for opencode `cd <project path> ; opencode --prompt "/caveman garagefab-work <job-id>"`. Paths with spaces are single-quoted. | Given job 178 of a project at `/Users/me/my app`, then the command for agy is `cd '/Users/me/my app' ; agy -i "/caveman garagefab-work 178"` and for opencode `cd '/Users/me/my app' ; opencode --prompt "/caveman garagefab-work 178"`. |
| HND-2 | M | The agent in the command is the one configured for the job's current stage: 02 → `spec`, 03 → `probe`, 04 → `coding`, 05 → `review`, 06 → `coding`. | Given a job in stage 04 with `agents.coding: opencode`, then the command uses `opencode`. |
| HND-3 | M | `garagefab install-skills` installs the `garagefab-work` skill into the global configuration directory of each supported agent found on the machine and reports each path. | Given both agents installed, then both receive the skill and the command prints two paths. Given an agent not installed, then it is skipped with a note. |
| HND-4 | M | The skill gets everything via the local API: job details, intent, draft spec, error logs, worktree path and status. It primes the agent to work in the job's worktree. | Given a job in `needs_clarification`, then the skill fetches the questions and the worktree path. |
| HND-5 | M | The skill can post clarification answers (SPC-3). It has no way to approve or reject (APR-7). | Given the skill's bearer token, then `POST /api/jobs/{id}/approve` returns `403`. |
| HND-6 | S | Spec edits made by the human with the agent are made in the job's worktree and picked up at Approve (SPC-6). | See SPC-6. |

### 4.16 Dashboard (UI)

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| UI-1 | M | **Overview:** job counts per status across all projects, a recent-activity feed, an "attention" list (`needs_clarification`, `spec_review`, `awaiting_approval`, `failed`, `interrupted`), intake and provider errors, and orphan worktrees. | Given jobs in several statuses, then counts match the database and attention items link to their jobs. |
| UI-2 | M | **Project board:** read-only Kanban with the seven stage columns; cards show title, work type, job id, and a status indicator. No drag-and-drop. | Given a job in `04`/`running`, then its card is in the `04_Coding` column with a running indicator. |
| UI-3 | M | **Task detail:** pipeline visualization (steps of the job's profile with state), action buttons, handoff button, evidence, and on-demand logs. | Given a feature job at stage 05, then steps 01–04 show complete, 05 running, 06–07 pending. |
| UI-4 | M | Action buttons are enabled only when valid: Approve (`spec_review`, `awaiting_approval`), Reject (`awaiting_approval`), Retry (`failed`, `interrupted`), Cancel (any non-terminal). | Given a `running` job, then only Cancel is enabled. |
| UI-5 | M | The UI updates live from the event stream without reload. | Given a status change, then the card and detail update within 1 s. |
| UI-6 | M | **Add Project** and **New Job** forms (PRJ-1, INT-1) with inline validation messages. | Given an invalid path, then the error appears next to the field. |
| UI-7 | M | The log viewer streams a running step and shows stored logs of finished steps. It loads lazily and never by default. | Given a running step, then new lines appear within 1 s; a finished step shows its stored file. |
| UI-8 | M | A ⚠️ badge with a count shows review warnings; clicking opens them. | Given 3 warnings, then the badge shows 3. |
| UI-9 | S | Diffs over a size limit are truncated with a "show more" control. | Given a 5 000-file diff, then the page stays responsive. |

### 4.17 Logging and Audit (LOG)

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| LOG-1 | M | Every step run records: start, end, duration, result (`success`, `fail`, `skipped`), executor (agent or command), attempt number, exit code, failure category, and log path. | Given a finished job, then every step run has all fields. |
| LOG-2 | M | Full stdout/stderr of every agent and command is stored in `~/.garagefab/logs/<job-id>/<step-id>.log`, one line per entry: `<RFC3339 UTC> <stdout\|stderr> <text>`. Garagefab stores the output as emitted and does not interpret it. | Given an agent that prints 10 000 lines, then the file has 10 000 entries in order. |
| LOG-3 | M | All produced artifacts are stored under `.garagefab/jobs/<id>/` and linked from the job. | Given a delivered job, then spec, review, probe (if any), evidence, clarification, and rejection files are in the PR. |
| LOG-4 | M | State changes, approvals, intake errors, and warnings are recorded as events (§6.6) with increasing ids. | Given a job lifecycle, then events can reconstruct the transition history. |
| LOG-5 | M | Logs are not shown by default; they are fetched on demand. | Given the job page, then no log request is made until the viewer is opened. |
| LOG-6 | S | If `GH_TOKEN` or `GITHUB_TOKEN` is set in Garagefab's environment, its value is masked if it appears in captured output. | Given an agent or command that prints the token, then the stored line shows `***`. |

### 4.18 CLI and Installation (CLI)

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| CLI-1 | M | `garagefab start [--port N] [--data-dir PATH] [--no-open]` runs the service in the **foreground**, prints the dashboard login URL, and opens the browser unless `--no-open` (OQ-6). | Given a first run, then the dashboard is reachable and the login URL is printed. |
| CLI-2 | M | `garagefab open` prints and opens a fresh login URL for the running service. | Given a lost session, then `open` restores access. |
| CLI-3 | M | `garagefab install-skills` (HND-3). | See HND-3. |
| CLI-4 | M | `garagefab status` prints running jobs and attention items by reading the database. It works while the service runs. | Given a running job, then it is listed with stage and status. |
| CLI-5 | M | `garagefab version` prints version, commit, and build date. | |
| CLI-6 | M | First start creates the data directory (`0700`), `config.yaml` (`0600`) with a generated API token, the database, and applies migrations. | Given an empty home, then all exist after start. |
| CLI-7 | M | Startup checks: `git` available at a supported version, port free, database schema not newer than the binary. If any project sets `github.repo`, checks `gh --version` (minimum version) and `gh auth status` (failure emits a startup warning and Overview provider error, not a fatal exit; P1). Failures of hard requirements exit non-zero with one clear message. | Given a newer schema, then the process refuses to start. Given missing `gh`, the process warns and starts. |
| CLI-9 | S | The README explains how to keep Garagefab running with tmux, launchd, and systemd and ships an example launchd plist and systemd unit (OQ-6). | Given the README, then each of the three setups has copy-paste instructions that were tested on macOS and Linux. |
| CLI-8 | S | `garagefab token rotate` generates a new API token and invalidates all sessions (the skill reads the new token from the config). | Given a rotation, then old cookies and old bearer tokens are rejected. |

### 4.19 Security (SEC)

| ID | Pri | Requirement | Acceptance criteria |
|----|-----|-------------|---------------------|
| SEC-1 | M | The server binds to a loopback address only. | Given `server.listen: 0.0.0.0:7878`, then startup fails with an explanation. |
| SEC-2 | M | Requests whose `Host` is not `127.0.0.1:<port>` or `localhost:<port>` are rejected. | Given a request with `Host: evil.example`, then `403`. |
| SEC-3 | M | All `/api` routes require a valid session cookie or bearer token, except `GET /api/health` and `POST /api/session`. | Given no credentials, then `401`. |
| SEC-4 | M | Approve and Reject require a session cookie. A valid bearer token alone is refused with `403`. | See APR-7. |
| SEC-5 | M | Cookie-authenticated state-changing requests must carry an `Origin` equal to the server's origin. | Given a cross-origin POST with a valid cookie, then `403`. |
| SEC-6 | M | Agent and command subprocesses receive an allow-listed environment: `PATH`, `HOME`, `USER`, `LANG`, `LC_*`, `TERM`, `SHELL`, `TMPDIR`, plus names listed in `engine.env_passthrough` (OQ-12). | Given `GH_TOKEN`, `GITHUB_TOKEN`, and the API token set in Garagefab's own environment, none is visible to an agent. |
| SEC-7 | M | Data directory is `0700`; `config.yaml` is `0600`; the database and logs are not world-readable. | Given a permissive mode, then startup warns. |
| SEC-8 | M | Artifact and log endpoints serve only names from a fixed allow-list or ids from the database; no client-supplied path is joined to the filesystem. | Given `../../etc/passwd` as an artifact name, then `404`. |

## 5. Non-Functional Requirements

| ID | Area | Requirement | Measure |
|----|------|-------------|---------|
| NFR-1 | Installation | One executable; only `git` and the chosen agent CLIs are needed on the machine. | Fresh macOS and Linux machines run the happy path with nothing else installed. **Relaxed in M8:** verified on a clean macOS (darwin/arm64) machine at `v0.1.0`; Linux verification deferred (see `milestones/M8.md`). |
| NFR-2 | Build | The binary builds with `CGO_ENABLED=0` and has no runtime dependencies. | `CGO_ENABLED=0 go build ./...` passes in CI. |
| NFR-3 | Platforms | macOS (arm64, amd64) and Linux (amd64, arm64). Windows is not supported in Phase 1. Minimum Git version: 2.30 (OQ-13). | Release artifacts for the four targets. |
| NFR-4 | Responsiveness | List endpoints stay responsive with a large job history: job listing is cursor-paginated and overview queries are bounded. | p95 < 200 ms with 1 000 jobs and 20 000 step records; board renders < 1 s; events reach the browser < 1 s. **Relaxed in M7:** the M7 suite verifies the pagination/bounding behavior functionally; the numeric thresholds are not gated in CI (see `milestones/M7.md`). |
| NFR-5 | Memory | Memory does not grow with command or agent output size; the command runner and agent stream paths bound in-memory output. | Streaming 500 MB of output increases RSS by < 100 MB. **Relaxed in M7:** the M7 suite verifies that in-memory command output is bounded to a fixed tail; the 500 MB / RSS threshold is not gated in CI (see `milestones/M7.md`). |
| NFR-6 | Durability | A process crash loses no committed state. After an OS crash or power loss the database stays consistent, but the last few transactions may be lost (WAL with `synchronous=NORMAL`). | Crash tests with `kill -9`. |
| NFR-7 | Concurrency | Five concurrent jobs run without user-visible "database is locked" errors, including while DBeaver or TablePlus reads the file. | Concurrency test with a parallel reader. |
| NFR-8 | Accessibility | The dashboard is operable by keyboard, text meets WCAG 2.1 AA contrast, and status is never conveyed by color alone. | Automated checks plus manual keyboard pass. |
| NFR-9 | Browsers | Latest two versions of Chrome, Firefox, and Safari. | Manual smoke test per release. |
| NFR-10 | Code quality | English code, comments, and docs. Exported identifiers documented. Linters and `go vet` pass. `internal/factory` statement coverage ≥ 80%. | CI gates. |
| NFR-11 | Observability | Structured application logs (`slog`) with job and step ids. | Log lines carry `job_id` where applicable. |
| NFR-12 | Upgrades | Migrations are forward-only; upgrading preserves data. | A test migrates the previous release's database. **Relaxed in M8:** forward-only, data-preserving migrations remain guaranteed by `pressly/goose`; the "previous release database" test is deferred to Phase 2, since there is no prior release at `v0.1.0` (see `milestones/M8.md`). |

## 6. Data Contracts

### 6.1 Job spec structure (`spec.md`) — v0 (OQ-10)

Required second-level headings (order is free; extra sections are allowed):

```markdown
# <Title>
## Summary
## Goals and Non-Goals
## Design
## Acceptance Criteria
## Implementation Plan
## Test Plan
## Risks and Assumptions
## Reproduction          <!-- bug_fix only -->
```

Validation rules:

1. All required headings are present and non-empty.
2. `Acceptance Criteria` has at least one item identified as `AC-<n>` (written Given/When/Then).
3. `Implementation Plan` has at least one ordered item; items should reference `AC-<n>` ids.
4. For `bug_fix`, `Reproduction` is required.

### 6.2 `review.json`

```json
{
  "schema_version": 1,
  "decision": "approve",
  "summary": "One paragraph.",
  "risk": {
    "side_effect":            { "score": 2, "rationale": "…" },
    "performance":            { "score": 1, "rationale": "…" },
    "backward_compatibility": { "score": 1, "rationale": "…" }
  },
  "findings": [
    { "severity": "minor", "file": "internal/x/y.go", "line": 42, "description": "…" }
  ],
  "warnings": [
    { "file": "internal/old/z.go", "description": "Pre-existing: …" }
  ],
  "spec_coverage": [
    { "criterion": "AC-1", "status": "met", "note": "" }
  ]
}
```

Rules: `decision` ∈ `approve | request_changes`; scores are integers 1–5 (OQ-9); `severity` ∈ `blocking | minor`; `decision: approve` must not contain a `blocking` finding; `spec_coverage.status` ∈ `met | partial | not_met` and may be empty when no job spec exists. `findings` concern the change; `warnings` are out-of-scope observations.

### 6.3 `probe.json`

```json
{
  "schema_version": 1,
  "command": "go test ./internal/foo -run TestBugRepro",
  "files": ["internal/foo/bug_repro_test.go"],
  "description": "What the test asserts and why it fails today."
}
```

Rules: `command` and `files` non-empty; `files` are relative paths inside the worktree without `..`.

### 6.4 Clarification files

`clarification-questions.md` — numbered questions (`Q1.`, `Q2.`, …), one per paragraph. `clarification.md` — one block per question:

```markdown
## Q1
<question text>
**Answer:** <answer text>
```

### 6.5 Configuration reference

**`<repo>/.garagefab/project.yaml`**

| Key | Type | Default | Notes |
|-----|------|---------|-------|
| `base_ref` | string | `origin/main` | Fetched before each job; `main` stays local |
| `agents.spec`, `agents.coding`, `agents.review` | string | — (required for enabled work types) | `agy` or `opencode` |
| `agents.probe` | string | value of `agents.coding` | |
| `work_types` | list | all four | |
| `commands.build`, `.test`, `.lint` | list of strings | empty | Run via `sh -c` in the worktree, in order |
| `guardrails.protected_paths` | list of globs | `["**/*_test.go"]` | GRD-1; existing matching files are protected, newly added files are allowed (see below) |
| `guardrails.test_paths` | list of globs | empty | GRD-5 |
| `guardrails.commands` | list of strings | empty | GRD-3 |
| `github.repo` | `owner/name` | none | Needed for GitHub intake, feedback, and delivery |
| `github.intake_label` | string | `garagefab` | Issues with this label are picked up by intake (P3) |
| `github.pr_issue_keyword` | `closes` \| `refs` | `closes` | Keyword placed before the issue number in PR bodies (OQ-14) |
| `intents_dir` | path | `.garagefab/intents` | Relative to the repo |
| `limits.max_concurrent_jobs` | int | none | ≤ global limit |
| `limits.max_repair_attempts` | int | global value | |
| `timeouts.agent`, `timeouts.command` | duration | global values | |

**Protected paths (`guardrails.protected_paths`, `GRD-1`).** Default `["**/*_test.go"]`. An
**existing** file matching a pattern (present at the step-start SHA) must not be modified, deleted,
or renamed by an agent step; **newly added** files matching a pattern are allowed. The effective
list (default or configured) is passed to both the spec and coding prompts, so a spec must not plan
edits to a protected file. To permit editing an existing protected file, narrow
`guardrails.protected_paths` for that project.

**`~/.garagefab/config.yaml`**

| Key | Default | Notes |
|-----|---------|-------|
| `server.listen` | `127.0.0.1:7878` | Loopback only |
| `server.api_token` | generated | |
| `engine.max_concurrent_jobs` | `5` | Read at start |
| `engine.max_repair_attempts` | `3` | |
| `engine.poll_interval` | `30s` | |
| `engine.step_timeouts` | `agent: 30m`, `command: 10m` | |
| `engine.env_passthrough` | `[]` | Extra environment variable names for agents |

### 6.6 Enumerations

- **Stages:** `01_Intent`, `02_Clarification_and_Spec`, `03_Failing_Probe`, `04_Coding`, `05_Independent_Review`, `06_Human_Approval_Gate`, `07_Done`.
- **Statuses:** `queued`, `running`, `needs_clarification`, `spec_review`, `awaiting_approval`, `interrupted`, `failed`, `cancelled`, `done`.
- **Work types:** `bug_fix`, `feature`, `refactor`, `docs`.
- **Step result:** `success`, `fail`, `skipped`. **Step kind:** `agent`, `command`, `gate`.
- **Failure category:** `flawed`, `blocked`, `manual`.
- **Event types:** `job.created`, `job.stage_changed`, `job.status_changed`, `step.started`, `step.finished`, `approval.recorded`, `warning.raised` (e.g., fetch fallback), `intake.error`, `provider.error`.

### 6.7 Worktree layout and naming

```
.garagefab/jobs/<id>/
  intent.md
  clarification-questions.md     # present only while clarification is pending
  clarification.md
  spec.md
  probe.json                     # bug_fix
  review.json
  evidence.md
  rejections/<n>.md
```

Branch: `garagefab/job-<id>`. Checkpoint commit message: `garagefab(job-<id>): <stage name>`. Commits use the developer's Git identity.

## 7. API Contract

All routes are under `/api`, JSON unless noted. **Auth:** `S` = session cookie only; `B` = session cookie or bearer token; `N` = none.

### 7.1 Errors

```json
{ "error": { "code": "invalid_state", "message": "Job 12 is running; Approve is not allowed.", "details": {} } }
```

| Code | HTTP | Meaning |
|------|------|---------|
| `validation_failed` | 400 / 422 | Bad input; `details` lists the reasons |
| `unauthorized` | 401 | Missing or invalid credentials |
| `forbidden` | 403 | Wrong auth type, bad Host or Origin |
| `not_found` | 404 | |
| `invalid_state` | 409 | Action not valid for current stage/status |
| `stale_evidence` | 409 | Worktree `HEAD` changed since evidence was built |
| `conflict` | 409 | Duplicate (name, path) |
| `internal` | 500 | |

### 7.2 Endpoints

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| GET | `/api/health` | N | `{status, version}` |
| POST | `/api/session` | N | Exchange the API token for a session cookie |
| GET | `/api/overview` | B | Counts per status, recent events, attention list, intake/provider errors, orphan worktrees |
| GET | `/api/projects` | B | List projects |
| POST | `/api/projects` | S | Register a project (PRJ-1..5) |
| GET | `/api/projects/{id}` | B | Project with validated config summary |
| POST | `/api/projects/{id}/archive` | S | Archive (PRJ-8) |
| POST | `/api/projects/{id}/config-template` | S | Create `project.yaml` template (PRJ-7) |
| GET | `/api/jobs` | B | List; filters `project`, `status`, `stage`, `limit`, `cursor` |
| POST | `/api/jobs` | S | Create a job (INT-1) |
| GET | `/api/jobs/{id}` | B | Job detail: stage, status, steps, handoff command, worktree path, PR URL, current questions or draft spec reference |
| GET | `/api/jobs/{id}/steps` | B | Step runs |
| GET | `/api/jobs/{id}/artifacts/{name}` | B | One of: `intent`, `clarification-questions`, `clarification`, `spec`, `probe`, `review`, `evidence` |
| GET | `/api/jobs/{id}/diff` | B | Unified diff from the merge base (paged) |
| GET | `/api/jobs/{id}/evidence` | B | Chain of evidence: summary and drill-down references |
| GET | `/api/jobs/{id}/steps/{stepId}/log` | B | Plain text with `?offset=`; as SSE when `Accept: text/event-stream` |
| POST | `/api/jobs/{id}/clarification` | B | Body `{ "answers": [{ "q": 1, "answer": "…" }] }` (SPC-3) |
| POST | `/api/jobs/{id}/approve` | **S** | Approve spec or final gate (SPC-6, APR-5) |
| POST | `/api/jobs/{id}/reject` | **S** | Body `{ "note": "…" }` (APR-6) |
| POST | `/api/jobs/{id}/retry` | S | PIP-7 |
| POST | `/api/jobs/{id}/cancel` | S | PIP-6 |
| GET | `/api/events` | B | SSE: all events; honors `Last-Event-ID` |

### 7.3 Server-sent events

Global stream:

```
id: 1042
event: job.status_changed
data: {"job_id":178,"project_id":3,"stage":"04_Coding","status":"running"}
```

Log stream:

```
id: 57
event: log
data: {"line":57,"ts":"2026-10-01T09:14:03Z","stream":"stdout","text":"Running tests…"}

event: end
data: {"result":"success"}
```

Heartbeat comment every 15 s. Reconnecting with `Last-Event-ID` resumes from the next event (global) or next line (log).

## 8. Error Handling and Edge Cases

| Situation | Behavior |
|-----------|----------|
| Agent binary not found | Step fails, category Blocked, message names the binary and agent |
| Agent exits 0 but produces no valid artifact | Flawed; re-run while attempts remain |
| Agent hangs or exceeds timeout | Process group terminated; Blocked |
| Agent ignores SIGTERM | SIGKILL after the grace period (default 10 s) |
| Command not found / permission denied | Blocked |
| No `commands.*` configured | Groups skipped; evidence warns |
| Project path moved or deleted | Project flagged unavailable; poller skips it; its jobs fail Blocked at the next step |
| Poller cycle takes longer than the interval | Next cycle waits; cycles never overlap |
| `gh` failure (missing, unauthenticated, rate limit) | Provider error on the Overview; jobs unaffected; retried next cycle |
| State label missing on GitHub repository | Created idempotently with `gh label create <name> --force` |
| PR already exists for job branch | If open: reused (DLV-2); if merged or closed: fails Blocked (never open duplicate PR) |
| Intent file malformed or has invalid `type` | Intake error shown; no job |
| Intent file edited after its job exists | Ignored; a log event notes it |
| Two browser tabs click Approve | Second gets `409` |
| Cancel during a running step | Process group terminated; worktree removed; `cancelled` |
| Worktree creation fails (disk, git error) | Blocked with git's message |
| Disk full while writing logs | Step fails Blocked; service keeps running |
| Port in use | `start` exits with a message; `--port` overrides |
| Second instance on the same data directory | Exits (RCV-5) |
| Database newer than binary | Refuses to start |
| Very large diff or log | Paged or truncated in the UI; full content stays downloadable |
| Clock change during a run | Durations use a monotonic clock; stored timestamps are wall-clock UTC |

## 9. Out of Scope for Phase 1

Team features and multi-user auth · Windows · agent sandboxing or network filtering (guardrails limit changes in the worktree, they do not stop an agent from acting elsewhere on the machine) · auto-merge or merge conflict resolution · webhooks and fsnotify · daemonization or OS service installation · configuration hot reload · log retention policies · per-test result counts (test report parsing) · built-in background mode and `install-service` · issue trackers other than GitHub · coding agents other than `agy` and `opencode` · custom work types and per-step gate configuration · token and cost tracking · Factory Brain · AI-enhanced failure categorization · OS notifications · "Open in Terminal" button · self-healing CI · everything under "Phase 2 Roadmap" in `intent.md`.

## 10. Acceptance Scenarios

Scenarios 1 and 2 are the **MVP success criteria** from `intent.md`. Scenarios 3–6 are release checks for pipeline safety. All run in CI with a fake agent, a temporary Git repository, and `FakeGHRunner` (no network access).

**Scenario 1 — Happy path (feature).** *Covers INT-3, SPC-1/4/5/6, COD-1/2/9, REV-1/2/5, APR-1/2/4/5, DLV-1/4, GHB-2.*
Given a registered project and an issue with labels `garagefab` and `type:feature` → a job is created; the draft job spec appears in `spec_review`; Approve → coding runs, commands pass; review writes `review.json`; the job is `awaiting_approval` with a complete evidence summary; Approve → a PR exists, the worktree is gone, the job is `done`, and the issue carries `garagefab:delivered` and a PR link comment. Human actions: create issue, approve spec, approve at the gate.

**Scenario 2 — Clarification.** *Covers SPC-1/2/3, HND-1/4/5.*
Given an ambiguous issue with labels `garagefab` and `type:feature` → the fake agent writes questions; the job is `needs_clarification`; the handoff command is shown; answers are posted with the bearer token; the job returns to `02`/`queued`, the spec agent receives the answers and produces a valid draft; the rest follows Scenario 1.

**Scenario 3 — Repair loop and guardrail.** *Covers COD-4/5/6, GRD-1/4.*
Coding attempt 1 fails a test → attempt 2 edits a protected file (violation) → attempt 3 succeeds; all commands re-run each time; the job passes with the counter at 2. A variant with failures on all three repairs ends in `04`/`failed` (Manual).

**Scenario 4 — Rejection.** *Covers APR-6, COD-5.*
At the gate, Reject with a note → the job returns to coding; the prompt contains the note; the counter is 0; the job reaches the gate again.

**Scenario 5 — Crash recovery.** *Covers RCV-1..4, WKT-5, PIP-7.*
Kill Garagefab with `kill -9` during coding with a live fake agent → on restart the agent process is gone, the job is `interrupted`, the worktree is kept; Retry resets to the last checkpoint and completes.

**Scenario 6 — Bug fix probe.** *Covers PRB-1..4, COD-8.*
A bug job: a probe that passes is rejected and re-run; a failing probe moves on; coding that modifies the probe file is a violation; coding that makes the probe pass proceeds to review.

## 11. Decisions and Open Items

All items below were reviewed with the developer. IDs are kept stable because requirements reference them.

| ID | Topic | Decision | Status |
|----|-------|----------|--------|
| OQ-1 | Source of the work type for GitHub and intent-file entries | `type:<work_type>` issue label; `type:` in intent-file front matter. Missing → no job, visible intake error. | Confirmed |
| OQ-2 | `docs` profile has no approval gate but creates a PR | PR is created right after review; if the review says `request_changes`, the job goes to the approval gate instead. | Confirmed |
| OQ-3 | Should `request_changes` loop back to coding? | No in Phase 1; it is shown at the gate. | Confirmed |
| OQ-4 | Test result detail in the evidence | **Pass/fail only.** Per-test counts (test report parsing, e.g., JUnit) are Phase 2. | Confirmed (changed from draft) |
| OQ-5 | Review reference when there is no job spec (`refactor`, `docs`) | The intent text. | Confirmed |
| OQ-6 | "Background service" in `intent.md` | Phase 1 runs in the **foreground**. The README explains how to keep it running with tmux, launchd, and systemd, with example files. Built-in background mode and `install-service` are Phase 2. | Confirmed (option A) |
| OQ-7 | Where each agent keeps global skills; skill format | Resolved by Spike A (`PROJECT_DOCS/spikes/agent-clis.md`). `agy`: `~/.gemini/antigravity/` / `plugins/`, `SKILL.md` format. `opencode`: plugins and MCP. | Confirmed |
| OQ-8 | CLI `run` command from `techstack.md` | Dropped. Commands: `start`, `status`, `open`, `install-skills`, `token rotate`, `version`. | Confirmed |
| OQ-9 | Risk score scale | Integers 1–5 per dimension. | Confirmed |
| OQ-10 | Required job spec headings | The v0 set in §6.1, refined after the first real runs (`architecture.md` O5). | Confirmed; refine later |
| OQ-11 | Projects without `github.repo` or a usable `gh` CLI | The job runs but fails (Blocked) at delivery with a clear message. | Confirmed |
| OQ-12 | Agent credentials in the environment | Allow-list plus `engine.env_passthrough`. | Confirmed |
| OQ-13 | Minimum Git version | 2.30. | Confirmed |
| OQ-14 | Issue keyword in PR bodies | `Closes #<n>` by default; `github.pr_issue_keyword: closes \| refs` per project. | Confirmed (with setting) |
| OQ-15 | Approve/Reject only from the dashboard session | Yes; bearer tokens (skill, CLI) are refused. | Confirmed |
| OQ-16 | One job per intent file or issue | Yes; re-triggering needs a new file or issue. | Confirmed |

**Remaining open items** (not blockers for the Spec): refinement of the job spec headings after real runs (OQ-10).
