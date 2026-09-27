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

## 2026-09-27 verify-change: selects no gate for frontend web paths
Skill or agent: `.claude/skills/verify-change/scripts/verify-change.sh`, path classification.
What happened: running `.claude/skills/verify-change/scripts/verify-change.sh -- <paths>` on `.vue`, `.ts`, or `.css` files under `frontend/web/` failed with `FlowSeer verification FAILED (exit 2)` because `gates_selected` remained false. The verifier script classifies paths for Go, protobuf, MIB, and Markdown, but selects no build, lint, or test gate for web paths. The step was followed as written. Web verification was performed directly with `./node_modules/.bin/vue-tsc --noEmit`, `eslint .`, `prettier --check .`, and `vitest run` from `frontend/web`.
Suggested change: add a path classification arm for `frontend/web/*` in `verify-change.sh` that selects a web gate running the `vue-tsc`, `eslint`, `prettier`, and `vitest` checks, calling local `node_modules/.bin/` binaries directly to avoid sandbox hangs under `pnpm <script>`.
