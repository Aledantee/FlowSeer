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

## 2026-10-03 drive: parking a reviewed phase cannot stop its lane
Skill or agent: `.claude/skills/drive/references/parking.md`, Park, and
`.claude/skills/delegate/scripts/orca-worker.sh`, `stop`.
What happened: the parking reference keeps a parked lane's commits on
`parked/<slug>` and then runs `stop`. `stop` refuses any lane whose branch is
not an ancestor of `HEAD`, so it refused `sim-p2-review` with its commits
already safe on `parked/sim-p2-review`. The coordinator repeated the rest of
`stop` by hand: terminal close, `runlog.py end`, `orca worktree rm`, and the
state file.
Suggested change: let `stop` accept a lane whose branch tip is an ancestor of
some `parked/*` branch, or give it a `--parked <branch>` flag that checks that.

## 2026-10-03 delegate: keys types without Enter
Skill or agent: `.claude/skills/delegate/SKILL.md`, Orca worker, `keys`.
What happened: the skill lists `keys` for a dialog answer. A resume message
sent with `keys` to an `agy` worker stopped on a network error sat unsent in
its input box until an `orca terminal send --text '' --enter` followed. The
script header says "no Enter", and the skill does not.
Suggested change: state in the skill that `keys` presses no Enter, and name
`tell` for a message the worker must act on.
