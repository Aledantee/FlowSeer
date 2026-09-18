---
name: delegate
description: Choose where a FlowSeer skill sends delegated work (Explore, a project subagent, a Herdr worker on any installed agent CLI, or an Orca worker) on whichever model tier fits, and what the brief must contain. Load before dispatching any agent from plan, implement, review, compound, or steer.
user-invocable: false
---

# Delegate FlowSeer work

The coordinating session owns the result: a delegate returns evidence, a
finding list, or a branch, and the coordinator integrates it and runs the
verifier. Delegate when the work would flood this context or can run in
parallel. A question that one grep answers is not delegated.

## Pick the role, then resolve the lane

Delegated work is named by role, never by model. `.claude/models/registry.yaml`
maps each role to a fit set of models, each model to a prepaid pool, and
each pool to the CLI and the way it is pinned. `tune` keeps it true; when
its `as_of` is more than 30 days old, say so in the report and continue.

| Work | Role | Worker |
| --- | --- | --- |
| Lookup with no judgment: which files reference a symbol, which fixtures exist, where a string appears | `lookup` | `Explore` subagent, or the pool's CLI |
| Bounded question that needs conventions read and evidence weighed | `research` | `repo-researcher` subagent, or the pool's CLI |
| Editing work that runs for minutes: an implementation unit, a solution refresh | `execute`; `execute-sensitive` when a changed path matches `sensitive_paths` | Herdr worker when a server runs, else Orca worker, else a `general-purpose` subagent with `isolation: worktree` |
| Independent review of one unit's files | `review-unit` | `independent-reviewer` subagent, or the pool's CLI |
| Review of the seams between units, and the verdict | `review-seam` | `independent-reviewer` subagent |
| Tie-break between reviewers, verdict on a hard plan | `judge` | native subagent; never on a `sensitive` unit |
| Adversarial read of a plan | `critique` | the pool's CLI |
| A whole plan handed to someone else | the user's choice | Orca full handoff |

Resolve a role to a lane in this order, once per lane:

1. Drop models whose pool `host.local.yaml` shows `signed_in` false or
   null, or over 85% on any window.
2. Drop `zen` unless every fitting prepaid pool is hot. `zen` is per-token;
   a wave that reaches it says so.
3. Drop models the role `exclude`s. For `review-unit`, also drop the
   executor's vendor.
4. Move a pool that already holds a running lane of this wave to the back
   until that lane settles.
5. Take the model whose pool has the most headroom, and within ten points
   the one with the lower registry price. A signed-in pool that reports no
   window (`windows: null`) counts as full headroom, so it is taken before
   a Claude window with a number on it.

Step 4 spreads a wave: a six-unit `execute` wave with four pools signed in
runs on four pools, not six times on one model. The four prepaid pools
(`claude`, `codex`, `google`, `go`) are paid for whether used or not, so
the report names every fitting pool the wave left idle and why.

Pinning by pool: `claude` and `codex` take `--model` and `--effort`;
`google` takes `--model gemini-3.8-flash-<effort>` on the `agy` launch;
`go` and `zen` take the opencode agent named in the registry's
`opencode_agents`, whose model is fixed in `~/.config/opencode/opencode.json`.
A model whose `effort` list lacks the role's level gets the highest level it
lists: `execute` routes at `xhigh`, and `gemini-3.8-flash-xhigh` is not a
model id, so that lane launches as `gemini-3.8-flash-high`.
The Herdr wrapper puts each of these on the worker's launch line from
`--cli`, `--model`, `--effort`, and `--agent`; the Agent tool takes `model`.
Name the model on every worker; never `inherit` or unset, and never the
coordinating session's own model. One model per task from start to finish.
At most three workers run at once; start the next wave after the first
settles.

## Discover what this host offers

Before the first dispatch of a session:

```bash
.claude/skills/tune/scripts/discover-host.sh > .claude/models/host.local.yaml
```

The file records the agent CLIs present (`claude`, `codex`, `agy`,
`opencode`), whether Orca is reachable, each pool's sign-in state and
rate-limit windows, and the opencode model ids split by pool. Run it
unsandboxed: `orca` uses a local socket. Then `herdr agent list
>/dev/null && echo herdr up`, also unsandboxed: success means the Herdr
lane is open. Native subagents always run on Claude; a Herdr worker runs
on any installed agent whose pool is signed in.

## Dispatch by quota

`account list` reports `rateLimits` per provider: a `status` and windows
(`session`, `weekly`, `fableWeekly`, `monthly`) with `usedPercent` and
`resetsAt`. Reading it is a step of every dispatch, not advice before one:
run it immediately before each wave, and again first whenever a delegated
session goes quiet, because an exhausted pool is the cheapest of the four
causes of silence to rule out and the only one visible without touching the
worker. A window at 0% may have just rolled over; the `resetsAt` in the
same row says whether it did. The worst window of every provider:

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

- A pool is usable when it is signed in and every window it reports is under
  85%. Claude and Codex windows come from `account list`; `google` and `go`
  report none, so read their usage pages before a wave larger than three
  units and treat a 429 or a "limit reached" reply as the pool going hot
  for the rest of the wave. `go` meters in dollars ($12 per 5 hours, $30
  per week, $60 per month): a lane's estimated spend from the registry
  price counts against it.
- The coordinating session and every native subagent draw on the Claude
  pool, a Fable session also on `fableWeekly`. Past 85% there, keep native
  delegation to `review-seam` and `judge` and send the rest to the other
  prepaid pools.
- When no fitting pool is usable, do not dispatch: work sequentially or wait
  for the earliest `resetsAt`, and tell the user which window is exhausted.

## Herdr, Orca, or native

Read-only delegates (`lookup`, `research`, `review-*`, `judge`) stay
native subagents on every host. Editing work goes to a Herdr worker when a
server runs, to an Orca worker when only Orca is reachable, and to a
`general-purpose` subagent with `isolation: worktree` otherwise. Both
runtimes use a local socket the Bash sandbox blocks, so every `herdr` and
`orca` command runs with the sandbox disabled; a sandboxed call reports the
runtime as not running.

### Herdr worker

`scripts/herdr-worker.sh` is the whole procedure; `references/herdr.md`
says what it works around and what to do when a step fails.

```bash
s=.claude/skills/delegate/scripts/herdr-worker.sh
$s start --lane <slug> --cli <claude|codex|agy> --model <id> [--effort <level>] --brief <file>
$s start --lane <slug> --cli opencode --agent <opencode agent> --brief <file>
$s wait <slug>            # blocks; prints done, idle, blocked, or timeout; the dialog follows blocked
$s read <slug>            # the worker's report, from its pane
$s status                 # one line per live worker
$s stop <slug>            # after the merge: ends the agent, removes workspace and checkout
```

`start` exits 0 only when the worker exists on branch `<slug>`, branched
from this worktree's `HEAD`, and has the brief; a failure removes what it
created and says why. `wait` returns once with the agent's own settled
state. `done` and `idle` both mean the turn ended: check the tree, then
read the report. `blocked` means a dialog is on screen: read it, answer it
with `$s keys <slug> <key>` when the brief anticipated it, otherwise
report it. Then merge the branch here, run the verifier on the changed
paths, `$s stop <slug>`, `git branch -d <slug>`.

### Orca worker and full handoff

Orca is the fallback `execute` lane when no Herdr server runs, and the
lane for a full handoff either way; `references/orca.md` has both
procedures. Available when `ORCA_TERMINAL_HANDLE` is set and `orca status
--json` reports `runtime.reachable: true`.

### Reading a worker's report

A worker runs the focused tests of its package and commits on its branch;
it does not run the verifier. Before reading the report as fact, check
the tree: `git -C <child> log --oneline -1` shows the commit the report
names, `git -C <child> status --porcelain` is empty, and the two or three
changes most expensive to get wrong are what the report says. A report
describes what a session believes it did, and the gap between that and
the tree is where a silent tool failure lives. Then merge the worker's
branch into this worktree and run the verifier once, sandbox disabled, on
the union of changed paths. A worker on `agy` or `opencode` loads none of
the repository hooks, so first check that `git diff --name-only
<base>..<branch> -- generated buf.lock` prints nothing, with `<base>` the
commit the lane was started from. A child whose branch did not land stays, and
the report names it with the reason, so the user can read it before it
goes. Never remove a child with a dirty tree; say what is there.

## Write the brief

A delegate has none of this conversation. The brief states, in order:

1. The goal in one sentence and the definition of done. A requirement
   carried from the plan is quoted, and the brief says it is not the
   worker's to restate, narrow, or move to another fixture.
2. The files or diff to work from, as repository-relative paths; a reviewer
   gets the path of a diff file in the scratchpad directory.
3. The conventions that apply, as paths, and the matched `docs/solutions/`
   entries; for a reviewer, also the pinned version of every external
   convention or library those files rely on, so a finding is checked
   against the version in `go.mod` or `buf.lock` rather than the latest.
4. What to return: evidence with `path:line`, findings by severity with a
   failure scenario each, or the changed paths, the focused test command and
   its result, and the commit hash. Outcome first, no preamble, no closing
   summary, no narration while working, no word budget; the register below.
5. For a unit of a plan with a ledger (`verify-change`'s `SKILL.md`
   documents it), the `note` line of every landed unit, verbatim, and
   nothing else from the ledger.
6. The boundaries: no edits outside the named files, no changes to
   `AGENTS.md`, `buf.yaml`, `tools/hooks/`, `.claude/settings.json`,
   `generated/`, or `buf.lock`, no plan labels in code. Work is set aside
   with a temporary commit or a copy under `$TMPDIR`, never `git stash`:
   the stash stack is shared by every worktree and concurrent session, and
   a worker's checkout does not inherit the session note that says so.
7. For a worker on any runtime: do not ask questions, and when something
   blocks, state the blocker and stop. A requirement the worker believes
   the code cannot satisfy is a blocker, even when a nearby weaker one is
   within reach: a rewritten requirement passes the verifier and reads as
   success from here. A worker that waits on the
   coordinator looks, from here, exactly like one that is working. Editing
   subagents stay out of its checkout: a worker that spawns the Agent tool
   without worktree isolation gets its subagents' files in its own tree
   and reads them as a duplicate dispatch. Read-only subagents are fine.
8. For an Orca worker only, the paragraph in `references/orca-sandbox.md`
   on reaching Orca from inside the worker's sandbox, verbatim. A Herdr
   worker reports in its pane and needs nothing of the kind.

A claim about the codebase in a brief (an import direction, a call's
behavior, a field's existence) is marked verified with `path:line`, or
unverified for the worker to confirm; a coordinator's instruction is a fact
established elsewhere and decays like one, and a worker checking it before
building on it is expected. A decision against a delegate's proposal states
the failure it avoids in terms the delegate can check in the tree; a
preference can only be complied with, and a delegate that can only comply
also complies with the coordinator's mistakes. A ledger `note` records what
the next unit needs to know about what landed, not a rule: when an override
of a worker will recur, make the edit to the convention doc that governs the
file type its own reviewed change before the next dispatch, or leave the
override out of the brief, because a brief that
names a convention doc as authoritative and contradicts it in a note puts
the worker between two sources with no rule for which wins.

A brief for `Explore` or `repo-researcher` names the directories to search and
leaves out `docs/plans/` unless the question is about a plan; the tree, the
package README, and the `docs/architecture/` record describe what exists.

State intended behavior as a specification. Do not tell a reviewer that the
change is tested, safe, or believed correct, and do not forward commit text
that says so.

## Register

Coordinator–delegate text is re-read every later turn; length is paid many
times. Briefs, reports, worktree comments, check-ins: terse. No articles,
filler, pleasantries, hedging, narration. Fragments fine. Identifiers,
paths, errors, numbers exact; code unchanged. Prose only for ordered
sequences, warnings, irreversible actions. Report: outcome first, nothing
the brief said. Status: one line. Reasoning depth comes from the role's
effort level, not from text.
