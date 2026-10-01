---
title: Harness Gates and Waiting Phase 3, Waiting and Rework - Plan
type: fix
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: rework
execution: code
parent: docs/plans/2026-09-30-2000-fix-harness-gates-and-waiting-plan.md
---

# Harness Gates and Waiting Phase 3, Waiting and Rework - Plan

> Implemented. 4 units, 2026-10-01T11:37:30Z to 2026-10-01T11:47:12Z.

## Goal

A coordinator wakes once per settled stage, or once an hour for a quota
check, instead of at every pause of a worker that runs its own lanes. Each
runtime waits in one blocking call, `drive` hands off to a successor session
after each landed phase, and the fix loop stops patching after two rounds on
one mechanism or three in total. The means are changes to `orca-worker.sh
wait`, a `successor.sh` script, and skill wording.

**Stop condition:** Orca stops reporting `childWorktreeIds` in `orca
worktree show --json`, so `wait` cannot see a lane's children. Orca 1.4.216
reports it beside `parentWorktreeId`.

## Decisions

The parent's Decisions apply. Local to this phase:

- An idle lane with child worktrees keeps waiting while a child works or its
  own screen changes. A child is quiet when it passes the lane's idle test:
  two reads five seconds apart, no hint, the same screen. When lane and
  children stay quiet for `--stall` seconds, `wait` prints `idle-children`
  with the child lanes' names. Why: `wait`'s idle test in `orca-worker.sh`
  reads only the lane's own screen. A review stage lane starts fix workers
  (`fix-loop.md` step 1) as its children, and a Claude lane ends its turn
  while they work, so the test prints `idle` there. A child left on purpose
  (`delegate`: "A child whose branch did not land stays") must not hold it.
- One clock serves `stalled` and `idle-children`. It restarts when the
  lane's screen changes or a child works, and `--stall 0` turns it off. A
  child with no state file is named by its id, one without a readable
  terminal by its lane name, and both count as quiet. A failed child query
  counts as one quiet child named `unreadable`, as `stop` refuses on one.
  Why: a lane blocked in a foreground `wait` on its child shows the hint
  over a still screen, which today reads as a hung stream.
- `wait --until <command>` prints `done` only when the command exits 0 in
  the lane's checkout, the lane passes the idle test, and no child works.
  While the command fails, `wait` prints what it would without `--until`.
  Why: a condition met while the worker or its lanes still commit would
  merge a moving branch, and a lane that settles without it hit a blocker.
- `--max <seconds>`, a positive integer, default 3600, replaces `--timeout
  <ms>`. At the ceiling `wait` prints `timeout` and then the screen, and the
  coordinator reads the quota once and waits again. Why: a wait with no
  ceiling drops `delegate`'s quota check on a quiet worker, and a 429 retry
  loop that redraws the screen never reads as stalled.
- A Claude coordinator runs `wait` once per lane with the Bash tool's
  `run_in_background` and `timeout: 7200000`, sandbox disabled, and acts on
  the completion notice. Nothing else is scheduled to check on the lane. A
  notice that the command hit its background time limit reads as `timeout`,
  and any other stop is reported as its notice says. Why: the Claude Code
  2.1.286 bundle (`~/.local/share/claude/versions/2.1.286`) says the
  parameter "runs the command detached: it keeps running across turns and
  re-invokes you when it exits". Unless `backgroundDeadlineDisabled` is set,
  it stops a background command after 1800000 ms by default and 7200000 ms
  at most, so `--max` stays under 7200.
- Codex lanes launch with `-c background_terminal_max_timeout=3600000`. A
  Codex coordinator starts `wait` with `exec_command` and polls it with an
  empty `write_stdin` at `yield_time_ms: 3600000`. Why: at tag
  `rust-v0.159.3` of github.com/openai/codex,
  `codex-rs/core/src/unified_exec/mod.rs` caps a yield at 30000 ms, and
  `process_manager.rs` beside it clamps an empty poll between 5000 ms and
  the cap `codex-rs/core/src/session/session.rs` takes from that key, 300000
  ms by default. On the installed 0.159.3, `codex -c
  'background_terminal_max_timeout="abc"' features list` fails with
  `expected u64`, and `unified_exec` is listed `stable`.
- `successor.sh <parent> --model <id> [--effort <level>]` runs only when
  Orca is reachable, the tree is clean, this worktree has no child worktree,
  and the model id starts with `claude-`. It creates one terminal with `orca
  terminal create --worktree active`, the Claude launch line a new
  `orca-worker.sh line` prints, and a quoted prompt to read
  `.claude/skills/drive/SKILL.md` and drive the parent. Why: a copied line
  would drift from the `switchModelsOnFlag` setting, which stops a flagged
  session from changing model unseen. `orca terminal create --help` says
  "Use this, not worktree create, for a fresh agent in the current
  checkout", and `claude --help` shows `claude [options] [command]
  [prompt]`. `orca-worker.sh status` cannot replace the child test, since
  its state directory holds the lanes of every worktree. A child kept on
  purpose refuses the hand-off.
- `successor.sh` exits 0 with the terminal handle only once the new screen
  shows Claude's `esc to interrupt` hint. With no hint it closes the
  terminal and exits 1, like a refusal. It exits 2 when a successor may be
  running: no handle came back, or the close failed. Why: `drive` ends its
  turn on exit 0 and continues on exit 1, so a successor that never started
  would end the drive unseen, and one left running would drive beside this
  session. The shell echoes the prompt, so its text on the screen proves
  nothing.
- The successor keeps the worker line's `--dangerously-skip-permissions`.
  Why: a coordinator stopped at a permission prompt with nobody watching
  ends the drive, and every lane already runs this way.
- A Claude coordinator hands off when a phase's last stage reads done, the
  state command still names a phase that can run, no lane of this session is
  live, and no plan in scope holds a `Parked by drive:` line. It passes its
  own model id, and `--effort` only when the user named one. Exit 1
  continues here, exit 2 stops the drive with a report, and the last phase
  goes to step 5. A coordinator on another runtime does not hand off
  (unconfirmed). Why: a phase landing while another is in flight leaves a
  live lane, so the last to settle hands off. A resumed drive asks parked
  questions first (`drive` step 4), in a terminal nobody watches.
- `drive` waits on each stage with `wait <slug> --until '<test>'`, the test
  an anchored `grep -q` for the frontmatter field of the stage's "Done
  when". On `done` without the stage's report on the screen it waits again
  without `--until`. Why: `done` tells a finished stage from a blocked one,
  and `implement` and `review` write the field before their last run.
- `review` step 4's rule stands: the first defect in a fix makes the
  property executable before the next round. After two rounds on one
  mechanism without a clean one, a third runs only when that property or one
  `research` lane's prior art names the fix, and otherwise the work goes to
  `plan`. After three rounds in total on one review it goes to `plan` unless
  the user overrides. A round counts against a mechanism when its re-review
  finds a correctness defect in it. Under `drive` the total limit is a
  `rework` verdict naming the limit, which step 4 parks with another round
  as an option. Why: `docs/agent-observations.md` records a fourth round
  past the three-round rule and five rounds on one concurrency mechanism.

## Requirements

1. `wait` on an idle lane with a working child keeps waiting, and prints
   `idle-children` naming the child once both stay idle for `--stall`.
   Example: with lane `l1` idle and child `c1` showing the hint, `wait l1
   --stall 1 --max 3` prints `timeout`.
2. `wait --until 'test -f DONE'` prints `done` only once `DONE` exists and
   the lane is idle. Example: an idle lane without `DONE` prints `idle`, and
   with `DONE` a lane showing a frozen hint prints `stalled`.
3. `wait` prints `timeout` after `--max` seconds, and without children or
   `--until` behaves as today (`test_wait_reports_idle_for_a_settled_screen`
   unchanged). Example: `wait l1 --stall 0 --max 1` on a lane showing the
   hint prints `timeout`, then the screen.
4. A Codex launch line carries `-c background_terminal_max_timeout=3600000`.
   Example: `line --cli codex --model gpt-6-sol` prints a line holding it.
5. `delegate` and `drive` give one blocking wait per lane per runtime and
   name no `--timeout` polling, ScheduleWakeup, or `sleep` loop. Example:
   `grep -n -E -- '--timeout|ScheduleWakeup|sleep' .claude/skills/delegate/SKILL.md .claude/skills/drive/SKILL.md`
   prints nothing, while `grep -c` counts at least 1 for `run_in_background`
   and `write_stdin` in the first file and for `--until` in the second.
6. `successor.sh` refuses a dirty tree, a live lane, or a non-Claude model,
   and otherwise records one `terminal create` whose command holds
   `.claude/skills/drive/SKILL.md` and the parent path. Example: `--model
   gpt-6-sol` exits 1 and the Orca log holds no `terminal create`.
7. `drive` step 2's "End a turn only while" list and its caps parenthetical
   match the hand-off and the new round limits. Example: the list names a
   started successor, and the parenthetical the two-round limit.
8. `fix-loop.md` and `review` step 4 state the round rule, and the two
   observations it resolves are deleted. Example: `grep -c -E 'fix loop
   exceeded|repeated concurrency fixes' docs/agent-observations.md` is 0.

## Out of scope

- A lane whose pending work has no Orca child worktree, such as a native
  subagent or a background command. `wait` prints `idle` at that pause, or
  `done` when `--until` holds, and the coordinator waits again. Children of
  a child are not read either, as `drive` nests lanes no deeper.
- A run log event or `check` for the successor, and closing a predecessor's
  terminal, which `land/references/orca-cleanup.md` lists for a person.
- The shell tool limits of `agy` and `omp`, unmeasured per `orca.md`. A
  coordinator there reruns a foreground `wait` the tool cut short.
- `wait --until` runs a command string, and `successor.sh` reads a plan
  path, a model id, and an effort level. The coordinating session writes all
  of them and is trusted, so the scripts guard against mistakes only.

## Units

### U1. Child-aware wait and launch lines
Files: `.claude/skills/delegate/scripts/orca-worker.sh`, `.claude/skills/delegate/scripts/test_orca_worker.py`, `.claude/skills/delegate/references/orca.md`
After: none
Change: `wait SLUG [--until CMD] [--max S] [--stall S]` follows the
Decisions' child, clock, `--until`, and `--max` rules. Each pass reads the
lane's children with `children` and finds each child's terminal in the state
file whose `worktree` equals its id. A child without a terminal is never
read. `--until` runs through `bash -c` in the lane's `path`, output
discarded, only once the lane passes the idle test. `--timeout` and `--max
0` are refused. The Codex launch line holds the poll cap. `line --cli <cli>
--model <id> [--effort <level>]` prints the launch line, `env -u` prefix
included, from the function `start` calls. The usage block and the `sed`
range that prints it cover `line` and the new `wait` shape. In `orca.md`,
the load line reads "anything but `idle` or `done`", and "What the script
does" describes the six outcomes, `line`, and the poll cap in place of the
`--timeout` sentence. "When a step fails" gains `idle-children` (read the
lane and each named child, then `tell` a lane that stopped waiting to
continue), and its quiet-worker entry names `timeout` as the point to rule
out its causes once and wait again.
Tests: `test_orca_worker.py`. The Orca stub keeps the `ORCA_STUB_SCREEN`
fallback and gains `ORCA_STUB_SCREENS`, a directory where a file named after
a terminal handle holds that terminal's screen, and
`ORCA_STUB_FAIL=worktree-show`. Every new `wait` case passes `--stall 1
--max 3` unless it names other flags, so a defect cannot spin to the default
ceiling. Cases: requirement 1's example, and `idle-children c1` with `c1`
idle. A child id without a state file prints `idle-children` with the id and
logs no read of an empty handle, which the stub answers with the hint. A
failed `worktree show` prints `idle-children unreadable`. A frozen hint on
the lane with a working child prints `timeout`. Requirement 2's two
examples, `done` on an idle lane with `DONE`, and `timeout` with `DONE`
while a child works. Requirement 3's example. `--stall 0` on an idle lane
with a quiet child prints `timeout`. `--timeout 1000` and `--max 0` exit
non-zero. `start` records the string `line` prints for the same arguments,
which for `codex` holds the poll cap. No case covers a live TUI's hint.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/delegate/scripts .claude/skills/delegate/references/orca.md`

### U2. Successor script
Files: `.claude/skills/drive/scripts/successor.sh`, `.claude/skills/drive/scripts/test_successor.py`
After: U1
Change: `successor.sh` finds `orca-worker.sh` from its own directory. It
refuses with exit 1 in the order model, effort, parent path, tree, Orca
reachability, children, each naming what it found and creating nothing. The
parent must exist and match `docs/plans/<name>-plan.md`, the effort level
must be lowercase letters, and a child query that fails or returns no
`childWorktreeIds` refuses. It builds the command from `orca-worker.sh line
--cli claude` and the prompt `"Read .claude/skills/drive/SKILL.md and drive
<parent>."`, and creates the terminal titled `drive`. It reads the screen up
to 20 times with `sleep 3` between reads and exits as the Decisions say,
with `successor may be running` and the handle on exit 2.
Tests: `test_successor.py` in a throwaway repository, with an Orca stub that
logs calls and a no-op `sleep` on `PATH`, as `test_orca_worker.py` has.
Cases: `gpt-6-sol`, a missing parent, an untracked file, a non-empty
`childWorktreeIds`, and a failed `worktree show` each exit 1 with no
`terminal create` logged. The accepted case logs one `terminal create` with
`--worktree active` whose command holds `--model claude-opus-5-5`,
`switchModelsOnFlag`, `.claude/skills/drive/SKILL.md`, and the parent path,
and exits 0 on a screen holding the hint. A screen holding only the echoed
command logs a `terminal close` and exits 1. A failed close and an empty
handle each exit 2. Nothing here covers a live Claude TUI.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/drive/scripts`

### U3. Fix round limits
Files: `.claude/skills/review/references/fix-loop.md`, `.claude/skills/review/SKILL.md`, `docs/agent-observations.md`
After: none
Change: `fix-loop.md` "When to stop" keeps its first two bullets and
replaces the three-round bullet with the Decisions' round rule in full. The
`research` lane is briefed with the mechanism, the property, and each
round's finding, and asked how `docs/solutions/`, `docs/architecture/`, and
the libraries the code imports solve it. A `rework` report names the limit
that caused it. `review` step 4's class bullet keeps its trigger and points
to `references/fix-loop.md` for the two-round limit. Requirement 8's two
entries leave `docs/agent-observations.md`.
Tests: none executable. Requirement 8's `grep` covers the deletion.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/review docs/agent-observations.md`

### U4. Waiting and hand-off wording
Files: `.claude/skills/delegate/SKILL.md`, `.claude/skills/drive/SKILL.md`
After: none
Change: `delegate`'s Orca worker block shows `wait <slug> [--until
<command>] [--max <seconds>]` with its six outcomes and the `line` command.
A paragraph after it gives the waiting rule for a Claude coordinator, a
Codex one (whose user sets the key in `~/.codex/config.toml` when starting
it by hand), and one on `agy` or `omp`. `done` and `idle` lead to the tree
check, every other outcome to `references/orca.md`, and Dispatch by quota
names `timeout` as the quiet-worker check. `drive` step 2 waits as
`delegate` describes, each stage's test written out as `grep -q` on the plan
path with the patterns `^artifact_readiness: implementation-ready$`,
`^status: implemented$`, `^review: accept`, and `^compound:`, and gives the
rule for `done` without a report. Its "End a turn only while" list gains a
started successor, and its caps parenthetical reads "three verifier rounds
on a unit, two fix rounds on a mechanism, three on a review". Step 3 gains
the hand-off: its four conditions for a Claude coordinator, the
`successor.sh` call with the sandbox disabled and `timeout: 180000`, a
one-line report on exit 0 that the drive and its closing question continue
in the named terminal, and the two other exits. Step 4 says a `rework` that
names the round limit parks with another round as an option.
Tests: none executable. Requirement 5's `grep` lines cover the wording.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/delegate/SKILL.md .claude/skills/drive/SKILL.md`

Waves: U1 U3 U4 | U2

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/delegate .claude/skills/drive .claude/skills/review docs/agent-observations.md
```

Then requirement 5's and 8's `grep` lines, and the parent's live drive.

## Definition of done

- [x] Verifier green on the union of changed paths.
- [x] This plan's `status` and outcome note set, and the parent's `Landed:`
      line for U3 filled. No plan labels in code or commits.

## Open questions

- Parked by drive: the review ended `rework` at the three-round cap with two low
  test-coverage findings open (a two-child `stop` case, five untested `wait` guards).
  Options: one tests-only fix round, then a re-review (closes both, about an hour) |
  accept with the two findings listed (no wrong behavior is known). Recommended: the
  tests-only round, because the guards are the stall and timeout rules coordinators
  now rely on, and the cases are already specified.

None blocks a unit. Unconfirmed: only a Claude coordinator hands off, which
narrows the parent's hand-off decision, since no rule says which Claude
model continues a drive started elsewhere. Unverified and untested here:

- A live Codex coordinator's empty poll runs the full 3600000 ms, and a
  background command's fate with `backgroundDeadlineDisabled` set.
- A Claude TUI started with a positional prompt shows the hint within 60
  seconds. A miss closes the successor and `drive` continues itself.
- Whether a hook's approval prompt for a policy-surface edit still shows
  under `--dangerously-skip-permissions`. `drive` step 4 parks such edits.
