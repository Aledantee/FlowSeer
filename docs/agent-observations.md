# Agent observations

Entries here record where a FlowSeer skill, agent definition, or hook did not
fit the work: a correction the user had to make to the procedure, a step that
was skipped or done by hand, a brief that missed something. The `compound`
skill's Observe mode appends them. Nothing reads this file automatically.

A maintainer reviews the entries, applies or rejects each through the change
process in [`agent-steering.md`](agent-steering.md), and deletes it. An entry
that sits here is not a rule; the skill or hook it names stays authoritative
until it changes. A lesson about the code belongs under
[`solutions/`](solutions/README.md), not here.

Entry format:

```markdown
## <YYYY-MM-DD> <skill>: <one-line title>
Skill or agent: <path and step>.
What happened: <what was corrected or did not fit>.
Suggested change: <smallest edit to the skill, agent, or hook>.
```

## Entries
