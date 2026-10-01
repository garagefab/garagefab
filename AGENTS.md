# AGENTS.md

> Instructions for AI coding agents working on the Garagefab codebase.

## Project Overview

Garagefab is a local-first AI software factory that orchestrates coding agents through a structured SDLC pipeline. It is a single Go binary with an embedded SQLite database and an embedded React web dashboard.

**Key documents** (read before making changes):

| Document | Location | Purpose |
|----------|----------|---------|
| Intent | `PROJECT_DOCS/01_intent.md` | Vision, principles, pipeline stages |
| Architecture | `PROJECT_DOCS/02_architecture.md` | Stack, components, boundaries, decisions |
| Specification | `PROJECT_DOCS/03_spec.md` | Observable behavior and acceptance criteria |
| Plan | `PROJECT_DOCS/04_plan.md` | Milestones, task order, working agreement |
| Milestone plans | `PROJECT_DOCS/milestones/*.md` | Per-milestone implementation details |

## Build Commands

```sh
make build      # Build UI, then build Go binary to bin/garagefab (CGO_ENABLED=0)
make test       # Run Go tests
make lint       # Run golangci-lint
make vet        # Run go vet
make ui         # Build the React UI (cd ui && npm ci && npm run build)
make ci         # lint + vet + test + build (full CI pipeline)
```

Prerequisites: Go 1.22+, Node 20+, git 2.30+, golangci-lint.

## Code Conventions

- **Language:** All code, comments, documentation, commit messages, and generated artifacts are in **English**.
- **Go style:** Standard Go conventions. Exported identifiers must have doc comments. Use `goimports` for formatting.
- **Error handling:** Return errors with context (`fmt.Errorf("config: load: %w", err)`). Never swallow errors silently.
- **Logging:** Use `log/slog` with structured fields. Include `job_id` and `step_id` where applicable.
- **Testing:** Table-driven tests preferred. Test names should reference requirement IDs where applicable (e.g., `TestLockFile_SecondInstance_RCV5`).
- **No CGO:** The binary must build with `CGO_ENABLED=0`. Never add a dependency that requires CGO.
- **Comments:** Preserve all existing comments and docstrings unrelated to your changes.

## Package Layout and Dependency Rules

```
cmd/garagefab/            main.go — wiring only
internal/
  factory/                engine, scheduler, pipeline, repair_loop, failure, evidence
  worker/
    agent/                Agent interface + adapters (agy, opencode)
    command/              command runner + built-in guardrails
    worktree/             git worktree lifecycle
  store/                  db, migrations/, repositories
  intake/                 poller: GitHub issues + intent-file scan → jobs
  provider/github/        issues, Projects v2 mirror, PR creation
  server/                 router, auth, API handlers, SSE hub, embed
  config/                 global + project config loading and validation
ui/                       React SPA source (Vite + TypeScript + Tailwind + shadcn/ui)
```

**Import rules (enforced by CI):**

1. `factory` defines interfaces it needs. It **must not** import `store`, `worker`, `provider`, or `server`. It never runs a binary and never contains SQL.
2. `store` owns all SQL, transactions, and migrations. **No other package** imports `database/sql`.
3. `worker` owns process execution and git worktrees. It returns structured results and **must not** import `store`.
4. `server` and `intake` are adapters: they call `factory` and read through `store`, but contain no pipeline logic.
5. `cmd/garagefab` is the only place that knows concrete types and wires everything together.

## Adding Dependencies

Every new Go dependency requires an entry in `PROJECT_DOCS/02_architecture.md` §3 (Decision Log). `CGO_ENABLED=0 go build ./...` must continue to pass.

## Working Agreement

- **Requirement traceability:** Tests and commits reference requirement IDs from `PROJECT_DOCS/03_spec.md` (e.g., `CLI-7`, `RCV-5`).
- **Change control:** If implementation reveals that `intent.md`, `architecture.md`, or `spec.md` is wrong or ambiguous, **stop and change the document first** (in its own PR), then continue.
- **Scope discipline:** Do not fix pre-existing issues or add features outside the current task's scope. Record out-of-scope observations as warnings or separate issues.

## UI Development

The web UI is a React SPA in `ui/`. It is built to `ui/dist/` and embedded into the Go binary.

```sh
cd ui
npm ci
npm run dev     # Dev server with hot reload (proxies /api to Go backend)
npm run build   # Production build to dist/
```

## Running Locally

```sh
make build
./bin/garagefab start              # Start with defaults (~/.garagefab, port 7878)
./bin/garagefab start --no-open    # Start without opening the browser
./bin/garagefab version            # Print version info
```
