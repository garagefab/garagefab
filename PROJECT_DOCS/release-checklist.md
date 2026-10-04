# Garagefab — Release Checklist

This is the manual, clean-machine verification required before tagging a release. It exercises the
two MVP scenarios from `PROJECT_DOCS/01_intent.md` with a **real** agent on a clean supported
platform. CI cannot run real agents, so this checklist is the release's end-to-end evidence.
Verifying **one** supported platform (macOS or Linux) per release is sufficient (`NFR-1` relaxed in
M8); repeat on the other platform when available.

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
   grep "garagefab_<version>_<os>_<arch>.tar.gz" checksums.txt | shasum -a 256 -c -   # or: sha256sum -c -
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
| Release tag / version | `v0.1.0` / prints `0.1.0` | not run |
| Checksum verified | PASS | not run |
| `garagefab version` matches tag | PASS (`0.1.0` for tag `v0.1.0`) | not run |
| Service starts; dashboard reachable | PASS (port 7878) | not run |
| Project registered | PASS (`garagefab-release-check`) | not run |
| Scenario 1 → PR + `done` + `garagefab:delivered` | PASS (job 1, PR #2) | not run |
| Scenario 2 → clarification round-trip → spec | PASS (job 2, PR #4) | not run |
| Browser smoke (Chrome, Firefox, Safari) | NOT RUN | not run |

**Agent versions tested:** `agy` 1.2.16 (recorded baseline, `R3`); `opencode` not exercised.

**Release sign-off:** 2026-10-04, macOS 15 (darwin/arm64), fresh `--data-dir`, by the maintainer.

**Notes from the run:**
- Version convention: the binary prints `0.1.0` for tag `v0.1.0`; the leading `v` is stripped in both
  the GoReleaser build and `make build` (aligned in M8 polish).
- The skill needs `GARAGEFAB_HOME` when the daemon is started with a non-default `--data-dir`,
  otherwise it reads the default `~/.garagefab/config.yaml`.
- Finding (out of M8 scope): `garagefab start --port N` overrides the runtime port but does not
  update `config.yaml`, so `gf-api.sh` reads the stale port. Tracked separately.
