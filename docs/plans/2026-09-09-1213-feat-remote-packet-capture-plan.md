---
title: Remote Packet Capture - Plan
type: feat
date: 2026-09-09
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
---

# Remote Packet Capture - Plan

## Goal

An operator names a capture against an enrolled edge, and the packets come back:
the edge captures on one of its own interfaces or terminates mirrored traffic
addressed to it, filters on the wire, writes a bounded pcapng artifact, and
streams the same records live while the capture runs. The means is two new
protobuf packages carrying the capture model and its streaming contracts, and a
capture module the edge host assembles. This plan is wrong if the edge host
landing in parallel turns out not to give a module a way to receive an
operator-originated command, because a capture nobody can start is not a feature.

## Decisions

- Split the schema across `flowseer/net/capture/v1` (ref-free values) and
  `flowseer/api/capture/v1` (the CaptureSession entity and its services). Why:
  the network model structure record draws the Primitive/Entity line at identity,
  and capture has messages on both sides of it. Recorded in
  [the remote packet capture direction](../architecture/2026-09-09-remote-packet-capture-direction.md).
- Support local-interface capture and a decapsulating receiver for ERSPAN I/II/III,
  GRE, VXLAN and TZSP; do not configure mirror sessions on devices. Why: ERSPAN
  has no standard (`draft-foschiano-erspan-03` is expired and Informational) and
  support varies by model inside one vendor's current firmware — FastIron has it
  on the ICX 7250 and 7650 and not on the 7150 the lab runs — while mirroring has
  no standard MIB and no OpenConfig model at all. Recorded in the direction above.
- Carry `PacketRecord` chunks on the wire and render pcapng at the edges. Why: the
  pcapng draft is a file format with no streaming design, and opaque bytes would
  hide every packet's metadata from the platform storing it. Recorded in the
  direction above.
- Frame every stream as a batched chunk with a dense sequence base, a counters
  snapshot, and no silent drops; an edge-originated stream re-proves itself with a
  fresh assertion inside the sixty-second window. Why: assertions are checked only
  when a stream opens, so a ten-minute capture is otherwise authorized by an
  expired credential. Recorded in
  [the streaming frame transport direction](../architecture/2026-09-09-streaming-frame-transport-direction.md).
- The capture engine lives in `src/modules/capture/` and does not use gopacket.
  Why: two hosts will assemble it, which is the admission rule in
  `src/modules/README.md`; and `src/common/internal/netpenguard` confines gopacket
  to netpen, which `AGENTS.md` forbids widening for our own artifacts.
- `CaptureFilter` is a disjunction of conjunctions and lives in `net/capture/v1`,
  not `net/packet/v1`. Why: that shape compiles to a linear cBPF program without a
  general expression compiler, and `net/packet/v1`'s deliberate refusal to define
  a universal matcher stands.
- Store the first cut's artifact on the edge. Why: `src/services/` holds only a
  README, so there is nothing central to store into. The device service record
  does decide how central persists what it owns, and none of those decisions
  covers a multi-megabyte artifact; the artifact descriptor in the schema is
  complete enough for central to serve it later without a schema change.

## Requirements

1. A capture session that names no packet count, byte count, or duration is
   rejected by protovalidate. Acceptance: a `CaptureSessionConfig` with a populated
   `ref` and `source` but an empty `budget` fails validation with the
   `capture_budget.bounded` rule; the same message with `max_duration` set to 60s
   passes.
2. A capture filter compiles to a cBPF program that the kernel accepts, and the
   program accepts exactly the packets the filter describes. Acceptance: the filter
   `any_of: [{ip_protocol: TCP, dst_port: {exact: 22}}, {ether_type: ARP}]`
   compiles to a program that accepts a TCP segment to port 22 and an ARP frame,
   and rejects a UDP datagram to port 22.
3. The decapsulating receiver unwraps a mirrored frame and records the outer
   wrapper as metadata. Acceptance: an ERSPAN Type II packet (GRE protocol 0x88BE,
   ERSPAN version 0x1, session id 7, truncation bit set) yields a `PacketRecord`
   whose `data` is the inner Ethernet frame and whose `mirror.erspan_type_ii`
   carries `session_id: 7` and `truncated: true`.
4. A capture stops at its first satisfied budget and reports why. Acceptance: a
   session with `max_packets: 100` ends at 100 records with lifecycle `COMPLETED`
   and `stop_reason: PACKET_COUNT`, and no 101st record appears in the artifact or
   the tail.
5. Loss is always attributable. Acceptance: a tail consumer that receives chunks
   with `first_sequence` 0 then 40 reads a `counters` snapshot on the second chunk
   whose drop fields sum to 20 given a 20-record batch, and the cause fields
   distinguish a ring drop from a transport drop.
6. The stored artifact is a pcapng file Wireshark reads without complaint.
   Acceptance: `capinfos` on the artifact of a 100-packet Ethernet capture reports
   100 packets, link type Ethernet, and the snap length the session configured.
7. A capture stream whose caller stops re-asserting is closed. Acceptance: an
   upload stream that sends no `SignedEdgeAssertion` for longer than the configured
   re-assertion interval is terminated with an unauthenticated error, and the
   session's state records the transport failure.
8. Captured bytes never reach telemetry. Acceptance: a capture run under a log and
   span recorder emits no attribute or log field containing packet payload, and the
   only capture data in telemetry is the counters.

## Out of scope

- Configuring SPAN, RSPAN, or ERSPAN sessions on a managed device. No standard
  model exists to configure them through; this is per-vendor capability work.
- Central-side storage, indexing, retention enforcement, and live fan-out to an
  operator client. `src/services/` holds only a README, and the persistence
  decisions the device service record does carry are about inventory rows and
  credentials rather than artifacts.
- Packet dissection, protocol decoding, or analysis of captured traffic. The
  artifact is handed to a tool that already does this well.
- The edge agent host itself: its enrollment, supervision tree, telemetry, and
  command channel land in the parallel host work this plan depends on.
- Wireless capture (802.11 monitor mode, radiotap) and any link type other than
  Ethernet and Linux SLL2.
- Mirrored traffic delivered over anything but IP. Huawei's Layer 3 remote
  mirroring can ride an MPLS LSP or TE tunnel (`hwMirrorTunnelType` in
  `spec/mib/huawei/HUAWEI-MIRROR-MIB`), and terminating those needs an MPLS data
  path the edge does not have.

## Units

### U1. Schema and direction records

Files: `docs/plans/2026-09-09-1213-feat-remote-packet-capture-phase1-plan.md`
After: none
Change: `flowseer/net/capture/v1` and `flowseer/api/capture/v1` exist with their
values, entity family, refs, and streaming service contracts; the network model
structure record's tree and import order name them.
Landed: 2026-09-09, `docs/plans/2026-09-09-1213-feat-remote-packet-capture-phase1-plan.md`.

### U2. Capture engine module

Files: `docs/plans/2026-09-09-1213-feat-remote-packet-capture-phase2-plan.md`
After: U1
Change: `src/modules/capture/` captures on a local interface, compiles a
`CaptureFilter` to cBPF, terminates the mirror encapsulations, enforces budgets,
accounts for every drop, and renders pcapng.
Landed:

### U3. Edge host wiring and lab validation

Files: `docs/plans/2026-09-09-1213-feat-remote-packet-capture-phase3-plan.md`
After: U2
Change: the edge host assembles the capture module as a `service.Module`, runs a
session end to end against the lab switches, and streams chunks under the
re-assertion rule.
Landed:

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto docs src
go test -race ./...
```

ERSPAN is proved without hardware, in three layers:

- Hand-built fixtures for the parser, one per wrapper. There is no vendor
  capture to check them against. The Wireshark sample set's
  `cisco-nexus92-erspan-marker.pcap` and `cisco-nexus10-erspan-marker.pcap` look
  like the thing to use and are not: they carry Cisco's ERSPAN3 *marker* packet,
  a separate proprietary format with its own Wireshark dissector
  (`epan/dissectors/packet-cisco-marker.c`, distinct from
  `packet-cisco-erspan.c`) holding a version and type, an SSID, a granularity
  and UTC offset, a 48-bit ASIC timestamp, UTC seconds and microseconds, a
  sequence, and an `0xA5A5A5A5` tail. Cisco's Nexus 9000 documentation describes
  it as a packet emitted once a second to carry the UTC reference for the ttag
  timestamp. It shares no field with `ErspanTypeIiiFields` and wraps no mirrored
  frame, so feeding it to the decapsulator decodes garbage rather than testing
  anything. The set holds no other ERSPAN file.
- A Linux sender in a container for the round trip. The kernel's `erspan` tunnel
  device emits Type I, II and III (`ip link add … type erspan … erspan_ver 0|1|2`,
  with `erspan_dir` and `erspan_hwid` valid on version 2 alone), and `tc … action
  mirred egress mirror dev <erspan>` feeds it. `erspan_ver 0` is a later addition
  than versions 1 and 2, so the image pins a kernel that has it rather than
  taking the host's. Open vSwitch is the second sender, with `type=erspan
  options:erspan_ver=…`, for a differently-written encoder.
- The lab for what emulation cannot reach: an ICX7150 local SPAN into the edge's
  capture interface, and a MikroTik TZSP stream to the edge's receiver, each
  producing an artifact `capinfos` reads. Both need the switches powered on,
  which needs advance notice.

The Linux sender does not cover everything, and with no vendor capture behind it
the gap is wider than it first looks. Its Type III implementation omits the
security group tag and the non-Ethernet frame type, and the optional platform
subheader is dropped from the schema, so for those fields the only check is a
fixture we wrote against a decoder we wrote — which proves the pair agree and
nothing else. No shipping-ASIC bytes exercise any mirror decapsulator at any
point in this plan. Interoperability with a real mirroring device stays
unproven until hardware is in the loop; say so in the handoff rather than
letting the sender count for more than it is.

## Definition of done

- [ ] Verifier green for every changed path in every phase.
- [ ] `spec/proto/flowseer/net/capture/v1/README.md` and
      `spec/proto/flowseer/api/capture/v1/README.md` written in the change that
      adds each package.
- [ ] `src/modules/README.md` gains its `capture` row in the change that adds the
      module.
- [ ] The network model structure record's package tree and import order name each
      new package, amended in the change that adds that package, with a dated
      entry under its `## Amendments` heading.
- [ ] `CONCEPTS.md` gains the CaptureSession entry in the change that adds the
      entity.
- [ ] Both direction records read `accepted-direction`, set by a person.
- [ ] This plan's `status` set with an outcome note under its title.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- How does an operator-originated capture command reach the edge? The edge calls
  central and central never calls the edge, and `EdgeService` has only `Enroll`,
  `Rekey` and `Heartbeat`. The command channel belongs to the parallel host work;
  U1 models the session so that either a poll or a server-stream of assignments
  fits, and U3 cannot start until the host has chosen one.
- Where does a stored artifact live once central exists, and who enforces
  retention? Needs its own direction record, written against the device service
  record's storage decisions rather than beside them, and it has to reconcile
  artifact expiry with that record's retire-is-not-purge rule. The capture
  direction takes a position on the reconciliation; a persistence record confirms
  or overrides it.
- Does the re-assertion frame also bind the RPC method? The edge README leaves
  method binding open for the first host plan to decide; if that plan binds it,
  the capture streams inherit the answer rather than setting their own.
