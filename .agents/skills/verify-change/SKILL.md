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
  files under it select the same gates. A path through a linked directory
  is rewritten to the one git tracks, so `.claude/skills/land` verifies
  `.agents/skills/land`.
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
checked in which mode, whether the OpenTelemetry tier would run, whether the
protobuf gates would run, whether `tools/buf/` is verified with
`go -C tools/buf mod verify`, and whether the hook tooling gates would run
(`hook_tooling=true`, which a `tools/scripts/` path, an `.agents/`, `.claude/`,
or `.codex/` path, `AGENTS.md`, or `tools/hooks/` selects).

## Read the verdict

The last line is the verdict. Quote it; do not summarize it.

| Last line | Meaning |
| --- | --- |
| `FlowSeer verification passed.` | every selected gate ran and passed |
| `FlowSeer verification FAILED (exit N) in gate: <command>` | that gate failed |
| `FlowSeer verification FAILED (exit N).` | no gate ran: a bad argument, a missing tool, no changed paths against the base, or a path list that selects no gate. The line above gives the reason |

A pass always means a gate ran. A path list that selects nothing (a typo, a
deleted file on its own, a file type nothing checks) exits non-zero, and so
does a run with no changed paths, since it would check nothing. A
`.golangci.yml` change on a targeted run also exits non-zero and asks for
`--full`, because the new configuration applies to every module. Anything
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

Every verifier run that names a path under `docs/plans/` also runs
`uv run tools/scripts/run.py verify check-plan-state`, including a run that names
only a `.state.json` path. The check always reads every state file, since one
is legal only beside its parent's and its phases', so an illegal file the run
did not name fails it too.

`implement` keeps `$(git rev-parse --git-dir)/flowseer-plan-status.json`,
never committed, so a later session resumes a plan without re-deriving what
landed. Every verifier run that runs a gate validates it first with
`uv run tools/scripts/run.py verify check-plan-status [LEDGER_PATH]`; `--print-selection` does not.
An absent ledger passes; a malformed one fails the run naming the field.

The skills write the ledger and the checkpoints file beside it only through
`uv run tools/scripts/run.py verify ledger`, which resolves the git directory itself, writes each
file whole, and recomputes `resume`; its `--help` lists the subcommands
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
    {"id": "U2", "status": "in_progress", "base": "d037089c",
     "commit": null, "verified_at": null, "note": null}
  ]
}
```

| Field | Rule |
| --- | --- |
| `status` | one of `pending`, `in_progress`, `passed`, `blocked`; a `passed` unit carries its commit and the receipt's `verified_at` |
| `base` | `HEAD` when the unit went `in_progress`; `verify ledger set <unit> passed` refuses a commit outside this branch or with nothing committed since the base, and a receipt whose `verified_at` is older than the unit's commit |
| `resume` | the `in_progress` units, or the next `pending` unit when none is in progress; empty once every unit is `passed` |
| `note` | one line, only for a decision or pitfall the next unit needs |
| `id` | unique |
| `plan` | resolves against the tree root and must exist |

`land` gates the merge on every unit being `passed` and removes the ledger
after the merge.

When `uv run tools/scripts/run.py plan record show <phase>` reports a non-null `parent`, the check also
proves the phase belongs in this tree. The phase state supplies `parent`,
`after`, and `landed`, while the parent's state supplies its `retired` entries:

- every phase in the phase state's `after` list has a landed range whose last
  commit is an ancestor of `HEAD` (a worktree forked before the previous
  phase merged fails this);
- on `main`, the phase's own state has no landed range and the parent's
  `retired` list has no entry for it (a phase being implemented a second
  time fails this).

The message says which check failed.

## Test changes

Before the gates, `uv run tools/scripts/run.py verify check-test-integrity` lists, against the
base, changes that weaken what the suite proves: a deleted `_test.go` file,
a removed `Test`, `Benchmark`, `Fuzz`, or `Example` function, an added
`t.Skip`, and a modified or deleted file under `testdata/`. The list prints
under `Test changes to account for:` and does not fail the run; `implement`
quotes it in its report with a reason per line, and `review` reads the
reasons.

## Package guarantees

`tools/check-guarantees` verifies each `GUARANTEES.md` in the directory of
a changed path (or all of them under `--full`) against
[`docs/conventions/guarantees.md`](../../../docs/conventions/guarantees.md).
The gate builds the Go checker and runs it on selected paths, or with `--all`
under `--full`. Goldmark parses the CommonMark blocks. Each section needs one
MUST/MUST NOT paragraph, one `-` list of WHEN ... THEN items, and one
`Proved by:` paragraph; unknown blocks fail the run. Counted blocks allow text,
line breaks, code spans, and emphasis. Raw HTML, images, links, and autolinks
fail; visible text comes from parsed nodes without rendering. Cited tests
resolve through `go list`'s `TestGoFiles` and `XTestGoFiles`. The linked
convention gives the full rules and diagnostics.
