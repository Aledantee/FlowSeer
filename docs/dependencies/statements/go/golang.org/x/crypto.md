---
name: golang.org/x/crypto
ecosystem: go
required_by:
  - go.mod
  - src/edge/netpen/go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

Packages `protocol/ssh` and `protocol/netconf` import `golang.org/x/crypto/ssh` at `src/protocol/ssh/session.go:10` and `src/protocol/netconf/session.go:18`. The edge netpen integration also imports it at `src/edge/netpen/test/integration/lab/lab.go:17`. The root and netpen `go.mod` files pin version `v0.57.0`.

## Why it is safe

The publisher is the Go project. Version `v0.57.0` was published at `2026-09-08T18:05:01Z` and was 23 days old on 2026-10-01. Its 14-day wait ended at `2026-09-22T18:05:01Z`, and the Go proxy reported origin commit `3f62bf119e84c6e35e8518a2958089ade622d1a3`. The OSV lookup dated 2026-10-01 returned `GO-2026-5932`, which affects `golang.org/x/crypto/openpgp`, `openpgp/packet`, `openpgp/armor`, `openpgp/clearsign`, `openpgp/errors`, `openpgp/elgamal`, and `openpgp/s2k`. Repository imports use `golang.org/x/crypto/ssh` at the paths above and do not import those affected OpenPGP paths. This is an import-path assessment, not a source review. The root module graph contains 9 versions, with 0 versions only reachable through this direct dependency. The netpen module graph contains 6 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement SSH cryptographic primitives and protocol handling for both the production NETCONF and SSH packages and the netpen lab. That is security-sensitive protocol code outside the repository's domain. The root graph contains 9 versions, with 0 versions only reachable through this direct dependency. The netpen graph contains 6 versions, with 0 versions only reachable through this direct dependency.
