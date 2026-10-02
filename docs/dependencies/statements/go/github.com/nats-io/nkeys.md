---
name: github.com/nats-io/nkeys
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

The package at src/modules/edgebus imports github.com/nats-io/nkeys at src/modules/edgebus/keys.go:11. The pinned direct requirement is `github.com/nats-io/nkeys` at `v0.4.16`.

## Why it is safe

The publisher is NATS.io. The pinned version `v0.4.16` was published at 2026-06-02T13:46:28Z and was 120 days old on 2026-10-01. Its 14-day wait ended at 2026-06-16T13:46:28Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/nats-io/nkeys` at `v0.4.16`. The dependency tree contains 4 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement NATS key generation and signing. That would duplicate cryptographic key formats and signing semantics in owned code. The dependency tree contains 4 versions, with 0 versions only reachable through this direct dependency.
