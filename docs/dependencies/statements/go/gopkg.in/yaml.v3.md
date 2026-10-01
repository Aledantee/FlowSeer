---
name: gopkg.in/yaml.v3
ecosystem: go
required_by:
  - go.mod
  - src/protocol/smi/bench/go.mod
  - src/protocol/smi/differential/go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The importing package graph is rooted at `go.mod` and `src/protocol/smi/bench/go.mod` and `src/protocol/smi/differential/go.mod`. It requires `gopkg.in/yaml.v3` at `v3.0.1`.

## Why it is safe

The lockfile pins `v3.0.1`, and the inventory classifies this dependency as `deploy`. The `tools/deps age` and `tools/deps advisories` commands provide its publication and advisory evidence. Source not yet reviewed.

## Why not owned code

The repository does not own the external package `gopkg.in/yaml.v3`. Its pinned tree size is reported by `go run ./tools/deps tree`. Replacing it would move that package boundary and its maintenance into FlowSeer.
