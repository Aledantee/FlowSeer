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

## 2026-09-27 plan: codegen refactor units under-scoped test files asserting old emitted shapes
Skill or agent: `.claude/skills/plan/SKILL.md`, step 3 (Units).
What happened: The phase2 re-plan under-scoped U2's owned paths: a codegen-shape change must own the tests that assert the old emission shape (`emit_resolve_test.go` asserted the old inline `return "with-hyphen"`; it was invalidated by the enum static-names-table decision and blocked the first implement pass). The step was followed as written, but missed existing tests asserting emitted syntax.
Suggested change: When planning a codegen or emitter shape change, require an explicit search for tests asserting generated source syntax and include all affected test files in the unit's owned `Files:` list.

## 2026-09-27 bench-gate: micro-benchmark baseline is stale after streaming walk redesign
Skill or agent: `src/protocol/snmp/bench/bench-gate.sh` and `testdata/baseline-micro.txt`.
What happened: The bench-gate baseline `src/protocol/snmp/bench/testdata/baseline-micro.txt` is stale: it records pre-streaming allocation levels (TableWalk 364, BulkWalk 481 allocs/op), never re-measured after the 2026-09-03 streaming redesign (commit `7210aa0c`), so `bench-gate.sh` fails for current `main` too. It is a pre-existing gate defect independent of phase 2; phase 2 verified parity with `main` via A/B benchmark runs instead.
Suggested change: Flag for a follow-up task to rebaseline `src/protocol/snmp/bench/testdata/baseline-micro.txt` against post-streaming reality on `main`.
