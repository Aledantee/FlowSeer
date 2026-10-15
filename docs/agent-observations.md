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

## 2026-10-15 delegate: a coordinator's record commit broke the next stage's fast-forward in the no-Orca path
Skill or agent: `.claude/skills/delegate/references/no-orca.md`, hand-off between stage branches.
What happened: after one stage finished, the drive coordinator committed a branch record on the plan branch. The next stage worker's `git merge --ff-only` of the previous stage's branch then failed because the two branches had diverged. The reference was followed as written.
Suggested change: have no-orca.md say the coordinator commits its records only after the last stage merge, or that stages merge with `--no-edit` instead of `--ff-only`.

## 2026-10-15 plan: a unit changed a rendered message without listing the test in another unit that pinned the old token
Skill or agent: `.claude/skills/plan/SKILL.md`, Units rules, the output-shape rule ("Grep `*_test.go` and `testdata/` for the distinctive token of the old output").
What happened: U5 of `docs/plans/2026-10-12-1259-feat-netpen-check-model-phase1-plan.md` changed the `Need` message to render fields as flags (the Decision at plan lines 215-218), and its `Files:` line (407) names no ip6 test, while `src/edge/netpen/attacks/ip6/ip6_test.go` at `db1eebc25` (line 211) read the old token `victim-mac`. That file belongs to U9, so U5's verifier stayed red until U9 landed and `implement` verified U5, U6, and U9 together under its red-until rule. The step was written and not followed: the grep it asks for finds the token.
Suggested change: make the grep mechanical rather than louder. The plan's third read, or `plan record` when readiness becomes `implementation-ready`, lists the test and testdata files holding each token a Change line replaces and fails when one lies outside that unit's `Files:` and outside any unit it names in `After:`.

## 2026-10-12 implement: semicolons in comments were replaced mechanically, not rewritten
Skill or agent: `.claude/skills/implement/SKILL.md`, step 3.1 (Finish: read the final diff against `docs/code-style.md`).
What happened: the doc style rules out semicolons in comments, and the implementer cleared about seventy of them in one pass by swapping `; ` for `, and `, which keeps the two clauses welded together. The user corrected this: the fix the style asks for is a rewritten sentence that reads as a person wrote it, which the swap is not. The step was followed as written, since it names the style but not how to apply it at the end.
Suggested change: in Finish item 1, add one sentence: a comment that fails the doc style is rewritten by hand, and a mechanical substitution across many files (semicolon to conjunction, a phrase swap) is not a fix.
