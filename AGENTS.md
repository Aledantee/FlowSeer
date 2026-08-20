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

## Isolation

Every session does its work in its own git worktree, so concurrent sessions cannot
clobber each other's edits. Starting on `master` (or any protected branch) in the
primary checkout, call the `EnterWorktree` tool with a short task-shaped name before
the first edit; read-only exploration in the primary checkout is fine. A
`PreToolUse` hook (`~/.claude/hooks/worktree-guard.sh`) denies mutating tools until
you do. Worktrees land in `.claude/worktrees/` and are gitignored; branch off local
`HEAD` (`.claude/settings.json` sets `worktree.baseRef: head`) because this repo has
no remote.

## Enforced rules

Three conventions below are enforced by hooks in `.claude/hooks/`, wired in
`.claude/settings.json`, rather than left to vigilance:

- `protect-generated.sh` (`PreToolUse`) — denies hand-edits to `generated/`,
  `frontend/web/generated/`, and `buf.lock`. Change the source of truth instead.
- `go-format.sh` (`PostToolUse`) — runs gofumpt + goimports on every edited `.go`
  file so nothing lands lint-dirty; reports back when it rewrote the file.
- `proto-check.sh` (`PostToolUse`) — `buf format -w`, then `buf lint` on the edited
  path (failures block and come back as feedback), then the message-sync check:
  reports Config/State/Event triad members and GlobalRef/LocalRef counterparts the
  edit did not bring along.

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
