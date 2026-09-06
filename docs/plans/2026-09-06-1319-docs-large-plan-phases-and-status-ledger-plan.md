---
title: Large Plans in Phases with a Status Ledger - Plan
type: docs
date: 2026-09-06
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: mixed
---

# Large Plans in Phases with a Status Ledger - Plan

> Implemented. The ledger check is `.claude/skills/verify-change/scripts/check-plan-status.py`; the phase method is `.claude/skills/plan/references/phases.md`.

## Goal

A plan that outlives one session is implemented by the next session without
re-deriving what landed, what was decided along the way, and what comes
next. The means: `plan` splits large work into phase plans along dependency
clusters, and `implement` keeps a JSON status ledger in the worktree's git
directory that it writes after every unit and reads first on resume, with
the verifier validating the ledger's shape and `close` gating the merge on
it. Stop condition: a session that resumes from the ledger still has to
read the git log to know where it stands, which means the ledger carries
the wrong content and the design needs a different artifact.

## Decisions

- The ledger is JSON, not a Markdown table in the plan. Why: Anthropic's
  long-running-harness article chose a JSON feature list with a `passes`
  field because the model overwrites and rewords JSON less readily than
  Markdown, and `close` can gate on a field it parses instead of prose it
  interprets.
- The ledger lives at `$(git rev-parse --git-dir)/flowseer-plan-status.json`,
  beside the verifier receipt, and is never committed. Why: it is
  worktree state, not a decision record. The plan's `status` field and
  outcome note stay the durable record, written from the ledger when the
  work lands. Nothing goes stale on master, and `close` already reads that
  directory.
- `implement` leaves the ledger in place at Finish; `close` removes it
  after the merge lands. Why: the ledger is then independent evidence
  against a wrong `status: implemented`, which is the only case where the
  `close` gate adds a signal the plan's `status` does not already carry.
- One ledger per worktree, naming its plan. Why: one plan at a time.
- The ledger schema is fixed and small: contract, plan path, a `resume`
  list, and per unit `id`, `status`, `commit`, `verified_at`, and a
  one-line `note`. Why: the Slipstream paper shows coherent-looking
  summaries still break later steps, and community reports say stale long
  notes mislead more than short ones. The note carries only a decision or
  pitfall the next session must not rediscover.
- A unit is committed before its verifier run, and the ledger records
  `HEAD` and the receipt's `verified_at` after that run. Why: the ledger
  then points at a commit the tree can confirm, and `verified_at` newer
  than the commit is the same check `close` makes on the receipt.
- `plan` splits a plan along dependency clusters, not by count alone. Why:
  cohesion-aware task partitioning gained 11 to 14 points over naive
  splitting on DevEval and CodeProjectEval, and naive splitting scored
  below sequential execution. The count threshold only triggers the check.
- Phases after the first are drafted lightly and re-planned at their turn.
  Why: ADaPT shows decomposing where execution fails beats fixed up-front
  decomposition, and by the time a later phase starts the tree has moved.
- A parent plan stays `planned` until its last phase lands. Why:
  `partially-implemented` means some units landed, and a parent with no
  code behind it would otherwise carry that label from the day it is
  written. Phase progress is recorded in the parent's Units.
- For a phase plan, `implement` runs each unit in a fresh worker context
  by default, sequentially, with the coordinator holding the ledger. Why:
  Context-Folding and "Context as a Tool" show milestone-triggered,
  structured context reset beats append-only context and summary-only
  management, and the `delegate` machinery for this already exists.
  Sequential because Cognition's case against multi-agents and the CAID
  versus STORM disagreement both concern parallel workers on one
  deliverable, which this plan does not need.
- Phase-splitting detail goes to `.claude/skills/plan/references/phases.md`.
  Why: it applies to a minority of plans, and skill bodies stay near 150
  lines with episodic material behind a pointer.
- No STATE.md, SUMMARY.md, or per-wave directory. Why: the plan is already
  the artifact, the steering record keeps one artifact format each, and
  the strongest community counter-signal is ceremony fatigue.

## Requirements

1. A plan with more than six units, or over 300 lines, is checked for
   dependency clusters and split into a parent plan plus one phase plan
   per cluster when there is more than one. Acceptance: a plan with nine
   units, four in `src/protocol/smi` and five in `src/protocol/snmp` whose
   `After` lines depend on the first four, becomes a parent plan with two
   phase units and two phase plans; a nine-unit plan whose units all touch
   one package stays one plan and says why under Decisions.
2. After each unit lands, the ledger names that unit `passed` with its
   commit and a `verified_at` newer than the commit, and `resume` names
   the next unit. Acceptance: after the second of four units, the ledger
   holds two `passed` entries with commits, two `pending`, and
   `resume: ["U3"]`.
3. A session that starts `implement` on a plan whose ledger exists reads
   the ledger before any edit. A `passed` commit that is not an ancestor
   of `HEAD` is reported, checked against the tree, and recorded under
   the plan's Open questions; the session asks before rewinding a unit.
   Acceptance: a ledger claiming U1 passed at commit `abc` in a worktree
   where `abc` is unreachable produces an Open questions entry naming U1
   and `abc`, and a question to the user, before any edit.
4. The verifier fails when the ledger is present and malformed.
   Acceptance: a ledger whose `units[0].status` is `"done"` fails with a
   message naming the field and the allowed values; a well-formed ledger,
   or none, passes.
5. `close` stops when the ledger names any unit that is not `passed`, and
   reports the ledger against the plan's `status`. Acceptance: a plan with
   `status: implemented` and a ledger with one `blocked` unit stops the
   merge and names `implement` as the next skill.
6. `implement` refuses a plan it cannot execute. Acceptance: a phase plan
   with `artifact_readiness: needs-decisions`, or a parent plan whose
   Units name other plan files, stops before any edit and names `plan` as
   the next skill.
7. A worker brief for a unit carries the `note` of every landed unit and
   nothing else from the ledger. Acceptance: the brief for U3 contains the
   notes of U1 and U2 and no `commit` or `verified_at` values.

## Out of scope

- Parallel execution beyond what `implement` already allows.
- A "Compact Instructions" section in `AGENTS.md`: a policy surface.
- A hook denying hand edits to the ledger; the verifier check is enough.
- Migrating landed plans; none is in progress.

## Units

### U1. Ledger contract in the verify-change skill

Files: `.claude/skills/verify-change/SKILL.md`,
`.claude/skills/verify-change/scripts/check-plan-status.py`,
`.claude/skills/verify-change/scripts/verify-change.sh`,
`tools/hooks/tests/run.sh`
After: none
Change: `SKILL.md` documents the ledger path and this schema, next to the
receipt paragraph:

```json
{
  "contract": "flowseer-plan-status/v1",
  "plan": "docs/plans/2026-09-06-1319-docs-example-plan.md",
  "resume": ["U2"],
  "units": [
    {"id": "U1", "status": "passed", "commit": "d037089c",
     "verified_at": "2026-09-06T12:10:00Z",
     "note": "Diagnostics keep the source span; the catalog is generated."},
    {"id": "U2", "status": "in_progress", "commit": null,
     "verified_at": null, "note": null}
  ]
}
```

`status` is one of `pending`, `in_progress`, `passed`, `blocked`.
`resume` lists the `in_progress` units, or the next `pending` unit when
none is in progress, and is empty when every unit is `passed`. `units`
holds at least one entry. The script takes the ledger path as its one
argument, defaulting to `flowseer-plan-status.json` under
`git rev-parse --git-dir`, exits 0 when the file is absent, and otherwise
exits 1 naming the first field that is missing, of the wrong type, or
outside the allowed values, and also when the named plan does not exist
relative to the current directory. `verify-change.sh` runs the script
right after `cd "$root"`, before any gate and before the dirty-marker
block, so a one-character JSON error is reported before the Go gates run
and a malformed ledger never clears a marker. `--print-selection` and the
no-changed-paths exits stay ahead of it.
Tests: `tools/hooks/tests/run.sh` runs the script directly, with an
explicit path under a `mktemp -d` fixture holding a git repository and a
plan file, for an absent ledger, a valid ledger, a bad `status`, and a
`plan` that does not exist. The verifier is not run inside the fixture.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/verify-change/SKILL.md .claude/skills/verify-change/scripts/check-plan-status.py .claude/skills/verify-change/scripts/verify-change.sh tools/hooks/tests/run.sh`

### U2. Ledger writes and resume in implement

Files: `.claude/skills/implement/SKILL.md`
After: U1
Change: Orient refuses a plan whose `artifact_readiness` is
`needs-decisions` or whose Units name other plan files, and names `plan`
as the next skill. It then gains a resume step before the first edit:
when the ledger exists and names this plan, read it and check each
`passed` commit with `git merge-base --is-ancestor <commit> HEAD`, where
any non-zero exit, including the exit for an unknown object, means not
an ancestor. Report each such unit, check its files against the tree,
record the mismatch under Open questions, and ask the user before
rewinding a unit; then continue from `resume`. A ledger naming
another plan is replaced only after the user confirms. When there is no ledger, write one with every unit
`pending` before the first edit. Step 2 sets the unit `in_progress` when
work on it starts, and after the unit's tests pass commits the unit, runs
the verifier on its paths, and writes `passed` with `git rev-parse HEAD`
and the receipt's `verified_at`; `resume` moves to the next unit, and
`note` is filled only when the unit produced a decision or pitfall the
next unit needs, in one line. A unit that cannot land is `blocked` with
the reason in `note`. With concurrent workers, `resume` is written after
the wave settles. Finish reads the ledger to write the plan's outcome
note and leaves the ledger in place for `close`. The schema is cited
from `verify-change`'s `SKILL.md`, not restated.
Tests: none; every embedded command runs once from a fresh shell instead.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/implement/SKILL.md`

### U3. Ledger gate and phase plans in close

Files: `.claude/skills/close/SKILL.md`
After: U1
Change: the checkpoint table gains a row: signal "Every unit landed",
where "the ledger, when present", required value "every `status` is
`passed`". A ledger with any other status is a stop that names
`implement`, even when the plan says `implemented`; the report names the
mismatch. An absent ledger is not a signal, since planless work and work
that landed before this plan have none. Step 1's plan discovery gains a
rule: when the changed set holds a phase plan and its parent, the phase
plan is this work's plan and the parent is reported, not gated on. After
the merge lands, `close` removes the ledger and says so.
Tests: none, same reason as U2.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/close/SKILL.md`

### U4. Phase split and phase plans in plan

Files: `.claude/skills/plan/SKILL.md`, `.claude/skills/plan/references/phases.md`
After: none
Change: the 300-line rule in step 3 becomes a pointer: over six units or
300 lines, load `references/phases.md`. That file gives the method:
cluster the units by the files they touch and the `After` edges between
them; for existing Go packages confirm the clusters with `go list -deps`,
for schema with the `buf` module graph. More than one cluster produces a
parent plan whose Units are the phases in dependency order, each with
`Files:` naming the phase plan path and a `Landed:` line filled when the
phase lands, and one phase plan per cluster at
`docs/plans/<date>-<type>-<slug>-phase<N>-plan.md` with a `parent:`
frontmatter field naming the parent. Only the first phase is written
implementation-ready; later phases carry Goal, Decisions, and
Requirements, `artifact_readiness: needs-decisions`, and a line saying
`plan` re-plans them when their turn comes. A single cluster stays one
plan and the plan says so under Decisions. The parent's `status` is
`planned` until the last phase lands, then `implemented`. Step 4
dispatches the reviewer against the parent and the first phase together.
Tests: none, same reason as U2.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/plan/SKILL.md .claude/skills/plan/references/phases.md`

### U5. Fresh-context units and ledger notes in delegate

Files: `.claude/skills/delegate/SKILL.md`, `.claude/skills/implement/SKILL.md`
After: U2
Change: `implement` says a phase plan's units run one at a time in a
worker, dispatched as `delegate` describes, with the coordinator merging,
verifying, and writing the ledger after each; the user may ask for the
main conversation instead. `delegate`'s brief section gains an item
before the boundaries item: for a unit of a plan with a ledger, the
`note` lines of every landed unit, verbatim, and nothing else from the
ledger. The full-handoff paragraph says the handoff brief carries the
whole ledger, since the child worktree has its own git directory.
Tests: none, same reason as U2.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/delegate/SKILL.md .claude/skills/implement/SKILL.md`

### U6. Rationale and sources in the steering record

Files: `docs/agent-steering.md`, `docs/README.md`
After: U1, U4
Change: the Project skills section gains a paragraph on phase plans and
the ledger with the reasons in Decisions above, and the research list
gains the sources this plan relied on: Anthropic's "Effective harnesses
for long-running agents" and memory tool docs, OpenAI's "Run long horizon
tasks with Codex", "Context as a Tool" (arXiv 2512.22087),
"Scaling Long-Horizon LLM Agent via Context-Folding" (arXiv 2510.11967),
Slipstream (arXiv 2605.08580), ADaPT (arXiv 2311.05772), "When Parallelism
Pays Off: Cohesion-Aware Task Partitioning" (arXiv 2606.00953, the source
of the 11 to 14 point figure), SWE-Bench Pro (arXiv 2509.16941), CAID
(arXiv 2603.21489), STORM (arXiv 2605.20563), Cognition's "Don't Build
Multi-Agents", Manus's context-engineering post, and the Claude Code
issue on compaction losing tracker files (anthropics/claude-code#29890).
`docs/README.md` gains the ledger path and the `parent:` field where it
documents the plan format.
Tests: each URL is fetched and its claim confirmed before the citation is
written; the verifier's link check resolves relative paths only and does
not check external links.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/agent-steering.md docs/README.md`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh --base master
tools/hooks/tests/run.sh
```

`--base master` selects every file the branch changed, the plan file with
its outcome note included, so the run clears each dirty marker. Once U2
lands, run the remaining units through the new procedure and confirm the
ledger matches the schema after each.

## Definition of done

- Verifier green for every changed path.
- `docs/README.md` and `docs/agent-steering.md` updated in the same change.
- `plan` and `implement` bodies stay near 150 lines.
- This plan's `status` set with an outcome note under its title.
- No plan labels in code or in the ledger script's messages beyond the
  unit `id` values the ledger itself carries.

## Open questions

- Six units as the split trigger comes from community reports of three to
  five phases per plan; no controlled study varies wave size. Tune it when
  a six-unit plan proves too large for one session or phases come out tiny.
- Whether the ledger should record the receipt's `paths` per unit.
  Recommended: not yet; the final receipt covers the union.
