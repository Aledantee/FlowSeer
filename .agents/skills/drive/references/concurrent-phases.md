# Run a parent's phases at once

Load this when the state command's last line names more than one phase.

Run several at once when the quota allows and the phases are independent:

- Independent means no `After:` between them and, for any stage past
  re-plan, no package in common across the `Files:` lines of their plans'
  Units. A phase that still needs re-planning has no units yet; its re-plan
  stage edits only its own plan file and can run beside anything, but its
  implement stage waits for the check.
- When the phase to re-plan reads `status: implemented`, a review sent it
  back, and its re-plan also empties its `Landed:` line in the parent and
  may set the parent back to `planned`
  (`plan/references/replan-implemented.md`). Run that re-plan with no
  implement stage in flight, since an implement worker that still reads the
  old range can take its own phase for the last one and set the parent
  `implemented`.
- Read the cap from `delegate`'s Wave size before the round. Run
  `k = min(independent ready phases, cap / 2)` phases at once, rounded down
  and at least one, and give each a share of `cap / k`, rounded down, so
  each stage worker holds at least one worker of its own. A cap of three
  drives one phase with a budget of two; a cap of six drives up to three
  phases with a budget of one each.
- Each phase moves through its stages on its own. Merge each stage as its
  worker settles and run step 2's after-stage list for it; a phase's next
  stage branches from this branch's `HEAD` at that moment, which then holds
  whatever else has merged.
- The parent is the one file concurrent phases both write. Resolve a
  conflict that touches only the parent's `Landed:` lines or `status` here
  by keeping every landed range other than one a re-plan emptied; send any
  other conflict back to the
  worker, as `implement/references/workers.md` describes.
- Recompute the cap when a phase finishes or parks, and start the next
  ready phase into the freed share, unless a finished phase is owed a land
  (`SKILL.md`, step 3).

When the last phases landed concurrently, each implement worker saw the
other still open and left the parent `planned`. Once both have merged, set
it to `implemented` here, commit, and run the verifier on the parent path.
