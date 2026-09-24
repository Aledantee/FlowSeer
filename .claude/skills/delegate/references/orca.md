# Orca workers and full handoffs

Load this when a worker's state does not match what its tree says, when
`scripts/orca-worker.sh` fails a step, or for a full handoff. `SKILL.md`
names the lane; this file is the procedure.

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
- For Codex, answers two startup dialogs: the update offer (`3`, skip
  until next version) and the hooks review for a repository with
  `.codex/hooks.json` (`t`, then escape). The update offer is recognized
  by its "Skip until next version" option, since Codex keeps an "Update
  available!" banner on screen after the dialog is answered. A prompt sent
  into either dialog is lost, so the start fails if the review remains.
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
  when the screen shows no "esc interrupt" hint on two reads five seconds
  apart. With `--timeout` it prints `timeout`, and the worker is still at
  work.
- `grade` appends a `grade` event (`accepted`, `amended`, `rejected`, or
  `blocked`, with verifier outcome `pass`, `fail`, or `none`) for the lane's
  `run` to the run log. Re-grading appends another; scorers read the last.
- `stop` refuses a lane that has no `grade` event for its `run`, is mid-turn,
  whose checkout is dirty, or whose branch is not merged into this one. Before
  removing the lane, it writes an `end` event with the branch head to the run
  log. `orca worktree rm` deletes the branch with the checkout, so no `git
  branch -d` follows, and an unmerged lane removed that way would lose its
  commits.

Measured on 2026-09-19 on the `opencode` lane only (Orca 1.4.203). The
`claude`, `codex`, and `agy` launch lines and the Codex dialog handling
are the ones the earlier Herdr wrapper used; they have not been run
through this script yet.

## When a step fails

- `start` says `--role is required`: pass a registry role name.
- `start` says a lane with that name exists: a previous lane was not
  stopped. `status` lists it; `stop` it or pick another slug.
- `start` says the runtime is not reachable: the call ran sandboxed, or
  Orca is not open.
- `wait` prints `idle` and the screen shows a dialog: a permission prompt
  the brief anticipated is answered with `keys <slug> <text>`; anything
  else is reported to the user with the screen text.
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
- A codex worker still stops at the hooks review: Orca reports "Agent
  startup blocked: codex-hooks-review-prompt". Use another pool unless
  that dialog has been answered on this host.

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
Orca is not running means the worker sent its report from inside the
sandbox: `orca-sandbox.md` says how to settle it. `worker-release` closes
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
