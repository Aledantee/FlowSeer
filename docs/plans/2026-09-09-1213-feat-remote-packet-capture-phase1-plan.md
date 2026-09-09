---
title: Remote Packet Capture Phase 1, Schema - Plan
type: feat
date: 2026-09-09
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
parent: docs/plans/2026-09-09-1213-feat-remote-packet-capture-plan.md
amends: docs/architecture/2026-08-20-network-model-structure-direction.md
---

# Remote Packet Capture Phase 1, Schema - Plan

## Goal

The complete protobuf model for remote packet capture exists and passes the
repository's schema gates: `flowseer/net/capture/v1` carries the ref-free values a
capture produces and selects on, `flowseer/api/capture/v1` carries the
CaptureSession entity and the streaming service contracts, and the network model
structure record names both. The only Go that changes is `buf generate` output.
This phase is wrong if `api/capture` turns out to need something from
`api/inventory`, because that would mean a capture session is scoped to a device
rather than to the edge that runs it, and the ref pair would be shaped
differently.

## Decisions

The parent plan's Decisions apply. Five are specific to the schema:

- The mirror wrapper is a typed variant, one message per encapsulation with its
  own fields and its own rules, under a required `oneof`. Why: an ERSPAN Type III
  header and a TZSP header share no field, and the conventions call for a variant
  message per arm exactly where the arms validate differently.
- `LinkType` keeps the pcap registry's real zero (`LINK_TYPE_NULL`) rather than
  inventing an unspecified sentinel. Why: the registry-pass-through class in
  `docs/conventions/protobuf.md`, the same treatment `IpProtocol` gets, where
  presence rather than zero carries "not reported".
- `CaptureFilterClause` gets its own `VlanMatch` instead of reusing
  `flowseer.net.switching.v1.VlanTag`. Why: `VlanTag` is an exact observed tag
  whose four fields are all `required`, and its own file comment says
  "configuration predicates use separate types". An operator asking for VLAN 100
  would otherwise have to assert a TPID, a PCP, and a DEI as well.
- `PacketRecord.data` carries a `max_len` rather than the chunk deriving a total
  size in CEL. Why: `buf.yaml` enables the `PROTOVALIDATE` lint rule, which
  compiles every expression, and CEL's macro set has no fold to sum a repeated
  field's element sizes with. Bounding the element and the item count bounds the
  chunk by multiplication.
- The phase stays one plan across its six units. Why: a plan past 300 lines is
  checked for dependency clusters, and these units are one. U5 and U6 embed the
  values U1 through U3 define, so an `api/capture` phase cut away from
  `net/capture` could not start until the other had landed and would buy nothing
  but a second handoff.

## Requirements

1. A `CaptureBudget` with no bound is invalid. Acceptance: `{}` fails the
   `capture_budget.bounded` CEL rule; `{max_duration: 60s}` passes; so does
   `{max_packets: 1000, max_bytes: 10485760}`.
2. A `CaptureFilterClause` that constrains nothing is invalid. Acceptance: a
   clause with every field unset fails `capture_filter_clause.non_empty`; a clause
   with only `ether_type: ETHER_TYPE_ARP` passes.
3. A clause cannot carry a registry value outside its wire domain. Acceptance: a
   clause with `ether_type: ETHER_TYPE_UNSPECIFIED` explicitly set fails the
   `flowseer.net.packet.v1.ether_type` predefined rule, and `ip_protocol` above
   255 or `dscp` above 63 fails its use-site bound.
4. A `CaptureFilter` with no clauses means "accept everything" and is valid.
   Acceptance: `CaptureFilter{}` passes validation, and its README line says an
   absent filter and an empty filter are the same thing.
5. `PacketRecord` distinguishes a truncated capture from a short packet.
   Acceptance: a record with `original_length: 1514` and 128 bytes of `data`
   validates, and `original_length` less than `len(data)` fails
   `packet_record.length_consistent`.
6. A `CaptureSessionState` that is not terminal carries no artifact, and a
   `COMPLETED` one carries a stop reason. Acceptance: state with lifecycle
   `CAPTURE_LIFECYCLE_RUNNING` and a populated `artifact` fails
   `capture_session_state.artifact_terminal`; lifecycle
   `CAPTURE_LIFECYCLE_COMPLETED` with `stop_reason` left at
   `CAPTURE_STOP_REASON_UNSPECIFIED` fails
   `capture_session_state.completed_has_reason`.
7. A capture chunk is bounded by construction. Acceptance: a
   `CapturePacketChunk` with 4097 records fails the repeated `max_items` rule, and
   a `PacketRecord` whose `data` exceeds 65535 octets fails its `max_len`, so the
   worst-case chunk is the product of the two and needs no CEL sum.
8. The upload stream's request is a `oneof` of a chunk and an assertion.
   Acceptance: `UploadCaptureRequest` with neither arm set fails the required
   `oneof`; with a `SignedEdgeAssertion` and no chunk it passes.
9. The schema gates pass. Acceptance: `buf lint` with `MINIMAL` and
   `PROTOVALIDATE` reports nothing on the new packages, `buf format` leaves them
   unchanged, and the triad and ref sync hooks report nothing.

## Out of scope

- Hand-written Go. `buf generate` output lands in the same change as the schema,
  per `docs/code-style-proto.md`, and nothing else under `src/` changes.
- Central-side persistence messages beyond the artifact descriptor.
- A device-facing mirror capability model.
- `buf breaking` as a gate. `buf.yaml` ignores the whole `spec/proto/flowseer`
  module until the first stable release, so a breaking check here would pass
  vacuously; the shape is held in review instead.

## Units

### U1. Capture values leaf: link type and counters

Files: `spec/proto/flowseer/net/capture/v1/link_type.proto`,
`spec/proto/flowseer/net/capture/v1/capture_counters.proto`
After: none
Change: `LinkType` names the pcap LINKTYPE registry values a FlowSeer capture may
report (`LINK_TYPE_NULL = 0`, `LINK_TYPE_ETHERNET = 1`, `LINK_TYPE_RAW = 101`,
`LINK_TYPE_LINUX_SLL = 113`, `LINK_TYPE_IPV4 = 228`, `LINK_TYPE_IPV6 = 229`,
`LINK_TYPE_LINUX_SLL2 = 276`), documented as registry pass-through with the
registry URL, so an unnamed assigned value stays meaningful. The engine emits
only Ethernet and Linux SLL2 in the first cut, which is an engine limit rather
than a schema one, and the comment says so.

`CaptureCounters` carries `received`, `accepted`, `dropped_by_interface`,
`dropped_by_budget`, and `dropped_by_transport` as `uint64`, each with a comment
saying which side of the pipeline counted it. The split by cause is what lets an
operator tell a switch oversubscribing the mirror port from an edge that cannot
keep up from an uplink that cannot.

Tests: none — `spec/proto/` holds only `.proto` and `README.md`, and schema rules
are enforced by `buf lint`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/capture/v1`

### U2. Capture filter

Files: `spec/proto/flowseer/net/capture/v1/capture_filter.proto`,
`spec/proto/flowseer/net/packet/v1/ip_protocol.proto`,
`spec/proto/flowseer/net/packet/v1/ip_dscp.proto`
After: none
Change: `CaptureFilter` holds `repeated CaptureFilterClause any_of` with
`max_items: 32`; an empty list accepts every packet. `CaptureFilterClause` ANDs
its populated fields and carries `EtherType ether_type`, `VlanMatch vlan`,
`EuiAddress src_mac`, `EuiAddress dst_mac`, `IpPrefix src_prefix`,
`IpPrefix dst_prefix`, `IpProtocol ip_protocol`, `TransportPortMatch src_port`,
`TransportPortMatch dst_port`, `TcpFlagsMatch tcp_flags`, `IcmpMatch icmp`, and
`IpDscp dscp`, each optional, with a `capture_filter_clause.non_empty` CEL rule.

Every registry enum field carries its use-site rule, which the conventions
require and which here also keeps the compiler honest. Without one a clause could
name EtherType zero, and the compiler would emit a comparison against an 802.3
length field that can never match, so the capture returns nothing and the
operator has no error to read.

`ether_type` takes
`(buf.validate.field).enum.(flowseer.net.packet.v1.ether_type) = true`, as
`VlanTag.tpid` already does. `ip_protocol` and `dscp` have no such rule to take.
[The model conventions](../conventions/protobuf.md) require every field using
`IpProtocol` or `IpDscp` to validate the whole numeric domain at the use site —
`0..255` and `0..63` — but neither enum is used anywhere in `spec/proto/flowseer/`
yet, so there is no landed pattern to copy and this unit writes the first one.
`buf.validate.EnumRules` carries no numeric bound, so each gets a predefined CEL
extension beside its enum in the shape `ether_type` already has: `ip_protocol` at
extension number 50002 in `ip_protocol.proto` and `ip_dscp` at 50003 in
`ip_dscp.proto`, the next free numbers on that extendee after `ether_type`
(50000) and `tcp_flag` (50001). The rule goes beside the enum rather than inline
here because the conventions ask it of every future field too, and a bound
written once cannot drift between use sites.

`VlanMatch` carries an optional `vlan_id` under the existing
`flowseer.net.switching.v1.vlan_tag_vid` predefined rule and an optional `pcp`
under `vlan_pcp`, with its own non-empty rule. Its comment records why the exact
`VlanTag` is not reused.

The file comment states the compile contract: every clause is a linear cBPF
sequence, the clauses are alternatives, and nothing here may require an
expression compiler.

Tests: none, as U1.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/capture/v1`

### U3. Mirror encapsulation and the packet record

Files: `spec/proto/flowseer/net/capture/v1/mirror.proto`,
`spec/proto/flowseer/net/capture/v1/packet_record.proto`
After: none
Change: `MirrorEncapsulation` names the wrappers the receiver terminates
(`MIRROR_ENCAPSULATION_UNSPECIFIED`, `_ERSPAN_TYPE_I`, `_ERSPAN_TYPE_II`,
`_ERSPAN_TYPE_III`, `_GRE`, `_VXLAN`, `_TZSP`) for use where a session declares
what it accepts. Every value carries the enum-name prefix, as the conventions
require and `MINIMAL` lint does not check.

`MirrorEnvelope` carries the outer delivery — `IpAddress source`,
`IpAddress destination` — plus a required `oneof wrapper` over
`ErspanTypeIFields`, `ErspanTypeIiFields`, `ErspanTypeIiiFields`, `GreFields`,
`VxlanFields`, and `TzspFields`. Each variant carries only what its own header
defines, with the bit widths in the field comments and the source cited in the
file comment: `draft-foschiano-erspan-03` for ERSPAN, noting it is expired and
Informational; RFC 2784 and RFC 2890 for GRE; RFC 7348 for VXLAN; the TZSP
description for TZSP.

Field placement follows the draft's headers rather than intuition, because the
two ERSPAN types divide their fields differently:

- `ErspanTypeIFields` is an empty marker message. Type I is GRE with protocol
  type 0x88BE and no ERSPAN header at all, so there is nothing to carry; the
  message comment says that, and `loopback_interface.proto` is the precedent for
  an arm that exists to be named.
- `ErspanTypeIiFields` carries `session_id` (10 bits, `lte: 1023`), `vlan`
  (`vlan_tag_vid` rule), `cos` (`vlan_pcp` rule), `encapsulation_type` (the
  draft's 2-bit `En` field, as its own enum over untagged, ISL, 802.1Q, and
  preserved), `truncated` (the `T` bit), and `index` (20 bits). Type II has no
  direction bit; do not add one.
- `ErspanTypeIiiFields` carries `session_id`, `vlan`, `cos`, `truncated`,
  `timestamp`, `timestamp_granularity` as its own enum over the draft's four
  `Gra` codes (100 microseconds, 100 nanoseconds, IEEE 1588, platform-defined),
  `security_group_tag`, `direction` (the `D` bit, which lives here and only
  here), `hardware_id`, `frame_type` (the 5-bit `FT`), `ethernet_frame` (the `P`
  bit), and `bad_frame` as an enum over the four `BSO` codes. The optional
  platform subheader is dropped, and the file comment says so: its layout varies
  by `Platf ID` and no receiver we plan to write interprets it.

`GreFields` carries the base header's `protocol_type` (16 bits, so a plain GRE
wrapper records what it said it carried) and the two optional words RFC 2890
adds: `key` (32 bits) and `sequence_number` (32 bits), each present exactly when
the outer header's `K` or `S` bit was set. The checksum RFC 2784 defines is
verified and discarded rather than carried; a receiver that kept it would invite
a reader to re-check it against an inner frame the sender never covered.

`VxlanFields` carries `vni` (24 bits, `lte: 16777215`). `TzspFields` carries
`encapsulated_protocol`, the header's 16-bit encapsulated-protocol value, of
which the receiver accepts only Ethernet (1).

`PacketRecord` carries `sequence` (dense from zero within a session),
`captured_at`, `original_length`, `bytes data` with
`(buf.validate.field).bytes.max_len = 65535`, and an optional
`MirrorEnvelope mirror` that is present exactly when the packet arrived wrapped.
A `packet_record.length_consistent` CEL rule requires `original_length >=
size(data)`, which is the truncation invariant every reader depends on.
`captured_at` is the named exception to the rule that a Primitive carries no
observation time; the capture direction record states why, and U4's README
repeats it where a reader of the package will find it.

Tests: none, as U1.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/capture/v1`

### U4. Capture values README and the net/capture tree entry

Files: `spec/proto/flowseer/net/capture/v1/README.md`,
`docs/architecture/2026-08-20-network-model-structure-direction.md`,
`test/conformance/proto/layering_test.go`
After: U1, U2, U3
Change: the package README says what the package holds and, in the manner of
`net/packet/v1`'s README, what it deliberately does not: no session identity, no
provenance beyond the packet's own timestamp, no dissected protocol fields, and
no general packet matcher, since `CaptureFilter` is scoped by its compile
contract. It states the `captured_at` exception and the reason for it.

The network model structure record gains `capture/v1` in its package tree with a
one-line description and `{net/addr, net/packet, net/switching} ← net/capture` in
its import order, plus a dated entry under `## Amendments` recording why the
package appeared. The record's import order is prose; `importOrder` in
`test/conformance/proto/layering_test.go` is where it executes, and a package
missing from that table fails `TestNetImportOrderCoversEveryPackage`, so
`net/capture` is declared there in the same unit. `api/capture` needs no entry:
the table governs `flowseer/net` alone. Only the `net/capture` half lands here; the `api/capture` lines
land in U6 with the package they describe, so the record never names a package
the tree does not have.

Tests: none.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/capture/v1/README.md docs/architecture/2026-08-20-network-model-structure-direction.md`

### U5. CaptureSession entity family

Files: `spec/proto/flowseer/api/capture/v1/capture_session.proto`, `CONCEPTS.md`
After: U1, U2, U3
Change: the family lands whole. `CaptureSessionLocalRef` carries a UUID `id`;
`CaptureSessionGlobalRef` carries the owning `EdgeGlobalRef` and the local ref,
because the edge that runs a capture is its one owning parent.

`CaptureSource` is a required `oneof` over `LocalInterfaceSource` (the interface
name as the host spells it, and a `promiscuous` flag) and `MirrorReceiverSource`
(the encapsulations to accept, the UDP port for the ones that need one, and the
optional bind interface). `CaptureBudget` carries `max_packets`, `max_bytes`,
`max_duration`, and `snap_length`, with a `capture_budget.bounded` rule requiring
at least one of the first three and a comment recording why the default snap
length is 128 octets: two VLAN tags, an IPv6 header, and a TCP header with
options fit, and the payload does not.

`CaptureAuthorization` records the operator who asked as a plain string, the
reason text, and whether full payload was deliberately requested, so a
headers-only capture and a payload capture are distinguishable in the record
rather than only in the budget. The string is provisional; see Open questions.

`CaptureLifecycle` (`CAPTURE_LIFECYCLE_UNSPECIFIED`, `_PENDING`, `_RUNNING`,
`_COMPLETED`, `_FAILED`, `_CANCELLED`) and `CaptureStopReason`
(`CAPTURE_STOP_REASON_UNSPECIFIED`, `_PACKET_COUNT`, `_BYTE_COUNT`, `_DURATION`,
`_OPERATOR`, `_ERROR`) live beside the entity, both FlowSeer-normalized
taxonomies with a real unspecified zero. `CaptureArtifact` describes the stored
pcapng: `byte_size`, `packet_count`, a SHA-256 `digest`, `LinkType`, and
`expires_at`.

`CaptureSessionConfig` holds ref, name, description, source, filter, budget, and
authorization. `CaptureSessionState` holds ref, lifecycle, stop reason,
`started_at`, `ended_at`, `CaptureCounters`, `LinkType`, and the artifact, with
CEL rules `capture_session_state.artifact_terminal` and
`capture_session_state.completed_has_reason`. `CaptureSessionEvent` holds ref,
`from`, and `to`, refusing a transition that changes nothing.

`CONCEPTS.md` gains the CaptureSession entry beside the other entities, with its
lifecycle named, since it is now part of the shared vocabulary.

Tests: none.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api/capture/v1 CONCEPTS.md`

### U6. Capture chunk, services, and the api/capture tree entry

Files: `spec/proto/flowseer/api/capture/v1/capture_chunk.proto`,
`spec/proto/flowseer/api/capture/v1/capture_service.proto`,
`spec/proto/flowseer/api/capture/v1/capture_edge_service.proto`,
`spec/proto/flowseer/api/capture/v1/README.md`,
`docs/architecture/2026-08-20-network-model-structure-direction.md`
After: U1, U3, U4, U5
Change: `capture_chunk.proto` holds the frames both directions share, in their own
file so neither service imports the other. `CapturePacketChunk` carries the
session ref, `first_sequence`, `repeated PacketRecord packets` bounded at
`max_items: 4096`, a `CaptureCounters counters` snapshot, and a `final` flag.
`CaptureArtifactChunk` carries `offset`, `bytes data` with
`(buf.validate.field).bytes.max_len = 1048576`, and a `final` flag, so a
multi-megabyte pcapng moves in bounded pieces the way the streaming direction
requires of every record-carrying frame.

`CaptureService` is operator-facing: `CreateCaptureSession`,
`StopCaptureSession`, `GetCaptureSession`, `ListCaptureSessions` (page size and
token, as `EdgeAdminService` lists edges), `DeleteCaptureSession`,
`TailCaptureSession`, and `DownloadCaptureSession`. Every method takes its own
`<Rpc>Request` and returns its own `<Rpc>Response`, streaming or not, as
`docs/code-style-proto.md` requires: `TailCaptureSessionResponse` wraps a
`CapturePacketChunk chunk` and `DownloadCaptureSessionResponse` wraps a
`CaptureArtifactChunk chunk`. The chunk stays shared as a field type, which is
what the streaming direction asks for, without welding the two services'
signatures together.

`CaptureEdgeService` is edge-facing: `UploadCapture(stream UploadCaptureRequest)
returns (UploadCaptureResponse)`, where `UploadCaptureRequest` is a required
`oneof` over `CapturePacketChunk chunk` and `SignedEdgeAssertion assertion`. The
`oneof` is what makes the re-assertion rule expressible on the stream, and the
README states the interval contract and what the server does when it lapses.

The package README explains the split from `net/capture/v1`, why the session's
owning parent is the edge, and the open question about how a capture command
reaches an edge, so a reader of the schema alone is not misled into thinking the
command path is settled.

The network model structure record gains `api/capture/v1` in its package tree and
`{net/capture, api/edge} ← api/capture` in its import order, with its own dated
`## Amendments` entry. U4 edits the same package tree, the same import order, and
the same `## Amendments` heading for the `net/capture` half, which is why this
unit waits on it: the two amendments serialize on one file rather than racing. The `net/capture ← api/capture` edge is the one that
carries the weight: state holds the counters and the link type, and every chunk
holds records.

Tests: none.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api/capture/v1 docs/architecture/2026-08-20-network-model-structure-direction.md`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto docs CONCEPTS.md generated
```

The verifier runs `buf format`, `buf lint` with `MINIMAL` and `PROTOVALIDATE`,
the triad and ref sync hooks, and `buf generate`. `PROTOVALIDATE` compiles every
CEL expression, so a rule that cannot be written fails at lint time rather than
passing everything at runtime, and none of these units may reach for a
suppression to get past that. `buf breaking` is not a gate here: `buf.yaml`
ignores the whole `spec/proto/flowseer` module until the first stable release.

`buf generate` must run unsandboxed; the memory of `verify --full` environment
facts records why.

## Definition of done

- [ ] Verifier green for `spec/proto/`, `docs/`, `CONCEPTS.md`, and `generated/`.
- [ ] Both new package READMEs written.
- [ ] The network model structure record's tree and import order name each
      package, each amended in the unit that adds it, each with a dated
      `## Amendments` entry.
- [ ] `CONCEPTS.md` carries the CaptureSession entry.
- [ ] `generated/` regenerated by `buf generate` in the same commit, never edited
      by hand.
- [ ] This plan's `status` set with an outcome note under its title, and the
      parent's `Landed:` line for U1 filled.
- [ ] No plan labels in the schema or its comments.

## Open questions

- What type names the operator in `CaptureAuthorization`? There is no operator or
  user entity in `spec/proto/flowseer/`, and the conventions forbid naming one
  through a generic entity ref before its store lands. This plan takes a plain
  string; if the parallel host work introduces an operator identity, the field
  becomes a ref and the change is a breaking one FlowSeer welcomes.
- Does `UploadCaptureRequest.assertion` reuse the header verifier verbatim, and
  does the nonce replay window apply per stream or per edge? The opening
  assertion travels in an `Authorization` header rather than a message field, so
  the re-assertion frame is a new carrier for an existing check. The first host
  plan owns the answer; the streaming direction only requires that the two be
  verified under one rule.
- Should `CaptureSessionConfig` name the interface as a plain string, or wait for
  the Interface entity package the network model record leaves deliberately
  undecided? This plan takes the string. The rule that a value names a peer by
  key governs `net/` primitives rather than an entity package, so it is a
  precedent here and not a mandate; what settles it is that the record calls an
  eventual `InterfaceRef` an entity-package concern that nothing has yet built,
  so there is no ref to take instead.
- Does `DownloadCaptureSession` belong on `CaptureService` at all, given that the
  artifact is on the edge in the first cut? Modelled now so the contract does not
  change when central gains a store, but it has no implementation until then.
