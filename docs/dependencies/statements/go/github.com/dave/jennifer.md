---
name: github.com/dave/jennifer
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

The package at src/protocol/snmp/cmd/mibgen imports github.com/dave/jennifer/jen at src/protocol/snmp/cmd/mibgen/emit_dispatch.go:6. The YANG generator imports it at src/protocol/yang/cmd/yanggen/emit_type.go:4. The pinned direct requirement is `github.com/dave/jennifer` at `v1.7.1`.

## Why it is safe

The publisher is the dave/jennifer project. The pinned version `v1.7.1` was published at 2024-09-08T22:27:02Z and was 752 days old on 2026-10-01. Its 14-day wait ended at 2024-09-22T22:27:02Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/dave/jennifer` at `v1.7.1`. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to hand-build Go source strings for mibgen and yanggen. That would make escaping, formatting, and syntax correctness generator code instead of a structured Go AST builder. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency.
