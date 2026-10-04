# Garagefab

> A lightweight, local-first software factory for solo developers. You plan the work, coding agents build it.

Garagefab orchestrates coding agents through a structured SDLC pipeline. It runs as a single Go binary with an embedded SQLite database and an embedded React dashboard.

Website: [https://garagefab.dev](https://garagefab.dev)

## Installation

### Download a release

Download the archive for your OS and architecture from
[GitHub Releases](https://github.com/garagefab/garagefab/releases), verify it against
`checksums.txt`, and put the `garagefab` binary on your `PATH`:

```sh
# macOS / Linux (replace <os> and <arch>: darwin|linux and amd64|arm64)
curl -LO https://github.com/garagefab/garagefab/releases/download/v0.1.0/garagefab_0.1.0_<os>_<arch>.tar.gz
curl -LO https://github.com/garagefab/garagefab/releases/download/v0.1.0/checksums.txt
grep "garagefab_0.1.0_<os>_<arch>.tar.gz" checksums.txt | shasum -a 256 -c -   # macOS
grep "garagefab_0.1.0_<os>_<arch>.tar.gz" checksums.txt | sha256sum -c -       # Linux
tar -xzf garagefab_0.1.0_<os>_<arch>.tar.gz
sudo mv garagefab /usr/local/bin/     # or ~/.local/bin/, ensuring it is on PATH
garagefab version
```

### Build from source

See [Building](#building) below.

## Prerequisites

**Runtime** — the only things needed to *run* Garagefab:

- **Git:** 2.30+
- The coding agent CLI(s) you configure for your projects (`agy` or
  `opencode`).
- **GitHub CLI (`gh`):** 2.x, authenticated — only for projects that set `github.repo`.

**Building from source** additionally needs:

- **Go:** 1.23+ (see [`go.mod`](go.mod))
- **Node.js:** 20+
- **golangci-lint:** recommended for linting

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

## Running

```sh
# Start with defaults (~/.garagefab, port 7878)
./bin/garagefab start

# Start without opening the browser
./bin/garagefab start --no-open

# Check version
./bin/garagefab version

# Install the garagefab-work skill for agy & opencode
./bin/garagefab install-skills

# Print and open a fresh dashboard login URL for a running service
./bin/garagefab open
```

A `--port N` override is **persisted** to `~/.garagefab/config.yaml`, so `garagefab open` and the
`garagefab-work` skill use the port the server actually binds. To switch back, run
`garagefab start --port 7878`.

### Keeping it running

Garagefab runs in the **foreground**. Choose one of the following to keep it alive.

**tmux** — simplest, no service manager:

```sh
tmux new-session -d -s garagefab 'garagefab start --no-open'
tmux attach -t garagefab          # to view, Ctrl-b d to detach
```

**launchd (macOS)** — example files in [`examples/launchd/`](examples/launchd/):

```sh
# User-level agent (starts at login, no root):
cp examples/launchd/dev.garagefab.garagefab.plist ~/Library/LaunchAgents/
launchctl load -w ~/Library/LaunchAgents/dev.garagefab.garagefab.plist

# System-level daemon (headless; edit <UserName> first, needs admin):
sudo cp examples/launchd/dev.garagefab.garagefab.daemon.plist /Library/LaunchDaemons/
sudo launchctl load -w /Library/LaunchDaemons/dev.garagefab.garagefab.daemon.plist
```

**systemd (Linux)** — example files in [`examples/systemd/`](examples/systemd/):

```sh
# User-level unit (no root):
mkdir -p ~/.config/systemd/user
cp examples/systemd/garagefab.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now garagefab.service
sudo loginctl enable-linger $USER     # keep running after you log out

# System-level unit (headless; edit User= first, needs root):
sudo cp examples/systemd/garagefab.system.service /etc/systemd/system/garagefab.service
sudo systemctl daemon-reload
sudo systemctl enable --now garagefab
```

Adjust the binary paths in the example files if you did not install to the path they assume.

## Browser support

The dashboard supports the latest two versions of Chrome, Firefox, and Safari.

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
- [`release-checklist.md`](PROJECT_DOCS/release-checklist.md) — Clean-machine release verification
- [`AGENTS.md`](AGENTS.md) — Instructions for AI coding agents

Contributions are welcome — see [`CONTRIBUTING.md`](CONTRIBUTING.md).

## License

[MIT](LICENSE)
