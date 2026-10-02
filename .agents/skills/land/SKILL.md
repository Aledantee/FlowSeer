---
name: land
description: Lands finished FlowSeer work by merging main into the current worktree's branch, retiring its plan into the direction records, verifying the result there, and leaving main one fast-forward away, the worktree and Orca card ready for deletion. Lands a multi-phase plan phase by phase. Use when asked to close, land, finish, or wrap up work after implement, review, and compound have run. Not while any of the three lacks its checkpoint or the review verdict is not accept (offers to run the missing one); never removes its own worktree or the Orca session.
argument-hint: "[plan path]"
---

# Land finished FlowSeer work

Merge only on checkpoints on disk. Never remove this worktree: `orca worktree
rm` kills the terminal that issues it and discards its history, so the person
runs it after reading the report. Merged child worktrees are removed in step 2.

A multi-phase plan lands phase by phase: run this skill once a phase's
`implement`, `review`, and `compound` checkpoints are on disk, not once
after the last phase. Each phase is then verified on top of `main`, with a
fast-forward to a commit that holds only finished phases. The
parent retires with its last phase (step 4).

Merge inside this worktree, from `main` into the branch. The harness refuses a
worktree-isolated session every git command that names the primary checkout
(`git -C` and `cd … && git` alike), so nothing below targets that checkout
except the fast-forward in step 5, attempted once and handed to the person
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
missing field pauses the merge. A plan an earlier run already retired
(step 4) has a `docs(plans): retire <slug>` commit in `main..HEAD`: read
its three fields from that commit's body, and do not retire it again.

`implement`, `review`, and `compound` leave their outcome as the `status`,
`review`, and `compound` fields of the plan's frontmatter, or for planless
work as one `key: value` line each in
`$(git rev-parse --git-dir)/flowseer-checkpoints`, beside the verifier receipt
and the ledger. `implement` writes the file anew and the other two append; the
last line for a key wins, and step 5 removes the file once the work has landed.

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
| review verdict is `rework` or `fixes needed` | fix the findings and review again (`review`, step 6); stop |
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

Do not inspect its working tree; the fast-forward in step 5 refuses on its own
when a local change there overlaps the merge.

Every worker, child worktree, and `orca-worker.sh` lane this task started must
be settled, released, or removed; load `references/orca-cleanup.md`
whenever `orca status --json` (unsandboxed) reports the runtime reachable,
since lanes this context did not start are invisible otherwise. A running
worker stops the skill.

## 3. Merge

When `HEAD..main` is empty there is nothing to merge; go to step 4.
Otherwise merge `main` into the branch:

```bash
git merge --no-edit main
```

Run the merge with the sandbox disabled when `main` touched `.claude/` or
`.agents/` since the merge-base
(`git diff --name-only HEAD...main -- .claude .agents` prints a path).
`.claude/skills` is a link to `.agents/skills`, where git tracks the skills,
and the sandbox denies writes under both even to git, so the merge aborts on
`unable to unlink old '.agents/skills/...': Operation not permitted`.
That failure is expected after any `steer` has landed, and the bypass is its
remedy. Resolve a conflict only when the resolution is mechanical; otherwise
`git merge --abort`, report the conflicting files, and stop.

After the merge commit exists, including a resolved conflict, run:

```bash
python3 .claude/skills/land/scripts/merge-check.py ORIG_HEAD..HEAD
```

A non-zero result stops land. Carry every `missing` block in the report.

## 4. Retire the plan

A plan describes open work; once its work lands, a later agent would read
it as a statement about the tree. Retire after the merge, so the links
`main` gained since the fork and the parent's merged `Landed:` lines are in
the tree the retire reads.

Step 1 was the plan status ledger's last reader, and the verifier rejects a
ledger that names a deleted plan, so remove it first:

```bash
find "$(git rev-parse --git-dir)" -maxdepth 1 -name flowseer-plan-status.json -delete
```

Then load `references/retire-plan.md` for each plan step 1 gated whose
`status` reads `implemented` (a phase plan, and its parent too when the
merged parent shows every phase landed), and for each plan the branch
marked `superseded` or `abandoned`. It promotes or amends the direction
records the plan's decisions call for, rewrites the links to the plan, and
deletes it in a commit of its own.

A merge can carry in a finished plan that no `land` gated, such as a phase a
`drive` merged into another branch. Retire those too: each plan the merged tree holds whose `status` is
`superseded` or `abandoned`, and each `implemented` one whose `review` and
`compound` fields read as step 1 requires. Report an `implemented` plan
missing a field without retiring it.

```bash
grep -l -E '^status: (implemented|superseded|abandoned)' docs/plans/*-plan.md
```

Planless work skips the first retire and still runs the check.

## 5. Verify and fast-forward

Verify the union, sandbox disabled:

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
test "$(git -C <primary> symbolic-ref --short HEAD)" = main && git -C <primary> merge --ff-only <sha>
# <primary>: the parent of $(git rev-parse --git-common-dir)
# <sha>: git rev-parse HEAD, read after the three checks
```

Name the verified commit, not the branch: a branch that keeps moving, as a
`drive` of further phases does, would carry unverified commits into `main`
when the person runs the command later. The branch test comes first because
that run can come long after step 2 read the primary checkout's `HEAD`.
`--ff-only` lands exactly that commit, and refuses when `main` no longer
leads to it. On that refusal the skill runs again from step 3 when
`git rev-list <sha>..HEAD` prints nothing, and from step 1 otherwise, since
the branch then carries work step 1 has not gated. Do not delete the
branch; Orca deletes it with the worktree. Once `main` holds the commit, the plan status ledger and the checkpoints
file are removed; when the person runs the fast-forward, this goes into
the report beside it (not `rm -f`, which Codex's command policy rejects):

```bash
find "$(git rev-parse --git-dir)" -maxdepth 1 \( -name flowseer-plan-status.json -o -name flowseer-checkpoints \) -delete
```

## 6. Mark the card

In Orca, mark the card as `references/orca-card.md` describes: `completed`
with the merge sha, or `ready for main: <sha>` and `in-review` when the
fast-forward is left for the person. A phase land whose parent still has
phases to run (the parent was not retired in step 4) appends
`merged into main as <sha>` or `ready for main: <sha>` and keeps the card's
status, since the worktree is still working.

## 7. Report

Outcome first: `main` fast-forwarded to `<sha>`, or `<sha>` verified on top of
`main` and one command away from it, or paused at the named signal with
step 1's question about it. Then the plans retired with the records drafted
or amended (a drafted record waits for a person to accept it), the commits
landed, the commands run with
their results, whether the verifier ran on the merged tree, and the commands
left for the user in the order to run them: the fast-forward, the ledger and
checkpoints removal, any child worktree removal the harness refused, and
`orca worktree rm --worktree active`, left out for a phase land whose parent
still has phases to run. A correction to this procedure is
logged as `compound`, Observe describes.
