# Dispatch the review stage

Load this before starting a review stage worker.

The review stage's `<base>` is the implement lane's `$base`, read as step 2
shows for that lane's `run`. That keeps the review on exactly this plan's
change even when another phase merged here meanwhile or the worker merged
its own branch here (then `git merge-base HEAD <branch>` gives the branch
tip).

Name the branch and the plan path in the brief. With a bare commit range,
`review` reads the change as other work and records no verdict in the plan.

When the branch's writers leave `review-seam` no model, the stage still runs
as one worker, resolved by `delegate`'s steps with step 3 skipped; it splits
its reviewers by writer as `delegate` describes and records the one verdict
itself.
