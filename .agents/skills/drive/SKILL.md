---
name: drive
description: Takes a FlowSeer plan to ready-to-land without the user starting each step. Runs plan, implement, review with its fix loop, and compound in worker sessions, drives a parent plan's phases in dependency order, and lands each finished phase. Use to drive a plan, when `next` offers it, or to continue a drive. Not for picking work (`next`), planless work, or landing a plan without phases (`land`).
argument-hint: "[plan or parent plan path]"
---

# Drive a FlowSeer plan

This session coordinates: it reads the state, dispatches, merges, verifies,
and records. Every stage that reads or writes code runs in a worker session
of its own, as `delegate` describes, from a fresh context and the plan file.
All plan state is in places other skills keep: each plan's state file, read
with `.claude/skills/plan/scripts/plan_record.py show <plan>` (a parent's
lists its phases and retired entries), the plan's Open questions, and the
branches. Running `drive` again therefore continues a drive.

## 1. Scope and preconditions

The argument names a plan. Without one, run `next` first and drive what the
user picks there.

```bash
python3 .claude/skills/drive/scripts/plan-state.py <plan>
```

- A parent plan: it prints, per phase, the next stage (`plan`, `implement`,
  `review`, `compound`, `land`), or done (landed here, fast-forward
  pending), on `main`, or waiting for other phases; its last line names the
  phases owed a land and, after `after`, the phases holding it, or else the
  phases that can run now. Run step 2 once
  per phase, in step 3's order.
- "not a parent plan": drive it by step 2, its stages read off its own state
  file.
- "not a plan file" for a parent that a `docs(plans): retire <slug>` commit
  deleted: its last phase landed; go to step 5.
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
`--role`, `--plan <path>`, and the stage name as `--unit`. A phase resumed
from a parked branch starts with `--base parked/<slug>` instead
(`references/parking.md`, Answer).

| Stage | Applies when | Worker runs | Role | Done when |
| --- | --- | --- | --- | --- |
| re-plan | `.claude/skills/plan/scripts/plan_record.py is <plan> readiness=needs-decisions`, or `.claude/skills/plan/scripts/plan_record.py show <plan>` reports status `implemented` and review `rework` | `plan` on this plan, against this tree | `plan` | `.claude/skills/plan/scripts/plan_record.py is <plan> readiness=implementation-ready` |
| implement | `.claude/skills/plan/scripts/plan_record.py is <plan> status!=implemented` | `implement` on the plan | `execute`, or `execute-sensitive` by path | `.claude/skills/plan/scripts/plan_record.py is <plan> status=implemented`, a phase's `landed` range is set, and every unit in the worker's ledger is `passed` |
| review | `.claude/skills/plan/scripts/plan_record.py show <plan>` reports no accepted review | `review` of the worker's branch against `<base>`, with the plan path, and step 6's fix loop | `review-seam` | `.claude/skills/plan/scripts/plan_record.py show <plan>` reports an accepted review |
| compound | `.claude/skills/plan/scripts/plan_record.py is <plan> compound=null` | `compound` on the plan | `execute` | `.claude/skills/plan/scripts/plan_record.py is <plan> compound!=null` |

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

Wait on the lane as `delegate` describes, with
`wait <slug> --until '<test>'`, the test being the state command for the
stage's "Done when":

- re-plan: `.claude/skills/plan/scripts/plan_record.py is <plan> readiness=implementation-ready`
- implement: `.claude/skills/plan/scripts/plan_record.py is <plan> status=implemented`
- review: `.claude/skills/plan/scripts/plan_record.py show <plan> --json | grep -q '"review": "accept'`
- compound: `.claude/skills/plan/scripts/plan_record.py is <plan> compound!=null`

On `done` without the stage's report on the screen, wait again without
`--until`.

After each stage:

1. Check the worker's tree before its report, as `delegate` describes, and
   run `.claude/skills/delegate/scripts/orca-worker.sh check <slug>` before
   the merge. A non-zero result stops this stage.
2. After the implement stage, read the worker's ledger before the child
   goes, since the merge does not bring it. Report every unit `passed` as
   the per-unit gate `land` would have read; a `blocked` unit parks the plan
   (step 4).
   `cat "$(git -C <child> rev-parse --git-dir)/flowseer-plan-status.json"`
3. Merge the worker's branch here. First check whether the worker merged it
   itself against its brief, with `$base` from this lane's `start` event and
   `$branch` the `branch` from that event. On `self-merged`, name it in the
   report and grade the lane with a `--note` saying so; items 4 to 7 still
   run.
   `[ "$(git rev-list --count "$base..$branch")" -gt 0 ] && git merge-base --is-ancestor "$branch" HEAD && echo self-merged`
   When the worker was not self-merged, after the merge commit exists,
   including a resolved conflict, run
   `python3 .claude/skills/land/scripts/merge-check.py ORIG_HEAD..HEAD`.
   A self-merged lane ran no coordinator `git merge`, so `ORIG_HEAD` may be
   stale. Run `python3 .claude/skills/land/scripts/merge-check.py "$base..HEAD"`
   for that case, using `$base` from the lane's `start` event or the base the
   item already records.
   A non-zero result stops the drive. Carry every `missing` block in the
   report.
4. Run the verifier once on the union of the changed paths, sandbox
   disabled: `.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>`
5. Grade the lane before stopping it, `accepted` when merged as left,
   `amended` when the coordinator corrected the work, `--verify pass` when
   the verifier ran green:
   `.claude/skills/delegate/scripts/orca-worker.sh grade <slug> --outcome accepted|amended --verify pass|fail`
6. Remove the child worktree: `.claude/skills/delegate/scripts/orca-worker.sh stop <slug>`
7. Read the stage's "done when" with the matching
   `.claude/skills/plan/scripts/plan_record.py is` or `show` command. A stage
   that reports success and leaves the field unset, or set to a value other
   than an accept, parks the plan with that as its question; do not run it
   again.

End a turn only while waiting on a started lane, with a started successor,
at a parked question, or when a failed lane check in step 1 or a failed
merge-check in step 3 stops the drive. A turn that ends right after
announcing the next stage leaves nothing to wake it. Run each stage once:
the skills' own caps (three verifier rounds on a unit, two reviewed rounds on
a review's scope) decide when patching stops, and a parked question is what
sends a plan back.

## 3. Drive a parent's phases

Re-run the state command at the start of every round and after a compaction,
and take the order from its output. When it exits 2, or
`.claude/skills/plan/scripts/plan_record.py check` names a fault, stop and
report it: a hand-edit or a merge left a state no command writes, and
guessing which side is right lands a phase twice. A round takes the phases its last line names that are not parked, in
its order, and runs step 2 on each from the stage the command printed.

Load `references/concurrent-phases.md` when that last line names more than
one phase that can run (not a `next: land` line): it says which run at once and how they split the cap. When a stage branch
conflicts with one already merged, read
`.claude/skills/implement/references/workers.md` whole: the branch goes back
to its worker.

A landed phase records its implementation range with
`.claude/skills/plan/scripts/plan_record.py implemented <phase> ... --landed <first>..<last>`. A phase in
another's `after` releases that dependent once its review and compound stages
also read done. The parent has no stage. Its status is computed from its phase
state.

When the state command's last line reads `next: land <phases>`, land them
before any new stage, as `land` describes for multi-phase plans. `land`
stops while a lane is live and gates every plan this branch carries past
`main`. From then on start no phase whose implement has not merged here.
The phases the line names after `after` have merged their implement: a
round takes those and runs their review and compound stages to done or
parks. Once the line reads `next: land <phases>` with no `after` and no
lane is live, run `land` once in this session on the phases owed. A phase that parks after its implement merged holds that
land until its question is answered, so the drive goes to step 5. A
fast-forward the harness refuses goes into step 5's
report and the drive continues. Any other stop in `land` stops the drive.
A pending fast-forward can outlive this session, so step 5 reads it off
the branch rather than from memory.

A Claude coordinator hands off to a successor between phases when four
conditions hold:

1. A phase's `land` has run.
2. The state command still names a phase that can run.
3. No lane of this session is live.
4. No plan in scope holds a `Parked by drive:` line.

Resolve the `plan` role lane through `delegate` at this hand-off. Call
`successor.sh`, sandbox disabled and `timeout: 180000`, with its CLI, model id,
and effort:

```bash
.claude/skills/drive/scripts/successor.sh <parent> --cli <cli> --model <id> --effort <level>
```

Exit handling:

- Exit 0: report in one line that the drive and its closing question continue
  in the named terminal, and end the turn.
- Exit 1: continue the drive in this session.
- Exit 2: stop the drive and report that a successor may be running with the
  printed handle.

When no further phase can run, go to step 5. A coordinator on another
runtime does not hand off.

## 4. Park what needs the user, continue elsewhere

Park a plan when its worker stops on a decision that is the user's (a ruling
that changes other units, the wire, or an accepted record; a design question
in a re-plan; a direction record awaiting acceptance), on a `blocked` unit,
on a review that ends in `rework` or `fixes needed` after its loop, or on a change to a policy
surface. A `rework` that names the round limit parks with the options
`review/references/fix-loop.md`, When to stop, lists: `plan`, dropping or
replacing the mechanism, or stopping. When both rounds were on one
mechanism, it parks with `plan` or stopping. No park offers a further
round. A review that accepts with follow-ups recorded does not park: its
report's follow-ups go into the drive's final report. Load `references/parking.md` to park it, and again when the user
answers a parked question. A resumed drive reads the `Parked by drive:`
lines first and asks them before anything else.

## 5. Stop and ask

Stop when every plan in scope has its three fields set, when only parked or
waiting plans remain, when no pool is usable, or when the verifier is red on
a merged union. Asking `drive` to run a parent is the user's answer for its
phase lands (step 3). A plan without phases lands only from the question
below, since a merge into `main` lands for every worktree; a policy-surface
change stays a parked question.

Report, outcome first: plans landed with their commit ranges, review
verdicts, and per-unit ledger result; plans parked with the question each
waits on; phases still waiting and on what; which phases ran at once and the
cap each round read; the pools used and left idle; the commands run with
results; the fast-forward `land` left for the person; the child worktrees
and `parked/` branches that remain, with the reason.

A phase land ends on its retire commit, so the newest one `main` lacks is
the commit to fast-forward to. Each later one descends from the earlier, so
that single command covers every phase landed here, whichever session ran
the land:

```bash
git log -1 --format=%H --grep '^docs(plans): retire' main..HEAD
```

Print the result in `land`'s fast-forward command (`land`, step 5).

Then ask the user (`AGENTS.md`, Agent behavior), in one call:

- every parked question, with its options and the recommendation;
- when a plan without phases has its three fields set: run `land` now
  (recommended), or stop here;
- when plans remain and a question was answered: continue the drive now, or
  stop here.

Log a correction to this procedure with `compound`, as Observe describes.
