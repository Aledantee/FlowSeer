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

## 2026-10-03 review: the round cap and the fourth round disagree
Skill or agent: `.claude/skills/review/references/fix-loop.md`, When to stop.
What happened: the cap reads "A review runs at most three rounds in total",
and the same bullet says "A fourth round runs only on that answer". A
fourth round that is not clean has no stated outcome. Under `drive` the
same answer starts a new review with a fresh count, while interactively it
is one more round. The step was followed as written and gives two readings.
Suggested change: make the user's answer start a new review, as "one more
gap pass" does, or state the outcome of a fourth round that is not clean.

## 2026-10-03 land, next: a `fixes needed` plan is sent to review step 6, not step 1
Skill or agent: `.claude/skills/land/SKILL.md`, the missing-checkpoint
table, and `.claude/skills/next/SKILL.md`, the `unchecked` row.
What happened: both send a plan whose verdict is `fixes needed` to
"`review`, step 6". `review` step 1 now reads the record of open items and
calls every run on a scope with a recorded verdict a new review. A session
entering at step 6 skips that read and has no stated round count.
Suggested change: both rows say to run `review` from step 1 with step 6.

## 2026-10-03 review: the record and the verdict are lost on two worker paths
Skill or agent: `.claude/skills/drive/references/parking.md` and
`.claude/skills/land/references/missing-checkpoint.md`.
What happened: the record of open review items travels only with the plan
file or the checkpoints file. A phase of a parent that `drive` parks
without merging the stage worker's branch loses it, and so does a planless
review run in a `land` worker's own git directory. The next review there
starts fresh. Both paths lose behavior findings the same way.
Suggested change: merge or copy the verdict commit and the checkpoints
lines back before the worker's checkout is released.

## 2026-10-03 delegate: a reviewer on a pool CLI ran the verifier and sat idle
Skill or agent: `.claude/skills/delegate/SKILL.md`, Write the brief, items
4 and 6.
What happened: a `review-unit` lane on `agy` started
`verify-change.sh --base main` as a background task before reviewing and
then waited on it. `orca-worker.sh wait` printed `idle` twice with no
report written, and the lane continued only after a `tell`. Item 6 keeps
the verifier from a unit worker and says nothing for a lane that returns a
report. The brief named no check the coordinator had already run.
Suggested change: item 6 says a report lane runs no verifier, and item 4's
"name the checks the coordinator already ran" is required for review lanes.
