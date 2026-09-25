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

## 2026-09-25 implement: full-tree generated/go/yang lint is too heavy to run
Skill or agent: `.claude/skills/verify-change` (and any coordinator or worker brief that lints `generated/`).
What happened: While implementing the generated-code-style plan, a full-tree `golangci-lint` run over `generated/go/yang` (~1,200 packages, 208 MB) was still running after over an hour of CPU on this host. The user directed: never run golangci-lint over the full generated/go/yang tree; lint only the three sample packages the plan names (ruckus-icx/openconfigvlan, ruckus-icx/openconfigsystem, aruba-cx/openconfignetworkinstance), and pass the same limit to any briefed worker.
Suggested change: verify-change and delegate brief-writing rules should state that lint coverage of `generated/go/yang` is capped at named sample packages, never the whole tree.
