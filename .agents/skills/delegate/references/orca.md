# Orca workers and full handoffs

Load this when a worker's state does not match what its tree says, when
`scripts/orca-worker.sh` fails a step, when `wait` prints anything but
`idle` or the screen shows an unexpected dialog, or for an orchestration run
or a full handoff. `SKILL.md` names the lane; this file is the procedure.

Contents: What the script does; When a step fails; Orchestration runs;
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
  `opencode` lane on a chosen model; a launch line can. opencode takes
  `--model provider/model` and runs its default agent.
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
- Copies the brief to `.orca-brief.md` in the child and sends a one-line
  pointer to it. A long paragraph through `orca terminal send` arrives as
  stray characters at the prompt, and the loss is silent at both ends. The
  file name is in the repository's `info/exclude`, so the child's tree
  stays clean. Orca reports `provider: unsupported` for opencode and
  cannot confirm delivery, so the script checks the screen for the
  pointer and sends it once more when it is missing. Once the terminal is up
  and before sending the pointer, `start` writes a `start` event to the run
  log with the lane metadata and base commit, storing `run` in the state file
  and printed JSON line.
- On a failure after the worktree exists, closes the terminal and removes
  the worktree. If either cleanup call fails, it keeps or writes lane state
  so `status` still lists the lane, and reports that it needs manual removal.
- `wait` does not trust `orca terminal wait --for tui-idle` alone: it was
  seen satisfied while an opencode worker was mid-turn. The turn has ended
  when two reads five seconds apart show no "esc to interrupt" or "esc to
  cancel" hint and the same screen. A screen that shows the hint unchanged
  for `--stall` seconds (default 1200) prints `stalled`; a terminal that
  cannot be read prints `exited`. With `--timeout` it prints `timeout`,
  and the worker is still at work.
- `keys` sends at most 200 characters without Enter, for dialog answers;
  longer text arrives with only its tail. `tell` copies a file into the
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

The behavior above holds for `opencode` on Orca 1.4.203, as of
2026-09-19. The `codex`, `agy`, and `claude` launches hold as of
2026-09-23, and the Codex launch with its hooks-review answer on codex
0.157.1 as of 2026-09-26.

## When a step fails

- `start` says `--role is required`: pass a registry role name.
- `start` says a lane with that name exists: a previous lane was not
  stopped. `status` lists it; `stop` it or pick another slug.
- `start` says the runtime is not reachable: the call ran sandboxed, or
  Orca is not open.
- `wait` prints `idle` and the screen shows a dialog: a permission prompt
  the brief anticipated is answered with `keys <slug> <text>`; anything
  else is reported to the user with the screen text.
- `wait` prints `stalled`: the screen showed a turn in progress and did not
  change for 20 minutes (`--stall <seconds>`), usually a hung model stream,
  which Escape does not reach on opencode. Read the screen first, since an
  `agy` or `opencode` tool call that prints nothing for that long looks the
  same. A hung stream is graded `blocked`, stopped with
  `stop <slug> --stalled`, and dispatched again; a dirty or unmerged
  checkout still stops `stop`, for a person to read.
- A Claude worker stops at a prompt to switch models or edit the prompt: a
  safety classifier flagged its request, and `orca-worker.sh` turns
  automatic switching off. Never pick switch. Report the flag and dispatch
  the work again on the next model in its own role's `fit` order, off
  Claude; an `execute` unit goes to `execute-sensitive`.
- `keys` says it takes 200 characters at most: write the text to a file and
  send it with `tell <slug> <file>`.
- `wait` keeps running on a quiet worker: rule out causes in cost order.
  The provider's quota (`pool-usage.sh`), then the screen for a prompt or
  a mangled instruction, then `git status` in the worker's checkout, where
  a written file with no commit means the worker is still testing.
- `read` shows only the end of a long report: the screen holds one frame.
  Prompt the worker to write its report to `REPORT.md` in its own
  worktree and reply with the path, read that file, and delete it before
  the merge.
- `stop` says the checkout is dirty: read what is there and report it; a
  worker that left files uncommitted is left in place.
- `stop` says the lane has no grade event: grade the lane with `orca-worker.sh grade`
  before stopping it.
- `stop` says the lane has child worktrees: the worker started lanes and
  left them. Each child's branch is merged into the lane (or dropped with
  the user's agreement) and the child removed before the lane is stopped;
  the ids it printed name them.
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
