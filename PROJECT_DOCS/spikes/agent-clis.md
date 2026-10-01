# Spike A: Agent CLI Research

> Date: 2026-10-01  
> Tested versions: `agy 1.2.14`, `opencode 1.18.34`  
> Platform: macOS arm64 (Darwin 24)  
> Repository test bed: `/tmp/garagefab-spike-a/`

---

## 1. Summary Table

| # | Question | agy | opencode |
|---|----------|-----|----------|
| **Q1** | **Headless run** | `agy --print "<prompt>" --dangerously-skip-permissions` (Alias: `-p`). Exits cleanly when turn completes. | `opencode run --auto "<prompt>"`. Exits cleanly when turn completes. |
| **Q2** | **Prompt delivery** | Passed via command-line argument. Piped raw stdin without flag fails with Exit 2 (`--print` expects prompt arg). | Passed via positional argument `[message..]`. Piped raw stdin without argument fails with Exit 1 (`You must provide a message`). |
| **Q3** | **Fresh session** | Standalone invocation defaults to fresh session (`conversation_id` created anew). Resuming requires `--continue` / `-c` or `--conversation <id>`. | Standalone invocation defaults to fresh session. Resuming requires `--continue` / `-c` or `--session <id>`. |
| **Q4** | **Permissions** | `--dangerously-skip-permissions` auto-approves all tool actions (file reads, edits, command execution) without prompting. | `--auto` auto-approves all permissions that are not explicitly denied without prompting. |
| **Q5** | **Working directory** | Respects process working directory (invoked cwd). Tool operations stay rooted in cwd. | Respects process working directory (invoked cwd) or `--dir <path>`. |
| **Q6** | **Exit codes** | **Exit 0 on both success and agent-level task failure!** Non-zero exit (e.g. 2) only on CLI flag/syntax errors or hard crashes. | **Exit 0 on both success and agent-level task failure!** Non-zero exit (e.g. 1) only on CLI parameter errors or hard crashes. |
| **Q7** | **Output format** | Supports `--output-format json` (single turn summary JSON object with status, usage, response) and `stream-json` (NDJSON turns). | Supports `--format json` emitting line-delimited NDJSON events (`step_start`, `text`, `tool_use`, `step_finish`). |
| **Q8** | **Instructions file** | Automatically loads **BOTH** `AGENTS.md` and `GEMINI.md` simultaneously into session rules. | Automatically reads `AGENTS.md`. |
| **Q9** | **Skills** | Global skills located in `~/.gemini/antigravity/builtin/skills/` and plugins in `~/.gemini/config/plugins/`. In print mode, skills are enabled unless `--disable-slash-commands` is passed. | Plugins and modules managed via `opencode plugin <module>`. Tool integrations via MCP / local scripts. |
| **Q10** | **Credentials** | Reads credentials from `~/.gemini/antigravity/` configuration, Google OAuth token, and API keys. | Configured via `opencode providers` (login/logout/list) or environment variables (`OPENAI_API_KEY`, etc.). |
| **Q11** | **No TTY** | Works without terminal (e.g. redirected from `/dev/null` and piped to `cat`) as long as prompt is passed as argument. | Works without terminal (redirected from `/dev/null` and piped to `cat`) as long as prompt is passed as argument. |
| **Q12** | **Process tree** | Direct binary execution. Child commands (e.g. `go run`, shell scripts) execute within child processes in the process group. Killing the process group cleanly terminates children. | Node/runtime execution. Child tools run within subshells in the process group. Group termination kills children cleanly. |
| **Q13** | **Versions** | `agy --version` outputs semver `1.2.14`. | `opencode --version` outputs semver `1.18.34`. |

---

## 2. Detailed Findings

### Q1 — Headless Run
- **`agy`**:
  - Command: `agy --print "<prompt>" --dangerously-skip-permissions`
  - Output: Prints the agent's textual response directly to stdout and exits.
  - Duration observed: ~35–45 seconds for a single code edit + test verification cycle.
  - Behavior: Never prompts interactively; terminates with exit code 0 when finished.
- **`opencode`**:
  - Command: `opencode run --auto "<prompt>"`
  - Output: Streams reasoning and tool execution summary, then prints completion and exits.
  - Duration observed: ~12–18 seconds for single code edit + test verification cycle.
  - Behavior: Runs completely unattended; terminates with exit code 0.

### Q2 — Prompt Delivery
- **Argument**:
  - Both CLIs natively support passing multi-line prompts as string arguments.
  - Tested: Short prompt (~50 chars) and multi-line task specs with special characters and quotes.
- **Piped Stdin**:
  - Tested: `cat prompt.txt | agy --print --dangerously-skip-permissions < /dev/null | cat`
    - Result: Failed with exit code 2: `Error: --print took "--dangerously-skip-permissions" as its prompt`. `--print` expects the prompt directly as its argument value.
  - Tested: `cat prompt.txt | opencode run --auto < /dev/null | cat`
    - Result: Failed with exit code 1: `Error: You must provide a message or a command`. `opencode run` requires the message argument.
- **Recommendation**: In Garagefab adapters (`worker/agent`), deliver prompts as command-line arguments, or pass prompt file paths if supported.

### Q3 — Fresh Session
- **`agy`**:
  - Standalone runs generate a new unique `conversation_id` (e.g., `fd3a3d31-b87b-4871-ac81-53592e6ea4bd`).
  - When asked about previous questions in the project, it explicitly confirmed:
    > *"In this current session: Each standalone CLI invocation starts a new session with a fresh context..."*
  - Resuming a session requires explicit `--continue` or `--conversation <id>`.
- **`opencode`**:
  - Standalone runs generate a new session ID (`sessionID: "ses_..."`).
  - When asked about previous context without `--continue`:
    > *"No history accessible. This prompt first seen in session. Checked workspace + git log — no prior questions stored."*
  - Resuming requires `--continue` or `--session <id>`.

### Q4 — Permissions
- Both tools require a specific bypass flag to operate autonomously without interactive terminal prompts:
  - `agy`: `--dangerously-skip-permissions` auto-approves all tool requests (reading files, editing files, running bash commands).
  - `opencode`: `--auto` auto-approves actions not explicitly denied.
- **Risk R1 Closed**: Both agents have fully working, reliable unattended execution flags.

### Q5 — Working Directory
- Both agents execute in the current working directory of the process (`cwd`).
- In `/tmp/garagefab-spike-a/`, both agents immediately discovered `hello.go`, `hello_test.go`, and `AGENTS.md`.
- `opencode` also accepts an explicit `--dir <path>` parameter.

### Q6 — Exit Codes
- **Critical Architectural Finding**:
  - When an agent successfully completes a task: **Exit code `0`**.
  - When an agent encounters an impossible task or internal failure (e.g. explicitly instructed to fail): **Exit code `0`**.
  - Neither agent CLI surfaces prompt/task failures as OS-level non-zero exit codes. Non-zero exit codes (1 or 2) are only emitted on invalid CLI flags, missing arguments, or process-level crashes.
- **Implication for Garagefab**:
  - Step execution status (`step_runs.status`) **cannot** be determined solely by `cmd.ProcessState.ExitCode()`.
  - For `agy`: Garagefab must parse `--output-format json` and inspect `"status": "SUCCESS"`.
  - For `opencode`: Garagefab must inspect the NDJSON event stream (`"type": "step_finish"`) and verify expected artifact/git changes.

### Q7 — Output Format
- **`agy`**:
  - `--output-format json` outputs a single structured JSON payload on exit:
    ```json
    {
      "conversation_id": "fd3a3d31-b87b-4871-ac81-53592e6ea4bd",
      "status": "SUCCESS",
      "response": "...",
      "duration_seconds": 46.63,
      "num_turns": 1,
      "usage": {
        "input_tokens": 116447,
        "output_tokens": 3824,
        "thinking_tokens": 2680,
        "cache_read_tokens": 301169,
        "total_tokens": 120271
      }
    }
    ```
  - Also supports `--output-format stream-json` for turn-by-turn streaming.
- **`opencode`**:
  - `--format json` outputs a real-time newline-delimited JSON (NDJSON) event stream:
    - `{"type":"step_start", ...}`
    - `{"type":"text", ...}`
    - `{"type":"tool_use", "part":{"tool":"read"|"edit"|"bash", ...}}`
    - `{"type":"step_finish", ...}`
  - Ideal for Garagefab's live SSE stream to forward tool activities directly to the UI.

### Q8 — Instructions File
- Tested with both `AGENTS.md` and `GEMINI.md` present in the test repository:
  - `agy` reported:
    > *"The following instruction files were loaded into the session rules: 1. AGENTS.md, 2. GEMINI.md"*
    **Both files are read and combined into the agent rules!**
  - `opencode` read `AGENTS.md`.
- **Closes Plan item P-2**: `AGENTS.md` is standard and fully recognized by both agents.

### Q9 — Skills
- **`agy`**:
  - Skills are installed globally in `~/.gemini/antigravity/builtin/skills/` and plugins under `~/.gemini/config/plugins/`.
  - In headless print mode, skills and slash commands are expanded by default (unless `--disable-slash-commands` is passed).
  - The handoff command `garagefab-work <job-id>` can be packaged as a skill (`SKILL.md`) in `~/.gemini/antigravity/skills/` or project `.gemini/skills/`.
- **`opencode`**:
  - Manages plugins and extensions via `opencode plugin` and MCP configurations.

### Q10 — Credentials
- **`agy`**: Managed through `~/.gemini/` configuration and Google authentication tokens.
- **`opencode`**: Managed through `opencode providers` or environment variables (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, etc.).
- **Adapter config**: Garagefab's `engine.env_passthrough` in `config.yaml` should pass `PATH`, `HOME`, `USER`, `SHELL`, and provider API keys (`GEMINI_API_KEY`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`).

### Q11 — No TTY
- Tested with stdin redirected (`< /dev/null`) and stdout piped (`| cat`).
- Both tools function without an interactive terminal when the prompt is supplied via CLI arguments.

### Q12 — Process Tree
- `agy` runs as a standalone process binary (`agy --print ...`).
- `opencode` runs as a Node-based CLI (`opencode run ...`).
- When executing sub-tools (e.g. bash scripts), processes are spawned within the parent process group.
- Using process groups (`Setpgid: true` in Go) and sending signals (`SIGTERM` / `SIGKILL` to `-pgid`) guarantees clean termination of all descendant processes without orphans.

### Q13 — Versions
- `agy --version`: `1.2.14`
- `opencode --version`: `1.18.34`
- Both commands write a single line version string to stdout and exit 0.

---

## 3. Adapter Design Implications

1. **Invocation Shape**:
   - `agy`: `agy --print <prompt> --dangerously-skip-permissions --output-format json` (with optional `--effort low|medium|high`)
   - `opencode`: `opencode run --auto --format json <prompt>`
2. **Success / Failure Detection**:
   - Garagefab cannot rely on process exit codes for agent turns. Adapters must:
     - Check exit code (must be 0 for a clean run).
     - Check JSON payload (`status == "SUCCESS"` for `agy`, absence of error events for `opencode`).
     - Inspect git diff or expected artifacts (e.g. `spec.md`, `review.md`).
3. **Execution Speed Optimization**:
   - `agy` spends significant time generating thinking tokens. The adapter should expose an `--effort` configuration flag (e.g., `medium` for coding/refactor, `high` for spec review).

---

## 4. Decisions to Update

- **`PROJECT_DOCS/02_architecture.md` (O2)**:
  - Marked resolved. Exact headless invocations, prompt flags, and auto-approval permissions documented.
- **`PROJECT_DOCS/03_spec.md` (OQ-7)**:
  - Marked resolved. Skill directory structure and CLI flag expansions verified.
- **`PROJECT_DOCS/04_plan.md` (P-2 & P-8)**:
  - P-2: `agy` reads both `AGENTS.md` and `GEMINI.md`. `AGENTS.md` is the primary repository standard.
  - P-8: Skill locations (`~/.gemini/antigravity/...`) verified.
