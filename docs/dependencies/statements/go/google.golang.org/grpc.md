---
name: google.golang.org/grpc
ecosystem: go
required_by:
  - go.mod
  - src/protocol/yang/test/integration/testenv/testdata/gnmitarget/go.mod
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

Packages `common/service` and `protocol/gnmi` import gRPC at `src/common/service/telemetry_otlp.go:18` and `src/protocol/gnmi/session.go:17`. The gNMI test target imports it at `src/protocol/yang/test/integration/testenv/testdata/gnmitarget/main.go:22`. The root and gNMI target `go.mod` files pin version `v1.84.0`.

## Why it is safe

The publisher is Google. Version `v1.84.0` was published at `2026-09-17T20:03:25Z` and was 14 days old by calendar date on 2026-10-01. The evidence marked it under the 14-day wait until `2026-10-01T20:03:25Z`, and the Go proxy reported origin commit `e84aa5ab15d1d2b29d54f838312ad490cb7551a8`. The OSV lookup dated 2026-10-01 returned `GO-2026-6443`, a server panic via missing authority or Host headers, affecting `google.golang.org/grpc/internal/transport` and `google.golang.org/grpc/internal/xds/server`. Repository source imports the public gRPC packages at the paths above and does not import those listed internal paths. This is an import-path assessment, not a source review. The root module graph contains 58 versions, with 25 versions only reachable through this direct dependency. The gNMI target graph contains 44 versions, with 43 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement HTTP/2 RPC transport, credentials, metadata, status handling, and the client behavior used by telemetry export and gNMI. Replacing those protocol boundaries would create a larger security and interoperability surface than the repository's domain code. The root graph contains 58 versions, with 25 versions only reachable through this direct dependency. The gNMI target graph contains 44 versions, with 43 versions only reachable through this direct dependency.
