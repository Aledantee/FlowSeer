---
name: github.com/openfga/api/proto
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: 2026-10-03
---

## Why it is required

The package at `src/services/device/internal/authz/openfga` imports `github.com/openfga/api/proto/openfga/v1` for the OpenFGA gRPC client and protobuf model messages. The pinned direct requirement in `go.mod` is `github.com/openfga/api/proto` at `v0.0.0-20260723150800-6981fff8d33b`.

## Why it is safe

The publisher is OpenFGA (CNCF). Version `v0.0.0-20260723150800-6981fff8d33b` corresponds to commit `6981fff8d33b` published at 2026-07-23T15:08:00Z and was past the 14-day wait on 2026-08-06. The OSV querybatch lookup dated 2026-10-03 returned no advisory for `github.com/openfga/api/proto` at this version or its indirect requirement `github.com/envoyproxy/protoc-gen-validate` at `v1.3.3`. The dependency tree contains 20 versions, with 5 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

OpenFGA defines its authorization model, validation rules, and gRPC service contracts in protobuf. Using the upstream API module ensures wire compatibility, typed request construction, and proto equality comparisons with OpenFGA v1.21.0 without hand-maintaining schemas. The dependency tree contains 20 versions, with 5 versions only reachable through this direct dependency.
