---
name: next
description: Decide what FlowSeer work comes next. Reads the open plans under docs/plans/ with their phase dependencies, the status ledger, and the unmerged branches, puts them in order (work in progress first, then landed work whose review is not accepted, then phases ready to re-plan, then ready plans, oldest first), and recommends one with alternatives. When no plan is open, compares GOALS.md with the tree and proposes what to plan. Use when asked what to do next, what is open, where things stand, or to pick up work. Not for the state of one plan being implemented; `implement` resumes that from its ledger.
argument-hint: "[area or plan path to narrow to]"
---

# Find the next FlowSeer work

This skill reads and recommends; it changes nothing. It ends with one
question, and the chosen skill does the work.

## 1. List the open plans

```bash
python3 .claude/skills/next/scripts/plan-queue.py
```

The script reads every plan's frontmatter, a parent plan's phase lines
(`After:`, `Landed:`), this worktree's ledger, and the unmerged branches
that change a plan. Take the queue from its output: a session's memory of
plans it read earlier is stale by the next merge, and reading 75 plans one
by one costs more than the answer is worth. Its groups, in the order to
take them:

| Group | Meaning | Next skill |
| --- | --- | --- |
| `in-progress` | `partially-implemented`, named by the ledger here, or an unblocked phase of a parent with landed phases | `implement` |
| `unchecked` | `implemented`, with a review verdict that is not an accept, or implemented on this branch with no `review` or `compound` field | `review` (step 6 for `rework`), or `compound` |
| `replan` | `artifact_readiness: needs-decisions`, prerequisites landed | `plan`, then `implement` |
| `ready` | `planned`, implementation-ready, nothing to wait for | `implement` |
| `waiting` | a prerequisite phase has not landed; the line names it | none yet |
| `stale` | a parent still `planned` whose phases have all landed | set its `status`, as `implement`'s Finish describes |

A phase line names its parent, which is the path to hand `drive`. Two
`in-progress` phases of one parent are taken in the parent's unit order.

Finishing comes before starting: an `in-progress` plan outranks every
`ready` one, because work left half-landed ages, and each open plan is a
tree other plans were written against.

With an argument, keep the lines whose path or title matches it.

## 2. Check the top candidates against the tree

Take the first three lines that are not `waiting` or `stale`. For each,
before it is recommended:

- `elsewhere:<branch>` means another unmerged branch already changes that
  plan. Do not recommend it here; name the branch. Implementing it twice
  is a merge nobody can take.
- Read the plan's Goal, its outcome note under the title, and its Open
  questions. For `partially-implemented`, the note names the units that
  remain and why they stopped; a unit that stopped on a blocker (lab
  hardware, a decision, a three-round `blocked`) is not resumable by
  `implement`, and the recommendation says what unblocks it instead.
- The tree wins over the plan: check that the first remaining unit's
  files are in the state the plan expects. A plan whose work landed under
  another plan is reported as stale, with the evidence, and its fix is a
  `status` edit, not an implementation.

## 3. When no plan is open: read the goals

Only when step 1 prints no `in-progress`, `unchecked`, `replan`, or
`ready` line, or when the user asks what is missing.

`GOALS.md` is the one statement of what the project is for. For each
goal in it, find what stands behind it: the package, the plan with its
`status`, or the search that found nothing. Delegate the reading to
`repo-researcher` as `delegate` describes, one agent per goal area, when
it spans more than a few files.

A gap is a goal in that file with nothing behind it. Quote the goal with
its line, and cite the search that came back empty. A goal the file does
not state is not a gap, however natural it looks from the code: propose it
to the user as an addition to `GOALS.md`, and do not plan from it.
When the file is missing or empty, say so and ask whether to draft it;
do not reconstruct goals from the package layout.

Order the gaps by what they unblock: a gap other goals depend on first,
then the one with the most evidence of intent (a direction record, a
research dossier, a schema with no consumer).

## 4. Recommend and ask

Report, outcome first: the one recommendation with its reason in a
sentence, then the queue as the script printed it, trimmed to the open
lines, then anything step 2 found stale or blocked. Then ask the user
(`AGENTS.md`, Agent behavior) with these options, the recommended first:

- The top candidate with its skill: "implement `<plan>`", "re-plan
  `<phase>`", or "plan `<gap>`".
- The second candidate, the same way.
- "drive `<plan>`" for the top candidate: `drive` takes a plan through
  `implement`, `review`, and `compound` in worker sessions without the
  user starting each step, and takes a parent through its open phases.
  Recommend it over `implement` for a candidate the script marks `large`
  and for a phase whose parent has several phases open, naming the parent
  as the thing to drive.
- Stop here.

Work starts only on that answer, in the skill the option names. A plan of
more than one wave is implemented in a fresh session from the plan file,
so say so when the answer is `implement`.

A correction to this procedure is logged as `compound`, Observe describes.
