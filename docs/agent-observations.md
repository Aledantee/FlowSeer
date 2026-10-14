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

## 2026-10-10 drive: ending a Codex turn leaves completed stages idle
Skill or agent: `.agents/skills/drive/SKILL.md`, step 2, and `.agents/skills/delegate/SKILL.md`, Orca worker waits.
What happened: the coordinator ended its turn while terminal waits ran. The workers completed, but no coordinator turn resumed to read their reports or start the next stages. The user had to request continuation. The allowed turn boundary was followed as written.
Suggested change: require Codex to remain in its polling turn unless an installed continuation mechanism is verified to restart the coordinator. A running shell wait alone does not establish that mechanism.

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

## 2026-10-12 next: a phase that waits on another parent's phase reads as ready to re-plan
Skill or agent: `.claude/skills/next/SKILL.md`, step 1, and `plan record
after` (`tools/scripts/lib/plans.py:217`).
What happened: the step was followed and still produced the wrong result.
`next plan-queue` put Deployment, Phase 1 in `replan` because its state
holds `after: []`. Its parent plan says no phase is written ready until the
central high availability plan lands, and central still embeds NATS and
keeps its certificate under `state_dir`, so the phase cannot be planned yet.
Recording the dependency with `plan record after` failed: "after names
docs/plans/2026-10-03-1533-feat-central-high-availability-phase3-plan.md,
which is neither in the phases nor in the retired list of
docs/plans/2026-10-03-1533-feat-deployment-plan.md". Deployment, Phase 3
(waits on edge high availability) has the same shape.
Suggested change: let `after` name a phase or plan of another parent, and
have `next plan-queue` read an unfinished one as `waiting`. Failing that, a
state field such as `blocked_by: <plan>` that `next` reads the same way.

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

## 2026-10-13 land: phase lands passed a diff-aware verifier while a repo-wide guard test was red on main
Skill or agent: `.claude/skills/land/SKILL.md`, the verification step (line
203), and `.claude/skills/verify-change/SKILL.md`, the `--full` guidance
(line 27).
What happened: `TestNoRawSecretFields` (`src/common/internal/secretguard/scan_test.go`)
scans every package under `src/` but lives in `src/common`. A diff-aware
run over a change elsewhere does not select that package, so phase lands
passed while main failed the test from 2026-10-04 until c53403f2 moved the
test keys and token out of exported fields. Only a `--full` run caught it. The steps were
followed as written, and the second case applies: the result was still
wrong, so it wants enforcement, not louder prose.
Suggested change: have the verifier select the repo-wide scan tests
(secretguard, `test/conformance/`) whenever a diff adds or changes a Go
source file under `src/`, or have `land` run `--full` once on the merged
tree before it leaves main one fast-forward away.

## 2026-10-13 drive: a worker's `isolation: worktree` checkout is outside the sandbox write allowlist
Skill or agent: `.claude/skills/drive/SKILL.md`, the worker briefs for
`implement`, `review`, and `compound`.
What happened: under auto mode the sandbox writes only to the drive's own
worktree, so a worker in a fresh `isolation: worktree` checkout needs
`dangerouslyDisableSandbox` for every git, test, and verifier command. The
auto-mode classifier allowed the bypass for some workers and refused it
for one ("Safety Bypass Flag"). The briefs told the workers to bypass, as
the skill says, and the stage then stopped on the refusal. The steps were
followed as written.
Suggested change: have `drive` start each worker in a worktree the
sandbox already allows, or state in the brief which commands the bypass
covers and name the refusal as a stop condition the coordinator resolves
before dispatch rather than a worker blocker.

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
