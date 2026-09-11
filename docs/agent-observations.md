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

## 2026-09-11 verify-change: a full run hangs forever when the Docker daemon does not answer
Skill or agent: `.claude/skills/verify-change/scripts/verify-change.sh`, the
`tools/test/service-otel-integration.sh` gate it runs at line 580; the
script's guard at `tools/test/service-otel-integration.sh:11` is a bare
`docker info`.
What happened: on the phase 3 and the phase 4 closes of the netsim plan, the
`--full` run the dirty marker demands reached this gate and blocked on
`docker info` with no timeout, since the daemon on the host had stopped
answering; the coordinator had to kill the probe by hand to get a verdict,
and both times the verdict was a failure the marker cannot be cleared by.
The step was followed as written.
Suggested change: bound the probe (`timeout 20 docker info`, or the
equivalent in bash since macOS lacks `timeout`) so the gate fails fast with
its own message, and let `verify-change` say which gate failed in its last
line rather than only `FAILED (exit 1)`.

## 2026-09-11 delegate: the Herdr wrapper undoes every Codex lane on the update banner
Skill or agent: `.claude/skills/delegate/scripts/herdr-worker.sh`, the Codex
startup-dialog loop at lines 93-106.
What happened: with codex-cli 0.153.4 and 0.154.0 available, the update offer
is answered with `2` and `enter` as written, but Codex then keeps an "Update
available!" banner box on the screen; the final `grep -q 'Update available'`
matches the banner and the start is undone with "still shows a startup dialog
after four rounds", so no Codex lane can start. Starting the agent by hand and
answering with `3` (skip until next version) worked, and the lane then took a
prompt. The step was followed as written.
Suggested change: match the dialog's prompt line ("Press enter to continue" or
the numbered options) rather than the words "Update available", and answer
with `3` so the offer stops recurring.
