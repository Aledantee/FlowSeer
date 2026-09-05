---
name: implement
description: Implement a FlowSeer plan from docs/plans/ or a concrete, already-decided build request end to end, unit by unit, with the repository verifier run on every changed path. Use when asked to implement, build, execute, or work a plan. Not for open-ended bugs or for requests that still need design choices.
---

# Implement a FlowSeer plan

## Inputs

- A plan path under `docs/plans/`, or a request whose design is settled.
- The current worktree. On a protected branch in the primary checkout, enter a
  worktree first (`AGENTS.md`, Isolation).

## 1. Orient

Read the plan's Goal, Decisions, and Units. Skip the rest until a unit cites
it. Check the plan's outcome note and the current tree: a plan may be partly
landed already, and the tree wins over the plan when they disagree about what
exists. Record such a mismatch in the plan's Open questions before touching
code.

Read the `docs/architecture/` record for the area and the `CONCEPTS.md`
entries the plan uses, so that names and boundaries in the code match the
shared vocabulary.

Read the conventions that apply to the files you will touch:
`docs/code-style.md` for Go, `docs/code-style-proto.md` and
`docs/conventions/protobuf.md` for schema, `docs/conventions/observability.md`
for instrumentation, `docs/doc-style.md` for any prose.

Record `git status --porcelain` before the first edit. Changes that are not
part of the task stay as they are; at Finish, diff against that snapshot and
report anything that appeared in the meantime rather than reverting it.

## 2. Work each unit

Take the units in the plan's order. For each:

1. Inspect the current source and tests for the unit's files.
2. Make the smallest change that satisfies the unit. Search for an existing
   helper before writing one. No abstraction with a single caller.
3. Write or extend the tests the unit names. A unit without a test needs a
   stated reason in the plan.
4. Run focused checks (`go test -race ./<pkg>/...`, `buf lint`) and then the
   verifier for the unit's paths:

   ```bash
   .claude/skills/verify-change/scripts/verify-change.sh -- <paths>
   ```

   For Go paths the verifier runs the module's race tests and can take
   several minutes; start it in the background and continue reading. When a
   unit intentionally leaves the package red until the next unit lands (a
   renumbering, a signature change), verify those units together and say so.
   A gate that fails only because the sandbox denies a listener or Docker is
   rerun unsandboxed; report the exact command either way.

5. Update the package README, convention doc, and any solution citations
   that the unit invalidates, in the same unit. Renaming a test or benchmark
   whose name the change made false is in scope.

Plan labels stay in the plan. Never write `U2`, `R4`, or a plan filename into
code, comments, or commit messages. State the rule the code enforces instead.

When a unit turns out to need a decision the plan does not make, stop that
unit, write the question and your recommended answer into the plan's Open
questions, and ask the user if the answer changes other units. Otherwise pick
the recommendation, record it in Decisions, and continue.

Delegate a bounded read-only question to `repo-researcher` when it would
otherwise cost more than a few file reads. Do not delegate the edit itself
unless the units are independent and the user asked for parallel work.

## 3. Finish

Run the verifier across everything the task changed. Pass the task's paths
explicitly when the worktree holds unrelated changes; otherwise use the branch
point (`--base master`, or `--base HEAD` for uncommitted work):

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- <paths>
```

Add `> Implemented.` under the plan's title when every unit landed. If some
did not, add `> Partially implemented: <units>.` and say why.

Read the final diff against the plan's Definition of done and against
`docs/code-style.md`, Rules for coding agents. Remove process narration,
history references, and planning identifiers from comments.

Report: units done, commands run and their results, deviations from the plan,
and residual risk. Do not run a code review automatically; the user asks for
`review` when they want one.
