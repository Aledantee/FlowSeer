---
name: verify-change
description: Runs FlowSeer's diff-aware format, lint, build, race-test, protobuf, web, hook, and configuration gates. Use after changing Go, protobuf, web, Claude hooks or settings, and before reporting implementation complete or preparing a commit. Not for judging whether a change is right; `review` does that.
argument-hint: "[--full | --base REF | -- paths]"
---

# Verify FlowSeer Change

## Run it

Run the verifier from the worktree root with the Bash sandbox disabled, since
the Go gates bind loopback listeners and the telemetry tier starts Docker.

```bash
.claude/skills/verify-change/scripts/verify-change.sh
```

| Scope | Command |
| --- | --- |
| every change on the branch, committed included | `verify-change.sh --base main` |
| named paths only, ignoring other worktree changes | `verify-change.sh -- <paths>` |
| all Go modules, protobuf sources, web, and Claude configuration | `verify-change.sh --full` |
| report the selection without running any gate | `verify-change.sh --print-selection -- <paths>` |

- Run it on the final tree, after the last edit; `land` compares the
  receipt's `verified_at` with the last commit.
- Finish a cross-module or schema change with `--full`. A targeted run is
  enough for documentation, hook, or single-module work.
- Pass explicit paths when the worktree holds changes outside the task. A
  directory expands to the files it holds, so `-- src/edge/agent` and the
  files under it select the same gates.
- A `frontend/web/` path needs the web workspace's local binaries, which a
  fresh worktree lacks. Install them with
  `pnpm --dir frontend/web install --frozen-lockfile`, outside the sandbox
  like the verifier (it reaches the npm registry and the global pnpm store),
  rather than linking another worktree's `node_modules`.
- In the background, make the script the last command of its invocation: a
  trailing `tail` or `echo` reports its own exit code as the gate's.
- Send output you may need to a file under `$TMPDIR` and grep it afterwards.
  A pipe through a filter drops lines before you know what the run held, and
  a `.md`, `.json`, or `.yaml` log written into the worktree is a new file of
  a verified type, which the marker hook records like any other edit.

`--print-selection` says which modules and dependent modules would be
checked in which mode, and whether the OpenTelemetry tier would run.

## Read the verdict

The last line is the verdict. Quote it; do not summarize it.

| Last line | Meaning |
| --- | --- |
| `FlowSeer verification passed.` | every selected gate ran and passed |
| `FlowSeer verification FAILED (exit N) in gate: <command>` | that gate failed |
| `FlowSeer verification FAILED (exit N).` | no gate ran: a bad argument, a missing tool, or a path list that selects no gate; the reason is the line above |

A pass always means a gate ran. A path list that selects nothing (a typo, a
deleted file on its own, a file type nothing checks) exits non-zero. Anything
that produces a pass without a gate having run (a cached test answer, a
wrapper's exit code, a step with nothing to check) is a bug in the verifier;
report it as one.

Fix a failed gate, or report the exact blocked command and its reason. A
non-race test does not stand in for a failed race test, and lint is never
skipped. Load `references/non-findings.md` when a run stops on a missing
tool, a `golangci-lint` lock, or a test timeout; those are not findings
about the change until that file's checks say so.

## What the gate covers

This list is closed; read it rather than restating it from memory.
Format, build, vet, race test, and lint run on every package that can
observe the change: those holding a changed file and every package importing
one. The whole module still compiles. The invariant packages, build tags,
nested and generated modules, web workspace, `buf breaking`, and the
OpenTelemetry tier complete the list: load `references/gate-coverage.md`
before stating what a run covered or explaining why a path selected a gate.

## Receipt and dirty marker

The marker hook records every edit by path: an editor edit from the tool
call, a Bash write by comparing the content hashes of the dirty paths
(`tools/hooks/tree-state.sh`) with the listing stored after the previous
Bash call. A command's text decides nothing. A passing run records its scope
in the worktree's git metadata, clears the marker lines it verified, and
rewrites that listing; `land` reads the receipt.

- A targeted run clears only the paths it named and records `full=false`. It
  prints the lines that remain under `Unverified edits remain after this run:`.
- A Bash change under `generated/`, or to a `go.mod`, `go.sum`, or
  `buf.lock`, also writes the `<Bash mutation; verify with --full>` line,
  since a generator and the module graph reach packages no path names. The
  path beside the line says what caused it. The line clears under `--full`,
  or under `--base REF` when every generated, `go.mod`, `go.sum`, or
  `buf.lock` path the marker names is byte-identical to `REF` or absent from
  both the tree and `REF`. Any marked file in either state clears the same
  way, since it is the base, not an edit.
- A `--full` run removes the marker outright, so a marker found beside a
  `full=true` receipt names paths edited after the run.

## Plan status ledger

`implement` keeps `$(git rev-parse --git-dir)/flowseer-plan-status.json`,
never committed, so a later session resumes a plan without re-deriving what
landed. Every verifier run that runs a gate validates it first with
`scripts/check-plan-status.py [LEDGER_PATH]`; `--print-selection` does not.
An absent ledger passes; a malformed one fails the run naming the field.

The skills write the ledger and the checkpoints file beside it only through
`scripts/ledger.py`, which resolves the git directory itself, writes each
file whole, and recomputes `resume`; its docstring lists the subcommands
(`init`, `set`, `show`, `checkpoint`). A worktree-isolated session cannot
write into the parent checkout's `.git/` through a redirect or the Write
tool, and the script's command line names only the unit, status, and note,
so it is the one caller that reaches the directory. The shape:

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

| Field | Rule |
| --- | --- |
| `status` | one of `pending`, `in_progress`, `passed`, `blocked`; a `passed` unit carries its commit and the receipt's `verified_at` |
| `resume` | the `in_progress` units, or the next `pending` unit when none is in progress; empty once every unit is `passed` |
| `note` | one line, only for a decision or pitfall the next unit needs |
| `id` | unique |
| `plan` | resolves against the tree root and must exist |

`land` gates the merge on every unit being `passed` and removes the ledger
after the merge.

When the plan carries a `parent:` field, the check also proves the phase
belongs in this tree:

- every phase the parent's `After:` names has a `Landed:` line whose last
  commit is an ancestor of `HEAD` (a worktree forked before the previous
  phase merged fails this);
- the parent on `main` shows this phase's own `Landed:` empty (a phase being
  implemented a second time fails this).

The message says which check failed.

## Test changes

Before the gates, `scripts/check-test-integrity.py` lists, against the
base, changes that weaken what the suite proves: a deleted `_test.go` file,
a removed `Test`, `Benchmark`, `Fuzz`, or `Example` function, an added
`t.Skip`, and a modified or deleted file under `testdata/`. The list prints
under `Test changes to account for:` and does not fail the run; `implement`
quotes it in its report with a reason per line, and `review` reads the
reasons.

## Package guarantees

`scripts/check-guarantees.py` verifies each `GUARANTEES.md` in the directory of
a changed path (or all of them under `--full`) against
[`docs/conventions/guarantees.md`](../../../docs/conventions/guarantees.md).
The check enforces a strict line grammar of positive shapes, so any line that
would open another CommonMark block (a fence, a setext underline, an indented
code block, a thematic break, a list, a block quote, an HTML block, a heading of
another level) fails with an unknown line format. It fails the run if a heading
is duplicated, a section lacks exactly one normative MUST line, lacks a
`- WHEN … THEN …` scenario bullet, or lacks exactly one column-0 `Proved by:`
line whose comma-separated items are Go identifiers (optionally backticked; the
list may wrap across indented continuation lines, and an empty item or trailing
comma is an error). A cited test resolves when it is a top-level test function
in the package's `TestGoFiles` and `XTestGoFiles` as `go list -json .` reports
them, using the signature forms Go accepts; a `go list` failure is reported once
and its citations are not checked.
