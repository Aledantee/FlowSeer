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
| every change on the branch, committed included | `verify-change.sh --base main` |
| named paths only, ignoring other worktree changes | `verify-change.sh -- <paths>` |
| all Go modules, protobuf sources, and Claude configuration | `verify-change.sh --full` |

Finish a cross-module or schema change with `--full`; a targeted run is
enough for documentation, hook, or single-module work. Pass explicit paths
when the worktree contains changes outside the current task.

The gate is this closed list, and a coordinator restating it from memory
is how lint went missing once: format, build, vet, race test, and lint on
every package that can observe the change, plus `src/common/errs` and
`test/conformance/proto` on every targeted run of the root module, because
their tests hold repository-wide invariants (error-code uniqueness, schema
layering) that no changed package's own tests can see. It runs on the final
tree, after the last edit; a run before it is evidence about a tree that no
longer exists, and `close` compares the receipt's `verified_at` with the
last commit.

The last line is the verdict: `FlowSeer verification passed.` or
`FlowSeer verification FAILED (exit N) in gate: <command>`. A failure
outside any gate (a bad argument, a missing formatter, no gate selected)
prints `FlowSeer verification FAILED (exit N).` with no gate name, and the
reason is the line above it. Quote the last line rather than summarize it.
When the script runs in the background, it is the last command of its
invocation: a trailing `tail` or `echo` reports its own exit code as the
gate's, and a session has announced a green verifier that way over a log
holding two `FAIL` lines. Any output you may need later goes to a file and
is grepped afterwards, never piped through a filter, since the filter is
applied before you know what the run contained.

A directory is expanded to the files it holds, so `-- src/edge/agent` and the
files under it select the same gates. A path list that selects no gate at all
— a typo, a deleted file on its own, a file of a type nothing checks — exits
non-zero saying so rather than reporting a pass, because a run that checked
nothing and a run that checked everything and found it clean must not print
the same line. The same rule covers every path to "passed": a cached test
answer, a wrapper's exit code, a step that had nothing to check. Whatever
produces a pass without a gate having run is the bug.

`--full` bounds `go test -p` to half the CPUs: a module-wide race run at
one package binary per CPU times out packages that start listeners or walk
a corpus, and they pass alone. A timeout under the wide run is still not a
finding until it reproduces package-alone, and an isolated pass does not
clear the change either. Attribution is one step: the same failure on a
detached checkout of the base with the change absent. `go list -deps` on
the failing package says where to look first, but it answers a
compile-time question only; a change reaches a test without appearing in
its imports through a shared port, a testdata directory, or an environment
variable.

`buf breaking` compares only the changed `.proto` files main already
holds, and prints that it skipped when every changed schema file is new on
the branch: `--path` naming a file absent from the baseline targets nothing,
which buf reports as a failure carrying no signal about the change. The
whole-module form under `--full` still covers a deletion.

Full runs and telemetry-sensitive paths (service Go sources, its Collector
integration sources and fixture, the wrapper, root `go.mod` or `go.sum`) run
the Docker-backed OpenTelemetry tier through
`tools/test/service-otel-integration.sh`. An unavailable daemon is a failed
gate. `--print-selection -- <paths>` reports whether that tier would run
without running any gate.

Every command-line tool the selected gates invoke is checked before the
first gate runs, and a missing one stops the run with the whole list and
no gate name: `required tools are not on PATH: gofumpt goimports`. That is
a setup failure, not a finding. Go tools are looked up in
`$(go env GOPATH)/bin` whether or not the session's PATH carries it. The
Docker daemon the telemetry tier needs is not a PATH tool and stays a
failed gate when unavailable. Do not replace a failed race
test with a non-race test or skip lint; fix the failure or report the exact
blocked command and reason.

The lint gate is exclusive machine-wide: `golangci-lint` takes one file
lock per machine, and the gate passes `--allow-serial-runners` so a run
waits for a concurrent instance instead of dying. A `golangci-lint` run by
hand alongside a verification, without that flag, fails with
`parallel golangci-lint is running`; that is contention, not a finding,
and the hand run is repeated once the verifier has finished.

A successful run records its scope in the worktree's git metadata and
clears the dirty-marker lines it verified; `close` reads that receipt. A
targeted run clears only the paths it named, and the
`<Bash mutation; verify with --full>` line clears only under `--full`, so
a passing run prints the lines that remain under
`Unverified edits remain after this run:`, and the receipt records
`full=false`. A `--full` run removes the marker outright, so a marker
found beside a `full=true` receipt was written after the run by a Bash
command the edit hook could not attribute to a path. Send a run's log to a
`.log` file, not to `.md`, `.json`, or `.yaml`: the hook marks a redirect
into those, so a `--full` run logged to `verify.md` re-creates the line it
just cleared.

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

When the plan carries a `parent:` field, the check also proves the phase
belongs in this tree: every phase the parent's `After:` names has a
`Landed:` line whose last commit is an ancestor of `HEAD`, and the
parent on `main` shows this phase's own `Landed:` empty. A worktree
forked before the previous phase merged fails the first, a phase being
implemented a second time fails the second, and the message says which.

## Test changes

Before the gates, the run lists changes that weaken what the suite
proves, from `scripts/check-test-integrity.py` against the base: a
deleted `_test.go` file, a removed `Test`, `Benchmark`, `Fuzz`, or
`Example` function, an added `t.Skip`, and a modified or deleted file
under `testdata/`. The list is printed under `Test changes to account
for:` and does not fail the run; `implement` quotes it in its report with
a reason per line, and `review` reads the reasons.
