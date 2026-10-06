# Dispatch the review stage

Load this before starting a review stage worker.

The review stage's `<base>` is the `$base` of the plan's first lane, read
as step 2 shows for that lane's `run`. It is the fork point of the plan's
branch, so the review covers exactly this plan's change even when another
phase merged here meanwhile or a worker merged the branch here itself (then
`git merge-base HEAD <branch>` gives the branch tip). The review lane's own
`start` event holds the worktree's `HEAD` at its join, the end of the
implement stage, and a review from there would read an empty change.

The review worker joins the plan's worktree (`--join <lane>`), so the
branch it reviews is the one checked out there.

Name the branch and the plan path in the brief. With a bare commit range,
`review` reads the change as other work and records no verdict in the plan.

Give the worker a report file under the session scratchpad directory, as
`delegate`, Write the brief, item 4 describes: it writes its whole step 5
report there and prints the verdict, the path, and the finding count.

When the branch's writers leave `review-seam` no model, the stage still runs
as one worker, resolved by `delegate`'s steps with step 3 skipped; it splits
its reviewers by writer as `delegate` describes and records the one verdict
itself.
