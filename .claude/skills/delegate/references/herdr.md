# Herdr workers

Load this when a Herdr worker's state does not match what its tree says,
when `scripts/herdr-worker.sh` fails a step, or when setting up a machine.
The measurements behind it are in `docs/research/herdr-trial-2026-09-10.md`.

## Setup, once per machine

```bash
herdr integration install claude
herdr integration install codex
herdr integration install opencode
```

Each install adds a hook to that agent's global config
(`~/.claude/settings.json`, `~/.codex/hooks.json`,
`~/.config/opencode/plugins/`). The hook is what makes `done` and `idle`
the agent's own report; `agy` has no integration and is read from the
screen.

Start the server from a plain terminal. Panes inherit the server's
environment, and a Claude worker started under
`CLAUDE_CODE_CHILD_SESSION` runs with transcript saving off.

```bash
env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_CHILD_SESSION herdr server
```

A server restart ends every pane process. Run `scripts/herdr-worker.sh
status` first; it prints `no live agents` when a restart is safe.

## What the script does

- Creates the worktree from the repository's main checkout with `--cwd`
  and the base resolved to a commit, because `herdr worktree create`
  refuses a linked worktree as its source (`linked_worktree_source`). The
  checkout lives at `<repo parent>/worktrees/<repo>/<slug>`.
- Starts the agent with the model on its launch line, then, for Codex,
  answers two startup dialogs: the update offer (key `2`, then enter) and
  the hooks review for a repository with `.codex/hooks.json` (`t`, then
  `esc`). Herdr reports the second as `idle`, and a prompt sent into it is
  lost, so the screen is read again afterwards and the start fails if a
  dialog remains.
- Submits the brief text as one prompt. `agy` drops a first submission
  now and then (`agent_prompt_stalled`, input line empty); the script
  sends it once more, then fails the start.
- On any failure after the worktree exists, removes the workspace, the
  checkout, and the branch, and says why.
- `wait` passes `--timeout` through when given and waits indefinitely
  otherwise; Herdr's `agent wait` has no default timeout of its own. With
  a timeout it prints `timeout`, and the worker is still at work.
- `stop` sends up to three interrupts, confirms the agent is gone, then
  removes the workspace and checkout with `git worktree remove` under the
  hood, so `git branch -d <slug>` follows directly.

## When a step fails

- `start` says a live agent with that name exists: a previous lane was
  not stopped. `status` lists it; `stop` it or pick another slug.
- `wait` prints `blocked`, followed by the pane: read the dialog. A
  permission prompt the brief anticipated is answered with `keys <slug>
  enter`; anything else is reported to the user with the pane text.
- `wait` prints `unknown`: Herdr sees an agent it cannot classify. `read`
  the pane; a prompt line with no activity means the turn ended and the
  hook did not fire, so treat it as `idle` after the tree check.
- `read` shows nothing after a finished turn: the agent drew on the
  alternate screen and the response left no scrollback. Prompt it to
  write its report to `REPORT.md` in its own worktree and reply with the
  path, read that file, and delete it before the merge.
- `stop` says the agent is still running: read its pane; a worker in the
  middle of a turn is left to finish or interrupted by hand.
- `stop` says the removal was refused, with Herdr's error: a dirty
  checkout is the usual cause. Read what is there and report it; a worker
  that left files uncommitted is left in place.
