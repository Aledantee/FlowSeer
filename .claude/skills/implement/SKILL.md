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
other plan files, is not executable: stop and name `plan` as the next
skill. Check the plan's `status` and the current tree: a plan may be
partly landed, and the tree wins over the plan about what exists. Record
such a mismatch in the plan's Open questions before touching code.

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
after the user confirms. When the work has a plan and no ledger, write one
with every unit `pending` before the first edit; a planless request keeps
no ledger.

Read the `docs/architecture/` record for the area, the `CONCEPTS.md` entries
the plan uses, and the conventions for the files you will touch:
`docs/code-style.md` for Go, `docs/code-style-proto.md` and
`docs/conventions/protobuf.md` for schema, `docs/conventions/observability.md`
for instrumentation, `docs/doc-style.md` for prose.

Record `git status --porcelain` before the first edit. Changes outside the
task stay as they are; at Finish, report anything that appeared since.

## 2. Work each unit

Take the units in the plan's order, or in parallel where `After` allows and
the user asked for it (see Units in workers). For each:

1. Re-read the unit, set it `in_progress` in the ledger, then inspect the
   current source and tests for its files.
2. Make the smallest change that satisfies it, through the editor tools:
   a Bash command that writes a source file or runs a generator (`sed -i`,
   a heredoc, `gofumpt -w`, `buf generate`, `go mod tidy`) marks the tree
   `<Bash mutation; verify with --full>` and turns Finish into a full
   module race run. Search for an existing helper first; no abstraction
   with a single caller. Before calling a third-party
   API the tree does not already use, check its signature: `go doc` for Go,
   Context7 (`mcp__context7__query-docs` or the `ctx7` CLI) for the rest.
   Where the plan and the working code disagree about a shape, the code
   wins: leave the member out and edit the plan in the same commit, with
   the reason, because a plan left contradicting its code is read as the
   specification by the next session. A branch that degrades on error
   makes the degraded state visible from outside (a span attribute, a
   counter, a log line), or the feature behind it can be dead with every
   test passing; a swallow that is safe only because the callee cannot
   fail says so at the call site.
3. Write or extend the tests the unit names. When the unit changes behavior,
   write the failing test first and watch it fail. A test for a
   concurrency, ordering, or security property is evidence only once it
   has been watched failing against the defect: revert the fix or feed a
   wrong implementation, and undo from a copy taken first
   (`cp <path> "$TMPDIR/<name>.orig"`), never with `git checkout` or
   `git restore`, which on an unfinished unit discard everything since the
   last commit. Assert what the fix causes, not what it prevents: an
   absence has more than one source, and a cancelled context supplies it
   as readily as the fix. When the test depends on the system being in a
   state, assert the state before the outcome. Say per test whether it is
   evidence for this change or a guard for later code. A self-authored
   fake peer produces only the sequence the client was coded to expect:
   seed it with leftover state ahead of the call under test (a banner, a
   retained buffer, an out-of-order message). A fixture that builds a wire
   message passes `protovalidate.Validate` in the test, so a message the
   wire would refuse fails where it is written. The exported `Config` of a
   module under `src/modules/` (say `src/modules/localnet/access`) gets one
   test in a package outside that directory: only that
   package shows a host can name every field's type and construct or
   implement a value. When correctness rests on an invariant another
   component holds, the comment and a test go on the holding side, at the
   branch that carries it, proved by the same reversal: revert each
   candidate and keep the one a test notices. A unit without a test needs
   a stated reason in the plan.
4. Update the package README, convention doc, solution citations, and any
   test or benchmark name the unit made false, in the same unit.
5. Run the focused checks (`go test -race ./<pkg>/...`, `buf lint`). A test
   run whose output may carry diagnostics goes to a file, grepped after
   (`go test ... > "$TMPDIR/run.log" 2>&1; grep -E '^(FAIL|--- FAIL)' "$TMPDIR/run.log"`),
   never through a filter that drops what it does not match. A unit that
   adds or removes a name in a repository-wide namespace (an error code, a
   telemetry scope, an event or metric name, a bus subject, a bucket)
   greps the tree for that name, since no per-package gate sees two
   owners. Commit the unit, then run the verifier for the unit's paths,
   sandbox disabled, in the background while you read on, as the last
   command of its invocation (a trailing `echo` or `tail` reports its own
   exit code as the gate's):

   ```bash
   .claude/skills/verify-change/scripts/verify-change.sh -- <paths>
   ```

   When a unit leaves the package red until the next unit lands, verify
   those units together and say so.

6. Once that run's last line reads `FlowSeer verification passed.`, quoted
   into the report verbatim, write the unit `passed` in the ledger with
   `git rev-parse HEAD` and that run's `verified_at` from the receipt,
   move `resume` to the next unit, and fill `note` only when the unit
   produced a decision or pitfall the next unit needs, in one line. A unit
   that cannot land is `blocked` with the reason in `note`. In Orca, set
   the worktree comment to the unit that landed.

Plan labels stay in the plan: never write `U2`, `R4`, or a plan filename into
code, comments, or commit messages.

When a unit needs a decision the plan does not make, write the question and
your recommended answer into the plan's Open questions. Ask the user when the
answer changes other units; otherwise take the recommendation, record it in
Decisions, and continue.

Delegate a bounded read-only question as `delegate` describes when it would
cost more than a few file reads.

### Units in workers

A phase plan (one with a `parent:` field), or a request to run units in
parallel, dispatches units to workers: load `references/workers.md` before
the first such unit.

## 3. Finish

The order here matters: first record the outcome in the plan and amend it
into the last unit's commit, rewrite that unit's `commit` in the ledger
with the new hash, and only then run the verifier, as the last action of
the task, sandbox disabled. A run before the last edit is evidence about a
tree that no longer exists, and `close` refuses a receipt older than the
last commit. Pass the task's paths explicitly when the worktree holds
unrelated changes; otherwise use `--base master`, or `--base HEAD` for
uncommitted work. When
`$(git rev-parse --git-dir)/flowseer-verification-dirty` holds the
`<Bash mutation; verify with --full>` line, run `--full` instead; nothing
else clears it. Quote the run's last line into the report; a line other
than `FlowSeer verification passed.` blocks the report.

Recording the outcome in the plan, read from the ledger, in the same commit
as the last unit leaves the ledger in place for `close` to gate on. Set
`status: implemented` and add `> Implemented.` under the title when every
unit landed; otherwise `status: partially-implemented` and
`> Partially implemented: <units>.` with the reason. When the plan carries
a `parent:` field, fill this phase's `Landed:` line in the parent, and set
the parent to `implemented` when this was its last phase, in the same
commit. A request that skipped the plan has nowhere to record this. In Orca the card entry below is then
the only implementation signal `close` reads, so write it even for a small
change; outside Orca the commit message carries the outcome and `close`
asks the user.

In Orca, mark the card for `close`, naming the plan path, or the request in
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

Report, outcome first: units done, commands run with results, deviations from
the plan, residual risk. Do not run a review; the user asks for `review`. A
correction to this procedure is logged as `compound`, Observe describes.
