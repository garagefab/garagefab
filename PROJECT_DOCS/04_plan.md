# Garagefab — Plan

> Version: 0.1 (draft) · Covers Phase 1 (MVP)
> Inputs: `intent.md`, `architecture.md`, `spec.md`.
> This document says **in what order** we build and **when a step counts as done**. It never redefines behavior; requirement IDs (e.g., `COD-4`) point to `spec.md`.

## 1. Approach

1. **Vertical slices, fake first.** Build the thinnest end-to-end path early (one job, a fake agent, real SQLite, real Git worktree), then widen it. Replace fakes with real agents and a real GitHub only after the pipeline logic is proven in CI.
2. **Fake agent as a first-class tool.** A single test binary that can succeed, write invalid artifacts, hang, ignore SIGTERM, crash, and spawn children. Almost every acceptance scenario in `spec.md` §10 depends on it.
3. **Dogfooding with the same artifacts.** Until Garagefab can run its own jobs, we apply its process by hand: one intent per work item, a job spec reviewed before coding, a separate review session before merge. Manual artifacts use the same layout as real jobs (`.garagefab/jobs/<n>/`, `spec.md` §6.7) so they can be imported later.
4. **Rolling-wave detail.** Only the next one or two milestones are broken into tasks. Later milestones stay coarse and are detailed when they become next.
5. **Sizes, not dates.** S, M, L describe relative effort and uncertainty:
   - **S** — one focused piece, one or two requirement areas, little uncertainty.
   - **M** — several areas with some integration work.
   - **L** — cross-cutting, integration-heavy, or high uncertainty; must be split into issues before starting.

## 2. Working Agreement

- **Tracking:** GitHub Issues. One issue is one job-sized change. Labels: `milestone:M<n>`, `req:<ID>`, `type:<bug_fix|feature|refactor|docs>`. This plan keeps only milestone-level detail.
- **Flow per issue:** short intent → job spec (what, acceptance criteria referencing requirement IDs, implementation plan) → human approval → implementation → separate-session review → PR → human merge.
- **Change control:** If implementation shows that `intent.md`, `architecture.md`, or `spec.md` is wrong or ambiguous, stop and change the document first (in its own PR), then continue. Decisions go to `architecture.md` §3 (technical) or `spec.md` §11 (behavioral).
- **Parking lot:** Ideas that are not Phase 1 go to a "Parking lot" issue labeled `phase-2`. They are never started inside a Phase 1 milestone.
- **Unverified commands:** Commands in `AGENTS.md` are drafts until M0 confirms them.

## 3. Milestone Overview

| # | Milestone | Size | Depends on | Exit criterion (short) |
|---|-----------|------|------------|------------------------|
| M0 | Foundation | S | — | `make ci` green on macOS and Linux; `garagefab start` serves a placeholder UI and `/api/health` |
| SA | Spike A: agent CLIs | S | — | `PROJECT_DOCS/spikes/agent-clis.md` answers every question in §5 for `agy` and `opencode`; start alongside M0–M1 so adapters are ready for M5 |
| M1 | Walking skeleton | L | M0 | A `refactor` job runs end to end with the fake agent through the API (spec Scenario 1 shape, without spec or GitHub) |
| M2 | Safety and repair | M | M1 | Scenarios 3 (repair, guardrail) and 5 (crash recovery) pass in CI |
| M3 | Spec flow and evidence | L | M2 | A `feature` job passes clarification, spec review, review report, and approval gate via the API; Scenarios 2 (without GitHub) and 4 pass |
| M4 | Dashboard and security | L | M3 | The `feature` flow and clarification flow are drivable from the UI with the fake agent |
| SB | Spike B: GitHub | S | — | **Cancelled** (superseded by D22/D23, see `PROJECT_DOCS/05_reviewd-change-plan-github-projects.md`) |
| M5 | Real agents and skill | M | M3, SA | A real `refactor` job completes locally with each of `agy` and `opencode`; the skill reads a job |
| M6 | GitHub and delivery | M | M4 | Scenario 1 passes against a real test repository |
| M7 | Remaining profiles and hardening | M | M5, M6 | Scenario 6 passes; edge-case table and NFR checks pass |
| M8 | Release | S | M7 | Scenarios 1 and 2 pass with real agents on clean macOS and Linux machines; `v0.1.0` tagged |

**Dogfooding gate:** at the end of M5 Garagefab can run real `refactor` and `docs` jobs on its own repository. From then on, work items of those types go through Garagefab. After M6, `feature` items do too.

### Status

| Milestone | Status |
|-----------|--------|
| M0 | Completed |
| SA | Completed |
| M1 | Completed |
| M2 | Completed |
| M3 | Completed |
| M4 | Completed |
| SB | Cancelled (superseded by D22/D23) |
| M5 | Completed |
| M6 | Completed |
| M7 | In progress (detailed plan: `milestones/M7.md`) |
| M8 | Not started |

## 4. Milestones

### M0 — Foundation (S)

**Goal:** A repository where everything later can be built, tested, and shipped.
**Covers:** `CLI-1` (basic), `CLI-5..7`, `RCV-5`, `SEC-7`, `NFR-2`.

Tasks (candidate issues):

1. Repository setup: `go.mod` (Go 1.22), `Makefile` (`build`, `test`, `lint`, `ui`, `ci`), `.gitignore`, README stub, license (see P-1).
2. CI (GitHub Actions, macOS and Linux matrix): `CGO_ENABLED=0 go build ./...`, `go vet`, `go test ./...`, `golangci-lint`, UI build, and an import-boundary check (architecture §6 rules).
3. `internal/config`: load or create `~/.garagefab/config.yaml` with a generated API token, enforce modes (`0700` directory, `0600` file), validation with clear errors.
4. `internal/store`: open SQLite via `modernc.org/sqlite` with the PRAGMAs from architecture §5, one write connection and a read pool, goose with embedded migrations (`0001_init`); test with a parallel reader. The real schema grows milestone by milestone.
5. Single-instance lock file (`RCV-5`).
6. Cobra CLI: `version`, `start` (loads config, runs migrations, serves `/api/health`), startup checks (`CLI-7`).
7. Embedded UI placeholder: Vite + React + Tailwind + shadcn initialized, `ui/dist` embedded with `//go:embed`, a page that calls `/api/health`.
8. Confirm or correct the commands in `AGENTS.md`.

**Exit criteria:** `make ci` is green on both platforms; the `CGO_ENABLED=0` binary starts, applies migrations, serves the placeholder UI, and refuses a second instance on the same data directory.
**Risks:** none significant.

### SA — Spike A: agent CLIs (S)

**Goal:** Replace assumptions about `agy` and `opencode` with facts, so adapters, prompts, and the skill can be designed (closes `architecture.md` O2 and `spec.md` OQ-7).
**Output:** `PROJECT_DOCS/spikes/agent-clis.md` plus recorded real outputs as test fixtures.
Questions are listed in §5.

### M1 — Walking skeleton (L)

**Goal:** One `refactor` job runs from creation to a "done"-shaped end through the API, with the fake agent, in a real worktree.
**Covers:** `PRJ-1..6` (API and validation), `INT-1` (API), `PIP-1..5`, `COD-1/2/9`, `REV` (fake review file only), `APR-5..7` (approve/reject/session-only enforced; evidence summary minimal), `WKT-1/3/4/6/9`, `SCH-1/3/4`, `LOG-1/2/4`, `CLI-4`, `SEC-1..5/8` (backend), `NFR-4` (baseline).

Deliverables:

- `store`: schema for projects, jobs, step runs, events, approvals, process records, sessions; repositories.
- `worker`: process-group runner with streamed logs, command runner, worktree manager (fetch fallback, serialization), the **fake agent**.
- `factory`: state machine for the `refactor` profile, scheduler with the global limit, step execution, checkpoint commits.
- `server`: auth middleware (session and bearer, Host and Origin checks), project and job endpoints, approve/reject/cancel, global SSE.
- `garagefab status`.

**Exit criteria:** A CI test registers a temporary Git repository, creates a `refactor` job via the API, and sees it reach the approval gate, then `done` (delivery stubbed), with logs, events, and a checkpoint commit present.
**Risks:** R2, R5, R9, R11 (see §6). Split into issues by package before starting.

### M2 — Safety and repair (M)

**Goal:** The pipeline is safe against bad agent behavior and crashes.
**Covers:** `COD-3..7/10`, `GRD-1..4`, `PIP-6/7`, `WKT-5/7/8`, `SCH-2/5`, `RCV-1..4`, `SEC-6`, failure categorizer.

**Exit criteria:** Spec Scenarios 3 and 5 pass in CI on macOS and Linux, including a fake agent that spawns child processes and one that ignores SIGTERM.

### M3 — Spec flow and evidence (L)

**Goal:** The `feature` profile works through the API.
**Covers:** `SPC-1..7`, `REV-1..6`, `APR-1..4`, `LOG-3`, `INT-1` (full), the review and job-spec validators, clarification handling, evidence builder and `evidence.md`.

**Exit criteria:** Scenarios 2 (clarification, with answers posted via the API) and 4 (rejection) pass; a `feature` job reaches the final gate with complete evidence.

### M4 — Dashboard and security (L)

**Goal:** Everything the developer needs, in the browser.
**Covers:** `UI-1..9`, login flow (`SEC-3` UI part, `CLI-2`), live updates, log viewer, forms (`PRJ`, `INT-1`), `NFR-8`.

**Exit criteria:** The happy path and the clarification flow run entirely from the dashboard with the fake agent; keyboard-only pass done.
**Risks:** R8. Keep screens as specified; no extra views.

### SB — Spike B: GitHub (S)

> **Status: Cancelled (superseded by D22/D23)**  
> GitHub Projects v2 is removed and all GitHub operations are unified under the official `gh` CLI. No spike experiments are needed.

### M5 — Real agents and skill (M)

**Goal:** Real agents replace the fake one in the same pipeline.
**Covers:** `HND-1..6`, `CLI-3`, `agy` and `opencode` adapters, prompt templates for each role, the `garagefab-work` skill.

**Exit criteria:** A real `refactor` job completes locally with each agent; `garagefab install-skills` installs the skill; the skill fetches a job and posts clarification answers. **The dogfooding gate opens.**
**Risks:** R1, R2, R3.

### M6 — GitHub and delivery (M)

**Goal:** Jobs enter from GitHub issues and intent files and leave as pull requests.
**Covers:** `INT-2..7`, `GHB-1..5`, `DLV-1..6`, the poller, issue feedback reconciler.

**Exit criteria:** Scenario 1 passes against a real test repository, with the fake agent in CI and a real agent as a manual check.
**Risks:** R4.

### M7 — Remaining profiles and hardening (M)

**Goal:** All four work types, plus the robustness bar.
**Covers:** `PRB-1..5`, `COD-8`, `GRD-5`, the `docs` profile paths, `LOG-6`, `CLI-8`, edge-case table (spec §8), `NFR-4..7`.

**Exit criteria:** Scenario 6 passes; performance, memory, and concurrency checks meet the NFR measures; the edge-case table has tests.

### M8 — Release (S)

**Goal:** Anyone can download and use it.
**Covers:** `CLI-9`, `NFR-1/3/9/12`, release artifacts for the four targets, README, example launchd plist and systemd unit, license and contribution notes.

**Exit criteria:** Scenarios 1 and 2 pass with real agents on clean macOS and Linux machines; `v0.1.0` is tagged.

## 5. Spike Questions

### Spike A — `agy` and `opencode`

Answer each question for both agents and record exact commands and outputs.

1. **Headless run:** What is the non-interactive invocation? Does it exit when done?
2. **Prompt delivery:** Argument, stdin, or file? Length limits?
3. **Fresh session:** How do we guarantee no resume or shared history between invocations?
4. **Permissions:** Are there approval prompts that block unattended runs? Which flags or settings allow file edits and command execution without prompting, and what do they permit?
5. **Working directory:** Does the agent respect the process working directory and stay inside it?
6. **Exit codes:** What do success, agent-reported failure, timeout, and crash look like?
7. **Output:** Plain text or a structured stream? Is there a stable way to see tool activity?
8. **Instructions file:** Which project file does each agent read (`AGENTS.md`, `GEMINI.md`, other)?
9. **Skills:** Where are global skills installed, and what is the skill file format? How is a skill invoked from the handoff command (`garagefab-work <job-id>`)?
10. **Credentials:** Which environment variables or config files must be passed through (`engine.env_passthrough`)?
11. **No TTY:** Does behavior change when stdin or stdout is not a terminal?
12. **Process tree:** Which child processes does the agent start, and does killing the process group clean them up?
13. **Versions:** Which versions were tested; how can the adapter detect an incompatible one?

**Unblocks:** adapter design (M5), prompt templates, skill packaging, `architecture.md` O2, `spec.md` OQ-7.

### Spike B — GitHub (Cancelled)

> **Cancelled.** Replaced by `gh` CLI architecture (D22/D23). Original research questions regarding GraphQL node IDs and Projects v2 permissions are obsolete.

## 6. Risks

| ID | Risk | Mitigation | Early signal |
|----|------|------------|--------------|
| R1 | An agent CLI has no dependable headless mode | Spike A first; design the adapter so a PTY-based launcher can be added; decide on scope early | Spike A answers 1, 4, 11 |
| R2 | Agents produce invalid or missing artifacts | Strict schemas, repair loop, prompt templates tested against recorded fixtures | High repair rate in M5 runs |
| R3 | Agent CLIs change flags between versions | Record tested versions, detect version at start, contract tests on fixtures | Adapter test failures after upgrade |
| R4 | `gh` CLI output or flags change between versions | Pin a minimum `gh` version; parse only `--json` output; `FakeGHRunner` contract tests | Test failures in `provider/github` |
| R5 | Process-group and signal behavior differs between macOS and Linux | Fake agent that spawns children; CI on both platforms from M0 | Flaky RCV tests |
| R6 | SQLite write contention under five concurrent jobs | One write connection, short transactions, no subprocess inside a transaction; load test in M7 | "database is locked" in logs |
| R7 | Scope creep through dogfooding (every run suggests new ideas) | Parking lot rule (§2); Phase 2 label | Phase 2 items in a milestone |
| R8 | Dashboard effort balloons | Only the screens in the Spec; shadcn components; read-only board | M4 exceeds its size |
| R9 | Git edge cases: hooks, submodules, LFS, large repositories | Hooks decision (P-3); unsupported cases documented in README | Failing checkpoint commits |
| R10 | Solo bandwidth | Keep issues small; one milestone in progress at a time | Several open L items |
| R11 | Timing-based process tests are flaky | Synchronize on events rather than sleeps; repeat runs in CI | Tests that pass only on rerun |

## 7. Definition of Done

**Issue (task):**

1. Acceptance criteria of every referenced requirement ID are covered by tests whose names or comments cite the ID.
2. `make ci` is green on macOS and Linux.
3. No new dependency without a decision-log entry (`architecture.md` §3).
4. Documents updated if behavior or decisions changed (via change control, §2).
5. Reviewed in a separate session; out-of-scope observations recorded as warnings or issues, not fixed in the same change.
6. PR references the issue and the requirement IDs.
7. `NFR-10` (code quality) and `NFR-11` (observability) are enforced continuously by CI gates and review.

**Milestone:**

1. All exit criteria met and the named scenarios pass in CI.
2. Status table in this plan updated.
3. Open items (§9) revisited; new risks recorded.
4. A short demo note (what was run, what was seen) added to the milestone's closing issue.

## 8. Traceability: Requirement Areas to Milestones

| Area | Requirements | Milestones |
|------|--------------|------------|
| PRJ | `PRJ-1..6` / `PRJ-7..8` | M1 (API, validation), M4 (UI) / M4 |
| INT | `INT-1` / `INT-2..7` | M1, M3, M4 / M6 |
| PIP | `PIP-1..5` / `PIP-6..7` | M1 / M2 |
| SPC | `SPC-1..8` | M3 (`SPC-8` in M4) |
| PRB | `PRB-1..5` | M7 |
| COD | `COD-1/2/9` / `COD-3..7/10` / `COD-8` | M1 / M2 / M7 |
| GRD | `GRD-1..4` / `GRD-5` | M2 / M7 |
| REV | `REV-1..6` | M1 (stub), M3 |
| APR | `APR-1..4` / `APR-5..7` | M3 / M1, M3 |
| DLV | `DLV-1..6` | M6 |
| WKT | `WKT-1/3/4/6/9` / `WKT-5/7/8` | M1 / M2 |
| SCH | `SCH-1/3/4` / `SCH-2/5` | M1 / M2 |
| RCV | `RCV-5` / `RCV-1..4` | M0 / M2 |
| GHB | `GHB-1..5` | M6 |
| HND | `HND-1..6` | M5 (`HND-1` display in M4) |
| UI | `UI-1..9` | M4 |
| LOG | `LOG-1/2/4` / `LOG-3` / `LOG-5` / `LOG-6` | M1 / M3 / M4 / M7 |
| CLI | `CLI-1/5/6/7` / `CLI-4` / `CLI-2` / `CLI-3` / `CLI-8` / `CLI-9` | M0 / M1 / M4 / M5 / M7 / M8 |
| SEC | `SEC-7` / `SEC-1..5/8` / `SEC-6` | M0 / M1 (login UI in M4) / M2 |
| NFR | `NFR-2` / `NFR-4..7` / `NFR-8` / `NFR-1/3/9/12` / `NFR-10/11` | M0 / M7 / M4 / M8 / continuous |

## 9. Open Items Affecting the Plan

| ID | Item | Decide by |
|----|------|-----------|
| P-1 | Open-source license (for example MIT or Apache-2.0) | Before the first public commit (M0) |
| P-2 | Does `agy` read `GEMINI.md`, `AGENTS.md`, or both? If it needs `GEMINI.md`, add a minimal pointer file | Spike A |
| P-3 | Checkpoint commits may trigger the developer's Git hooks and fail. Proposal: use `--no-verify` for Garagefab's own commits and say so in the docs | M1 |
| P-4 | Manual dogfooding artifacts before Garagefab runs: proposal is `.garagefab/jobs/<n>/` with the same layout | Start of M0 |
| P-5 | Fake agent design: one binary driven by a scenario file or flags | Start of M1 |
| P-6 | CI provider: assumed GitHub Actions with a macOS and Linux matrix | M0 |
| P-7 | Release tooling (for example goreleaser; build-time only) | M8 |
| P-8 | `spec.md` OQ-7 (skill locations and format) and OQ-10 (job spec headings, refine after real runs) | Spike A, M5 |
| P-9 | Location of documents in the repository (root or `docs/`); update `AGENTS.md` links accordingly | M0 |

## 10. Updating This Plan

- Update the status table when a milestone starts and when it ends.
- When a milestone becomes next, break it into issues and add the issue links under its section.
- Re-estimate sizes only at milestone boundaries.
- Plan changes that alter scope go through change control (§2).
