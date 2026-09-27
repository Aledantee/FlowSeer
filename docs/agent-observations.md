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

## 2026-09-26 delegate: three-model fix round left no eligible review-seam model
Skill or agent: `.claude/skills/delegate/SKILL.md`, step 3 (`model_differs_from` and `vendor_differs_from`).
What happened: After a fix round where three models (Claude Opus 5.5, gpt-5.6-sol, and gemini-3.8-flash) each wrote part of the branch, no single review-seam model was legal under the selection rules. The coordinator split the re-review into two lanes by commit authorship. The step could not be followed as written.
Suggested change: State that a re-review is split by the writer of each commit when no fit model is free of all writers.

## 2026-09-26 delegate: codex lane logged hook exit code 127
Skill or agent: `.codex/hooks.json` or `.claude/skills/delegate/scripts/orca-worker.sh`.
What happened: A codex lane printed "Hook failed — hook exited with code 127" early in its turn on worker fix-delegate2 on 2026-09-26. Cause was uninvestigated; likely a command registered in `.codex/hooks.json` was missing from PATH in the worker environment. The step was followed as written.
Suggested change: Verify that all commands invoked by `.codex/hooks.json` exist on PATH or provide fallbacks when missing.

## 2026-09-27 verify-change: no build, test, or lint gate mapping for frontend web files
Skill or agent: `.claude/skills/verify-change/scripts/verify-change.sh`, path dispatching (`gates_selected`).
What happened: The step was followed as written. When running `verify-change.sh` against changed paths in `frontend/web/`, the verifier reported "FlowSeer verification selected no build, test or lint gate for these paths" and exited with code 2. The script dispatches Go, Proto, MIB, and Markdown paths, but has no gate mapping for TypeScript, Vue, or CSS files under `frontend/web/`. As a result, web build, test, lint, and formatting checks had to run manually.
Suggested change: Add path matching for `frontend/web/` in `verify-change.sh` to trigger web workspace gates (such as `pnpm test`, `pnpm typecheck`, `pnpm lint`, and `pnpm format:check`).

## 2026-09-27 review: hot pool dropped second reviewer on diff exceeding 1,500 lines
Skill or agent: `.claude/skills/review/SKILL.md`, step 3 ("Dispatch the reviewer"), and `.claude/skills/delegate/SKILL.md`, step 3.
What happened: Phase 4 changed 2,410 lines across 40 files, exceeding the 1,500-line threshold where review step 3 requires dispatching multiple reviewers by subsystem in parallel. Because the executor was gemini-3.8-flash, delegate step 3 excluded Google models for review independence. When the claude pool was hot (>85%), claude-opus-5-5 was dropped, leaving only gpt-5.6-sol. Neither skill defines how to size review lanes when diff volume exceeds one reviewer but pool headroom leaves only a single model eligible. The coordinator dispatched a single reviewer on gpt-5.6-sol covering the entire diff, noting that the second reviewer was dropped due to the hot pool. The step could not be followed as written.
Suggested change: State that when diff size requires multiple reviewers but pool headroom and vendor exclusion leave a single model eligible, the coordinator may split subsystem reviews across multiple lanes on that single model/pool if slots permit, or proceed with a single reviewer and log the reduced redundancy.

## 2026-09-27 drive: dependent phase started before predecessor review stopped writing
Skill or agent: `.claude/skills/drive/SKILL.md`, step 3, and `.claude/skills/drive/scripts/plan-state.py`, dependency scheduling.
What happened: The procedure was followed as written. Phase 3 became ready when Phase 2 had an implementation range in its parent `Landed:` field, although Phase 2 still needed review. The Phase 2 review fix loop then followed `review` step 6 and fixed every site in each defect class, including Phase 3 files that were now changing in parallel. Reconciling the accepted review branches produced content conflicts in `GlobalSearch.vue`, `a11y.test.ts`, `UiCombobox.vue` and its test, and the dropdown-menu and popover stories. The merge had to combine two independently verified fixes to the same mechanisms.
Suggested change: Keep a phase that declares `After: <predecessor>` waiting until the predecessor reaches `done` through implementation, review, and compound. If implementation-only dependencies remain useful, give them a separate field instead of treating `Landed:` as completion for scheduling.

## 2026-09-27 verify-change: a marked path missing on both sides never clears
Skill or agent: `.claude/skills/verify-change/scripts/verify-change.sh`, dirty-marker clearing under `--base`.
What happened: after merging `main` (whose binding-size work deleted 75 files under `generated/go/yang/`) into a branch that never touched `generated/`, `verify-change.sh --base main` ended `FlowSeer verification passed.` but kept `<Bash mutation; verify with --full>` and all 75 paths in `flowseer-verification-dirty`, plus an untracked `frontend/web/.impeccable/live/server.json` that no longer existed. Every listed path was absent from both `HEAD` and `main`, and `git diff --name-only main -- generated go.mod go.sum buf.lock` was empty. The `--full` remedy the land skill names is ruled out on this host because it builds and race-tests `generated/go/yang`. The fast-forward was handed to the person.
Suggested change: treat a marked path that exists in neither the working tree nor the base ref as identical to the base when clearing the marker.
