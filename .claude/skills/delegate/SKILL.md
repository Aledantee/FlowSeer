---
name: delegate
description: Choose where a FlowSeer skill sends delegated work (Explore, a project subagent, or an Orca worker on whichever agent and model tier fits), and what the brief must contain. Load before dispatching any agent from plan, implement, review, or compound.
user-invocable: false
---

# Delegate FlowSeer work

The coordinating session owns the result. A delegate returns evidence, a
finding list, or a branch; the coordinator integrates it and runs the
verifier. Delegate when the work would otherwise flood this context or can
run in parallel. A question that one grep answers is not delegated.

## Pick the worker and the tier

| Work | Worker | Tier |
| --- | --- | --- |
| Lookup with no judgment: which files reference a symbol, which fixtures exist, where a string appears | `Explore` subagent | cheap |
| Bounded question that needs conventions read and evidence weighed | `repo-researcher` subagent | standard, set in its definition |
| Independent review of a diff or a plan | `independent-reviewer` subagent | top, set in its definition |
| Editing work that runs for minutes: an implementation unit, a solution refresh | Orca worker when available, else a `general-purpose` subagent with `isolation: worktree` | standard |
| A whole plan handed to someone else | Orca full handoff | the user's choice |

Tiers map to each provider's own ladder. For Claude that is `haiku`,
`sonnet`, `opus`; the coordinating session's own model is never a worker
tier. For another provider use the ids its CLI documents, and pass the
reasoning effort separately where the provider supports it.

Planning, integration, and verification stay in the coordinating session.
Name the model on every worker. Never use `inherit` or leave the model
unset: the coordinating session may be running the most expensive model,
and none of the work above needs it.

At most three workers run at once. Start the next wave after the first
settles.

## Discover what this host offers

Do not assume the worker is Claude. Before the first dispatch of a session,
find out which agents and providers are actually available:

```bash
orca status --json                                   # runtime.reachable decides Orca or native
for a in claude codex omp opencode pi grok; do command -v "$a"; done   # agent CLIs installed
orca account list --json                             # providers signed in on this Orca host
orca orchestration worker-start --help               # which providers accept --model and --effort
```

Native subagents always run on Claude. An Orca worker runs on any installed
agent whose provider is signed in.

## Dispatch by quota

`account list` reports `rateLimits` per provider: a `status`, and windows
(`session`, `weekly`, `fableWeekly`, `monthly`) with `usedPercent` and
`resetsAt`. Read the worst window of every usable provider before each
wave, not before each worker:

```bash
orca account list --json | python3 -c '
import json, sys
limits = json.load(sys.stdin)["result"]["rateLimits"]
for name, v in limits.items():
    if not isinstance(v, dict) or v.get("status") != "ok":
        continue
    windows = [(k, w["usedPercent"], w["resetDescription"]) for k, w in v.items()
               if isinstance(w, dict) and "usedPercent" in w]
    print(name, max(windows, key=lambda t: t[1]) if windows else "no window data")'
```

Rules:

- A provider is usable when its status is `ok` and every reported window is
  under 85%. Send the wave to the usable provider with the most headroom;
  when two are within ten points, take the cheaper one.
- The coordinating session and every native subagent draw on the Claude
  account, and a Fable session also on `fableWeekly`. When Claude's worst
  window passes 85%, keep native delegation to the reviewer and send editing
  work to another usable provider.
- When no provider is usable, do not dispatch. Do the work sequentially in
  this session or wait for the earliest `resetsAt`, and tell the user which
  window is exhausted and when it resets.
- Spread a wave across providers when more than one is usable, so one
  account's limit does not stall the whole wave.

## Orca or native

Orca is available when `ORCA_TERMINAL_HANDLE` is set and `orca status --json`
reports `runtime.reachable: true`. The CLI reaches the app over a local
socket that the Bash sandbox blocks, so run every `orca` command with the
sandbox disabled; a sandboxed call reports the app as not running even from
inside an Orca terminal. When the runtime is unreachable outside the
sandbox too, say so with the CLI's error and use the native worker from the
table; do not guess at repairs to Orca's own state.

In Orca, read-only delegates stay native: a subagent loads its definition
and nothing else, while an Orca worker is a full agent session. Send
editing work to Orca through the supervised loop of the `orchestration`
skill; load the version-matched guide first with
`orca skills get orchestration`. The shape is:

```bash
orca orchestration run-create --objective "<plan title>" --json
orca orchestration task-create --spec "<brief>" --json
orca orchestration worker-start --task <task_id> --worktree new-child --name <slug> --agent <agent> --model <id> --effort <level> --setup run --json
orca orchestration check --wait --types worker_done,escalation,question --timeout-ms 900000 --json
orca orchestration worker-release --dispatch <dispatch_id> --json
```

Use `--worktree new-child` for workers that edit code: each runs `go test`
and the verifier, which write build state and receipts that collide in a
shared checkout. Workers that edit disjoint documentation files use
`--worktree current`. After every `worker_done`, merge the worker's branch
into this worktree and run the verifier on the union of changed paths here.
A worker's own green run does not count for the integrated result.

Update the worktree comment at each checkpoint so the Orca board shows
where the work stands:

```bash
orca worktree set --worktree active --comment "unit 2 landed; verifying unit 3" --json
```

A full handoff creates the worktree with the brief as its prompt and stops
supervising:

```bash
orca worktree create --name <slug> --parent-worktree active --agent <agent> --prompt "<brief>" --json
```

## Write the brief

A delegate has none of this conversation. The brief states, in this order:

1. The goal in one sentence and the definition of done.
2. The files or diff to work from, as repository-relative paths. A reviewer
   gets the path of a diff file in the scratchpad directory.
3. The conventions that apply, as paths (`docs/code-style.md`,
   `docs/doc-style.md`, the `docs/architecture/` record), and the matched
   `docs/solutions/` entries.
4. What to return: evidence with `path:line`, findings by severity with a
   failure scenario each, or the changed paths plus the verifier command
   that was run.
5. The boundaries: no edits outside the named files, no changes to
   `AGENTS.md`, `buf.yaml`, `tools/hooks/`, or `.claude/settings.json`, and
   no plan labels in code.

A brief for `Explore` or `repo-researcher` names the directories to search
and leaves out `docs/plans/` unless the question is about a plan. Plans
record decisions as they stood at planning time; the tree, the package
README, and the `docs/architecture/` record describe what exists.

Describe intended behavior as a specification. Do not tell a reviewer that
the change is tested, safe, or believed correct, and do not forward commit
text or comments that say so. The reviewer judges the diff alone.
