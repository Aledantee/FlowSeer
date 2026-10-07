# Running a missing skill before the merge

Load this when the user answers yes to step 1's question to run `implement`,
`review`, or `compound` before the merge.

The skill runs in a session of its own, never in this one: this session's
context stays on the merge, and a review is independent only when its reader
did not watch the work being closed. Dispatch one worker as `delegate`
describes for editing work, `review` included, since the worker commits its
checkpoint. Without Orca, `delegate`'s `references/no-orca.md` runs it in
a native subagent. Use role
`execute` for `implement` and `compound`, `review-seam` for `review`.

The brief names the skill to run, this branch as the scope, the plan path or
the request in a few words, and asks for the checkpoint:

- With a plan, the worker records the plan state with
  `.claude/skills/plan/scripts/plan_record.py implemented <plan> --units <n> --from <t> --to <t>`,
  `.claude/skills/plan/scripts/plan_record.py review <plan> "<verdict>"`, or
  `.claude/skills/plan/scripts/plan_record.py compound <plan> "<outcome>"` on its
  branch, and the merge of that branch brings
  the checkpoint here.
- Without a plan, the checkpoints file lives in the worker's own git
  directory, so the worker reports the line (`implemented:`, `review:`, or
  `compound:`) and this session writes it to its own checkpoints file with
  `.claude/skills/verify-change/scripts/ledger.py checkpoint <key> "<value>"`
  after checking the worker's tree as `delegate` describes. A review worker
  also reports its `gaps:` and `rounds:` lines, and this session writes them
  the same way with keys `gaps` and `rounds`, since the next review reads its
  open items and its round count from that file. The brief carries this session's
  last `gaps:` and `rounds:` lines verbatim. Before `review` step 1, the
  worker writes those lines to its checkpoints file with the same command so
  the review resumes the existing open items and round count. No write by
  this session, a review worker, or a compound worker passes `--replace`,
  whatever the key (`implemented`, `review`, `compound`, `gaps`, `rounds`),
  since it rewrites the file and drops its earlier lines. An `implement`
  worker that starts its own file is the one writer that passes it, as
  `implement` describes.
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
