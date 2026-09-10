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

Resolve a role to a lane in this order: drop models whose pool
`host.local.yaml` shows `signed_in` false or null, or over 85% on any
window; drop `zen` unless every fitting prepaid pool is hot; drop models
the role `exclude`s; for `review-unit`, drop the executor's vendor; then take
the model whose pool has the most headroom, and within ten points the one
with the lower registry price. A signed-in pool that reports no window
(`windows: null`) counts as full headroom, so it is taken before a Claude
window with a number on it. Assignment is per lane, not per wave: a pool
that already holds a running lane in this wave drops to the back until that
lane settles, so a six-unit `execute` wave with four pools signed in runs on
four pools, not six times on Sonnet or six times on Gemini. Four pools are
prepaid (`claude`, `codex`, `google`, `go`); an unspent window is waste.
`zen` is per-token, and a wave that reaches it says so. The report names
every fitting pool the wave left idle and why.

Pinning by pool: `claude` and `codex` take `--model` and `--effort` on the
Orca dispatch line or the Agent tool's `model`; `google` takes
`--model gemini-3.8-flash-<effort>` on the `agy` launch; `go` and `zen` take
the opencode agent named in the registry's `opencode_agents`, whose model is
fixed in `~/.config/opencode/opencode.json`. A Herdr worker takes all four
on its launch line, which is why it is the first choice for `execute`:
`scripts/herdr-worker.sh start --cli <pool cli> --model <id> [--effort
<level>] [--agent <opencode agent>]` builds it. Orca's `worker-start` pins
Claude, Codex, and Cursor ids only, and a dispatch into an `agy` or
`opencode` terminal is never submitted (checked 2026-09-09), so without a
Herdr server a `google` or `go` lane runs headless instead: create the
child worktree with `git worktree add -b <slug> <path> HEAD`, then

```bash
.claude/skills/tune/scripts/bench.sh --lane <slug> --cli agy --model gemini-3.8-flash-<effort> \
  --brief <brief file> --dir <path> --out <json>          # google
.claude/skills/tune/scripts/bench.sh --lane <slug> --cli opencode --model <opencode-go id> \
  --agent <opencode agent> --brief <brief file> --dir <path> --out <json>   # go, zen
# <opencode-go id> is opencode-go/<model> for go and opencode/<model> for zen:
# host.local.yaml lists the <model> part; bench.sh splits on the first slash.
```

unsandboxed and in the background; the JSON carries wall time and usage,
the worker commits on its branch, and the coordinator merges and verifies
it like an Orca worker's. These CLIs run with permissions off and load none
of the repository hooks, so before merging, check that the branch touched
neither `generated/` nor `buf.lock`: `git diff --name-only master...<branch>
-- generated buf.lock` must print nothing. Name the model on every worker;
never `inherit` or unset, and never the coordinating session's own model.
One model per task from start to finish. At most three workers run at once;
start the next wave after the first settles.

## Discover what this host offers

Before the first dispatch of a session:

```bash
.claude/skills/tune/scripts/discover-host.sh > .claude/models/host.local.yaml
```

The file records the agent CLIs present (`claude`, `codex`, `agy`,
`opencode`), whether Orca is reachable and which providers its `--model`
pins, each pool's sign-in state and rate-limit windows, and the opencode
model ids split by pool. Run it unsandboxed: `orca` uses a local socket.
Native subagents always run on Claude. An Orca worker runs on any installed
agent whose pool is signed in.

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
  85%. Claude's windows come from `account list`; `codex`, `google`, and `go`
  report none through Orca yet, so read their usage pages before a wave
  larger than three units and treat a 429 or a "limit reached" reply as the
  pool going hot for the rest of the wave. `go` meters in dollars
  ($12 per 5 hours, $30 per week, $60 per month): a lane's estimated spend
  from the registry price counts against it.
- The coordinating session and every native subagent draw on the Claude
  pool, a Fable session also on `fableWeekly`. Past 85% there, keep native
  delegation to `review-seam` and `judge` and send the rest to the other
  prepaid pools.
- When no fitting pool is usable, do not dispatch: work sequentially or wait
  for the earliest `resetsAt`, and tell the user which window is exhausted.

## Herdr, Orca, or native

Herdr is available when `herdr status server` reports `status: running`.
It is the lane for `execute` work on every pool, since it starts and tracks
`claude`, `codex`, `agy`, and `opencode` alike and reports an approval
dialog as `blocked`; `references/herdr.md` has the setup and the pitfalls.
Run every `herdr` command with the sandbox disabled. One worker per lane:

```bash
s=.claude/skills/delegate/scripts/herdr-worker.sh
$s start --lane <slug> --cli <claude|codex|agy|opencode> --model <id> [--effort <level>] [--agent <opencode agent>] --brief <file> --base HEAD
$s wait <slug>                  # prints done, idle, or blocked; blocked also prints the dialog
$s read <slug> --lines 80       # the worker's report
```

`start` creates the child worktree under `<repo parent>/worktrees/<repo>/`
from this worktree's `HEAD`, writes the brief beside it, starts the agent,
and sends a one-line pointer to the brief. After `wait`, check the tree as
the paragraph on worker reports below says, merge the branch here, verify,
then `$s stop <slug>` and `git branch -d <slug>`. Read-only delegates stay
native in Herdr as in Orca. Herdr keeps no quota: the Dispatch by quota step
stands, and the Orca worktree comment and card are still updated at each
checkpoint when the session runs under Orca.

Orca is available when `ORCA_TERMINAL_HANDLE` is set and `orca status --json`
reports `runtime.reachable: true`; it is the `execute` lane when no Herdr
server runs, and the full-handoff lane either way. Run every `orca` command with the sandbox
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

A codex worker inherits the pool's approval policy unless told otherwise
and then stops at a prompt nobody sees. The full-handoff form `orca
worktree create --agent codex` accepts no flags, so a controlled codex
handoff is a worktree without `--agent` followed by `orca terminal create
--command "codex -a never --sandbox danger-full-access --model <id>"`:
`workspace-write` cannot reach Orca's socket outside the workspace, so a
worker under it commits and cannot send `worker_done`. Whether
`worker-start` passes an approval policy through is unverified; read `orca
skills get orchestration` before a codex dispatch there.

A worker runs the focused tests of its package and commits on its branch; it
does not run the verifier. After every `worker_done`, check the tree before
reading the report as fact: `git -C <child> log --oneline -1` shows the
commit the report names, `git -C <child> status --porcelain` is empty, and
the two or three changes most expensive to get wrong are what the report
says. A report describes what a session believes it did, and the gap
between that and the tree is where a silent tool failure lives. Then merge
the worker's branch into this worktree and run the verifier once, sandbox
disabled, on the union of changed paths.

When `check --wait` returns nothing, no channel distinguishes a worker
blocked on the coordinator from one at work. Rule out the causes in cost
order: the provider's quota, as Dispatch by quota describes; the terminal
tail (`orca terminal read`) for an approval prompt or an instruction that
arrived mangled; then `git status` in the worker's worktree, where a written
file with no commit means the worker is still testing. A clean tree, a final
commit, and an agent that says Orca is not running means the worker sent its
report from inside the sandbox: settle it as `references/orca-sandbox.md`
describes.

Update the worktree comment at each checkpoint:

```bash
orca worktree set --worktree active --comment "unit 2 landed; verifying unit 3" --json
```

A full handoff creates the worktree with the brief as its prompt and stops
supervising. The brief carries the whole ledger when one exists, since
the child worktree has its own git directory:

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
   `generated/`, or `buf.lock`, no plan labels in code.
7. For an Orca worker, how to reach Orca from inside its own sandbox. Copy
   this paragraph into the brief verbatim:

   > Every `orca` command (`orchestration send`, `check`, `ask`,
   > `heartbeat`, `worker_done`) must run through the Bash tool with the
   > parameter `dangerouslyDisableSandbox` set to `true`. The sandbox blocks
   > Orca's local socket, and a sandboxed call reports "Orca is not running"
   > even though it is. `buf generate` and the verifier script need the same
   > setting. Send `worker_done` that way, once, with the injected task and
   > dispatch ids.

   A worker that does not get this finishes its work and cannot report it;
   `references/orca-sandbox.md` says how to settle such a dispatch and the
   per-machine setting that removes the need. A Herdr worker needs none of
   this: it reports in its pane and the coordinator reads it there.
8. For an Orca worker, that editing subagents stay out of its checkout. A
   worker that spawns the Agent tool without worktree isolation gets its
   subagents' files in its own tree and reads them as a duplicate dispatch.
   Read-only subagents are fine; editing work runs in the worker's own turn.
9. For a terminal-driven worker, the brief itself goes in a file under the
   worker's worktree and the terminal gets a one-line pointer to it: a long
   paragraph through `orca terminal send` arrives as stray characters at the
   prompt, and the loss is silent at both ends. The brief never requires an
   acknowledgement before the worker continues; a worker that waits on the
   coordinator looks, from here, exactly like one that is working.

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
