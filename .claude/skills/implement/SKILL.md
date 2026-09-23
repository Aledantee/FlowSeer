---
name: implement
description: Implement a FlowSeer plan from docs/plans/ or a concrete, already-decided build request end to end, unit by unit, with the repository verifier run on every changed path. Use when asked to implement, build, execute, or work a plan. Not for open-ended bugs or for requests that still need design choices.
argument-hint: "[plan path]"
---

# Implement a FlowSeer plan

## Inputs

- A plan path under `docs/plans/`, or a request whose design is settled.
- The current worktree. On a protected branch in the primary checkout, enter a
  worktree first (`AGENTS.md`, Isolation).

## 1. Orient

Read the plan's Goal, Decisions, and Units; the rest when a unit cites it.
A plan whose `artifact_readiness` is `needs-decisions`, or whose Units name
other plan files, is not executable: say why and ask the user whether to
run `plan` to settle it (recommended) or stop here. Check the plan's
`status` and the current tree: a plan may be
partly landed, and the tree wins over the plan about what exists. Record
such a mismatch in the plan's Open questions before touching code.

After a context compaction, or when this session resumes one that planned,
re-read the plan and the ledger before trusting the summary: a summary
that reads well still drops the unit that was in flight and the ruling it
rested on.

### Resume from the ledger

The ledger at `$(git rev-parse --git-dir)/flowseer-plan-status.json`,
whose shape `verify-change`'s `SKILL.md` documents, records which units
landed. When it exists and names this plan, read it before the first
edit and check every `passed` commit:

```bash
git merge-base --is-ancestor <commit> HEAD
```

Any non-zero exit, the one for an unknown object included, means not an
ancestor: report the unit, compare its files with the tree, record the
mismatch under Open questions, and ask the user before rewinding a unit.
Then continue from `resume`. A ledger naming another plan is replaced only
after the user confirms (`init --force`). When the work has a plan and no
ledger, write one with every unit `pending` before the first edit; a
planless request keeps no ledger. Every ledger write goes through
`.claude/skills/verify-change/scripts/ledger.py`, which resolves the git
directory itself and recomputes `resume`; read it with `show` or the Read
tool, and never write it by hand:

```bash
.claude/skills/verify-change/scripts/ledger.py init <plan> U1 U2 U3
```

Read the `docs/architecture/` record for the area, the `CONCEPTS.md` entries
the plan uses, and the conventions for the files you will touch:
`docs/code-style.md` for Go and its Testing section for every test, `docs/code-style-proto.md` and
`docs/conventions/protobuf.md` for schema, `docs/conventions/observability.md`
for instrumentation, `docs/doc-style.md` for prose.

Record `git status --porcelain` before the first edit. Changes outside the
task stay as they are; at Finish, report anything that appeared since.

## 2. Work the units

Group the units into waves from their `After` lines: a wave is every unit
whose prerequisites have landed. A wave of two or more units runs in
workers, as many at once as `delegate`'s Wave size allows (or the budget
a `drive` brief names), as `references/workers.md` describes; load
it before the first such wave, and for any plan with a `parent:` field.
A wave of one unit runs here. Serial execution of independent units is
the slow path and needs a reason in the report, such as no pool with
headroom.

For each unit:

1. Re-read the unit, set it `in_progress` in the ledger with
   `ledger.py set <unit> in_progress`, then inspect the current source and
   tests for its files.
2. Make the smallest change that satisfies it, through the editor tools,
   which run the format and schema hooks a Bash write skips. A Bash
   command that changes `generated/`, a `go.mod` or `go.sum`, or
   `buf.lock` (`buf generate`, `go mod tidy`) marks the tree
   `<Bash mutation; verify with --full>` and turns Finish into a full
   module race run; any other Bash write is marked by path. Search for an existing helper first; no abstraction
   with a single caller. Before calling a third-party API the tree does
   not already use, check its signature: `go doc` for Go, Context7
   (`mcp__context7__query-docs` or the `ctx7` CLI) for the rest. Where
   the plan and the working code disagree about a shape, the code wins:
   leave the member out and edit the plan in the same commit, with the
   reason, because a plan left contradicting its code is read as the
   specification by the next session.
3. Write or extend the tests the unit names, under the Testing rules of
   `docs/code-style.md`. When the unit changes behavior, write the failing
   test first and watch it fail. Every new test is watched failing
   against the defect before it counts: revert the fix or feed a wrong
   implementation, and undo from a copy taken first
   (`cp <path> "$TMPDIR/<name>.orig"`), never with `git checkout` or
   `git restore`, which on an unfinished unit discard everything since the
   last commit. A new package has no prior behavior to revert, so its
   mutation is the guard the test pins, removed. The unit's commit body
   carries one line per new test, the mutation and the quoted `--- FAIL`
   line it produced, because `review` runs in a later session and reads
   the commits, not this conversation. A unit without a test needs a
   stated reason in the plan.
4. Update the package README, convention doc, solution citations, and any
   test or benchmark name the unit made false, in the same unit.
5. Check, commit, verify, in that order:
   - Run the focused checks (`go test -race ./<pkg>/...`, `buf lint`).
     Output that may carry diagnostics goes to a file and is grepped after
     (`go test ... > "$TMPDIR/run.log" 2>&1; grep -E '^(FAIL|--- FAIL)' "$TMPDIR/run.log"`),
     never through a filter that drops what it does not match.
   - When the unit adds or removes a name in a repository-wide namespace
     (an error code, a telemetry scope, an event or metric name, a bus
     subject, a bucket), grep the tree for that name: no per-package gate
     sees two owners.
   - Commit the unit.
   - Run the verifier for the unit's paths, sandbox disabled, in the
     background while you read on, as the last command of its invocation
     (a trailing `echo` or `tail` reports its own exit code as the gate's):

   ```bash
   .claude/skills/verify-change/scripts/verify-change.sh -- <paths>
   ```

   When a unit leaves the package red until the next unit lands, verify
   those units together and say so.

6. Once that run's last line reads `FlowSeer verification passed.`, quoted
   into the report verbatim, write the unit `passed` in the ledger:
   `ledger.py set U1 passed` records `HEAD` and that run's `verified_at`
   from the receipt and moves `resume` to the next unit; add `--note` only
   when the unit produced a decision or pitfall the next unit needs, in
   one line. In Orca, set the worktree comment to the unit that landed.

A unit still red after three verifier rounds is `blocked` in the ledger
(`ledger.py set U1 blocked --note "<reason>"`), and the Finish question
offers taking it back to `plan`: a fourth
patch on the same failure optimizes the test that is visible, not the
requirement behind it, and the plan is where the requirement lives.

Plan labels stay in the plan: never write `U2`, `R4`, or a plan filename into
code, comments, or commit messages.

### Rulings

A unit that needs a decision the plan does not make gets one of two
treatments. When the answer changes other units, the wire, or an accepted
record, ask the user (`AGENTS.md`, Agent behavior) with the options you
see and your recommendation, and wait. Otherwise rule and continue: append to the
plan's Decisions, at the moment of the call, one line of the form
`Ruled: <what>. Why: <reason>. Cost if wrong: <what a reversal touches>.`
Open questions holds only what is still open; a decision filed there
after the fact reads as unresolved to the reviewer and as settled to the
next implementer. Rulings are the first item of the Finish report.

A ruling is provisional until its unit lands. Evidence that contradicts
one (a test that passes when the ruling says it cannot, an experiment
whose result the ruling does not predict) stops the unit: revise the
`Ruled:` line and everything written from it, comments and test docs
included, before the next edit. Comments are written from the source, not
from the ruling; a false ruling left standing gets cited as though it were
the code.

Delegate a bounded read-only question as `delegate` describes when it would
cost more than a few file reads.

## 3. Finish

The order here matters: first record the outcome in the plan and amend it
into the last unit's commit, rewrite that unit's `commit` in the ledger
with the new hash (`ledger.py set <unit> passed`, which reads `HEAD`
again), and only then run the verifier, as the last action of
the task, sandbox disabled. A run before the last edit is evidence about a
tree that no longer exists, and `land` refuses a receipt older than the
last commit. Run it as `--base main -- <paths>`, the task's paths
explicit when the worktree holds unrelated changes, and `--base main`
alone otherwise; a run against `HEAD` after the commit sees an empty
diff, so the test-change list below would come out empty for a branch
that deleted a test three units ago. When
`$(git rev-parse --git-dir)/flowseer-verification-dirty` holds the
`<Bash mutation; verify with --full>` line, run `--full` instead; nothing
else clears it. Quote the run's last line into the report; a line other
than `FlowSeer verification passed.` blocks the report.

Read the deviations off the tree, not from memory: a session's account of
what it changed covers a fraction of what it did and drifts toward the
plan it was given.

```bash
.claude/skills/implement/scripts/plan-deviations.py <plan> main -- <paths>
```

Every path under "Changed, named by no unit" and every entry under "Named
by a unit, unchanged" goes into the report with its reason. The verifier's
`Test changes to account for:` block, when it prints one, is quoted the
same way, one reason per line: a deleted or skipped test and a rewritten
golden file are the recorded ways a passing suite stops proving anything.

Recording the outcome in the plan, read from the ledger, in the same commit
as the last unit leaves the ledger in place for `land` to gate on. Set
`status: implemented` and add `> Implemented.` under the title when every
unit landed; otherwise `status: partially-implemented` and
`> Partially implemented: <units>.` with the reason. The note carries the
unit count and the span of the ledger's `verified_at` values, as
`> Implemented. 6 units, 2026-09-11T10:02Z to 2026-09-11T16:40Z.`, so
`steer` can tune the phase size from data. When the plan carries a
`parent:` field, fill this phase's `Landed:` line in the parent with the
commit range in backticks, as `` `601e6e03..7cdc35dd` ``, first commit to
last: the ledger check reads the last commit of that range to prove a
later phase's worktree holds this one, and a `Landed:` written as prose
fails it. Set the parent to `implemented` when this was its last phase,
in the same commit. A request that skipped the plan records the outcome as
one line, `implemented: <request in a few words>`, written as the whole
content of `$(git rev-parse --git-dir)/flowseer-checkpoints`, the file
`land` (step 1) reads for planless work; overwriting rather than
appending drops the lines an earlier task left in a reused worktree:

```bash
.claude/skills/verify-change/scripts/ledger.py checkpoint --replace implemented "<request in a few words>"
```

Write it even for a small change, since it is then the only implementation
signal `land` has. In Orca the card entry below is written as well.

In Orca, mark the card for `land`, naming the plan path, or the request in
a few words when there is no plan:

```bash
orca worktree set --worktree active --workspace-status in-review \
  --comment "implemented: <plan path or request>" --json
```

Use `partially implemented: <units>` and leave the status at `in-progress`
when units remain.

Read the final diff against the plan's Definition of done and against
`docs/code-style.md`, Rules for coding agents. Remove process narration,
history references, and planning identifiers from comments.

In Orca, no child worktree this task created remains: `orca worktree list
--json` lists none whose `parentWorktreeId` is this worktree, other than
the ones the report names with the reason they stayed.

Report, outcome first: rulings, units done with the waves they ran in,
commands run with results, the deviation and test-change lists with their
reasons, residual risk.

End by asking the user what happens next (`AGENTS.md`, Agent behavior):
run `review` on the branch now (recommended when every unit landed);
continue with the remaining units, naming them; take a `blocked` unit back
to `plan` (recommended over any further patch, which the three-round cap
forbids); stop here.
Review runs only on that answer, never on its own. A correction to this
procedure is logged as `compound`, Observe describes.
