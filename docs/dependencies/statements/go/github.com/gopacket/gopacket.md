---
name: github.com/gopacket/gopacket
ecosystem: go
required_by:
  - src/edge/netpen/go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The importing package graph is rooted at `src/edge/netpen/go.mod`. It requires `github.com/gopacket/gopacket` at `v1.7.3`.

## Why it is safe

The lockfile pins `v1.7.3`, and the inventory classifies this dependency as `deploy`. The `tools/deps age` and `tools/deps advisories` commands provide its publication and advisory evidence. Source not yet reviewed.

## Why not owned code

The repository does not own the external package `github.com/gopacket/gopacket`. Its pinned tree size is reported by `go run ./tools/deps tree`. Replacing it would move that package boundary and its maintenance into FlowSeer.
