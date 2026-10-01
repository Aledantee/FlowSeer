---
name: github.com/testcontainers/testcontainers-go
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The YANG integration test environment imports github.com/testcontainers/testcontainers-go at src/protocol/yang/test/integration/testenv/testenv.go:11. The SNMP integration environment also imports it at src/protocol/snmp/test/integration/testenv/container.go:9. The pinned direct requirement is `github.com/testcontainers/testcontainers-go` at `v0.44.0`.

## Why it is safe

The publisher is Testcontainers. The pinned version `v0.44.0` was published at 2026-08-07T10:52:31Z and was 54 days old on 2026-10-01. Its 14-day wait ended at 2026-08-21T10:52:31Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/testcontainers/testcontainers-go` at `v0.44.0`. The dependency tree contains 89 versions, with 51 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement container lifecycle, readiness, and Docker integration for integration environments. That would duplicate test infrastructure and make environment handling owned. The dependency tree contains 89 versions, with 51 versions only reachable through this direct dependency.
