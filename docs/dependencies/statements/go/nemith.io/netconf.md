---
name: nemith.io/netconf
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

Package `protocol/netconf` imports `nemith.io/netconf` and its SSH transport at `src/protocol/netconf/session.go:19` and `src/protocol/netconf/session.go:20`. The root `go.mod` pins version `v0.0.4`.

## Why it is safe

The publisher is the Nemith project. Version `v0.0.4` was published at `2026-01-08T17:00:49Z` and was 266 days old on 2026-10-01. Its 14-day wait ended at `2026-01-22T17:00:49Z`, and the Go proxy reported origin commit `8d43db445fb8869175b9093196f846db3a126f86`. The OSV lookup dated 2026-10-01 returned no advisory for this version. The root module graph contains 5 versions, with 4 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement NETCONF session framing and its SSH transport for the `protocol/netconf` package. That would duplicate a protocol boundary whose interoperability is exercised through the existing transport seam. The tree contains 5 versions, with 4 versions only reachable through this direct dependency.
