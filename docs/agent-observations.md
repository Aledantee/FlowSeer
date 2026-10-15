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

## 2026-10-12 implement: semicolons in comments were replaced mechanically, not rewritten
Skill or agent: `.claude/skills/implement/SKILL.md`, step 3.1 (Finish: read the final diff against `docs/code-style.md`).
What happened: the doc style rules out semicolons in comments, and the implementer cleared about seventy of them in one pass by swapping `; ` for `, and `, which keeps the two clauses welded together. The user corrected this: the fix the style asks for is a rewritten sentence that reads as a person wrote it, which the swap is not. The step was followed as written, since it names the style but not how to apply it at the end.
Suggested change: in Finish item 1, add one sentence: a comment that fails the doc style is rewritten by hand, and a mechanical substitution across many files (semicolon to conjunction, a phrase swap) is not a fix.
