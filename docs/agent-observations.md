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

## 2026-09-26 delegate: codex 0.157.1 hooks review ends on a hook detail screen
Skill or agent: `.claude/skills/delegate/scripts/orca-worker.sh`, start command (codex hooks review).
What happened: Two `start --cli codex` runs with codex 0.157.1 on 2026-09-26 failed with "pointer not on review-f46's screen after two submissions". The screen showed a single hook's detail view (`Trust Trusted`, `space/enter toggle · esc back`) for the Orca `codex-hook.sh` entry in `~/.codex/hooks.json`, so the `t`-then-escape answer left the TUI inside the review, and the pointer's Enter went to the toggle. The update check was off and did not appear. The step was followed as written; the lane ran as `codex exec` instead.
Suggested change: Match the detail view (`esc back`) and send escape until the review list and then the prompt show, or launch with the hooks review pre-answered if codex has a config key for it.
