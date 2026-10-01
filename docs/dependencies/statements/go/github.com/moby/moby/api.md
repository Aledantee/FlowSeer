---
name: github.com/moby/moby/api
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The integration test environment package imports github.com/moby/moby/api/types/container at src/common/service/test/integration/testenv/otel.go:18. The SNMP integration environment imports the API at src/protocol/snmp/test/integration/testenv/snmpsim.go:12. The pinned direct requirement is `github.com/moby/moby/api` at `v1.56.0`.

## Why it is safe

The publisher is the Moby project. The pinned version `v1.56.0` was published at 2026-09-03T20:34:34Z and was 27 days old on 2026-10-01. Its 14-day wait ended at 2026-09-17T20:34:34Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/moby/moby/api` at `v1.56.0`. The dependency tree contains 10 versions, with 2 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement Docker Engine API request and model types for integration test environments. That would duplicate a versioned daemon API and expand test-only owned code. The dependency tree contains 10 versions, with 2 versions only reachable through this direct dependency.
