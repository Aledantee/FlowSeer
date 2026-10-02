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

## 2026-10-02 delegate: a long report from a report-only lane scrolls off the screen
Skill or agent: `.claude/skills/delegate/SKILL.md`, Orca worker, `read`, and
Write the brief, item 4.
What happened: a `review-unit` lane on `agy` returned six findings.
`orca-worker.sh read` printed `warning: older output is no longer retained`
and showed only findings 4 to 6. The first three were recovered by a
`tell` asking the worker to write the report to a file in the scratchpad
directory. The step was followed as written.
Suggested change: brief item 4 names a scratchpad file for the report of a
lane that returns one, and the coordinator reads that file, with `read`
kept for the status line.

## 2026-10-02 steer: a script in a new skill lands without its executable bit
Skill or agent: `.claude/skills/steer/SKILL.md`, step 3, new skill
candidate.
What happened: `diagnose/scripts/hitl-loop.sh` was created through the
editor tools and `chmod +x` failed with `Operation not permitted`, since
the sandbox denies writes under the skills directory. The script was
committed as mode 100644 while every other skill script is 100755. The
step does not mention the mode.
Suggested change: the step sets the mode in the index with
`git update-index --chmod=+x <path>`, which the sandbox allows.

## 2026-10-02 delegate: a merge on main dropped the unreadable-Orca change to pool-usage.sh
Skill or agent: `.claude/skills/delegate/scripts/pool-usage.sh` and
`test_pool_usage.py`, and `.claude/skills/land/SKILL.md`, step 3.
What happened: merge `cd07426d` on `main` took its second parent's version
of both files whole. The first parent's change from `642b3ad8` and
`0ff7b91e` is gone: `orca_unreadable()`, which reports the `claude` pool as
signed in from the CLI's own token when Orca is unreadable, and its tests.
`delegate/SKILL.md` and `references/pool-rows.md` still describe that
behavior. `merge-check.py ORIG_HEAD..HEAD` reports the loss on every later
`land`, since the range holds the merge commits `main` brought in, and
exits 1 for a merge the landing branch did not make.
Suggested change: restore the dropped change or state that `2b02d559`
replaces it and correct the two documents. In `land` step 3, tell a loss
in a merge already on `main` apart from one in the landing branch's own
merge, and stop only on the second.

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
