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

## 2026-09-23 review: unit reviewers reused the executor's vendor
Skill or agent: `.claude/skills/delegate/SKILL.md`, step 3.
What happened: In the first review pass for the agent run field plan, the
review-seam worker sent both `review-unit` tasks to `gpt-6-sol`, the executor's
model. The vendor exclusion was skipped, and the coordinator had to restate it
in the second brief. The run log records both reviewer models.
Suggested change: Check each `review-unit` dispatch against the executor's
vendor and reject a matching vendor before the reviewer starts.

## 2026-09-23 drive: stage worker merged into the coordinator's branch
Skill or agent: `.claude/skills/drive/SKILL.md`, "After each stage," step 3.
What happened: In the second review pass, the worker fast-forwarded its own
branch into the coordinator's worktree. That merge belongs to the coordinator;
the coordinator branch's reflog records the fast-forward merge.
Suggested change: Record the coordinator's HEAD before dispatch and fail the
stage if it changes before the coordinator's merge step.
