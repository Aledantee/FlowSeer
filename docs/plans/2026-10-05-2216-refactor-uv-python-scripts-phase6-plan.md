---
title: Python Scripts Under uv Phase 6, Package-Local Scripts - Plan
type: refactor
date: 2026-10-05
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-10-05-2216-refactor-uv-python-scripts-plan.md
---

# Python Scripts Under uv Phase 6, Package-Local Scripts - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The scripts that live beside the package that runs them are standalone uv
scripts: `src/protocol/smi/bench/bench-gate.sh`,
`src/protocol/snmp/bench/bench-gate.sh`, the four `deploy/lab/write-*.sh`,
and `src/protocol/snmp/test/integration/scripts/capture-snmprec.sh`.

## Decisions

- The parent plan's Decisions and the
  [repository scripting record](../architecture/2026-10-05-repository-scripting-direction.md)
  apply.
- Each stays in its directory as `<name>.py` with its own metadata block
  and imports nothing from `tools/scripts/`, as the record decides.
- The Go tests that run or read them change in the same unit:
  `src/protocol/smi/bench/gate_test.go:44` runs `sh bench-gate.sh`, and
  `src/services/device/test/integration/lab_fixtures_test.go` reads the
  text of `write-lab-secrets.sh` in three places (`:381`, `:401`, and
  `:422`, the last checking `deploy/lab/README.md` against the script). One
  asserts that every `dex.env` assignment is single-quoted, which is an
  assertion about a here-document. The re-plan
  restates it against the Python (the written file's content, produced by
  running the script into a temporary directory) so the property is still
  held: a bcrypt hash reaches Dex unsubstituted.
- The task files (`src/protocol/smi/bench/Taskfile.yml:54`,
  `src/protocol/snmp/bench/Taskfile.yml:132`) call `uv run bench-gate.py`.
- `write-lab-secrets.sh` calls `openssl` 8 times and the lab scripts call
  `jq` and `curl`. `json` and `urllib` replace the last two. Whether
  `openssl` stays a subprocess or the standard library covers the
  certificate work is decided in the re-plan from what the script
  generates. A cryptography package would be a dependency with a statement.
- `deploy/lab/README.md`, `docs/runbooks/lab-icx7150-first-write.md`, and
  the snmp integration README name the new commands. The runbook test
  (`runbook_test.go:189`) runs runbook blocks through `sh -c`, which the
  re-plan reads before deciding whether those blocks may stay shell: they
  are documentation an operator pastes, not repository scripts.

## Requirements

1. Each bench gate gives the same verdict. Example: `gate_test.go` passes
   with the command changed to `uv run bench-gate.py`, on its existing
   transcripts.
2. The lab secrets script writes owner-only files. Example: every file
   under the output directory has mode `0600` on POSIX.
3. `dex.env` holds each value literally. Example: a generated bcrypt hash
   `$2y$10$...` appears in the file unchanged and single-quoted.
