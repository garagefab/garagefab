# Garagefab — Intent Document

## Vision

Garagefab is an open-source, local-first **AI software factory** that orchestrates AI coding agents through a structured, repeatable Software Development Life Cycle (SDLC). It transforms the ad-hoc usage of AI coding tools into a disciplined, automated pipeline — where every task flows through defined stages, every decision leaves an audit trail, and the human developer remains in control at the right moments.

The core inspiration comes from the AI-Native SDLC philosophy: instead of a linear flow where humans manually hand off work between phases, the process becomes a **loop** with AI embedded at each point and automated handovers between stages.

## Business Context

Garagefab serves a multi-layered strategic purpose:

1. **Open-Source Tool** — A publicly available software factory for solo developers, hosted at [github.com/garagefab/garagefab](https://github.com/garagefab/garagefab).
2. **Dogfooding** — Garagefab itself will be developed as if the tool were already complete, manually applying its own principles at every step. This surfaces real problems, validates the design, and generates authentic lessons for the tool.

## Target User

**Solo developer** — a single software developer who uses AI coding agents (Antigravity CLI, opencode, Claude Code, Codex, etc.) and wants to bring structure, repeatability, and quality gates to their AI-assisted development workflow.

Team features are explicitly out of scope for the initial version. The tool should be simple to install, easy to understand, and have minimal external dependencies.

## Guiding Principles

### 1. Artifact-Driven Handoffs
Every stage in the pipeline communicates via committed, human-readable artifacts: `intent` → `spec.md` → code + tests → test results → review report → PR. The Git commit history **is** the audit trail. No information lives only in memory or a database — if it matters, it's a file.

### 2. Prompts Are Advisory, Hooks Are Deterministic
Instructions given to AI agents via prompts or `AGENTS.md` are best-effort — the agent usually follows them but there is no guarantee. Rules that **must not be broken** (e.g., "do not modify test files during coding") must be enforced by deterministic guardrail hooks (command steps that verify compliance after the agent runs), not by prompting alone.

### 3. One Agent per Task, Fresh Session per Step
No monolithic super-agent. Each pipeline step that requires AI runs a dedicated, scoped agent in a fresh session with no shared conversation history. The coding agent and the review agent never share context. This prevents bias, "review theater," and context contamination.

### 4. Human Control at the Right Moments
The factory automates what can be automated, but the human decides when to intervene. Approval gates are configurable. Low-risk steps flow automatically; high-risk steps (code push, PR creation) pause for human approval. The human is never forced to watch — they check in when notified.

### 5. Tool Agnosticism
Garagefab is not locked to any specific AI coding agent, LLM provider, or issue tracker. The user chooses their own tools. The system provides adapter interfaces so different tools can be plugged in. Phase 1 supports Antigravity CLI and opencode as coding agents, and GitHub Issues as the issue tracker, but the architecture is designed for extensibility.

### 6. Low Barrier to Entry
The tool must be easy to install and run. Single binary, embedded database, embedded web UI, zero external service dependencies. A solo developer should go from download to first pipeline run with minimal configuration.

### 7. Scope Discipline
Agents and reviews operate strictly within the scope defined by the spec. A coding agent does not fix pre-existing technical debt. A review agent does not reject work for issues unrelated to the current task. Scope creep is the factory's enemy.

## System Architecture Overview

Garagefab runs as a **single binary background service** on the developer's local machine. In Phase 1 the process runs in the foreground and is kept alive with the developer's own tools (tmux, launchd, systemd); built-in daemonization and service installation are Phase 2. It consists of:

- **Orchestrator** — The central brain. Manages the job queue, executes pipeline steps, enforces state transitions, handles retries, and serves the web dashboard API. All project repositories must be accessible on the same local filesystem.
- **Agent Steps** — Pipeline steps that invoke an AI coding agent CLI as a subprocess. The orchestrator prepares the task-specific prompt, starts the agent in the correct git worktree directory, captures stdout/stderr as logs, and evaluates results when the process exits. The agent itself reads project-level instructions (AGENTS.md, GEMINI.md, etc.) from the repository — garagefab does not inject these.
- **Command Steps** — Pipeline steps that run deterministic shell commands (test runners, build tools, linters, guardrail hooks). No AI involved. The orchestrator runs the commands sequentially, evaluates exit codes, and determines pass/fail. Command steps serve dual purposes: validation (run tests) and guardrails (verify the agent didn't violate constraints).
- **Web Dashboard** — An embedded web UI served by the same binary. Provides visibility into all projects and jobs, action buttons for human intervention, and access to logs and artifacts.
- **Agent Adapter Interface** — An abstraction layer between the orchestrator and the AI agent CLIs. Phase 1 implements subprocess-based adapters for Antigravity CLI and opencode. The interface is designed so that new adapters (for other agents or API-based invocation) can be added without modifying the orchestrator.

### Architecture Proposal for the Spec Phase
During the spec phase, the following deployment architecture should be evaluated: a single compiled binary that bundles the orchestrator (API server + pipeline engine), an embedded database (e.g., SQLite), and a pre-built web frontend served as static assets from within the binary. This approach eliminates external runtime dependencies, simplifies installation to a single file download, and aligns with the "low barrier to entry" principle. The specific technology stack, language, and framework choices will be determined during the spec phase.

## SDLC Pipeline

### Pipeline Stages

Jobs progress through the following stages, tracked in both garagefab's internal state and mirrored to GitHub Projects:

| Stage | Type | Description |
|-------|------|-------------|
| `01_Intent` | Entry | The raw intent — what needs to be done and why. Entered via issue, intent file, or dashboard. |
| `02_Clarification_and_Spec` | Agent Step + Human Review | An agent examines the intent for clarity and completeness. If the intent is ambiguous, incomplete, or contradictory, the job enters **Needs Clarification** status and waits for human input. If actionable, the agent produces a draft `spec.md` containing both the design (what to build) and the implementation plan (how to build it). The draft spec then enters **Spec Review** status — the human reviews, and optionally opens their coding agent to discuss and refine the spec with the agent. The pipeline does not proceed until the human explicitly approves the spec. |
| `03_Failing_Probe` | Agent Step | (Bug fix only) An agent creates a failing test that proves the bug exists on the current main branch. The test is executed and its failure is recorded as evidence. The pipeline does not proceed to coding until the bug is reproduced. |
| `04_Coding` | Agent Step + Command Step | An AI coding agent implements the spec in an isolated git worktree. After coding completes, command steps run tests, build, and lint. If any command step fails, the error output is fed back to the coding agent for repair (validate-and-repair loop, max configurable retries). Deterministic guardrail hooks verify that the agent did not violate constraints (e.g., modifying protected files). |
| `05_Independent_Review` | Agent Step | A separate AI agent, in a fresh session with no shared context from the coding step, reviews the changes against the spec. Produces a structured review report with risk scores (side-effect risk, performance risk, backward compatibility risk). Out-of-scope observations (pre-existing technical debt, missing tests in untouched code) are recorded as **warnings** — they do not block the pipeline but are visible in the dashboard. |
| `06_Human_Approval_Gate` | Human Step | The chain of evidence is presented to the human in the dashboard. The human approves or rejects. Rejection sends the job back to the coding step. |
| `07_Done` | Terminal | A Pull Request is created from the worktree changes. The human merges at their discretion. |

### Pipeline Profiles (Work Types)

Phase 1 provides four fixed pipeline profiles. The user selects the work type when creating a job.

| Work Type | Active Stages |
|-----------|---------------|
| **Bug Fix** | 01\_Intent → 02\_Clarification/Spec → 03\_Failing\_Probe → 04\_Coding → 05\_Review → 06\_Approval → 07\_Done |
| **Feature** | 01\_Intent → 02\_Clarification/Spec → 04\_Coding → 05\_Review → 06\_Approval → 07\_Done |
| **Refactor** | 01\_Intent → 04\_Coding → 05\_Review → 06\_Approval → 07\_Done |
| **Docs** | 01\_Intent → 04\_Coding → 05\_Review → 07\_Done |

> **Note:** The `docs` profile skips stage 06. However, if the review agent's decision is `request_changes`, the job routes to 06\_Human\_Approval\_Gate instead of proceeding directly to 07\_Done (see `spec.md` OQ-2, REV-6).

### Clarification and Spec Review Gates

The `02_Clarification_and_Spec` stage contains two sequential gates:

**Gate 1 — Clarification (conditional):**
The agent evaluates whether the intent is **actionable**:
- If clear and complete → proceed to spec generation.
- If ambiguous, incomplete, or contradictory → set status to **Needs Clarification** and halt. The human interacts with the agent to resolve ambiguities (see Human Interaction Model below). The pipeline resumes only after clarification is provided.

The agent must never proceed with assumptions on ambiguous requirements. Asking is always preferred over guessing.

**Gate 2 — Spec Review (mandatory):**
After the agent produces a draft `spec.md`, the job enters **Spec Review** status. The human reviews the spec and either:
- **Approves** → pipeline proceeds to the next step.
- **Refines** → opens their coding agent (via the copy-paste command) to discuss and modify the spec, then approves.

The spec is the foundation for all downstream work. It must not proceed without explicit human approval.

### Validate-and-Repair Loop

When a command step (tests, build, lint) fails after a coding agent step:
1. The orchestrator captures the error output (stdout/stderr, exit code).
2. The error is categorized (see Factory Failure Categorization).
3. If categorized as **Flawed** → the error output is sent back to the coding agent as input, and the agent is re-invoked to fix the issue.
4. This loop repeats up to a configurable maximum number of retries (default: 3).
5. If the maximum is exceeded, the job is escalated to the human.

### Factory Failure Categorization

When an agent or command step fails, the orchestrator categorizes the failure deterministically (no AI) based on exit codes and output patterns:

| Category | Signal | Action |
|----------|--------|--------|
| **Flawed** | Test failure, assertion error, lint error | Retry — feed error to agent |
| **Blocked** | Permission denied, timeout, connection refused, missing dependency | Escalate to human |
| **Manual** | Max retries exceeded | Escalate to human |

Phase 2 will introduce AI-enhanced failure categorization for more nuanced classification.

### Configurable Approval Gates

Each step in the pipeline can be configured with an optional human approval gate before or after execution. Phase 1 provides a fixed configuration with two mandatory gates:
- `02_Clarification_and_Spec` — **Spec Review** gate after the agent produces a draft spec (for work types that include this step).
- `06_Human_Approval_Gate` — **Final Approval** gate before PR creation.
- Other steps proceed automatically on success.

Phase 2 will allow per-step, per-work-type approval gate configuration.

### Chain of Evidence

At the `06_Human_Approval_Gate`, the human is presented with a structured evidence package assembled from all previous step outputs:

**Summary View:**
- Build status (pass/fail)
- Test results (pass/fail; per-test counts such as X/Y are Phase 2)
- Review decision and risk scores
- Number of files changed
- Warnings count (out-of-scope observations)

**Detail Drill-Down (on demand):**
- Full git diff
- Test execution logs
- Review agent's full report (including out-of-scope warnings)
- Agent execution logs
- Spec reference
- Failing probe result (for bug fixes)

No extra data collection is needed — this is a presentation layer over artifacts already produced by previous steps.

## Job Entry Points

Jobs can enter the pipeline through multiple channels:

1. **Intent File** — A file following the naming convention `*-intent.md`, created in a designated project directory, triggers the pipeline automatically.
2. **GitHub Issue** — Creating a GitHub Issue with `Status=01_Intent` in the linked GitHub Project triggers the pipeline.
3. **Dashboard** — The user creates a new job directly from the web UI, selecting the project and work type.

All three entry points are first-class citizens — any of them can initiate a full pipeline run.

## Human Interaction Model

When a job requires human input (clarification needed, approval gate, or human review), the primary interaction mechanism is:

**Dashboard Copy-Paste Command:**
The web dashboard displays a copy button next to each job that needs attention. The button copies a command like:
```
cd /path/to/project ; agy garagefab-work 178
```
or
```
cd /path/to/project ; opencode garagefab-work 178
```

The command includes the coding agent configured for that step. The user pastes this into their terminal, which:
1. Navigates to the project directory.
2. Launches the configured coding agent.
3. The `garagefab-work` skill (installed globally for each supported agent) reads the job details from garagefab's API and prepares the agent for the conversation.

**Skill Distribution:**
Garagefab installs its skills (e.g., `garagefab-work`) into the global configuration directory of each supported coding agent on the local machine. This enables any supported agent to interact with garagefab jobs.

**garagefab Skills:**
The skills defined within the garagefab skill package are in scope for Phase 1 development:
- **`garagefab-work`** — Enables the coding agent to interact directly with garagefab jobs during human-in-the-loop workflows (clarification, spec refinement, failure debugging). It fetches job details, intent, draft specs, error logs, and git worktree status from garagefab's local API and primes the agent for the conversation.

## Git Worktree Isolation

Every job runs all of its agent and command steps in an isolated git worktree created from the project's main branch. This ensures:
- The main branch is never directly modified by an agent.
- Multiple jobs can run concurrently without interfering with each other.
- Failed jobs can be discarded cleanly.
- The developer's working directory is never disturbed.

The worktree lifecycle:
1. **Created** when the job reaches its first agent step. Depending on the work type, that is the clarification/spec step (Feature, Bug Fix) or the coding step (Refactor, Docs). Agents in the spec and failing-probe steps need to read the repository, and the draft `spec.md` must live somewhere durable, so they cannot wait for the coding step.
2. **Used** by every agent and command step of the job (spec, failing probe, coding, test, review). Job artifacts such as `spec.md`, clarification answers, the review report, and the evidence summary are written under `.garagefab/jobs/<job-id>/` inside the worktree and committed on the job's branch, so they are included in the Pull Request.
3. **Converted to PR** at the Done step.
4. **Cleaned up** after the PR is created (or the job is cancelled). Jobs that fail or are interrupted keep their worktree until the human retries or cancels them.

When the human opens a coding agent to clarify or refine a job (see Human Interaction Model), the `garagefab-work` skill receives the job's worktree path from garagefab's API, so edits to the spec are made in the job's worktree rather than in the developer's own checkout.

## Web Dashboard

### Navigation Hierarchy

```
🏠 Overview (all projects summary)
  └── 📋 Project Board (kanban — read-only visibility of jobs across stages)
        └── 🔧 Task Detail (pipeline visualization + action buttons + artifacts)
```

### Overview Screen
- Number of jobs per status across all projects.
- Recent activity feed.
- Jobs requiring attention (pending approval, needs clarification, failed).

### Project Board
- Kanban-style board with columns matching pipeline stages.
- Cards represent individual jobs.
- **Read-only** — no drag-and-drop. The orchestrator manages state transitions.
- Visual indicators for job status (running, waiting, failed, needs attention).

### Task Detail
- Pipeline visualization showing which step the job is currently at.
- Action buttons: Approve, Reject, Retry, Cancel.
- Copy-paste command button for opening the job in a coding agent.
- Chain of evidence (summary + drill-down).
- Agent execution logs (accessible on demand, not cluttering the main view).
- Out-of-scope warnings from review agent (⚠️ badge).

### Project Onboarding
New projects are added via the dashboard: "Add Project" → specify repository path → configure basic settings (which agents to use, which pipeline profiles to enable).

## Multi-Project Support

Garagefab can manage multiple independent projects simultaneously. Each project has:
- Its own repository path.
- Its own agent configuration (which coding agent, which review agent per step).
- Its own job queue and pipeline state.
- Its own section in the dashboard.

Jobs from different projects run independently and do not interfere with each other.

## Logging and Audit

### Phase 1
- **Step metadata**: Every pipeline step records start time, end time, duration, status (success/fail/skipped), and the agent or command that executed it.
- **Agent logs**: Full stdout/stderr from agent and command step executions is captured and stored. Accessible on demand from the task detail view — not displayed by default to avoid clutter.
- **Artifact trail**: All produced artifacts (spec.md, test results, review reports, diffs) are stored and linked to the job.

### Phase 2
- Token usage tracking per agent invocation.
- Cost estimation per job and per project.
- Aggregated metrics and analytics.

## GitHub Integration

### Phase 1
- **GitHub Issues** as a job entry point (issue with `Status=01_Intent` triggers pipeline).
- **GitHub Projects** for external visibility (job status mirrored to project board columns: `01_Intent` through `07_Done`).
- **Pull Requests** created automatically at the `07_Done` stage.

### Phase 2
- Extensible issue tracker interface (JIRA, GitLab Issues, etc.).

## MVP Success Criteria

The MVP is considered successful when the following two scenarios work end-to-end:

### Scenario 1: Happy Path (Automated Flow with Spec Review)
> The user creates a GitHub Issue with `Status=01_Intent` (type: feature) for a registered project. Garagefab detects it and runs the pipeline:
> - Spec agent produces a draft `spec.md` (design + implementation plan)
> - User reviews and approves the spec in the dashboard (or refines it via coding agent)
> - Coding agent implements the approved spec in an isolated worktree
> - Command steps run tests and build — all pass
> - Independent review agent produces a review report with risk scores
> - Dashboard displays the chain of evidence at the final approval gate
> - User approves → PR is created → job moves to Done
>
> **Human intervention: creating the issue, reviewing/approving the spec, and approving at the final gate.**

### Scenario 2: Clarification Flow (Human-in-the-Loop)
> The user creates a GitHub Issue with an ambiguous description. Garagefab detects it and starts the pipeline. The spec agent determines the intent is not actionable and sets the job to "Needs Clarification." The user sees this in the dashboard, copies the terminal command, opens the agent, answers clarifying questions, and the agent updates the job. The pipeline resumes and completes as in Scenario 1.
>
> **Human intervention: creating the issue, clarifying the ambiguity, and approving at the gate.**

## Constraints

- **Local-first**: Phase 1 runs entirely on the developer's local machine. No cloud services required (except GitHub for issues/PRs/projects).
- **Single binary**: The entire application (orchestrator, dashboard, database) ships as one executable.
- **Zero external dependencies**: No external databases, message queues, or services beyond what the binary provides (embedded DB, embedded web server).
- **Code quality**: Code should be clean and well-documented.
- **English code and docs**: All code, comments, and generated documents are in English. Conversations with the developer may be in Turkish.

## Phase 2 Roadmap

The following features are explicitly deferred to Phase 2 to keep the MVP focused:

### Factory Intelligence
- **Factory Brain (Project Memory)** — Per-project persistent memory with core facts (push) and deep archive (pull). Git-backed Markdown files indexed with SQLite FTS5. Smart injection of relevant rules into agent prompts.
- **Agent-Generated Issues** — Umbrella for three categories: Agent Suggestions (process improvement proposals), Agent Discoveries (technical debt, missing tests found during work), and Agent Complaints (requests for more access or information).
- **Continuous Evals** — Benchmarking agent performance against a suite of past tasks to detect regression when prompts or models change.
- **AI-Enhanced Failure Categorization** — Using an AI agent to classify failures more accurately than deterministic pattern matching.

### Pipeline Customization
- **Custom Work Types** — Users define their own work types with custom pipeline step configurations.
- **Custom Step Configuration** — Users modify which steps are active for each work type.
- **Triage Agent** — Automatic classification of incoming jobs into work types by a lightweight AI agent at pipeline entry.
- **Configurable Merge Behavior** — Per-work-type configuration of auto-merge vs. PR-only vs. manual merge.
- **Per-Step Approval Gate Configuration** — Fine-grained control over which steps require human approval.

### Operational
- **Token Budget and Cost Control** — Per-job and per-step token budgets with automatic halt on budget exceeded. Important due to non-linear cost compounding in retry loops: token usage grows super-linearly (not just 3x for 3 retries, but potentially 5-7x due to context accumulation).
- **OS Notifications** — macOS/Linux system notifications for approval gates and job completions.
- **Background Mode and Service Installation** — `install-service` for launchd/systemd and a built-in background mode.
- **Test Report Parsing** — Per-test result counts (e.g., JUnit XML) in the chain of evidence.
- **Web UI Direct Terminal Launch** — "Open in Terminal" button that launches the coding agent directly from the dashboard via backend API.
- **Self-Healing CI** — Automatic detection and repair of CI failures after PR creation (max retry budget to prevent runaway costs).

### Extended Integrations
- **Additional Coding Agents** — Claude Code, Codex, Aider, and others.
- **Additional Issue Trackers** — JIRA, GitLab Issues, and others via extensible provider interface.
- **Maintain Stage** — Production monitoring loop that watches for anomalies and automatically generates intent files for fixes.
- **Domain-Specialist Reviewers** — Parallel specialized review agents (security, performance, infrastructure) based on PR risk profile.

### Advanced Features
- **Security Sandboxes** — Running agents in isolated environments with scoped secrets and network filtering.
- **Context Lake** — Centralized graph of service ownership, incident history, and dependencies for enriched agent context.
- **garagefab-work Slash Command** — In-agent command to pull garagefab job context without leaving the agent session.

## References

- **AI-Native SDLC Playbook** (Claude Academy) — Primary conceptual framework for the pipeline design and AI-native development principles.
- **Implementing the AI-Native SDLC Playbook** (Claude Academy) — Practical implementation patterns and platform harness architecture.
- **InterCode 2026, TrueFoundry, Pragmatic Engineer** — Industry perspectives on agentic software factories, failure categorization, and operational lessons.
- **Vercel AI SDK** — Reference for clean workflow and tool-loop agent implementation patterns (for the spec phase).
