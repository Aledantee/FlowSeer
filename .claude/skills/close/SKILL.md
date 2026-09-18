---
name: close
description: Land finished FlowSeer work by merging main into the current worktree's branch, verifying the result there, and leaving main one fast-forward away, with the worktree and its Orca card ready for deletion. Use when asked to close, land, finish, or wrap up work after implement, review, and compound have run. Does not merge while any of the three has not left its checkpoint or the review verdict is not accept, and offers to run the missing one; removes merged child worktrees but never its own worktree or the Orca session.
argument-hint: "[plan path]"
---

# Close finished FlowSeer work

A merge into main lands for every other worktree, so this skill checks the
evidence the other skills left before it merges. When a checkpoint is
missing it asks whether to run that skill now, and merges only once the
checkpoint is on disk. It never removes its own worktree: `orca worktree rm` kills
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
plan; the parent is reported, not gated on. A branch that implemented
several plans, as a `drive` of a parent's phases leaves it, is gated on
every one of them: each plan's three fields are read, and one missing
field pauses the merge.

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
compound outcome) pauses the merge. Say which signal failed, and the ledger
against the plan's `status` when they disagree, then ask the user, as
`AGENTS.md`, Agent behavior, describes, whether to run the missing skill
now:

| Missing | Options, recommended first |
| --- | --- |
| implementation, or a unit `pending` or `in_progress` | run `implement` on the remaining units; stop |
| a unit `blocked` | take it back to `plan`; stop. Never `implement` again: the unit already failed three verifier rounds |
| review verdict | run `review` on the branch now; stop |
| review verdict is `rework` | fix the findings and review again (`review`, step 6); stop |
| compound outcome | run `compound` now; record `compound: no lesson` when the user says there is none; stop |

On yes, the skill runs in a session of its own, never in this one: this
session's context stays on the merge, and a review is independent only
when its reader did not watch the work being closed. Dispatch one worker as
`delegate` describes for editing work (a Herdr worker, else an Orca child
worktree, else a `general-purpose` subagent with `isolation: worktree`),
role `execute` for `implement` and `compound`, `review-seam` for `review`.
The brief names the skill to run, this branch as the scope, the plan path
or the request in a few words, and asks for the checkpoint:

- With a plan, the worker commits the plan's `status`, `review`, or
  `compound` field on its branch, and the merge of that branch brings the
  checkpoint here.
- Without a plan, the checkpoints file lives in the worker's own git
  directory, so the worker reports the line (`review: accept`) and this
  session writes it with
  `.claude/skills/verify-change/scripts/ledger.py checkpoint <key> "<value>"`
  after checking the worker's tree as `delegate` describes.
- A question the skill would put to the user comes back as the worker's
  blocker, and this session asks it.

Merge the worker's branch, remove the child, and start this step again
from the top: the checkpoint on disk gates the merge, not the answer, and
the merged commit makes the receipt stale, which the table below remedies.
Several missing signals are asked in one question and worked one worker
after another, in order: `implement`, `review`, `compound`. An absent ledger is
not a signal; planless work has none. A partial implementation is never
merged because its landed units pass.

A failed signal with a mechanical remedy is not a stop. Name the remedy,
batch every remedy from steps 1 and 2 into one question to the user, apply
what they approve, and re-read the signal afterwards.

| Failed signal | Remedy |
| --- | --- |
| receipt older than the last commit | a verifier run |
| marker names paths, receipt `full=false` | a targeted run naming those paths; a marker carrying the receipt's mtime holds the lines that run could not clear, listed in its output under `Unverified edits remain after this run:` |
| marker holds `<Bash mutation; verify with --full>`, receipt `full=false` | a `--full` run |
| marker beside a `full=true` receipt | a targeted run naming the marker's paths: they were edited after the full run |
| no `implemented:` line for planless work done in the main conversation or by `steer`, `main..HEAD` non-empty, receipt signal holds | ask whether the work is complete; on yes, write the line to the checkpoints file, and in Orca to the card, before merging |

## 2. Check both trees

The worktree must have nothing left to land:

```bash
git status --porcelain            # empty
git log --oneline main..HEAD    # the commits about to land; at least one
git log --oneline HEAD..main    # commits the branch has not seen
```

Uncommitted changes that belong to the task are committed first, with the
plan's outcome in the same commit. For uncommitted changes that do not
belong to the task, say what they are and ask the user which remedy
applies: commit them under their own message when they are finished work,
or leave them and stop when another session is mid-edit.

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
of `main` and one command away from it, or paused at the named signal with
step 1's question about it. Then the commits landed, the commands run with their
results, whether the verifier ran on the merged tree, and the commands left
for the user in the order to run them: the fast-forward, the ledger and
checkpoints removal, any child worktree removal the harness refused, and
`orca worktree rm --worktree active`. A correction to this procedure is
logged as `compound`, Observe describes.
