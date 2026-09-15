# Contributing to FlowSeer

FlowSeer is a development repository, not a released distribution. Nothing
outside it consumes the schemas or the Go APIs yet, so a breaking change is
fine whenever it makes the design better. State it as a fact in your change
description and move on; do not add a compatibility shim or a deprecation path
to keep a landed shape alive. This stops when [`AGENTS.md`](AGENTS.md) declares
the first stable release.

## Set up

You need Go 1.27. Everything else depends on what you touch:

| If you change | You also need |
| --- | --- |
| Go code | `golangci-lint` v2 (lint and formatting gate) |
| `spec/proto/` | Buf v2 |
| `frontend/web/` | Node 22.12 or newer, pnpm 11.25.0 |
| integration suites that start real targets | Docker |

Get the checkout green before you edit anything. A failure you meet later is
then yours rather than something you inherited:

```bash
go build ./...
go vet ./...
go test -race ./...
```

`netpen` is a separate module so that its packet and terminal dependencies stay
out of the control-plane graph, and it is not covered by the commands above:

```bash
go -C src/edge/netpen test -race ./...
```

Integration suites that need real services use testcontainers and honor
`testing.Short()`, so a plain `go test -race ./...` does not start Docker.

## Read the conventions that apply

FlowSeer writes its rules down instead of discovering them in review, and the
same rules bind people and coding agents. [`AGENTS.md`](AGENTS.md) is the entry
point for both; its name is historical. Read what your change touches, not the
whole set:

| Document | Scope |
| --- | --- |
| [`docs/code-style.md`](docs/code-style.md) | Go APIs, errors, concurrency, tests, and the comment rules that apply to every language here |
| [`docs/code-style-proto.md`](docs/code-style-proto.md) | protobuf syntax, evolution, and validation under `spec/proto/` |
| [`docs/code-style-web.md`](docs/code-style-web.md) | the TypeScript frontend, which has its own toolchain |
| [`docs/conventions/protobuf.md`](docs/conventions/protobuf.md) | message shapes: the Config/State/Event triad, refs, tenancy, provenance |
| [`docs/conventions/observability.md`](docs/conventions/observability.md) | logging, traces, metrics, and what must never reach them |
| [`docs/conventions/testing.md`](docs/conventions/testing.md) | where a test lives and which module owns it |
| [`docs/doc-style.md`](docs/doc-style.md) | all prose, including commit messages and PR descriptions |

For work on the device service, inventory, discovery, or ingestion planes, read
the accepted direction under [`docs/architecture/`](docs/architecture/README.md)
first. [`CONCEPTS.md`](CONCEPTS.md) defines the domain vocabulary, and
[`docs/README.md`](docs/README.md) maps the rest.

## Work on the change

Several sessions often run against this repository at once, so work on a branch
in its own git worktree rather than editing `main` in the primary checkout. A
hook enforces that for Claude sessions and will refuse the first write
otherwise.

Make the smallest change that does the job, and leave unrelated worktree state
alone. When your change makes a documented claim false, fix the document in the
same commit; a stale example costs more trust than a missing one.
`docs/README.md` explains where a new document belongs, and the short answer is
usually "an existing one".

Some boundaries are enforced by hooks and tests rather than by review:

- `generated/` and `buf.lock` are never hand-edited. Change the schema or the
  generator input and regenerate: `buf generate` for protobuf, `go generate .`
  from the repository root for the SNMP MIB bindings.
- `spec/proto/` holds `.proto` files and package-boundary `README.md` files.
  Schema rules are enforced through `buf lint`, never through executable tests.
- Protocol and common libraries carry no FlowSeer domain types. Keep the
  direction of dependency pointing away from them.
- Do not add an exclusion, ignore, suppression, or hook exception so that your
  own change passes. Propose the policy change separately, on its own merits.
- `AGENTS.md`, `buf.yaml`, `tools/hooks/`, `.claude/settings.json`,
  `.codex/hooks.json`, and the merge-gate configuration are policy surfaces.
  Changing one is its own review.

## Check it before you hand it over

The diff-aware verifier picks its gates from the paths you changed and runs them
over the packages that can observe the change:

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

Use `--full` for a cross-module or schema change. The last line is the verdict,
either `FlowSeer verification passed.` or a failure naming the gate that broke.
Quote that line rather than paraphrasing it, and never report a pass from a run
that selected no gates.

The verifier is fast feedback. The merge gate is the authority, and all four
must pass:

1. `golangci-lint run`
2. `go build ./...`
3. `go vet ./...`
4. `go test -race ./...`

Frontend changes add `pnpm lint`, `pnpm typecheck`, and `pnpm test` in
`frontend/web/`. If a required tool is missing or a check is blocked, say which
command you could not run and why. Do not swap a race test for a plain one.

## Commits and pull requests

Subjects follow conventional commits with a scope taken from the path, in the
imperative:

```text
feat(netsim/fabric): derive reach and topology state from stated facts
fix(snmp): retain exact lookup uncertainty
docs(netsim): record physical and topology uncertainty landing
```

The body is for why the change looks the way it does, plus anything a reviewer
would otherwise have to reconstruct. No emoji. `doc-style.md` governs commit and
PR text the same as documentation, which means plain language, a real example
where one helps, and none of the tells of machine-written prose.

Report what you ran and what it said, including the parts that failed or that
you skipped. A residual risk named in the PR is cheap; one found in production
is not.

## If you use a coding agent

Point it at `AGENTS.md` and let it work. The workflows under `.claude/skills/`
carry the multi-step procedures (`plan`, `implement`, `review`, `compound`,
`close`), and `docs/agent-steering.md` explains why they are shaped that way.
Repository guidance outranks an agent's private memory; when they disagree,
correct the memory.

You are answerable for what you submit either way. A change nobody on the
sending side understands moves the work of understanding it to the reviewer,
which is the fastest way to lose one.
