---
name: golang.org/x/net
ecosystem: go
required_by:
  - go.mod
  - src/edge/netpen/go.mod
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

Packages `modules/capture/filter` and `modules/capture/rawsocket` import `golang.org/x/net/bpf` at `src/modules/capture/filter/compile.go:4` and `src/modules/capture/rawsocket/rawsocket.go:8`. The netpen link package also imports it at `src/edge/netpen/link/bpf.go:9`. The root and netpen `go.mod` files pin version `v0.60.0`.

## Why it is safe

The publisher is the Go project. Version `v0.60.0` was published at `2026-10-08T17:22:58Z`, and the Go proxy reported origin commit `18ece0ce30bc35fa81fe72028bf309bb3ff3f4a5`. It was adopted on 2026-10-12, inside its 14-day wait, under the advisory exception in the [dependency admission record](../../../../../architecture/2026-10-01-dependency-admission-direction.md). The OSV lookup dated 2026-10-12 returned `GO-2026-6603`, `GO-2026-6610`, `GO-2026-6611`, `GO-2026-6612`, and `GO-2026-6617` for `v0.59.0`, all HTTP/2 flaws in `golang.org/x/net/http2` and the copy of it in `net/http`. Each is fixed in `v0.60.0` and in Go 1.27.2. The ruling is that they affect FlowSeer: the device service serves Connect through `net/http` over TLS at `src/services/device/internal/host/host.go:610`, which negotiates HTTP/2, and `google.golang.org/grpc` builds on `golang.org/x/net/http2`. The root and netpen `go.mod` files therefore also require `go 1.27.2`, so no older toolchain builds them. `v0.60.0` requires no newer version of any other module than `v0.59.0` did. The root module graph contains 10 versions, with 0 versions only reachable through this direct dependency. The netpen module graph contains 7 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to assemble and interpret Berkeley Packet Filter programs across the capture and netpen platform variants. That would duplicate OS-sensitive packet-filter code in two owned packages. The root graph contains 10 versions, with 0 versions only reachable through this direct dependency. The netpen graph contains 7 versions, with 0 versions only reachable through this direct dependency.
