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

## 2026-10-04 delegate: `wait` prints `limited` for a finished lane whose report says "quota"
Skill or agent: `.claude/skills/delegate/scripts/orca-worker.sh`, `limited()`
at line 66, and `.claude/skills/delegate/references/orca.md`, the `limited`
outcome.
What happened: two lanes that had committed and reported printed `limited`
where `idle` was true. Their reports held "Lite quota 2K credits" and "no
Orca quota request", and the pattern matches the bare word `quota` in the
last 30 screen lines. Any lane working on quota code reads as rate limited.
The step was followed as written: the reference says a finished report that
mentions a limit is handled as `idle`, so the coordinator read each screen
and carried on.
Suggested change: drop the bare `quota` alternative from the pattern, or
require it next to a word such as `exceeded` or `exhausted`.

## 2026-10-04 review: the class instruction in a fix brief has no bound, and a worker changed readers the plan left alone
Skill or agent: `.claude/skills/review/references/fix-loop.md`, One round,
step 1.
What happened: the fix brief named the class as the step requires (every
place a malformed reply reaches `emit` or degrades with no `error`) and told
the worker to find and fix the other sites. The worker rewrote the Google
reader, the Z.ai limits loop, and the Orca window loop of
`.claude/skills/delegate/scripts/pool-usage.sh`, none of which the plan
changed. A Z.ai report without `limits` then printed `signed_in: true` where
it printed `false`, and two rows gained an `error`. The next round found
three behavior findings in that code and restored it. The step was followed
as written.
Suggested change: step 1 bounds the class to code the reviewed change
added or modified. A site in code the change left alone goes in the
worker's report as a note for the coordinator and is not edited.
