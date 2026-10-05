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

## 2026-10-05 plan: a re-planned phase keeps its `Landed:` range in the parent
Skill or agent: `.claude/skills/plan/references/replan-implemented.md`, steps 1 to 4.
What happened: the reset covers the plan's own fields, Units, and ledger. For
a phase plan the parent still holds the `Landed:` range `implement` wrote
(`implement/references/outcome-records.md`), so `plan-queue.py` reads a
dependent phase's prerequisite as landed and groups it `in-progress`, not
`waiting`, while the re-planned phase is open. A parent that reads
`implemented` stays so. The step was followed as written.
Suggested change: in the same edit, clear this phase's `Landed:` line in the
parent and set a parent that reads `implemented` back to `planned`. Check
first that nothing reads the first run's range before the next `implement`
Finish.

## 2026-10-05 plan: the `Waves:` line is edited, not rebuilt, on a re-plan
Skill or agent: `.claude/skills/plan/references/replan-implemented.md`, step 3 and the example.
What happened: step 3 removes a built unit's id from the `Waves:` line. With
U1 and U2 built and U4 `After: U2`, U3 and U4 both end `After: none` and the
graph yields `Waves: U3 U4`, while removing ids leaves `Waves: U3 | U4`.
`plan/SKILL.md` says to write the waves the graph yields. `implement` reads
the `After:` lines, so execution is unaffected. The example does not state
U4's `After:`.
Suggested change: "rewrite the `Waves:` line from the `After:` lines that
remain", and give U4 `After: U3` in the example.

## 2026-10-05 next: two sent-back phase states have no queue test
Skill or agent: `.claude/skills/next/scripts/test_plan_queue.py`, the sent-back phase tests.
What happened: `plan-queue.py` groups both states as intended and no test
pins them. A sent-back phase changed on the branch with `review: accept` and
a compound field, `Landed:` empty, must read `replan`: the replan condition
extended with `and not (rel in changed_here and rel in phase_of and review
in ACCEPTED)` passes all 12 tests. A sent-back phase with `review: rework`
and an unlanded prerequisite must read `waiting`: `"waiting" if missing and
review != "rework" else "replan"` passes them too.
Suggested change: run the branch-side phase fixture over three frontmatter
states with `subTest` (`review: rework`, `review: accept` with compound, and
no review), and add a `review: rework` companion to
`test_sent_back_phase_waits_for_an_unlanded_prerequisite`.
