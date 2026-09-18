---
name: verify-change
description: Run FlowSeer's diff-aware format, lint, build, race-test, protobuf, hook, and configuration gates. Use after changing Go, protobuf, Claude hooks or settings, and before reporting implementation complete or preparing a commit.
argument-hint: "[--full | --base REF | -- paths]"
---

# Verify FlowSeer Change

## Run it

Run the verifier from the worktree root, with the Bash sandbox disabled: the
Go gates bind loopback listeners and the telemetry tier starts Docker, and
the sandbox denies both.

```bash
.claude/skills/verify-change/scripts/verify-change.sh
```

| Scope | Command |
| --- | --- |
| every change on the branch, committed included | `verify-change.sh --base main` |
| named paths only, ignoring other worktree changes | `verify-change.sh -- <paths>` |
| all Go modules, protobuf sources, and Claude configuration | `verify-change.sh --full` |

- Run it on the final tree, after the last edit. An earlier run is evidence
  about a tree that no longer exists, and `close` compares the receipt's
  `verified_at` with the last commit.
- Finish a cross-module or schema change with `--full`. A targeted run is
  enough for documentation, hook, or single-module work.
- Pass explicit paths when the worktree holds changes outside the task. A
  directory expands to the files it holds, so `-- src/edge/agent` and the
  files under it select the same gates.
- In the background, make the script the last command of its invocation: a
  trailing `tail` or `echo` reports its own exit code as the gate's.
- Send output you may need to a file under `$TMPDIR` and grep it
  afterwards. A pipe through a filter drops lines before you know what the
  run contained, and a log written into the worktree as `.md`, `.json`, or
  `.yaml` is a new file of a verified type, which the marker hook records
  like any other edit.

## Read the verdict

The last line is the verdict. Quote it; do not summarize it.

| Last line | Meaning |
| --- | --- |
| `FlowSeer verification passed.` | every selected gate ran and passed |
| `FlowSeer verification FAILED (exit N) in gate: <command>` | that gate failed |
| `FlowSeer verification FAILED (exit N).` | no gate ran: a bad argument, a missing tool, or a path list that selects no gate; the reason is the line above |

A pass always means a gate ran. A path list that selects nothing (a typo, a
deleted file on its own, a file type nothing checks) exits non-zero, because
a run that checked nothing and a run that found everything clean must not
print the same line. The same holds for a cached test answer, a wrapper's
exit code, and a step with nothing to check: whatever produces a pass
without a gate having run is a bug in the verifier, and is reported as one.

Fix a failed gate, or report the exact blocked command and its reason. A
non-race test does not stand in for a failed race test, and lint is never
skipped.

## What the gate covers

The gate is this closed list; read it here rather than restating it from
memory. Format, build, vet, race test, and lint run on every package that
can observe the change: those holding a changed file and every package
importing one. The whole module still compiles. In addition:

- `src/common/errs` and `test/conformance/proto` run on every targeted run
  of the root module. They hold repository-wide invariants (error-code
  uniqueness, schema layering) that no changed package's tests can see.
- Packages are vetted once per build tag their files carry, so a tagged
  integration or bench test that stopped compiling fails the gate.
- A nested module that replaces the root module (`src/protocol/*/bench`,
  `src/edge/netpen`) is built and vetted after a root change; its race
  tests run under `--full`.
- `buf breaking` compares only the changed `.proto` files `main` already
  holds, and prints that it skipped when every changed schema file is new
  on the branch: `--path` naming a file the baseline lacks targets nothing,
  which buf reports as a failure that says nothing about the change. The
  whole-module form under `--full` still covers a deletion.
- Full runs and telemetry-sensitive paths (service Go sources, its Collector
  integration sources and fixture, the wrapper, root `go.mod` or `go.sum`)
  run the Docker-backed OpenTelemetry tier through
  `tools/test/service-otel-integration.sh`. An unavailable daemon is a
  failed gate. `--print-selection -- <paths>` reports whether that tier
  would run, without running any gate.

## Failures that are not findings

- Every command-line tool the selected gates invoke is
  checked before the first gate, and a missing one stops the run with the
  whole list and no gate name: `required tools are not on PATH: gofumpt
  goimports`. Go tools are looked up in `$(go env GOPATH)/bin` whether or
  not the session's PATH carries it. This is setup, not a finding. The
  Docker daemon is not a PATH tool and stays a failed gate when unavailable.
- `golangci-lint` takes one file lock per machine. The
  gate passes `--allow-serial-runners` and waits for a concurrent instance;
  a hand run beside it, without that flag, fails with
  `parallel golangci-lint is running`. Repeat the hand run once the
  verifier has finished.
- `--full` bounds `go test -p` to half the
  CPUs, because packages that start listeners or walk a corpus time out
  under one test binary per CPU and pass alone. A timeout is a finding only
  once it reproduces with the package run alone, and a pass alone does not
  clear the change either. Attribute it in one step: the same failure on a
  detached checkout of the base, with the change absent. `go list -deps` on
  the failing package says where to look first, but it answers a
  compile-time question only: a change also reaches a test through a
  shared port, a `testdata/` directory, or an environment variable.

## Receipt and dirty marker

The marker hook records every edit by path: an editor edit from the tool
call, a Bash write from the tree, by comparing the content hashes of the
dirty paths (`tools/hooks/tree-state.sh`) with the listing stored after the
previous Bash call. A command's text decides nothing. A passing run records
its scope in the worktree's git metadata, clears the marker lines it
verified, and rewrites that listing; `close` reads the receipt.

- A targeted run clears only the paths it named and records `full=false`.
  It prints the lines that remain under
  `Unverified edits remain after this run:`.
- A Bash change under `generated/`, or to a `go.mod`, `go.sum`, or
  `buf.lock`, also writes the `<Bash mutation; verify with --full>` line,
  because a generator and the module graph reach packages no path names.
  That line clears only under `--full`; the path beside it says what
  caused it.
- A `--full` run removes the marker outright, so a marker found beside a
  `full=true` receipt names paths edited after the run.

## Plan status ledger

`implement` keeps `$(git rev-parse --git-dir)/flowseer-plan-status.json`,
never committed, so a later session resumes a plan without re-deriving
what landed. Every verifier run that runs a gate validates it first with
`scripts/check-plan-status.py [LEDGER_PATH]`; `--print-selection` does
not. An absent ledger passes, a malformed one fails the run naming the
field.

The skills write the ledger and the checkpoints file beside it only
through `scripts/ledger.py`, which resolves the git directory itself,
writes each file whole, and recomputes `resume`; its docstring lists the
subcommands (`init`, `set`, `show`, `checkpoint`). A session isolated in
a worktree cannot write into the parent checkout's `.git/` through a
redirect or the Write tool, and the script's command line names only the
unit, status, and note, so it is the one caller that reaches the
directory, as the verifier is for its receipt. The shape:

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
