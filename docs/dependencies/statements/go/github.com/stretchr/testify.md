---
name: github.com/stretchr/testify
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: cut
approved: 2026-10-01
---

## Why it is required

The conformance package imports github.com/stretchr/testify/require at test/conformance/proto/bfd_rules_test.go:6. The pinned direct requirement is `github.com/stretchr/testify` at `v1.12.1`.

## Why it is safe

The publisher is stretchr. The pinned version `v1.12.1` was published at 2026-08-17T08:24:05Z and was 44 days old on 2026-10-01. Its 14-day wait ended at 2026-08-31T08:24:05Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/stretchr/testify` at `v1.12.1`. The dependency tree contains 3 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

The standard library `testing` package can replace the elementary `require` assertions in the three conformance test files. No owned test framework is needed, and the direct dependency can be removed without replacing its assertions with another package. The dependency tree contains 3 versions, with 0 versions only reachable through this direct dependency.
