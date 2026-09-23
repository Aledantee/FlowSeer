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

## 2026-09-23 tune: opencode lane usage omits earlier messages
Skill or agent: `.claude/skills/tune/SKILL.md`, step 4, and
`.claude/skills/tune/scripts/bench.sh` opencode lane.
What happened: The step ran as written, but the session message endpoint
returned only the final message's usage and cost. A multi-step Kimi K3 lane
reported $0.03 while Synthetic's pool meter recorded $1.28. The resulting
cost figure understates work done before the final message.
Suggested change: After the run, total usage and cost over all messages in
the opencode session. Compare that total with the pool meter when the lane has
exclusive pool use, before reporting lane cost or comparing models.
