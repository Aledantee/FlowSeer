---
name: github.com/stretchr/testify
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The conformance package imports github.com/stretchr/testify/require at test/conformance/proto/bfd_rules_test.go:6. The pinned direct requirement is `github.com/stretchr/testify` at `v1.12.1`.

## Why it is safe

The publisher is stretchr. The pinned version `v1.12.1` was published at 2026-08-17T08:24:05Z and was 44 days old on 2026-10-01. Its 14-day wait ended at 2026-08-31T08:24:05Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/stretchr/testify` at `v1.12.1`. The dependency tree contains 3 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to replace assertion and test helper code in conformance tests. That would add a local test framework without improving shipped behavior. The dependency tree contains 3 versions, with 0 versions only reachable through this direct dependency.
