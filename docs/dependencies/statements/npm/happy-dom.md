---
name: happy-dom
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

The importing package graph is rooted at `frontend/web/package.json`. It requires `happy-dom` at `20.14.5`.

## Why it is safe

The lockfile pins `20.14.5`, and the inventory classifies this dependency as `run`. The `tools/deps age` and `tools/deps advisories` commands provide its publication and advisory evidence. Source not yet reviewed.

## Why not owned code

The repository does not own the external package `happy-dom`. Its pinned tree size is reported by `go run ./tools/deps tree`. Replacing it would move that package boundary and its maintenance into FlowSeer.
