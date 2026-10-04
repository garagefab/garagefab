# Contributing to Garagefab

Thanks for your interest. This project is a local-first AI software factory; contributions are
welcome. Please read the project documents before making changes:

- [`PROJECT_DOCS/01_intent.md`](PROJECT_DOCS/01_intent.md) — vision and principles
- [`PROJECT_DOCS/02_architecture.md`](PROJECT_DOCS/02_architecture.md) — stack, components, boundaries
- [`PROJECT_DOCS/03_spec.md`](PROJECT_DOCS/03_spec.md) — observable behavior and acceptance criteria
- [`PROJECT_DOCS/04_plan.md`](PROJECT_DOCS/04_plan.md) — milestones and working agreement
- [`AGENTS.md`](AGENTS.md) — instructions for AI coding agents

All code, comments, documentation, commit messages, and generated artifacts are in **English**.

## Prerequisites

Go 1.23+, Node 20+, git 2.30+, and `golangci-lint`. `gh` 2.x (authenticated) is needed only for
projects that set `github.repo`. The binary must build with `CGO_ENABLED=0`.

## Build and test

```sh
make build      # Build the UI, then the Go binary to bin/garagefab (CGO_ENABLED=0)
make test       # Run Go tests
make lint       # Run golangci-lint
make vet        # Run go vet
make ui         # Build the React UI only
make ci         # build + lint + vet + test (the full CI pipeline)
```

Run `make ci` before opening a pull request; it mirrors the CI workflow.

## Branch and commit discipline

- **Never work directly on `main`.** Create a dedicated branch: milestone work uses
  `m<N>-<short-description>`, other work uses a descriptive name.
- **Requirement traceability:** reference requirement IDs from
  [`PROJECT_DOCS/03_spec.md`](PROJECT_DOCS/03_spec.md) (for example `PIP-2`, `NFR-5`) in tests and
  commits.
- **Commit messages:** a concise summary only — **maximum 5 lines** total. Do not add co-author or
  similar trailer lines.
- **Pull requests:** reference the issue and the requirement IDs. Keep the PR description a concise
  summary — **maximum 5 lines** total, without trailer lines.

Working in the codebase: follow the package dependency rules in
[`PROJECT_DOCS/02_architecture.md`](PROJECT_DOCS/02_architecture.md) §6 (enforced by CI), keep the
educational commenting standard described in [`AGENTS.md`](AGENTS.md), and prefer table-driven tests
named after the requirement they cover.

## Change control

If implementation shows that `intent.md`, `architecture.md`, or `spec.md` is wrong or ambiguous,
**stop and change the document first** (in its own PR), then continue. Technical decisions go to
`architecture.md` §3; behavioral decisions go to `spec.md` §11.

Do not fix pre-existing issues or add features outside the current task's scope — record them as
warnings or separate issues instead.

## License

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE).
