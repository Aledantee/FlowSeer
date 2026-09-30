# Agent observations

Entries here record where a FlowSeer skill, agent definition, or hook did not
fit the work: a correction the user had to make to the procedure, a step that
was skipped or done by hand, a brief that missed something, a sequence that
recurs with no skill behind it. The `compound` skill's Observe mode appends
them. Nothing reads this file on its own; the `steer` skill works the queue
when a maintainer asks for it.

`steer` applies or rejects each entry through the change process in
[`agent-steering.md`](agent-steering.md) and deletes it. Edits to a hook, a
hook registration, or `AGENTS.md` stop at a staged diff for a person's
review, since those are policy surfaces. An entry that sits here is not a
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

## 2026-09-30 steer: Bash test assertions are not fail-fast
Skill or agent: `tools/hooks/tests/run.sh`, the bare `[[ ]]` assertions around
lines 70-107.
What happened: the assertions rely on `set -e`, but the host's `/usr/bin/env
bash` is GNU bash 3.2.57 on arm64-apple-darwin25. A direct check,
`bash -c 'set -euo pipefail; [[ a == b ]]; echo reached'`, prints `reached`
and exits 0. A failing check can therefore report success.
Suggested change: route each assertion through an explicit `if` or status
check, and add a regression case for a deliberately false assertion.

## 2026-09-30 implement: passed-unit commits were not verified against the worker base
Skill or agent: `.claude/skills/implement/SKILL.md`, the unit completion step
that records a passed unit's `HEAD` after verification.
What happened: U8 and U9 were recorded with `139391fe` and `b64fc806`, yet
those commits are earlier ancestors in the current history and contain the
unit changes from an earlier review round. No unit commit represented the
worker's completed pass. The implement guidance says to record `HEAD`, while
the tree check had to detect that the recorded commits predated the relevant
work.
Suggested change: require a passed-unit commit to be reachable from the
worker base and the worker's new `HEAD`, then reject an ancestor reused from
an earlier round.

## 2026-09-30 implement: independent units halted behind a blocked prerequisite
Skill or agent: `.claude/skills/implement/SKILL.md`, the wave grouping and unit
progress steps.
What happened: U5 has `After: none` in
`docs/plans/2026-09-30-1036-fix-unverified-external-claims-plan.md`, but it was
halted when U1 was blocked. Work with satisfied prerequisites should have
continued independently.
Suggested change: keep blocked units in their own state and dispatch every
other unit whose `After` prerequisites are satisfied.

## 2026-09-30 implement: plan-deviations rejects unquoted Files lines
Skill or agent: `.claude/skills/implement/scripts/plan-deviations.py`, its
`QUOTED` parser and `no unit with a Files field` guard.
What happened: the helper only collects backtick-quoted paths, while this
plan writes paths after unquoted `Files:` fields. Running it against the plan
returns `no unit with a Files field in docs/plans/2026-09-30-1036-fix-unverified-external-claims-plan.md` with status 2.
Suggested change: parse both backtick-quoted and unquoted comma-separated
paths, while retaining the existing symbol and directory handling.

## 2026-09-30 implement: commit bodies contain literal backslash-n text
Skill or agent: `.claude/skills/implement/SKILL.md`, the unit commit step and
its requirement for one line per test failure.
What happened: the bodies of `39ef5aa6`, `07473cae`, and `eee9557b` contain
literal `\\n` sequences where commit paragraphs should contain newlines. The
history therefore records escaped formatting instead of readable test
evidence.
Suggested change: make the commit helper pass separate message paragraphs or
write the body through standard input, then reject a body containing literal
`\\n` before accepting the unit.

## 2026-09-30 review: repeated concurrency fixes should trigger prior-art search
Skill or agent: `.claude/skills/review/SKILL.md` fix loop and `.claude/skills/plan/SKILL.md` re-plan trigger.
What happened: The phase 2 plan records five review rounds on the tenant claim before the record-first rollback protocol was replaced with an atomic batch (operator authorization phase 2, landed 2026-09-30; the retired plan's Review section is in git history). The local fix loop still left concurrency failures until a prior-art-based re-plan changed the mechanism.
Suggested change: After a second review round finds another failure in the same concurrency mechanism, pause the fix loop and require a bounded prior-art search or a re-plan before another patch.
