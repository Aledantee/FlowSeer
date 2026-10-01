---
name: delegate
description: Chooses where a FlowSeer skill sends delegated work (Explore, a project subagent, or an Orca worker on any installed agent CLI) on whichever model tier fits, and what the brief must contain. Load before dispatching any agent from plan, implement, review, compound, or steer. Not for a question one grep answers, and not for refreshing the model registry (`tune`).
user-invocable: false
---

# Delegate FlowSeer work

The coordinator owns the result: a delegate returns evidence, findings, or
a branch; the coordinator integrates it and runs the verifier. Delegate
when the work would flood this context or can run in parallel; answer a
one-grep question yourself.

## Pick the role, then resolve the lane

Name work by role, never by model. Read `~/.claude/models/registry.yaml`
when present, then lay `.claude/models/registry.yaml` over it (`tune` says
how an override merges). When only one exists, it is the registry. An
`as_of` over 30 days old: say so in the report and continue. Review roles
(`review-unit`, `review-seam`, `judge`) never run on Sonnet: their `fit`
and `last_resort` hold none. With no registry readable, a review lane takes
`claude-opus-5-5`, or `claude-fable-5-1` when Opus 5.5 authored the change,
and the report says the registry was missing.

| Work | Role | Worker |
| --- | --- | --- |
| Lookup with no judgment: which files reference a symbol, which fixtures exist, where a string appears | `lookup` | `Explore` subagent, or the pool's CLI |
| Bounded question that needs conventions read and evidence weighed | `research` | `repo-researcher` subagent, or the pool's CLI |
| Writing or re-planning a plan under `docs/plans/` | `plan` | Orca worker when Orca is reachable, else the pool's CLI |
| Editing work that runs for minutes: an implementation unit, a solution refresh | `execute`; `execute-sensitive` when a changed path matches `sensitive_paths` | Orca worker when Orca is reachable, else see Orca or native |
| Independent review of one unit's files | `review-unit` | `independent-reviewer` subagent, or the pool's CLI |
| Review of the seams between units, and the verdict | `review-seam` | `independent-reviewer` subagent, or the pool's CLI |
| Tie-break between reviewers, verdict on a hard plan | `judge` | native subagent; never on a `sensitive` unit |
| Adversarial read of a plan | `critique` | the pool's CLI |
| A whole plan handed to someone else | the user's choice | Orca full handoff |

CLI and vendor constraints come only from the role's fields (`fit`,
`exclude`, `vendor_differs_from`, `model_differs_from`, `never_sensitive`).
A stage being a skill under `.claude/skills/` does not tie it to `claude`;
every agent CLI reads that file. A `fit` entry is a model id, optionally
followed by `@<level>`. Every step below matches the model id before the
`@` against pool rows, `exclude`, vendors, and executors. Resolve each
lane in this order:

1. Drop models whose pool row, as `scripts/pool-usage.sh` printed it for
   this wave (`~/.claude/models/host.yaml` holds the session-start rows),
   shows `signed_in` false or null, or over 85% on a window that applies to
   the model. Orca reporting a provider `unavailable` is not a pool row.
2. Drop models the role `exclude`s. For `review-unit`, also drop the
   `vendor` of the model that executed the unit under review; for
   `review-seam`, that model itself. Load `references/review-lanes.md` to
   find the executor.
3. Drop a pool whose running lanes of this wave fill its slots (Wave size),
   and move one that holds any running lane to the back.
4. Take the first model left in the role's `fit` order. Headroom enters
   only through steps 1 and 3. A signed-in pool with `windows: null` has
   room until it answers with a 429.

When steps 1–2 leave no `fit` model, apply them to `last_resort` when the
role has one and take a survivor by step 4; the report names the lane a
last resort and the pools that were out. A review role left with no
survivor because the executors cover its fit set splits by writer, as
`references/review-lanes.md` describes. The report names every fitting
prepaid pool (`claude`, `codex`, `google`, `synthetic`) the wave left idle
and why, since they are paid for either way.

Name the model on every worker, never `inherit` or unset, one model per
task start to finish; the coordinator's model is an ordinary candidate.
`scripts/orca-worker.sh` builds the launch line from `--cli`, `--model`,
and `--effort`; the Agent tool takes `model`. `claude` and `codex` take
`--model` and `--effort`; `google` takes `--model gemini-3.8-flash-<effort>`
on `agy`; `synthetic` takes `--model <pool_id>` on `omp`, which has no agent
profiles. A `fit` entry written `<model>@<level>` launches at that
level, the one `tune` measured as the best tradeoff for the role: the model
id goes to `--model` and the level to `--effort` (into the id on `agy`). A bare
entry launches at the role's `effort`. A model whose `effort` list lacks
that level gets its highest listed level: `execute` routes at `xhigh`, so a
bare `gemini-3.8-flash` launches as `gemini-3.8-flash-high`. A role's
`min_effort` is a floor under either. Load `references/sensitive.md`
when a changed path matches `sensitive_paths`.

## Wave size

Only independent work widens with quota: units of one wave (no `After`
between them, no shared file), phases with no `After` between them and
disjoint files, one solution per worker in a refresh, one reviewer per
unit. Chained work runs in turn. A usable pool holds slots by the worst
window that applies to the lane in its row: 2 under 50%, 1 from 50% to 85%
or unknown (`windows: null`), 0 over 85% or signed out. The cap is the sum
over the pools that fit the role, at most six, never more than the
independent tasks ready; the rest runs in rounds. Recompute before every
wave; a 429 mid-wave removes that pool's slots for the rest of it.
Read-only native subagents count against `claude`. Start the next wave
after the current one settles. A worker that dispatches workers of its own
(a `drive` stage) uses the worker budget its brief names instead of a cap,
since two coordinators reading the same rows would each spend all of it.

## Discover what this host offers

Before the first dispatch of a session, unsandboxed:

```bash
mkdir -p ~/.claude/models
.claude/skills/tune/scripts/discover-host.sh > ~/.claude/models/host.yaml
```

It records the agent CLIs, Orca reachability (`orca: reachable: true`
opens the worker lane), each pool's sign-in state and windows, and the
the synthetic model ids omp serves. Native subagents run only on Claude; an Orca
worker runs on any installed agent whose pool is signed in.

## Dispatch by quota

Run this immediately before each wave, and first on `timeout` (the
quiet-worker check) or whenever a delegated session goes quiet:

```bash
.claude/skills/delegate/scripts/pool-usage.sh    # unsandboxed
```

A pool is usable when signed in and every window that applies to the lane
is under 85%; only the pool's own row counts. A 429 or "limit reached"
marks it hot for the rest of the wave. When no fitting pool is usable, do
not dispatch: work sequentially or wait for the earliest `resetsAt`, and
tell the user which window is exhausted. Load `references/pool-rows.md`
when reading a `google` or `synthetic` row, a `fableWeekly` window, a window
at 0%, a row with an `error`, or when `claude` is past 85%. The
coordinating session and every native subagent draw on the Claude pool, a
Fable session also on `fableWeekly`.

## Orca or native

`lookup`, `research`, and `judge` stay native subagents on every host. A
review lane stays native only when its resolved model is a Claude model a
native subagent can be pinned to; any other runs on its pool's CLI through
`orca-worker.sh start --role <role>`. Editing work, including every stage
worker of `land` or `drive`, goes to an Orca worker. When `orca status
--json` does not report `runtime.reachable: true`, load
`references/no-orca.md` before routing any lane, review lanes included. Run every `orca` command with the sandbox
disabled; a sandboxed call reports the runtime as not running.

### Orca worker

```bash
s=.claude/skills/delegate/scripts/orca-worker.sh
$s line --cli <cli> --model <id> [--effort <level>]
$s start --lane <slug> --cli <claude|codex|agy> --model <id> [--effort <level>] --role <role> [--plan <path>] [--unit <unit>] --brief <file>
$s start --lane <slug> --cli omp --model <pool_id> --role <role> [--plan <path>] [--unit <unit>] --brief <file>
$s wait <slug> [--until <command>] [--max <seconds>]  # blocks; prints idle, done, stalled, timeout, exited, or idle-children, then the screen
$s read <slug>            # the worker's report, from its screen
$s keys <slug> <text>     # a dialog answer, at most 200 characters
$s tell <slug> <file>     # a message over the 200 characters `keys` takes
$s status                 # one line per live lane
$s grade <slug> --outcome <accepted|amended|rejected|blocked> --verify <pass|fail|none> [--note <text>]
$s stop <slug>            # after grade and merge: closes the terminal, removes checkout and branch
```

`start` exits 0 only when the worker runs in a child worktree branched from
this branch with the brief on its screen; its JSON line names the branch
(prefixed with the git user) and `run`.

A Claude coordinator runs `wait` once per lane with the Bash tool's
`run_in_background` and `timeout: 7200000`, sandbox disabled, and acts on
the completion notice. Nothing else is scheduled to check on the lane. A
notice that the command hit its background time limit reads as `timeout`,
and any other stop is reported as its notice says. A Codex coordinator
(whose user sets `background_terminal_max_timeout = 3600000` in
`~/.codex/config.toml` when starting it by hand) starts `wait` with
`exec_command` and polls it with an empty `write_stdin` at
`yield_time_ms: 3600000`. On `agy` or `omp`, shell tool limits are
unmeasured (`references/orca.md`), so a coordinator there reruns a
foreground `wait` the tool cut short.

`done` and `idle` lead to the tree check, then read the report. A
permission dialog also reads as idle: answer one the brief anticipated with
`keys`, otherwise report it. Then merge here, run the verifier on the
changed paths, `grade`, and `stop`, which refuses an ungraded, mid-turn,
dirty, or unmerged lane (removal deletes the branch). Every other outcome
routes to `references/orca.md`. Load `references/orca.md` as well when a
step fails, when the screen shows an unanticipated dialog or a Claude
model-switch prompt, and for an orchestration run or a full handoff.

| Outcome | A lane that commits work | A lane that returns a report (`critique`, `research`, `review-unit` on a pool CLI) |
| --- | --- | --- |
| `accepted` | Merged as the worker left it, apart from what the hooks would have formatted. | The report is used as written. |
| `amended` | Merged after the coordinator changed the work to make it correct or green. | Used after the coordinator corrected it. |
| `rejected` | Not merged, or redone. | Discarded. |
| `blocked` | The worker stopped on a stated blocker. | The worker stopped on a stated blocker. |

`--verify` is the first verifier run after the merge, or `none` when nothing was merged.

### Reading a worker's report

A worker runs its package's focused tests and commits; it does not run the
verifier. Before reading the report as fact or merging its branch, run the
lane check:

```bash
.claude/skills/delegate/scripts/orca-worker.sh check <slug>
```

A non-zero check stops the merge. Grade the lane `rejected`, leave it
unmerged, and dispatch it again through the model-switch rule. Then check the
tree:
`git -C <child> log --oneline -1` shows the commit the report names,
`git -C <child> status --porcelain` is empty, and the two or three changes
most expensive to get wrong are what the report says. An idle lane whose
child has changes but no new commit stopped short: `tell` it to commit.
Merge the branch here. After the merge commit exists, including a resolved
conflict, run:

```bash
python3 .claude/skills/land/scripts/merge-check.py ORIG_HEAD..HEAD
```

A non-zero result stops the merge step. Carry every `missing` block in the
report, then run the verifier once, sandbox disabled, on the union of changed
paths; for a worker on `agy` or `omp`, load
`references/hookless-merge.md` after the merge, before the verifier. A child whose branch did not land stays,
and the report names it with the reason. Never remove a child with a dirty
tree; say what is there.

## Write the brief

Write the brief in the session scratchpad directory and pass its absolute
path, never `$TMPDIR`: `orca-worker.sh` runs unsandboxed, where `$TMPDIR`
is shared and a stale brief of the same name dispatches without error. In
order:

1. The goal in one sentence and the definition of done. Quote a plan
   requirement and say it is not the worker's to restate, narrow, or move
   to another fixture. A stage worker's brief names the project skill by
   path (`.claude/skills/review/SKILL.md`).
2. The files or diff, as repository-relative paths; a reviewer gets the
   path of a diff file in the scratchpad directory.
3. The conventions that apply, as paths, and the matched `docs/solutions/`
   entries. For every worker, name the pinned version (`go.mod`, `buf.lock`)
   of every external convention or library those files rely on, and the
   vendored spec paths under `spec/` for every protocol or vendor surface
   they touch. State an external fact in the brief only with its source
   (`AGENTS.md`, Investigation discipline), since a worker repeats an
   uncited fact as settled.
4. What to return: evidence with `path:line`; findings by severity, each
   with a failure scenario; or the changed paths, the focused test command
   and its result, and the commit hash (an editing worker commits before
   reporting). Outcome first, no preamble, closing summary, narration, or
   word budget; see Register. Name the checks the coordinator already ran
   with their result, and say to report once the named scope is checked; an
   editing worker still runs the focused checks its own edits invalidate.
5. For a unit of a plan with a ledger (`verify-change`'s `SKILL.md`
   documents it), the `note` line of every landed unit, verbatim, and
   nothing else from the ledger.
6. The boundaries: no edits outside the named files; no changes to
   `AGENTS.md`, `buf.yaml`, `tools/hooks/`, `.claude/settings.json`,
   `generated/`, or `buf.lock`; no edit to a plan Decision marked
   `decided by the user` (a finding or unit that needs one changed is a
   blocker); no plan labels in code; no running a script
   under `tools/hooks/` (it blocks on stdin; the focused tests, `go tool -modfile=tools/buf/go.mod buf lint`,
   and the verifier on the changed paths are the checks); no lint or race
   run over all of `generated/go/yang` (it exhausts host memory; lint two
   or three sample packages); no git write outside the worker's own
   checkout (the coordinator merges). Scratch files and set-aside work go
   under the worker's own `$TMPDIR` (a literal `/tmp` path prompts or is
   denied) or into a temporary commit, never `git stash`, whose stack every
   worktree and session shares.
   A fix worker that needs a file outside the named files and the classes
   allowed by `fix-loop.md` step 1 reports a blocker naming the file and
   reason. A comment, skipped or weakened test, or partial change is not a
   fix.
7. For every runtime: no questions; state a blocker and stop. A requirement
   the worker believes the code cannot satisfy is a blocker, even when a
   weaker one is within reach. Editing subagents need worktree isolation,
   or their files land in the worker's tree and read as a duplicate
   dispatch; read-only subagents are fine.
8. For a worker started through `orca orchestration` only, the brief
   paragraph from `references/orca-sandbox.md`, verbatim.

Mark each claim about the codebase (an import direction, a call's
behavior, a field's existence) verified with `path:line`, or unverified for
the worker to confirm; a worker checking a coordinator's instruction is
expected. Load `references/overrides.md` when the brief decides against a
delegate's proposal, when it would repeat an override of an earlier
worker, or when the coordinator amends a worker's output or writes a
ledger `note`. A
brief for `Explore` or `repo-researcher` names the directories to search
and leaves out `docs/plans/` unless the question is about a plan. State
intended behavior as a specification; never tell a reviewer the change is
tested, safe, or believed correct, or forward commit text that says so.

## Register

Briefs, reports, worktree comments, check-ins: terse, since every later turn
re-reads them. No articles, filler, pleasantries, hedging, narration.
Fragments fine. Identifiers, paths, errors, numbers exact; code unchanged.
Prose only for ordered sequences, warnings, irreversible actions. Report:
outcome first, nothing the brief said. Status: one line. Reasoning depth
comes from the role's effort level, not from text.
