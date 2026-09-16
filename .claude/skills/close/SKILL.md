---
name: close
description: Land finished FlowSeer work by merging the current worktree's branch into main and leaving the worktree and its Orca card ready for deletion. Use when asked to close, land, finish, or wrap up work after implement, review, and compound have run. Refuses when any of the three has not run or the review verdict is not accept; removes merged child worktrees but never its own worktree or the Orca session.
argument-hint: "[plan path]"
---

# Close finished FlowSeer work

A merge into main lands for every other worktree, so this skill checks the
evidence the other skills left before it merges, and stops with the missing
step named when it is not there. It does not run `implement`, `review`, or
`compound` itself. It never removes its own worktree: `orca worktree rm` kills
the terminal that issues it and discards the terminal history, so a person
runs it after reading the report. Child worktrees the task created are
different: step 2 removes the merged ones, as `delegate` describes.

## 1. Read the checkpoints

The branch is the current worktree's branch; the plan is the argument, or
the plan under `docs/plans/` that records this work, found among the files
`git diff --name-only main...HEAD` lists. Work that skipped the plan under
`plan`'s skip rule has none. A plan the branch touched for another reason,
say a typo fix or a `superseded_by` field, is not this work's plan; say so
and treat the work as planless. When the changed set holds a phase plan
and the parent its `parent:` field names, the phase plan is this work's
plan; the parent is reported, not gated on. Planless work has no `status`
field to read, so its implementation signal is the card's `implemented:`
entry with the card status `in-review`, a non-empty `main..HEAD` range, and the
receipt signal. The report says the merge landed without a plan.

| Signal | Where | Required value |
| --- | --- | --- |
| Implementation landed | plan frontmatter, or the card when the work is planless | `status: implemented`, or a comment beginning `implemented:` with `.workspaceStatus` `in-review` and commits in `main..HEAD` |
| Verifier ran after the last edit | `$(git rev-parse --git-dir)/flowseer-verification-receipt` present, `flowseer-verification-dirty` absent | `verified_at` newer than the last commit |
| Every unit landed | `$(git rev-parse --git-dir)/flowseer-plan-status.json`, when present | every `status` is `passed` |
| Review verdict | Orca worktree comment, `review:` entry | `accept` or `accept after fixes` |
| Lesson captured or declined | Orca worktree comment, `compound:` entry | a solution path, `no lesson`, or `observation logged` |

In Orca (`ORCA_TERMINAL_HANDLE` set, `orca status --json` reachable
unsandboxed), read the card:

```bash
orca worktree show --worktree active --json   # .result.worktree.comment and .workspaceStatus
```

Outside Orca there is no card: ask the user for the review verdict and the
compound outcome, and for the implementation outcome when there is no plan,
in one question and record the answers in the report.

A failed signal whose remedy is another skill's work (a plan not
implemented, a `partially implemented:` card entry, a ledger unit that is
not `passed` even when the plan says `implemented`, no review verdict, no
compound outcome) stops the skill here: report which one, the ledger
against the plan's `status` when they disagree, and name the skill to run
next. An absent ledger is not a signal; planless work has none. Do not
merge a partial implementation because the landed units pass. A failed
signal with a mechanical remedy (a receipt older than the
last commit, a dirty marker) is not a stop: name the remedy, ask the user
whether to apply it, and re-read the signal after doing so. Planless work
done in the main conversation, or by `steer`, leaves no `implemented:`
entry at all; when `main..HEAD` is non-empty and the receipt signal
holds, that is a mechanical remedy too: ask the user whether the work is
complete, and on yes write the entry with `orca worktree set` before
merging. Batch every remedy from steps 1 and 2 into one question.

## 2. Check both trees

The worktree must have nothing left to land:

```bash
git status --porcelain            # empty
git log --oneline main..HEAD    # the commits about to land; at least one
git log --oneline HEAD..main    # commits the branch has not seen
```

Uncommitted changes that belong to the task are committed first, with the
plan's outcome in the same commit. For uncommitted changes that do not
belong to the task, say what they are and propose the remedy: commit them
under their own message when they are finished work, or leave them and
stop when another session is mid-edit. Ask before either.

The primary checkout must be on `main` and clean; another session may be
mid-edit there:

```bash
primary=$(git worktree list --porcelain | head -1 | sed 's/^worktree //')
git -C "$primary" branch --show-current   # main
git -C "$primary" status --porcelain      # empty
```

When the primary checkout is not clean, inspect each path read-only and
propose what to do with it: an untracked build artifact, profile, test
binary, or capture is deleted; a tracked modification or a source file is
left alone, since another session owns it. Ask before deleting anything,
and never delete a tracked change.

Orca workers started for this task must be settled and released, so that
removing the worktree later kills nothing:

```bash
orca orchestration worker-list --json
orca terminal list --worktree active --json   # only this terminal remains
```

Release a settled worker with `worker-release`; a running worker stops the
skill. A child worktree of this one whose branch has landed here is removed
now, as `delegate/references/orca.md` describes under Remove a finished
child worktree; one whose branch did not land, or that holds uncommitted
files, is named in the report and left alone:

```bash
orca worktree list --json   # entries whose parentWorktreeId is this worktree
```

A Herdr lane has no Orca entry: `.claude/skills/delegate/scripts/herdr-worker.sh
status` (unsandboxed) must list no worker of this task, and a merged lane
still listed is stopped with `stop <slug>` and its branch deleted.

When main has moved, the verifier run that satisfies the receipt
signal uses the merge-base as its base, not `main`: `--base main`
diffs against main's tip and pulls main's own changes into the scope.

## 3. Merge

Merge from the primary checkout, never by switching this worktree's branch:

```bash
git -C "$primary" merge --no-ff --no-edit <branch>
```

When `HEAD..main` was empty, the branch's verifier run covers the result.
When main had moved, run the verifier in the primary checkout over the
union:

```bash
(cd "$primary" && .claude/skills/verify-change/scripts/verify-change.sh --base <merge-base>)
```

Report a red verifier or a conflict as is, with the exact command and output.
Resolve a conflict only when the resolution is mechanical; otherwise
`git -C "$primary" merge --abort`, report the conflicting files, and stop.

Do not delete the branch; Orca deletes it with the worktree. Remove the
plan status ledger once the merge has landed, and say so in the report:

```bash
rm -f "$(git rev-parse --git-dir)/flowseer-plan-status.json"
```

## 4. Mark the card

```bash
orca worktree set --worktree active --workspace-status completed \
  --comment "merged into main as <sha>" --json
```

## 5. Report

Outcome first: merged as `<sha>` into main, or stopped at the named signal
with the skill to run next. Then the commits landed, the commands run with
their results, whether the verifier ran again on main, and the one command
left for the user: `orca worktree rm --worktree active`. A correction to this
procedure is logged as `compound`, Observe describes.
