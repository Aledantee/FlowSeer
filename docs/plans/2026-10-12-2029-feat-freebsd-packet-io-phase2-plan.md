---
title: FreeBSD Packet I/O, Phase 2, Capture Module - Plan
type: feat
date: 2026-10-12
artifact_contract: flowseer-plan/v2
execution: code
amends: docs/architecture/2026-10-03-edge-high-availability-direction.md
---

# FreeBSD Packet I/O, Phase 2, Capture Module - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`rawsocket.OpenLocalInterface` and `rawsocket.OpenMirrorReceiver` work on
FreeBSD. The means are a local source over `bpfdev` and a mirror receiver
over raw and UDP sockets with FreeBSD's control messages, both behind the
receive loops the Linux sources already have.

Stop condition: the plan is wrong if the VM shows `bs_recv` excluding
frames a userspace filter later rejects, since the counter contract rests
on it.

## Decisions

The parent's decisions apply
([parent plan](2026-10-12-2029-feat-freebsd-packet-io-plan.md)) and the
[FreeBSD packet I/O record](../architecture/2026-10-12-freebsd-packet-io-direction.md),
"Per-path choices". This phase adds:

- The local source's receive loop moves out of `local_linux.go` into a file
  with no build constraint, over a reader seam that yields one frame with
  its wire length and capture time, or a retryable or terminal error. The
  Linux reader wraps `recvmsg` and `restoreVLAN`. The FreeBSD reader drains
  one `ReadBatch` at a time and copies each record. Why: the loop's
  cancellation, close, and panic paths are the subtle part
  (`receiveOnce`'s comment), and one copy tested on every host beats two.
- The mirror loop, `runMirrorLoop`, moves to a `linux || freebsd` file,
  since it uses `unix.Sockaddr`. Socket opening and control-message parsing
  stay per platform.
- FreeBSD control-message parsers have no build constraint and take raw
  bytes: `IP_RECVDSTADDR` (a 4-byte `in_addr`), `IP_RECVIF` (a
  `sockaddr_dl` with `sdl_index` at offset 2, native order), and
  `IPV6_PKTINFO` (address at 0, `ipi6_ifindex` at 16).
- The `*_other.go` stubs become `!linux && !freebsd`.
  `ErrUnsupportedPlatform`'s hint names Linux and FreeBSD.
- The edge high availability record loses its "Local capture is Linux-only"
  sentence and its open question "What capture does on FreeBSD", which the
  FreeBSD packet I/O record answers.

## Requirements

1. Parent requirement 2, in the VM.
2. Parent requirement 3, in the VM, for GRE from `gre(4)` and VXLAN from
   `vxlan(4)` in a VNET jail.
3. `bind_interface` drops traffic from another interface before counting
   it. Example: with `bind_interface` `epair1b`, VXLAN arriving on
   `epair0b` yields no frame and leaves `Stats` received at 0.
4. `Stats` reports deltas. Example: two calls with three frames between
   them report 3 and then 0.
5. A non-Ethernet interface is refused. Example: `OpenLocalInterface("lo0",
   false, nil)` returns an error with code `capture/source-open`.
6. The Linux behaviour is unchanged. Example: every test in
   `local_linux_test.go` and `mirror_linux_test.go` passes.

## Out of scope

- Any change to the capture engine, the filter compiler, the pcapng writer,
  or `mirror.Decode`.

## Open questions

- Whether the FreeBSD reader should keep the unread records of a batch
  across a `Close` or drop them. Dropping matches Linux, where a closed
  socket's queue is lost.
- Which jail setup `vxlan(4)` needs in the bento box. Unverified.
