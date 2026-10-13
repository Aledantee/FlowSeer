---
title: FreeBSD Packet I/O, Phase 3, netpen Leg and simload Sender - Plan
type: feat
date: 2026-10-12
artifact_contract: flowseer-plan/v2
execution: code
---

# FreeBSD Packet I/O, Phase 3, netpen Leg and simload Sender - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`link.Open` and `packetio.OpenSender` work on FreeBSD. The means are
`bpfdev` adapters behind the seams the Linux types already use
(`packetSocket` in `link_linux.go`, `writeSocket` in `sender_linux.go`).

Stop condition: the plan is wrong if netpen's attacks need frames the host
itself sends, since the leg opens with `BPF_D_IN`.

## Decisions

The parent's decisions apply
([parent plan](2026-10-12-2029-feat-freebsd-packet-io-plan.md)) and the
[FreeBSD packet I/O record](../architecture/2026-10-12-freebsd-packet-io-direction.md),
"Per-path choices". This phase adds:

- `linuxLeg` becomes a shared leg in a `linux || freebsd` file, and timeout
  detection joins its `packetSocket` seam. Why: `isTimeout` compares
  against `afpacket.ErrTimeout`, and the FreeBSD adapter's timeout is an
  empty `ReadBatch`.
- The FreeBSD adapter keeps the unread records of a batch and returns one
  copy per `ReadPacketData`, which gives the leg the one-frame-per-read
  shape `afpacket.TPacket` has.
- `SetFilter` on FreeBSD refuses a program over 512 instructions with
  `ErrCodeLegOpen`, before it reaches `BIOCSETF`.
- `linuxSender` becomes a shared sender in a `linux || freebsd` file. The
  FreeBSD opener passes `HeaderComplete` and a read filter of one
  `RetConstant{Val: 0}`.
- netpen imports `go.aledante.io/FlowSeer/src/common/net/bpfdev` through its
  existing `replace` of the root module. `bpfdev` uses only x/sys and x/net,
  so it adds no new module to the root graph.
- The `*_other.go` files and `sender_other_test.go` become
  `!linux && !freebsd`. The hints name Linux and FreeBSD.

## Requirements

1. Parent requirement 4, in the VM.
2. Parent requirement 5, in the VM.
3. A filter over 512 instructions is refused. Example: `SetFilter` with 513
   instructions returns an error with code `netpen/leg-open`, and the
   installed filter is unchanged.
4. The Linux behaviour is unchanged. Example: every test in
   `link_linux_test.go` and `sender_linux_test.go` passes.

## Out of scope

- netpen attacks, the runner, and recon. Only the leg changes.

## Open questions

- Whether any netpen attack reads frames its own host sends through the
  attack leg. A grep of `src/edge/netpen/attacks` for that pattern decides
  it before this phase is written ready.
