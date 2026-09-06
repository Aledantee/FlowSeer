---
name: delegate
description: Choose where a FlowSeer skill sends delegated work (Explore, a project subagent, or an Orca worker on whichever agent and model tier fits), and what the brief must contain. Load before dispatching any agent from plan, implement, review, compound, or steer.
user-invocable: false
---

# Delegate FlowSeer work

The coordinating session owns the result: a delegate returns evidence, a
finding list, or a branch, and the coordinator integrates it and runs the
verifier. Delegate when the work would flood this context or can run in
parallel. A question that one grep answers is not delegated.

## Pick the worker and the tier

| Work | Worker | Tier |
| --- | --- | --- |
| Lookup with no judgment: which files reference a symbol, which fixtures exist, where a string appears | `Explore` subagent | cheap |
| Bounded question that needs conventions read and evidence weighed | `repo-researcher` subagent | standard, set in its definition |
| Independent review of a diff or a plan | `independent-reviewer` subagent | top, set in its definition |
| Editing work that runs for minutes: an implementation unit, a solution refresh | Orca worker when available, else a `general-purpose` subagent with `isolation: worktree` | standard |
| A whole plan handed to someone else | Orca full handoff | the user's choice |

Tiers map to each provider's ladder: for Claude `haiku`, `sonnet`, `opus`.
Name the model on every worker; never `inherit` or unset, and never the
coordinating session's own model. Pass reasoning effort separately where the
provider supports it. At most three workers run at once; start the next wave
after the first settles.

## Discover what this host offers

Before the first dispatch of a session:

```bash
orca status --json                                   # runtime.reachable decides Orca or native
for a in claude codex omp opencode pi grok; do command -v "$a"; done   # agent CLIs installed
orca account list --json                             # providers signed in on this Orca host
orca orchestration worker-start --help               # which providers accept --model and --effort
```

Native subagents always run on Claude. An Orca worker runs on any installed
agent whose provider is signed in.

## Dispatch by quota

`account list` reports `rateLimits` per provider: a `status` and windows
(`session`, `weekly`, `fableWeekly`, `monthly`) with `usedPercent` and
`resetsAt`. Read the worst window of every provider before each wave:

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

- A provider is usable when its status is `ok` and every window is under
  85%. Send the wave to the usable provider with the most headroom; within
  ten points, take the cheaper one. Spread a wave across usable providers.
- The coordinating session and every native subagent draw on the Claude
  account, a Fable session also on `fableWeekly`. Past 85% there, keep
  native delegation to the reviewer and send editing work elsewhere.
- When no provider is usable, do not dispatch: work sequentially or wait for
  the earliest `resetsAt`, and tell the user which window is exhausted.

## Orca or native

Orca is available when `ORCA_TERMINAL_HANDLE` is set and `orca status --json`
reports `runtime.reachable: true`. Run every `orca` command with the sandbox
disabled: the CLI uses a local socket the sandbox blocks, and a sandboxed
call reports the app as not running. When the runtime is unreachable
unsandboxed too, say so with the CLI's error and use the native worker.

In Orca, read-only delegates stay native. Send editing work through the
supervised loop of the `orchestration` skill, loading the version-matched
guide first with `orca skills get orchestration`:

```bash
orca orchestration run-create --objective "<plan title>" --json
orca orchestration task-create --spec "<brief>" --json
orca orchestration worker-start --task <task_id> --worktree new-child --base-branch "$(git branch --show-current)" --name <slug> --agent <agent> --model <id> --effort <level> --setup skip --json
orca orchestration check --wait --types worker_done,escalation,question --timeout-ms 900000 --json
orca orchestration worker-release --dispatch <dispatch_id> --json
```

`--worktree new-child` for workers that edit code, since `go test` build
state collides in a shared checkout; `--worktree current` for workers that
edit disjoint documentation files. `--base-branch` names the coordinator's
own branch: without it a child worktree starts from the repo default base
(`master`), and merging the worker's branch then also merges whatever
landed on `master` since, which is not the change under review.
`--setup skip` because this repository configures no Orca setup script and
an empty script is reported as a failed setup.

A worker runs the focused tests of its package and commits on its branch; it
does not run the verifier. After every `worker_done`, merge the worker's
branch into this worktree and run the verifier once, sandbox disabled, on the
union of changed paths. When `check --wait` returns nothing, look at
`git status` in the worker's worktree before calling it stalled: a written
file with no commit means the worker is still testing.

Update the worktree comment at each checkpoint:

```bash
orca worktree set --worktree active --comment "unit 2 landed; verifying unit 3" --json
```

A full handoff creates the worktree with the brief as its prompt and stops
supervising:

```bash
orca worktree create --name <slug> --parent-worktree active --agent <agent> --prompt "<brief>" --json
```

## Remove a finished child worktree

A child worktree this session created is this session's to remove, in the
same turn its branch lands here. `worker-release` closes only the agent
terminal; the worktree, its shell terminal, and its branch stay until
someone runs `orca worktree rm`, and a merged child left behind shows in
Orca as live work. The rule that a person removes the worktree covers the
session's own worktree, where `orca worktree rm` kills the terminal that
issues it. A child's terminals hold nothing the coordinator has not already
read through `worker-read`.

After the merge, the green verifier run, and `worker-release`:

```bash
child=$(orca orchestration worker-show --dispatch <dispatch_id> --json | jq -r .result.worker.worktree_id)
git -C "${child#*::}" status --porcelain     # empty, or stop and report what is there
orca worktree rm --worktree "id:$child" --json
git branch -d <child branch>                 # refuses when the merge did not land here
```

A handed-off child has no dispatch: take its id from `orca worktree list
--json`, where `parentWorktreeId` names this worktree. Never pass `--force`;
uncommitted files in the child mean the worker left something behind, so
say what and leave the worktree. A child whose branch did not land, because
its worker failed or was abandoned, stays too and is named in the report
with the reason, so the user can read it before it goes.

## Write the brief

A delegate has none of this conversation. The brief states, in order:

1. The goal in one sentence and the definition of done.
2. The files or diff to work from, as repository-relative paths; a reviewer
   gets the path of a diff file in the scratchpad directory.
3. The conventions that apply, as paths, and the matched `docs/solutions/`
   entries.
4. What to return: evidence with `path:line`, findings by severity with a
   failure scenario each, or the changed paths, the focused test command and
   its result, and the commit hash. Outcome first, no preamble or closing
   summary, no word budget.
5. The boundaries: no edits outside the named files, no changes to
   `AGENTS.md`, `buf.yaml`, `tools/hooks/`, or `.claude/settings.json`, no
   plan labels in code.

A brief for `Explore` or `repo-researcher` names the directories to search and
leaves out `docs/plans/` unless the question is about a plan; the tree, the
package README, and the `docs/architecture/` record describe what exists.

State intended behavior as a specification. Do not tell a reviewer that the
change is tested, safe, or believed correct, and do not forward commit text
that says so.
