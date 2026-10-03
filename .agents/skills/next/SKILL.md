---
name: next
description: Decides what FlowSeer work comes next. Orders the open plans under docs/plans/ (finished work owed a land, then work in progress, then unaccepted reviews, then phases to re-plan, then ready plans, oldest first) against the ledger and unmerged branches, and recommends one with alternatives; with no plan open, compares GOALS.md with the tree and proposes what to plan. Use when asked what to do next, what is open, where things stand, or to pick up work. Not for the state of one plan being implemented; `implement` resumes that from its ledger.
argument-hint: "[area or plan path to narrow to]"
---

# Find the next FlowSeer work

Read and recommend; change nothing. End with one question, and let the
chosen skill do the work.

## 1. List the open plans

```bash
python3 .claude/skills/next/scripts/plan-queue.py
```

The script reads every plan's frontmatter, a parent plan's phase lines
(`After:`, `Landed:`), this worktree's ledger, and the unmerged branches
that change a plan. Take the queue from its output, never from plans read
earlier in the session or one by one. Its groups, in the order to take
them:

| Group | Meaning | Next skill |
| --- | --- | --- |
| `land` | `implemented` on this branch with an accepted `review`, a `compound` field, and no entry under `## Review gaps`, still on disk (a phase also has its `Landed:` range) | `land`, or `drive` on the parent for a phase |
| `in-progress` | `partially-implemented`, named by the ledger here, an unblocked phase of a parent with landed phases, or a finished phase whose `Landed:` line is empty | `implement` |
| `unchecked` | `implemented`, with a review verdict that is not an accept (`gaps open` included), or implemented on this branch with no `review` or `compound` field, or with an entry listed under `## Review gaps` on this branch or on `main`. The row prints the verdict, and the gap count beside an accept | `review` (step 6 for `rework` or `fixes needed`, the gap pass in `review`'s `references/fix-loop.md` for `gaps open` or a listed gap), or `compound` |
| `replan` | `artifact_readiness: needs-decisions`, prerequisites landed | `plan`, then `implement` |
| `ready` | `planned`, implementation-ready, nothing to wait for | `implement` |
| `waiting` | a prerequisite phase has not landed; the line names it | none yet |
| `stale` | a parent still `planned` whose phases have all landed | set its `status`, as `implement`'s Finish describes |
| `retire` | `implemented`, `superseded`, or `abandoned` on `main` and still on disk. An `implemented` plan with a verdict that is not an accept, or with a listed review gap, is `unchecked` instead | `land/references/retire-plan.md`, or a `steer` sweep for several |

A phase line names its parent, which is the path to hand `drive`. Take two
`in-progress` phases of one parent in the parent's unit order.

With an argument, keep the lines whose path or title matches it.

## 2. Check the top candidates against the tree

Take the first three lines that are not `waiting`, `stale`, or `retire`;
report the count of `retire` lines as clean-up owed. For each,
before recommending it:

- `elsewhere:<branch>` means another unmerged branch already changes that
  plan. Do not recommend it here; name the branch.
- Read the plan's Goal, its outcome note under the title, and its Open
  questions. For `partially-implemented`, the note names the units that
  remain and why they stopped; a unit that stopped on a blocker (lab
  hardware, a decision, a three-round `blocked`) is not resumable by
  `implement`, so say what unblocks it instead.
- The tree wins over the plan: check that the first remaining unit's
  files are in the state the plan expects. Report a plan whose work landed
  under another plan as stale, with the evidence; its fix is a `status`
  edit, not an implementation.

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
sentence, then every plan the script printed, `waiting` and `stale`
included and `retire` given only as step 2's count, then anything step 2
found stale or blocked. Name each plan, here
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
