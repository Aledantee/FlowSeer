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

## 2026-09-16 plan: a unit's Tests line prescribed the test that could not catch the risk the plan named
Skill or agent: `.claude/skills/plan/SKILL.md`, the per-unit `Tests:` line.
What happened: phase 3d's Open questions said the MST BPDU octet layout "is
verifiable from no in-repo file" and that "the tests prove only the round-trip
and the body lengths, and no peer capture is available here"
(`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-phase3d-plan.md`).
Unit U2's Tests line then asked for "the R14a-wire round-trip byte for byte",
which is symmetric and passes whatever placement the encoder chooses.
`implement` followed it, and two fields shipped in each other's octets until
review found it (fixed in `02ec81b0`). The step was followed as written and
still produced the wrong result.
Suggested change: have `plan` check each unit's Tests line against that unit's
own risks, so a risk the plan states as unverifiable by the tests either gets a
test that pins it or an explicit note that nothing in the unit covers it.
