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
