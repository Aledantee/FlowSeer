---
name: land
description: Lands finished FlowSeer work by merging main into the current worktree's branch, verifying the result there, and leaving main one fast-forward away, the worktree and Orca card ready for deletion. Use when asked to close, land, finish, or wrap up work after implement, review, and compound have run. Not while any of the three lacks its checkpoint or the review verdict is not accept (offers to run the missing one); never removes its own worktree or the Orca session.
argument-hint: "[plan path]"
---

# Land finished FlowSeer work

Merge only on checkpoints on disk. Never remove this worktree: `orca worktree
rm` kills the terminal that issues it and discards its history, so the person
runs it after reading the report. Merged child worktrees are removed in step 2.

Merge inside this worktree, from `main` into the branch. The harness refuses a
worktree-isolated session every git command that names the primary checkout
(`git -C` and `cd … && git` alike), so nothing below targets that checkout
except the fast-forward in step 3, attempted once and handed to the person
verbatim when refused.

## 1. Read the checkpoints

The branch is the current worktree's branch. The plan is the argument, or the
plan under `docs/plans/` that records this work among the files
`git diff --name-only main...HEAD` lists; work that skipped the plan under
`plan`'s skip rule has none. A plan touched for another reason (a typo fix, a
`superseded_by` field) is not this work's plan: say so and treat the work as
planless. Of a phase plan and its `parent:`, the phase plan is this work's;
report the parent, do not gate on it. A branch that implemented several plans
(a `drive` of a parent's phases) is gated on every one's three fields; one
missing field pauses the merge.

`implement`, `review`, and `compound` leave their outcome as the `status`,
`review`, and `compound` fields of the plan's frontmatter, or for planless
work as one `key: value` line each in
`$(git rev-parse --git-dir)/flowseer-checkpoints`, beside the verifier receipt
and the ledger. `implement` writes the file anew and the other two append; the
last line for a key wins, and step 3 removes the file once the work has landed.

```text
implemented: <request in a few words>
review: accept
compound: no lesson
```

In Orca (`ORCA_TERMINAL_HANDLE` set, `orca status --json` reachable
unsandboxed) the card carries the same entries; load
`references/orca-card.md` and read the card after the plan or the file.

| Signal | Where | Required value |
| --- | --- | --- |
| Implementation landed | plan `status`, or the checkpoints file's `implemented:` line with commits in `main..HEAD` (in Orca also the card's `implemented:` entry with `.workspaceStatus` `in-review`) | `status: implemented`, or the line present |
| Verifier ran after the last edit | `$(git rev-parse --git-dir)/flowseer-verification-receipt` present, `flowseer-verification-dirty` absent | `verified_at` newer than the last commit |
| Every unit landed | `$(git rev-parse --git-dir)/flowseer-plan-status.json`, when present | every `status` is `passed` |
| Review verdict | plan `review` field, or the checkpoints file's `review:` line (in Orca also the card) | `accept` or `accept after fixes` |
| Lesson captured or declined | plan `compound` field, or the checkpoints file's `compound:` line (in Orca also the card) | a solution path, `no lesson`, or `observation logged` |

A verdict or outcome in neither place is missing, whatever the conversation
holds; an answer from the user counts only once it is written to disk, as
the rows below do. An absent ledger is not a signal.

A failed signal whose remedy is another skill's work (a plan not implemented,
a `partially implemented:` entry, a ledger unit not `passed` even when the
plan says `implemented`, no review verdict, no compound outcome) pauses the
merge. Say which signal failed, and the ledger against the plan's `status`
when they disagree, then ask the user (`AGENTS.md`, Agent behavior) whether to
run the missing skill now, all missing signals in one question:

| Missing | Options, recommended first |
| --- | --- |
| implementation, or a unit `pending` or `in_progress` | run `implement` on the remaining units; stop |
| a unit `blocked` | take it back to `plan`; stop. Never `implement` again: the unit already failed three verifier rounds |
| review verdict | run `review` on the branch now; stop |
| review verdict is `rework` | fix the findings and review again (`review`, step 6); stop |
| compound outcome | run `compound` now; record `compound: no lesson` when the user says there is none; stop |

On yes, load `references/missing-checkpoint.md`. A partial implementation is
never merged because its landed units pass.

A failed signal with a mechanical remedy (a stale receipt, a dirty marker, a
missing `implemented:` line for planless work) is not a stop. Load
`references/mechanical-remedies.md`, name the remedy, batch every remedy from
steps 1 and 2 into one question to the user, apply what they approve, and
re-read the signal afterwards.

## 2. Check both trees

The worktree must have nothing left to land:

```bash
git status --porcelain            # empty
git log --oneline main..HEAD    # the commits about to land; at least one
git log --oneline HEAD..main    # commits the branch has not seen
```

Commit uncommitted changes that belong to the task first, with the plan's
outcome in the same commit. For uncommitted changes that do not, say what they
are and ask the user: commit them under their own message when they are
finished work, or leave them and stop when another session is mid-edit.

The primary checkout must have `main` checked out; read it from the git
directory, not with a command against the checkout:

```bash
cat "$(git rev-parse --git-common-dir)/HEAD"   # ref: refs/heads/main
```

Do not inspect its working tree; the fast-forward in step 3 refuses on its own
when a local change there overlaps the merge.

Every worker, child worktree, and `orca-worker.sh` lane this task started must
be settled, released, or removed; load `references/orca-cleanup.md`
whenever `orca status --json` (unsandboxed) reports the runtime reachable,
since lanes this context did not start are invisible otherwise. A running
worker stops the skill.

## 3. Merge

When `HEAD..main` is empty, the branch's verifier run covers the result and
there is nothing to merge here. Otherwise merge `main` into the branch:

```bash
git merge --no-edit main
```

Run it with the sandbox disabled when `main` touched `.claude/` since the
merge-base (`git diff --name-only HEAD...main -- .claude` prints a path): the
sandbox denies writes under `.claude/skills/` even to git, and the merge
aborts on `unable to unlink old '.claude/skills/...': Operation not permitted`.
That failure is expected after any `steer` has landed, and the bypass is its
remedy. Resolve a conflict only when the resolution is mechanical; otherwise
`git merge --abort`, report the conflicting files, and stop.

Then verify the union, sandbox disabled:

```bash
.claude/skills/verify-change/scripts/verify-change.sh --base main
```

This run clears the marks the merge left on files identical to `main`, and the
`<Bash mutation; verify with --full>` line when every generated or module file
it stands for came from `main`. When that line survives, the branch itself
changed one of them: run `--full`. Report a red verifier as is, with the exact
command and output, and stop.

`main` itself moves only in the primary checkout, and only when the run's
last line reads `FlowSeer verification passed.`, no
`flowseer-verification-dirty` file remains, and the receipt's
`verified_at` is newer than `HEAD`'s commit. The fast-forward is a command
of its own, run after those three are read, never chained onto the
verifier run or the check of its result.
Run it once; a worktree-isolated session is refused, and the report then
carries the command verbatim for the person:

```bash
git -C <primary> merge --ff-only <branch>   # <primary>: the parent of $(git rev-parse --git-common-dir)
```

`--ff-only` lands exactly the commit the verifier passed, and refuses when
`main` moved again in the meantime, in which case this step runs again from
the merge. Do not delete the branch; Orca deletes it with the worktree.
Once `main` holds the branch, the plan status ledger and the checkpoints
file are removed; when the person runs the fast-forward, this goes into
the report beside it (not `rm -f`, which Codex's command policy rejects):

```bash
find "$(git rev-parse --git-dir)" -maxdepth 1 \( -name flowseer-plan-status.json -o -name flowseer-checkpoints \) -delete
```

## 4. Mark the card

In Orca, mark the card as `references/orca-card.md` describes: `completed`
with the merge sha, or `ready for main: <sha>` and `in-review` when the
fast-forward is left for the person.

## 5. Report

Outcome first: `main` fast-forwarded to `<sha>`, or `<sha>` verified on top of
`main` and one command away from it, or paused at the named signal with
step 1's question about it. Then the commits landed, the commands run with
their results, whether the verifier ran on the merged tree, and the commands
left for the user in the order to run them: the fast-forward, the ledger and
checkpoints removal, any child worktree removal the harness refused, and
`orca worktree rm --worktree active`. A correction to this procedure is
logged as `compound`, Observe describes.
