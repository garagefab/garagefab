# Guardrail/Spec Alignment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the spec agent aware of the project's `protected_paths` so generated specs never mandate editing protected files, eliminating the guaranteed first-attempt `GRD-1` violation and the test-file fragmentation it causes.

**Architecture:** The spec stage already receives `projCfg`, but its `PromptData` omits `ProtectedPaths` (only the coding prompt sets it). Populate the field in `executeSpecStage` and render one conditional constraint section in `spec.md.tmpl`. No pipeline state-machine change, no new dependency.

**Tech Stack:** Go 1.22, `text/template` (`missingkey=error`), golden-file prompt tests.

**Spec:** `PROJECT_DOCS/03_spec.md` (`SPC-1`, `SPC-2`, `GRD-1`, `GRD-4`); design rationale in the Context section below and the M6 manual-verification note in `PROJECT_DOCS/milestones/M6.md`.

## Context (why)

Observed during the M6 real-agent run (job 1, `opencode`):

- The `feature` spec agent produced a plan that mandated editing the existing `calc_test.go`.
- `guardrails.protected_paths` defaults to `["**/*_test.go"]`, and `CheckProtectedPaths` protects **existing** files matching the glob (new files are allowed).
- Coding attempt 1 therefore modified `calc_test.go` → `GRD-1` violation → `Flawed` → repair. Attempt 2 restored the file and created `subtract_test.go`, which the independent review then flagged as a spec deviation.

Root cause: the spec agent is never told which paths are protected. `ProtectedPaths` reaches only `coding.md.tmpl` (`internal/factory/prompt.go:57`, `internal/factory/prompts/coding.md.tmpl:35-36`); the spec stage's `PromptData` (`internal/factory/pipeline.go:364-371`) leaves it empty. Result: a first attempt that is guaranteed to violate by construction.

## Global Constraints

- All code, comments, and docs in English; educational code comments required (AGENTS.md).
- `CGO_ENABLED=0 go build ./...`, `go vet ./...`, `go test ./...`, and `golangci-lint run ./...` must pass; `make ci` green.
- No new Go dependency; import rules (AGENTS.md) unchanged.
- Tests cite requirement IDs in their names (e.g. `_GRD1`).
- Work on a dedicated branch; never commit on `main`.
- Behavior/decision changes go through change control (AGENTS.md §2): update `PROJECT_DOCS/03_spec.md` (Task 2) before or with the code that changes behavior.

## Review Focus

Input classes / conditions the spec implies but no task's own tests fully cover; each gets a test in the owning task:

1. `protected_paths` unset/empty → the spec prompt must **omit** the section entirely (no empty heading). Task 1, Step 10.
2. Default applies when unset (`["**/*_test.go"]`) → the spec prompt must describe the effective list, not an empty one. Task 1, Step 7.
3. `bug_fix` jobs: probe files (COD-8) are only known after the probe stage (03), which runs **after** spec (02); the spec prompt must list only configured `protected_paths` and must not claim to know probe files. Task 1, Step 7 (bug_fix variant).
4. A human-edited `spec.md` at `spec_review` that still plans protected edits remains blocked by the coding guardrail (unchanged) — no false sense of safety. Task 2 (documented), verified by existing `TestEngine_Guardrail_Violation_GRD1_GRD4`.
5. `docs`/`refactor` (no spec stage) are unaffected — the spec template change must not alter their prompts. Task 1, Step 6 (golden diff shows only spec goldens change).

---

### Task 1: Pass protected paths into the spec prompt

**Files:**
- Modify: `internal/factory/pipeline.go` (spec `PromptData`, ~line 364; `executeSpecStage`, ~line 310)
- Modify: `internal/factory/prompts/spec.md.tmpl`
- Test: `internal/factory/prompt_test.go` (`TestRenderPrompt_GoldenFiles` scenario table)
- Test: `internal/factory/pipeline_repair_test.go` (new engine test)
- Test data: `internal/factory/testdata/prompts/spec_protected_paths.golden`

**Interfaces:**
- Consumes: `ProjectConfig.Guardrails.ProtectedPaths []string` (`internal/factory/types.go:294`); `PromptData.ProtectedPaths []string` (`internal/factory/prompt.go:57`); `RoleSpec`; `RenderPrompt(role string, d PromptData) (string, error)`.
- Produces: a rendered spec prompt containing the effective protected-path list; no new exported symbol.

- [ ] **Step 1: Add a failing golden scenario** to `TestRenderPrompt_GoldenFiles` in `internal/factory/prompt_test.go`:

```go
{
    name: "spec_protected_paths",
    role: RoleSpec,
    data: PromptData{
        JobID:          104,
        WorkType:       WorkTypeFeature,
        Intent:         "Add a Subtract function with tests.",
        ArtifactDir:    ".garagefab/jobs/104",
        ProtectedPaths: []string{"**/*_test.go", "go.mod"},
    },
},
```

- [ ] **Step 2: Run the test and verify it fails**

Run: `go test ./internal/factory/ -run TestRenderPrompt_GoldenFiles -count=1`
Expected: FAIL — `open internal/factory/testdata/prompts/spec_protected_paths.golden: no such file`.

- [ ] **Step 3: Add the conditional section to `internal/factory/prompts/spec.md.tmpl`**, immediately before `## Constraints` (line 53), so the fixed section order stays `Role & Rules → Context → Required Output → Protected Paths → Constraints`:

```
{{if .ProtectedPaths}}
### Protected Paths
The coding step must NOT modify existing files matching these patterns:
{{range .ProtectedPaths}}- {{.}}
{{end}}
Your Implementation Plan MUST NOT require editing an existing file that matches any pattern above. To add tests for such a file, plan a NEW file instead, or leave the test addition to the coding step.
{{end}}
```

- [ ] **Step 4: Create the golden file**

Run: `go test ./internal/factory/ -run TestRenderPrompt_GoldenFiles -update`
Then inspect `internal/factory/testdata/prompts/spec_protected_paths.golden`: it must contain the `### Protected Paths` heading and both patterns. Confirm `git diff` shows **only** `spec_*.golden` unchanged except the new file (the block is guarded by `{{if .ProtectedPaths}}`, so `spec_first_run`/`spec_clarification`/`spec_bugfix` must not change).

- [ ] **Step 5: Run the golden test and verify it passes**

Run: `go test ./internal/factory/ -run TestRenderPrompt_GoldenFiles -count=1`
Expected: PASS.

- [ ] **Step 6: Run all prompt tests to confirm no regression**

Run: `go test ./internal/factory/ -run TestRenderPrompt -count=1`
Expected: PASS; coding/review/probe goldens byte-identical.

- [ ] **Step 7: Add an engine test asserting the spec stage passes protected paths.** In `internal/factory/pipeline_repair_test.go`, add `TestSpecPrompt_IncludesProtectedPaths_GRD1` that mirrors the setup of the existing spec-stage tests (feature job, `MockProjectConfigProvider` with `Guardrails.ProtectedPaths: []string{"**/*_test.go"}`, `ScriptableAgentRunner`), runs `ExecuteJob`, and asserts the first recorded agent invocation has `Role == factory.RoleSpec` and `strings.Contains(inv.Prompt, "**/*_test.go")`. Add a second sub-case without `ProtectedPaths` asserting `!strings.Contains(inv.Prompt, "### Protected Paths")`.

- [ ] **Step 8: Implement the pipeline change.** In `executeSpecStage` (`internal/factory/pipeline.go`), read the patterns once before the loop:

```go
var protectedPaths []string
if projCfg != nil {
    protectedPaths = projCfg.Guardrails.ProtectedPaths
}
```

and add the field to the spec `PromptData` (line ~364):

```go
promptData := PromptData{
    JobID:          job.ID,
    WorkType:       job.WorkType,
    Intent:         job.Intent,
    ArtifactDir:    fmt.Sprintf(".garagefab/jobs/%d", job.ID),
    Clarification:  clarificationContent,
    RepairFeedback: repairFeedback,
    ProtectedPaths: protectedPaths,
}
```

- [ ] **Step 9: Run the factory package tests**

Run: `go test ./internal/factory/... -count=1`
Expected: PASS (both new tests included).

- [ ] **Step 10: Verify omission for unset paths (Review Focus 1).** Confirm the Step 7 second sub-case passes: no `### Protected Paths` header when `ProtectedPaths` is empty/nil.

- [ ] **Step 11: Full verification**

Run: `CGO_ENABLED=0 go build ./... && go vet ./... && go test ./... -count=1 && golangci-lint run ./...`
Expected: build clean, vet clean, all packages `ok`, `0 issues`.

- [ ] **Step 12: Commit**

```bash
git add internal/factory/pipeline.go internal/factory/prompts/spec.md.tmpl \
        internal/factory/prompt_test.go internal/factory/pipeline_repair_test.go \
        internal/factory/testdata/prompts/spec_protected_paths.golden
git commit -m "fix(grdf): spec agent aware of protected_paths (GRD-1, SPC-2)"
```

---

### Task 2: Document the `protected_paths` policy

**Files:**
- Modify: `PROJECT_DOCS/03_spec.md` (`§6.5` config reference / guardrails semantics)
- Modify: `README.md` (project.yaml reference section)
- Modify: `PROJECT_DOCS/milestones/M7.md` if it restates guardrail semantics (add only, do not rewrite history)

**Interfaces:**
- Consumes: nothing.
- Produces: documented behavior (no code).

- [ ] **Step 1: Add the semantics to `PROJECT_DOCS/03_spec.md` `§6.5`.** State, verbatim:
  - `guardrails.protected_paths` (default `["**/*_test.go"]`): an **existing** file matching a pattern may not be modified, deleted, or renamed by an agent step (`GRD-1`); **newly added** files are allowed.
  - The effective list (default or configured) is passed to both the spec and coding prompts; specs must not plan edits to protected files.
  - To permit editing an existing test file, narrow `protected_paths` for that project (see Task 3).

- [ ] **Step 2: Add the same note to the `README.md` project.yaml reference** (short, one paragraph + the default value).

- [ ] **Step 3: Commit** (`docs: document protected_paths semantics (GRD-1, SPC-2)`).

---

### Task 3 (decision-gated): legitimate edits to existing protected files

Pick **one** before implementing; A is recommended for this change.

- **Option A (recommended, YAGNI):** Do nothing in code. A project whose tests legitimately evolve narrows `guardrails.protected_paths` (already supported). Task 2 documents this. No new task.
- **Option B (small follow-up):** Add `guardrails.allowed_paths []string`; `CheckProtectedPaths` exempts paths matching it even when they also match `protected_paths`. Requires: config field + validation (`internal/config/project.go`), `factory.ProjectGuardrails.AllowedPaths` + adapter mapping, a comparison in `internal/worker/command/guardrails.go`, and tests `TestGuardrail_AllowedPaths_GRD1`. Deliver as its own issue/branch.
- **Option C (larger):** A review-gate human override permitting specific protected edits. Requires new job state and a new approval path; out of scope for Phase 1 unless the user asks.

**Recommendation:** ship Task 1 + Task 2 now; open a separate small issue for Option B only if a real project needs it.

---

## Self-Review

**Spec coverage:** `SPC-1`/`SPC-2` (spec prompt content) → Task 1; `GRD-1` (protected semantics preserved) → Task 1 Step 6 + Task 2; `GRD-4` (failure recorded) already fixed in `fix-guardrail-steprun-recording`; default-policy and override questions → Task 2 + Task 3.

**Step scan:** every step has one action and a checkable result; no step decides nothing; no transcript bodies (only the one template block whose exact copy matters and the two-line `PromptData` edit).

**Type consistency:** `ProtectedPaths []string` is the existing field on both `ProjectConfig.Guardrails` and `PromptData`; `RoleSpec` is the existing constant used by `RenderPrompt`. No new names introduced.

**Review Focus:** the five conditions above each map to a named step/test (Steps 6, 7, 10, and existing `TestEngine_Guardrail_Violation_GRD1_GRD4`).

**Proportion:** plan is shorter than the touched surface; code blocks are the fixed template copy and two short edits.
