# Garagefab

> A lightweight, local-first software factory for solo developers. You plan the work, coding agents build it.

Garagefab orchestrates coding agents through a structured SDLC pipeline. It runs as a single Go binary with an embedded SQLite database and an embedded React dashboard.

Website: [https://garagefab.dev](https://garagefab.dev)

## Prerequisites

- **Go:** 1.22+
- **Node.js:** 20+
- **Git:** 2.30+
- **golangci-lint:** (recommended for linting)

## Building

```sh
# Build UI, then build Go binary to bin/garagefab (CGO_ENABLED=0)
make build

# Run tests
make test

# Run vet
make vet

# Run lint
make lint

# Full CI check (lint + vet + test + build)
make ci
```

## Running Locally

```sh
# Start with defaults (~/.garagefab, port 7878)
./bin/garagefab start

# Start without opening the browser
./bin/garagefab start --no-open

# Check version
./bin/garagefab version

# Install garagefab-work skill for agy & opencode
./bin/garagefab install-skills
```

## Configuration

Per-project behavior is configured in `<repo>/.garagefab/project.yaml` (full reference:
[`03_spec.md` §6.5](PROJECT_DOCS/03_spec.md#65-configuration-reference)). Guardrails protect
existing files from agent edits: `guardrails.protected_paths` (default `["**/*_test.go"]`) prevents
an agent step from modifying, deleting, or renaming an **existing** file matching a pattern;
**newly added** files are allowed. The effective list (default or configured) is passed to both the
spec and coding prompts, so the spec prompt instructs the agent not to plan edits to a protected
file. To permit editing an existing protected file, narrow `guardrails.protected_paths` for that
project.

## Documentation

Full architectural and design specifications are located in [`PROJECT_DOCS/`](PROJECT_DOCS/):

- [`01_intent.md`](PROJECT_DOCS/01_intent.md) — Vision, principles, pipeline stages
- [`02_architecture.md`](PROJECT_DOCS/02_architecture.md) — Stack, components, boundaries, decisions
- [`03_spec.md`](PROJECT_DOCS/03_spec.md) — Observable behavior and acceptance criteria
- [`04_plan.md`](PROJECT_DOCS/04_plan.md) — Milestones and roadmap
- [`AGENTS.md`](AGENTS.md) — Instructions for AI coding agents

## License

[MIT](LICENSE)