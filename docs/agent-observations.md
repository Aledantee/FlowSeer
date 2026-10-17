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

## 2026-10-16 plan: a removal scope excluded citation repairs that tests require
Skill or agent: `.claude/skills/plan/SKILL.md`, steps 2 and 3.
What happened: the phase 4 removal plan excluded operator-authorization record
edits while shortening files that record cites. The scope did not fit the
existing citation gate. `TestTheDirectionRecordCitationsAndDecisions` checks
that cited line ranges fit their files
(`src/services/device/test/integration/lab_fixtures_test.go:707-755`).
Commits `51887643` and `f24d3bc9` record the blocked scope and its amendment.
Suggested change: when scoping a removal, inspect tests that validate incoming
documentation references and include required citation repairs before marking
the plan ready. Distinguish those repairs from changes to accepted decisions.

## 2026-10-15 delegate: a coordinator's record commit broke the next stage's fast-forward in the no-Orca path
Skill or agent: `.claude/skills/delegate/references/no-orca.md`, hand-off between stage branches.
What happened: after one stage finished, the drive coordinator committed a branch record on the plan branch. The next stage worker's `git merge --ff-only` of the previous stage's branch then failed because the two branches had diverged. The reference was followed as written.
Suggested change: have no-orca.md say the coordinator commits its records only after the last stage merge, or that stages merge with `--no-edit` instead of `--ff-only`.
