# Herdr workers

Load this when dispatching an `execute` lane through Herdr, or when a Herdr
worker's state does not match what its tree says. `scripts/herdr-worker.sh`
carries the mechanics; this file is what it relies on and what to do when a
step fails. Measured on Herdr 0.9.0, 2026-09-10; the trial notes live in
`docs/research/herdr-trial-2026-09-10.md`.

## What Herdr is for here

Herdr is a terminal runtime with a socket API. A worker is an interactive
agent in a pane, in a worktree Herdr created, and its state is read from the
agent's own hooks (`herdr integration install claude|codex|opencode`) or,
for `agy`, from the screen. That gives the coordinator four things Orca's
`worker-start` does not:

- A worker on any installed CLI. `agent start --kind agy` and `--kind
  opencode` start and are tracked; the `google` and `go` pools run as
  supervised lanes, not as headless `bench.sh` runs.
- A `blocked` state. An approval dialog is reported as `blocked`, and
  `agent prompt --wait` returns it within seconds instead of waiting on a
  silent worker.
- No dispatch token. A worker reports through its pane and its branch; a
  coordinator compaction loses nothing it cannot read back with `agent
  read`.
- A `done` that means the turn ended, from the agent's own stop hook.

What it does not give: quota. `orca account list --json` stays the pre-wave
gate, and the model pin is on the launch line the script builds.

## Setup, once per machine

```bash
herdr integration install claude
herdr integration install codex
herdr integration install opencode
```

Each install edits that agent's global hook file (`~/.claude/settings.json`,
`~/.codex/hooks.json`, `~/.config/opencode/plugins/`); the Codex one adds a
SessionStart hook that Codex asks to review on first launch, which the
script answers. Start the server from a plain terminal, never from inside a
Claude Code session: panes inherit the server's environment, and a worker
started under `CLAUDE_CODE_CHILD_SESSION` runs with transcript saving off.

```bash
env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_CHILD_SESSION herdr server &
herdr status server
```

A server restart loses every running pane process; do not restart one with
live workers.

## Pitfalls the script works around

- `herdr worktree create` refuses to run from a linked worktree
  (`linked_worktree_source`). The script resolves the base to a commit and
  creates from the repository's main checkout with `--cwd`, so a child still
  branches from the coordinator's `HEAD`.
- The brief goes to `<root>/briefs/<lane>.md`, beside the worktrees, so the
  worker's tree stays clean. OpenCode asks permission to read outside its
  directory; `agent prompt --wait` returns `blocked` and the script's
  caller answers `enter` (Allow once) after reading the dialog.
- Codex on a repository with `.codex/hooks.json` opens a hooks-review
  dialog that Herdr reports as `idle`/`done`, not `blocked`; a prompt sent
  into it is swallowed. The script trusts and closes it before prompting.
- `agy` drops the first submission now and then: `agent_prompt_stalled`
  with the input line empty. The script retries once.
- `agent prompt --wait --timeout N` returns `timeout` when the agent has not
  settled by N ms; that is not a failure, the agent is `working`. Use
  `wait` with a long timeout for the settled state.
- Every `herdr` command needs the sandbox off, like `orca`.

## Reading a settled worker

`wait` prints `done`, `idle`, or `blocked`. `done` and `idle` both mean the
turn ended; the report is in `read` and the commit is in the tree. Check
the tree before the report, as `SKILL.md` says for any worker. `blocked`
prints the visible pane: read the dialog, answer it with `keys` only when
it is the kind of prompt the brief anticipated, otherwise report it.

## Removing a lane

After the branch has been merged here and verified:

```bash
scripts/herdr-worker.sh stop <lane>
git branch -d <lane>
```

`stop` ends the agent, removes the Herdr workspace, and removes the
worktree checkout; it refuses a dirty tree, which then needs a person's
look.
