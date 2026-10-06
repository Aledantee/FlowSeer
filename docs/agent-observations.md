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

## 2026-10-06 drive: stage branches merge at the end, and the plan state has no branch field
Skill or agent: `.claude/skills/drive/SKILL.md`, step 2, "After each stage", item 3, and `.claude/skills/plan/scripts/plan_record.py`.
What happened: the user corrected the drive of the uv scripts plan. Stage branches are no longer merged into the coordinator branch after each stage. Every stage commits on one branch, and the work is merged once at the end. The user wants that as the default. The user also asked for the branch to be recorded in the plan state. The state contract has no field for it, so the branch went into a `## Branch` section of `docs/plans/2026-10-05-2216-refactor-uv-python-scripts-plan.md`. The step was followed as written and was still the wrong default.
Suggested change: add a `branch` field to the plan state, written by a `plan_record.py branch <plan> <name>` subcommand and read by `show`, `check`, and `plan-queue.py`. Then change `drive` step 2 so later stages run on the recorded branch and the merge happens once, before `land`.
Decided by the user, 2026-10-06: this goes to `plan` as a harness plan and is not applied as a steer edit, since it changes the state contract, `orca-worker.sh`, and `drive` steps 2 and 3. Delete this entry when that plan exists.
