# Garagefab — Release Checklist

This is the manual, clean-machine verification required before tagging a release. It exercises the
two MVP scenarios from `PROJECT_DOCS/01_intent.md` with a **real** agent on a clean macOS and Linux
machine. CI cannot run real agents, so this checklist is the release's end-to-end evidence.

Complete one section per machine and paste the observed results into the results table. Keep the
filled-in copy attached to the release's closing issue.

## Prerequisites (per machine)

- A clean machine with no prior Garagefab data (`~/.garagefab` absent).
- `git` 2.30+ on `PATH`.
- The agent CLI(s) to be tested (`agy`, `opencode`) installed and working.
- For Scenario 1 delivery: `gh` 2.x, authenticated (`gh auth status`), and a writable test
  repository whose owner can create issues and pull requests.
- Network access for `git fetch` and `gh`.

## Steps (repeat on each machine)

1. **Download and verify.** Download the release archive for the machine's OS/arch from the GitHub
   Release. Verify it against `checksums.txt`:
   ```sh
   sha256sum -c checksums.txt   # or: shasum -a 256 -c checksums.txt
   tar -xzf garagefab_<version>_<os>_<arch>.tar.gz
   ./garagefab version
   ```
   Expected: the checksum matches, and the version equals the release tag.

2. **Start the service.**
   ```sh
   ./garagefab start --no-open
   ```
   Expected: it prints `Garagefab server listening on 127.0.0.1:7878` and a dashboard login URL;
   `~/.garagefab/config.yaml` is created (`0600`). Open the login URL in a browser.

3. **Register a test project.** Use a Git repository you can push to. Add
   `<repo>/.garagefab/project.yaml` (see `PROJECT_DOCS/03_spec.md` §6.5) with at least:
   ```yaml
   agents:
     coding: <agy|opencode>
     review: <agy|opencode>
   commands:
     test: ["<your test command>"]
   github:
     repo: <owner>/<name>
   ```
   Register it from the dashboard (Add Project). Expected: the project appears with its path and
   enabled work types; an invalid path or config is rejected with a message naming the failed key.

4. **Scenario 1 — happy path (feature).** Create an issue in the test repository labeled
   `garagefab` and `type:feature` with a clear intent. Wait for the next poll (≤ 30 s + 1 s).
   Expected, in order:
   - a job appears and reaches `02`/`spec_review`;
   - **Approve spec** → coding runs, configured commands pass, `review.json` is written;
   - the job reaches `06`/`awaiting_approval` with the evidence summary;
   - **Approve** → a pull request exists (title = job title, body contains `Closes #<n>`), the
     worktree is removed, the job is `07`/`done`, and the issue shows `garagefab:delivered`.

5. **Scenario 2 — clarification.** Create an ambiguous issue labeled `garagefab` and
   `type:feature`. Expected:
   - the job becomes `02`/`needs_clarification` and the questions are shown;
   - copy the handoff command, run it in a terminal; the skill posts the answers (bearer token);
   - the job returns to `02`/`queued`, re-runs the spec agent, and produces a valid draft;
   - the rest proceeds as Scenario 1.

6. **Browser smoke test.** Open the dashboard in the latest two versions of Chrome, Firefox, and
   Safari. Expected: the board, task detail, and live updates render and operate by keyboard.

## Results

Record the outcome per machine. `PASS`/`FAIL` plus any notes (errors, timings, versions).

| Item | macOS (arm64) | Linux (amd64) |
|------|---------------|---------------|
| Release tag / version | | |
| Checksum verified | | |
| `garagefab version` matches tag | | |
| Service starts; dashboard reachable | | |
| Project registered | | |
| Scenario 1 → PR + `done` + `garagefab:delivered` | | |
| Scenario 2 → clarification round-trip → spec | | |
| Browser smoke (Chrome, Firefox, Safari) | | |

**Agent versions tested:** `<agy version>`, `<opencode version>`

**Release sign-off:** date, machine, and who ran it.
