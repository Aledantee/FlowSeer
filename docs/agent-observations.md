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

## 2026-09-25 delegate: a claude lane silently fell back from its pinned model on a cyber refusal
Skill or agent: `.claude/skills/delegate/SKILL.md` (lane resolution, "One model per task from start to finish", and "Reading a worker's report") and `.claude/skills/delegate/scripts/orca-worker.sh` (`start`, `wait`).
What happened: The `style-review` lane was started with `--cli claude --model claude-opus-5`, reviewing a change that included `src/edge/netpen` attack tooling. At 18:56:13Z, eight minutes in, Claude Code logged a `model_refusal_fallback` event tagged `[cyber]` ("Opus 5's safeguards flagged this message … Switched to Opus 4.8"). The lane answered its remaining 174 turns as `claude-opus-4-8` (86 before that as `claude-opus-5`, per the model field in its transcript). Nothing surfaced the switch. `wait` and `status` report no model, the coordinator noticed only because the screen footer read "Opus 4.8", and the verdict was recorded as the pinned model's. The registry already rates `claude-opus-5` `refusal_cyber: medium`, but lane resolution does not read that field. The user ruled that this must not happen.
Safeguard: Claude Code applies it on its own, not the lane or the repository. When the model's safeguards flag a message, the client switches the session to a fallback model and writes a `system` event with `subtype: model_refusal_fallback` and a category tag (here `[cyber]`) to the session transcript. It logs no error, sends no exit code, and does not stop the turn.
Open investigation (user, 2026-09-25): whether Claude Code offers a hook event for this fallback (or for any model change within a session). With one, a repository hook could stop the lane, or mark its output unaccepted, as it happens, instead of a transcript scan at settle.
Suggested change: (1) Lane resolution drops models with `refusal_cyber` other than `low` for any unit whose paths hold offensive-security code (`src/edge/netpen/**`), and the registry lists those paths the way it lists `sensitive_paths`. (2) Launch claude lanes with the refusal fallback disabled, if Claude Code offers a setting for it, so a refusal stops the lane as a blocker instead of changing its model. (3) `orca-worker.sh` checks the lane's transcript at settle for `model_refusal_fallback` or a `model` other than the pinned one, and reports it. The coordinator then treats the lane's output as unaccepted until it is re-run or accepted by the user.

## 2026-09-26 delegate: lane routing has no rule for offensive-security paths or for Claude as a last resort
Skill or agent: `.claude/skills/delegate/SKILL.md` ("Pick the role, then resolve the lane"), `.claude/models/registry.yaml` (`sensitive_paths`, `refusal_cyber`).
What happened: Lane resolution picks by pool headroom and price and never reads `refusal_cyber`, so a Claude lane can be routed onto `src/edge/netpen` attack tooling. The cyber safeguard then silently swaps the model (see the entry on the model fallback). During the audit drive, with codex near its weekly limit, the user ruled on two points:
- `src/edge/netpen` is definitively off Claude. Its order is codex (`gpt-5.6-sol`) while it has quota, then google (`gemini-3.8-flash`), and Claude (`claude-opus-4-8`, pinned from the start, never Opus 5.x) only when really nothing else is available.
- For a review stage, Claude is an acceptable fallback when the fitting non-Claude pools are out of quota. That is a preference, not a ban.
The coordinator had first recorded the preference as a blanket rule, and a relayed message then briefly overstated it as "run netpen on Claude now". Neither rule existed in the skill, so each drive re-derived routing from chat messages.
Suggested change: Add an `offensive_paths` list to `registry.yaml` (`src/edge/netpen/**`, `src/common/internal/netpenguard/**`). In `delegate`, lane resolution for a unit that touches those paths drops Claude models unless no other fitting pool is signed in and under its limit, and it then pins `claude-opus-4-8`. Also state that Claude is an ordinary fallback for review roles, and that every Claude lane's transcript is checked for a model change before its work is accepted.
