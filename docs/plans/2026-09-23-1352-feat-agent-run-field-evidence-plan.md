---
title: Agent run log and field evidence for tune - Plan
type: feat
date: 2026-09-23
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: observation logged
execution: mixed
---

# Agent run log and field evidence for tune - Plan
> The `delegate` tie-break decided below was superseded before landing:
> `main` switched step 5 to take the first fitting model in the `fit`
> order (`c487c98b`), which makes the order `tune` writes decide routing
> outright. The merge kept `main`'s step 5.

> Implemented. 5 units, 2026-09-23T12:07:23Z to 2026-09-23T12:31:56Z.

## Goal

`tune` routes on how each model actually performs in this repository's
delegated work, not only on one calibration task. It measures, per model
and role:

- how often the model's work was accepted, amended, rejected, or blocked;
- how often its review findings held;
- how long it worked, how many tokens it used, and what that cost.

Four pieces produce the data. Every Orca worker lane writes a durable
record to a machine-wide run log, and the coordinator grades the lane
before stopping it. Reviews log how many findings each reviewer raised
and how many held. A `tune` script joins the log with each CLI's own
transcript store. `tune` then uses the result to write field rows, order
fit sets, propose fit-set changes, and choose what to recalibrate.
`delegate` breaks headroom ties by fit-set order, so the order `tune`
writes reaches routing.

Stop condition: the transcript stores cannot be joined to a lane by
working directory and time window, for example two lanes sharing one path
at once, or a store that records no directory. Every speed and token
figure rests on that join.

## Decisions

- **Where the log lives.** One append-only JSON Lines file,
  `~/.claude/models/runs.jsonl`, beside `registry.yaml`. Why: the registry
  and its evidence are machine-wide because pools and accounts are
  (`.claude/skills/tune/SKILL.md`, the file table). A lane's record also
  has to outlive its worktree: `orca-worker.sh stop` deletes
  `<git common dir>/orca-workers/<lane>.json`, the only place the lane's
  CLI is written today.

- **Events, not rows.** The log holds `start`, `grade`, `end`, and
  `review` events. Each carries `v: 1`, `event`, `run`, and `at` (UTC,
  RFC 3339). `run` is a random id minted at `start`, since lane slugs are
  reused. Why: `orca-worker.sh` never stops a lane graded `rejected`,
  because it refuses an unmerged branch, so a row updated in place at
  `stop` would lose that lane. An event written the moment the fact is
  known cannot.

- **One writer.** `.claude/skills/delegate/scripts/runlog.py` writes every
  event. `orca-worker.sh` and `review` call it, and `field.py` imports it
  to read the log. It appends each event as a single `write` on a file
  opened with `O_APPEND`, under `fcntl.flock`. It targets Python 3.9 and
  later: no `datetime.UTC`, no `match`. `FLOWSEER_RUNLOG` overrides the
  path, so tests never touch the real log. Why:
  - parallel lanes of one wave write at the same time;
  - the schema then lives in one file;
  - a hook runtime may resolve an older `python3` than an interactive
    shell does.

- **Grading is required before `stop`.** The command is
  `orca-worker.sh grade <slug> --outcome accepted|amended|rejected|blocked
  --verify pass|fail|none [--note TEXT]`, and `stop` refuses a lane with
  no grade. Why: the user chose a coordinator grade on 2026-09-23, and an
  outcome inferred from git cannot tell a rejected lane from an abandoned
  one.

  | Outcome | A lane that commits work | A lane that returns a report (`critique`, `research`, `review-unit` on a pool CLI) |
  | --- | --- | --- |
  | `accepted` | Merged as the worker left it, apart from what the hooks would have formatted. | The report is used as written. |
  | `amended` | Merged after the coordinator changed the work to make it correct or green. | Used after the coordinator corrected it. |
  | `rejected` | Not merged, or redone. | Discarded. |
  | `blocked` | The worker stopped on a stated blocker. | The worker stopped on a stated blocker. |

  `--verify` is the first verifier run after the merge, or `none` when
  nothing was merged.

- **What `start` requires and prints.** `start` requires `--role` and
  takes an optional `--plan <path>` and `--unit <label>`. The role is a
  registry role name. For a `drive` stage worker it is the role in
  `drive`'s stage table, and `drive` passes the stage name as `--unit`.
  `start` writes its event before sending the pointer; a failed write
  runs `undo`, so a live worker always has a `run`. `start` adds `run` to
  the JSON line it prints, which is how a coordinator names an Orca
  reviewer's run after `stop` has deleted the state file. Why:
  - a rate per model without the role mixes execute and review work,
    which the registry already keeps apart under `local`;
  - `runlog.py` checks only the role's shape (`[a-z][a-z-]*`), because
    the registry is YAML and this host has no PyYAML;
  - `field.py` reports runs whose role is not in the registry.

- **Reviews log their findings.** `review` logs one `review` event per
  reviewer after its step 4. The event holds:
  - the reviewer's model;
  - its role, `review-unit` or `review-seam`;
  - its Claude agent id, or its Orca `run`;
  - the plan path;
  - the counts `findings`, `held`, and `unverified`.

  Why: most reviewers are native subagents that never pass through
  `orca-worker.sh`, and step 4 is where the coordinator already decides
  which findings hold. The call runs unsandboxed, like `orca-worker.sh`:
  the sandbox allows no writes under `~/.claude/models`. A failed write
  goes into the review's report, and the review carries on.

- **The scorer.** `.claude/skills/tune/scripts/field.py` is read-only and
  prints JSON. It reads four sources, filtered to sessions whose `cwd`
  contains `--match` (default `FlowSeer`):

  | Source | Where it comes from | Label |
  | --- | --- | --- |
  | Orca lanes | the run log | `orca` |
  | Native subagents | `~/.claude/projects/*/*/subagents/*.jsonl` with their `meta.json` | `native` |
  | Top-level Claude sessions with `entrypoint: cli` | Claude transcripts | `coordinator` |
  | Top-level Claude sessions with `entrypoint: sdk-cli` | Claude transcripts | `headless` |

  A top-level session already joined to an Orca lane counts once, as that
  lane. Why:
  - the user chose all sessions on 2026-09-23;
  - reviewers and researchers run as native subagents;
  - 234 of the latest 300 FlowSeer transcripts are `sdk-cli`, as read on
    2026-09-23, and include `bench.sh` runs that no grade covers;
  - `~/.claude/projects` also holds other repositories.

- **Model ids come from what the model reported.** A native or headless
  run takes its model from the transcript's `message.model`, because
  `meta.json` carries aliases such as `sonnet` and `inherit`. An agy lane
  maps back through the registry's `id_format`
  (`gemini-3.8-flash-high` becomes `gemini-3.8-flash`). An opencode lane
  maps through the per-model `agent:` field. Registry rows are read with
  `catalogue.py`'s `registry_file_models`, so the registry has one line
  parser.

- **How a lane joins its transcript.** Each CLI's store is matched on the
  lane's worktree path and the window from `start` to `end` (or to
  `grade`, or open when neither exists):
  - **Claude:** the directory
    `~/.claude/projects/<path with every non-alphanumeric as "-">`.
    `/Users/aledante/orca/workspaces/FlowSeer/snipefish` maps to
    `-Users-aledante-orca-workspaces-FlowSeer-snipefish`, checked on
    2026-09-23.
  - **Codex:** a file under `~/.codex/sessions/**/*.jsonl` whose
    `session_meta` `payload.cwd` is the path. Tokens come from the last
    `token_count` event's `info.total_token_usage`.
  - **opencode:** the `session` table's `directory` column in
    `~/.local/share/opencode/opencode.db`. U4 reads the message table's
    cost columns from the database itself, since no script reads that
    database today.

  Why: each store records the working directory (all three read on
  2026-09-23), and a worktree path is unique while the lane lives.

- Ruled: a transcript joins when its recorded activity overlaps the lane
  window, even if its session metadata predates `start`. Why: the real
  Codex smoke session began at 12:26:45 UTC, two seconds before the
  `start` event written after terminal launch; its user turn and tokens
  fell inside the window. Cost if wrong: the scorer's join and transcript
  fixtures need changing.

- **agy transcripts are not parsed in this plan.** Its runs report
  `transcript: unsupported` and keep the run log's elapsed time and
  grade. Why: the format under `~/.gemini/antigravity-cli/conversations/`
  is unread, and the grade does not depend on it.

- **Two times per run.**
  - `elapsed_s` runs from `start` to `grade` in the run log.
  - `active_s` sums, over each turn, the span from a turn-opening message
    to the last record before the next one. On Claude, a turn opens at a
    `type: user` record that is neither `isMeta` nor a `tool_result`. On
    Codex, it opens at a user-role message that is not injected context.
    Tool time, including test runs, therefore counts on both CLIs.

  Why: a worker sits idle while the coordinator reads and merges.
  `elapsed_s` counts that wait and `active_s` does not. On Claude,
  `tool_result` records are `type: user`, and counting them as turn
  starts would drop every test run from a Claude lane.

- **One cost formula.** It lives in
  `.claude/skills/tune/references/calibration.md`, and `field.py` uses it,
  so field cost and calibration cost compare.
  - Tokens are normalised per CLI. Codex's `cached_input_tokens` is a part
    of `input_tokens`, and its `reasoning_output_tokens` is a part of
    `output_tokens`.
  - Cached input is charged at 0.1 × input.
  - Claude cache writes cost 1.25 × input for `ephemeral_5m` and 2 × input
    for `ephemeral_1h`.
  - Every figure is marked `est`. opencode's recorded cost replaces the
    estimate.

- **`tune` uses the results.** A new step and a `tune field` mode follow
  these rules; the thresholds are a starting point and the skill says so.
  - It writes `field.<role>` only for roles in the registry, and only for
    a model and role with at least 5 graded runs, or at least 10 findings
    for a review role. Each row carries `as_of`, `since`, `runs`, the
    outcome counts, `verify_pass`, `held`/`findings` for reviewers, and
    the medians of `active_s`, `tokens`, and `cost_usd`.
  - It orders each fit set by field success rate: accepted over graded,
    with `blocked` left out. Within ten points it orders by median
    `active_s`. This applies only where every model in the set has enough
    runs; otherwise the set keeps its calibration order.
  - It proposes removing a model from a role when the model's
    amended-plus-rejected share is at least 0.4 over at least 5 graded
    runs, or its held share is under 0.5 over at least 10 findings.
  - It proposes calibrating a model that field runs show in a role it has
    no `local` result for.
  - Entry to a fit set still needs a calibration result, and every change
    to a fit set is asked, as step 5 already requires.

  The skill's rule "enters or leaves only on a calibration result" and
  the registry's header comment on role order are rewritten to match.
  Why: the user asked on 2026-09-23 that `tune` use the results rather
  than only record them. The task mix differs per model, and a small
  sample swings a rate.

- **`delegate` breaks headroom ties by fit-set order.** Step 5 becomes:
  take the model whose pool has the most headroom, and within ten points
  the one earlier in the fit set, then the lower price. Why: step 5
  (`.claude/skills/delegate/SKILL.md`, "Resolve a role to a lane") never
  reads fit-set order today, so an order `tune` writes would not change
  routing. The registry's comment ("takes the first whose pool has room")
  and the skill disagree now; this makes them agree.

- **Python tests run in the verifier.** `verify-change.sh` runs
  `python3 -m unittest discover -s <dir> -p 'test_*.py'` once for each
  `.claude/skills/*/scripts` directory that holds a test file, and fails
  when such a directory reports 0 tests. Why: no skill directory is a
  package, so one discovery from `.claude/skills` finds nothing, and
  Python 3.9 reports "Ran 0 tests, OK".

- **Bootstrap order.** Lanes started before U2 merges carry no `run`, and
  the new `stop` refuses them. So U3 and U4 merge and stop before U2's
  branch merges, and the U2 lane is removed by hand in Orca. No shim
  admits a lane without a run.

- **Transcript retention.** `cleanupPeriodDays: 365` was set in
  `~/.claude/settings.json` on 2026-09-23. The oldest FlowSeer transcript
  was 30 days old, which is the default window, and the store grows about
  4.7 GB a month. This is a machine setting, not part of this change.

- **Codex retention check.** No session retention setting was configured in
  `~/.codex/config.toml` on 2026-09-23, so `tune` has no Codex retention
  value to name in its field step.

- **Start rollback when cleanup fails (the user's ruling, 2026-09-23).**
  When `undo` cannot close the terminal or remove the worktree, it keeps
  the lane's state file and its message says the lane is still live, so
  `status` shows the lane and a person removes it. A failed `end` write
  inside `undo` is accepted as residual risk: it needs a second fault, and
  it costs at most one transcript join in `field.py`.

## Requirements

1. `runlog.py start --lane l1 --cli codex --model gpt-6-sol --role execute
   --worktree /w/l1 --branch u/l1 --base abc123` with `FLOWSEER_RUNLOG=$f`
   appends one line to `$f` with `v: 1`, `event: start`, a fresh `run`,
   and those fields, and prints the `run` id.
2. `runlog.py start ... --role Execute` exits non-zero and writes nothing.
3. Twenty `runlog.py review` processes started together leave twenty
   complete JSON lines in `$f`.
4. `orca-worker.sh start` without `--role` exits non-zero before creating a
   worktree.
5. `orca-worker.sh stop l1` on a lane with no `grade` event exits non-zero
   with a message naming `grade`, and removes nothing.
6. `orca-worker.sh grade l1 --outcome accepted --verify pass` appends a
   `grade` event with the lane's `run`. A second `grade` for the same lane
   appends another, and the scorer uses the last.
7. `orca-worker.sh stop l1` after a grade appends an `end` event with the
   branch head, then removes the lane as it does today.
8. Take a fixture run log with a Codex lane at `/w/l1`, and a fixture
   Codex session whose `session_meta` names `/w/l1` inside the window.
   `field.py` reports that run with uncached input equal to
   `input_tokens − cached_input_tokens`. A second session at `/w/l1` a day
   later is not joined.
9. A fixture subagent has `meta.json` saying `model: sonnet`, and its
   transcript says `claude-sonnet-5`. `field.py` reports it under model
   `claude-sonnet-5`, source `native`.
10. With three graded runs of one model and role, `field.py` prints the
    counts and marks the group `sample: small`. With five, it does not.
11. `field.py` prints the one `agy` lane in a fixture, launched as
    `gemini-3.8-flash-high`, under `gemini-3.8-flash` with
    `transcript: unsupported` and its `elapsed_s`.
12. A Claude fixture transcript has a user message at 0 s, a `tool_use` at
    10 s, its `tool_result` at 70 s, and a final assistant record at 80 s.
    `field.py` reports `active_s: 80`.
13. A top-level Claude session joined to an Orca lane appears once, as
    `orca`.
14. `runlog.py review --model claude-opus-5-5 --role review-unit --agent a1
    --findings 4 --held 3 --unverified 1` appends one `review` event with
    those counts. `--held 5 --findings 4` exits non-zero.

## Out of scope

- Parsing `agy` conversations.
- Outcomes for native subagents other than review counts. `lookup` and
  `research` get speed and tokens only.
- Grading coordinator or headless sessions.
- Any dashboard. `field.py` prints JSON, and `tune` reports in text.
- Backfilling runs from before the log existed.

## Units

### U1. Run log writer and the verifier's Python tests

Files: `.claude/skills/delegate/scripts/runlog.py`,
`.claude/skills/delegate/scripts/test_runlog.py`,
`.claude/skills/verify-change/scripts/verify-change.sh`
After: none

Change:
- `runlog.py` has the subcommands `start`, `grade`, `end`, and `review`,
  and a `read(path)` function that yields events and skips (and counts)
  lines that do not parse.
- Each write validates the event's fields, including `held + unverified
  <= findings`, then appends under `flock`.
- `start` prints the run id.
- `verify-change.sh`, in its `hook_tooling` block, runs the per-directory
  discovery from Decisions.

Tests: `test_runlog.py` covers requirements 1, 2, 3 (subprocesses, then
every line parses), and 14. It also covers a truncated trailing line,
which `read` skips and counts.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/delegate/scripts/runlog.py .claude/skills/delegate/scripts/test_runlog.py .claude/skills/verify-change/scripts/verify-change.sh`

### U2. Worker lanes write and grade runs; delegate and drive use them

Files: `.claude/skills/delegate/scripts/orca-worker.sh`,
`.claude/skills/delegate/scripts/test_orca_worker.py`,
`.claude/skills/delegate/SKILL.md`,
`.claude/skills/delegate/references/orca.md`,
`.claude/skills/drive/SKILL.md`
After: U1

Change:
- **`start`:** requires `--role` and takes `--plan` and `--unit`. After
  the terminal is up and before the pointer is sent, it calls
  `runlog.py start` with the lane's CLI, model, effort, agent, role, plan,
  unit, worktree path, branch, and `git rev-parse <base>`. A failed write
  runs `undo`. `start` stores `run` in the state file and prints it in its
  JSON line.
- **`grade`:** a new subcommand, as in Decisions.
- **`stop`:** refuses a lane with no `grade` event for its `run`.
  Otherwise it writes `end` with `git rev-parse <branch>` before removing
  the lane.
- **Usage header:** lists `grade`.
- **`delegate/SKILL.md`:** step 5 takes the fit-set tie-break. The Orca
  worker section shows `--role` and `grade` and holds the outcome table
  once.
- **`orca.md`:** notes the run log.
- **`drive/SKILL.md`:** adds a grade step between the verifier and the
  removal of the child, with `--role` from its stage table, `--plan`, and
  the stage as `--unit`.

Tests: `test_orca_worker.py` runs the script with a stub `orca` first on
`PATH`. The stub answers `status`, `terminal read`, `terminal close`, and
`worktree rm`. The tests run in a temporary git repository with a
prepared state file and `FLOWSEER_RUNLOG`, and cover requirement 4 (the
stub records no `worktree create`), 5, 6, and 7. Nothing here covers
`start`'s event with a real terminal; Verification has the manual lane.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/delegate/scripts/orca-worker.sh .claude/skills/delegate/scripts/test_orca_worker.py .claude/skills/delegate/SKILL.md .claude/skills/delegate/references/orca.md .claude/skills/drive/SKILL.md`

### U3. Review logs reviewer precision

Files: `.claude/skills/review/SKILL.md`
After: U1

Change: step 4 ends with one `runlog.py review` call per reviewer, run
unsandboxed. It names the reviewer's model, role, agent id or Orca `run`,
the plan path when there is one, and the counts. A finding moved to
"pre-existing" counts as held, and one the coordinator could not confirm
counts as unverified. A failed write goes into the report. The
coordinator's own reading logs nothing.

Tests: the event's shape is covered by requirement 14 in U1. That the
skill makes the call is checked by the Verification review.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/review/SKILL.md`

### U4. Field scorer and the shared cost formula

Files: `.claude/skills/tune/scripts/field.py`,
`.claude/skills/tune/scripts/test_field.py` (its fixtures, including an
opencode database, are built in a temporary directory, not committed),
`.claude/skills/tune/references/calibration.md`
After: U1

Change:
- **Command line:** `field.py [--since YYYY-MM-DD] [--match TEXT]
  [--runlog PATH] [--claude-projects DIR] [--codex-sessions DIR]
  [--opencode-db PATH] [--registry PATH ...]` prints
  `{as_of, since, runs: [...], groups: [...], unmatched: [...]}`.
- **Each run carries:**
  - source, model, role, CLI, outcome, and verify;
  - `elapsed_s` and `active_s`;
  - tokens by kind;
  - `cost_usd` with `est`;
  - tool errors: Claude `tool_result` records with `is_error`, and `null`
    on the other CLIs.
- **Groups** are per model, role, and source. Each holds counts, rates,
  medians, the reviewer counts `findings`/`held`, and `sample: small`
  below the thresholds in Decisions.
- **Native roles** come from `agentType`: `Explore` is `lookup`,
  `repo-researcher` is `research`, and `independent-reviewer` takes the
  role on its `review` event, else `review`. Anything else is
  `native-other`.
- **`calibration.md`** states the cost formula from Decisions, and
  `field.py` names it.

Tests: `test_field.py` covers requirements 8 through 13, plus:
- the path-to-directory mapping, on the snipefish example and on a path
  containing `_`;
- the cost of one Claude transcript (with both cache-write kinds) and one
  Codex session, each against a hand-computed value.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/tune/scripts/field.py .claude/skills/tune/scripts/test_field.py .claude/skills/tune/references/calibration.md`

### U5. Tune uses field results

Files: `.claude/skills/tune/SKILL.md`, `docs/agent-steering.md`
After: U2, U4

Change:
- **New step and mode.** `tune` gains step "3a. Read field results", and
  `field` joins its `argument-hint`. The step runs `field.py --since`
  from the `as_of` of the last field block, or from 90 days ago when
  there is none. It writes `field.<role>` for registry roles and one
  `evidence.md` line per changed block, then applies the ordering,
  removal, and calibration proposals from Decisions.
- **Rewrites in `tune/SKILL.md`:**
  - The rule that a model "enters or leaves a role's fit set only on a
    calibration result" becomes: entry needs calibration, while order and
    removal proposals may rest on field results. The thresholds are named
    as a starting point.
  - Step 4 names field-flagged models as the first calibration
    candidates.
  - Step 5 writes the registry header comment on role order to match
    `delegate`'s tie-break. Its report lists field-driven proposals
    separately from calibration-driven ones.
- **`agent-steering.md`:** a paragraph on why lanes are graded by the
  coordinator, and why field data orders fit sets but cannot add a model
  to one.

The `After: U2` edge exists because the ordering rule is only true once
`delegate` reads the order.

Tests: none in code. Verification runs `tune field` once.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/tune/SKILL.md docs/agent-steering.md`

Waves: U1 | U2 U3 U4 | U5. Within the middle wave, U3 and U4 merge and
stop before U2 merges; the U2 lane is removed by hand (see Decisions,
bootstrap order).

## Verification

- `.claude/skills/verify-change/scripts/verify-change.sh --` on the union
  of changed paths. The Python tests run inside it, and the output shows
  a non-zero test count for each scripts directory.
- Manual: run one real `execute` lane with the sandbox disabled, through
  `orca-worker.sh start --role execute`, then `grade`, then `stop`. Then
  `field.py` shows that run joined to its transcript, with non-zero
  `active_s` and tokens.
- Manual: `review` of this branch writes one `review` event per reviewer.
  Check with `grep '"event": "review"' ~/.claude/models/runs.jsonl`.
- Run `tune field` once. Its report names each block written and each
  proposal, or says that no group reached the threshold.

## Definition of done

- [x] Verifier green for every changed path.
- [x] `delegate`, `drive`, `review`, and `tune` describe the new flags,
      the grade, the tie-break, the review event, and the field step in
      the same change.
- [x] `docs/agent-steering.md` records the why.
- [x] This plan's `status` set, with an outcome note under its title.
- [x] No plan labels in code.

## Open questions

None.
