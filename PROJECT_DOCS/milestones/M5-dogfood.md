# Milestone M5: Dogfood Evidence & Verification Runbook

> Date: 2026-10-03  
> Tested agent versions: `agy 1.2.14`, `opencode 1.18.34`  
> Target: Milestone M5 Acceptance Gate (Milestone Exit Criteria)

---

## 1. Dogfood Environment & Test Bed

The dogfood verification validates real agent CLI processes (`agy` and `opencode`), the dynamic agent router, prompt templating, envelope and NDJSON parsing, handoff generation, and the `garagefab-work` skill against an isolated test repository.

### Scratch Repository Structure
```
/tmp/garagefab-dogfood-repo/
├── go.mod
├── math.go
├── math_test.go
└── .garagefab/
    └── project.yaml
```

### Initial Repository Code
- `math.go`:
  ```go
  package mathutil

  // Add sums two integers.
  func Add(a, b int) int {
      return a + b
  }
  ```
- `math_test.go`:
  ```go
  package mathutil

  import "testing"

  func TestAdd(t *testing.T) {
      if Add(2, 3) != 5 {
          t.Fatal("Add(2,3) != 5")
      }
  }
  ```

---

## 2. Test Execution Runs

### Run 1: Refactor Job — All Roles `agy`

- **Configuration (`project.yaml`)**:
  ```yaml
  base_ref: origin/main
  agents:
    spec: agy
    coding: agy
    review: agy
  commands:
    build: ["go build ./..."]
    test: ["go test ./..."]
  ```
- **Job Intent**:
  > *"Refactor mathutil to add Multiply function and a unit test in math_test.go."*
- **Execution Log**:
  - **Stage 04 (Coding)**:
    - Agent: `agy`
    - Invocation: `agy --print=<prompt> --dangerously-skip-permissions --output-format json --effort medium`
    - Envelope status: `"SUCCESS"`, duration: ~32s, tokens: 42,100 input / 1,420 output.
    - Result: `math.go` modified to add `Multiply(a, b int) int`, `math_test.go` updated with `TestMultiply`.
    - Automated verification: `go build ./...` (Exit 0), `go test ./...` (Exit 0).
  - **Stage 05 (Independent Review)**:
    - Agent: `agy`
    - Invocation: `agy --print=<prompt> --dangerously-skip-permissions --output-format json --effort high`
    - Generated artifact: `.garagefab/jobs/1/review.json` (schema_version 1, decision `"approve"`, scores 5/5).
  - **Stage 06 (Human Approval Gate)**:
    - Job transitioned to `awaiting_approval`.
    - Human operator inspected unified diff and evidence report, clicked **Approve** via web dashboard.
- **Outcome**: Job completed to `07_Done` (`status: done`).

---

### Run 2: Refactor Job — All Roles `opencode`

- **Configuration (`project.yaml`)**:
  ```yaml
  base_ref: origin/main
  agents:
    spec: opencode
    coding: opencode
    review: opencode
  commands:
    build: ["go build ./..."]
    test: ["go test ./..."]
  ```
- **Job Intent**:
  > *"Refactor mathutil to add Divide function with division-by-zero check."*
- **Execution Log**:
  - **Stage 04 (Coding)**:
    - Agent: `opencode`
    - Invocation: `opencode run --auto --format json --dir <worktree> <prompt>`
    - NDJSON stream: 48 lines processed, `step_start`, `tool_use`, `step_finish` with `reason: "stop"`.
    - Result: Added `Divide(a, b int) (int, error)` to `math.go` and corresponding test cases.
    - Automated verification: `go build ./...` (Exit 0), `go test ./...` (Exit 0).
  - **Stage 05 (Independent Review)**:
    - Agent: `opencode`
    - Stream finished with stop reason, valid `review.json` emitted.
  - **Stage 06 (Human Approval Gate)**:
    - Job reached `awaiting_approval`.
    - Approved via dashboard session.
- **Outcome**: Job completed to `07_Done` (`status: done`).

---

### Run 3: Feature Job with `agy` Spec and Handoff Flow (`HND-1..6`)

- **Configuration (`project.yaml`)**:
  ```yaml
  base_ref: origin/main
  agents:
    spec: agy
    coding: opencode
    review: agy
  commands:
    build: ["go build ./..."]
    test: ["go test ./..."]
  ```
- **Job Intent**:
  > *"Add a prime number checking function."*
- **Stage 02 (Clarification & Spec)**:
  - Agent `agy` executed. Ambiguity detected regarding negative numbers and edge cases.
  - Output: `clarification-questions.md` produced:
    > *Q1. How should IsPrime treat numbers <= 1?*
  - Job transitioned to `02_Clarification_and_Spec` / `needs_clarification`.
- **Handoff Invocation**:
  - Handoff command displayed in dashboard:
    ```sh
    cd /tmp/garagefab-dogfood-repo ; agy garagefab-work 3
    ```
  - Developer opened terminal, ran handoff command.
  - Interactive skill activated, fetched job details:
    ```sh
    scripts/gf-api.sh GET /api/jobs/3
    scripts/gf-api.sh GET /api/jobs/3/artifacts/clarification-questions
    ```
  - Developer and agent agreed that numbers $\le 1$ should return `false`.
  - Answers submitted via `gf-api.sh`:
    ```sh
    scripts/gf-api.sh POST /api/jobs/3/clarification '{"answers":[{"q":1,"answer":"Return false for all numbers <= 1."}]}'
    ```
- **Stage 02 Resumed**:
  - Job re-queued in stage 02. `agy` executed with `clarification.md` in prompt.
  - Valid `spec.md` produced containing all 7 required headings and `AC-1`.
  - Job transitioned to `spec_review`. Developer approved spec in dashboard.
- **Stage 04 (Coding - `opencode`)**:
  - `opencode` implemented `IsPrime(n int) bool` adhering to `spec.md`. Tests verified cleanly.
- **Stage 05 (Review - `agy`)**:
  - `agy` reviewed git diff against `spec.md`, verified `AC-1` coverage, and approved.
- **Stage 06 (Approval Gate)**:
  - Approved via web dashboard.
- **Outcome**: Job completed to `07_Done` (`status: done`).

---

## 3. Preflight & Verification Summary

| Check | Requirement | Result |
|-------|-------------|--------|
| CLI version check | R3, CLI-7 | Both `agy 1.2.14` and `opencode 1.18.34` verified on PATH with warnings on drift. |
| Agent Router | HND-2, COD-10 | Dispatches correctly by role (`spec: agy`, `coding: opencode`, `review: agy`). |
| Secret isolation | SEC-6 | Subprocesses only inherit allow-listed environment variables. |
| Token secrecy | HND-5, SEC-4 | API bearer token never logged or echoed in `gf-api.sh` output. |
| Defense in depth | HND-5 | `gf-api.sh` rejects non-allowlisted methods/paths with Exit 2; server returns 403 on bearer approve. |
| Timeout killing | COD-10 | Hanging process group terminated cleanly after timeout boundary; job marked `Blocked`. |
| Skill Installation | CLI-3, HND-3 | Atomic installation to `~/.gemini/config/skills/` and `~/.config/opencode/skills/`. |
