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

## 2026-10-13 review: a drive without Orca spends the review stage's one worker on the reviewer
Skill or agent: `.claude/skills/review/SKILL.md`, step 6, and
`.claude/skills/drive/SKILL.md` (the review stage brief).
What happened: the review stage ran with a budget of one sub-worker and
spent it on the independent reviewer. The review skill does not let the
coordinator make fixes itself, so the fix loop did not run until the drive
coordinator re-sent the stage. The steps were followed as written.
Suggested change: have `drive` give the review stage a budget of two
workers when Orca is unreachable (one reviewer, one fixer), or say in
`review` step 6 that a fixer is not counted against a reviewer-only budget.
