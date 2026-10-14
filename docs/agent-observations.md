# Agent observations

Entries here record where a FlowSeer skill, agent definition, or hook did not
fit the work: a correction the user had to make to the procedure, a step that
was skipped or done by hand, a brief that missed something, a sequence that
recurs with no skill behind it. The `compound` skill's Observe mode appends
them. Nothing reads this file on its own; the `steer` skill works the queue
when a maintainer asks for it.

`steer` applies or rejects each entry through the change process in
[`agent-steering.md`](agent-steering.md) and deletes it. An edit to a policy surface `AGENTS.md`, Hard boundaries, names stops at a
staged diff for a person's review. An entry that sits here is not a
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

## 2026-10-14 verify-change: the full gate is too slow
Skill or agent: `tools/scripts/verify/change.py` (`--full`), and
`.claude/skills/land/SKILL.md`, step 5.
What happened: the user asked for this to be recorded: the gate is far too
slow and needs optimizing. Landing one phase that changed `go.mod` took
three `--full` runs of about 40 minutes each on 2026-10-13 and 2026-10-14.
The first failed on an `edgebus` NATS timeout
(`TestReceiverToForwarderCarriesBodiesUnchanged`), the second on a Docker
API timeout in `tools/test/service-otel-integration.sh`
(`TestManagedTelemetryConcurrentServicesStayIsolated`), and each failed
test passed when run alone. The race tests of one run summed to 1054
seconds over 251 packages, the five slowest being
`src/protocol/smi/differential` (166 s),
`src/services/device/test/integration` (159 s),
`src/common/service/test/integration` (115 s), `src/modules/edgebus`
(85 s), and `src/services/device/internal/captureapi` (76 s). When two
verifier runs overlap on one machine, the NATS-backed suites (`edgebus`,
`intake`, `host`, `syslogsource`) time out at 10 to 30 seconds and pass
alone with `-p 1`.
Suggested change: the verifier prints a gate line with the time into the
run and a table of gate durations (`tools/scripts/verify/change.py`,
`report_timings`), so the next `--full` run says where the 40 minutes go.
The optimization is a design choice. The options are to run independent
gates (web, Go modules, protobuf) in parallel, to move the slowest
integration and differential suites behind a tag that only a merge gate
sets, to let a `go.mod` change confined to `tools/` skip the full race
run, to rerun a failed package alone with `-p 1` once before declaring
the gate red and say so in the output, and to hold a machine-wide lock so
two race runs never overlap.
Question for the user: which of these does the next `--full` run's
duration table decide, and which are wanted regardless? Recommended:
read one duration table first, then choose, since the gates' own time
and the overlap are different problems.

## 2026-10-14 verify-change: one argument holding several space-separated paths passes without clearing their marker lines
Skill or agent: `tools/scripts/verify/change.py`, `collect_paths` and
`clear_marker`.
What happened: a run started as `verify change -- $paths` from zsh, which
does not split an unquoted variable, passed one argument holding twenty
space-separated paths. The run selected the hook tooling gates, ran the
script suites, printed `FlowSeer verification passed.`, and left every
one of those paths under `Unverified edits remain after this run:`. The
same paths named as separate arguments cleared. The cause inside the
verifier is unverified (the gate line printed the paths as separate words).
Suggested change: refuse an explicit path argument that holds whitespace
and names no file, naming the argument, so a mis-split list exits non-zero
before any gate as a selection that names nothing does.
