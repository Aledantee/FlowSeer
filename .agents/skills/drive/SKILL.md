---
name: drive
description: Takes a FlowSeer plan to ready-to-land without the user starting each step. Runs plan (when re-planning is needed), implement, review with its fix loop, and compound in worker sessions, merging and verifying between them; drives a parent plan's phases in dependency order. Parks a plan that needs the user, continues with independent ones, resumes from the plan files. Use to drive a plan, when `next` offers it, or to continue a drive. Not for picking work (`next`), planless work, or landing on main (`land`).
argument-hint: "[plan or parent plan path]"
---

# Drive a FlowSeer plan

This session coordinates: it reads the state, dispatches, merges, verifies,
and records. Every stage that reads or writes code runs in a worker session
of its own, as `delegate` describes, from a fresh context and the plan file.
All state is in files other skills keep (the parent's `Landed:` lines; each
plan's `status`, `review`, and `compound` fields and Open questions; the
branches), so running `drive` again continues a drive.

## 1. Scope and preconditions

The argument names a plan. Without one, run `next` first and drive what the
user picks there.

```bash
python3 .claude/skills/drive/scripts/plan-state.py <plan>
```

- A parent plan: it prints, per phase, the next stage (`plan`, `implement`,
  `review`, `compound`), or done on this branch, on `main`, or waiting for
  other phases; its last line names the phases that can run now. Run step 2
  once per phase, in step 3's order.
- "not a parent plan": drive it by step 2, its stages read off its own
  frontmatter.
- With `status` as the request, report that output and stop.

Before the first dispatch:

- This session is in a worktree on its own branch with a clean tree
  (`git status --porcelain` empty). Every stage merges into this branch, so
  a later phase's worktree holds the earlier ones.
- Do not drive a line flagged `elsewhere:<branch>`; name the branch.
- Load `delegate`, discover the host, and read the quota with its
  `pool-usage.sh`. State all four rows (`claude`, `codex`, `google`,
  `synthetic`) before the first dispatch; a quota line that names two pools
  routed on two pools. No usable pool means no drive: say which window is
  exhausted and when it resets.

## 2. Drive one plan

Run the stages in order from the first whose "done when" the files do not
already show. Each stage is one worker in a child worktree branched from
this branch's `HEAD`, started with `orca-worker.sh start` with the table's
`--role`, `--plan <path>`, and the stage name as `--unit`.

| Stage | Applies when | Worker runs | Role | Done when |
| --- | --- | --- | --- | --- |
| re-plan | `artifact_readiness: needs-decisions` | `plan` on this plan, against this tree | `execute` | the plan reads `implementation-ready` |
| implement | `status` is not `implemented` | `implement` on the plan | `execute`, or `execute-sensitive` by path | the plan reads `implemented`, a phase's `Landed:` line in its parent carries the range, and every unit in the worker's ledger is `passed` |
| review | `review` is absent or not an accept | `review` of the worker's branch against `<base>`, with the plan path, and step 6's fix loop | `review-seam` | the plan's `review` field reads `accept` or `accept after fixes` |
| compound | `compound` is absent | `compound` on the plan | `execute` | the plan's `compound` field is set |

`$base` is the commit a lane's branch forked from, read from the `start`
event it logged, with `$run` the `run` that `orca-worker.sh start` printed:

```bash
base=$(python3 -B -c 'import sys; sys.path.insert(0, ".claude/skills/delegate/scripts"); import runlog; print(next(e["base"] for e in runlog.read() if e.get("event") == "start" and e["run"] == sys.argv[1]))' "$run")
```

Load `references/review-stage.md` before starting a review stage worker.

The brief follows `delegate` and adds:

- The skill as the path of its `SKILL.md` (`.claude/skills/<stage>/SKILL.md`)
  and the plan path. Any agent CLI can follow that file, so take the lane
  from `delegate`'s resolution over all four pools; being a Claude Code
  skill is no reason to pick `claude`.
- A decision the skill would put to the user is a blocker to state and stop
  on.
- The worker budget: the stage's share of the cap from `delegate`'s Wave
  size, read just before the dispatch, less one for the stage worker, at
  least one. It may hold that many workers of its own (`implement`'s waves,
  `review`'s fix loop) and runs the rest in turn. With one phase in flight
  the share is the whole cap. The stage skill's own rules about workers
  stand; only the count is this drive's.

After each stage:

1. Check the worker's tree before its report, as `delegate` describes.
2. After the implement stage, read the worker's ledger before the child
   goes, since the merge does not bring it. Report every unit `passed` as
   the per-unit gate `land` would have read; a `blocked` unit parks the plan
   (step 4).
   `cat "$(git -C <child> rev-parse --git-dir)/flowseer-plan-status.json"`
3. Merge the worker's branch here. First check whether the worker merged it
   itself against its brief, with `$base` for this lane's `run` and
   `$branch` the `branch` from its `start` line. On `self-merged`, name it in
   the report and grade the lane with a `--note` saying so; items 4 to 7
   still run.
   `[ "$(git rev-list --count "$base..$branch")" -gt 0 ] && git merge-base --is-ancestor "$branch" HEAD && echo self-merged`
4. Run the verifier once on the union of the changed paths, sandbox
   disabled: `.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>`
5. Grade the lane before stopping it, `accepted` when merged as left,
   `amended` when the coordinator corrected the work, `--verify pass` when
   the verifier ran green:
   `.claude/skills/delegate/scripts/orca-worker.sh grade <slug> --outcome accepted|amended --verify pass|fail`
6. Remove the child worktree: `.claude/skills/delegate/scripts/orca-worker.sh stop <slug>`
7. Read the stage's "done when" off the merged files. A stage that reports
   success and leaves the field unset parks the plan with that as its
   question; do not run it again.

End a turn only while waiting on a started lane, at a parked question, or
when step 1 or step 5 stops the drive; a turn that ends right after
announcing the next stage leaves nothing to wake it. Run each stage once:
the skills' own caps (three verifier rounds on a unit, three review rounds
on a mechanism) decide when patching stops, and a parked question is what
sends a plan back.

## 3. Drive a parent's phases

Re-run the state command at the start of every round and after a compaction,
and take the order from its output. When it disagrees with the tree (a phase
reads `implemented` with an empty `Landed:`, or the parent reads
`implemented` while a phase still needs a stage), stop and report it: an
interrupted run left it, and guessing which side is right lands a phase
twice. A round takes the phases its last line names that are not parked, in
its order, and runs step 2 on each from the stage the command printed.

Load `references/concurrent-phases.md` when that last line names more than
one phase: it says which run at once, how they split the cap, and how to
set the parent's `status` when the last phases land together.

A landed phase fills its `Landed:` line, which records implementation only:
a phase named in another's `After:` releases that dependent once its review
and compound stages also read done. The parent has no stage; its `status`
follows its last phase, as `implement` writes it.

## 4. Park what needs the user, continue elsewhere

Park a plan when its worker stops on a decision that is the user's (a ruling
that changes other units, the wire, or an accepted record; a design question
in a re-plan; a direction record awaiting acceptance), on a `blocked` unit,
on a review that ends in `rework` after its loop, or on a change to a policy
surface. Load `references/parking.md` to park it, and again when the user
answers a parked question. A resumed drive reads the `Parked by drive:`
lines first and asks them before anything else.

## 5. Stop and ask

Stop when every plan in scope has its three fields set, when only parked or
waiting plans remain, when no pool is usable, or when the verifier is red on
a merged union. Landing stays with `land`, started by the user from the
question below, since a merge into `main` lands for every worktree; a
policy-surface change stays a parked question.

Report, outcome first: plans landed with their commit ranges, review
verdicts, and per-unit ledger result; plans parked with the question each
waits on; phases still waiting and on what; which phases ran at once and the
cap each round read; the pools used and left idle; the commands run with
results; the child worktrees that remain, with the reason.

Then ask the user (`AGENTS.md`, Agent behavior), in one call:

- every parked question, with its options and the recommendation;
- when everything in scope landed: run `land` now (recommended), or stop
  here;
- when plans remain and a question was answered: continue the drive now, or
  stop here.

Log a correction to this procedure with `compound`, as Observe describes.
