---
title: Plan state in a JSON file behind one script - Plan
type: refactor
date: 2026-10-05
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Plan state in a JSON file behind one script - Plan

## Goal

A plan's machine state lives in a committed JSON file beside the plan, one
script is its only writer, and every reader goes through one module. The
means: `plan_record.py` owns `<plan>.state.json`, performs each transition
as a command, and exports the tests `next`, `drive`, and the verifier share.
The plan's prose stays Markdown.

Stop condition: if a skill needs a state edit that no command can express
without free text, the command set is wrong and the plan returns to `plan`.

## Decisions

- State moves out of the plan's frontmatter and body into
  `docs/plans/<name>-plan.state.json`. Why: three scripts
  (`.agents/skills/next/scripts/plan-queue.py`,
  `.agents/skills/drive/scripts/plan-state.py`,
  `.agents/skills/verify-change/scripts/check-plan-status.py`) each parse
  the Markdown with their own regexes and their own test for a finished
  phase, and commit `02689de5` fixed two places where they disagreed.
  (decided by the user, 2026-10-05)
- A phase's prerequisites and landed range live in the phase's own state
  file. The parent's state lists its phases and nothing else about them.
  Why: the parent's `Landed:` lines are the one file concurrent phases all
  write (`.agents/skills/drive/references/concurrent-phases.md`), and a
  re-plan had to reset a second file
  (`.agents/skills/plan/references/replan-implemented.md`, step 5).
  (decided by the user, 2026-10-05)
- The verifier checks that each state file is a legal combination. No hook
  denies a hand-edit. Why: a check on the result holds whoever wrote the
  file, and a hook could not tell a repair from a mistake.
  (decided by the user, 2026-10-05)
- A parent's status is computed from its phases and never stored. Why:
  `implement` sets it by hand when the last phase lands
  (`.agents/skills/implement/references/outcome-records.md`), `drive` sets
  it when two phases land together, and `plan-queue.py` carries a `stale`
  group for the parent nobody set.
- A retired phase leaves one entry in the parent's state, written by the
  `retire` command, with its landed range. Why: `land` deletes a phase's
  files (`.agents/skills/land/references/retire-plan.md`), and a dependent
  phase still has to read that its prerequisite landed.
- The outcome note (`> Implemented. 6 units, <from> to <to>.`) becomes the
  `outcome` object in the state file. Why: `steer` greps the line for the
  phase-size audit and a re-plan has to delete it by hand.
- `plan_record.py` lives in `.agents/skills/plan/scripts/`. Why: the plan
  skill owns the artifact format. Its `check`, `finished`, and `on_main`
  decide what the verifier fails, so the file is an enforcement surface
  under the review discipline of `docs/agent-steering.md`, Change and
  review process, and U1's diff gets the same guardrail review as U4's.
  Naming it in `AGENTS.md`, Hard boundaries, is a separate policy request.
- The state files are written before the readers switch, and the Markdown
  keeps its old fields until the last unit. Why: the verifier runs its
  plan check on every run, so a tree whose readers want state files that
  do not exist yet fails every unit after it.
- The frontmatter keeps `title`, `type`, `date`, `execution`, `amends`, and
  `artifact_contract`, which becomes `flowseer-plan/v2`. Why: those
  describe the document and no script gates on them.
- No direction record. Why: the reason a skill is shaped as it is belongs
  in `docs/agent-steering.md`, which U5 updates.
- A parent stored as `superseded` or `abandoned` reads that way, and only
  a stored `planned` is computed from its phases. Why: computed without
  exception, `abandon` and `supersede` on a parent would change nothing a
  reader sees, and the parent could never retire.
  (decided by the user, 2026-10-05)

Ruled: `check` also rejects an implemented phase without `landed`, `superseded_by` set on any status but `superseded` or missing on that one, `after` on a plan without a parent, a phase its parent's `phases` does not list, a `phases` entry whose state names another parent, and a `retired` entry whose plan is still on disk. Why: Requirement 4 has `implemented` refuse a phase without a range, and a command refuses only what `check` rejects. The link rules let a reader follow `parent` and `phases` in either direction without testing both. Cost if wrong: one condition and one test case per rule, and U2's migration has to satisfy them.
Ruled: `replan` without `--needs-decisions` sets `readiness` to `implementation-ready`. Why: the flag is the only way the row of Requirement 4 names to leave a re-plan open. Cost if wrong: one line and the `replan` step of U5.
Ruled: `is` also takes `<field>!=<value>`, reads an unset field as `null`, and exits 2 when the plan has no state or the field does not exist. Why: `land`'s gate asks whether `compound` is set, which equality cannot express, and a `wait --until` must not read a missing file as a false test. Cost if wrong: `cmd_is` and the conditions U5 writes.
Ruled: `retire` runs `git rm -f` and stages the parent's state. Why: the state file usually carries an uncommitted command result when a plan retires, and a commit holding the delete without the parent's `retired` entry fails `check`. It refuses a plan whose Markdown has uncommitted changes, since the forced delete would drop them. Cost if wrong: a few lines of `transition` and `cmd_retire`.
Ruled: a command that writes two state files restores both when the second write or the `git rm` fails, and `replan` reads the ledger before it changes the state. Why: a phase its parent does not list, or a reset plan beside a ledger of passed units, is a fault no command repairs. Cost if wrong: the rollback in `transition` and two tests.

## Requirements

1. Every `docs/plans/*-plan.md` has a state file and every state file has
   a plan. Example: `plan_record.py check` on a tree with
   `a-plan.state.json` and no `a-plan.md` exits 1 and names the file.
2. A state file holds exactly these keys:

   ```json
   {
     "contract": "flowseer-plan-state/v1",
     "status": "planned",
     "readiness": "implementation-ready",
     "review": null,
     "review_rounds": 0,
     "compound": null,
     "outcome": null,
     "superseded_by": null,
     "parent": null,
     "after": [],
     "landed": null,
     "phases": [],
     "retired": []
   }
   ```

   `status` is `planned`, `partially-implemented`, `implemented`,
   `superseded`, or `abandoned`. `readiness` is `implementation-ready` or
   `needs-decisions`. `review` is null, `accept`, `accept after fixes`,
   `fixes needed`, or `rework`. `outcome` is null or
   `{"units": 6, "from": "<UTC time>", "to": "<UTC time>", "note": ""}`.
   `landed` is null or `{"first": "<sha>", "last": "<sha>"}`. `after` and
   `phases` hold plan paths. `retired` holds
   `{"plan": "<path>", "status": "<final status>", "landed": {...}}`
   objects, with `landed` null unless the phase was `implemented`. Example: a file with an
   extra key `Landed` fails `check` with the key named.
3. `check` rejects an illegal combination. Example: each of these exits 1
   with the rule named: `landed`
   set while `status` is not `implemented`, `landed` set with `parent`
   null, `phases` non-empty with `parent` set, an `after` path that is
   neither in the parent's `phases` nor in its `retired`, a parent whose
   stored `status` is `implemented` or `partially-implemented`, and, once U6 lands, a frontmatter that still carries `status`,
   `artifact_readiness`, `review`, `review_rounds`, `compound`,
   `superseded_by`, or `parent`. A `review` beside `status: planned` is
   legal, since `review` covers a plan reviewed mid-flight
   (`.agents/skills/review/SKILL.md`, step 1).
4. Each transition is one command that leaves a legal file:

   | Command | Effect |
   | --- | --- |
   | `init <plan> [--needs-decisions] [--parent <p> [--after <q>...]]` | writes the file, and adds the plan to the parent's `phases` |
   | `ready <plan>` | `readiness` to `implementation-ready` |
   | `after <plan> <path>...` | replaces a phase's `after`, for a re-plan that finds a changed prerequisite |
   | `implemented <plan> --units <n> --from <t> --to <t> [--landed <first>..<last>]` | `status`, `outcome`, and for a phase `landed` |
   | `partial <plan> --units <n> --from <t> --to <t> --note <units left and why>` | `status: partially-implemented` and the `outcome` with its note |
   | `review <plan> <verdict> [--rounds <n>]` | `review`, and `review_rounds` when given. Omitted, the stored count stays |
   | `compound <plan> <outcome>` | `compound` |
   | `replan <plan> [--needs-decisions]` | `status: planned`, clears `review`, `compound`, `outcome`, `landed`, sets `review_rounds` to 0, and deletes this worktree's ledger when it names the plan |
   | `supersede <plan> --by <path>` | `status` and `superseded_by` |
   | `abandon <plan>` | `status` |
   | `retire <plan>` | removes the plan and its state with `git rm`, moves a phase from the parent's `phases` to its `retired` with its final status and range, and prints the outcome, review, and compound lines for the commit body |
   | `show <plan> [--json]` | prints the state, a parent's with its computed status |
   | `is <plan> <field>=<value>` | exits 0 when the field holds the value and 1 otherwise, for `drive`'s `wait --until` and `land`'s gate |

   Example: `implemented` on a phase without `--landed` exits 1. With
   `--landed a..b` where `b` is no ancestor of `HEAD` it exits 1.
5. A parent's status is computed: `implemented` when `phases` is empty
   and `retired` holds an entry with a landed range. Otherwise `planned`.
   Example: a parent whose last phase reads `implemented` and is still on
   disk prints `status: planned`, and a parent whose only phase retired
   as `abandoned` prints `status: planned`.
   A stored `superseded` or `abandoned` stands. Example: an abandoned
   parent with a retired phase that carries a range prints
   `status: abandoned`.
6. One exported test decides whether a phase frees its dependents: its
   `retired` entry carries a landed range, or its `landed.last` is an
   ancestor of `main`, or it reads `implemented` and
   `implementation-ready` with an accepted review, a compound outcome,
   and every phase in its own `after` finished. Example: a phase at
   `implemented` with `review: fixes needed` and a landed range not on
   `main` is not finished, and `plan-queue.py` prints its dependent as
   `waiting`. A phase retired as `abandoned` does not free its dependent.
7. `plan-queue.py`, `plan-state.py`, and `check-plan-status.py` hold no
   regular expression over plan frontmatter, `After:`, or `Landed:` lines.
   Example: `grep -nE "Landed|artifact_readiness" plan-queue.py` matches
   only prose in the docstring.
8. The queue groups and drive stages keep their meaning, less `stale`,
   which a computed parent status makes unreachable. Example: an
   implemented plan with `review: rework` reads `replan` from the queue and
   `plan` from the state command.
9. The verifier runs `check` on every run that names a path under
   `docs/plans/`, and a run naming only a `.state.json` path selects that
   gate. Example: `verify-change.sh -- docs/plans/x-plan.state.json` with
   an illegal file exits non-zero with the rule in its output.
10. After the migration every plan on disk passes `check`, and the queue
    prints the same group for each plan as before it, less `stale`.
    Example: the two `plan-queue.py --json` outputs, reduced to path and
    group, are equal.
11. A plan counts as changed on a branch when its `.md` or its
    `.state.json` changed. Example: a plan written on `main` and marked
    `implemented` on a branch, where only the state file differs, reads
    `unchecked` and not `retire`, and another branch that ran a command on
    it flags it `elsewhere`.
12. The landed-twice test holds after a retire. Example: `main` holds a
    phase only as an entry in its parent's `retired`, a worktree forked
    earlier still holds the phase at `planned` with a ledger naming it,
    and `check-plan-status.py` exits 1.

## Out of scope

- A plan's Units stay Markdown. `ledger.py`,
  `.agents/skills/implement/scripts/plan-deviations.py`, and the queue's
  unit count keep reading `### U1.` headings and `Files:` lines.
- The worktree ledger `flowseer-plan-status.json` and the
  `flowseer-checkpoints` file for planless work keep their shape.
- `## Review gaps`, `Parked by drive:` lines, and Open questions stay
  prose in the plan.
- `plan_record.py` reads state files written by this repository's agents
  and people. That author is trusted, and `check` guards against mistakes,
  not hostile input.

## Units

### U1. The state module and its commands
Files: `.agents/skills/plan/scripts/plan_record.py`, `.agents/skills/plan/scripts/test_plan_record.py`
After: none
Change: `plan_record.py` reads and writes state files atomically (write to
a temporary file in the same directory, then rename, as `ledger.py` does),
implements the commands of Requirement 4 and `check [<paths>]`, and
exports `load(plan)`, `status(plan)` with the computed parent status,
`finished(phase)`, `sent_back(state)`, `on_main(sha)`, and
`plan_of(path)`, which maps a `.md` or `.state.json` path to its plan.
`load` raises a named error for a missing state file. Every command
refuses a transition whose result `check` would reject and prints the
rule. The frontmatter rule of Requirement 3 is not in `check` yet. The
file is executable. The worker stops at the diff for the user's guardrail
review before the commit.
Tests: `test_plan_record.py` builds a scratch repository as
`.agents/skills/next/scripts/test_plan_queue.py` does. One case per row of
Requirement 4, asserting the file after the command, with `review`
keeping the stored rounds when `--rounds` is omitted. One case per illegal
combination of Requirement 3. Requirement 5 over three parents (last phase
implemented on disk, one retired with a range, one retired as abandoned).
Requirement 6 over the five prerequisite states
`test_dependent_phase_waits_until_its_prerequisite_is_finished` lists,
plus a retired phase with a range, one retired as abandoned, and a range
on `main`. `replan` deletes a ledger naming the plan and leaves one naming
another plan.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills/plan/scripts/plan_record.py .agents/skills/plan/scripts/test_plan_record.py`

### U2. Write a state file beside every plan
Files: `docs/plans/` (a new `*-plan.state.json` beside each `*-plan.md`)
After: U1
Change: a one-off script, kept in the scratchpad and not committed, reads
each plan with the parsers `plan-queue.py` holds at this unit's base
commit and writes its state file: the frontmatter fields, each parent
unit's `After:` and `Landed:` lines as the phases' `after` and `landed`,
a parent's on-disk phases under `phases`, its landed and deleted phases
under `retired` with `status: implemented`, and a `> Implemented.` line as
`outcome`. The Markdown is not edited. Before writing, it saves
`plan-queue.py --json` reduced to path and group for U3's comparison.
This plan's own state file is written with the rest.
Tests: `plan_record.py check` passes on the tree. A plan that reads
`planned` with a `review` field keeps both.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans`

### U3. The queue and the drive state read the module
Files: `.agents/skills/next/scripts/plan-queue.py`, `.agents/skills/next/scripts/test_plan_queue.py`, `.agents/skills/drive/scripts/plan-state.py`, `.agents/skills/drive/scripts/test_plan_state.py`
After: U2
Change: both scripts import `plan_record.py` by path with `importlib`, as
`test_plan_state.py` imports its subject, and take status, readiness,
review, compound, phases, prerequisites, and the finished test from it.
`frontmatter`, `FIELD`, `COMMIT_RANGE`, `finished_phases`, `sent_back`, and
the `Landed`/`After` branches of `units` leave `plan-queue.py`.
`frontmatter`, `phases`, `is_phase`, and `on_main` leave `plan-state.py`.
`changed_here` and `branches_touching_plans` pass each path through
`plan_of`. Three things leave because no command can produce their state:
the `stale` group, the queue's branch for a finished phase with an empty
range, and the stages `implement (Landed: empty in the parent)`,
`landed (no commit range)`, and `plan (phase plan missing)`.
`plan-queue.py` keeps the unit count from `### U` headings.
Tests: both test files build fixtures with `plan_record.py` commands.
These cases go with the branches above:
`test_finished_phase_with_empty_landed_line_needs_implement`,
`test_unreviewed_phase_with_empty_landed_line_needs_implement`, and in
`test_plan_state.py` the assertions on a `planned` phase with a range and
on a phase with no file. `test_sent_back_phase_on_this_branch_with_empty_landed_line_is_a_replan`
keeps its three states with the range the command requires.
`test_finished_parent_that_needs_decisions_still_retires` becomes a case
on the computed status. Every other case stays. New: Requirement 11's two
examples, and Requirement 10's comparison against the output U2 saved.
`test_plan_state.py` runs against a scratch repository, since `stage` no
longer takes a hand-built unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills/next/scripts .agents/skills/drive/scripts/plan-state.py .agents/skills/drive/scripts/test_plan_state.py`

### U4. The verifier reads the module and checks state files
Files: `.agents/skills/verify-change/scripts/check-plan-status.py`, `.agents/skills/verify-change/scripts/verify-change.sh`, `tools/hooks/tests/run.sh`
After: U2
Change: `check_phase_ancestry` takes the phase's `parent`, `after`, and
each prerequisite's `landed` from `plan_record.py`. The landed-twice test
reads two places on `main` with `git show`: the phase's own state, and
the `retired` list of the parent's state. `FIELD`, `LANDED_RANGE`,
`parent_units_text`, and `frontmatter_field` leave the file.
`verify-change.sh` runs `plan_record.py check` when a named path is under
`docs/plans/` and treats a `.state.json` path there as selecting that
gate. These three files are policy surfaces: the worker stops at the diff
for the user's guardrail review and commits only on that answer.
Tests: the phase fixture in `tools/hooks/tests/run.sh` (the five
`plan status check` assertions on a phase) is rebuilt on state files and
keeps its five outcomes, with "a Landed line written as prose" replaced by
"a landed object without `last`". Three assertions are added: Requirement
12's example, the verifier failing on an illegal state file, and a run
naming only a `.state.json` path not exiting as no gate selected.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills/verify-change/scripts/check-plan-status.py .agents/skills/verify-change/scripts/verify-change.sh tools/hooks/tests/run.sh`

### U5. The skills and docs name the commands
Files: `.agents/skills/plan/SKILL.md`, `.agents/skills/plan/references/phases.md`, `.agents/skills/plan/references/replan-phase.md`, `.agents/skills/plan/references/replan-implemented.md`, `.agents/skills/implement/SKILL.md`, `.agents/skills/implement/references/outcome-records.md`, `.agents/skills/implement/references/workers.md`, `.agents/skills/review/SKILL.md`, `.agents/skills/review/references/fix-loop.md`, `.agents/skills/compound/SKILL.md`, `.agents/skills/land/SKILL.md`, `.agents/skills/land/references/retire-plan.md`, `.agents/skills/land/references/missing-checkpoint.md`, `.agents/skills/drive/SKILL.md`, `.agents/skills/drive/references/concurrent-phases.md`, `.agents/skills/next/SKILL.md`, `.agents/skills/steer/SKILL.md`, `.agents/skills/verify-change/SKILL.md`, `docs/README.md`, `docs/agent-steering.md`
After: U2
Change: every step that reads or writes a state field names a command.
Writes: `plan`'s template drops the moved frontmatter keys, gains the
`init` step, and states the v2 contract. `phases.md` describes a parent
whose units name phase plans with no `After:` or `Landed:` line, and a
re-plan that changes a prerequisite runs `after`. `replan-implemented.md`
shrinks to the `replan` command, the Units list, and the `Waves:` rule.
`outcome-records.md` and `implement`'s Finish name `implemented` and
`partial` and drop the parent edit. `review` step 5, `fix-loop.md`, and
`missing-checkpoint.md` name `review` and `compound`. `retire-plan.md`
names `retire`, and its link-rewriting step leaves `*-plan.state.json`
files to that command. Reads: `drive`'s `wait --until` conditions and its
step 7, `land`'s gate and its search for plans a merge carried in,
`review`'s read of the round count, `implement`'s readiness gate, and
`workers.md`'s test for a phase use `is` or `show`. `land` picks this
work's plan from changed `.md` and `.state.json` paths.
`replan-phase.md` test 2 reads the phase's state and the parent's
`retired` on `main`. `concurrent-phases.md` drops the parent conflict
rule, the re-plan exception, and the closing paragraph on setting the
parent. `next` drops the `stale` row. `steer` step 5.4 reads unit counts
with `show --json` beside the retire commits. `docs/README.md` and
`docs/agent-steering.md` describe the state file and why it replaced the
frontmatter fields. Each embedded command is run once, verbatim, in a
scratch repository, since `retire` and `replan` delete files.
Tests: none of its own. This grep returns only lines that describe the
state file's JSON or the retire commit body:
`grep -rnE "Landed:|artifact_readiness|review_rounds|\^(status|review|compound):|\`parent:\`|status: implemented" .agents/skills --include='*.md'`
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills/plan .agents/skills/implement .agents/skills/review .agents/skills/compound .agents/skills/land .agents/skills/drive/SKILL.md .agents/skills/drive/references .agents/skills/next/SKILL.md .agents/skills/steer/SKILL.md .agents/skills/verify-change/SKILL.md docs/README.md docs/agent-steering.md`

### U6. Strip the moved fields from the Markdown
Files: `docs/plans/` (every `*-plan.md`), `.agents/skills/plan/scripts/plan_record.py`, `.agents/skills/plan/scripts/test_plan_record.py`
After: U3, U4, U5
Change: the moved frontmatter keys, each parent unit's `After:` and
`Landed:` lines, and each `> Implemented.` line leave the plans, and
`artifact_contract` reads `flowseer-plan/v2`. `check` gains the
frontmatter rule of Requirement 3. This plan's own file is stripped with
the rest. The `plan_record.py` diff stops for the guardrail review like
U1's.
Tests: a case in `test_plan_record.py` for a frontmatter that still
carries `status`. `plan_record.py check` passes on the tree, and the
queue output still equals the one U2 saved.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans .agents/skills/plan/scripts`

Waves: U1 | U2 | U3 U4 U5 | U6

## Verification

```bash
python3 .agents/skills/plan/scripts/test_plan_record.py
python3 .agents/skills/next/scripts/test_plan_queue.py
python3 .agents/skills/drive/scripts/test_plan_state.py
tools/hooks/tests/run.sh
python3 .claude/skills/plan/scripts/plan_record.py check
.claude/skills/verify-change/scripts/verify-change.sh --base main -- .agents/skills docs tools/hooks/tests/run.sh
```

## Definition of done

- The verifier is green for every changed path.
- The diffs of U1, U4, and U6 have each had the user's guardrail review
  before their commit.
- No skill tells an agent to hand-edit a state field.
- `docs/README.md` and `docs/agent-steering.md` describe the state file.
- This plan's state reads `implemented` with its outcome, set by the
  command.
- No plan labels in code.

## Open questions

- U1, U4, and U6 each stop at a diff for the guardrail review, so
  `implement` pauses three times and resumes on the user's answer. The
  ledger cannot mark a unit `passed` before its commit.
- Between U3 and U6 the state files are the truth and the Markdown still
  shows the old fields. A hand-edit to the Markdown in that window changes
  nothing a script reads. Nothing in the units covers it.
- Two worktrees that each run a command on the same plan produce a JSON
  merge conflict. With phase-owned state this needs two sessions on one
  phase, which `plan-queue.py` already flags as `elsewhere`. Unconfirmed
  that no other pair of writers exists: `drive`'s parking and `land`'s
  retire both write a parent's state (`phases`, `retired`) and can meet a
  phase `init` from a re-plan.
- Whether the frontmatter keeps a human-readable `status` mirror for
  someone reading the plan on its own. The plan says no, since a mirror is
  a second place to drift.

## Review gaps

Open follow-ups from the first review. None of them holds the verdict.

- .agents/skills/plan/scripts/plan_record.py:60: remove `parent` from `MOVED`; fails: a plan whose frontmatter carries `parent:` must fail `check`; class: gap
- .agents/skills/plan/scripts/plan_record.py:298: return early for `superseded` as well as `planned`; fails: a parent stored as `superseded` with a retired phase that carries a range must read `superseded`; class: gap
- .agents/skills/plan/scripts/plan_record.py:391: catch only `OSError` in the rollback; fails: a `retire` whose `git rm` fails must leave the parent's state unchanged; class: gap
- .agents/skills/next/scripts/plan-queue.py:150: `not review or state["compound"] is None` to `not review`; fails: an implemented plan on a branch with an accepted review and no compound outcome must read `unchecked`; class: gap
- .agents/skills/next/scripts/plan-queue.py:157: the partial-status and ledger condition to `False`; fails: a partially implemented plan, and a planned plan the ledger names, must read `in-progress`; class: gap
- .agents/skills/next/scripts/plan-queue.py:74: `plans_in` accepts only `.state.json` paths; fails: a finished plan with only a Markdown edit on a branch must read `land`; class: gap
- .agents/skills/next/scripts/plan-queue.py:140: `state["phases"] or state["retired"]` to `state["phases"]`; fails: a parent this branch changed whose phases are all retired must stay out of the queue; class: gap
- .agents/skills/drive/scripts/plan-state.py:56: remove the compound branch; fails: a phase with an accepted review and no compound outcome must read `compound`; class: gap
- .agents/skills/drive/scripts/plan-state.py:46: `if waiting and state["status"] != "implemented"`; fails: an implemented phase whose prerequisite is still planned must read `waits for`; class: gap
- .agents/skills/verify-change/scripts/check-plan-status.py:134: `landed = retired.get(prerequisite)` to `None`; fails: a phase whose prerequisite is retired with a range in this tree must pass; class: gap
- .agents/skills/next/scripts/plan-queue.py:27: "a parent whose phases have all landed reads implemented"; it reads so once no phase is on disk and a retired one carries a range; class: convention
- .agents/skills/next/scripts/plan-queue.py:88: `read_plans`, `describe`, and `queue` each have one caller, as does `retired_stage` in plan-state.py:27; class: convention
- .agents/skills/plan/scripts/plan_record.py:535: a ledger delete that fails after the state reset leaves the reset state beside the old ledger; class: hardening
- .agents/skills/review/SKILL.md:250: `<verdict>` and `<outcome>` are unquoted here and in fix-loop.md:201, missing-checkpoint.md:19, compound/SKILL.md:38, and docs/README.md:41, so `fixes needed` splits into two arguments; class: convention
- .agents/skills/implement/SKILL.md:182: a semicolon in prose, as in drive/SKILL.md:137, land/SKILL.md:38, next/SKILL.md:41 and :55, and docs/agent-steering.md:352 and :913; class: convention
