---
name: github.com/openconfig/gnmi
ecosystem: go
required_by:
  - go.mod
  - src/protocol/yang/test/integration/testenv/testdata/gnmitarget/go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The package at src/protocol/gnmi imports github.com/openconfig/gnmi/proto/gnmi at src/protocol/gnmi/session.go:12. The gNMI target test package also imports it at src/protocol/yang/test/integration/testenv/testdata/gnmitarget/main.go:21. The pinned direct requirement is `github.com/openconfig/gnmi` at `v0.14.1`.

## Why it is safe

The publisher is OpenConfig. The pinned version `v0.14.1` was published at 2025-03-26T22:09:38Z and was 553 days old on 2026-10-01. Its 14-day wait ended at 2025-04-09T22:09:38Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/openconfig/gnmi` at `v0.14.1`. The dependency tree contains 22 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to define and maintain gNMI protobuf and client types for sessions and subscriptions. That would fork a protocol surface and its interoperability behavior. The dependency tree contains 22 versions, with 0 versions only reachable through this direct dependency.
