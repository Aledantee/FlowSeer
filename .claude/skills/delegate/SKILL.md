---
name: delegate
description: Choose where a FlowSeer skill sends delegated work (Explore, a project subagent, or an Orca worker on any installed agent CLI) on whichever model tier fits, and what the brief must contain. Load before dispatching any agent from plan, implement, review, compound, or steer.
user-invocable: false
---

# Delegate FlowSeer work

The coordinating session owns the result: a delegate returns evidence, a
finding list, or a branch, and the coordinator integrates it and runs the
verifier. Delegate when the work would flood this context or can run in
parallel. A question that one grep answers is not delegated.

## Pick the role, then resolve the lane

Delegated work is named by role, never by model. The registry maps each
role to a fit set of models, each model to a prepaid pool, and each pool to
the CLI and the way it is pinned. Read the machine-wide
`~/.claude/models/registry.yaml` when present, then lay the project's
`.claude/models/registry.yaml` over it: `tune` says how an override merges.
`tune` keeps the registry true; when its `as_of` is more than 30 days old,
say so in the report and continue.

| Work | Role | Worker |
| --- | --- | --- |
| Lookup with no judgment: which files reference a symbol, which fixtures exist, where a string appears | `lookup` | `Explore` subagent, or the pool's CLI |
| Bounded question that needs conventions read and evidence weighed | `research` | `repo-researcher` subagent, or the pool's CLI |
| Editing work that runs for minutes: an implementation unit, a solution refresh | `execute`; `execute-sensitive` when a changed path matches `sensitive_paths` | Orca worker when Orca is reachable, else see Orca or native |
| Independent review of one unit's files | `review-unit` | `independent-reviewer` subagent, or the pool's CLI |
| Review of the seams between units, and the verdict | `review-seam` | `independent-reviewer` subagent |
| Tie-break between reviewers, verdict on a hard plan | `judge` | native subagent; never on a `sensitive` unit |
| Adversarial read of a plan | `critique` | the pool's CLI |
| A whole plan handed to someone else | the user's choice | Orca full handoff |

A constraint on the CLI or vendor comes from the role's fields in the
registry (`fit`, `exclude`, `vendor_differs_from`, `never_sensitive`),
which the steps below apply, and from nowhere else. A stage being a skill
under `.claude/skills/` does not tie it to `claude`: every agent CLI reads
that file and follows it.

Resolve a role to a lane in this order, once per lane:

1. Drop models whose pool row (`~/.claude/models/host.yaml`, refreshed per wave by
   `scripts/pool-usage.sh`) shows `signed_in` false or null, or over 85% on
   a window that applies to the model. Orca reporting a provider as
   `unavailable` is not a pool row; see Dispatch by quota.
2. Drop `zen` unless every fitting prepaid pool is hot. `zen` is per-token;
   a wave that reaches it says so.
3. Drop models the role `exclude`s. For `review-unit`, also drop the
   `vendor` of the model that executed the unit under review. The run
   log names it, with `$plan` set to the plan's path. A unit with no line
   of its own ran in a lane that ran several: one printed as `-`, or as
   a `drive` stage name such as `implement`. With no such lane either,
   the coordinator executed it, on its own vendor. A
   `google` id carries the effort suffix (`gemini-3.8-flash-high` is
   `gemini-3.8-flash`), and an opencode agent name is the registry
   model whose `agent` it is:

   ```bash
   python3 -B -c 'import sys; sys.path.insert(0, ".claude/skills/delegate/scripts"); import runlog; [print(e.get("unit") or "-", e.get("model") or e["agent"]) for e in runlog.read() if e.get("event") == "start" and e["role"].startswith("execute") and e.get("plan") == sys.argv[1]]' "$plan"
   ```

   A reviewer a session spawns as its own subagent runs on that session's
   vendor, so it is a lane this step applies to like any other.
4. Drop a pool whose running lanes of this wave fill its slots (Wave
   size, below), and move one that holds any running lane to the back.
5. Take the first model left in the role's `fit` order. `fit` lists the
   models best-first by their calibration on the role: speed, then pool
   usage, among those that passed. Headroom enters only through steps 1
   and 4, so a pool is passed over when it is hot or its slots are taken,
   not because a later model's pool has more room. A signed-in pool whose
   source failed (`windows: null`) counts as having room until it answers
   with a 429.

Step 4 spreads a wave: a six-unit `execute` wave with four pools signed in
runs on four pools, not six times on one model. The four prepaid pools
(`claude`, `codex`, `google`, `synthetic`) are paid for whether used or not, so
the report names every fitting pool the wave left idle and why.

Pinning by pool: `claude` and `codex` take `--model` and `--effort`;
`google` takes `--model gemini-3.8-flash-<effort>` on the `agy` launch;
`synthetic` and `zen` take the opencode agent named in the registry's
`opencode_agents`, whose model is fixed in `~/.config/opencode/opencode.json`
to the model's `pool_id`.
A model whose `effort` list lacks the role's level gets the highest level it
lists: `execute` routes at `xhigh`, and `gemini-3.8-flash-xhigh` is not a
model id, so that lane launches as `gemini-3.8-flash-high`.
`scripts/orca-worker.sh` puts each of these on the worker's launch line from
`--cli`, `--model`, `--effort`, and `--agent`; the Agent tool takes `model`.
Name the model on every worker; never `inherit` or unset, and never the
coordinating session's own model. One model per task from start to finish.

## Wave size

How many workers run at once follows the quota, and only independent work
widens with it: the units of one wave (no `After` between them, no shared
file), phases with no `After` between them and disjoint files, one
solution per worker in a refresh, one reviewer per unit. Work that chains
runs in turn however much quota is idle.

Each usable pool holds slots, read from the worst window that applies to
the lane in its `pool-usage.sh` row:

| Worst applicable window | Slots |
| --- | --- |
| under 50% | 2 |
| 50% to 85% | 1 |
| unknown (`windows: null`) | 1 |
| over 85%, or signed out | 0 |

The wave's cap is the sum of the slots over the pools that fit the role,
at most six, and never more than the independent tasks ready. Four idle
pools give six at once; one pool at 60% gives one, and the rest of the
wave runs in rounds. The six is the coordinator's limit, not the pools':
every lane passes through one session's tree check, merge, and verifier
run, and the verifier runs one at a time. Recompute the cap before every
wave; a 429 mid-wave takes that pool's slots away for the rest of it.
Read-only native subagents count against the `claude` pool's slots like
any lane on it. Start the next wave after the current one settles.

A worker that runs a skill which dispatches workers of its own (a `drive`
stage) gets a budget in its brief, a number of workers it may hold, and
uses that instead of computing a cap: two coordinators reading the same
rows would each spend the whole headroom.

## Discover what this host offers

Before the first dispatch of a session:

```bash
mkdir -p ~/.claude/models
.claude/skills/tune/scripts/discover-host.sh > ~/.claude/models/host.yaml
```

The file records the agent CLIs present (`claude`, `codex`, `agy`,
`opencode`), whether Orca is reachable, each pool's sign-in state and
rate-limit windows (through `scripts/pool-usage.sh`), and the opencode
model ids split by pool. Run it unsandboxed: `orca` uses a local socket,
`agy` reads the keyring, and `opencode` writes a log file. `orca: reachable:
true` in that file means the worker lane is open. Native subagents always
run on Claude; an Orca worker runs on any installed agent whose pool is
signed in.

## Dispatch by quota

No single tool sees all four pools, so `scripts/pool-usage.sh` reads each
from the source that owns its numbers and prints one row per pool with
`signed_in`, the used percent of every window, and the `worst` one with its
reset time:

```bash
.claude/skills/delegate/scripts/pool-usage.sh    # unsandboxed
```

| Pool | Source | Windows |
| --- | --- | --- |
| `claude`, `codex` | `orca account list --json`, `rateLimits` | `session`, `weekly`, `fableWeekly` |
| `google` | `agy -p /quota --output-format json`, answered without a model turn | `gemini-5h`, `gemini-weekly`, `3p-5h`, `3p-weekly` |
| `synthetic` | `GET https://api.synthetic.new/v2/quotas` with the key opencode holds; the call is not counted | `5h`, `week` |

`orca account list` also carries an `antigravity` row with
`status: unavailable`. That status says Orca cannot read its usage (no
Gemini CLI sign-in); it says nothing about the pool.
Never drop `google` on it. A pool is out only when its own row from
`pool-usage.sh` shows `signed_in: false` or a window over the threshold. A
row with `windows: null` and an `error` means the source failed: say so in
the report and treat the pool as signed in with unknown headroom.

Reading the rows is a step of every dispatch, not advice before one: run the
script immediately before each wave, and again first whenever a delegated
session goes quiet, because an exhausted pool is the cheapest of the four
causes of silence to rule out and the only one visible without touching the
worker. A window at 0% may have just rolled over; `resets` in the same row
says whether it did.

- A pool is usable when it is signed in and every window that applies to the
  lane is under 85%. On `google` the `gemini-*` windows meter Gemini models
  and the `3p-*` windows meter Claude and GPT models run through `agy`; only
  the group of the lane's model counts.
- `synthetic` meters a rolling five-hour request limit and a weekly credit
  limit, both counted by Synthetic, so usage from another host is in the
  numbers. Both refill in ticks instead of resetting, so its row carries
  no `resets`; the tick interval has not been measured.
- A 429 or a "limit reached" reply marks the pool hot for the rest of the
  wave, whatever the row said.
- The coordinating session and every native subagent draw on the Claude
  pool, a Fable session also on `fableWeekly`. Past 85% there, keep native
  delegation to `review-seam` and `judge` and send the rest to the other
  prepaid pools.
- When no fitting pool is usable, do not dispatch: work sequentially or wait
  for the earliest `resetsAt`, and tell the user which window is exhausted.

## Orca or native

Read-only delegates (`lookup`, `research`, `review-*`, `judge`) stay
native subagents on every host, except a `review-unit` reviewer when
step 3 drops the coordinator's own vendor: it runs on the first fitting
pool's CLI through `orca-worker.sh start --role review-unit`. Without
Orca that unit gets no independent reviewer; the coordinator's own
reading in `review` is its pass, and the report says so. Editing work goes to an Orca worker when
`orca status --json` reports `runtime.reachable: true`. Otherwise it goes
to a `general-purpose` subagent with `isolation: worktree` only when the
role's fit set holds a Claude model, pinned to that model. A native
subagent runs only on Claude, and a Claude model outside the fit set is
not calibrated as fit for the role.

A stage worker of `land` or `drive` runs a skill and commits its
checkpoint, so it is editing work whatever role supplies its model, a
`review-seam` stage included. Without Orca such a stage does not run here
or in a read-only subagent: stop and name the stage for the user to run in
a fresh session. Other editing work whose role's fit set holds no Claude
model (`execute` today) runs as follows:

- The coordinator does the units itself, one at a time, along the calling
  skill's path for a wave of one. This is the user's standing exception to
  "never the coordinating session's own model" above.
- When the role `exclude`s the coordinator's model (`execute-sensitive`
  on a top-tier session), nothing runs: report the unit as blocked on Orca.

Orca uses a local socket the Bash sandbox blocks, so every `orca` command
runs with the sandbox disabled; a sandboxed call reports the runtime as
not running.

### Orca worker

`scripts/orca-worker.sh` is the whole procedure; `references/orca.md` says
what it works around, what to do when a step fails, and how a full handoff
differs.

```bash
s=.claude/skills/delegate/scripts/orca-worker.sh
$s start --lane <slug> --cli <claude|codex|agy> --model <id> [--effort <level>] --role <role> [--plan <path>] [--unit <unit>] --brief <file>
$s start --lane <slug> --cli opencode --agent <opencode agent> --role <role> [--plan <path>] [--unit <unit>] --brief <file>
$s wait <slug>            # blocks; prints idle, exited, or timeout, then the screen
$s read <slug>            # the worker's report, from its screen
$s status                 # one line per live lane
$s grade <slug> --outcome <accepted|amended|rejected|blocked> --verify <pass|fail|none> [--note <text>]
$s stop <slug>            # after grade and merge: closes the terminal, removes checkout and branch
```

`start` requires `--role` and takes optional `--plan` and `--unit`. It exits
0 only when the worker exists in a child worktree branched from this
worktree's branch and has the brief on its screen. A failed start removes the
worktree when cleanup succeeds. If terminal close or worktree removal fails,
`status` keeps showing the lane for manual removal, and the error says so.
Once the terminal is up and before sending the brief pointer, it logs a
`start` event to the run log with base commit and
metadata, and stores `run` in the state file. Its JSON line names the
branch, which Orca prefixes with the git user, and `run`. `wait` prints
`idle` when the turn ended: check the tree, then read the report. A
permission dialog also reads as idle, which is why the screen follows:
answer a dialog the brief anticipated with `$s keys <slug> <text>`,
otherwise report it.

Then merge the branch here, run the verifier on the changed paths, grade
the lane, and `$s stop <slug>`. `stop` refuses a lane that has no `grade`
event for its `run`, is mid-turn, dirty, or not merged here, because
removing the worktree deletes its branch. On stop, it logs an `end` event
with the branch head before removing the lane.

| Outcome | A lane that commits work | A lane that returns a report (`critique`, `research`, `review-unit` on a pool CLI) |
| --- | --- | --- |
| `accepted` | Merged as the worker left it, apart from what the hooks would have formatted. | The report is used as written. |
| `amended` | Merged after the coordinator changed the work to make it correct or green. | Used after the coordinator corrected it. |
| `rejected` | Not merged, or redone. | Discarded. |
| `blocked` | The worker stopped on a stated blocker. | The worker stopped on a stated blocker. |

`--verify` is the first verifier run after the merge, or `none` when nothing was merged.

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
the repository hooks: no format-on-edit, no guard on `generated/`, no Stop
gate. The coordinator's checks after the merge are the only gate its
branch gets. First check that `git diff --name-only <base>..<branch> --
generated buf.lock` prints nothing, with `<base>` the commit the lane was
started from. Then format what it changed and commit the result, since the
verifier's format gate fails on what the hook would have fixed. Run the
message-sync hook on each changed schema, which the verifier does not
cover, and the suppression hook on the lines the branch adds (it prints
the first five matches):

```bash
git diff --name-only --diff-filter=d <base>..<branch> -- '*.go' ':!generated' | xargs -r sh -c 'gofumpt -w "$@" && goimports -w "$@"' sh
git diff --name-only --diff-filter=d <base>..<branch> -- 'spec/proto/*.proto' | xargs -r -n1 buf format -w
git diff --name-only --diff-filter=d <base>..<branch> -- 'spec/proto/*.proto' | while read -r f; do
  jq -n --arg cwd "$PWD" --arg f "$PWD/$f" '{cwd:$cwd,tool_input:{file_path:$f}}' | tools/hooks/proto-check.sh; done
git diff -U0 <base>..<branch> | sed -n 's/^+\([^+].*\)/\1/p' | jq -Rs '{tool_input:{file_path:"<branch>",content:.}}' | tools/hooks/suppression-warn.sh
```

A reported message-sync gap is fixed before the verifier runs. A
suppression the worker's report does not justify is removed and its
finding fixed; one it does justify goes into the report for the user,
since `AGENTS.md` makes a suppression a policy change. Then run the
verifier.

A child whose branch did not land stays, and the report names it with
the reason, so the user can read it before it goes. Never remove a child with a dirty tree; say what is there.

## Write the brief

A delegate has none of this conversation. Write the brief file, like a
reviewer's diff, in the session scratchpad directory and pass its absolute
path. Never `$TMPDIR`: `orca-worker.sh` runs unsandboxed, where `$TMPDIR`
names a different, shared directory, and a stale brief another session
left at the same name is dispatched without an error. The brief states,
in order:

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
   `generated/`, or `buf.lock`, no plan labels in code, and no git write
   in any checkout but the worker's own: the coordinator merges the
   worker's branch, and a worker that merges into the coordinator's
   checkout lands work there before the tree check. Work is set aside
   with a temporary commit or a copy under the worker's own `$TMPDIR`
   (it never crosses a sandbox boundary, unlike a brief), never `git stash`:
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
8. For a worker started through `orca orchestration` only, the paragraph
   in `references/orca-sandbox.md` on reaching Orca from inside the
   worker's sandbox, verbatim. A worker started by `orca-worker.sh` reports
   on its screen and needs nothing of the kind.

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
