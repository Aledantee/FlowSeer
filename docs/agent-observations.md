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

## 2026-10-05 delegate: no review-unit lane survives when Orca is down and units ran on Sonnet
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Pick the role" with `references/no-orca.md`, as `review` step 3 uses it.
What happened: without Orca, `execute` resolves to `claude-sonnet-5-5` (the only Claude model in its `fit`), so every unit is Anthropic-written. `review-unit` then drops Anthropic by `vendor_differs_from`, and its non-Claude models fall away because the codex pool row lists only `gpt-5.5` and `no-orca.md` gives a non-Claude review lane no reviewer. The phase review had no independent reviewer by construction. The steps were followed as written.
Suggested change: give `review-unit` a `last_resort` that a native subagent can run (for example `claude-opus-5-5`, model differing from the executor), or let `no-orca.md` launch a codex review lane directly when a pool model fits, so a no-Orca host still gets a second reader.
