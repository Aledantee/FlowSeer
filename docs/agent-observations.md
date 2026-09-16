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

## 2026-09-16 hooks: the Edit hook cannot find the Go formatters off PATH
Skill or agent: `tools/hooks/go-format.sh` through `tools/hooks/common.sh`,
`hook_init`.
What happened: with gofumpt and goimports installed in `$(go env GOPATH)/bin`
but that directory absent from the session's PATH, the hook reports
`gofumpt, goimports is not on PATH; edited Go files were not formatted`
after every edit. The verifier half of this gap is fixed in
`verify-change.sh`; the hook half is a policy-surface change staged for
guardrail review (`hook_go_tools_on_path` in `common.sh` and its assertion
in `tools/hooks/tests/run.sh`). Delete this entry when that lands, or when
the review rejects it and a different remedy is chosen.
Suggested change: resolve the formatters from `go env GOBIN`, then the first
`GOPATH` entry's `bin`, before PATH decides.
