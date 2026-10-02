---
name: go.opentelemetry.io/otel/metric
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

Packages `common/service` and `services/device/internal/telemetry` import `go.opentelemetry.io/otel/metric` at `src/common/service/telemetry.go:10` and `src/services/device/internal/telemetry/telemetry.go:18`. The root `go.mod` pins version `v1.46.0`.

## Why it is safe

The publisher is the OpenTelemetry Go project. Version `v1.46.0` was published at `2026-08-25T16:52:31Z` and was 37 days old on 2026-10-01. Its 14-day wait ended at `2026-09-08T16:52:31Z`, and the Go proxy reported origin commit `58db4c898f5b5594f8ba78f156475bf48486e2f2`. The OSV lookup dated 2026-10-01 returned no advisory for this version. The root module graph contains 25 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement metric instruments, providers, and no-op behavior used by common service, device, edge bus, and protocol packages. That would scatter a replacement metric contract through code that already shares OpenTelemetry APIs. The tree contains 25 versions, with 0 versions only reachable through this direct dependency.
