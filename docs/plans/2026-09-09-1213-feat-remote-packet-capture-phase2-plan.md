---
title: Remote Packet Capture Phase 2, Capture Engine - Plan
type: feat
date: 2026-09-09
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-09-1213-feat-remote-packet-capture-plan.md
---

# Remote Packet Capture Phase 2, Capture Engine - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`src/modules/capture/` turns a `CaptureSessionConfig` into a stream of
`PacketRecord` chunks and a pcapng artifact: it opens a capture source, pushes a
compiled filter into the kernel, terminates mirrored traffic, stops at its
budget, and accounts for every packet it did not deliver. The module is a
library at this stage, with no `service.Module` wiring, following the precedent
`src/modules/localnet` set — the leaf's shape follows the host's supervision
tree, and the host is landing in parallel. This phase is wrong if the filter
grammar turns out not to compile to a linear cBPF program, because then the
filter has to move into userspace and the whole performance argument changes.

## Decisions

The parent plan's Decisions apply. Specific to the engine:

- Capture through `golang.org/x/sys/unix` on AF_PACKET with a classic BPF filter
  attached via `SO_ATTACH_FILTER`, reading with `recvfrom` rather than a mmapped
  TPACKET ring in the first cut. Why: gopacket is barred outside netpen by
  `src/common/internal/netpenguard`, and a diagnostic capture bounded by an
  explicit budget is not a line-rate workload. The ring is an optimization to
  reach for when `dropped_by_interface` says it is needed, and the source
  interface is shaped so it can be swapped without touching the callers.
- Compile `CaptureFilter` to `[]bpf.Instruction` from `golang.org/x/net/bpf` and
  assemble with its `Assemble`, the same pair `src/edge/netpen/link/bpf.go`
  already uses. Why: it is in the module graph already and it removes any need
  for libpcap or cgo.
- Deliver through `pump.Pump` with `TrySendDropOldest`. Why: the streaming frame
  transport direction requires a producer that never blocks on its consumer and
  always reports what it dropped, which is exactly that method's contract.
- Write the pcapng artifact ourselves rather than shelling out. Why: the blocks
  the artifact needs are a Section Header, one Interface Description, an
  Enhanced Packet Block per record, and an Interface Statistics Block for the
  counters, and hand-writing those is smaller than a dependency.
- Test the decapsulator against bytes we did not write. Golden pcaps carry the
  parser tests, starting with the Wireshark sample set's
  `cisco-nexus92-erspan-marker.pcap` and `cisco-nexus10-erspan-marker.pcap`, and
  a containerised Linux `erspan` tunnel and an Open vSwitch `type=erspan` port
  carry the round-trip tests. Why: a decoder tested only against its own encoder
  proves the pair agree and nothing else, and no ERSPAN-capable device is in the
  lab. Those samples are marker packets rather than mirrored traffic, so they
  prove the Type III header parse and leave the inner-frame extraction to the
  senders. The Linux Type III encoder omits the security group tag and the
  non-Ethernet frame type, so those fields need a golden pcap or they go
  untested, and `erspan_ver 0` needs a kernel new enough to have it — the image
  pins one rather than taking the host's.

## Requirements

Carried from the parent: R2 (filter compiles and selects correctly), R3
(decapsulation with preserved metadata), R4 (budget stop with a reason), R5
(attributable loss), R6 (Wireshark-readable artifact), R8 (no payload in
telemetry). Acceptance examples are in the parent plan and are re-derived against
the tree when this phase is planned.

## Out of scope

- `service.Module` wiring, gates, and config messages. Those land with the host
  in phase 3.
- Non-Linux capture. The source is build-tagged like `src/edge/netpen/link`, and
  every other platform returns an unsupported-platform error.
- Wireless link types and radiotap.

## Open questions

- Does the mirror receiver bind its own UDP and raw sockets, or does it consume
  the same AF_PACKET source with a filter that selects the encapsulations? The
  second is one code path and one set of counters; the first is simpler to
  reason about. Decide against the tree when this phase is planned.
- Does the module own the artifact file, or does it hand chunks to a sink the
  host provides? The host's storage configuration decides, and the host does not
  exist yet.
- Is `src/modules/` the right home before a second host exists, or does the
  engine start in the edge host's `internal/`? The admission rule wants two
  hosts; the direction record names the edge agent and a future central
  collector, which is the same argument `localnet` was admitted on.
