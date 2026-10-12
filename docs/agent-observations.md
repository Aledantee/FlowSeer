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

## 2026-10-12 implement: the per-unit verifier run checks whitespace against the unit's own commit
Skill or agent: `.claude/skills/implement/SKILL.md`, step 2.5, and
`references/workers.md` (the wave's verifier run after the merge).
What happened: the step was followed as written. Each unit committed, then
ran `verify-change.sh -- <paths>`, whose base defaults to `HEAD`
(`verify-change.sh:44`), so its `git diff --check "$base"` gate saw an
empty diff. Three committed patch files with "space before tab in indent"
and a trailing blank line passed every per-unit and per-wave run, and only
the Finish run with `--base main` failed, two rounds before it passed.
Suggested change: have step 2.5 and the wave run pass `--base <the unit's
ledger base>` (the `base` the ledger records at `in_progress`), or have the
verifier's whitespace gate diff from the merge base with `main` when no
base is given.

## 2026-10-12 next: a phase that waits on another parent's phase reads as ready to re-plan
Skill or agent: `.claude/skills/next/SKILL.md`, step 1, and `plan record
after` (`tools/scripts/lib/plans.py:217`).
What happened: the step was followed and still produced the wrong result.
`next plan-queue` put Deployment, Phase 1 in `replan` because its state
holds `after: []`. Its parent plan says no phase is written ready until the
central high availability plan lands, and central still embeds NATS and
keeps its certificate under `state_dir`, so the phase cannot be planned yet.
Recording the dependency with `plan record after` failed: "after names
docs/plans/2026-10-03-1533-feat-central-high-availability-phase3-plan.md,
which is neither in the phases nor in the retired list of
docs/plans/2026-10-03-1533-feat-deployment-plan.md". Deployment, Phase 3
(waits on edge high availability) has the same shape.
Suggested change: let `after` name a phase or plan of another parent, and
have `next plan-queue` read an unfinished one as `waiting`. Failing that, a
state field such as `blocked_by: <plan>` that `next` reads the same way.
