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

## 2026-10-02 delegate: pool usage unread when Orca is not installed
Skill or agent: `.claude/skills/delegate/scripts/pool-usage.sh`, read by
`delegate` "Dispatch by quota" and `drive` step 1.
What happened: on a host without the `orca` binary, the `claude` and
`codex` rows came back `signed_in: null`, `windows: null`, `error: orca
not installed`, although both CLIs were installed. The script falls back
to each CLI's own token only when Orca runs and has no account for the
pool (`pool-usage.sh:6`), not when Orca is absent. The step was followed
as written, so the drive dispatched on unknown headroom at one slot.
Suggested change: when `orca` is missing or unreachable, read the
`claude` and `codex` windows through the native CLI fallback the script
already has, and state in `references/pool-rows.md` that `orca not
installed` triggers it. How each CLI exposes its windows is unverified
and is the first thing to check.
