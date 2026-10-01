---
name: google.golang.org/protobuf
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

Packages `common/service` and `services/device/internal/auditapi` import protobuf runtime packages at `src/common/service/delivery.go:16` and `src/services/device/internal/auditapi/service.go:13`. The root `go.mod` pins version `v1.36.12`.

## Why it is safe

The publisher is Google. Version `v1.36.12` was published at `2026-08-10T13:29:45Z` and was 52 days old on 2026-10-01. Its 14-day wait ended at `2026-08-24T13:29:45Z`, and the Go proxy reported origin commit `cdd4c5f7406e82462949c7a65defa9f3029c162d`. The OSV lookup dated 2026-10-01 returned no advisory for this version. The root module graph contains 5 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to maintain protobuf encoding, reflection, generated-message support, and the runtime contracts used by services, edge code, schemas, and conformance tests. A local runtime would fork the wire implementation at the center of the repository. The tree contains 5 versions, with 0 versions only reachable through this direct dependency.
