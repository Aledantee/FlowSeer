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

## 2026-10-12 implement: the per-unit verifier run checks whitespace against the unit's own commit
Skill or agent: `.claude/skills/implement/SKILL.md`, step 2.5, and
`references/workers.md` (the wave's verifier run after the merge).
What happened: the step was followed as written. Each unit committed, then
ran `verify-change.sh -- <paths>`, whose base defaults to `HEAD`
(`verify-change.sh:44`), so its `git diff --check "$base"` gate saw an
empty diff. Three committed patch files with "space before tab in indent"
and a trailing blank line passed every per-unit and per-wave run, and only
the Finish run with `--base main` failed, two rounds before it passed.
Suggested change: have step 2.5 and the wave run pass `--base <the unit's
ledger base>` (the `base` the ledger records at `in_progress`), or have the
verifier's whitespace gate diff from the merge base with `main` when no
base is given.

## 2026-10-13 review: a drive without Orca spends the review stage's one worker on the reviewer
Skill or agent: `.claude/skills/review/SKILL.md`, step 6, and
`.claude/skills/drive/SKILL.md` (the review stage brief).
What happened: the review stage ran with a budget of one sub-worker and
spent it on the independent reviewer. The review skill does not let the
coordinator make fixes itself, so the fix loop did not run until the drive
coordinator re-sent the stage. The steps were followed as written.
Suggested change: have `drive` give the review stage a budget of two
workers when Orca is unreachable (one reviewer, one fixer), or say in
`review` step 6 that a fixer is not counted against a reviewer-only budget.

## 2026-10-13 verify-change: a full run takes about 40 minutes and blocks every land that touches go.mod
Skill or agent: `.claude/skills/verify-change/scripts/verify-change.sh`
(`--full`), and `.claude/skills/land/SKILL.md`, step 5.
What happened: the step was followed as written. A change to any `go.mod`
leaves the `<Bash mutation; verify with --full>` marker, and `land` then
needs a `--full` run. One such run on this machine took 40 minutes from
start to receipt. Its race tests summed to 1054 seconds over 251
packages, and the five slowest were:
`src/protocol/smi/differential` (166 s),
`src/services/device/test/integration` (159 s),
`src/common/service/test/integration` (115 s), `src/modules/edgebus`
(85 s), and `src/services/device/internal/captureapi` (76 s). When two
verifier runs overlap on one machine, the NATS-backed suites
(`edgebus`, `intake`, `host`, `syslogsource`) time out at 10 to 30
seconds and pass when run alone with `-p 1`, so a red run then costs a
second full run.
Suggested change: profile where the 40 minutes go (gates run one after
another, and the web gates run before the Go gates), then pick the
cheapest of: run independent gates in parallel, move the slowest
integration and differential suites behind a tag that only `--full` on
the merge gate sets, and let a `go.mod` change whose module graph does not
change outside `tools/` skip the full race run. Separately, raise the
NATS suites' wait deadlines or hold a machine-wide lock for the race run
so that two runs never overlap.
