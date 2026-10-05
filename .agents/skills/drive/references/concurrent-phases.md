# Run a parent's phases at once

Load this when the state command's last line names more than one phase.

Run several at once when the quota allows and the phases are independent:

- Independent means no `after` relationship between them and, for any stage
  past re-plan, no package in common across the `Files:` lines of their plans'
  Units. A phase that still needs re-planning has no units yet; its re-plan
  stage edits only its own plan state and can run beside anything, but its
  implement stage waits for the check.
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
- Recompute the cap when a phase finishes or parks, and start the next
  ready phase into the freed share, unless a finished phase is owed a land
  (`SKILL.md`, step 3).
