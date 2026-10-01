---
name: github.com/gosnmp/gosnmp
ecosystem: go
required_by:
  - src/protocol/snmp/bench/go.mod
criteria: run
verdict: keep
approved: ""
---

## Why it is required

The SNMP benchmark package imports github.com/gosnmp/gosnmp at src/protocol/snmp/bench/macro_test.go:12. The pinned direct requirement is `github.com/gosnmp/gosnmp` at `v1.45.0`.

## Why it is safe

The publisher is the gosnmp community. The pinned version `v1.45.0` was published at 2026-09-19T08:48:32Z and was 11 days old on 2026-10-01. It is under the 14-day wait until 2026-10-03T08:48:32Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/gosnmp/gosnmp` at `v1.45.0`. The dependency tree contains 5 versions, with 4 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement an independent SNMP client for the benchmark comparison cases. That would turn a benchmark reference into product code and make protocol fidelity its responsibility. The dependency tree contains 5 versions, with 4 versions only reachable through this direct dependency.
