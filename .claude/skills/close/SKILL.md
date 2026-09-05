---
name: close
description: Land finished FlowSeer work by merging the current worktree's branch into master and leaving the worktree and its Orca card ready for deletion. Use when asked to close, land, finish, or wrap up work after implement, review, and compound have run. Refuses when any of the three has not run or the review verdict is not accept; never removes the worktree or the Orca session.
argument-hint: "[plan path]"
---

# Close finished FlowSeer work

A merge into master lands for every other worktree, so this skill checks the
evidence the other skills left before it merges, and stops with the missing
step named when it is not there. It does not run `implement`, `review`, or
`compound` itself. It never removes the worktree: `orca worktree rm` kills the
terminal that issues it and discards the terminal history, so a person runs it
after reading the report.

## 1. Read the checkpoints

The branch is the current worktree's branch; the plan is the argument, or the
plan file the branch changed under `docs/plans/`.

| Signal | Where | Required value |
| --- | --- | --- |
| Implementation landed | plan frontmatter | `status: implemented` |
| Verifier ran after the last edit | `$(git rev-parse --git-dir)/flowseer-verification-receipt` present, `flowseer-verification-dirty` absent | `verified_at` newer than the last commit |
| Review verdict | Orca worktree comment, `review:` entry | `accept` or `accept after fixes` |
| Lesson captured or declined | Orca worktree comment, `compound:` entry | a solution path, `no lesson`, or `observation logged` |

In Orca (`ORCA_TERMINAL_HANDLE` set, `orca status --json` reachable
unsandboxed), read the card:

```bash
orca worktree show --worktree active --json   # .result.worktree.comment and .workspaceStatus
```

Outside Orca there is no card: ask the user for the review verdict and the
compound outcome in one question and record the answers in the report.

Any failed signal stops the skill here. Report which one and name the skill
to run next. Do not merge a partial implementation because the landed units
pass.

## 2. Check both trees

The worktree must have nothing left to land:

```bash
git status --porcelain            # empty
git log --oneline master..HEAD    # the commits about to land; at least one
git log --oneline HEAD..master    # commits the branch has not seen
```

Uncommitted changes that belong to the task are committed first, with the
plan's outcome in the same commit. Uncommitted changes that do not belong to
the task stop the skill; say what they are.

The primary checkout must be on `master` and clean; another session may be
mid-edit there:

```bash
primary=$(git worktree list --porcelain | head -1 | sed 's/^worktree //')
git -C "$primary" branch --show-current   # master
git -C "$primary" status --porcelain      # empty
```

Orca workers started for this task must be settled and released, so that
removing the worktree later kills nothing:

```bash
orca orchestration worker-list --json
orca terminal list --worktree active --json   # only this terminal remains
```

Release a settled worker with `worker-release`; a running worker stops the
skill.

## 3. Merge

Merge from the primary checkout, never by switching this worktree's branch:

```bash
git -C "$primary" merge --no-ff --no-edit <branch>
```

When `HEAD..master` was empty, the branch's verifier run covers the result.
When master had moved, run the verifier in the primary checkout over the
union:

```bash
(cd "$primary" && .claude/skills/verify-change/scripts/verify-change.sh --base <merge-base>)
```

Report a red verifier or a conflict as is, with the exact command and output.
Resolve a conflict only when the resolution is mechanical; otherwise
`git -C "$primary" merge --abort`, report the conflicting files, and stop.

Do not delete the branch; Orca deletes it with the worktree.

## 4. Mark the card

```bash
orca worktree set --worktree active --workspace-status completed \
  --comment "merged into master as <sha>" --json
```

## 5. Report

Outcome first: merged as `<sha>` into master, or stopped at the named signal
with the skill to run next. Then the commits landed, the commands run with
their results, whether the verifier ran again on master, and the one command
left for the user: `orca worktree rm --worktree active`. A correction to this
procedure is logged as `compound`, Observe describes.
