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

## 2026-10-04 review: coordinator fixes can bypass review rounds
Skill or agent: `.claude/skills/review/SKILL.md`, steps 5 and 6.
What happened: The `apply the fixes here` and `apply chosen findings only` options allow coordinator edits to a security or behavior finding, followed by `accept after fixes`, without a delegated reviewer or an incremented round count. The review loop's reviewer and round-cap rules can therefore be bypassed by choosing the coordinator path.
Suggested change: Require the same reviewed round, or a separately recorded review and round count, before accepting coordinator-applied security or behavior fixes.

## 2026-10-04 review: the rework row has no option for an oversized fix
Skill or agent: `.claude/skills/review/SKILL.md`, step 6, and `references/fix-loop.md`, When to stop.
What happened: `rework` includes a security or behavior finding too large to fix in place, including at round zero or one, but step 6 points only to When to stop. That section gives choices for a second round that is not clean, not for this oversized finding, so the coordinator has no specified next action.
Suggested change: State the plan, drop-or-replace, and stop options for an oversized security or behavior finding in the `rework` row, including the security case.

## 2026-10-04 land: planless checkpoint recovery leaves replace semantics incomplete
Skill or agent: `.claude/skills/land/references/missing-checkpoint.md`, planless checkpoint handoff.
What happened: The text forbids `--replace` for this session's `implemented:` write and the review worker's `gaps:` and `rounds:` writes, but omits this session's `review:` and `compound:` writes. Replacing the file during either recovery can erase other checkpoints and leave land with an incomplete record.
Suggested change: State one append-only rule for every coordinator and worker checkpoint write in this flow, with `--replace` reserved for the implement writer that initializes the file.

## 2026-10-04 delegate: an empty brief can start a worker without a task
Skill or agent: `.claude/skills/delegate/scripts/orca-worker.sh`, `start` validation.
What happened: `start` checks that the brief path is a file, but accepts a zero-byte brief and sends it to the worker. A worker can therefore begin without the task that its lane was meant to carry.
Suggested change: require a non-empty regular file before creating the worktree or launching the worker, and report that the brief is empty.

## 2026-10-04 delegate: a dead child lane blocks parent cleanup
Skill or agent: `.claude/skills/delegate/scripts/orca-worker.sh`, `stop`, and parent review or fix cleanup.
What happened: `stop` refuses a parent while a child worktree remains and then refuses a dirty child. A dead fix lane can therefore keep its parent review lane from being released even when the parent has finished.
Suggested change: add a recovery step to the review fix loop and drive cleanup that inspects and settles dead child lanes before stopping the parent, while preserving dirty work for a person to read.

## 2026-10-03 review: fix rounds reopened on behavior changes until bounded to tests and records against a fixed mutation set
Skill or agent: `.claude/skills/review/references/fix-loop.md`, step 1.
What happened: Fix workers in rounds 4 and 5 introduced production code modifications that produced new edge-case findings during re-review, reopening the review loop. The loop closed in round 6 only after a recorded decision bounded fixes to test assertions and documentation alignments against the fixed list of 13 remaining mutations, freezing production behavior.
Suggested change: in `.claude/skills/review/references/fix-loop.md`, after requirements are settled or when a loop exceeds three rounds, instruct fix briefs to freeze production code and restrict changes to tests and documentation against the identified defect set.

## 2026-10-03 delegate: orca-worker wait reads idle while an agy background verifier runs
Skill or agent: `.claude/skills/delegate/scripts/orca-worker.sh`, `wait` command.
What happened: `agy` workers can end their turn while their verifier command continues in a background task. The terminal screen settles without a working indicator, so `orca-worker.sh wait` reports `idle` before verification finishes and before changes are committed. This occurred twice in this drive: once during implementation and once in round 6 fix lane A.
Suggested change: in `orca-worker.sh wait`, check for active background tasks or inspect git worktree commit status before declaring the lane idle, or instruct `agy` worker briefs to run verifier commands synchronously with an adequate timeout.

## 2026-10-03 review: parallel fix briefs touching shared test files trigger merge-check lost change failure on equivalent resolution
Skill or agent: `.claude/skills/review/references/fix-loop.md`, steps 1 and 2.
What happened: In round 1, two parallel fix workers were dispatched concurrently. One worker's brief included an instruction to clean up plan labels in integration tests, causing both workers to edit comment lines in `src/services/device/test/integration/openfga_model_test.go`. Although both changes were equivalent resolutions removing the same obsolete comment syntax, `merge-check.py` on the merge commit (`f4f121d3`) reported a lost change for the second parent and exited non-zero, stopping the round.
Suggested change: in `.claude/skills/review/references/fix-loop.md` step 1, instruct coordinators to enforce disjoint file sets across parallel fix workers and forbid broad style or comment cleanup outside a worker's assigned finding files. In `merge-check.py`, recognize equivalent comment-only removals between parents.

## 2026-10-03 delegate: orca-worker terminal wait times out during concurrent Claude lane startup
Skill or agent: `.claude/skills/delegate/scripts/orca-worker.sh`, `start` command.
What happened: During heavy multi-lane dispatch, `orca terminal wait --for tui-idle --timeout-ms 90000` timed out three times while Claude CLI initialized and queried models, causing lane startup failures before the brief pointer could be sent.
Suggested change: in `orca-worker.sh start`, increase the startup wait timeout or add a retry loop around `orca terminal wait` before declaring terminal initialization failed.

## 2026-10-04 delegate: `wait` prints `limited` for a finished lane whose report says "quota"
Skill or agent: `.claude/skills/delegate/scripts/orca-worker.sh`, `limited()`
at line 66, and `.claude/skills/delegate/references/orca.md`, the `limited`
outcome.
What happened: two lanes that had committed and reported printed `limited`
where `idle` was true. Their reports held "Lite quota 2K credits" and "no
Orca quota request", and the pattern matches the bare word `quota` in the
last 30 screen lines. Any lane working on quota code reads as rate limited.
The step was followed as written: the reference says a finished report that
mentions a limit is handled as `idle`, so the coordinator read each screen
and carried on.
Suggested change: drop the bare `quota` alternative from the pattern, or
require it next to a word such as `exceeded` or `exhausted`.

## 2026-10-04 review: the class instruction in a fix brief has no bound, and a worker changed readers the plan left alone
Skill or agent: `.claude/skills/review/references/fix-loop.md`, One round,
step 1.
What happened: the fix brief named the class as the step requires (every
place a malformed reply reaches `emit` or degrades with no `error`) and told
the worker to find and fix the other sites. The worker rewrote the Google
reader, the Z.ai limits loop, and the Orca window loop of
`.claude/skills/delegate/scripts/pool-usage.sh`, none of which the plan
changed. A Z.ai report without `limits` then printed `signed_in: true` where
it printed `false`, and two rows gained an `error`. The next round found
three behavior findings in that code and restored it. The step was followed
as written.
Suggested change: step 1 bounds the class to code the reviewed change
added or modified. A site in code the change left alone goes in the
worker's report as a note for the coordinator and is not edited.
