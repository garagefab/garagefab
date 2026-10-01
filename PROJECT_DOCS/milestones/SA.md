# SA — Spike A: Agent CLIs

> Status: **Not started**
> Size: S · Depends on: —
> Unblocks: M5 (adapters, prompts, skill), `architecture.md` O2, `spec.md` OQ-7

## Goal

Replace assumptions about `agy` (Antigravity CLI) and `opencode` with verified facts. Every question below must be answered for **both** agents with exact commands and recorded outputs. The output is a reference document and a set of test fixtures.

## Deliverables

| Artifact | Location | Purpose |
|----------|----------|---------|
| Spike report | `docs/spikes/agent-clis.md` | Answers all 13 questions with evidence |
| Test fixtures | `docs/spikes/fixtures/agy/` and `docs/spikes/fixtures/opencode/` | Recorded real outputs for adapter contract tests |
| Decision updates | `PROJECT_DOCS/02_architecture.md` §3 | New entries if any assumption changes |
| Open question closures | `architecture.md` O2, `spec.md` OQ-7 | Mark resolved with references to findings |

## Method

This is a **research spike**, not a coding task. The work is:
1. Read documentation and source code for each agent.
2. Run controlled experiments in a temporary Git repository.
3. Record exact commands, outputs, and observations.
4. Write findings into the spike report.

### Test Repository Setup

Create a minimal temporary Git repository for experiments:
```
/tmp/garagefab-spike-a/
├── .git/
├── AGENTS.md          # minimal instructions
├── GEMINI.md          # if needed for agy
├── hello.go           # trivial file for agents to edit
└── hello_test.go      # trivial test for agents to run
```

Each experiment runs in a clean state (reset the repo between runs).

---

## Questions and Experiment Plan

### Q1 — Headless Run

**Question:** What is the non-interactive invocation? Does it exit when done?

**Experiments:**
- `agy`: Try `agy "write hello world to hello.go"` and variations. Check if it runs without interactive prompts and exits on completion.
- `opencode`: Try `opencode -p "write hello world to hello.go"` or equivalent. Check documentation for headless/non-interactive flags.
- For each: record the exact command, whether it blocks waiting for input, and whether it exits cleanly.

**Record:** Command, stdout/stderr (first and last 50 lines), exit code, wall-clock duration.

### Q2 — Prompt Delivery

**Question:** Argument, stdin, or file? Length limits?

**Experiments:**
- Try passing prompts via: CLI argument, stdin pipe, file flag (if available).
- Test with a short prompt (~100 chars) and a long prompt (~10K chars, typical job spec size).
- Check if there's a documented or practical length limit.

**Record:** Each delivery method with the exact command and whether it worked.

### Q3 — Fresh Session

**Question:** How do we guarantee no resume or shared history between invocations?

**Experiments:**
- Run agent twice in sequence with different prompts. Does the second run reference the first?
- Check for session/conversation files: `~/.agy/`, `~/.opencode/`, `~/.config/`, etc.
- Look for `--no-resume`, `--new-session` or similar flags.
- Check if a unique `--session-id` or similar can be used.

**Record:** Session file locations, flags that control history, verified behavior.

### Q4 — Permissions

**Question:** Are there approval prompts that block unattended runs? Which flags or settings allow file edits and command execution without prompting?

**Experiments:**
- Run agent with a prompt that requires file editing. Does it ask for permission?
- Run agent with a prompt that requires running a shell command. Does it ask?
- Find flags like `--auto-approve`, `--yes`, `--dangerously-skip-permissions` or equivalent.
- Check config files for permission settings.

**Record:** Default behavior, flags/settings for auto-approval, what each permission level allows.

> [!WARNING]
> This is critical for unattended operation. If an agent has no reliable way to skip approval prompts, Garagefab cannot run it autonomously — this is Risk R1.

### Q5 — Working Directory

**Question:** Does the agent respect the process working directory and stay inside it?

**Experiments:**
- `cd /tmp/garagefab-spike-a && agy "list files in this directory"` — does it see the test repo?
- Ask the agent to create a file — does it land in the working directory?
- Ask the agent to read a file outside the working directory — can it?

**Record:** Working directory behavior, any sandboxing or path restrictions.

### Q6 — Exit Codes

**Question:** What do success, agent-reported failure, timeout, and crash look like?

**Experiments:**
- Successful run → record exit code (expect 0).
- Ask agent to do something impossible (e.g., "compile this Python file as Go") → record exit code.
- Kill the agent with SIGTERM during a run → record behavior.
- Kill with SIGKILL → record behavior.
- If there's a timeout flag, set it very short and let it expire.

**Record:** Exit code for each case, any stderr output patterns.

### Q7 — Output

**Question:** Plain text or a structured stream? Is there a stable way to see tool activity?

**Experiments:**
- Run a successful task and capture stdout and stderr separately.
- Check if output is plain text, JSON lines, or a custom format.
- Look for `--json`, `--output-format`, or verbose/debug flags.
- Check if tool calls (file edits, command runs) are reported in the output.

**Record:** Sample output (first 100 lines), format, any structured logging options.

### Q8 — Instructions File

**Question:** Which project file does each agent read (`AGENTS.md`, `GEMINI.md`, other)?

**Experiments:**
- Create `AGENTS.md` with a unique marker string. Run agent — does it reference the marker?
- Create `GEMINI.md` with a different marker. Run agent — does it reference that?
- Check documentation for configurable instruction file names.
- Test with both files present — which takes priority?

**Record:** Which files are read, priority order, configuration options.

> [!NOTE]
> This answers plan open item P-2.

### Q9 — Skills

**Question:** Where are global skills installed, and what is the skill file format? How is a skill invoked from the handoff command?

**Experiments:**
- Check documentation for skill/plugin installation directories.
- `agy`: look in `~/.agy/`, `~/.config/agy/`, `~/.gemini/` for skill definitions.
- `opencode`: look in `~/.opencode/`, `~/.config/opencode/` for equivalent.
- Examine the format of existing installed skills (if any).
- Test invoking a skill: `agy garagefab-work 123` — how does the agent resolve `garagefab-work`?

**Record:** Skill directory paths, file format (YAML, JSON, Markdown?), invocation mechanism, examples.

> [!NOTE]
> This answers `spec.md` OQ-7 and plan open item P-8.

### Q10 — Credentials

**Question:** Which environment variables or config files must be passed through?

**Experiments:**
- Check which env vars each agent requires (API keys for LLM providers).
- `agy`: likely needs `GEMINI_API_KEY` or similar.
- `opencode`: check for `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, or similar.
- Test: unset the expected env var and run — what error appears?
- Check if agents read from config files instead of env vars.

**Record:** Required env vars, optional env vars, config file locations, error messages when missing.

### Q11 — No TTY

**Question:** Does behavior change when stdin or stdout is not a terminal?

**Experiments:**
- Run with stdin from `/dev/null`: `agy "list files" < /dev/null`
- Run with stdout piped: `agy "list files" | cat`
- Run with both: `agy "list files" < /dev/null 2>&1 | cat`
- Compare output format and behavior to interactive run.

**Record:** Differences in output, prompts, or behavior. Whether the agent detects and adapts to non-TTY.

### Q12 — Process Tree

**Question:** Which child processes does the agent start, and does killing the process group clean them up?

**Experiments:**
- Run agent with a task that takes a few seconds.
- While running, inspect the process tree: `pstree -p <pid>` or `ps -o pid,ppid,pgid,command --forest`.
- Record all child processes.
- Kill the process group: `kill -- -<pgid>` — are all children gone?
- Kill only the parent with SIGTERM — do children persist as orphans?

**Record:** Process tree snapshot, child process names, cleanup behavior for SIGTERM and group kill.

> [!NOTE]
> This is critical for crash recovery (`RCV-1..4`) and Risk R5.

### Q13 — Versions

**Question:** Which versions were tested; how can the adapter detect an incompatible one?

**Experiments:**
- Record exact version: `agy --version`, `opencode --version` (or equivalent).
- Check if there's a machine-readable version output.
- Review changelogs for recent breaking changes to flags or behavior.

**Record:** Version string format, version command, any API stability guarantees.

---

## Output Structure

The spike report (`docs/spikes/agent-clis.md`) should follow this template:

```markdown
# Spike A: Agent CLI Research

> Date: YYYY-MM-DD
> Tested versions: agy vX.Y.Z, opencode vX.Y.Z
> Platform: macOS arm64

## Summary Table

| # | Question | agy | opencode |
|---|----------|-----|----------|
| 1 | Headless run | ... | ... |
| ... | ... | ... | ... |

## Detailed Findings

### Q1 — Headless Run
#### agy
[exact commands, outputs, conclusions]
#### opencode
[exact commands, outputs, conclusions]

### Q2 — ...
...

## Adapter Design Implications
[What this means for the AgentRunner interface and adapters]

## Decisions to Update
[Changes needed in architecture.md and spec.md]

## Open Issues
[Anything unresolved that needs follow-up]
```

## Test Fixtures

For each agent, capture and commit these files under `docs/spikes/fixtures/<agent>/`:

| Fixture | Description |
|---------|-------------|
| `success_stdout.txt` | stdout from a successful simple task |
| `success_stderr.txt` | stderr from a successful simple task |
| `failure_stdout.txt` | stdout from a failing task |
| `failure_stderr.txt` | stderr from a failing task |
| `timeout_stderr.txt` | stderr when killed or timed out |
| `version_output.txt` | output of version command |
| `process_tree.txt` | pstree/ps output during a run |

These fixtures become the basis for adapter unit tests in M5.

## Exit Criteria

- [ ] All 13 questions answered for both `agy` and `opencode` with recorded evidence.
- [ ] `docs/spikes/agent-clis.md` is complete and follows the template above.
- [ ] Test fixtures committed under `docs/spikes/fixtures/`.
- [ ] `architecture.md` O2 marked resolved with a reference to findings.
- [ ] `spec.md` OQ-7 answered (skill locations and format).
- [ ] Plan open items P-2 (instructions file) and P-8 (skill format) answered.
- [ ] Any assumption changes recorded as decision log entries in `architecture.md` §3.
- [ ] If Risk R1 materializes (no reliable headless mode), a mitigation plan is documented.

## Risks

| Risk | Mitigation |
|------|------------|
| R1 (no headless mode) | Design adapter for PTY-based launcher; decide scope early |
| Agent not installed or API key unavailable | Document installation steps; test one agent at a time |
| Agent behavior differs between macOS and Linux | Note platform in findings; CI on both later |
