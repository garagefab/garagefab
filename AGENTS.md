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
- **Comments:** Follow the Educational & Architecture-Aware Code Comments standard below. Preserve all existing comments and docstrings unrelated to your changes.

### Educational & Architecture-Aware Code Comments

All Go source and test files must maintain a comprehensive, educational commenting standard so that developers without prior Go experience (especially engineers coming from Java/Spring and enterprise backend backgrounds) can easily understand the design, concepts, and implementation:

1. **File Header: Architectural Role & Enterprise/Java Bridge**
   - Every file must begin with a package-level header comment explaining its role in Hexagonal / Clean Architecture (Domain Core, Inbound/Outbound Port, Driving/Driven Adapter, Composition Root).
   - Provide concrete Java / Spring Boot comparisons (e.g., `@RestController`, Spring Data JPA, `TaskExecutor`, `ProcessBuilder`, Flyway/Liquibase, ArchUnit, `@Transactional`).

2. **Go Idiom & Language Concept Bridge**
   - Explain Go-specific mechanisms used in the file for engineers unfamiliar with Go (e.g., pointers `*` vs `&`, structural subtyping / implicit interfaces, `defer`, Goroutines & channels, `select`, `dbtx` interface pattern, `io/fs.FS`, `//go:embed`, linker `-ldflags -X`).
   - Highlight *why* Go uses that pattern (e.g., why explicit `dbtx` is preferred over ThreadLocal transactions).

3. **In-Line Logic & Rationale**
   - Explain the "why" behind non-obvious steps, error handling strategies, goroutine synchronization, and security guardrails inline.
   - Focus on clear operational explanations rather than mechanical restatements of the code.

**Preservation Rule:** AI agents modifying existing files must never remove, shorten, or overwrite existing educational comments. When adding new features or refactoring, new code must be documented following this exact standard.

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
