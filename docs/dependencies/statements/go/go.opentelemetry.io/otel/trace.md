---
name: go.opentelemetry.io/otel/trace
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

Packages `common/service` and `protocol/netconf` import `go.opentelemetry.io/otel/trace` at `src/common/service/telemetry.go:13` and `src/protocol/netconf/session.go:16`. The root `go.mod` pins version `v1.46.0`.

## Why it is safe

The publisher is the OpenTelemetry Go project. Version `v1.46.0` was published at `2026-08-25T16:52:31Z` and was 37 days old on 2026-10-01. Its 14-day wait ended at `2026-09-08T16:52:31Z`, and the Go proxy reported origin commit `58db4c898f5b5594f8ba78f156475bf48486e2f2`. The OSV lookup dated 2026-10-01 returned no advisory for this version. The root module graph contains 25 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to define span, tracer, context, and propagation contracts shared by service and protocol packages. A local trace API would split correlation behavior across those callers. The tree contains 25 versions, with 0 versions only reachable through this direct dependency.
