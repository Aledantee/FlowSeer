---
title: Drive Stages on One Branch per Plan - Plan
type: refactor
date: 2026-10-06
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Drive Stages on One Branch per Plan - Plan

## Goal

A `drive` runs every stage of one plan (re-plan, implement, review,
compound) in one child worktree on one branch, and merges that branch into
the coordinator's branch once, before `land`. The plan's state file records
the branch, and the state readers follow it, so a resumed drive finds a
plan whose work is not merged yet. The means are a `branch` field in
`plan_record.py`, a way for `orca-worker.sh` to start a new session in an
existing lane's worktree, and a rewrite of `drive` step 2.

Stop condition: if `orca terminal create` cannot open a second terminal in
a worktree whose first terminal was closed, the one-worktree mechanism does
not work and this plan returns to `plan`.

## Decisions

- One branch per plan, and for a parent plan one branch per phase. Phases
  still land one by one. Why: `AGENTS.md`, Agent behavior, and `land` keep
  their rule, and `main` keeps getting small batches. (decided by the user,
  2026-10-06)
- One worktree per plan with a new worker session per stage. No stage
  branch exists and nothing is merged after a stage. Why: this is the
  correction recorded in `docs/agent-observations.md`. A fresh child per
  stage that is fast-forwarded into the plan branch would still integrate
  after every stage. (decided by the user, 2026-10-06)
- The coordinator records the branch in its own checkout and commits it
  there, right after the first stage's `start` prints the branch name. Why:
  Orca prefixes the branch with the git user
  (`.agents/skills/delegate/scripts/orca-worker.sh`, header comment), so the
  name is known only after the worktree exists, and a resumed coordinator
  reads its own tree.
- `branch` sits between `superseded_by` and `parent` in the state file. Why:
  the coordinator changes that one line while the plan's workers change
  `status`, `review`, `compound`, `outcome`, and `landed` on the plan
  branch. Git reports a conflict for changes to adjacent lines, and neither
  neighbour changes during a drive.
- `show`, `is`, `plan-state.py`, and `plan-queue.py` read a
  plan's state from the recorded branch when that branch exists and is not
  an ancestor of `HEAD`, through `git show <branch>:<state file>`. Writing
  commands act on the checkout they run in. Why: the stage workers write
  the state on the plan branch, and until the one merge the coordinator's
  copy still reads `planned`. `wait --until` already runs its test in the
  worker's checkout (`orca-worker.sh`, the `cd "$path"` in `wait`), so only
  the coordinator's own reads change.
- `finished` reads the checkout only. Why: a dependent phase forks from the
  coordinator's `HEAD`, so its prerequisite frees it only once the
  prerequisite's branch is merged here. A followed read would start the
  dependent on a tree without that code, and
  `.agents/skills/verify-change/scripts/check-plan-status.py` would then
  fail it for a prerequisite with no landed range.
- `land` waits for every phase that has a recorded, unmerged branch. Why:
  `land` and `successor.sh` refuse while a child worktree remains
  (`.agents/skills/land/references/orca-cleanup.md`,
  `.agents/skills/drive/scripts/successor.sh`), and each such phase keeps
  its worktree until its last stage or a park.
- Every state file gains the key in the unit that adds it, and a file
  without it fails `check`. Why: `shape_faults` already treats a missing
  key as a fault, and `AGENTS.md` rules out a compatibility default. Cost:
  a branch that added a state file with the old script fails the verifier
  after it merges `main`, until `"branch": null` is added to that file. The
  `missing key 'branch'` message names the fix.
- A parent's own state never carries a branch. Why: its phases each carry
  one, and a parent has no stage.
- `replan` keeps the branch. Why: a re-plan stage runs in the plan's
  worktree, and the work it re-plans is on that branch.
- Without Orca, each stage still gets a fresh subagent worktree, and the
  plan branch is fast-forwarded to the stage's head. Why: the Agent tool's
  `isolation: worktree` creates a new worktree per call and takes no
  existing one (the tool's parameter list in this runtime has `isolation`
  and no path). Whether a later release adds one is unverified.
- The coordinator runs the verifier once, on the union, after the one
  merge. A stage lane is graded `--verify none`, and the last lane of a
  plan carries the merge's result. Why: `delegate` defines `--verify` as
  the first verifier run after the merge, and stage workers already run the
  verifier their skill names in the plan's worktree. Each lane is graded
  once, since `runlog.py` appends a second grade without a check.
- Ruled: `plan-state.py` leaves a phase whose unmerged recorded branch is
  under `parked/` out of the list after `after`, and applies the earlier
  rule (a phase at review or compound holds the land) only to a phase read
  from this checkout. Why: parking removes the phase's worktree and keeps
  its work off this branch (`.agents/skills/drive/references/parking.md`),
  so neither reason for `land` to wait applies, and a parked phase would
  otherwise hold every other phase's land until its question is answered.
  Cost if wrong: the `holding` expression in `plan-state.py`, its test
  `test_land_owed_does_not_wait_for_a_parked_phase`, and one sentence in
  `drive` step 3.
- Ruled: `plan-queue.py` and `plan-state.py` name a plan's branch only
  while its state is read from it, and a row's `branch` is null once the
  branch is merged or gone. Why: the flag says where the plan's work is,
  and after the merge it is in this checkout. Cost if wrong: the `branch`
  value of a row and one line of each script's output.

## Requirements

1. `plan_record.py branch <plan> <name>` stores the name, and
   `branch <plan> --clear` stores null. Example: after
   `branch docs/plans/x-plan.md alice/x-implement`, `show` prints
   `branch: alice/x-implement` and `is x-plan.md branch=alice/x-implement`
   exits 0.
2. `branch` refuses a name that is not a branch-name-safe string and
   refuses a parent. Example: `branch <parent> a` exits 1 with a message
   naming the phases, and `branch <plan> "a b"` exits 1.
3. `check` fails a state file without the key. Example: a file with every
   other key reports `missing key 'branch'`.
4. A read follows the recorded branch. Example: the checkout's file reads
   `planned` with `branch: work`, the file on `work` reads `implemented`,
   and `work` is not an ancestor of `HEAD`. `is <plan> status=implemented`
   exits 0, and `show` prints a line `read from: work`. After `work` is
   merged, the same command reads the checkout's file.
5. A recorded branch that does not exist is read from the checkout, and
   `show` says so. Example: `branch: gone` with no such ref prints
   `branch: gone (missing, read from this checkout)`.
6. `plan-state.py` and `plan-queue.py` report the stage and group from the
   followed state. Example: with requirement 4's fixture, `plan-state.py`
   prints `review` for the phase and `plan-queue.py` lists it `unchecked`
   with a `branch:work` flag.
7. `orca-worker.sh start --join <lane>` starts a new terminal in that
   lane's worktree under a new lane name and logs a `start` event whose
   `base` is the worktree's `HEAD` at that moment. It refuses when the
   joined lane is unknown, when a lane on that worktree still has a live
   terminal, or when the worktree is dirty. Example: after `stop a
   --keep-worktree`, `start --lane b --join a ...` prints a JSON line with
   `a`'s `path` and `branch` and a new `run`.
8. `orca-worker.sh stop <lane> --keep-worktree` closes the terminal, logs
   the `end` event, and leaves the worktree, the branch, and the lane's
   state file, whose `terminal` it empties. It keeps the grade, mid-turn,
   and dirty-tree refusals of `stop` and drops the merged-branch refusal.
   Example: after it, `git worktree list` still names the path and
   `status` prints the lane as kept. `stop b` (no flag) later removes the
   worktree once the branch is merged, with the state file of every lane
   that named it. `stop` on a kept lane skips the terminal close and the
   `end` event, which `--keep-worktree` already ran.
9. `drive` step 2 reads as one procedure: the first stage starts the
   plan's worktree and records the branch, each later stage joins it,
   nothing merges after a stage, and the branch merges once before `land`
   with `merge-check.py` and the verifier run then.

## Out of scope

- One branch for all phases of a parent. The first Decision rules it out.
- A change to `land`, `implement`, `review`, or `compound`. They run in
  whichever checkout holds the work.
- The `Branch` section of
  `docs/plans/2026-10-05-2216-refactor-uv-python-scripts-plan.md`. That
  drive collects every phase on one branch, as that section says, and
  finishes that way.
- `plan_record.py` reads state files this repository's own skills write.
  Their author is trusted, and the shape checks catch a hand-edit or a bad
  merge, not hostile input.

## Units

### U1. Branch field and followed reads in the plan state
Files: .agents/skills/plan/scripts/plan_record.py, .agents/skills/plan/scripts/test_plan_record.py, .agents/skills/plan/SKILL.md, docs/plans/
After: none
Change: `BLANK` holds `branch` between `superseded_by` and `parent`, null by
default. `shape_faults` accepts null or a string matching
`[A-Za-z0-9][A-Za-z0-9._/-]*`. `combination_faults` reports a branch on a
plan with `phases` or `retired`. `cmd_branch` writes the field through
`transition`. A new `followed(plan, root)` returns the state from
`git show <branch>:<state path>` when the stored branch resolves under
`refs/heads/` and is not an ancestor of `HEAD`, with the stored `branch`
kept, and the stored state otherwise. A followed file that fails
`shape_faults` raises `InvalidState` naming the branch. `cmd_show`,
`cmd_is` and `status` read through `followed`, and `finished` keeps
reading the checkout. `cmd_show`
prints `read from: <branch>` or the missing-branch note. Every
`docs/plans/*-plan.state.json` gains `"branch": null` at that position,
rewritten by loading and writing each file with the new key order. The
field list in `plan` step 3 names `branch` and the command.
Tests: `test_plan_record.py` gains cases for requirements 1 to 5: set and
clear, the refused name, the refused parent, the missing key, a read that
follows an unmerged branch, the same read after the merge, a missing
branch, a followed file with a shape fault, and a phase compounded on an
unmerged branch that `finished` still reports unfinished. Each uses the scratch
repository the existing cases build.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills/plan docs/plans`

### U2. Queue and stage readers follow the branch
Files: .agents/skills/next/scripts/plan-queue.py, .agents/skills/next/scripts/test_plan_queue.py, .agents/skills/drive/scripts/plan-state.py, .agents/skills/drive/scripts/test_plan_state.py, .agents/skills/next/SKILL.md
After: U1
Change: `plan-queue.py` reads each plan through `plan_record.followed`,
carries `branch` in its JSON rows, and prints `branch:<name>` among the
flags. `plan-state.py` reads through `followed` in `stage`, `report`, and
`open_plans`, and prints the branch after a phase's stage. Its `holding`
list, printed after `after` on the `next: land` line, names every phase
with a recorded branch that is not merged here, at any stage. A plan whose
recorded branch is checked out in another worktree is still flagged
`elsewhere` by the existing ledger and branch tests, and `next` step 2
says a `branch:` flag names where the plan's work is.
Tests: `test_plan_queue.py` and `test_plan_state.py` each gain the
requirement 6 fixture: a phase `planned` in the checkout and `implemented`
on an unmerged branch reads `unchecked` and `review`, and reads the
checkout again once the branch is merged. `test_plan_state.py` also
covers a phase owed a land beside a phase implementing on an unmerged
branch: the last line reads `next: land <first> after <second>`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills/next .agents/skills/drive/scripts`

### U3. Join and keep a lane's worktree
Files: .agents/skills/delegate/scripts/orca-worker.sh, .agents/skills/delegate/scripts/test_orca_worker.py, .agents/skills/delegate/SKILL.md, .agents/skills/delegate/references/orca.md, .agents/skills/delegate/references/no-orca.md
After: none
Change: `start` takes `--join <lane>`, exclusive with `--base`. It reads
the joined lane's `worktree`, `path`, and `branch` from its state file,
skips `orca worktree create`, and runs the rest of `start` unchanged, so
the new lane has its own terminal, `run`, and state file. A start that
fails after a join closes its terminal and never removes the worktree.
`stop` takes `--keep-worktree` as requirement 8 states. A kept lane's state
file stays with its `terminal` emptied and a `"kept": true` key, so `--join`
may name any kept lane of the worktree and `status` prints it as kept
instead of `unavailable-terminal`. A lane has a live terminal when a state
file naming the worktree is not kept. A `stop` without
the flag on any lane of the worktree removes it and every state file that
names it. The command list in
`delegate` gains both flags. `orca.md` records what was observed on a live
Orca for a second terminal in one worktree. `no-orca.md` says a stage
subagent starts with `git merge --ff-only <recorded branch>` and that the
coordinator moves the recorded branch to the stage's head with
`git branch -f` once the stage's checks pass.
Tests: `test_orca_worker.py` gains cases against its fake `orca`: a join
reuses the path and branch and logs a `start` with the worktree's `HEAD` as
`base`. A join of an unknown lane, of a lane with a live terminal, and of a
dirty worktree each exit 1 and create no terminal. `--join` with `--base`
exits 1. `stop --keep-worktree` on an unmerged, graded lane leaves the
worktree, and `status` prints the lane as kept. `stop` on a kept lane
closes no terminal and logs no second `end`. A failed joined start leaves the worktree. A final `stop` removes
the worktree and both state files. The fake cannot show that Orca opens a
second terminal in a used worktree, so the implementer runs one join by
hand on a live Orca and quotes the two `start` lines in the commit body.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills/delegate`

### U4. Drive runs a plan on its branch
Files: .agents/skills/drive/SKILL.md, .agents/skills/drive/references/review-stage.md, .agents/skills/drive/references/concurrent-phases.md, .agents/skills/drive/references/parking.md, docs/agent-steering.md
After: U1, U2, U3
Change: `drive` step 1 no longer says every stage merges into this branch.
Step 2 starts a plan's first stage as it does now, then runs
`plan_record.py branch <plan> <branch>` here and commits it. A later stage
starts with `--join <first lane>`. "After each stage" keeps the lane check,
the ledger read, and the "done when" read. Every lane but the plan's last
is graded `--verify none` and ended with `stop --keep-worktree`. The merge, the
`merge-check.py` run, the verifier on the union, and the removing `stop`
move to a new "After the last stage" list that runs once the plan's
compound stage is done, before `land`. That list grades the last lane once,
with the merge's verifier result, and takes `$base` for the self-merged
check and for `merge-check.py` from the first lane's `start` event, since a
joined lane's base covers its own stage only.
Step 3 says a phase owed a land is merged by that list first, and its rule
about phases "whose implement has not merged here" reads "whose implement
stage is not done", since no implement merges before its phase finishes.
The phases the state line names after `after` are those with an unmerged
recorded branch. Each runs its remaining stages and the "After the last
stage" list, or parks, before `land` runs.
`review-stage.md` takes
`<base>` from the first lane's `start` event, which is the fork point of
the plan branch. `concurrent-phases.md` says each phase has its own
worktree and that nothing merges until a phase finishes. `parking.md`
keeps its mechanism, since `land` and the successor hand-off refuse while
a child worktree remains: the plan's branch is copied to `parked/<slug>`
and the lane is stopped without the flag, `<slug>` being the slug of the
lane passed to `stop`. It adds two commands. On park,
`plan_record.py branch <plan> parked/<slug>` is run and committed here, so
reads follow the parked work. On resume, the new lane starts with
`--base parked/<slug>` as now, and its branch is recorded in place of the
parked one.
`docs/agent-steering.md`, `drive`, gains the reason.
Tests: none, the unit changes prose. Every command embedded in the edited
files is run once, verbatim, from a fresh shell, as
`docs/agent-steering.md`, Skills and delegated agents, requires.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills/drive docs/agent-steering.md`

Waves: U1 U3 | U2 | U4

## Verification

```bash
python3 -m unittest discover -s .agents/skills/plan/scripts -p 'test_*.py'
python3 -m unittest discover -s .agents/skills/next/scripts -p 'test_*.py'
python3 -m unittest discover -s .agents/skills/drive/scripts -p 'test_plan_state.py'
python3 -m unittest discover -s .agents/skills/delegate/scripts -p 'test_orca_worker.py'
.claude/skills/plan/scripts/plan_record.py check
```

One manual check on a live Orca, in U3: a join after `stop
--keep-worktree`.

## Definition of done

- [ ] The verifier is green for every changed path.
- [ ] `plan_record.py check` passes over every state file on disk.
- [ ] `drive`, `delegate`, `next`, and `plan` name the new field and flags
      in the change that adds them.
- [ ] The outcome is recorded with
      `.claude/skills/plan/scripts/plan_record.py implemented <plan> --units <n> --from <t> --to <t>`
      or `partial`.
- [ ] No plan labels in code.

## Open questions

- The uv scripts plan moves `plan_record.py`, `plan-queue.py`, and
  `plan-state.py` under `tools/scripts/skills/` in its phase 2 and ports
  `orca-worker.sh` in a later phase. This plan names today's paths. When a
  uv phase that moves one of these files has landed first, the unit applies
  to the moved file and its command spelling, and the implementer says so
  in the outcome note.

## Review gaps

Follow-ups from the review of the implemented plan. They do not hold the
verdict.

- .agents/skills/plan/scripts/plan_record.py:186: `shape_faults` accepts any string as `branch`; fails: `check` on a state file holding `"branch": "a b"`; class: gap
- .agents/skills/next/scripts/plan-queue.py:206: the `changed_here.update` line removed; fails: a phase implemented on its unmerged branch whose branch record is already on `main` is grouped `retire`, not `unchecked`; class: gap
- .agents/skills/drive/scripts/plan-state.py:112: `open_plans` reading with `load`; fails: a plan without phases implemented on its unmerged branch is listed `planned`; class: gap
- .agents/skills/drive/scripts/plan-state.py:95: `plan not in owed` removed; fails: a phase owed a land on an unmerged branch prints `next: land <phase> after <phase>`; class: gap
- .agents/skills/delegate/scripts/orca-worker.sh:579: the live-lane refusal of a removing `stop` disabled; fails: `stop <kept lane>` while a joined lane's terminal is live removes that lane's checkout; class: gap
- .agents/skills/delegate/scripts/orca-worker.sh:572: the already-kept refusal of `stop --keep-worktree` disabled; fails: a second `stop <lane> --keep-worktree` on a kept lane exits 0; class: gap
