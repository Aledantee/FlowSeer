---
title: Dependency Admission Phase 2, Removals - Plan
type: chore
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-10-01-1457-chore-dependency-admission-plan.md
---

# Dependency Admission Phase 2, Removals - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Every dependency whose statement a person ruled `cut` in phase 1 is gone
from its manifest and lockfile, replaced by owned code where something still
needs the behavior. Build-only npm packages sit under `devDependencies`, so
the `deploy` set holds only what ships.

## Decisions

- The parent plan's Decisions apply. The cut list is the set of statements
  with `verdict: cut` and an `approved` date. The re-plan reads it from
  `docs/dependencies/statements/` and plans one unit per removal, since
  each replaces different code.
- A removal that changes an exported API or a wire shape is made without a
  compatibility shim, as `AGENTS.md` states for work before the first stable
  release.
- A removal deletes the statement in the same change. The phase 1 gate
  fails a statement without a direct dependency.
- Owned code that replaces a dependency goes under `src/common/` in a
  subject directory when more than one package uses it. The re-plan checks
  each replacement against the standard the removed library implemented and
  cites it.

## Requirements

1. No manifest requires a dependency ruled `cut`. Example: after a ruling
   against a module, `grep` for its path in every `go.mod` and `go.sum`
   returns nothing.
2. `go run ./tools/deps inventory` lists fewer versions than at the end of
   phase 1, and the report gives both counts.
3. No npm package used only at build time is in `dependencies`. Example:
   the inventory classifies `@tailwindcss/vite` as `run`.
4. Removing a dependency adds none. A replacement that needs a new
   dependency goes back to the person as a new statement.
