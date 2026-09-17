---
title: Remote Packet Capture - Direction
type: direction
date: 2026-09-09
topic: remote-packet-capture
status: proposed-direction
---

# Remote Packet Capture - Direction

An operator troubleshooting a site cannot reach the wire. The edge can. Remote
packet capture is the feature that closes that gap: an operator names a capture,
an edge runs it, and the packets come back. This record settles where the schema
lives, what an edge is asked to do, and what the platform is not allowed to do
with what comes back.

## The edge captures and decapsulates; it does not configure mirrors

An edge acquires packets two ways, and only two.

It captures on one of its own interfaces. Whatever puts traffic on that
interface — a SPAN cable, an RSPAN VLAN trunked to the edge's port, a passive
TAP — is the operator's arrangement, and the edge neither knows nor cares.

It terminates encapsulated mirror traffic addressed to it. A switch that can send
mirrored frames across a routed network wraps them, and the edge unwraps them: the
inner frame becomes the captured packet, and the outer wrapper becomes metadata
recorded beside it. The receiver handles ERSPAN Type I, II and III, plain GRE,
VXLAN, and TZSP, because that is the set the corpus actually uses.

It does not configure the mirror session on the device. There is nothing to
configure it *through*: the repository's own vendor survey records that mirroring
has no standard MIB and no OpenConfig model, that `SMON-MIB`'s `portCopyTable`
(RFC 2613) is the standard that lost, and that each family ships its own
(`HH3C-MIRRORGROUP-MIB`, `HUAWEI-MIRROR-MIB`, `CISCOSB-MIR-MIB`, mirroring objects
inside `FOUNDRY-SN-SWITCH-GROUP-MIB`) with nothing in common but the idea
(`docs/research/network-domain-atlas/entities/03-switching.md`). Device-side
mirror configuration is a per-vendor capability model and belongs to whatever
record decides device-facing capabilities, not to this one.

## ERSPAN is a receiver capability, never a foundation

ERSPAN reads like a standard and is not one. `draft-foschiano-erspan-03` is an
expired individual submission with Informational intent and no IETF endorsement,
and it describes what Cisco shipped rather than what anyone agreed to.

Support is uneven inside a single vendor's current firmware, which is the part
that costs you. FastIron implements ERSPAN, but per model: the RUCKUS FastIron
Features and Standards Support Matrix records it on the ICX 7250 from 08.0.40 and
the ICX 7650 from 08.0.70, and not on the ICX 7150 at any release. The lab's
Ruckus is an ICX7150-24-POE on FastIron 10.0.10g, so the switch on the desk falls
on the wrong side of that line while its stablemates do not. Meanwhile MikroTik
streams over TZSP and cloud fabrics mirror over VXLAN.

A receiver that assumed ERSPAN would therefore fail against particular models of a
vendor it already supports, which is a worse failure than not supporting a vendor
at all: it looks like a bug rather than a boundary.

Huawei makes the same point from a different direction. `HUAWEI-MIRROR-MIB` in
`spec/mib/huawei/` models three tiers rather than one: local mirroring
(`hwLocalObserveTable` and the port, flow, and slot tables beside it), Layer 2
remote mirroring (`hwRemoteObserveTable`, `hwRemotePortMirrorTable`), and Layer 3
remote mirroring in `hwRemoteMirrorInstanceTable`, where `hwRemoteObservePortIp`
is the "Remote mirror destination". The tier an ERSPAN receiver cares about is the
third, and on Huawei it is a separately obtained plug-in rather than part of the
base image, with its own "how to obtain the Layer 3 remote mirroring (ERSPAN)
plug-in" page in the S300, S500, S2700, S5700, S6700, S9300, and S12700
configuration guides.

`hwMirrorTunnelType` in that table settles a question the receiver would otherwise
get wrong. It takes `lspTunnel(1)`, `teTunnel(2)`, or `greTunnel(3)`, so Huawei's
Layer 3 remote mirroring can ride an MPLS LSP or TE tunnel and never appear as an
IP-delivered packet at all. Our receiver terminates IP-delivered wrappers, and
only those. "The device supports Layer 3 remote mirroring" is therefore not the
same claim as "our receiver can catch it", and the capability model that
eventually describes a device's mirroring has to carry the transport as well as
the tier.

That table also records `hwMirrorSliceSize`, 64 to 9600 octets, "Number of bytes
of intercepted packets" — a device-side snap length. It is independent of the snap
length on our own capture session, which bounds what the edge keeps; where both
apply, the smaller wins and the packet is truncated twice.

So ERSPAN is one arm of a decapsulation `oneof` and one value of an
encapsulation enum. A design that assumed ERSPAN as the transport would have no
answer for half the corpus, including the switch on the desk.

## Two packages, split on the existing rule

The network model structure record draws the line at identity: values with no
ref, tenant, or lifecycle are Primitives under `flowseer/net/…`, and everything
that carries a ref is an Entity further up. Capture falls on both sides of it, so
it gets one package on each.

`flowseer/net/capture/v1` holds the values. A link type, a capture filter, the
mirror encapsulation metadata, one captured packet, and the capture counters are
all ref-free descriptions of what was on the wire. The package imports
`net/addr`, `net/packet`, and `net/switching`, and nothing above them.

`flowseer/api/capture/v1` holds the CaptureSession entity, its ref pair, its
lifecycle, and its services. A session has an identity, an owner, a lifecycle,
and an operator who authorized it, which is the definition of an Entity. Its
owning parent is the edge that runs it, so `api/capture` imports `api/edge`.

`net/packet/v1` deliberately declines to define a universal packet matcher, and
that stands. `CaptureFilter` has a narrower job: it is a capture-time selection
predicate with a compile-to-BPF contract and a grammar bounded by it, and it
belongs with the capture values that share that contract. It composes the existing match
atoms — `TransportPortMatch`, `TcpFlagsMatch`, `IcmpMatch`, `EtherType`,
`IpProtocol` — rather than restating them.

The filter grammar is a disjunction of conjunctions and nothing more. Every
clause ANDs its populated fields, and the clauses OR. That is precisely the shape
that compiles to a linear cBPF program without a general expression compiler, and
a filter language that cannot be executed on the capture path is a filter
language that silently becomes a userspace loop.

## Records on the wire, pcapng at the edges

The wire form is `PacketRecord` chunks under the streaming frame transport
direction, not pcapng blocks in a `bytes` field.

The pcapng draft is explicit about being a file format: its Section Header Block
carries a section length for seeking, its structure is built for backward
navigation, and it acknowledges no live transport requirement
(`draft-ietf-opsawg-pcapng-05`). Carrying it as opaque bytes would also make
every packet's metadata invisible to the platform that is storing and indexing
it, which defeats the point of having a schema.

pcapng stays where it earns its keep: as a rendering. The edge writes it for the
stored artifact, a client writes it to hand to Wireshark, and both are pure
functions of the records. Link types come from the pcap LINKTYPE registry as
pass-through values (`LINKTYPE_ETHERNET` is 1, `LINKTYPE_LINUX_SLL2` is 276), and
the registry's real zero, `LINKTYPE_NULL`, is kept, so presence rather than zero
means "not reported" — the same treatment `IpProtocol` already gets.

A `PacketRecord` carries its own capture timestamp, which is a named exception to
the rule that a Primitive holds no observation time. The rule exists so that a
`Vlan` row does not inherit the story of which binding fetched it, and that story
still rides the envelope here. A packet's timestamp is different in kind: it is
part of what was captured rather than a fact about the capturing, a pcapng
Enhanced Packet Block cannot be written without it, and a batch of packets
sharing one envelope timestamp would lose the inter-packet timing that is the
whole reason to look at a capture. The exception is stated in
`net/capture/v1`'s README so a reader of the package sees it where they need it.

## Every capture is bounded and authorized

A capture session with no stop condition is a disk filling on a customer's
network. The schema refuses one: a session must carry at least one of a packet
count, a byte count, or a duration, enforced in protovalidate rather than in a
service that might forget.

Snap length defaults to headers only. An operator diagnosing a topology problem
needs the first hundred-odd bytes of each frame, and asking for the payload
should be a deliberate act with a reason attached, not what happens when a field
is left unset.

Captured payload is the content of other people's communications. Under Directive
2002/58/EC Article 5(1) that content is confidential by default across the EU,
and the Regulation that would have replaced the Directive was withdrawn in the
Commission's 2025 work programme, so the Directive is the standing rule rather
than a transitional one. The Digital Omnibus proposed in November 2025 and still
in negotiation would move the terminal-equipment consent rules of Article 5(3)
into the GDPR; it leaves the confidentiality rule in Article 5(1) where it is, so
the provision this record rests on is not the one in play. FlowSeer is not the party that decides whether a given capture is lawful;
the operator's organization is. What FlowSeer owes is the record that makes the
decision auditable and the restraint that keeps the data from spreading:

- a session records who authorized it and why, and a capture with full payload
  records that choice distinctly from a headers-only one;
- retention is bounded and explicit on the session, and expiry deletes the
  artifact bytes. This is a deliberate departure from the device service's
  "retire is not purge" rule, which keeps history and events and reserves hard
  deletion for a tenant-level forget. That rule is right for an inventory row and
  wrong for other people's traffic: the session record and its counters survive
  expiry, and only the payload goes. The session record is what an audit needs;
  the payload is what an audit is about;
- captured bytes never enter a log line, a span attribute, or a metric label,
  which is `docs/conventions/observability.md` applied rather than extended;
- an artifact is served only to a caller authorized for that session, and a
  download is itself an event.

## Consequences

- Two new protobuf packages join the tree in the network model structure record,
  with `{net/addr, net/packet, net/switching} ← net/capture` and
  `{net/capture, api/edge} ← api/capture` added to the import order. The
  `net/capture ← api/capture` edge is the one that carries the weight: a session's
  state holds the counters and the link type, and every chunk holds records.
- The capture engine lives in `src/modules/capture/`, because the edge agent and
  a future central collector both terminate mirrored traffic, which is the
  two-host admission rule in `src/modules/README.md`.
- That engine may not use gopacket. `src/common/internal/netpenguard` confines
  gopacket to the netpen module, and the correct response is to write the capture
  path against `golang.org/x/sys/unix` and `golang.org/x/net/bpf` rather than to
  widen the guard's allowlist for our own convenience.
- Central-side storage and fan-out are not decided here. The device service
  record already decides how central persists what it owns — a secret store
  holding credentials behind a `credential_ref`, JetStream events with durable
  consumers feeding a state store, and the retire-is-not-purge retention rule —
  but none of that is a store for a multi-megabyte artifact, and `src/services/`
  holds only a README. So the first cut stores the artifact on the edge, and the
  artifact descriptor in the schema is complete enough for a central store to
  serve it later without a schema change. Whoever writes that store reconciles it
  against the device service record rather than starting fresh.

## Sources

Researched 2026-09-09 from primary sources (IETF drafts and RFCs, vendor
documentation, EU legislation) and from schemas already vendored here.

- ERSPAN: `draft-foschiano-erspan-03`, an expired individual submission with
  Informational intent, latest revision February 2017 —
  https://datatracker.ietf.org/doc/draft-foschiano-erspan/
- The other wrappers the receiver terminates: RFC 2784 (GRE) and RFC 2890 (the
  GRE key and sequence number), RFC 7348 (VXLAN) —
  https://www.rfc-editor.org/ ; TZSP, UDP port 37008, a four-byte header of
  tagged fields ahead of the encapsulated frame —
  https://www.wireshark.org/docs/dfref/t/tzsp.html
- FastIron ERSPAN support by model: the RUCKUS FastIron Features and Standards
  Support Matrix, which covers the ICX 7150, 7250 and 7650 together and records
  ERSPAN on the 7250 from 08.0.40 and the 7650 from 08.0.70 and on the 7150 at
  no release —
  https://support.ruckuswireless.com/documents/4023-fastiron-09-0-10-ga-features-and-standards-support-matrix
- Mirroring has no standard model: `SMON-MIB::portCopyTable` (RFC 2613) and the
  per-vendor models that replaced it, recorded here in
  `docs/research/network-domain-atlas/entities/03-switching.md` (the `mirroring`
  section) and `docs/research/network-domain-atlas/vendors/standards.md` ("the
  standards that lost").
- Huawei's three mirroring tiers, `hwMirrorTunnelType`, and `hwMirrorSliceSize`
  — `spec/mib/huawei/HUAWEI-MIRROR-MIB`, vendored here.
- pcapng: `draft-ietf-opsawg-pcapng-05`, whose abstract describes "a format to
  record captured packets to a file" —
  https://datatracker.ietf.org/doc/draft-ietf-opsawg-pcapng/
- Link types: the tcpdump and libpcap LINKTYPE registry, source of
  `LINKTYPE_NULL = 0`, `LINKTYPE_ETHERNET = 1` and `LINKTYPE_LINUX_SLL2 = 276` —
  https://www.tcpdump.org/linktypes.html
- Confidentiality of communications: Directive 2002/58/EC Article 5(1) —
  https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:32002L0058 . The
  replacing Regulation was withdrawn in the 2025 Commission work programme,
  COM(2025) 45 final of 11 February 2025, Annex IV —
  https://commission.europa.eu/document/download/7617998c-86e6-4a74-b33c-249e8a7938cd_en?filename=COM_2025_45_1_annexes_EN.pdf

## Amendments

### 2026-09-17 — the CaptureSession entity moved to model/capture

"Two packages, split on the existing rule" above describes
`flowseer/api/capture/v1` as holding the CaptureSession entity together
with its services. The entity — the ref pair, the lifecycle, and the chunk
frames `CapturePacketChunk` and `CaptureArtifactChunk` — split out to
`model/capture/v1`, which imports `model/edge` for the owning ref in place
of `api/edge`. `api/capture/v1` keeps `CaptureService` and imports
`model/capture` for the entity it returns; `CaptureEdgeService` left
`api/capture` for `edge/capture`, and the open question about reaching the
edge moved with it. The import-order line in Consequences reads
`{model/capture, model/edge, net/capture} ← api/capture` and
`{model/edge, net/capture} ← model/capture` now. See [the network model
structure
record](2026-08-20-network-model-structure-direction.md#the-package-tree).
