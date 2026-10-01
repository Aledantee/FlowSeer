---
name: gopkg.in/yaml.v3
ecosystem: go
required_by:
  - go.mod
  - src/protocol/smi/bench/go.mod
  - src/protocol/smi/differential/go.mod
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

Packages `tools/deps/inventory`, `protocol/yang/cmd/yanggen`, and `protocol/snmp/cmd/mibgen` import `gopkg.in/yaml.v3` at `tools/deps/inventory/statements.go:11`, `src/protocol/yang/cmd/yanggen/config.go:13`, and `src/protocol/snmp/cmd/mibgen/config.go:11`. The tool caller is `go run ./tools/deps`, and the generator callers are `src/protocol/yang/cmd/yanggen` and `src/protocol/snmp/cmd/mibgen`. The root, SMI benchmark, and SMI differential `go.mod` files pin version `v3.0.1`.

## Why it is safe

The publisher is the go-yaml project. Version `v3.0.1` was published at `2022-05-27T08:35:30Z` and was 1588 days old on 2026-10-01. Its 14-day wait ended at `2022-06-10T08:35:30Z`. The Go proxy reported no origin commit. The OSV lookup dated 2026-10-01 returned no advisory for this version. The root module graph contains 2 versions, with 0 versions only reachable through this direct dependency. The SMI benchmark graph contains 2 versions, with 1 version only reachable through this direct dependency. The SMI differential graph contains 2 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to maintain a YAML parser for dependency inventories, generator configuration, protocol fixtures, and integration manifests. Those inputs cross tool and benchmark boundaries, so a local parser would create several format implementations to maintain. The root graph contains 2 versions, with 0 versions only reachable through this direct dependency. The SMI benchmark graph contains 2 versions, with 1 version only reachable through this direct dependency. The SMI differential graph contains 2 versions, with 0 versions only reachable through this direct dependency.
