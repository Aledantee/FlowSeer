# Delegating without Orca

Load this when `orca status --json` (run with the sandbox disabled) does not
report `runtime.reachable: true`.

- A review lane (`review-unit` or `review-seam`) that resolves to a model
  other than a Claude model, or to none because step 2 dropped the
  executor's vendor, runs `independent-reviewer` on the first Claude model
  in the role's `fit` that is not the executor's model. For `review-unit`
  this reads `vendor_differs_from` as a model difference: a reviewer from
  the executor's vendor is a weaker second reader than one from another
  vendor, and still a reader the change otherwise lacks. The report names
  the lane a same-vendor reviewer. For example, units that ran on
  `claude-sonnet-5-5` are reviewed on `claude-opus-5-5`. Only when the
  executor's model is the one Claude model in `fit` does the lane get no
  independent reviewer. The coordinator's own reading in `review` is then
  its pass, and the report says so.
- A `lookup`, `research`, or `judge` lane whose resolved model is not a
  Claude model runs native on the first Claude model in the role's `fit`
  that steps 1 and 2 of "Pick the role" leave. With none left, the
  coordinator does the lane's work itself, and the report says so.
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
  - The coordinator logs the lane in place of `delegate orca-worker`, so
    `drive`'s `$base` and `review-lanes.md`'s executor lookup still work.
    Before the dispatch it records `base=$(git rev-parse HEAD)`. Once the
    result names the subagent's worktree and branch, it runs the line
    below, which prints the `run`:

    ```bash
    uv run tools/scripts/run.py delegate runlog start --lane <slug> --cli claude --role <role> --worktree <path> --branch <branch> --base "$base" --model <id> --plan <plan> --unit <stage>
    ```

  - A later stage of a plan whose first stage ran this way cannot join that
    subagent's worktree, since `isolation: worktree` always makes a new
    one. The coordinator records the first stage's branch. A later stage's
    subagent starts with `git merge --ff-only <recorded branch>`, and the
    coordinator moves the recorded branch to the stage's head with `git
    branch -f <recorded branch> <head>` once the stage's checks pass, so
    the next stage forks from the plan's work.
  - Before the first stage of a `drive`, when the session runs in auto
    mode, ask the user (`AGENTS.md`, Agent behavior) to leave auto mode for
    the drive (recommended, since every stage otherwise stops on a
    refusal), or to accept one `!` command per refused step. The auto-mode
    classifier refuses what a stage needs: the verifier with the sandbox
    disabled, a commit or edit that touches a policy surface, a test
    mutation, and git in a stage worker's worktree. The worktree
    `isolation: worktree` creates lies outside the sandbox's write
    allowlist, so every git, test, and verifier command there runs with
    the sandbox disabled, and the classifier decides per command, so it
    can allow that for one worker and refuse it for the next (unverified
    against the harness documentation).
  - When the user accepted the `!` commands, the brief says which commands
    need the bypass (git, `go test`, the verifier, in the worker's
    worktree) and that a classifier refusal is a blocker quoting the
    refused command and the worktree path, not a reason to try another
    form. On that blocker the coordinator hands the user the command to
    run with `!` in that worktree, then continues the same subagent with
    `SendMessage`, which the Agent tool describes as resuming it with its
    context, once it ran. A stage does not stop on the refusal. A
    subagent that made no change may lose its worktree when it ends
    (unverified): when the path is gone, start the stage again.
  - After the merge it runs `delegate runlog end --run <run> --head <sha>`, and
    `delegate runlog grade --run <run> --outcome <outcome> --verify <result>` in
    place of `delegate orca-worker grade`. From the coordinator checkout, run
    `uv run tools/scripts/run.py worktree close <path>` after the worker has
    stopped. This removes the clean checkout and its branch once another
    local branch contains its `HEAD`. When refused, report the path and
    reason. Use `--force` only when the user explicitly chooses to discard
    commits that no other local branch preserves. It skips only that check.
    See [worktree commands](../../../../tools/scripts/worktree/README.md). The rest of the calling skill's after-stage
    steps run unchanged.
- Other editing work whose role's fit set holds no Claude model:
  - The coordinator does the units itself, one at a time, along the calling
    skill's path for a wave of one.
  - When the role `exclude`s the coordinator's model (`execute-sensitive`
    on a top-tier session), nothing runs: report the unit as blocked on
    Orca.
