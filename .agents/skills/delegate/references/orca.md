# Orca workers and full handoffs

Load this when a worker's state does not match what its tree says, when
`scripts/orca-worker.sh` fails a step, when `wait` prints anything but
`idle` or `done`, or the screen shows an unexpected dialog, or for an orchestration run
or a full handoff. `SKILL.md` names the lane; this file is the procedure.

Contents: What the script does; A second terminal in one worktree; When a step fails; Orchestration runs;
Full handoff.

## What the script does

- Creates a child worktree with `orca worktree create --parent-worktree
  active --base-branch <this branch> --setup skip`. Without `--base-branch`
  the child starts from `main`, and merging it then also merges whatever
  landed on `main` since. `--setup skip` because this repository
  configures no Orca setup script, and an empty script is reported as a
  failed setup. Orca prefixes the branch with the git user
  (`<user>/<slug>`); the script's JSON line carries the real name.
- Starts the agent with `orca terminal create --command "<launch line>"`
  and the model on that line. `orca orchestration worker-start --model`
  pins Claude, Codex, and Cursor ids only, so it cannot start an `agy` or
  `omp` lane on a chosen model; a launch line can. omp pins the model with
  `--model`, sets reasoning effort with `--thinking <level>`, has no agent,
  and reports its cost inline.
- Unsets `CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`, and
  `CLAUDE_CODE_CHILD_SESSION` on the launch line: a Claude worker started
  under the coordinator's child-session variables runs with transcript
  saving off.
- For Codex, launches with `-c check_for_update_on_startup=false` and
  answers "Hooks need review", which shows when a hook in
  `.codex/hooks.json` or `~/.codex/hooks.json` is new or changed, with
  "Trust all and continue": the number on that option's line, then Enter.
  The number is read from the screen because Codex renumbers its dialogs:
  answering the update offer broke that way in 0.157.0, running the
  upgrade and leaving the terminal at a shell. A prompt sent into the
  review is lost, and its Enter opens a hook's detail view, so the start
  fails if the review remains or has no trust option, the update offer
  shows, or the screen asks to restart Codex.
- Waits up to 90 seconds for the agent's TUI to go idle before it sends
  anything. A timed-out `orca terminal wait` prints a normal result with
  `wait.satisfied` false (`orca skills get orca-cli`), and that result gets
  one more wait of 180 seconds, since a Claude TUI started beside several
  others can take longer and a pointer sent into a TUI that is still
  starting is lost. A wait that prints no `satisfied` field is not
  repeated: it fails that way within seconds for a running `agy` terminal.
- Refuses an empty brief before it creates the worktree.
- Copies the brief to `.orca-brief.md` in the child and sends a one-line
  pointer to it. A long paragraph through `orca terminal send` arrives as
  stray characters at the prompt, and the loss is silent at both ends. The
  file name is in the repository's `info/exclude`, so the child's tree
  stays clean. Orca reported `provider: unsupported` for the earlier
  opencode lane and could not confirm delivery, so the script checks the
  screen for the pointer and sends it once more when it is missing. Once the terminal is up
  and before sending the pointer, `start` writes a `start` event to the run
  log with the lane metadata and base commit, storing `run` in the state file
  and printed JSON line.
- With `--join <lane>`, skips `orca worktree create` and runs everything
  else above in the worktree of a kept lane, so the new lane has its own
  terminal, `run`, and state file. `stop <lane> --keep-worktree` is what
  keeps a lane: it closes the terminal, logs `end`, and leaves the
  worktree, with the state file's `terminal` emptied and `"kept": true`.
  A start that fails after a join closes its terminal and never removes the
  worktree. A `stop` without the flag removes the worktree and every state
  file that names it.
- On a failure after the worktree exists, closes the terminal and removes
  the worktree. If either cleanup call fails, it keeps or writes lane state
  so `status` still lists the lane, and reports that it needs manual removal.
- `wait` does not trust `orca terminal wait --for tui-idle` alone: it was
  seen satisfied while a worker was mid-turn. The lane and each child are
  quiet only after two reads five seconds apart show no "esc to interrupt"
  or "esc to cancel" hint and the same screen. It keeps waiting while a child
  works or its screen changes. When the lane is quiet and no child works,
  `--until <command>` runs first in the lane checkout. Success prints `done`.
  Without `--until`, a quiet lane without children prints `idle`, or
  `limited` when the last 30 lines of its screen name a rate limit, a usage
  limit, an exceeded, exhausted, or reached quota, or a reset time. The word `quota`
  alone does not match, since a finished report on quota code names it. A failing
  command falls through to `idle` or `limited` without children or to `idle-children
  <names>` after the child-idle clock. The child-idle clock is `--stall`
  seconds, 1200 by default, and restarts when the lane's screen changes or a
  child works. A child without a state file is named by its raw worktree id. A
  child without a readable terminal is named by its lane name. A failed child
  query is named `unreadable`. These
  cases count as quiet. `stalled` means the lane is still working, no child is
  working, and the lane screen stayed unchanged for `--stall` seconds.
  `timeout` is the positive `--max` ceiling. `--max` defaults to 3600 seconds,
  and `--stall 0` disables the stalled and child-idle clock. `--timeout` is
  refused. The screen follows every outcome except an `exited` result caused
  by a failed screen read. An `exited` status from `terminal wait` is followed
  by the screen.
- `line --cli <cli> --model <id> [--effort <level>]` prints the launch line
  used by `start`. Codex lines include
  `-c background_terminal_max_timeout=3600000`, so a coordinator can poll
  the blocking wait for up to one hour.
- `check` reads the lane's `cli` from state and the `model` and `at` values
  from its `start` event. On a Claude lane it checks the session files for
  another model or a refusal fallback. Other lanes print `not checked: <cli>`
  and succeed. `grade --outcome accepted` and `amended` repeats the same
  check before writing the grade. A failed check is graded `rejected`, left
  unmerged, and dispatched again under the model-switch rule.
- `keys` sends at most 200 characters without Enter, for dialog answers. A
  message the worker must act on goes through `tell`, which submits it.
  Longer text through `keys` arrives with only its tail. `tell` copies a file into the
  checkout as `.orca-note.md` and submits a pointer to it, as `start` does
  with the brief, and fails when no new pointer reaches the screen.
- `grade` appends a `grade` event (`accepted`, `amended`, `rejected`, or
  `blocked`, with verifier outcome `pass`, `fail`, or `none`) for the lane's
  `run` to the run log. Re-grading appends another; scorers read the last.
- `stop` refuses a lane that has no `grade` event for its `run`, is
  mid-turn (unless `--stalled`, after `wait` printed `stalled`),
  whose checkout is dirty, whose branch is not merged into this one, or
  whose worktree still has Orca child worktrees of its own: Orca drops a
  removed worktree's lineage, so those children would turn top-level. Before
  removing the lane, it writes an `end` event with the branch head to the run
  log. `orca worktree rm` deletes the branch with the checkout, so no `git
  branch -d` follows, and an unmerged lane removed that way would lose its
  commits.

The lane-only behavior above was measured on the earlier `opencode` lane on
Orca 1.4.203. The newer `--until`, `--max`, and `line` behavior is unverified,
as are child-aware waiting and the Codex poll cap on a live Orca. The Codex
launch and its hooks-review answer were measured on codex 0.157.1. The `agy`,
`omp`, and `claude` lanes start through this script, but their behavior here is
not measured.

## A second terminal in one worktree

`start --join <lane>` opens a new terminal with `orca terminal create
--worktree id:<id>` in the worktree of a lane that `stop --keep-worktree`
left behind, whose first terminal is closed. Whether Orca accepts that, and
whether the new terminal's checkout is the kept lane's path and branch, is
what a `drive` that joins its stages depends on.

Observed on Orca 1.4.221, with two `claude` lanes: after `stop <first>
--keep-worktree`, `git worktree list` still named the checkout and `status`
printed the lane as `kept`. `start --lane <second> --join <first>` exited 0,
and its JSON line carried the first lane's `worktree`, `path`, and `branch`
with a new `terminal` and `run`. The second worker took its brief there, and
a `stop <second>` without the flag removed the checkout, the branch, and
both state files. `orca terminal show` on the first terminal afterwards
reports it `orphaned` with `exitCause.kind: operator_close`, so a closed
terminal's handle stays readable and is not reused. Other Orca versions and
the `codex`, `agy`, and `omp` lanes are unverified for a join.

## When a step fails

- `start` says `--role is required`: pass a registry role name.
- `start` says a lane with that name exists: a previous lane was not
  stopped. `status` lists it; `stop` it or pick another slug.
- `start` says the runtime is not reachable: the call ran sandboxed, or
  Orca is not open.
- `wait` prints `idle` and the screen shows a dialog: a permission prompt
  the brief anticipated is answered with `keys <slug> <text>`; anything
  else is reported to the user with the screen text.
- `wait` prints `limited`: the lane is quiet and its screen names a limit.
  Read the screen. A worker waiting for its pool's window to reset has not
  finished: read the pool's row with `pool-usage.sh` and wait again with
  `--max` past the reset, or grade the lane `blocked` and dispatch the work
  on another pool. A finished report that only mentions a limit is handled
  as `idle`. The match is a broad pattern, since no CLI's wording of that
  wait was captured (unverified).
- `stop` says a branch has commits that are not merged or kept on
  `parked/<slug>`: merge the branch, or park it as
  `drive/references/parking.md` describes, then run `stop` again.
- `wait` prints `idle-children <names>`: read the parent lane and each named
  child, then `tell` a lane that stopped waiting to continue. A state-file
  name is a lane slug and can be passed to `read`. A child without a state
  file is shown as its raw worktree id and has no terminal handle for this
  script to read. `unreadable` means the child query failed, so check Orca and
  run the wait again. `read unreadable` is not a valid lane lookup.
- `wait` prints `stalled`: the screen showed a turn in progress and did not
  change for 20 minutes (`--stall <seconds>`), usually a hung model stream
  that Escape may not reach. Read the screen first, since an `agy` or `omp`
  tool call that prints nothing for that long looks the same. A hung stream is graded `blocked`, stopped with
  `stop <slug> --stalled`, and dispatched again; a dirty or unmerged
  checkout still stops `stop`, for a person to read.
- A Claude worker stops at a prompt to switch models or edit the prompt: a
  safety classifier flagged its request, and `orca-worker.sh` turns
  automatic switching off. Never pick switch. Report the flag and dispatch
  the work again on the next model in its own role's `fit` order, off
  Claude; an `execute` unit goes to `execute-sensitive`.
- `keys` says it takes 200 characters at most: write the text to a file and
  send it with `tell <slug> <file>`.
- `wait` keeps running on a quiet worker, or prints `timeout`: rule out
  causes in cost order once. The provider's quota (`pool-usage.sh`), then
  the screen for a prompt or a mangled instruction, then `git status` in the
  worker's checkout, where a written file with no commit means the worker is
  still testing. Wait again after that check.
- `read` shows only the end of a long report: the screen holds one frame.
  Prompt the worker to write its report to `REPORT.md` in its own
  worktree and reply with the path, read that file, and delete it before
  the merge.
- `stop` says the checkout is dirty: read what is there and report it. A
  worker whose terminal is live and left files uncommitted is left in
  place.
- `stop` says the checkout is dirty and the lane's terminal has exited:
  nothing will commit that work, and the lane blocks the `stop` of its
  parent. `wait` printing `exited` or `status` printing `gone` is not
  proof, since both also follow a failed screen read on a live lane.
  Confirm it with the `terminal` handle from the lane's state file under
  `<git common dir>/orca-workers/`, unsandboxed:
  `orca terminal show --terminal <handle> --json` reads status `exited`.
  Without that, report the lane and leave it. With it, park the lane. Commit
  the tree in the child, keep the commit on `parked/<slug>`, grade the lane
  `blocked`, and stop it. The report names the branch, where the work stays
  for a person to read. Whether `orca terminal close` succeeds on a
  terminal that has already exited is unverified. When `stop` then says the
  terminal close failed, report the lane and leave it.

  ```bash
  git -C <child> add -A
  git -C <child> commit -m "wip: uncommitted work of exited lane <slug>"
  git branch parked/<slug> <lane branch>
  .claude/skills/delegate/scripts/orca-worker.sh grade <slug> --outcome blocked --verify none --note "exited dirty; work on parked/<slug>"
  .claude/skills/delegate/scripts/orca-worker.sh stop <slug>
  ```
- `stop` says the lane has no grade event: grade the lane with `orca-worker.sh grade`
  before stopping it.
- `stop` says the lane has child worktrees: the worker started lanes and
  left them. Each child's branch is merged into the lane (or dropped with
  the user's agreement) and the child removed before the lane is stopped;
  the ids it printed name them. A child whose terminal exited with a dirty
  checkout is parked as above, so it does not hold the lane.
- `start` says the brief is empty: the brief file has no bytes. Write the
  brief and start the lane again.
- `start` says the hooks review remains or has no "Trust all and
  continue" option: Codex changed the dialog again. Its screen is in the
  error; update the match in `orca-worker.sh`, or run `codex` in the
  child once by hand and trust the hooks, then start the lane again.
- `start` says Codex updated itself and exited, or showed its update
  offer despite `check_for_update_on_startup=false`: run `codex` by hand
  once to finish or dismiss the update, or set
  `check_for_update_on_startup = false` in `~/.codex/config.toml`, then
  start the lane again. Use another pool if the offer still shows.

Update the worktree comment at each checkpoint:

```bash
orca worktree set --worktree active --comment "unit 2 landed; verifying unit 3" --json
```

## Orchestration runs

`orca orchestration` adds task dependencies, mail between agents, and
decision gates. Use it when a wave needs those, on `claude` or `codex`
only, and from the coordinator terminal of the run: `worker-start` is
refused anywhere else (`consumer_fenced`). Load the version-matched guide
with `orca skills get orchestration`, then:

```bash
orca orchestration run-create --objective "<plan title>" --json
orca orchestration task-create --spec "<brief>" --json
orca orchestration worker-start --task <task_id> --worktree new-child --base-branch "$(git branch --show-current)" --name <slug> --agent <claude|codex> --model <id> --effort <level> --setup skip --json
orca orchestration check --wait --types worker_done,escalation,question --timeout-ms 900000 --json
orca orchestration worker-release --dispatch <dispatch_id> --json
```

`check --wait` is re-armed by heartbeats and returns before a long worker
finishes; reissue it. A clean tree, a final commit, and an agent that says
Orca is not running means the worker, lacking the brief paragraph
`SKILL.md` item 8 requires, could not report from inside the sandbox.
Settle it: read its terminal tail for the summary, merge the branch,
`worker-stop` then `worker-abandon` the dispatch, and mark the task
completed by hand. `worker-release` closes
only the agent terminal, so remove the merged child afterwards:

```bash
child=$(orca orchestration worker-show --dispatch <dispatch_id> --json | jq -r .result.worker.worktree_id)
git -C "${child#*::}" status --porcelain     # empty, or stop and report what is there
orca worktree rm --worktree "id:$child" --json
```

A handed-off child has no dispatch: take its id from `orca worktree list
--json`, where `parentWorktreeId` names this worktree. Never pass
`--force`. The session's own worktree is a person's to remove: `orca
worktree rm` on it kills the terminal that issues it.

## Full handoff

A full handoff creates the worktree with the brief as its prompt and stops
supervising. The brief carries the whole ledger when one exists, since the
child worktree has its own git directory:

```bash
orca worktree create --name <slug> --parent-worktree active --agent <agent> --prompt "<brief>" --json
```

`--agent` accepts no flags, so a handoff on a chosen model is a worktree
without `--agent` followed by `orca terminal create --command "<launch
line>"`, with the launch lines `orca-worker.sh` uses.
