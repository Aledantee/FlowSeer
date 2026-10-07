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
- Each phase has its own worktree and branch and moves through its stages
  there. Run step 2's "After each stage" list as each worker settles, and
  start the phase's next stage with `--join`. Nothing merges here until a
  phase's last stage is done, so a phase never sees another's unfinished
  work, and a phase that starts later forks from a `HEAD` that holds only
  finished phases.
- Run step 2's "After the last stage" list for one phase at a time. Its
  merge and verifier run are this checkout's, and two at once would verify
  a union neither phase owns.
- Recompute the cap when a phase finishes or parks, and start the next
  ready phase into the freed share, unless a finished phase is owed a land
  (`SKILL.md`, step 3).
