---
name: close
description: Land finished FlowSeer work by merging main into the current worktree's branch, verifying the result there, and leaving main one fast-forward away, with the worktree and its Orca card ready for deletion. Use when asked to close, land, finish, or wrap up work after implement, review, and compound have run. Refuses when any of the three has not left its checkpoint or the review verdict is not accept; removes merged child worktrees but never its own worktree or the Orca session.
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

The merge runs inside this worktree, from `main` into the branch. A
worktree-isolated session is refused every git command that names the
primary checkout, `git -C` and `cd … && git` alike, by the harness rather
than by a repository hook, so nothing below targets that checkout except
the fast-forward in step 3, attempted once and handed to the person
verbatim when refused. Conflicts are resolved and the verifier runs where
the tests already are.

## 1. Read the checkpoints

The branch is the current worktree's branch; the plan is the argument, or
the plan under `docs/plans/` that records this work, found among the files
`git diff --name-only main...HEAD` lists. Work that skipped the plan under
`plan`'s skip rule has none. A plan the branch touched for another reason,
say a typo fix or a `superseded_by` field, is not this work's plan; say so
and treat the work as planless. When the changed set holds a phase plan
and the parent its `parent:` field names, the phase plan is this work's
plan; the parent is reported, not gated on.

`implement`, `review`, and `compound` each leave their outcome on disk: as
the `status`, `review`, and `compound` fields of the plan's frontmatter when
the work has a plan, and otherwise as one `key: value` line each in
`$(git rev-parse --git-dir)/flowseer-checkpoints`, beside the verifier
receipt and the ledger. `implement` writes the file anew and the other two
append; the last line for a key wins, and step 3 removes the file once the
work has landed, so a reused worktree does not inherit a verdict.

```text
implemented: <request in a few words>
review: accept
compound: no lesson
```

In Orca (`ORCA_TERMINAL_HANDLE` set, `orca status --json` reachable
unsandboxed) the card carries the same entries; read the plan or the file
first and the card second, and report a disagreement as a stop:

```bash
orca worktree show --worktree active --json   # .result.worktree.comment and .workspaceStatus
```

| Signal | Where | Required value |
| --- | --- | --- |
| Implementation landed | plan `status`, or the checkpoints file's `implemented:` line with commits in `main..HEAD` (in Orca also the card's `implemented:` entry with `.workspaceStatus` `in-review`) | `status: implemented`, or the line present |
| Verifier ran after the last edit | `$(git rev-parse --git-dir)/flowseer-verification-receipt` present, `flowseer-verification-dirty` absent | `verified_at` newer than the last commit |
| Every unit landed | `$(git rev-parse --git-dir)/flowseer-plan-status.json`, when present | every `status` is `passed` |
| Review verdict | plan `review` field, or the checkpoints file's `review:` line (in Orca also the card) | `accept` or `accept after fixes` |
| Lesson captured or declined | plan `compound` field, or the checkpoints file's `compound:` line (in Orca also the card) | a solution path, `no lesson`, or `observation logged` |

A verdict or outcome in neither place is missing, whatever the conversation
holds: an answer taken from the user here would live in the transcript,
where a later session or a re-run of this skill cannot read it.

A failed signal whose remedy is another skill's work (a plan not
implemented, a `partially implemented:` entry, a ledger unit that is not
`passed` even when the plan says `implemented`, no review verdict, no
compound outcome) stops the skill here: report which one, the ledger
against the plan's `status` when they disagree, and name the skill to run
next. An absent ledger is not a signal; planless work has none. Do not
merge a partial implementation because the landed units pass.

A failed signal with a mechanical remedy is not a stop: name the remedy,
ask the user whether to apply it, and re-read the signal after doing so. A
receipt older than the last commit wants a verifier run. The dirty marker
names its own remedy, read together with the receipt's `full=` line. A
receipt with `full=false` clears only the paths it named: paths in the
marker want a targeted run naming them, the `<Bash mutation; verify with
--full>` line wants `--full`, and a marker carrying that receipt's mtime
was rewritten by the run with the lines it could not clear, listed in its
output under `Unverified edits remain after this run:`. A `--full` run
removes the marker outright, so a marker beside a `full=true` receipt was
written after the run by a Bash command the edit hook could not attribute
(a redirect into a `.md`, `.json`, or `.yaml` file, a `sed -i`); when
`git status --porcelain` is empty and no commit followed the receipt, that
command changed nothing the run did not cover, and the remedy is to delete
the marker rather than re-run `--full`, which the same redirect would
mark again.
Planless work done in the main conversation, or by `steer`, leaves no
`implemented:` line; when `main..HEAD` is non-empty and the receipt signal
holds, that is a mechanical remedy too: ask the user whether the work is
complete, and on yes write the line to the checkpoints file, and in Orca to
the card, before merging. Batch every remedy from steps 1 and 2 into one
question.

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

The primary checkout must have `main` checked out, which its git directory
says without a command against it:

```bash
cat "$(git rev-parse --git-common-dir)/HEAD"   # ref: refs/heads/main
```

Its working tree is the person's and is not inspected from here; the
fast-forward in step 3 refuses on its own when a local change there
overlaps the merge.

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
files, is named in the report and left alone. A `git worktree remove` or
`git branch -d` the harness refuses from this session goes into the report
as a command for the person, with the child's path and branch:

```bash
orca worktree list --json   # entries whose parentWorktreeId is this worktree
```

A Herdr lane has no Orca entry: `.claude/skills/delegate/scripts/herdr-worker.sh
status` (unsandboxed) must list no worker of this task, and a merged lane
still listed is stopped with `stop <slug>` and its branch deleted.

## 3. Merge

When `HEAD..main` is empty, the branch's verifier run covers the result and
there is nothing to merge here. Otherwise merge `main` into the branch:

```bash
git merge --no-edit main
```

Run it with the sandbox disabled when `main` touched `.claude/` since the
merge-base (`git diff --name-only HEAD...main -- .claude` prints a path):
the sandbox denies writes under `.claude/skills/` even to git replaying a
committed change, and the merge aborts on
`unable to unlink old '.claude/skills/...': Operation not permitted`. That
failure is expected after any `steer` has landed, not a defect, and the
bypass is its remedy. Resolve a conflict only when the resolution is
mechanical; otherwise `git merge --abort`, report the conflicting files,
and stop.

Then verify the union, sandbox disabled. `main` is now an ancestor of
`HEAD`, so a diff against it is the branch's whole change on top of it:

```bash
.claude/skills/verify-change/scripts/verify-change.sh --base main
```

When the dirty marker holds the `<Bash mutation; verify with --full>` line,
run `--full` instead. Report a red verifier as is, with the exact command
and output, and stop.

`main` itself moves only in the primary checkout. Run the fast-forward
once; a worktree-isolated session is refused, and the report then carries
the command verbatim for the person:

```bash
git -C <primary> merge --ff-only <branch>   # <primary>: the parent of $(git rev-parse --git-common-dir)
```

`--ff-only` lands exactly the commit the verifier passed, and refuses when
`main` moved again in the meantime, in which case this step runs again from
the merge. Do not delete the branch; Orca deletes it with the worktree.
Once `main` holds the branch, the plan status ledger and the checkpoints
file are removed; when the person runs the fast-forward, this goes into
the report beside it:

```bash
rm -f "$(git rev-parse --git-dir)/flowseer-plan-status.json" "$(git rev-parse --git-dir)/flowseer-checkpoints"
```

## 4. Mark the card

```bash
orca worktree set --worktree active --workspace-status completed \
  --comment "<existing>; merged into main as <sha>" --json
```

When the fast-forward is left for the person, the entry appended is
`ready for main: <sha>` and the status stays `in-review`. The existing
comment is kept in both cases: `--comment` replaces it, and a re-run of
this skill reads the card's entries.

## 5. Report

Outcome first: `main` fast-forwarded to `<sha>`, or `<sha>` verified on top
of `main` and one command away from it, or stopped at the named signal with
the skill to run next. Then the commits landed, the commands run with their
results, whether the verifier ran on the merged tree, and the commands left
for the user in the order to run them: the fast-forward, the ledger and
checkpoints removal, any child worktree removal the harness refused, and
`orca worktree rm --worktree active`. A correction to this procedure is
logged as `compound`, Observe describes.
