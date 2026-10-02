# Running a missing skill before the merge

Load this when the user answers yes to step 1's question to run `implement`,
`review`, or `compound` before the merge, or to close a listed review gap
(the gap pass, below).

The skill runs in a session of its own, never in this one: this session's
context stays on the merge, and a review is independent only when its reader
did not watch the work being closed. Dispatch one worker as `delegate`
describes for editing work, `review` included, since the worker commits its
checkpoint. Without Orca, `delegate`'s `references/no-orca.md` runs it in
a native subagent. Use role
`execute` for `implement` and `compound`, `review-seam` for `review`.

The brief names the skill to run, this branch as the scope, the plan path or
the request in a few words, and asks for the checkpoint:

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

Before merging the worker's branch, run
`.claude/skills/delegate/scripts/orca-worker.sh check <slug>`. A non-zero
result stops the merge. After the merge commit exists, including a resolved
conflict, run
`python3 .claude/skills/land/scripts/merge-check.py ORIG_HEAD..HEAD`.
A non-zero result also stops the merge. Carry every `missing` block in the
report. Remove the child and start step 1 again from the top: the checkpoint
on disk gates the merge, not the answer, and the merged commit makes the
receipt stale, which step 1 then remedies.
Several missing signals are worked one worker after another, in order:
`implement`, `review`, `compound`.

## The gap pass

When step 1 offered the gap pass for a listed review gap, dispatch one worker
as above, role `execute`. The brief names
`.claude/skills/review/references/fix-loop.md`, "The gap pass", this branch as
the scope, and the plan path. The work is done when
`python3 .claude/skills/land/scripts/review-gaps.py <plan>` exits 0. The
worker leaves source outside tests, comments, and docs untouched: a gap whose
fix needs it is a blocker for the worker to state, and that fix is a round of
`review`'s fix loop, not this pass. For planless work the worker reports the
gap fixes, and this session records `accept after fixes` with
`ledger.py checkpoint` after checking the worker's tree. Merge as above, and
start step 1 again from the top.
