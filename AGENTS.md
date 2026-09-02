# AGENTS.md

## Conventions

Binding on humans and agents equally; each doc states its own scope.

- [`docs/code-style.md`](docs/code-style.md) — Go style; its comment and agent
  rules apply to every language in the repo.
- [`docs/code-style-proto.md`](docs/code-style-proto.md) — protobuf schema style
  under `spec/proto/` (buf modules, edition 2024, evolution, protovalidate).
- [`docs/conventions/protobuf.md`](docs/conventions/protobuf.md) — message shapes:
  Config/State/Event triad, LocalRef/GlobalRef pair, ambient tenancy, provenance,
  enum placement, typed variants.
- [`docs/code-style-web.md`](docs/code-style-web.md) — TypeScript web frontend
  (`frontend/web/`; own toolchain, Go rules do not apply).
- [`docs/doc-style.md`](docs/doc-style.md) — all prose: docs, READMEs, schema
  comments, commit and PR text. The load-bearing rules: explain why, show a
  working example, document the hard parts, no marketing register, update docs
  in the change that invalidates them, and write like a person — none of the
  machine-writing tells that doc catalogues (em-dash chains, rule-of-three
  lists, trailing participles, puffery, comment-per-line).
- [`docs/agent-knowledge.md`](docs/agent-knowledge.md) — where shared rules and
  learnings live; repository guidance wins over private memory.

## Hard boundaries

- `spec/proto/` contains only `.proto` files and package-boundary `README.md`
  files. Enforce schema rules through `buf lint`, not executable tests.
- Never add an exclusion, ignore, suppression, or hook exception to make your own
  artifacts pass; request the policy change explicitly and separately.
- `AGENTS.md`, `buf.yaml`, `.agent/hooks/`, `.claude/settings.json`,
  `.codex/hooks.json`, and merge-gate configuration are policy surfaces; changes
  require explicit guardrail review.

## Isolation

On a protected branch in the primary checkout, enter a session worktree (short
task-shaped name) before the first edit — a hook denies mutations until you do;
read-only exploration is fine. Worktrees live in `.claude/worktrees/` (gitignored)
and branch off local `HEAD` — the repo has no remote.

## Agent behavior

- Prefer the runtime's native tools and subagents. Use `repo-researcher` for a
  bounded read-only repository question, `independent-reviewer` for a fresh pass
  over specified changed files; give each a precise question, paths, and expected
  output, and keep small sequential work in the main conversation.
- Auto-memory is personal and fallible; promote durable team facts per
  `docs/agent-knowledge.md`.

## Investigation discipline

- State the leading hypothesis, alternatives, and a discriminating test before
  claiming a root cause; cite the evidence before editing.
- Before a live-device write or a mutation over 10,000 records, state the blast
  radius and wait for explicit approval.

## Enforced rules

Hooks in `.agent/hooks/` (registered per runtime in `.claude/settings.json` and
`.codex/hooks.json`) deny hand-edits to `generated/` and `buf.lock`, reject
non-source files under `spec/proto/`, auto-run gofumpt/goimports and
`buf format`/`buf lint` on edits, check triad and ref message sync, and block
completion until the `verify-change` skill passes for the affected scope. Hooks
are fast feedback, not the authority — `go test -race ./...` enforces the same
invariants.

## Layout

- `src/backend/`, `src/common/`, `src/edge/` — Go (`go.aledante.io/FlowSeer`).
- `spec/proto/` + `spec/mib/` — schema sources of truth; `generated/` is
  `buf generate` output, never edited by hand.
- `docs/architecture/` — accepted direction records; read first for work on the
  device service, inventory, discovery, or ingestion planes.
- `docs/solutions/` — captured learnings (bugs, conventions, patterns) with YAML
  frontmatter; check when working in a documented area.
- `CONCEPTS.md` — shared domain vocabulary (entities, named processes, status
  concepts); relevant when orienting to the codebase or discussing domain terms.
- `.golangci.yml` — lint & format gate (`golangci-lint run`; gofumpt + goimports).
- `.serena/project.yml` — shared Serena configuration for Go-aware symbol lookup,
  reference discovery, and diagnostics. Put machine-local
  overrides in the ignored `.serena/project.local.yml` file.
