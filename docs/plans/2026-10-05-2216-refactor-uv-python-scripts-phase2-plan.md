---
title: Python Scripts Under uv Phase 2, Existing Python - Plan
type: refactor
date: 2026-10-05
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Python Scripts Under uv Phase 2, Existing Python - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Every Python script under `.agents/skills/*/scripts/` is a registered command
under `tools/scripts/`, with its tests under `tools/scripts/tests/`, and the
skill directories hold no Python. The 27 files are listed by
`git ls-files '.agents/skills/*/scripts/*.py'`.

## Decisions

- The parent plan's Decisions and the
  [repository scripting record](../architecture/2026-10-05-repository-scripting-direction.md)
  apply.
- One package per skill under `tools/scripts/skills/`, named after the
  skill with underscores. The verifier's checks and `ledger.py` go to
  `tools/scripts/verify/`, and `check-prose.py` goes there too, as the
  record's layout says. `AGENTS.md` names the prose scripts as a policy
  surface today, and `verify/` already is one.
- Plan-file parsing becomes `lib/plans.py`: frontmatter fields, unit
  headings, and the `Files:`, `After:`, and `Landed:` lines. Five scripts
  parse these separately today (`plan-state.py`, `plan-queue.py`,
  `plan-deviations.py`, `check-plan-status.py`, `check-prose.py`). The
  re-plan reads all five first and lists every difference between their
  parsers, since two of them disagreeing on an edge case is a behavior
  change to rule on, not to merge silently.
- `runlog.py` takes its lock through `lib/lock.py`, which uses `fcntl` on
  POSIX and `msvcrt` on Windows. `merge-check.py` stops hashing `/dev/null`
  (`merge-check.py:35`) and passes empty standard input to
  `git hash-object -t tree --stdin`.
- The path edit in `field.py:16` is deleted. `catalogue` and `runlog` are
  imported as modules of their packages.
- Tests that start a shell script not yet ported (`test_orca_worker.py`,
  `test_pool_usage.py`, `test_successor.py`, `test_bench.py`,
  `test_verify_paths.py`) move with the rest and keep starting that script
  by its current path until its phase ports it. `test_verify_paths.py:7`
  finds the verifier beside its own file, so it takes the path from
  `lib/repo.py` instead.
- `tools/hooks/tests/run.sh:985` and `:1129` run `check-plan-status.py` and
  `check-test-integrity.py` by path, and `pre-tool-policy.sh:54` matches
  `.agents/skills/verify-change/scripts/*` and
  `.agents/skills/prose/scripts/*`. Both files change in this phase, and
  both are policy surfaces.
- Every `SKILL.md`, reference file, and document that names a moved script
  changes in the unit that moves it. `verify-change.sh` calls the checks
  through `run.py`, and its per-skill unittest loop is removed once no
  skill directory holds a test.

## Requirements

1. Each moved script answers as before. Example: `uv run
   tools/scripts/run.py next plan-queue --json` prints what
   `plan-queue.py --json` prints on the same tree.
2. No tracked `.py` file remains under `.agents/skills/`. Example:
   `git ls-files '.agents/skills/**/*.py'` prints nothing.
3. No module under `tools/scripts/` imports `fcntl` outside `lib/lock.py`.
   Example: a test greps the tree and fails on a second importer.
4. The lock excludes a second writer. Example: two processes appending 500
   run-log lines each leave 1,000 whole lines.
5. The suite count does not drop. Example: `run.py test` reports at least
   the sum of the tests the per-skill suites run today.
