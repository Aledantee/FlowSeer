---
name: github.com/yuin/goldmark
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The check-guarantees package imports github.com/yuin/goldmark at tools/check-guarantees/check.go:20. It also imports goldmark's AST package at tools/check-guarantees/check.go:21. The pinned direct requirement is `github.com/yuin/goldmark` at `v1.8.6`.

## Why it is safe

The publisher is yuin. The pinned version `v1.8.6` was published at 2026-09-03T06:08:38Z and was 27 days old on 2026-10-01. Its 14-day wait ended at 2026-09-17T06:08:38Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/yuin/goldmark` at `v1.8.6`. The dependency tree contains 2 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement a Markdown parser for check-guarantees. That would duplicate CommonMark parsing and make checker correctness an owned language implementation. The dependency tree contains 2 versions, with 0 versions only reachable through this direct dependency.
