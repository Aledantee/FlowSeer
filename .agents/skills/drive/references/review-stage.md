# Dispatch the review stage

Load this before starting a review stage worker.

The review stage's `<base>` is the implement lane's `$base`, read as step 2
shows for that lane's `run`. That keeps the review on exactly this plan's
change even when another phase merged here meanwhile or the worker merged
its own branch here (then `git merge-base HEAD <branch>` gives the branch
tip).

Name the branch and the plan path in the brief. With a bare commit range,
`review` reads the change as other work and records no verdict in the plan.

A gap pass runs in this worker too. The brief states that the worker is the
pass's coordinator (`review`'s `references/fix-loop.md`, "The gap pass"): it
merges its own fix lanes, reruns every quoted mutation on the merged tree,
deletes a gap's line only when the suite fails on it, and writes
`accept after fixes` only once no line remains. A worker that cannot close a
gap leaves the verdict `gaps open` and reports the gap, which parks the plan
(`drive`, step 4).

When the branch's writers leave `review-seam` no model, the stage still runs
as one worker, resolved by `delegate`'s steps with step 3 skipped; it splits
its reviewers by writer as `delegate` describes and records the one verdict
itself.
