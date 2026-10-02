---
name: golang.org/x/net
ecosystem: go
required_by:
  - go.mod
  - src/edge/netpen/go.mod
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

Packages `modules/capture/filter` and `modules/capture/rawsocket` import `golang.org/x/net/bpf` at `src/modules/capture/filter/compile.go:4` and `src/modules/capture/rawsocket/rawsocket.go:8`. The netpen link package also imports it at `src/edge/netpen/link/bpf.go:9`. The root and netpen `go.mod` files pin version `v0.59.0`.

## Why it is safe

The publisher is the Go project. Version `v0.59.0` was published at `2026-09-08T19:18:02Z` and was 23 days old on 2026-10-01. Its 14-day wait ended at `2026-09-22T19:18:02Z`, and the Go proxy reported origin commit `540d04cfe5028e2655754591a4d3e08c586809f2`. The OSV lookup dated 2026-10-01 returned no advisory for this version. The root module graph contains 10 versions, with 0 versions only reachable through this direct dependency. The netpen module graph contains 7 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to assemble and interpret Berkeley Packet Filter programs across the capture and netpen platform variants. That would duplicate OS-sensitive packet-filter code in two owned packages. The root graph contains 10 versions, with 0 versions only reachable through this direct dependency. The netpen graph contains 7 versions, with 0 versions only reachable through this direct dependency.
