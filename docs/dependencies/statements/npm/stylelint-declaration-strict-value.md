---
name: stylelint-declaration-strict-value
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

The importing package graph is rooted at `frontend/web/package.json`. It requires `stylelint-declaration-strict-value` at `1.12.1`.

## Why it is safe

The lockfile pins `1.12.1`, and the inventory classifies this dependency as `run`. The `tools/deps age` and `tools/deps advisories` commands provide its publication and advisory evidence. Source not yet reviewed.

## Why not owned code

The repository does not own the external package `stylelint-declaration-strict-value`. Its pinned tree size is reported by `go run ./tools/deps tree`. Replacing it would move that package boundary and its maintenance into FlowSeer.
