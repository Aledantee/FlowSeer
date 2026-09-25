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

## 2026-09-25 drive: orca-worker.sh lacks options and commands named in drive skill
Skill or agent: `.claude/skills/drive/SKILL.md` (steps 1 and 4) and `.claude/skills/delegate/scripts/orca-worker.sh`.
What happened: `drive/SKILL.md` documents calling `orca-worker.sh start` with `--role`, `--plan`, and `--unit`, and calling `orca-worker.sh grade`, but `orca-worker.sh` does not implement these options or the `grade` subcommand.
Suggested change: Align `drive/SKILL.md` and `orca-worker.sh` by either adding `--role`, `--plan`, `--unit`, and `grade` handling to `orca-worker.sh` or removing those invocations from `drive/SKILL.md`.

## 2026-09-25 delegate: orca-worker.sh wait reports idle early for claude and agy lanes
Skill or agent: `.claude/skills/delegate/scripts/orca-worker.sh` (`wait` command).
What happened: `orca-worker.sh wait` reported `idle` prematurely for claude and agy workers while the agent was still processing its turn.
Suggested change: Tighten idle detection in `orca-worker.sh wait` for claude and agy lanes by verifying process quiescence or terminal output stability before returning `idle`.

## 2026-09-25 delegate: orca-worker.sh keys truncates inputs longer than ~250 characters
Skill or agent: `.claude/skills/delegate/scripts/orca-worker.sh` (`keys` command).
What happened: Text sent to a worker terminal with `orca-worker.sh keys` that exceeded ~250 characters was truncated, with only the tail of the message arriving in the terminal.
Suggested change: Update `orca-worker.sh keys` to chunk long input strings or inject text via temporary files or bracketed paste rather than raw keystroke bursts.

## 2026-09-25 drive: opencode lanes hang on model stream with Escape ignored
Skill or agent: `.claude/skills/delegate/scripts/orca-worker.sh` (opencode lane supervision).
What happened: Two opencode lanes hung on a streaming model response for over an hour, and sending `Escape` failed to interrupt the stream, blocking the lane until manual intervention.
Suggested change: Add a streaming watchdog timeout to opencode supervision in `orca-worker.sh` to terminate and recover hung worker processes when stream interrupts fail.
