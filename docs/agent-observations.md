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

## 2026-09-27 verify-change: a marked path missing on both sides never clears
Skill or agent: `.claude/skills/verify-change/scripts/verify-change.sh`, dirty-marker clearing under `--base`.
What happened: after merging `main` (whose binding-size work deleted 75 files under `generated/go/yang/`) into a branch that never touched `generated/`, `verify-change.sh --base main` ended `FlowSeer verification passed.` but kept `<Bash mutation; verify with --full>` and all 75 paths in `flowseer-verification-dirty`, plus an untracked `frontend/web/.impeccable/live/server.json` that no longer existed. Every listed path was absent from both `HEAD` and `main`, and `git diff --name-only main -- generated go.mod go.sum buf.lock` was empty. The `--full` remedy the land skill names is ruled out on this host because it builds and race-tests `generated/go/yang`. The fast-forward was handed to the person.
Suggested change: treat a marked path that exists in neither the working tree nor the base ref as identical to the base when clearing the marker.
