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

## 2026-09-28 tune: the calibration verifier step race-tests every importer of `pump`
Skill or agent: `.agents/skills/tune/references/calibration.md`, "Acceptance tests" ("Then run the verifier on the candidate's changed paths").
What happened: the step was followed on two `Merge` lanes branched from bd9e0862, with `verify-change.sh -- src/common/pump/merge.go src/common/pump/merge_test.go` run from each lane's root. That base's verifier (`git show bd9e0862:.claude/skills/verify-change/scripts/verify-change.sh`, lines 287 to 315) widens the changed package to every package whose deps include it and runs `go test -race` on the set, which reached `generated/go/mib/...`. The run was still going after 600 s and was stopped. `golangci-lint run ./src/common/pump/` and `go test -race ./src/common/pump/` gave the lint result the grading needs in seconds. The step was followed and still produced the wrong result, so this wants a scoped check, not louder prose.
Suggested change: grade a calibration lane with a fixed package-scoped check (`golangci-lint run ./src/common/pump/` and `go test -race -count=1 ./src/common/pump/`), named in calibration.md, so every lane on every base runs the same check at the same cost.

## 2026-09-28 implement: nothing stops a test-side accessibility suppression
Skill or agent: `.agents/skills/implement/SKILL.md`, step 2 (Work the units), and `AGENTS.md` hard boundaries.
What happened: nothing in `implement` step 2 or the verifier stops a unit from making a failing accessibility audit pass by stripping attributes such as `aria-hidden` in test setup (the audit lives in `frontend/web/src/ui/a11y.test.ts`). The `AGENTS.md` hard boundary forbids that suppression, but only in prose, so only review catches it.
Suggested change: enforce the suppression ban through an automated check under `test/conformance/` or a pre-commit hook that flags attribute stripping and disabled rules in test files, rather than relying on prose.
