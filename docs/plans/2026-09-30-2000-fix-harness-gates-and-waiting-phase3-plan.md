---
title: Harness Gates and Waiting Phase 3, Waiting and Rework - Plan
type: perf
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-30-2000-fix-harness-gates-and-waiting-plan.md
---

# Harness Gates and Waiting Phase 3, Waiting and Rework - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A coordinator wakes once per settled stage, or once an hour for a quota
check, instead of at every pause of a worker that runs its own lanes.
Each runtime waits in one blocking call, `drive` hands off to a successor
session after each landed phase, and the fix loop stops patching after two
rounds on one mechanism or three in total. The means are changes to
`orca-worker.sh wait`, a `successor.sh` script, and skill wording.

**Stop condition:** Orca stops reporting `childWorktreeIds` in `orca
worktree show --json`, so `wait` cannot see a lane's children.

## Decisions

The parent's Decisions apply. Local to this phase:

- An idle lane with live child worktrees is still waiting while any
  child's screen shows the working hint or the lane's own screen changes.
  When the lane and every child stay idle for `--stall` seconds, `wait`
  prints `idle-children` with the child lanes' names. Why: a stage worker
  ends its turn while its lanes work, and today's idle test reads that
  pause as the stage's end. A child left on purpose (`delegate`: "A child
  whose branch did not land stays") must not hold the wait forever. The
  lanes' state files share `<git common dir>/orca-workers/`, and each names
  its `worktree` and `terminal`, which maps a child id to a screen.
- `wait --until <command>` prints `done` only when the command exits 0 in
  the lane's checkout and the lane passes the idle test. Why: a condition
  met while the worker is still committing would merge a moving branch,
  and `stop` refuses a mid-turn lane.
- `wait` gains `--max <seconds>`, default 3600, after which it prints
  `timeout`. The coordinator reads the quota once and waits again. Why: a
  blocking wait with no ceiling would drop the point where `delegate` asks
  for a quota check on a quiet worker, and a 429 retry loop that redraws
  the screen never reads as stalled.
- A Claude coordinator runs `wait` once per lane with the Bash tool's
  `run_in_background` and acts on its completion notice, with no
  ScheduleWakeup heartbeat or `sleep` loop. Why: the tool's contract
  re-invokes the session when the command exits, so waiting costs no
  model call. The contract is Claude Code's tool description and is
  unverified against published documentation.
- Codex lanes launch with `-c background_terminal_max_timeout=3600000`,
  and `delegate` tells a user-started Codex coordinator to set the same
  key in `~/.codex/config.toml`. A Codex coordinator polls a running
  `wait` with empty `write_stdin` at that cap. Why: `exec_command` yields
  after at most 30000 ms, and an empty `write_stdin` poll blocks up to
  `background_terminal_max_timeout`, default 300000 ms, per
  `codex-rs/core/src/tools/handlers/shell_spec.rs`,
  `codex-rs/core/src/unified_exec/process_manager.rs`, and
  `codex-rs/config/src/config_toml.rs` on the `main` branch of
  github.com/openai/codex. The installed CLI is 0.159.2, and that the same
  bounds apply there is unverified.
- `successor.sh <parent> --model <id> [--effort <level>]` runs only when
  the tree is clean, no lane from `orca-worker.sh status` is live, and the
  model id starts with `claude-`. It creates one terminal with `orca
  terminal create --worktree active` and the Claude launch line
  `orca-worker.sh` builds, prompting `drive` on the parent. `drive` calls
  it when a phase's stages read done and no other phase is in flight,
  then ends its turn. Why: `orca terminal create --help` recommends it
  "for a fresh agent in the current checkout", `claude --help` takes the
  prompt as a positional argument, and with concurrent phases the last
  one to settle hands off.
- After two fix rounds on one mechanism without a clean one, the
  coordinator makes the property executable as `review` step 4 already
  says, or, when it cannot, dispatches one `research` lane for prior art
  or takes the work to `plan`. A third round on that mechanism runs only
  when the property or the research names the fix. After three rounds in
  total on one review, the work goes to `plan` unless the user overrides.
  Why: the two 2026-09-30 entries in `docs/agent-observations.md` record a
  fourth round past the current three-round rule and five rounds on one
  concurrency mechanism, and this rule resolves both.

## Requirements

1. `wait` on an idle lane with a working child keeps waiting, and prints
   `idle-children` naming the child once both stay idle for `--stall`.
2. `wait --until 'test -f DONE'` prints `done` only once `DONE` exists and
   the lane is idle.
3. `wait` prints `timeout` after `--max` seconds, and without children or
   `--until` behaves as today (`test_wait_reports_idle_for_a_settled_screen`
   unchanged).
4. A Codex launch line carries `-c background_terminal_max_timeout=3600000`.
5. `delegate` and `drive` give one blocking wait per lane per runtime and
   name no `--timeout` polling, ScheduleWakeup, or `sleep` loop.
6. `successor.sh` refuses a dirty tree, a live lane, or a non-Claude model,
   and otherwise records one `terminal create` whose command holds
   `.claude/skills/drive/SKILL.md` and the parent path.
7. `drive` step 2's "End a turn only while" list and its caps parenthetical
   match the hand-off and the new round limits.
8. `fix-loop.md` and `review` step 4 state the round rule, and the two
   observations it resolves are deleted.

## Open questions

- Whether Codex 0.159.2 accepts `background_terminal_max_timeout`. The
  re-plan checks one real Codex lane's poll interval, and keeps the
  300000 ms default cap if the key is rejected at launch.
