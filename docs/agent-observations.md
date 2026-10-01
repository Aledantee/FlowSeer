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

## 2026-10-01 codex: hook command resolution is unverified on lane PATHs
Skill or agent: `.codex/hooks.json` project hook commands and
`/Users/aledante/.codex/hooks.json` SessionStart hook.
What happened: Codex lanes reported `Hook failed: hook exited with code 127`.
The project commands invoke bare `git` while constructing every hook path
(`.codex/hooks.json:9-79`), and the user SessionStart hook invokes bare `bash`
(`/Users/aledante/.codex/hooks.json:47-52`). A lane PATH without either
executable can fail before the target script reports its own error. The exact
failing command was not isolated.
Suggested change: run hook launchers through resolved executable paths, or add a
startup check that reports which command is missing under the lane PATH.
Status: awaiting guardrail review. The staged `.codex/hooks.json` commands
report an unresolved root, a missing `git`, or a missing script and exit 1,
pinned in `tools/hooks/tests/run.sh`. Every project hook exits 0 under
`PATH=/usr/bin:/bin`, so a lane PATH is not shown to be the cause. A session
directory outside the repository reproduces the 127. The user-level file is
outside the repository and unchanged.
