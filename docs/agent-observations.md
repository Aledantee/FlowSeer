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

## 2026-09-17 delegate: a brief written to $TMPDIR reached a worker as another session's brief
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Write the brief", and
`references/herdr.md`.
What happened: the brief was written by a sandboxed Bash command, whose
`$TMPDIR` is the per-session sandbox directory, and read by the unsandboxed
`herdr-worker.sh start`, whose `$TMPDIR` is the shared system one. The path
resolved on both sides, so no step failed: the worker was dispatched with a
stale brief another session had left at the same relative path, reported the
task "already implemented", and changed nothing. The skill names the
scratchpad directory only for a reviewer's diff file.
Suggested change: say that a brief, like a diff, is written to the session
scratchpad directory, because every runtime command runs unsandboxed and
`$TMPDIR` does not mean the same directory on both sides of that boundary.

## 2026-09-18 review: a record's premise went stale while its paths were kept current
Skill or agent: `.claude/skills/review/SKILL.md`, and the amendment step every
phase plan writes.
What happened: three phases of a refactor amended six architecture records so
every path they cite matches the tree, and a reviewer then found that one
record's opening premise ("FlowSeer has no streaming RPC and no chunked payload
anywhere in spec/proto") had been false since an earlier phase landed five of
them. Each amendment pass checked the citations it was given and nothing else.
The step was followed as written.
Suggested change: when a change amends a record, check the record's premises
against the tree, not only its paths and full names. A record whose paths are
current and whose premise is false is worse than a stale one, because the fresh
paths make the reader trust the premise.
