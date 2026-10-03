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

## 2026-10-03 review: fix rounds reopened on behavior changes until bounded to tests and records against a fixed mutation set
Skill or agent: `.claude/skills/review/references/fix-loop.md`, step 1.
What happened: Fix workers in rounds 4 and 5 introduced production code modifications that produced new edge-case findings during re-review, reopening the review loop. The loop closed in round 6 only after a recorded decision bounded fixes to test assertions and documentation alignments against the fixed list of 13 remaining mutations, freezing production behavior.
Suggested change: in `.claude/skills/review/references/fix-loop.md`, after requirements are settled or when a loop exceeds three rounds, instruct fix briefs to freeze production code and restrict changes to tests and documentation against the identified defect set.

## 2026-10-03 delegate: orca-worker wait reads idle while an agy background verifier runs
Skill or agent: `.claude/skills/delegate/scripts/orca-worker.sh`, `wait` command.
What happened: `agy` workers can end their turn while their verifier command continues in a background task. The terminal screen settles without a working indicator, so `orca-worker.sh wait` reports `idle` before verification finishes and before changes are committed. This occurred twice in this drive: once during implementation and once in round 6 fix lane A.
Suggested change: in `orca-worker.sh wait`, check for active background tasks or inspect git worktree commit status before declaring the lane idle, or instruct `agy` worker briefs to run verifier commands synchronously with an adequate timeout.
