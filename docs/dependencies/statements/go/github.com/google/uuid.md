---
name: github.com/google/uuid
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

The package at src/services/device/internal/edgeapi imports github.com/google/uuid at src/services/device/internal/edgeapi/admin.go:10. The tenant package also imports it at src/common/tenant/tenant.go:7. The pinned direct requirement is `github.com/google/uuid` at `v1.6.0`.

## Why it is safe

The publisher is Google. The pinned version `v1.6.0` was published at 2024-01-23T18:54:04Z and was 981 days old on 2026-10-01. Its 14-day wait ended at 2024-02-06T18:54:04Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/google/uuid` at `v1.6.0`. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement UUID generation, parsing, and validation for identifiers. That would own a general-purpose identifier format and its edge cases instead of keeping that boundary with a maintained library. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency.
