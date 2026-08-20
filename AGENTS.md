# AGENTS.md

Index for agents and teammates working on **FlowSeer**. This file is a thin index
only: the load-bearing rules live inside the docs it links, not here.

## Conventions

Binding on humans and agents equally; each doc states its own scope.

- [`docs/code-style.md`](docs/code-style.md) — Go style: doc-comment contract,
  comment discipline, naming, spacing, errors, concurrency, testing, toolchain, and
  the rules coding agents must follow. Its comment rules and agent rules apply to
  every language in the repo.
- [`docs/code-style-proto.md`](docs/code-style-proto.md) — protobuf schemas under
  `spec/proto/` (buf modules, edition 2024, presence, symbol visibility, naming,
  evolution, protovalidate, and what editions change in generated Go).
- [`docs/code-style-web.md`](docs/code-style-web.md) — the TypeScript web frontend
  (`frontend/web/`; own toolchain, the Go rules do not govern it).

## Layout

- `src/backend/`, `src/common/`, `src/edge/` — Go (`go.aledante.io/FlowSeer`).
- `spec/proto/` + `spec/mib/` — schema sources of truth; `generated/` is
  `buf generate` output and is never edited by hand.
- `docs/solutions/` — captured learnings from past work (bugs, conventions,
  architecture patterns), by category, with YAML frontmatter (`module`, `tags`,
  `problem_type`); relevant when implementing or debugging in a documented area.
- `CONCEPTS.md` — shared domain vocabulary (entities, named processes, status
  concepts); relevant when orienting or discussing domain concepts.
- `.golangci.yml` — lint & format gate (`golangci-lint run`; gofumpt + goimports).
