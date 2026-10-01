---
name: connectrpc.com/connect
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The package at src/services/device/internal/auditapi imports connectrpc.com/connect at src/services/device/internal/auditapi/service.go:12. The edge API package imports it at src/services/device/internal/edgeapi/enroll.go:12. The pinned direct requirement is `connectrpc.com/connect` at `v1.21.0`.

## Why it is safe

The publisher is ConnectRPC. The pinned version `v1.21.0` was published at 2026-09-08T13:20:25Z and was 22 days old on 2026-10-01. Its 14-day wait ended at 2026-09-22T13:20:25Z. The OSV lookup dated 2026-10-01 returned no advisory for `connectrpc.com/connect` at `v1.21.0`. The dependency tree contains 5 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement HTTP RPC routing, protocol negotiation, marshaling, and client-server plumbing used by device services. That would duplicate transport infrastructure and make interoperability code owned. The dependency tree contains 5 versions, with 0 versions only reachable through this direct dependency.
