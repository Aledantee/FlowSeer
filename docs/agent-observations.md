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
before `ledger.py` refused the stale receipt. The step was followed as
written. `delegate`, Write the brief, already rules `$TMPDIR` out for briefs
for the same reason.
Suggested change: in both places, name the session scratchpad directory for
a log an unsandboxed command writes and a later command reads, and keep
`$TMPDIR` for files one command writes and reads itself.
