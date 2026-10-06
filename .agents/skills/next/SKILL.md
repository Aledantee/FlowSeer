---
name: next
description: Decides what FlowSeer work comes next. Orders the open plans under docs/plans/ against the ledger and unmerged branches and recommends one with alternatives; with no plan open, compares GOALS.md with the tree and proposes what to plan. Use when asked what to do next, what is open, where things stand, or to pick up work. Not for the state of one plan being implemented; `implement` resumes that from its ledger.
argument-hint: "[area or plan path to narrow to]"
---

# Find the next FlowSeer work

Read and recommend; change nothing. End with one question, and let the
chosen skill do the work.

## 1. List the open plans

```bash
python3 .claude/skills/next/scripts/plan-queue.py
```

The script reads every plan's state file through `plan_record.py`, the
units' `Files:` lines, the ledger of this worktree and of every other, and
the unmerged branches that change a plan. Take the
queue from its output, never from plans read
earlier in the session or one by one. Its groups, in the order to take
them:

| Group | Meaning | Next skill |
| --- | --- | --- |
| `land` | implemented on this branch with an accepted review and a compound outcome, still on disk | `land`, or `drive` on the parent for a phase |
| `in-progress` | partially implemented, named by this worktree's ledger, or an unblocked phase of a parent with landed phases | `implement` |
| `unchecked` | implemented with a review verdict that is neither an accept nor `rework`, or implemented on this branch with no review or no compound outcome | `review` (from step 1, with step 6 for `fixes needed`), or `compound` |
| `replan` | readiness `needs-decisions` with prerequisites finished, or an implemented plan whose review reads `rework` or that carries that readiness | `plan`, which ends with `plan_record.py ready` for a planned plan and `plan_record.py replan` for an implemented one, then `implement` |
| `ready` | planned, implementation-ready, every prerequisite finished | `implement` |
| `waiting` | a prerequisite phase is not finished: it has no landed range, or a range not on `main` while its review or compound outcome is open or one of its own prerequisites is not finished. The line names it | none yet |
| `retire` | implemented, superseded, or abandoned on `main` and still on disk | `land/references/retire-plan.md`, or a `steer` sweep for several |

`plan-queue.py` computes the group from the state files, the ledger, and the
branches. `.claude/skills/plan/scripts/plan_record.py show <plan>` prints the
fields behind one line.

The script also orders the lines, and that order is the one to take:

- Within a group a line flagged `harness` comes first. Such a plan changes
  the skills, hooks, or host-side scripts and no product code, and product
  work started before it runs on the old workflow.
- A line flagged `shares files with <plan>` is printed after that plan,
  even when its own group comes earlier, since the named plan is further
  along on a file both change.

A phase line names its parent, which is the path to hand `drive`. Take two
  `in-progress` phases of one parent in the parent's state order.

With an argument, keep the lines whose path or title matches it.

## 2. Check the top candidates against the tree

Take the first three lines that are not `waiting` or `retire`, in the
order printed, and
report the count of `retire` lines as clean-up owed. For each,
before recommending it:

- `elsewhere:<branch>` means another unmerged branch already changes that
  plan, or another worktree's ledger names it. Do not recommend it here;
  name the branch.
- Compare the candidate with the `other worktrees:` line, when the script
  printed one. A session
  that has not committed yet leaves only its branch name. When a branch
  reads as the candidate's subject (`worktree-review-plan-state` beside a
  plan about the plan state file), do not recommend the candidate; name
  the branch and say the match is by name.
- Read the plan's Goal and Open questions. Use
  `.claude/skills/plan/scripts/plan_record.py show <plan>` to read its status
  and outcome. For `partially-implemented`, the outcome names the units that
  remain and why they stopped. A unit that stopped on a blocker (lab
  hardware, a decision, a three-round `blocked`) is not resumable by
  `implement`, so say what unblocks it instead.
- The tree wins over the plan: use `.claude/skills/plan/scripts/plan_record.py show <plan>` and check that the first remaining unit's
  files are in the state the plan expects. Report a plan whose work landed
  under another plan as a mismatch, with the evidence. Resolve it with
  `.claude/skills/plan/scripts/plan_record.py supersede <plan> --by <path>`,
  not an implementation.

## 3. When no plan is open: read the goals

Only when step 1 prints no `land`, `in-progress`, `unchecked`, `replan`,
or `ready` line, or when the user asks what is missing.

For each goal in `GOALS.md`, find what stands behind it: the package, the
plan with its `status`, or the search that found nothing. Delegate the
reading to `repo-researcher` as `delegate` describes, one agent per goal
area, when it spans more than a few files.

A gap is a goal in that file with nothing behind it. Quote the goal with
its line, and cite the search that came back empty. A goal the file does
not state is not a gap, however natural it looks from the code: propose it
to the user as an addition to `GOALS.md`, and do not plan from it. When
the file is missing or empty, say so and ask whether to draft it; do not
reconstruct goals from the package layout.

Order the gaps by what they unblock: a gap other goals depend on first,
then the one with the most evidence of intent (a direction record, a
research dossier, a schema with no consumer).

## 4. Recommend and ask

Report, outcome first: the one recommendation with its reason in a
sentence, then every plan the script printed, `waiting` included and `retire`
given only as step 2's count, then anything step 2 found blocked. Name each
plan, here
and in the options, by its full title as the script prints it, followed by
its group and path, never by a slug, a date prefix, or a shortened title.
Then ask the user (`AGENTS.md`, Agent behavior) with these options, the
recommended first:

- The top candidate with its skill: "land `<plan>`", "implement
  `<plan>`", "re-plan `<phase>`", or "plan `<gap>`".
- The second candidate, the same way.
- "drive `<plan>`" for the top candidate. Recommend it over `implement`
  for a candidate the script marks `large` and for a phase whose parent
  has several phases open, naming the parent as the thing to drive.
- Stop here.

Work starts only on that answer, in the skill the option names. A plan of
more than one wave is implemented in a fresh session from the plan file,
so say so when the answer is `implement`.

A correction to this procedure is logged as `compound`, Observe describes.
