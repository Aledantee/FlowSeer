---
name: buf.build/go/protovalidate
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The package at src/services/device/internal/edgeapi imports buf.build/go/protovalidate at src/services/device/internal/edgeapi/enroll.go:11. The registry package also imports it at src/services/device/internal/registry/registry.go:20. The pinned direct requirement is `buf.build/go/protovalidate` at `v1.4.0`.

## Why it is safe

The publisher is Buf. The pinned version `v1.4.0` was published at 2026-08-31T17:15:21Z and was 30 days old on 2026-10-01. Its 14-day wait ended at 2026-09-14T17:15:21Z. The OSV lookup dated 2026-10-01 returned no advisory for `buf.build/go/protovalidate` at `v1.4.0`. The dependency tree contains 32 versions, with 21 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement protobuf validation and its integration with generated messages. That would duplicate a shared validation contract and make every rule change local maintenance. The dependency tree contains 32 versions, with 21 versions only reachable through this direct dependency.
