---
name: buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The package at src/services/device/internal/host imports buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate at src/services/device/internal/host/validation.go:9. Generated package code also imports it at generated/go/proto/flowseer/net/packet/v1/ip_dscp.pb.go:10. The pinned direct requirement is `buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go` at `v1.36.12-20260825204119-511051f7f437.2`.

## Why it is safe

The publisher is Buf. The pinned version `v1.36.12-20260825204119-511051f7f437.2` was published at 2026-09-03T19:37:03Z and was 27 days old on 2026-10-01. Its 14-day wait ended at 2026-09-17T19:37:03Z. The OSV lookup dated 2026-10-01 returned no advisory for `buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go` at `v1.36.12-20260825204119-511051f7f437.2`. The dependency tree contains 6 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to hand-maintain the generated Protovalidate Go bindings used by protobuf validation. That would duplicate generated schema code, let it drift from the schemas, and make validation changes part of this repository's implementation surface. The dependency tree contains 6 versions, with 0 versions only reachable through this direct dependency.
