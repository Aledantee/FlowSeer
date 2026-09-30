---
title: Harness Gates and Waiting Phase 1, Correctness Gates - Plan
type: fix
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-30-2000-fix-harness-gates-and-waiting-plan.md
---

# Harness Gates and Waiting Phase 1, Correctness Gates - Plan

## Goal

Before a coordinator merges a Claude lane, `orca-worker.sh check` proves
from the lane's session files that it ran on the model it was started
with. After every merge a workflow skill makes, `merge-check.py` fails on a
path whose merge dropped all of one side's change. A fix worker that needs
a file beyond what its brief allows stops with a blocker that names it.
The means are two scripts and the skill steps that call them.

**Stop condition:** the session-file directory for a lane cannot be found
from its path (Claude Code changes its naming), so `check` refuses every
Claude lane.

## Decisions

The parent's Decisions apply. Local to this phase:

- `check` runs before the merge, and `grade --outcome accepted|amended`
  runs it again as a backstop. Why: `delegate`, Reading a worker's report,
  merges before `grade`, so a check only in `grade` would refuse a lane
  whose commits are already on the branch.
- The check reads Claude Code's session files, not the screen. Why: each
  assistant record carries `message.model`, and a fallback writes a
  `system` record with `subtype: model_refusal_fallback`, `originalModel`,
  and `fallbackModel`. This shape is observed on this host and not
  documented, so U1 commits a trimmed real record pair as its fixture.
  `c3d034df` records that Opus 5, Opus 5.5, and Fable 5.1 move a flagged
  session to Opus 4.8 for the rest of the session, and `a58f9293` is a
  review verdict merged from a lane that had moved.
- The check stays although Claude lanes launch with
  `switchModelsOnFlag:false` since `c3d034df`. Why: the setting changes
  what the CLI does at its prompt, and only the session file proves which
  model did the work.
- The session directory is `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/`
  plus the lane path with every character outside `[a-zA-Z0-9]` replaced
  by `-`. A path whose name would exceed 200 characters is refused as
  unsupported. Why: Claude Code 2.1.285 names the directory with
  `replace(/[^a-zA-Z0-9]/g, "-")` and truncates with a hash suffix above
  200 characters, read from the installed bundle under
  `~/.local/share/claude/versions/2.1.285`. The hash is not reproduced
  here, and lane paths are far shorter.
- Only records at or after the run log `start` event's `at` count. Why:
  a lane path is reused by later lanes of the same slug, and its session
  directory keeps the files of every earlier lane at that path.
- A model matches when it equals the expected id or the expected id plus
  a `-YYYYMMDD` suffix. `<synthetic>` is ignored. Why: the registry names
  `claude-haiku-4-5` while its records read `claude-haiku-4-5-20251001`,
  and `<synthetic>` marks the CLI's own notices.
- Only top-level session files are checked for the model. Fallback
  records count in `subagents/` files too. Why: subagents run other models
  by design, but a fallback anywhere means work ran on a model nobody
  chose.
- `merge-check.py` compares lines, not whole blobs. For each merge in the
  range and each path either side changed since the merge base, a side's
  change is lost when the merge holds none of the lines it added and every
  line it removed. Blank lines and lines of two or fewer non-space
  characters are left out of both sets. Why: a blob comparison misses a
  one-sided reset, where the merge equals the base, and fails a correct
  merge whose other parent already holds the change. A line rule catches
  the first and passes the second.
- Paths a side deleted or renamed (`git diff -M --name-status`) are listed
  as not compared rather than judged. Why: a rename carries the other
  side's edit to a new path, which a per-path rule reads as a drop.
- The script takes a revision range and checks every merge commit in it
  (`git rev-list --merges`). Callers pass `ORIG_HEAD..HEAD` after `git
  merge`. Why: a worker's branch often fast-forwards, and the merges the
  worker made itself are then inside the range, not at its tip.
- "The files a fix may touch" means the brief's named files plus the
  classes `fix-loop.md` step 1 already allows. Why: step 1 lets a fix reach
  the owning layer, files it leaves stale, and other sites of the class,
  and a rule that ignored that would contradict it.

## Requirements

1. `check` fails for a Claude lane whose top-level session file, written
   after the lane's start, holds an assistant record from another model.
   Example: lane started with `claude-opus-5-5` at `2026-09-30T10:00:00Z`,
   a record at 10:05 from `claude-opus-4-8`: exit 1 naming both ids.
2. `check` fails when any session file of the lane, `subagents/`
   included, holds a `model_refusal_fallback` record after the start.
3. `check` passes records from before the start, a dated model suffix, and
   `<synthetic>`. Example: a 09:00 record from `claude-opus-4-8` and a
   10:05 record from `claude-haiku-4-5-20251001` for expected
   `claude-haiku-4-5`: exit 0.
4. `check` fails when no session file was written after the start, naming
   the directory it searched. Example: lane `p2_fix` searches
   `…-FlowSeer-p2-fix`.
5. `check` on a non-Claude lane prints that it did not check and exits 0.
6. `grade --outcome accepted` and `--outcome amended` refuse a lane that
   fails `check`. `blocked` and `rejected` are written regardless.
7. `merge-check.py` fails on a merge that keeps none of one side's change.
   Example: base `a b`, first parent `a b x1`, second parent `a b y1`,
   merge `a b y1`: path listed as dropping the first parent, exit 1.
   The same with an unchanged second parent and merge `a b` also fails.
8. It lists added lines a merge lacks when the side kept some of its
   change, and exits 0. Example: first parent adds `x1` and `x2`, merge
   keeps `x1`: `x2` listed, exit 0.
9. It passes a merge equal to a parent that already holds the other's
   change. Example: first parent adds `x1`, second adds `x1` and `y1`,
   merge equals the second: exit 0.
10. It lists a renamed path as not compared, and a range without merges
    prints `no merges` and exits 0.
11. `delegate`, `drive`, `land`, `implement`, and `fix-loop.md` run `check`
    before each lane merge and `merge-check.py ORIG_HEAD..HEAD` after each
    merge, and a failure stops that step.
12. `delegate` item 6 and `fix-loop.md` step 1 say a change beyond the
    files a fix may touch is a blocker naming the file and reason, and
    that a comment, skipped or weakened test, or partial change in its
    place is not a fix.

## Out of scope

- The coordinator's own session model. The user starts that session, and
  its fallback shows there.
- Semantic merge checks. The script compares lines only.
- `merge-check.py` reads commits in this repository written by the harness
  and the user. Their authors are trusted: it guards against mistakes, not
  a merge built to evade it.

## Units

### U1. Lane model check
Files: `.claude/skills/delegate/scripts/model_check.py`, `.claude/skills/delegate/scripts/test_model_check.py`, `.claude/skills/delegate/scripts/testdata/fallback-records.jsonl`, `.claude/skills/delegate/scripts/orca-worker.sh`, `.claude/skills/delegate/scripts/test_orca_worker.py`, `.claude/skills/delegate/references/orca.md`
After: none
Change: `model_check.py <lane path> <expected model> <since>` applies the
Decisions' directory, time, match, and scope rules. It prints one line per
violation and exits 1 on any violation or when no session file was
written after `since`. `orca-worker.sh check <slug>` reads `cli` from the
lane state and `model` and `at` from the run log `start` event, runs the
script for `claude` lanes, and prints `not checked: <cli>` otherwise.
`grade` calls the same function before writing an `accepted` or `amended`
event. The usage block and `orca.md` describe `check`, and `orca.md` says
a failed check is graded `rejected`, left unmerged, and dispatched again
by the model-switch rule. `testdata/fallback-records.jsonl` holds one
assistant record and one fallback record copied from a session file on
this host, with every field but `type`, `subtype`, `timestamp`,
`message.model`, `originalModel`, and `fallbackModel` removed.
Tests: `test_model_check.py` covers requirements 1 to 4 on session files
built from the testdata records in a temporary `CLAUDE_CONFIG_DIR`.
`test_orca_worker.py` covers requirement 5 and requirement 6, both refused
outcomes and both written ones.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/delegate/scripts .claude/skills/delegate/references/orca.md`

### U2. Merge check script
Files: `.claude/skills/land/scripts/merge-check.py`, `.claude/skills/land/scripts/test_merge_check.py`
After: none
Change: `merge-check.py <range>` checks each merge in the range with the
Decisions' line rule, prints a `lost` line per dropped side, a `missing`
block per path with added lines the merge lacks, and a `not compared` line
per renamed or deleted path. It exits 1 on any `lost` line.
Tests: `test_merge_check.py` builds throwaway repositories for
requirements 7 to 10, plus a merge whose only lost lines are blank or
single braces, which must pass.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/land/scripts/merge-check.py .claude/skills/land/scripts/test_merge_check.py`

### U3. Skill steps call the gates
Files: `.claude/skills/delegate/SKILL.md`, `.claude/skills/drive/SKILL.md`, `.claude/skills/land/SKILL.md`, `.claude/skills/land/references/missing-checkpoint.md`, `.claude/skills/implement/references/workers.md`, `.claude/skills/review/references/fix-loop.md`
After: U1, U2
Change: `delegate`, Reading a worker's report, adds `orca-worker.sh check`
to the tree check before the merge and `merge-check.py ORIG_HEAD..HEAD`
right after it. `drive` step 2 items 1 and 3, `land` step 3 (after its
merge of `main`), `missing-checkpoint.md`, `workers.md`, and fix-loop
step 2 name the same calls at their merges, and each report carries the
`missing` list. `delegate` item 6 and fix-loop step 1 gain requirement 12,
and step 1 says the coordinator extends the list and dispatches again
when such a blocker returns.
Tests: none executable. `check-prose.py` through the verifier covers the
wording, and the next drive exercises the calls.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/delegate/SKILL.md .claude/skills/drive/SKILL.md .claude/skills/land .claude/skills/implement/references/workers.md .claude/skills/review/references/fix-loop.md`

Waves: U1 U2 | U3

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/delegate .claude/skills/land .claude/skills/drive/SKILL.md .claude/skills/implement/references/workers.md .claude/skills/review/references/fix-loop.md
python3 .claude/skills/land/scripts/merge-check.py f5be45ac^..f5be45ac
```

The second command should exit 1 and name files under `.claude/skills/`.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] Skill prose updated in the same change.
- [ ] Parent's `Landed:` line for this phase filled.
- [ ] No plan labels in code or commit messages.

## Open questions

- Whether `merge-check.py` names the seven files the solution lists for
  `f5be45ac`. Unverified until U2 runs. A different set means the line
  rule needs another look before U3 wires it in.
