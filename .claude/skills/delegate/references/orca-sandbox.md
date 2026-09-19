# Orca workers and the Bash sandbox

Load this when writing the brief for a worker started through `orca
orchestration`, when such a worker finished without sending `worker_done`,
or when setting up a machine so workers can reach Orca sandboxed. A worker
started by `scripts/orca-worker.sh` never calls `orca` and needs none of it.

## The brief paragraph

Copy this into every orchestration worker's brief verbatim:

> Every `orca` command (`orchestration send`, `check`, `ask`,
> `heartbeat`, `worker_done`) must run through the Bash tool with the
> parameter `dangerouslyDisableSandbox` set to `true`. The sandbox blocks
> Orca's local socket, and a sandboxed call reports "Orca is not running"
> even though it is. `buf generate` and the verifier script need the same
> setting. Send `worker_done` that way, once, with the injected task and
> dispatch ids.

The brief itself goes in a file under the worker's worktree and the
terminal gets a one-line pointer to it: a long paragraph through `orca
terminal send` arrives as stray characters at the prompt, and the loss is
silent at both ends.

## Settling a worker that could not report

A worker that did not get the paragraph finishes its work and cannot
report it. The signs: a clean tree, a final commit, and an agent that says
Orca is not running. Read its terminal tail for the summary, merge the
branch, `worker-stop` then `worker-abandon` the dispatch, and mark the
task completed by hand.

## The per-machine fix

Orca's control socket lives under `~/Library/Application Support/orca/`
with a name that changes on every app restart, so no path allowlist holds.
Setting `{"sandbox":{"network":{"allowAllUnixSockets":true}}}` in the
user's `~/.claude/settings.json` lets every session and worker reach Orca
sandboxed, with no restart. It is a user setting because the path is
machine-specific; nothing in the repository can carry it.
