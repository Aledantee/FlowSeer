---
name: verify-change
description: Run FlowSeer's diff-aware format, lint, build, race-test, protobuf, hook, and configuration gates. Use after changing Go, protobuf, Claude hooks or settings, and before reporting implementation complete or preparing a commit.
argument-hint: "[--full | --base REF | -- paths]"
---

# Verify FlowSeer Change

Run the verifier from the worktree root. It selects checks from the changed
paths and, within a Go module, vets, race-tests, and lints only the packages
that can observe the change: those holding a changed file and every package
that imports one of them. The whole module still compiles. Those packages
are also vetted once per build tag their files carry, so a tagged
integration or bench test that stopped compiling fails the gate; and a
nested module that replaces the root module (`src/protocol/*/bench`,
`src/edge/netpen`) is built and vetted after a root change, its race tests
left to `--full`.

```bash
.claude/skills/verify-change/scripts/verify-change.sh
```

Run it with the Bash sandbox disabled: the Go gates bind loopback listeners
and the telemetry tier starts Docker, and the sandbox denies both.

| Scope | Command |
| --- | --- |
| every change on the branch, committed included | `verify-change.sh --base master` |
| named paths only, ignoring other worktree changes | `verify-change.sh -- <paths>` |
| all Go modules, protobuf sources, and Claude configuration | `verify-change.sh --full` |

Finish a cross-module or schema change with `--full`; a targeted run is
enough for documentation, hook, or single-module work. Pass explicit paths
when the worktree contains changes outside the current task.

A directory is expanded to the files it holds, so `-- src/edge/agent` and the
files under it select the same gates. A path list that selects no gate at all
— a typo, a deleted file on its own, a file of a type nothing checks — exits
non-zero saying so rather than reporting a pass, because a run that checked
nothing and a run that checked everything and found it clean must not print
the same line.

Full runs and telemetry-sensitive paths (service Go sources, its Collector
integration sources and fixture, the wrapper, root `go.mod` or `go.sum`) run
the Docker-backed OpenTelemetry tier through
`tools/test/service-otel-integration.sh`. An unavailable daemon is a failed
gate. `--print-selection -- <paths>` reports whether that tier would run
without running any gate.

A missing required tool is a failed gate. Do not replace a failed race test
with a non-race test or skip lint; fix the failure or report the exact
blocked command and reason.

A successful run records its scope in the worktree's git metadata and clears
matching dirty markers; `close` reads that receipt.

## Plan status ledger

`implement` keeps `$(git rev-parse --git-dir)/flowseer-plan-status.json`,
never committed, so a later session resumes a plan without re-deriving
what landed. Every verifier run that runs a gate validates it first with
`scripts/check-plan-status.py [LEDGER_PATH]`; `--print-selection` does
not. An absent ledger passes, a malformed one fails the run naming the
field. The shape:

```json
{
  "contract": "flowseer-plan-status/v1",
  "plan": "docs/plans/2026-09-06-1319-docs-example-plan.md",
  "resume": ["U2"],
  "units": [
    {"id": "U1", "status": "passed", "commit": "d037089c",
     "verified_at": "2026-09-06T12:10:00Z",
     "note": "Diagnostics keep the source span; the catalog is generated."},
    {"id": "U2", "status": "in_progress", "commit": null,
     "verified_at": null, "note": null}
  ]
}
```

`status` is one of `pending`, `in_progress`, `passed`, `blocked`; a
`passed` unit carries its commit and the receipt's `verified_at`.
`resume` lists the `in_progress` units, or the next `pending` unit when
none is in progress, and is empty once every unit is `passed`. `note` is
one line, only for a decision or pitfall the next unit needs. Unit ids are
unique. `plan` resolves against the tree root and must exist. `close` gates the merge on
every unit being `passed` and removes the ledger after the merge.
