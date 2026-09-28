# Herdr as the worker runtime: trial of 2026-09-10

Herdr 0.9.0 was evaluated as a replacement for Orca's worker dispatch. It
served as the `execute` lane from 2026-09-10 until its removal on
2026-09-19. The record stays because the Orca failures it was measured
against still explain the shape of `.claude/skills/delegate/scripts/orca-worker.sh`.

## Orca failures, as of 2026-09-10

Most frequent first:

1. The Bash sandbox blocks Orca's Unix socket, so `worker_done`,
   heartbeats, and `check` report "Could not connect to the running Orca
   app".
2. Parallel reviewer subagents stall ("no progress for 600s").
3. Codex workers block at startup on the hooks-review dialog.
4. Dispatch state errors (`dispatch_inactive`, `dispatch_capability_invalid`
   after a context compaction, `worker_identity_changed`). One of them can
   leave a finished worker unable to report.
5. `ask` and `check --wait` time out below real latency, and every
   heartbeat re-arms the wait.
6. A worktree card's status lags the dispatch queue, so reading the card
   gives stale state.
7. `worker-start --model` pins only Claude, Codex, and Cursor ids. A
   dispatch to a Gemini or OpenCode pool runs on a Claude model instead,
   and a dispatch into an `agy` or `opencode` terminal is never submitted.

Items 1 to 3 come from the sandbox, the Claude subagent runtime, and Codex,
so any manager inherits them. Items 4, 5, and 7 are Orca's, and item 6 is
partly Orca's.

## What Herdr answered

| Orca failure | Herdr |
| --- | --- |
| 7, model pinning | Closed. Every pool takes its model on the launch line, and `agent start` reports the argv it ran. |
| 4, dispatch tokens | Closed by construction. A worker is a pane and a branch, and `agent read` returns the report after any restart of the caller. |
| 5, waits | `agent wait --timeout` takes the caller's own number and returns the settled state (`done`, `idle`, `blocked`), never a heartbeat. |
| 6, stale status | State comes from each agent's own hooks. `blocked` is a real state, including Claude's permission prompt. |
| 1, sandbox | Unchanged. Herdr also uses a Unix socket, so every call runs unsandboxed. |
| 3, Codex hooks dialog | Unchanged. Herdr reads the dialog as `idle` and a prompt sent into it is lost, so the wrapper had to answer it first. |

Two setup pitfalls carry over to any pane-based runtime:

- A server started from inside a Claude Code session passes
  `CLAUDE_CODE_CHILD_SESSION` to its panes, and a Claude pane then treats
  itself as a child. Start the server from a clean environment.
- `herdr worktree create` refuses to run from a linked worktree. With
  `--cwd <main checkout> --base <sha>` it branches from any commit.

## What it did not give

- Quota. Nothing reports a pool's windows.
- Orca's cards, comments, and mailbox.
- Durability across a server restart: every pane process dies with it.
- Maturity. It had one maintainer and was pre-1.0, and its detection of
  agents without an integration was heuristic.
