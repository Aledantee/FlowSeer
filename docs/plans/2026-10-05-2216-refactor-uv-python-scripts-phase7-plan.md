---
title: Python Scripts Under uv Phase 7, Layout Gate and Windows Run - Plan
type: refactor
date: 2026-10-05
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Python Scripts Under uv Phase 7, Layout Gate and Windows Run - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The tree cannot regress: a gate fails a host-side shell script or a bare
`python3` invocation, the verifier no longer requires `jq` or `shellcheck`,
and the hooks and suites have run on a Windows host.

## Decisions

- The parent plan's Decisions and the
  [repository scripting record](../architecture/2026-10-05-repository-scripting-direction.md)
  apply.
- The gate is a Go test under `test/conformance/`, beside the layout
  checks the Stop hook already runs, so it needs no new wiring: the Stop
  gate runs every package under `test/conformance/`.
- The exempt paths are a literal list in the gate, each with its reason:
  the snmpd pass-scripts, the clixon `startup.sh`, vendored files under
  `spec/`, and `docs/research/`. The list is the rule the record states,
  written down once. Adding an entry is a change to the record.
- The gate also fails a tracked `.py` file outside `tools/scripts/` that
  lacks an inline metadata block, with `docs/research/` exempt.
- `CONTRIBUTING.md` and `README.md` name uv as the script runtime and give
  the install command per platform from https://docs.astral.sh/uv/.
- The Windows run is done by a person on a Windows host, and its commands
  and results go into `docs/agent-steering.md`. A failure there reopens the
  phase that owns the failing command.
- The repository scripting record gains its `Landed` lines.

## Requirements

1. A new host-side shell script fails the gate. Example:
   `tools/scripts/x.sh` fails with the path named.
2. A bare interpreter call fails the gate. Example: a `SKILL.md` line
   starting `python3 ` fails, and `uv run tools/scripts/run.py` passes.
3. The verifier's required tools hold no `jq`, `shellcheck`, or `python3`.
4. On Windows with uv, git, and Go installed and no Git Bash: `uv run
   tools/scripts/run.py test` reports no failure, the verifier passes on
   `docs`, and an edit under `generated/` is denied in a Claude Code
   session.
