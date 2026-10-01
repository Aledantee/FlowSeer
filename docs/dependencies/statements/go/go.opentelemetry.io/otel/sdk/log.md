---
name: go.opentelemetry.io/otel/sdk/log
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

Package `common/service` imports `go.opentelemetry.io/otel/sdk/log` at `src/common/service/telemetry_sdk.go:20` and `src/common/service/telemetry_otlp_transform.go:12`. The root `go.mod` pins version `v0.22.0`.

## Why it is safe

The publisher is the OpenTelemetry Go project. Version `v0.22.0` was published at `2026-08-25T16:52:31Z` and was 37 days old on 2026-10-01. Its 14-day wait ended at `2026-09-08T16:52:31Z`, and the Go proxy reported origin commit `58db4c898f5b5594f8ba78f156475bf48486e2f2`. The OSV lookup dated 2026-10-01 returned no advisory for this version. The root module graph contains 33 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to provide the log SDK implementation that turns the shared log API into records consumed by the OTLP transform. That would duplicate a telemetry pipeline beside the metric and trace SDKs. The tree contains 33 versions, with 0 versions only reachable through this direct dependency.
