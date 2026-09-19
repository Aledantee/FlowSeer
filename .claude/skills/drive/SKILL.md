---
name: drive
description: Take a FlowSeer plan from its file to ready-to-close without the user starting each step. For one plan, runs plan (when it needs re-planning), implement, review with its fix loop, and compound, each in a worker session of its own, merging and verifying between them. For a parent plan, drives each open phase that way in dependency order. Parks a plan that needs a decision from the user, continues with independent ones, and resumes from the plan files in a later session. Use when asked to drive a plan, when `next` offers it, or to continue a drive. Not for picking the work (`next`), for work without a plan, or for landing on main (`close`).
argument-hint: "[plan or parent plan path]"
---

# Drive a FlowSeer plan

This session coordinates and keeps its context small: it reads the queue,
dispatches, merges, verifies, and records. Every stage that reads or
writes code runs in a worker session of its own, as `delegate` describes,
so each stage starts from a fresh context and the plan file, not from what
the last stage left in this conversation.

All state is in files other skills already keep: the parent's `Landed:`
lines, each plan's `status`, `review`, and `compound` fields, each plan's
Open questions, and the branches. Nothing is remembered between sessions,
so running `drive` again continues a drive.

## 1. Scope and preconditions

The argument names a plan. Without one, run `next` first and drive what
the user picks there.

```bash
python3 .claude/skills/drive/scripts/plan-state.py <plan>
```

- For a parent plan it prints, per phase, the stage the phase needs next
  (`plan`, `implement`, `review`, `compound`), or that it is done on this
  branch, on `main`, or waiting for other phases, and its last line names
  the phases that can run now. Step 2 runs once per phase, as step 3
  orders them.
- A plan it calls "not a parent plan" has no phases and is driven as
  step 2 describes, its stages read off its own frontmatter.
- With `status` as the request, report that output and stop.

Before the first dispatch:

- This session is in a worktree on its own branch with a clean tree
  (`git status --porcelain` empty). Every stage merges into this branch,
  which is what lets a later phase's worktree hold the earlier ones.
- A line flagged `elsewhere:<branch>` is not driven; name the branch.
- Load `delegate`, discover the host, and read the quota with its
  `pool-usage.sh`. State all four rows (`claude`, `codex`, `google`,
  `synthetic`) before the first dispatch; a quota line that names two pools
  routed on two pools. No usable pool means no drive: say which window is
  exhausted and when it resets.

## 2. Drive one plan

The plan goes through these stages in order, starting at the first one
whose "done when" the files do not already show. Each stage is one worker
in a child worktree branched from this branch's `HEAD`.

| Stage | Applies when | Worker runs | Role | Done when |
| --- | --- | --- | --- | --- |
| re-plan | `artifact_readiness: needs-decisions` | `plan` on this plan, against this tree | `execute` | the plan reads `implementation-ready` |
| implement | `status` is not `implemented` | `implement` on the plan | `execute`, or `execute-sensitive` by path | the plan reads `implemented`, a phase's `Landed:` line in its parent carries the range, and every unit in the worker's ledger is `passed` |
| review | `review` is absent or not an accept | `review` of the worker's branch against `<base>`, with the plan path, and step 6's fix loop | `review-seam` | the plan's `review` field reads `accept` or `accept after fixes` |
| compound | `compound` is absent | `compound` on the plan | `execute` | the plan's `compound` field is set |

`<base>` is this branch's commit before the plan's implement stage merged,
so the review reads exactly that plan's change. Naming the branch and the
plan path is what makes `review` record its verdict in the plan: a bare
commit range reads to it as other work, which records nothing.

The brief follows `delegate`, and adds three things:

- The skill to run, as the path of its `SKILL.md`
  (`.claude/skills/<stage>/SKILL.md`), and the plan path. The worker reads
  that file and follows it, which every agent CLI can do, so the stage
  worker's lane comes from `delegate`'s resolution over all four pools
  like any other. "The skill is a Claude Code skill" is not a reason to
  pick `claude`: that choice leaves `google` and `synthetic` idle and spends the
  pool this session and every native subagent already draw on.
- A decision the skill would put to the user is a blocker to state and
  stop on.
- The worker budget. `delegate` allows three workers at once, and the
  stage worker is one of them, so it may hold two of its own, for
  `implement`'s waves or `review`'s fix loop, and runs the rest in turn.
  The stage skill's own rules about workers stand; only the count is this
  drive's.

After each stage:

1. Check the worker's tree before its report, as `delegate` describes.
2. After the implement stage, read the worker's ledger before the child
   goes, because it lives in the child's git directory and the merge does
   not bring it:

   ```bash
   cat "$(git -C <child> rev-parse --git-dir)/flowseer-plan-status.json"
   ```

   Every unit `passed` goes into the report as the per-unit gate `close`
   would have read. A `blocked` unit parks the plan (step 4).
3. Merge the worker's branch here.
4. Run the verifier once on the union of the changed paths, sandbox
   disabled.
5. Remove the child worktree.
6. Read the stage's "done when" off the merged files. A stage that
   reports success and leaves the field unset parks the plan with that
   as its question; it is not run again.

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

A stage runs once. The caps inside the skills (three verifier rounds on a
unit, three review rounds on a mechanism) already decide when patching
stops, so what sends a plan back is a parked question, not another try.

## 3. Drive a parent's phases

Re-run the state command at the start of every round, and after a
compaction, and take the order from its output; the tree moves under a
drive, and a list held in memory goes stale first. When its output
disagrees with the tree (a phase reads `implemented` with an empty
`Landed:`, or the parent reads `implemented` while a phase still needs a
stage), stop and report it: the run that left it was interrupted, and
guessing which side is right is how a phase lands twice. A round takes
the first phase its last line names that is not parked, and runs step 2
on it from the stage the command printed. One
phase is driven at a time, because its stage worker already spends the
worker budget. Phases with disjoint packages and no `After:` between them
can run at once in separate worktrees, one drive each, as
`plan/references/phases.md` describes; the parent's `Landed:` lines are
the only thing they share.

A phase that lands fills its `Landed:` line, which moves the phases
waiting on it into the next round. The parent itself has no stage: its
`status` follows its last phase, as `implement` writes it.

## 4. Park what needs the user, continue elsewhere

A plan is parked when its worker stops on a decision that is the user's
(a ruling that changes other units, the wire, or an accepted record; a
design question in a re-plan; a direction record awaiting acceptance), on
a `blocked` unit, on a review that ends in `rework` after its loop, or on
a change to a policy surface.

Park it in the file. Append to the plan's Open questions, commit, and run
the verifier on the plan path, so the receipt post-dates the commit:

```markdown
- Parked by drive: <question>. Options: <a> | <b>. Recommended: <a>,
  because <reason>.
```

Merge what the worker landed before it stopped only when the verifier
passes on it. A parked phase and every phase whose `After:` reaches it
leave the round; independent phases continue. A plan without phases that
parks ends the drive at step 5.

A drive resumed later reads the `Parked by drive:` lines first and asks
them before anything else.

## 5. Stop and ask

The drive stops when every plan in scope has its three fields set, when
only parked or waiting plans remain, when no pool is usable, or when the
verifier is red on a merged union. Landing stays with `close`, which the
user starts from the question below, because a merge into `main` lands
for every other worktree; a policy-surface change stays a parked
question for the same reason `steer` stages one.

Report, outcome first: plans landed with their commit ranges, review
verdicts, and per-unit ledger result, plans parked with the question each
waits on, phases still waiting and on what, the pools used and left idle,
the commands run with results, and the child worktrees that remain with
the reason.

Then ask the user (`AGENTS.md`, Agent behavior), in one call:

- every parked question, with its options and the recommendation;
- when everything in scope landed: run `close` now (recommended), or stop
  here;
- when plans remain and a question was answered: continue the drive now,
  or stop here.

An answer to a parked question is written into the plan's Decisions as the
user's, the `Parked by drive:` line is removed in the same commit, the
verifier runs on the plan path, and the plan rejoins the next round.

A correction to this procedure is logged as `compound`, Observe describes.
