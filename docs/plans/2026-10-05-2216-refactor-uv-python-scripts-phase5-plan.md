---
title: Python Scripts Under uv Phase 5, Delegate, Drive, and Tune Scripts - Plan
type: refactor
date: 2026-10-05
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-10-05-2216-refactor-uv-python-scripts-plan.md
---

# Python Scripts Under uv Phase 5, Delegate, Drive, and Tune Scripts - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`orca-worker.sh`, `pool-usage.sh`, `successor.sh`, `bench.sh`,
`discover-host.sh`, `check-tools.sh`, and `hitl-loop.sh` are registered
commands, and no here-document of Python remains.

## Decisions

- The parent plan's Decisions and the
  [repository scripting record](../architecture/2026-10-05-repository-scripting-direction.md)
  apply.
- `pool-usage.sh` is unwrapped first: 534 of its 567 lines are one Python
  here-document, and `test_pool_usage.py` reads that block out of the shell
  file with `ast` to test it. The test then imports the module.
- `orca-worker.sh` (557 lines) and `successor.sh` are ported behind their
  existing suites (`test_orca_worker.py`, 1,196 lines, and
  `test_successor.py`), which start the script by path and need only the
  command changed.
- `orca-worker.sh`, `pool-usage.sh`, and `bench.sh` call `timeout`, which
  `subprocess` timeouts replace. The re-plan lists every other POSIX-only
  tool the seven scripts call and states its Windows counterpart, or marks
  the command POSIX-only with the reason.
- `hitl-loop.sh` is a template the `diagnose` skill copies to `$TMPDIR` and
  runs with `bash`. It becomes a command, `diagnose hitl-loop`, and the
  skill stops copying a file.
- `check-tools.sh` reports on Node, pnpm, and the web tools. It keeps doing
  that, as the frontend toolchain is out of the parent's scope.

## Requirements

1. Each suite passes against the shell script and the Python command.
   Example: `test_orca_worker.py` with the command set to each.
2. No tracked file holds a Python here-document. Example: `git grep -n
   "<<'PY'"` prints nothing outside `docs/plans/`.
3. `pool-usage` prints the same rows. Example: on fixture CLI output, the
   YAML rows match the shell version's byte for byte.
