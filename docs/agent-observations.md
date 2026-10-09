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

## 2026-10-09 delegate: a stage worker's report is not routed to a file
Skill or agent: `.claude/skills/delegate/SKILL.md`, Write the brief, item 4,
and `.claude/skills/drive/SKILL.md`, the implement row of the stage table.
What happened: item 4 gives a report file to "a lane whose product is a
report (`critique`, `research`, a review lane, a review stage ...)", and
`.claude/skills/drive/references/review-stage.md` repeats it for review. The
implement and compound stages are not in that list, and `drive` has no
reference for them that says otherwise. Their reports (changed paths, the
focused test result, the hash of each commit, and the policy-surface diffs
that wait for approval) also run long, and `delegate orca-worker read`
returns only the terminal's last screens, so the coordinator of the uv
scripts Phase 4 plan had to ask for a file at each stop. The steps were
followed as written.
Suggested change: in item 4, give every stage worker a report file under the
session scratchpad directory and name that rule in `drive` where it briefs
`implement` and `compound`, not only `review`.
