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

## 2026-09-25 drive: reviewer fix-loop boundary scoped too narrowly degraded an accept to rework
Skill or agent: `.claude/skills/drive/SKILL.md`, step 2 (review brief), and `.claude/skills/review/SKILL.md`, step 6 (fix loop).
What happened: The review stage brief scoped the changed paths too narrowly, excluding `src/edge/netpen/test/integration/lab/iosxe.go` and `src/edge/netpen/Taskfile.yml`. The reviewer returned `rework` with three clean fixes it could not apply in-boundary, requiring coordinator intervention to widen the boundary. The steps were followed as written.
Suggested change: State in `drive`'s review brief-writing rules that a fix-loop boundary must include the owning-layer file a test references (so a helper addition in the lab layer stays in-boundary) and any neighbor file the change made stale (such as taskfile descriptions or docs).

## 2026-09-25 review: fix-loop worker left its edits uncommitted, stalling the reviewer
Skill or agent: `.claude/skills/review/SKILL.md` step 6 (fix loop) and `.claude/skills/delegate` (the worker brief for a dispatched fix worker).
What happened: during the flowssh review fix loop, the review worker dispatched a fix worker (agy/gemini) that made the correct edits but did not commit them; the reviewer then polled its branch for a commit that never came and burned ~8 minutes in sleep loops before the coordinator intervened, read the uncommitted diff from the fix worker's tree, and had the reviewer apply and commit it. The steps were followed as written.
Suggested change: a fix worker's brief must state that it commits its changes on its branch (the reviewer/coordinator reads the commit, not the working tree), and the reviewer's fix loop should check the fix worker's tree for uncommitted changes when its commit poll times out rather than spinning.

## 2026-09-25 plan: 300-line phase-split trigger forced split logic on inventory-shaped plans
Skill or agent: `.claude/skills/plan/references/phases.md`.
What happened: The plan ran to 530 lines because it carried an audit inventory of sites across 1,228 files rather than complex architecture decisions. Following `phases.md`'s 300-line trigger would have split the plan into five subsequent phases marked `needs-decisions`, forcing later sessions to re-derive the inventory. The author explicitly opted out of the split under Decisions, and all six units landed successfully in a single plan. The rule was followed as written until the author made an explicit exception.
Suggested change: Add a clause to `phases.md` exempting inventory-shaped refactor and conformance plans whose line count is driven by site enumerations rather than unresolved decisions.

## 2026-09-25 drive: orca-worker invocation named unsupported flags and missing grade subcommand
Skill or agent: `.claude/skills/drive/SKILL.md`, step 2 and step 5, and `.claude/skills/delegate/scripts/orca-worker.sh`.
What happened: `drive` step 2 directs launching workers with `orca-worker.sh start --role <role> --plan <path> --unit <stage>`, and step 5 directs grading lanes with `orca-worker.sh grade <slug> --outcome ... --verify ...`. `orca-worker.sh start` accepts neither `--role`, `--plan`, nor `--unit`, and has no `grade` subcommand. The coordinator had to work around the discrepancy by passing supported flags and omitting the grade step. The steps could not be followed as written.
Suggested change: Update `drive` steps 2 and 5 to use only supported flags of `orca-worker.sh`, or implement `--role`, `--plan`, `--unit`, and `grade` in `orca-worker.sh`.

## 2026-09-25 drive: orca-worker wait returned idle mid-turn on review lane
Skill or agent: `.claude/skills/delegate/scripts/orca-worker.sh`, wait command, and `.claude/skills/drive/SKILL.md`, step 2.
What happened: `orca-worker.sh wait` returned `idle` with exit 0 while the review lane worker was actively mid-turn, because `working()` looked only for an `esc interrupt` hint that vanished between tool calls. Under `drive` step 2, an `idle` verdict with an empty child tree triggers plan parking, risking parking a plan whose worker is seconds from committing. The terminal's `status:` line was the only reliable signal.
Suggested change: Strengthen `orca-worker.sh wait` to inspect terminal status and prompt readiness before reporting `idle`, rather than relying solely on the absence of the interrupt prompt string.

## 2026-09-25 delegate: review worker silently swapped from Opus 5 to Opus 4.8
Skill or agent: `.claude/skills/delegate/scripts/orca-worker.sh` and `.claude/skills/delegate/SKILL.md`.
What happened: The review lane was launched with `--model claude-opus-5` and initially reported Opus 5 at startup, but the final screen and git commit attribution reported `Claude Opus 4.8`. For `sensitive` units where `delegate` warns a refusal reads as a model swap, an unannounced model swap under the hood can change reasoning capability or safety behavior without coordinator visibility.
Suggested change: Check and record the resolved model version in `orca-worker.sh` or `runlog`, and emit a warning when the reported model drifts from the requested ID.
