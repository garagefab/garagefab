# Guided E2E Walkthroughs

> Audience: AI coding agents operating in this repository.
> Goal: turn an end-to-end test into a guided, pausable tour so a human can
> understand how the system actually behaves, step by step.

This guide describes a pattern that has been proven on this repo. Read it before
writing a new E2E test that you intend to run as a guided tour.

---

## 1. When to use this

Use a guided walkthrough when the human wants to **understand or debug behavior in
a real system**, not just get a pass/fail:

- Onboarding onto the pipeline (intake → spec → coding → review → gate → delivery).
- Debugging a flaky or surprising stage.
- Demonstrating a milestone / feature end to end.

Do **not** use it for plain regression coverage. A guided tour is a *mode* of a
normal test, never a replacement for it.

---

## 2. Mental model: one test, two modes

Write **one** test. It must behave identically in both modes:

| Mode | Trigger | Workspace | Speed | Purpose |
|------|---------|-----------|-------|---------|
| Normal (CI) | default | `t.TempDir()` | fast, deterministic | regression / CI |
| Walkthrough | `E2E_WALKTHROUGH=1` | persistent `E2E_WORKSPACE` | human-paced | guided tour |

**Hard rule:** when `E2E_WALKTHROUGH` is unset, every walkthrough helper must be a
**no-op**. The test's asserted behavior and runtime must not change. If your hook
is not inert when disabled, it is wrong.

Shared helpers already exist in
[`cmd/garagefab/walkthrough_test.go`](../../cmd/garagefab/walkthrough_test.go):

- `walkthroughEnabled()` — true only when `E2E_WALKTHROUGH=1`.
- `walkthroughDir()` — persistent dir (`E2E_WORKSPACE`, default `$TMPDIR/garagefab-walkthrough`).
- `walkthroughWorkspace(t)` — returns the persistent dir in tour mode (reset once, not cleaned up) or `t.TempDir()` otherwise.
- `pause(t, step, lines...)` — prints a checkpoint banner and blocks until the sentinel file appears.

Do not copy these into your test; import nothing — same package, just call them.

---

## 3. The pause-hook contract

- **Enable:** `E2E_WALKTHROUGH=1`.
- **Workspace:** `E2E_WORKSPACE=<abs dir>` (otherwise `$TMPDIR/garagefab-walkthrough`).
  Reset once at the start via `walkthroughWorkspace(t)`; **never** auto-deleted.
- **Resume:** create the sentinel file — `touch <workspace>/continue`. `pause`
  removes it and returns.
- **Two env vars, one sentinel.** Nothing else.

Why a poll loop and not a debugger: it works in any environment (agent CLI, CI,
IDE) with no external tooling. The trade-off is that the process is **not frozen**
— see the next section.

---

## 4. Checkpoint design rules

1. **Place checkpoints where the pipeline is idle.** The best spots are human
   gates (`spec_review`, `awaiting_approval`) and terminal states. There the
   scheduler has nothing to do, so the snapshot is stable even though background
   goroutines are still alive.
2. **A `pause` does not freeze the world.** Unlike a debugger breakpoint, other
   goroutines keep running. Do not pause in the middle of an active stage and
   expect the state to hold.
3. **Control the timeline when you need determinism.** If you want to observe
   "job created but not yet processed", start the scheduler *after* that
   checkpoint instead of racing it.
4. **Extend wall-clock deadlines in tour mode.** A loop with
   `deadline := time.Now().Add(30 * time.Second)` will fail if the human lingers
   at a checkpoint inside that loop. Gate it:
   ```go
   window := 30 * time.Second
   if walkthroughEnabled() {
       window = 30 * time.Minute
   }
   deadline := time.Now().Add(window)
   ```
5. **Never assert on asynchronous cleanup immediately.** E.g. worktree removal
   happens *after* the DB commit that marks the job `done`; poll briefly for it.
   This is a real race that bites normal CI runs too, not just tours.

---

## 5. Agent driving protocol

You (the agent) drive the tour; the human inspects. Per checkpoint:

1. **Start the test in the background** with the tour env set, e.g.:
   ```sh
   rm -rf /tmp/garagefab-walkthrough
   E2E_WALKTHROUGH=1 E2E_WORKSPACE=/tmp/garagefab-walkthrough \
     go test ./cmd/garagefab -run TestYourE2E -v -count=1 -timeout 60m
   ```
   Run it as a background task; a foreground call would block.
2. **Wait for the checkpoint banner** in the task output (e.g. `shell_output`
   with `wait: "output"`). The test is now parked.
3. **Relay to the human**, in this order:
   - *What happened* (which stage transitioned, which artifact appeared).
   - *Where to look* — absolute paths + copy-pasteable `sqlite3` / `git` / `cat`
     commands. Run the read-only ones yourself and show the output; it makes the
     explanation concrete.
   - *What is next* (the next checkpoint and how to reach it).
4. **Wait for the human to say "continue"** (or equivalent). Do not resume
   unprompted — the whole point is to let them inspect.
5. **Resume** by creating the sentinel: `touch <workspace>/continue`. Repeat.
6. **After the last checkpoint**, let the test finish, report the result, and ask
   before deleting the workspace.

Do not narrate the fixed "correct" behavior from memory. Read the actual state
(DB rows, files, git log) and describe what is really there.

---

## 6. Narration template

Keep every checkpoint message to three parts — this is the format users liked:

```
CHECKPOINT <letter> - <what just finished>
What happened: <one or two concrete sentences>
Look:          <absolute path / exact command>
Next:          <what the resume triggers>
```

Put the machine-readable version in the `pause(...)` call (banner lines), and the
richer prose in your chat reply.

---

## 7. Blueprint: adding a new guided E2E test

1. **Name it for the requirement:** `TestE2E_<flow>_<REQ-IDs>`
   (e.g. `TestE2E_IntentFile_To_Done_INT2_APR7_DLV4`).
2. **Make it hermetic** (section 8): temp dirs only, stub external CLIs on `PATH`,
   a local bare Git remote instead of a real one.
3. **Workspace:** `tempDir := walkthroughWorkspace(t)`; derive `dbPath` etc. from it.
4. **Wire the real system in-process** (see the canonical example): real `store`,
   `factory.Engine`, `factory.Scheduler`, `server` via `httptest`, real
   `intake.Poller`.
5. **Insert `pause(...)`** at the idle/gate checkpoints, with concrete look-at
   lines. Use `walkthroughEnabled()` to widen any wall-clock deadline that spans a
   checkpoint.
6. **Keep the normal path untouched:** no behavior change when `E2E_WALKTHROUGH`
   is unset. Verify with:
   ```sh
   go test ./cmd/garagefab -run TestYourE2E -count=1     # must stay fast + pass
   ```
7. **Then run the tour** with the env vars from section 5.

---

## 8. Hermetic E2E ingredients (required)

A guided tour must not depend on network, credentials, or a real GitHub. Use:

- **Stub CLIs on `PATH`.** Write small `#!/bin/sh` stubs (e.g. `gh`, and agent
  CLIs like `agy`/`opencode`) into a temp dir and prepend it:
  ```go
  t.Setenv("PATH", stubBinDir + string(filepath.ListSeparator) + os.Getenv("PATH"))
  ```
  See `createStubAgentBinaries` in `e2e_m5_test.go` and `createStubGHBinary` in
  `e2e_intent_demo_test.go`.
- **A local bare `origin`.** `git init --bare <dir>`, `git remote add origin <dir>`,
  `git push -u origin main`. A bare repo is the correct push target (a non-bare
  repo is not). See `setupIntentDemoRepo`.
- **Fake agent runner** for the pipeline: `agent.NewFakeRunner()` (routes every
  role). It writes the artifacts each stage expects.
- **A real GitHub repo is not needed.** `github.repo` in `project.yaml` is just an
  `owner/repo` identifier passed to `gh`; with the stub it never touches network.

---

## 9. Anti-patterns

- ❌ Pausing inside a wall-clock deadline loop without extending the deadline.
- ❌ Asserting a worktree/artifact is gone immediately after `done` (async cleanup).
- ❌ Depending on real `gh`, real agents, or any network during a tour.
- ❌ Using `t.TempDir()` as the tour workspace (it is deleted at test end).
- ❌ Making the hook change asserted behavior — walkthrough mode must only add
  pauses and messaging.
- ❌ Narrating from assumptions instead of reading the live DB/files.

---

## 10. Cleanup

Tour mode intentionally leaves `<workspace>` behind. When done, ask the human, then
remove it:

```sh
rm -rf /tmp/garagefab-walkthrough
```

Normal (CI) runs need no cleanup — `t.TempDir()` removes itself.

---

## 11. Canonical examples

- **Go E2E + tour:** [`cmd/garagefab/e2e_intent_demo_test.go`](../../cmd/garagefab/e2e_intent_demo_test.go)
  (intent file → poller → both human gates → delivery → done).
- **Shared hook:** [`cmd/garagefab/walkthrough_test.go`](../../cmd/garagefab/walkthrough_test.go).
- **Shell equivalent (real binary, real processes):** [`scripts/e2e-intent-demo.sh`](../../scripts/e2e-intent-demo.sh).
- **Stub CLI precedent:** `createStubAgentBinaries` in
  [`cmd/garagefab/e2e_m5_test.go`](../../cmd/garagefab/e2e_m5_test.go).
