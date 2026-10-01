---
name: github.com/sleepinggenius2/gosmi
ecosystem: go
required_by:
  - src/protocol/smi/differential/go.mod
criteria: run
verdict: keep
approved: ""
---

## Why it is required

The importing package graph is rooted at `src/protocol/smi/differential/go.mod`. It requires `github.com/sleepinggenius2/gosmi` at `v0.4.4`.

## Why it is safe

The lockfile pins `v0.4.4`, and the inventory classifies this dependency as `run`. The `tools/deps age` and `tools/deps advisories` commands provide its publication and advisory evidence. Source not yet reviewed.

## Why not owned code

The repository does not own the external package `github.com/sleepinggenius2/gosmi`. Its pinned tree size is reported by `go run ./tools/deps tree`. Replacing it would move that package boundary and its maintenance into FlowSeer.
