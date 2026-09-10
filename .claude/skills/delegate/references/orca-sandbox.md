# Orca workers and the Bash sandbox

Load this when an Orca worker finished without sending `worker_done`, or
when setting up a machine so workers can reach Orca sandboxed. The brief
paragraph in `SKILL.md`, "Write the brief", item 7, is what every worker
gets; this file is the background.

A worker that does not get that paragraph finishes its work and cannot
report it; the coordinator then finds a committed branch with no
`worker_done`, settles the dispatch by hand, and loses the worker's
summary. The signs: a clean tree, a final commit, and an agent that says
Orca is not running. Read its terminal tail for the summary, merge the
branch, `worker-stop` then `worker-abandon` the dispatch, and mark the task
completed by hand.

The durable fix is per machine, not per call: Orca's control socket lives
under `~/Library/Application Support/orca/` with a name that changes on
every app restart, so no path allowlist holds. Setting
`{"sandbox":{"network":{"allowAllUnixSockets":true}}}` in the user's
`~/.claude/settings.json` lets every session and worker reach Orca
sandboxed, with no restart. It is a user setting because the path is
machine-specific; nothing in the repository can carry it.
