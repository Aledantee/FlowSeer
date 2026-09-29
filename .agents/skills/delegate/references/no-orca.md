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
  `review-seam` stage included. It runs in a `general-purpose` subagent
  with `isolation: worktree`, never in a read-only one, pinned to the
  first Claude model in the stage role's `fit`, then its `last_resort`,
  that steps 1 and 2 of "Pick the role" leave. Stop and name the stage for
  the user to run in a fresh session only when no such model is left or
  the Agent tool cannot run.
  - The coordinator logs the lane in place of `orca-worker.sh`, so
    `drive`'s `$base` and `review-lanes.md`'s executor lookup still work.
    Before the dispatch it records `base=$(git rev-parse HEAD)`. Once the
    result names the subagent's worktree and branch, it runs the line
    below, which prints the `run`:

    ```bash
    python3 .claude/skills/delegate/scripts/runlog.py start --lane <slug> --cli claude --role <role> --worktree <path> --branch <branch> --base "$base" --model <id> --plan <plan> --unit <stage>
    ```

  - After the merge it runs `runlog.py end --run <run> --head <sha>`, and
    `runlog.py grade --run <run> --outcome <outcome> --verify <result>` in
    place of `orca-worker.sh grade`. `git worktree remove <path>` takes the
    place of `orca-worker.sh stop`. When the harness refuses it, the
    report names the path. The rest of the calling skill's after-stage
    steps run unchanged.
- Other editing work whose role's fit set holds no Claude model:
  - The coordinator does the units itself, one at a time, along the calling
    skill's path for a wave of one.
  - When the role `exclude`s the coordinator's model (`execute-sensitive`
    on a top-tier session), nothing runs: report the unit as blocked on
    Orca.
