---
name: github.com/sleepinggenius2/gosmi
ecosystem: go
required_by:
  - src/protocol/smi/differential/go.mod
criteria: run
verdict: keep
approved: ""
---

## Why it is required

The SMI differential package imports github.com/sleepinggenius2/gosmi at src/protocol/smi/differential/outcome.go:39. The pinned direct requirement is `github.com/sleepinggenius2/gosmi` at `v0.4.4`.

## Why it is safe

The publisher is the sleepinggenius2/gosmi project. The pinned version `v0.4.4` was published at 2022-02-04T23:35:17Z and was 1699 days old on 2026-10-01. Its 14-day wait ended at 2022-02-18T23:35:17Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/sleepinggenius2/gosmi` at `v0.4.4`. The dependency tree contains 19 versions, with 17 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to write a second SMI parser to act as the independent side of the differential comparison. That would invalidate the comparison and add parser maintenance. The dependency tree contains 19 versions, with 17 versions only reachable through this direct dependency.
