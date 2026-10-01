---
name: google.golang.org/genproto/googleapis/rpc
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

Package `common/service` imports `google.golang.org/genproto/googleapis/rpc/errdetails` at `src/common/service/telemetry_otlp.go:17`. The root `go.mod` pins version `v0.0.0-20260928230214-8a89bd6388cc`.

## Why it is safe

The publisher is Google. Version `v0.0.0-20260928230214-8a89bd6388cc` was published at `2026-09-28T23:02:14Z` and was 3 days old on 2026-10-01. It was marked under the 14-day wait until `2026-10-12T23:02:14Z`, and the Go proxy reported origin commit `8a89bd6388cc9f960fc7076f7e2a43f96ad592e9`. The OSV lookup dated 2026-10-01 returned no advisory for this version. The root module graph contains 7 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to define the generated Google RPC error-detail messages used to attach structured status details to service telemetry. That would duplicate wire types shared with gRPC clients and servers. The tree contains 7 versions, with 0 versions only reachable through this direct dependency.
