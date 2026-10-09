# Agent observations

Entries here record where a FlowSeer skill, agent definition, or hook did not
fit the work: a correction the user had to make to the procedure, a step that
was skipped or done by hand, a brief that missed something, a sequence that
recurs with no skill behind it. The `compound` skill's Observe mode appends
them. Nothing reads this file on its own; the `steer` skill works the queue
when a maintainer asks for it.

`steer` applies or rejects each entry through the change process in
[`agent-steering.md`](agent-steering.md) and deletes it. An edit to a policy surface `AGENTS.md`, Hard boundaries, names stops at a
staged diff for a person's review. An entry that sits here is not a
rule; the skill or hook it names stays authoritative until it changes. A
lesson about the code belongs under [`solutions/`](solutions/README.md),
not here.

Entry format:

```markdown
## <YYYY-MM-DD> <skill>: <one-line title>
Skill or agent: <path and step>, or `new skill candidate: <working name>`.
What happened: <what was corrected or did not fit, and whether the step
was followed as written>.
Suggested change: <smallest edit to the skill, agent, or hook>.
```

## Entries

## 2026-10-07 plan: a launcher port had no required live start check
Skill or agent: `.claude/skills/plan/SKILL.md`, unit verification checks,
and `.claude/skills/implement/SKILL.md`, step 2.3.
What happened: the uv scripts Phase 5 plan's U2 Tests line (2026-10-05,
retired with its land; its text at `6b831fb4`) required shell and Python lifecycle suites with controlled tool replies.
Its Verification section required documented replacements on fixtures.
Neither required a live Orca start. The port refused a terminal reply
without a status field until `da5a613f` removed that refusal.
`tools/scripts/skills/delegate/orca_worker.py:269` records that Orca
1.4.221's terminal object has no status field. The regression test at
`tools/scripts/tests/skills/delegate/test_orca_worker.py:1493` now accepts
that reply. The prescribed checks left the real reply shape unchecked.
Suggested change: decide whether plan or implement should require a live
start check when changing a launcher that drive itself uses. Name the
runtime version and preserve the observed reply as test evidence.

## 2026-10-07 plan: a filename scan missed inline module imports
Skill or agent: `.claude/skills/plan/SKILL.md`, unit verification checks.
What happened: U6 of the uv scripts Phase 2 plan (2026-10-05, retired
with its land; its text at `7d2d5e46`) searched old script names only with a `.py` suffix. The inline programs removed
by `cfce3311` from `.agents/skills/drive/SKILL.md` and
`.agents/skills/delegate/references/review-lanes.md` used `import runlog` and
inserted the old directory into `sys.path`. The prescribed scan could not
match those callers. Following that check left a caller shape unchecked.
Suggested change: when planning a script move, include module names, old
directories, and inline interpreter programs in the caller audit, and execute
the documented replacement commands against fixtures.

## 2026-10-06 implement: a verifier log under `$TMPDIR` was read back from another directory
Skill or agent: `.claude/skills/implement/SKILL.md`, step 2.5, and
`.claude/skills/verify-change/SKILL.md`, "Send output you may need to a file
under `$TMPDIR`".
What happened: the verifier runs with the sandbox disabled, where `$TMPDIR`
is a different directory from the sandboxed one. The coordinator wrote the
run's log to `$TMPDIR/verify-u1.log` unsandboxed, then read
`$TMPDIR/verify-u1.log` from a sandboxed command and got a file of that name
left by an earlier session, whose last line read `FlowSeer verification
passed.` The real run had failed. A lane was graded `--verify pass` on it
before `verify ledger` refused the stale receipt. The step was followed as
written. `delegate`, Write the brief, already rules `$TMPDIR` out for briefs
for the same reason.
Suggested change: in both places, name the session scratchpad directory for
a log an unsandboxed command writes and a later command reads, and keep
`$TMPDIR` for files one command writes and reads itself.

## 2026-10-07 drive: policy-surface units cannot be approved edit by edit inside workers
Skill or agent: `.claude/skills/drive/SKILL.md`, step 4 (park on a change to
a policy surface) and `.claude/skills/delegate/SKILL.md`, Orca worker.
What happened: the uv scripts Phase 2 plan (2026-10-05, retired at
`b8648d37`) said each `run.py` edit "prompts the person". On a Codex worker
the hook's `ask` failed as `PreToolUse hook returned unsupported
permissionDecision:ask`, and the edit went through with no prompt. On a
Claude worker started by `orca-worker.sh` (launch line carries
`--dangerously-skip-permissions`) five Edit calls on `tools/scripts/run.py`
returned no prompt either, while a Write that created `tools/scripts/lib/lock.py`
and a later Edit on `run.py` in another lane did prompt. Why the Claude
cases differ is not known. The drive fell back to per-unit approval: the
worker stopped uncommitted, the coordinator showed the user the diff, and
the approval named the blobs (`git hash-object`) the commit had to match.
Suggested change: `drive` names the fallback (stop uncommitted, approve by
blob hash per unit) instead of parking, and routes a unit whose `Files:`
line names a policy surface to a Claude worker only, since Codex cannot
answer `ask`. `delegate` records in Orca worker that the prompt is not
reliable under the launch flags.

## 2026-10-08 delegate: a lane whose terminal Orca reclaimed cannot be stopped
Skill or agent: `tools/scripts/skills/delegate/orca_worker.py`, `stop`, and
`.claude/skills/delegate/references/orca.md`, When a step fails.
What happened: an Orca restart on 2026-10-07 reclaimed every worker terminal
(`orca terminal show` answered `terminal_handle_stale`). `stop <slug>
--keep-worktree` then failed at the terminal close and recorded nothing, and
`start --join` refused the kept worktree because the dead lane still read as
live. The coordinator wrote the run log `end` and the `kept` mark by hand,
which is what `stop --keep-worktree` writes after a close that succeeds.
`orca.md` covers a terminal that exited, with a `terminal show` status of
`exited`, not a handle the runtime no longer knows.
Suggested change: `stop` treats `terminal_handle_stale` (and any reply that
names no terminal) as a closed terminal: it logs `end`, marks the lane kept
or removes it, and says so. `orca.md` names the stale handle beside the
exited terminal.

## 2026-10-09 steer: a hook that crashes lets the call proceed
Skill or agent: `tools/scripts/hooks/worktree_guard.py`, `main`, and the
hook registrations in `.claude/settings.json`.
What happened: a Bash command that holds a NUL in a primary checkout on a
protected branch makes `words` raise `ValueError: embedded null byte`
(`worktree_guard.py:228`). `main` catches `ValueError` only around the JSON
read (`:365`), so the hook exits 1 with a traceback and no decision.
Reproduced 2026-10-09 with `rm a\0b` on `main`. The plan's Decisions say
`main` already catches it. Claude Code treats an exit code other than 2 as
non-blocking (the hooks reference the plan fetched 2026-10-07, quoted in its
Decisions), so the call proceeds. Whether Codex does the same for exit 1 is
`codex-rs/hooks/src/events/pre_tool_use.rs:261` to `:284`, unverified here.
Suggested change: decide whether a guard's uncaught exception is a deny by
construction, for example one wrapper in `tools/scripts/run.py` that turns
an exception in a `PreToolUse` hook into `hookio.deny`, instead of a
`try` in each hook. `pre-tool-policy` and `protect-generated-bash` already
deny on an uncaught exception per the same Decisions.

## 2026-10-09 implement: a stage stops whole when a unit's rule makes a planned or standing test unbuildable
Skill or agent: `.claude/skills/implement/SKILL.md`, step 2, and
`.claude/skills/drive/SKILL.md`, step 4.
What happened: the fabric and device seam phase of the simulation overhaul
stopped three times on a test, not on code. A planned test could not be
built because validation refuses its input (plan commit `9f8a6beb` moved it
to a later unit). A standing test lost the state it planted because the
unit's rule released it first (`01add323`). A planned test needed a device
two of its three callers cannot accept (`2b5a531a`, settled in `63e54bdd`).
Each stop was followed as written: the worker stated a blocker and the
whole stage ended, with fifteen units still pending the first time. The
coordinator ruled on each as test placement or setup and restarted the
stage.
Suggested change: give `implement` a rule for these cases. A planned test
its unit cannot build moves to the first later unit that can, noted in both
units' Tests text. A standing test whose planted state the rule removes
gets a setup that survives the rule, with its counts and reasons unchanged,
plus a case for what the rule now does. The worker stops only when neither
keeps the proof or a user Decision would change. Say in `drive`, step 4,
that such a ruling is the coordinator's and is reported, not parked.

## 2026-10-09 delegate: `orca-worker status` read a working lane as idle
Skill or agent: `tools/scripts/skills/delegate/orca_worker.py`, `status`.
What happened: `status` printed `idle` for a plan stage lane whose screen
showed the worker mid-turn. The screen, read with `read`, was right. The
step was followed as written and gave the wrong answer.
Suggested change: have `status` use the same working test `wait` uses, or
print `unknown` when the two disagree. Add a test with a mid-turn screen.

## 2026-10-09 drive: skills loaded before a merge of `main` name commands the merge removed
Skill or agent: `.claude/skills/drive/SKILL.md`, step 3.
What happened: the drive merged `main` into its branch between phases
(`30071f66`). That merge replaced `.claude/skills/delegate/scripts/orca-worker.sh`
with `uv run tools/scripts/run.py delegate orca-worker`. The coordinator's
loaded copy of `drive`, `delegate`, and `land` still named the shell
script, so the next dispatch failed on a missing file. Worker lanes started
from the pre-merge branch were stopped by a Stop hook that printed
`run.py: unknown command: hooks stop-check`.
Suggested change: in `drive`, step 3, after a land's merge of `main`,
reload the workflow skills when
`git diff --name-only ORIG_HEAD..HEAD -- .agents/skills tools/scripts`
prints a path, and start no lane from a branch older than that merge.

## 2026-10-09 verify-change: the OpenTelemetry integration gate fails under Colima without three settings
Skill or agent: `.claude/skills/verify-change/SKILL.md`, the service
OpenTelemetry integration gate.
What happened: a stage worker's final verifier failed with
`rootless Docker not found, failed to create Docker provider` although
`docker info` succeeded on the Colima context. It passed with `DOCKER_HOST`
set to the Colima socket, `TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock`,
and a `TMPDIR` under the home directory, since the default one is not
mounted in the VM. The worker cites
`github.com/testcontainers/testcontainers-go@v0.44.0/internal/core/docker_host.go:126-150`
for the lookup order. That reading is unverified here. No step names these
settings, so each worker rediscovers them.
Suggested change: have the gate read the active Docker context's socket
when `DOCKER_HOST` is unset, or name the three settings in
`verify-change`'s notes on Docker.
