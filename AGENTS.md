# AGENTS.md

## Working language

Conversation language does not change the working language. Write every plan,
document, code change, code comment, and piece of documentation in English. The
only exception is translated content in localization (`i18n`) files.

## Conventions

Binding on humans and agents equally; each doc states its own scope.

- [`docs/code-style.md`](docs/code-style.md) — Go style; its comment and agent
  rules apply to every language in the repo.
- [`docs/code-style-proto.md`](docs/code-style-proto.md) — protobuf schema style
  under `spec/proto/` (buf modules, edition 2024, evolution, protovalidate).
- [`docs/conventions/protobuf.md`](docs/conventions/protobuf.md) — message shapes:
  Config/State/Event triad, LocalRef/GlobalRef pair, ambient tenancy, provenance,
  enum placement, typed variants.
- [`docs/conventions/observability.md`](docs/conventions/observability.md) —
  logging, OpenTelemetry events, traces, metrics, semantic-convention versions,
  namespacing, cardinality, correlation, and privacy; read before adding or
  changing instrumentation.
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
- [`docs/agent-steering.md`](docs/agent-steering.md) — how to decide whether a
  recurring rule belongs here, in scoped docs, in a skill, or in enforcement.

## Hard boundaries

- `spec/proto/` contains only `.proto` files and package-boundary `README.md`
  files. Enforce schema rules through `buf lint`, not executable tests.
- Never add an exclusion, ignore, suppression, or hook exception to make your own
  artifacts pass; request the policy change explicitly and separately.
- `AGENTS.md`, `buf.yaml`, `tools/hooks/`, `.claude/settings.json`,
  `.codex/hooks.json`, and merge-gate configuration are policy surfaces; changes
  require explicit guardrail review.

## Isolation

On a protected branch in the primary checkout, enter a session worktree (short
task-shaped name) before the first edit; read-only exploration is fine.
Worktrees live outside the repository and branch off local `HEAD` — the repo has
no remote. The Claude worktree hook defaults to the sibling
`worktrees/<repo>/` directory.

## Agent behavior

- Keep small sequential work in the main conversation. Delegate through the
  `delegate` skill, which names the worker and model for each kind of work:
  `repo-researcher` for a bounded read-only question, `independent-reviewer`
  for a fresh pass over changed files, and an Orca worker for editing work
  when an Orca runtime is reachable.
- The project skills `plan`, `implement`, `review`, `compound`, `close`,
  and `steer` under `.claude/skills/` carry the multi-step workflows; each
  says when it applies and when to skip it. `close` merges into `master`
  only after `implement`, `review`, and `compound` have left their
  checkpoints and leaves the worktree ready for removal; removing it is a
  person's action. `steer` works the queue in `docs/agent-observations.md`
  on request and stops at a staged diff for any policy surface.
  `docs/agent-steering.md` records why they are shaped this way.
- Auto-memory is personal and fallible; promote durable team facts per
  `docs/agent-knowledge.md`.
- FlowSeer is still building its building blocks and nothing external consumes
  them. Make a breaking change whenever it improves the overall design, in
  schemas, Go APIs, and service contracts alike. Do not add a compatibility
  shim, guard, or deprecation path to preserve a landed shape, and do not raise
  "this breaks the wire" as a blocker; state it as a fact in the plan. This
  holds until the first stable release is declared here.

## Work sequence

1. Read only the conventions, accepted direction, and captured solutions that
   apply to the task. Inspect the current source and tests before proposing a
   change.
2. Record the existing worktree state and preserve changes that are not part of
   the task. Make the smallest change that satisfies the request.
3. Run focused checks while working. Before handoff, run the diff-aware verifier
   for every changed path:

   ```bash
   .claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
   ```

4. Review the final diff against the request and repository guidance. Report the
   commands run, their results, and any residual risk.

## Investigation discipline

- State the leading hypothesis, alternatives, and a discriminating test before
  claiming a root cause; cite the evidence before editing.
- Before a live-device write or a mutation over 10,000 records, state the blast
  radius and wait for explicit approval.

## Enforced rules

Hooks in `tools/hooks/` (registered per runtime in `.claude/settings.json` and
`.codex/hooks.json`) deny hand-edits to `generated/` and `buf.lock`, reject
non-source files under `spec/proto/`, deny file writes in the primary checkout
on a protected branch, prompt for approval before an edit to a policy surface,
auto-run gofumpt/goimports and `buf format`/`buf lint` on edits, check triad
and ref message sync (a member the file-level comment names as deliberately
absent is not reported), and flag newly added lint suppressions. Both
runtimes' Stop hooks run the repository layout checks and the panic gate —
a `panic` outside a `Must`/`must` function, a `go` statement outside
`src/common/spawn` — and name edits the verifier has not seen. Hooks are
fast feedback, not the authority — `go test -race ./...` enforces the same
invariants.

## Layout

- Go (`go.aledante.io/FlowSeer`), one module unless a directory says otherwise:
  - `src/protocol/` — protocol and schema-language libraries; no domain types.
  - `src/common/` — cross-cutting foundations (`errs`, `pump`, `service`);
    no domain types.
  - `src/modules/` — reusable modules a host assembles; see its README.
  - `src/services/` — control-plane services, assembled from modules.
  - `src/edge/` — applications built to run at the edge (may also run centrally).
- `spec/proto/` + `spec/mib/` — schema sources of truth; `generated/` is
  `buf generate` output, never edited by hand.
- `docs/architecture/` — accepted direction records; read first for work on the
  device service, inventory, discovery, or ingestion planes.
- `docs/solutions/` — captured learnings (bugs, conventions, patterns) with YAML
  frontmatter; check when working in a documented area.
- `README.md` is the human entry point; `docs/README.md` maps the documentation
  system of record.
- `CONCEPTS.md` — shared domain vocabulary (entities, named processes, status
  concepts); relevant when orienting to the codebase or discussing domain terms.
- `.golangci.yml` — lint & format gate (`golangci-lint run`; gofumpt + goimports).
- `tools/serena/project.yml` — shared Serena configuration for Go-aware symbol
  lookup, reference discovery, and diagnostics. Complete the one-time client
  setup in `tools/serena/README.md`; put machine-local overrides in the ignored
  `tools/serena/project.local.yml` file.
