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

## 2026-09-25 delegate: same-model and sensitive-role rules are stricter than the user wants
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Pick the role, then resolve the lane" (pinning paragraph and the `execute-sensitive` row), and `.claude/models/registry.yaml` (`roles.execute-sensitive`, `sensitive_paths`).
What happened: user correction during a drive. When synthetic and codex reached 85%, the `execute-sensitive` fit set (gpt-5.6-sol, kimi-k3) left no usable pool and the phase would have stalled. The user ruled that claude, and Opus 5.5 specifically, is fine for this work. Two rules were followed as written and gave the wrong result: "never the coordinating session's own model" and the `exclude` list on `execute-sensitive`, which avoids models by assumed refusal risk.
Suggested change: in `delegate`, drop the rule against the coordinator's own model; keep only that review roles (`review-unit`, `review-seam`) run on a model other than the one that wrote the change. Tie `execute-sensitive` to measured refusals instead of path-based avoidance: a lane that a model declines or silently downgrades on a security classifier gets recorded (by `tune` or the run log) against that model and path, and only models with a recorded refusal are excluded. Until a refusal is measured, route sensitive paths like `execute`.

## 2026-09-25 drive: orca-worker.sh lacks the --role, --plan, --unit flags and the grade step drive calls
Skill or agent: `.claude/skills/drive/SKILL.md`, step 2 (start with `--role`/`--plan`/`--unit`, `<base>` from the run log) and after-stage step 5 (`orca-worker.sh grade`); `.claude/skills/delegate/scripts/orca-worker.sh`.
What happened: commit `ea8e65d5` added run logging, `--role`, and `grade` to `orca-worker.sh` and `delegate`. A later merge dropped them from the script and from `delegate`'s text, and `drive` still names them. The drive ran lanes without those flags and recorded `<base>` by hand. The step was followed as far as the script allowed.
Suggested change: restore `ea8e65d5`'s script and `delegate` changes (with `test_orca_worker.py`), or remove the run-log, role, and grade references from `drive`; either way add a test that `drive`'s documented `orca-worker.sh` invocations parse.
