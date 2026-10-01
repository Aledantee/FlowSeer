---
name: github.com/bufbuild/buf
ecosystem: go
required_by:
  - tools/buf/go.mod
criteria: run
verdict: keep
approved: ""
---

## Why it is required

The tool directive at tools/buf/go.mod:3 names github.com/bufbuild/buf/cmd/buf. Repository callers run it through go tool -modfile=tools/buf/go.mod buf lint and buf build, including the commands documented at spec/proto/ruckus/README.md:65. The pinned direct requirement is `github.com/bufbuild/buf` at `v1.73.0`.

## Why it is safe

The publisher is Buf. The pinned version `v1.73.0` was published at 2026-09-11T12:34:41Z and was 19 days old on 2026-10-01. Its 14-day wait ended at 2026-09-25T12:34:41Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/bufbuild/buf` at `v1.73.0`. The dependency tree contains 284 versions, with 283 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to replace Buf's schema lint, build, and code-generation toolchain. That would move a repository-wide protobuf tool into owned code and remove the stable tool directive caller. The dependency tree contains 284 versions, with 283 versions only reachable through this direct dependency.
