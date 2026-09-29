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

## 2026-09-29 land: a driven phase plan reached main without its retire
Skill or agent: `.claude/skills/land/SKILL.md`, step 4 (retire), as reached from `drive`.
What happened: `docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-phase1-plan.md` sat on `main` with `status: implemented`, review and compound recorded (`42297bab`, `5c9e8051`), and no `docs(plans): retire` commit. `steer`'s sweep retired it. Whether `land` ran for the phase or the phase reached `main` through a `drive` merge alone is not recorded.
Suggested change: have `drive` hand a finished phase to `land`, or have `land` retire every implemented phase plan on its branch, so a phase that lands never waits for the sweep.
