---
name: example.test/dep
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

`src` imports `example.test/dep` from `go.mod`.

## Why it is safe

The pinned version is `v0.1.0`. The dependency is used by the shipping package.

## Why not owned code

The repository does not own this external module. Replacing it would duplicate the dependency's package boundary.
