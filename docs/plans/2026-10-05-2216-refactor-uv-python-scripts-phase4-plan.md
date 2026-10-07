---
title: Python Scripts Under uv Phase 4, Verifier - Plan
type: refactor
date: 2026-10-05
artifact_contract: flowseer-plan/v2
execution: code
---

# Python Scripts Under uv Phase 4, Verifier - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`verify-change.sh` (1,060 lines) and `tools/test/service-otel-integration.sh`
are Python under `tools/scripts/verify/`, started as
`uv run tools/scripts/run.py verify change -- <paths>`.

## Decisions

- The parent plan's Decisions and the
  [repository scripting record](../architecture/2026-10-05-repository-scripting-direction.md)
  apply.
- `test_verify_paths.py` and `test_ledger.py` are made to take the verifier
  command as a parameter and are extended first, against the shell
  verifier, until they cover every path class and every exit path the
  script has. The re-plan lists the classes from the script's `case` arms.
- The port keeps each fix `docs/agent-steering.md` records under
  "`verify-change` and the hooks": a directory expands to its files, a
  no-gate run exits non-zero before touching the receipt, the last line
  names the verdict and the gate that was running, and the `docker info`
  probe is bounded. Each gets a named test.
- The verifier's embedded Python block (`verify-change.sh:718`) becomes a
  function.
- Gates stay subprocesses with argument lists: `go`, `gofumpt`,
  `goimports`, `golangci-lint`, `buf` through `go tool`, `pnpm`, `docker`.
  The verifier still searches `$(go env GOPATH)/bin`.
- `AGENTS.md`, Work sequence, names the new command, and so does every
  `SKILL.md` and plan template that carries a `Verify:` line. The `plan`
  skill's unit template changes with it. Open plans under `docs/plans/`
  keep working because `implement` reads the `Verify:` line as text for a
  person, which the re-plan confirms by reading `implement`'s scripts.
- Receipts and the dirty marker keep their file formats, so a worktree
  verified before the port is still read correctly after it.

## Requirements

1. The suites pass against both verifiers before the shell one is deleted.
   Example: `test_verify_paths.py` passes with the command set to each.
2. A run that selects no gate exits non-zero and leaves the receipt
   untouched. Example: `verify change -- no/such/dir` exits non-zero and the
   receipt file's modification time is unchanged.
3. The last line of every run names the verdict. Example: a failing `go
   vet` ends with a line holding `FAIL` and `go vet`.
4. No document names `verify-change.sh`. Example: `git grep -l
   verify-change.sh` prints nothing outside `docs/plans/`.
