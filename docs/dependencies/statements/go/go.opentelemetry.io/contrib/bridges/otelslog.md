---
name: go.opentelemetry.io/contrib/bridges/otelslog
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

Package `common/service` imports `go.opentelemetry.io/contrib/bridges/otelslog` at `src/common/service/telemetry_sdk.go:16`. The root `go.mod` pins version `v0.20.1`.

## Why it is safe

The publisher is the OpenTelemetry Go project. Version `v0.20.1` was published at `2026-08-26T09:59:02Z` and was 36 days old on 2026-10-01. Its 14-day wait ended at `2026-09-09T09:59:02Z`, and the Go proxy reported origin commit `c4c6248ec2289133b6a51f554ca9367ece1de8e7`. The OSV lookup dated 2026-10-01 returned no advisory for this version. The root module graph contains 27 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to maintain the adapter between Go's `slog` records and OpenTelemetry log records used by the service telemetry setup. That would duplicate a narrow integration boundary whose tree contains 27 versions, with 0 versions only reachable through this direct dependency.
