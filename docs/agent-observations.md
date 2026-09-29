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

## 2026-09-28 implement: nothing stops a test-side accessibility suppression
Skill or agent: `.agents/skills/implement/SKILL.md`, step 2 (Work the units), and `AGENTS.md` hard boundaries.
What happened: nothing in `implement` step 2 or the verifier stops a unit from making a failing accessibility audit pass by stripping attributes such as `aria-hidden` in test setup (the audit lives in `frontend/web/src/ui/a11y.test.ts`). The `AGENTS.md` hard boundary forbids that suppression, but only in prose, so only review catches it.
Suggested change: enforce the suppression ban through an automated check under `test/conformance/` or a pre-commit hook that flags attribute stripping and disabled rules in test files, rather than relying on prose.

## 2026-09-29 delegate: a land or drive stage without Orca stops instead of using a native subagent
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Orca or native", the
paragraph on stage workers of `land` or `drive`, and
`.claude/skills/land/SKILL.md`, step 2, which defers to it ("without Orca,
its 'Orca or native' stops here").
What happened: without Orca, the coordinator stopped a stage and handed it
to the user for a fresh session, as the paragraph says. The user
corrected the procedure: a native subagent is allowed when nothing else is
available. The step was followed as written.
Suggested change: without Orca, run the stage in a native `general-purpose`
subagent in the worktree, pinned to a model other than the change's author,
and stop only when no native subagent can run.

## 2026-09-29 delegate: a review stage fell back to Sonnet with no machine-wide registry
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Pick the role, then
resolve the lane".
What happened: with no `~/.claude/models/registry.yaml`, the coordinator
picked Sonnet for a `review-seam` stage. The user corrected it: review runs
on Opus or Fable, never on Sonnet. The skill states no default for a
missing registry, so the step gave no rule to follow. Where the project
registry is read, its review fit sets already omit Sonnet
(`.claude/models/registry.yaml:76-77`).
Suggested change: state that review roles' `fit` and `last_resort` never
hold Sonnet, and that with no registry read a review lane defaults to Opus
or Fable, whichever did not author the change.
