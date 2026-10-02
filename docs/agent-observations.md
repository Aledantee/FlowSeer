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
