---
name: go.opentelemetry.io/proto/otlp
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

Package `common/service` imports generated OTLP packages from module `go.opentelemetry.io/proto/otlp` at `src/common/service/telemetry_sdk.go:27`. The root `go.mod` pins version `v1.11.0`.

## Why it is safe

The publisher is the OpenTelemetry Go project. Version `v1.11.0` was published at `2026-07-22T21:40:59Z` and was 71 days old on 2026-10-01. Its 14-day wait ended at `2026-08-05T21:40:59Z`, and the Go proxy reported origin commit `bc625d6e040020737ab65c675c87e03bc841fd60`. The OSV lookup dated 2026-10-01 returned no advisory for this version. The root module graph contains 10 versions, with 0 versions only reachable through this direct dependency.

## Why not owned code

FlowSeer would have to define and maintain the generated OTLP message types used by the service's log, metric, and trace transforms. Those wire types must remain aligned across collectors and exporters. The tree contains 10 versions, with 0 versions only reachable through this direct dependency.
