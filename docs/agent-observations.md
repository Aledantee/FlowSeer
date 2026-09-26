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

## 2026-09-26 delegate: codex update dialog dropped terminal to shell before brief was read
Skill or agent: `.claude/skills/delegate/scripts/orca-worker.sh`, start command (codex branch).
What happened: On 2026-09-26 with codex 0.157.0, answering the startup update dialog triggered `brew upgrade codex` and dropped the terminal to a shell ("Update ran successfully! Please restart Codex."). The lane never read its brief, and `wait` later reported idle. The step was followed as written; the option numbering or match string no longer matches current codex prompts.
Suggested change: Detect a shell prompt or the "restart Codex" message after the update dialog and fail `start` with undo, or launch codex with update checks disabled.

## 2026-09-26 delegate: three-model fix round left no eligible review-seam model
Skill or agent: `.claude/skills/delegate/SKILL.md`, step 3 (`model_differs_from` and `vendor_differs_from`).
What happened: After a fix round where three models (Claude Opus 5.5, gpt-5.6-sol, and gemini-3.8-flash) each wrote part of the branch, no single review-seam model was legal under the selection rules. The coordinator split the re-review into two lanes by commit authorship. The step could not be followed as written.
Suggested change: State that a re-review is split by the writer of each commit when no fit model is free of all writers.

## 2026-09-26 delegate: codex lane logged hook exit code 127
Skill or agent: `.codex/hooks.json` or `.claude/skills/delegate/scripts/orca-worker.sh`.
What happened: A codex lane printed "Hook failed — hook exited with code 127" early in its turn on worker fix-delegate2 on 2026-09-26. Cause was uninvestigated; likely a command registered in `.codex/hooks.json` was missing from PATH in the worker environment. The step was followed as written.
Suggested change: Verify that all commands invoked by `.codex/hooks.json` exist on PATH or provide fallbacks when missing.
