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

## 2026-09-06 implement: Bash edits force a full verifier run
Skill or agent: `.claude/skills/implement/SKILL.md`, step 2 and Finish.
What happened: several unit edits went through `sed` and a Python
heredoc in Bash. The Bash hook recorded a `<Bash mutation; verify with
--full>` marker, so a documentation-only change ended in a full module
race run of about twenty minutes, and the first attempt aborted because
another worktree's golangci-lint was running. The step was followed as
written; nothing in it says which tool to edit with.
Suggested change: in step 2, say that edits go through the editor tools
and that a Bash write to a source file costs a `--full` run at Finish.
