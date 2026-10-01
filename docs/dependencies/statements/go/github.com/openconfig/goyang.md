---
name: github.com/openconfig/goyang
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The YANG generator package imports github.com/openconfig/goyang/pkg/yang at src/protocol/yang/cmd/yanggen/load.go:16. The pinned direct requirement is `github.com/openconfig/goyang` at `v1.6.3`.

## Why it is safe

The publisher is OpenConfig. The pinned version `v1.6.3` was published at 2025-07-01T18:46:07Z and was 456 days old on 2026-10-01. Its 14-day wait ended at 2025-07-15T18:46:07Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/openconfig/goyang` at `v1.6.3`. The dependency tree contains 26 versions, with 1 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to parse and resolve YANG modules for yanggen. That would duplicate a schema compiler and its resolution rules in owned code. The dependency tree contains 26 versions, with 1 versions only reachable through this direct dependency.
