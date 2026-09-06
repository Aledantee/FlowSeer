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
Check the plan's `status` and the current tree: a plan may be partly landed,
and the tree wins over the plan about what exists. Record such a mismatch in
the plan's Open questions before touching code.

Read the `docs/architecture/` record for the area, the `CONCEPTS.md` entries
the plan uses, and the conventions for the files you will touch:
`docs/code-style.md` for Go, `docs/code-style-proto.md` and
`docs/conventions/protobuf.md` for schema, `docs/conventions/observability.md`
for instrumentation, `docs/doc-style.md` for prose.

Record `git status --porcelain` before the first edit. Changes outside the
task stay as they are; at Finish, report anything that appeared since.

## 2. Work each unit

Take the units in the plan's order, or in parallel where `After` allows and
the user asked for it (see Parallel units). For each:

1. Re-read the unit, then inspect the current source and tests for its files.
2. Make the smallest change that satisfies it. Search for an existing helper
   first; no abstraction with a single caller. Before calling a third-party
   API the tree does not already use, check its signature: `go doc` for Go,
   Context7 (`mcp__context7__query-docs` or the `ctx7` CLI) for the rest.
3. Write or extend the tests the unit names. When the unit changes behavior,
   write the failing test first and watch it fail. A unit without a test
   needs a stated reason in the plan.
4. Run the focused checks (`go test -race ./<pkg>/...`, `buf lint`), then the
   verifier for the unit's paths, sandbox disabled, in the background while
   you read on:

   ```bash
   .claude/skills/verify-change/scripts/verify-change.sh -- <paths>
   ```

   When a unit leaves the package red until the next unit lands, verify
   those units together and say so.

5. Update the package README, convention doc, solution citations, and any
   test or benchmark name the unit made false, in the same unit. In Orca,
   set the worktree comment to the unit that landed.

Plan labels stay in the plan: never write `U2`, `R4`, or a plan filename into
code, comments, or commit messages.

When a unit needs a decision the plan does not make, write the question and
your recommended answer into the plan's Open questions. Ask the user when the
answer changes other units; otherwise take the recommendation, record it in
Decisions, and continue.

Delegate a bounded read-only question as `delegate` describes when it would
cost more than a few file reads.

### Parallel units

Units marked `After: none`, or whose prerequisites have landed, may run at
once when the user asked for it: up to three workers, one unit each,
dispatched as `delegate` describes. The brief carries the plan path, the
unit's text, the conventions for its files, and the focused test command.
Workers do not run the verifier. After each report, merge the worker's branch
here, run the verifier on the union of changed paths, then release the
worker and remove its worktree as `delegate` describes, before the next
wave.

## 3. Finish

Run the verifier across everything the task changed, sandbox disabled. Pass
the task's paths explicitly when the worktree holds unrelated changes;
otherwise use `--base master`, or `--base HEAD` for uncommitted work.

Record the outcome in the plan, in the same commit as the last unit. Set
`status: implemented` and add `> Implemented.` under the title when every
unit landed; otherwise `status: partially-implemented` and
`> Partially implemented: <units>.` with the reason. A request that skipped
the plan has nowhere to record this. In Orca the card entry below is then
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
