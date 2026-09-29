# Delegating without Orca

Load this when `orca status --json` (run with the sandbox disabled) does not
report `runtime.reachable: true`.

- A review lane (`review-unit` or `review-seam`) whose resolved model is not
  a Claude model a native subagent can be pinned to gets no independent
  reviewer. The coordinator's own reading in `review` is its pass, and the
  report says so.
- Editing work goes to a `general-purpose` subagent with
  `isolation: worktree`, pinned to a Claude model, only when the role's fit
  set holds one; a Claude model outside the fit set is not calibrated for
  the role.
- A stage worker of `land` or `drive` runs a skill and commits its
  checkpoint, so it is editing work whatever role supplies its model, a
  `review-seam` stage included. It runs neither here nor in a read-only
  subagent: stop and name the stage for the user to run in a fresh session.
- Other editing work whose role's fit set holds no Claude model:
  - The coordinator does the units itself, one at a time, along the calling
    skill's path for a wave of one.
  - When the role `exclude`s the coordinator's model (`execute-sensitive`
    on a top-tier session), nothing runs: report the unit as blocked on
    Orca.
