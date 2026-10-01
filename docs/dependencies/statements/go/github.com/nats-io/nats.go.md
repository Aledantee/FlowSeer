---
name: github.com/nats-io/nats.go
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The common service package imports github.com/nats-io/nats.go at src/common/service/bus.go:19. The audit publisher imports its JetStream package at src/services/device/internal/auditapi/publisher.go:6. The pinned direct requirement is `github.com/nats-io/nats.go` at `v1.54.0`.

## Why it is safe

The publisher is NATS.io. The pinned version `v1.54.0` was published at 2026-09-18T19:34:58Z and was 12 days old on 2026-10-01. It is under the 14-day wait until 2026-10-02T19:34:58Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/nats-io/nats.go` at `v1.54.0`. The dependency tree contains 16 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement NATS client and JetStream APIs used by service and edge-bus code. That would duplicate messaging protocol and persistence semantics. The dependency tree contains 16 versions, with 0 versions only reachable through this direct dependency.
