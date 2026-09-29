# Fix and re-review rounds

The coordinating session runs the rounds; no skill runs them on its own, and
`implement` covers a plan's units, not a review's findings. One round:

1. Dispatch the fixes as `delegate` describes, one worker per file group.
   Brief each with its findings' `path:line`, failure scenario, and smallest
   fix, and with the class `SKILL.md` step 4 named: the mechanism, and the
   instruction to find and fix every other site that engages it and report
   the sites it cleared. A fix worker may edit its findings' files, the file
   in the owning layer where the fix belongs (a helper a test needs goes in
   that layer, not in the test), every file the fix leaves stale (a Taskfile
   description, a README), and the files of the other sites it finds, each
   listed in its report. It never edits a plan Decision marked
   `decided by the user` (`delegate`, Write the brief, item 6). The review's changed paths do not
   bound the fix.
   The coordinating session does not make the fixes itself.
2. Merge each worker's branch, then run the verifier once on the union of
   the changed paths, before anything is reviewed again.
3. Repeat `SKILL.md` steps 3 and 4 over the branch diff, briefing the
   reviewer with the previous round's findings and the changed paths, so it
   judges each fix against its finding instead of rediscovering it. When the
   previous round's remedy was an executable property (step 4), that
   artifact is the brief's primary subject: is its enumeration complete
   against the source it claims to read, does each case fail for the rule it
   names, and is each exemption an argument no input can violate? A wrong
   invariant is worse than none, since the next reader trusts it and stops
   looking.

## When to stop

- A round with no correctness findings ends the loop: list what remains in
  the final report, set the verdict to `accept after fixes`, and end with
  the `accept` row's question.
- The coordinator holds the rounds' history, so it is the one that sees a
  round find a defect in the previous round's fix for the same mechanism.
  Apply step 4's class rule before the next round.
- After three rounds on the same mechanism without a clean one, take the
  work to `plan` with what the rounds established.

Before the final report, stop each fix worker's lane: `orca worktree list
--json` lists no worktree whose `parentWorktreeId` is this one, other than
those the report names with the reason they stayed. A lane left behind
blocks the coordinator's `stop` of this worktree.
