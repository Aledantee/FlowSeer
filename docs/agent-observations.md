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

## 2026-10-02 delegate: pool rows carry no account plan, so a percent window reads as equal headroom on every plan
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Pick the role, then
resolve the lane" step 1, Wave size, and Dispatch by quota, with
`.claude/skills/delegate/scripts/pool-usage.sh` and
`.claude/skills/tune/scripts/discover-host.sh`, which write the pool rows.
What happened: the `codex` row read `signed_in: true` with its only window at
0%, so a review wave gave the pool two slots. The account was on the free
plan. Codex answered `400 The 'gpt-6.1-sol' model is not supported when using
Codex with a ChatGPT account`, then the same for `gpt-6-sol`, and four lanes
were started and stopped before the user said to avoid the pool. The steps
were followed as written. The user's correction: check which kind of account
each pool is signed in to, `codex` and `claude` included, because half of a
20x plan's window is far more capacity than 80% of a standard plan's.
Suggested change: `pool-usage.sh` and `discover-host.sh` record each pool's
account plan in its row. `delegate` step 1 drops a model the plan does not
serve. Wave size counts slots from the capacity left, the plan's multiplier
times the unused share of the window, instead of from the percent alone.
