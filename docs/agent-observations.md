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

## 2026-09-15 verify-change: a concurrent linter turns the verifier red for no reason
Skill or agent: `.claude/skills/verify-change/SKILL.md`, and the
`golangci-lint run` gate in `scripts/verify-change.sh`.
What happened: `golangci-lint` takes a machine-global lock. Two `--full`
runs died with `Error: parallel golangci-lint is running` and
`FlowSeer verification FAILED (exit 3) in gate: golangci-lint run`, once
because the session ran `golangci-lint` by hand to check a review finding
while the verifier was in flight, once because a targeted
`verify-change.sh -- <path>` overlapped the full run. Every gate before the
linter had passed both times. Nothing in the skill says the gate is
exclusive, so the failure reads as a real one, and a session that believes
it will go looking for a defect that is not there. Roughly forty minutes of
full-suite runtime was spent twice over. The step was followed as written.
Suggested change: say in the skill that the linter gate is mutually
exclusive machine-wide, that only one verification may run at a time and no
separate `golangci-lint` may run alongside it, and that
`parallel golangci-lint is running` means contention rather than a finding
and the run should be repeated. A clearer message from the script itself
when it sees that string would be better than prose.

## 2026-09-15 review: nothing describes the fix-and-re-review loop the skill's own rule assumes
Skill or agent: `.claude/skills/review/SKILL.md`, step 4 and the Report
section.
What happened: the user asked to fix findings in subagents and re-review in
a loop until the work was clean. `review` ends at a report and says
"Report only"; `implement` covers a plan's units, not a review's findings.
So the loop was run ad hoc: seven fix rounds and five review rounds, with
the coordinator choosing each round's scope, dispatching worktree workers,
merging, and deciding when to stop. The skill already carries the rule that
matters most for such a loop, in step 4: when a round finds a defect in the
previous round's fix for the same mechanism, stop patching and make the
property executable. That rule fired twice here and was right both times,
but nothing says whose job it is to notice across rounds, and a session
that reads `review` alone would not know the loop is a shape the repository
expects. The step was followed as written; the gap is that no step covers
the rounds after the first.
Suggested change: a short section in `review` for the iterate case, naming
who runs it, that fixes are dispatched as `delegate` describes rather than
made in the coordinating session, that the verifier runs on the union after
each merge and before the next review round, and that the stop condition is
a round with no correctness findings. Point step 4's same-mechanism rule at
it so the two read as one procedure.

## 2026-09-16 plan: a unit's Tests line prescribed the test that could not catch the risk the plan named
Skill or agent: `.claude/skills/plan/SKILL.md`, the per-unit `Tests:` line.
What happened: phase 3d's Open questions said the MST BPDU octet layout "is
verifiable from no in-repo file" and that "the tests prove only the round-trip
and the body lengths, and no peer capture is available here"
(`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-phase3d-plan.md`).
Unit U2's Tests line then asked for "the R14a-wire round-trip byte for byte",
which is symmetric and passes whatever placement the encoder chooses.
`implement` followed it, and two fields shipped in each other's octets until
review found it (fixed in `02ec81b0`). The step was followed as written and
still produced the wrong result.
Suggested change: have `plan` check each unit's Tests line against that unit's
own risks, so a risk the plan states as unverifiable by the tests either gets a
test that pins it or an explicit note that nothing in the unit covers it.
