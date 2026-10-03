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

## 2026-10-02 hooks: proto source guard allows every dotfile
Skill or agent: `tools/hooks/pre-tool-policy.sh`, the `spec/proto/` branch,
and `test/conformance/proto/layout_test.go`, `protoPathViolation`.
What happened: both checks explicitly allow every dotfile, including a hidden
script such as `.audit.sh`. The tests pin that allowance, while `AGENTS.md`,
Hard boundaries, permits only `.proto` files and package-boundary `README.md`
files. The enforced source-only rule therefore has an exception its authority
does not grant.
Suggested change: remove the dotfile allowance in both checks and pin the
rejection in the hook suite and conformance cases. The change is staged in
`tools/hooks/pre-tool-policy.sh`, `tools/hooks/tests/run.sh`, and
`test/conformance/proto/layout_test.go` for guardrail review. It remains pending
until that review accepts it.

## 2026-10-03 review: fix loop lacks a bound once authorized past the three-round cap
Skill or agent: `.claude/skills/review/references/fix-loop.md`, "When to stop".
What happened: the review loop ran six rounds on `DeviceIndex`, three rounds past the three-round cap, because user authorization was requested and granted without a defined limit on subsequent rounds. The step was followed as written, but the skill states only that a fourth round runs on user authorization and provides no rule for subsequent iterations on the same mechanism.
Suggested change: bound any user-authorized extension to one additional round (round four). If the mechanism remains deficient after round four, require taking the design back to `plan` rather than allowing unbounded patch iterations.

## 2026-10-03 delegate: lane wait returns idle while worker waits for pool quota reset
Skill or agent: `.claude/skills/delegate/scripts/orca-worker.sh`, `wait` command, and `.claude/skills/delegate/references/orca.md`.
What happened: a coordinator waiting on an Orca worker received an `idle` outcome while the worker was paused waiting for a pool quota reset window. Because the worker screen was unchanged and showed no active interrupt hints during the wait, the two screen reads five seconds apart matched, causing `wait` to report `idle` before work had completed.
Suggested change: check for pool wait and rate-limit indicators on the terminal screen before classifying an unchanged screen as `idle`, or require an explicit prompt or exit marker before returning `idle`.

## 2026-10-03 review: worker verdict report printed to terminal scrolls off screen
Skill or agent: `.claude/skills/review/SKILL.md`, step 5, and `.claude/skills/drive/references/review-stage.md`.
What happened: a review worker emitted its detailed verdict report directly to terminal stdout instead of writing to a scratchpad file. The text exceeded the terminal buffer capacity and scrolled off the screen, requiring the report to be re-requested to a file.
Suggested change: specify in `review-stage.md` and `review/SKILL.md` step 5 that review stage workers must write their full report to a designated scratchpad file and output only the verdict summary and file path to the terminal.
