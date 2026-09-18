---
name: drive
description: Drive a FlowSeer parent plan from docs/plans/ through every phase in order, re-planning each phase when its turn comes, then implementing, reviewing with fix rounds, and compounding it, all from one coordinating worktree with the workers in child worktrees. Use when asked to drive, run, or work a whole plan with phases, to resume or pause one, to report its status, or to say which plans are open. Not for a plan without phases, which goes to implement, and not for landing the result, which stays with close.
argument-hint: "[parent plan path] [status] [until <phase>]"
---

# Drive a parent plan through its phases

This skill owns the order of the stages. How a phase is planned, built,
reviewed, or captured is the text of `plan`, `implement`, `review`, and
`compound`; load each when its stage starts and follow it there, and send a
correction about a stage to the skill that owns it.

The order is read from the files: the parent's `Landed:` lines and each
phase plan's `artifact_readiness`, `status`, `review`, and `compound`
fields. A conversation that was compacted or resumed holds a stale copy.

```bash
.claude/skills/drive/scripts/plan-state.py <parent plan>
```

Each phase prints the stage it needs next (`plan`, `implement`, `review`,
`compound`), or that it is `done` on this branch, `on main`, or waiting for
other phases. The last line names the phases that can run now, `close`
when the branch is finished, or `nothing` when every phase is on `main`.
Without an argument it lists every open plan under `docs/plans/`, which
answers "what is not finished". The units inside a phase are the ledger's:
`ledger.py show`, as `implement` describes.

## 1. Orient

Run the state command. When the request was for status, report its output
with the ledger's and stop. A plan the command calls "not a parent plan"
goes to `implement`.

Read the parent's Goal, Decisions, and Units. Work from one coordinating
worktree for the whole parent, entered as `AGENTS.md`, Isolation describes.
This session stays the coordinator that `implement`'s
`references/workers.md` and `review`'s step 6 describe, so the ledger, the
verifier receipt, and the plan fields `close` reads all sit in this
worktree. The workers those skills dispatch, and the planner in step 2,
run in child worktrees of it as `delegate` describes, which keeps this
session's context to the state output and the workers' reports.

When the state disagrees with the tree (a phase reads `implemented` with an
empty `Landed:`, or the parent reads `implemented` while the state command
still names a phase to run), stop and report it. The run that left it was
interrupted, and guessing which side is right repeats the double landing
`docs/agent-steering.md` records.

After a compaction or on `resume`, run the state command again and trust it
over the summary.

## 2. Run one phase

Take the phases the last line names, in the parent's order. Phases that can
run at once and whose files are disjoint each get a coordinating worktree
and a session of their own, as `plan`'s `references/phases.md` allows; one
session drives one phase at a time.

Run the stage the state names, then run the state command again before the
next one. The stages, in order:

1. `plan`, when the phase reads `needs-decisions`. Dispatch a planner
   worker whose brief says to invoke the `plan` skill on the phase plan
   against the tree as it now stands and to leave it
   `implementation-ready`; the state command asks for this stage again
   until that field changes. Merge its branch. Read the re-planned
   Decisions before going on: a decision that changes another phase, the
   wire, or an accepted `docs/architecture/` record is a stop (step 3).
2. `implement`. Load `implement` and its `references/workers.md`. When
   `ledger.py show` names the previous phase's plan and the state command
   shows that phase past `implement`, replace the ledger with
   `ledger.py init <phase plan> <units> --force`: asking to drive the
   parent is the confirmation `implement` wants for that one replacement.
   A ledger naming any other plan is `implement`'s to ask about.
   `implement`'s Finish fills the parent's `Landed:` line.
3. `review`. Load `review` and scope it to the paths the phase plan's
   units name, with that phase plan as the plan whose frontmatter takes the
   verdict; `review` records a verdict for a plan's paths and records none
   for a commit range. Run its step 6 without being asked again, since
   driving a plan is the request to fix and re-review. The round cap in
   that step is a stop here.
4. `compound`. Load `compound` once per phase, with the review rounds'
   findings as the candidate lessons, and leave its outcome in the phase
   plan's `compound` field. `no lesson` is a normal outcome, and the stage
   still runs to record it.

After the last stage, remove the phase's child worktrees and branches as
`delegate` describes, and set the Orca card comment to the phase that
finished. With `until <phase>`, or when the user asked to pause after a
phase, stop once that phase reads `done`, with no lane running and the tree
clean.

## 3. Stop conditions

Stop and report, with the state output, when:

- a re-plan needs a decision the records do not settle, or makes one that
  changes another phase, the wire, or an accepted record;
- `implement` blocks a unit, or `review` reaches its round cap;
- the verifier is red after a merge and the cause is outside the phase;
- a stage would edit a policy surface (`AGENTS.md`, Hard boundaries);
- the state and the tree disagree (step 1).

Anything else is worked through: a quiet worker is checked as `delegate`
describes, a conflicting branch is re-based by its worker, and a ruling
that touches one unit is made and recorded as `implement`, Rulings says.

## 4. Hand off

When the last line reads `next: close`, run the verifier once over the
whole branch, sandbox disabled, as the last action:

```bash
.claude/skills/verify-change/scripts/verify-change.sh --base main
```

Landing is the user's call, so `close` is named in the report and left
unrun. `close` gates on the one plan it is given, which here is the last
phase's; the earlier phases' verdicts are gated only by the state command,
so the report lists every phase plan's `review` and `compound` values
beside the `close` invocation.

Report, outcome first: the state output, per phase the rulings, the review
rounds with what they found, and the compound outcome, then the stops that
were hit and how they were resolved, and the verifier's last line quoted. A
correction to this procedure is logged as `compound`, Observe describes.
