# AGENTS.md

Index for agents and teammates working on **FlowSeer**. Detailed conventions live
inside the docs linked below; the repository boundaries in this file are intentionally
short because every task must load them.

## Conventions

Binding on humans and agents equally; each doc states its own scope.

- [`docs/code-style.md`](docs/code-style.md) — Go style: doc-comment contract,
  comment discipline, naming, spacing, errors, concurrency, testing, toolchain, and
  the rules coding agents must follow. Its comment rules and agent rules apply to
  every language in the repo.
- [`docs/code-style-proto.md`](docs/code-style-proto.md) — protobuf schemas under
  `spec/proto/` (buf modules, edition 2024, presence, symbol visibility, naming,
  evolution, protovalidate, and what editions change in generated Go).
- [`docs/conventions/protobuf.md`](docs/conventions/protobuf.md) — how FlowSeer-owned
  messages are *shaped*: the Config/State/Event triad, the LocalRef/GlobalRef pair,
  ambient tenancy, provenance on the envelope, enum placement, typed variants. The
  model layer above the proto style guide.
- [`docs/code-style-web.md`](docs/code-style-web.md) — the TypeScript web frontend
  (`frontend/web/`; own toolchain, the Go rules do not govern it).

## Hard boundaries

- `spec/proto/` is production Buf input. It contains only `.proto` files and
  package-boundary `README.md` files. Put executable schema tests in
  `src/common/protoconformance/` and their fixtures in that package's `testdata/`.
- Never add a Buf exclusion, ignore, skip, lint suppression, or hook exception to
  make a task's own artifacts pass. If a correct change requires relaxing a
  repository guardrail, stop and request that policy change explicitly; do not bundle
  the relaxation with the feature that depends on it.
- Treat `AGENTS.md`, `buf.yaml`, `.agent/hooks/`, `.claude/settings.json`,
  `.codex/hooks.json`, `src/common/protoconformance/`, and merge-gate configuration
  as policy surfaces. Changes to them require an explicit guardrail review.

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

Shared implementations live in `.agent/hooks/`; thin registrations in
`.claude/settings.json` and `.codex/hooks.json` adapt them to each agent. Codex users
must review changed project hooks with `/hooks` before Codex will run them.

- `pre-tool-policy.sh` (`PreToolUse`) — denies hand-edits to generated output and
  `buf.lock`, and rejects non-source artifacts under `spec/proto/`.
- `go-format.sh` (`PostToolUse`) — runs gofumpt + goimports on every edited `.go`
  file so nothing lands lint-dirty; reports back when it rewrote the file.
- `proto-check.sh` (`PostToolUse`) — `buf format -w`, then `buf lint` on the edited
  path (failures block and come back as feedback), then the message-sync check:
  reports Config/State/Event triad members and GlobalRef/LocalRef counterparts the
  edit did not bring along.
- `stop-check.sh` (`Stop`) — runs the repository-layout package before an agent stops.

Hooks are fast feedback, not the authority: `go test -race ./...` runs the same
source-tree invariant through `src/common/protoconformance/` even when an edit path
bypasses hooks.

## Layout

- `src/backend/`, `src/common/`, `src/edge/` — Go (`go.aledante.io/FlowSeer`).
- `spec/proto/` + `spec/mib/` — schema sources of truth; `generated/` is
  `buf generate` output and is never edited by hand.
- `docs/architecture/` — accepted direction records (architecture-level decisions
  that later brainstorms and plans build toward); read first when a task touches
  the device service, inventory, discovery, or ingestion planes.
- `docs/solutions/` — captured learnings from past work (bugs, conventions,
  architecture patterns), by category, with YAML frontmatter (`module`, `tags`,
  `problem_type`); relevant when implementing or debugging in a documented area.
- `CONCEPTS.md` — shared domain vocabulary (entities, named processes, status
  concepts); relevant when orienting or discussing domain concepts.
- `.golangci.yml` — lint & format gate (`golangci-lint run`; gofumpt + goimports).
