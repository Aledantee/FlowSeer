---
name: github.com/synadia-io/orbit.go/jetstreamext
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The tenant store package imports github.com/synadia-io/orbit.go/jetstreamext at src/services/device/internal/tenantstore/store.go:19. The pinned direct requirement is `github.com/synadia-io/orbit.go/jetstreamext` at `v0.3.2`.

## Why it is safe

The publisher is Synadia. The pinned version `v0.3.2` was published at 2026-07-27T13:53:22Z and was 65 days old on 2026-10-01. Its 14-day wait ended at 2026-08-10T13:53:22Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/synadia-io/orbit.go/jetstreamext` at `v0.3.2`. The dependency tree contains 11 versions, with 3 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement JetStream extensions used by tenant store code. That would duplicate NATS-specific helpers and their evolving API. The dependency tree contains 11 versions, with 3 versions only reachable through this direct dependency.
