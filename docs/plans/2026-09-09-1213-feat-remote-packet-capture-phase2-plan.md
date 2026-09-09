---
title: Remote Packet Capture Phase 2, Capture Engine - Plan
type: feat
date: 2026-09-09
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-09-1213-feat-remote-packet-capture-plan.md
---

# Remote Packet Capture Phase 2, Capture Engine - Plan

## Goal

`src/modules/capture/` turns a capture source, filter, and budget into a
stream of packet batches and a pcapng artifact: it opens a capture source,
pushes a compiled filter into the kernel or the byte stream, terminates
mirrored traffic, stops at its budget, and accounts for every packet it did
not deliver. The module is a library at this stage, with no `service.Module`
wiring, following the precedent `src/modules/localnet` set — the leaf's shape
follows the host's supervision tree, and the host is landing in a later
phase. This phase is wrong if the filter grammar turns out not to compile to
a linear cBPF program, because then the filter has to move into userspace
and the whole performance argument changes; it does compile, and the
decision below shows why.

## Decisions

The parent plan's Decisions apply. Specific to the engine, carried from the
prior draft and confirmed against the landed schema:

- Capture through `golang.org/x/sys/unix` on AF_PACKET with a classic BPF
  filter attached via `SO_ATTACH_FILTER`, reading with `recvfrom` rather than
  a mmapped TPACKET ring in the first cut. Why: gopacket is barred outside
  netpen by `src/common/internal/netpenguard`, confirmed by
  `no_heavy_deps_test.go`, which flags any `gopacket` import outside
  `src/edge/netpen/`; a diagnostic capture bounded by an explicit budget is
  not a line-rate workload. The ring is an optimization to reach for when
  `dropped_by_interface` says it is needed.
- Compile `CaptureFilter` to `[]bpf.Instruction` from `golang.org/x/net/bpf`
  and assemble with its `Assemble`, the same pair
  `src/edge/netpen/link/bpf.go` already uses, re-implemented here rather than
  imported: `link.Assemble` lives in a package that also imports gopacket
  transitively through `link_linux.go`, and importing that package from
  `src/modules/capture` would pull the guarded dependency across the module
  boundary. `golang.org/x/net/bpf` is already an indirect dependency of the
  root module (`go.mod`); this phase makes it direct.
- Every `CaptureFilterClause` compiles to one AND-chain that falls through to
  the next clause's chain on a mismatch and jumps to accept on a full match;
  clauses tried in order, no match after the last one rejects. A clause
  reading past a possible 802.1Q tag (any field other than `ether_type` or
  `vlan` itself) branches into a tagged and an untagged instruction sequence
  rather than sharing one dynamically computed offset across the whole
  program: `golang.org/x/net/bpf`'s IHL-computing instruction
  (`LoadMemShift`) takes a constant base offset, so combining it with a
  runtime VLAN shift needs its own `LoadIndirect`-plus-mask-and-shift
  sequence rather than reuse, and duplicating the tagged/untagged pair per
  field keeps that reuse unnecessary. A clause naming an IPv4 or IPv6
  address (`src_prefix`, `dst_prefix`) skips generating the other address
  family's sub-chain, since the address value itself fixes the family; a
  clause with no address field but an IP-dependent field (`ip_protocol`,
  either port, `tcp_flags`, `icmp`, `dscp` — R2's own acceptance example,
  `{ip_protocol: TCP, dst_port: {exact: 22}}`, is exactly this case) does not
  know the family in advance and emits both forms, one gated on EtherType
  0x0800 and one on 0x86DD. Every branch stays forward-only and every
  duplication is bounded (at most tagged/untagged times IPv4/IPv6, never
  compounding across fields), so the program stays linear even though it is
  not as compact as a naive per-field instruction count would suggest.
  `CaptureFilter.any_of` is capped at 32 clauses and `CaptureFilterClause`
  has 12 matchable fields
  (`spec/proto/flowseer/net/capture/v1/capture_filter.proto:39-62`); the
  compiler counts emitted instructions and returns an error above the
  classic BPF instruction ceiling instead of silently truncating a program
  the kernel would then reject piecemeal.
- IPv6 extension headers between the fixed 40-byte header and the transport
  header are not walked. A clause naming a transport-port, TCP-flags, or
  ICMP field assumes the fixed header is immediately followed by the
  transport header for an IPv6 address family match, which is the same
  assumption tcpdump's own BPF compiler makes and is a stated limitation of
  a *linear* program, not a reason to move the filter into userspace.
- Deliver through `pump.Pump[Batch]` with `TrySendDropOldest`
  (`src/common/pump/pump.go:175`), where `Batch` is a package-local type
  (`FirstSequence uint64`, `Records []*capturev1.PacketRecord`,
  `Counters *capturev1.CaptureCounters`, `Final bool`) mirroring
  `CapturePacketChunk` minus its `session` ref. Why: the streaming frame
  transport direction requires a producer that never blocks on its consumer
  and always reports what it dropped, which is exactly that method's
  contract; the ref is host-owned identity a library has no business
  minting, so the engine hands the host everything a `CapturePacketChunk`
  needs except the ref it does not have.
- `TrySendDropOldest` reports how many *items* it dropped, never which one,
  and reports it at the moment of the send that triggered the drop — before
  that send's own `Batch.Counters` were built, so the drop cannot be
  attributed to the batch being sent. The engine is the pump's only
  producer, so it keeps its own FIFO of the record counts of batches it
  believes are still buffered, mirroring the pump's occupancy exactly
  (append on every successful send, pop-then-append on a `dropped=1` send,
  pop the front twice — once for the evicted item, once treating `v` itself
  as never enqueued — on `dropped=1` with `delivered=false`, and so on for
  every case `TrySendDropOldest`'s doc comment enumerates). Whatever record
  count that bookkeeping attributes to a drop accumulates into a pending
  counter and is added to `dropped_by_transport` on the *next* batch the
  engine constructs, never the one mid-send, which is what the streaming
  frame transport direction means by "a stream that dropped says so in the
  counters of the next chunk it sends". Sequence numbers themselves need no
  such bookkeeping: they are assigned once per record at construction,
  before delivery is attempted, so a batch that never reaches a consumer
  still leaves its sequence range visible as a gap.
- Write the pcapng artifact ourselves rather than shelling out, as a
  streaming `io.Writer`-based encoder (Section Header, one Interface
  Description, one Enhanced Packet Block per record, one Interface
  Statistics Block on close) rather than a batch renderer. Why: the blocks
  the artifact needs are small enough that hand-writing them is smaller than
  a dependency, and a streaming writer lets a caller pipe the same records it
  drains from the pump straight to disk without buffering the whole capture
  in memory first.
- Test the decapsulator against bytes we did not write, where such bytes
  exist. **Correction, made during implementation**: the parent plan's
  Verification section and this phase's prior draft both asserted that the
  Wireshark sample captures wiki's `cisco-nexus92-erspan-marker.pcap` and
  `cisco-nexus10-erspan-marker.pcap` prove the ERSPAN Type III header parse.
  They do not. Byte-level inspection, cross-checked against Wireshark's own
  `packet-cisco-marker.c` dissector (a "CISCO ERSPAN3 Marker Packet"
  dissector distinct from `packet-cisco-erspan.c`) and Cisco's Nexus 9000
  documentation, shows both files carry a UDP/8880, Cisco-proprietary
  timestamp-synchronization packet — sent once a second to correlate an
  ASIC-relative timestamp with UTC — with its own field set (Version, Type,
  SSID, Granularity, UTC offset, a 48-bit ASIC timestamp, UTC seconds and
  microseconds, a sequence number, and an `0xA5A5A5A5` tail signature) that
  shares no field with `ErspanTypeIiiFields`. It is not GRE-encapsulated and
  is not an ERSPAN-wrapped mirrored frame at all; feeding its bytes to
  `ParseErspanTypeIII` produces field values with no relationship to
  anything the header actually carries. The Wireshark sample-captures page
  has no other ERSPAN-related file, and a wider search was judged not worth
  the phase. The correction was confirmed independently and accepted before
  this unit landed; the parent plan's own Verification section is being
  corrected separately and is not amended here. Every mirror encapsulation,
  including ERSPAN Type III, is instead proved against a hand-built fixture
  to the same rigor, and a containerised Linux `erspan` tunnel and an Open
  vSwitch `type=erspan` port carry the independent-encoder round-trip tests
  for Type I, II, and III and plain GRE. Why a fixture is weaker evidence
  than golden bytes, stated plainly: a decoder tested only against its own
  encoder, or against a fixture built from the same header layout the
  decoder codes to, proves the pair agree with each other and nothing about
  what a shipping ASIC actually emits. That gap is not closed by this
  phase; it is named again in Verification below. ERSPAN Type III's own P
  bit (`ethernet_frame` in `ErspanTypeIiiFields`) still governs whether a
  Type III packet — marker or otherwise — yields an inner frame: false means
  a decoded `MirrorEnvelope` and no `PacketRecord`, since the record's `data`
  field is required and there is no frame to put there.

New decisions, answering the three open questions the prior draft left, and
the design work needed to act on them:

- **The mirror receiver binds dedicated sockets; it does not reuse the
  AF_PACKET source.** `MirrorReceiverSource` (`capture_session.proto`)
  carries `bind_interface` alongside `udp_port`, distinct fields from
  `LocalInterfaceSource.interface_name` — the schema already treats "receive
  traffic addressed to me" as a different capability from "tap everything on
  this wire". A receiver opens one `AF_INET`/`AF_INET6` `SOCK_RAW` socket
  with protocol `IPPROTO_GRE` for the four GRE-family encapsulations
  (`erspan_type_i/ii/iii`, `gre`) and one `SOCK_DGRAM` socket bound to
  `udp_port` for the UDP-family ones (`vxlan`, `tzsp`), each optionally bound
  to `bind_interface` via `SO_BINDTODEVICE`. The reason is that AF_PACKET
  taps L2 frames regardless of destination and would force the receiver to
  reimplement "is this addressed to me" itself, work the kernel's own IP
  delivery already does for free. Both sockets use `recvmsg` with
  `IP_PKTINFO`/`IPV6_RECVPKTINFO` enabled to learn the destination address
  and `recvfrom`'s peer address for the source, rather than reading the
  outer IP header out of the payload: `raw(7)`'s "the IP header is included
  in the received data" holds for an `AF_INET` `SOCK_RAW` socket but not for
  `AF_INET6`, which strips it, so a mixed IPv4/IPv6 receiver needs one
  address-recovery path that works for both, and the UDP path needs
  `IP_PKTINFO` regardless since a `SOCK_DGRAM` socket never receives the IP
  header. `mirror.Decode` (U2) therefore takes the source and destination
  addresses as explicit parameters rather than assuming they are the first
  bytes of its input; the caller (this decision) is what resolves them.
- **The compiled filter runs in two places, not one.** `LocalInterfaceSource`
  attaches the compiled program to the kernel via `SO_ATTACH_FILTER`, exactly
  as the existing decision states, because the frame the kernel hands back is
  already the frame the filter describes. A `MirrorReceiverSource` frame is
  not: the byte the filter's field offsets assume start after decapsulation,
  and decapsulation is envelope-dependent (GRE header length varies with the
  key and sequence-number bits, ERSPAN Type II and III headers differ, VXLAN
  and TZSP headers differ again), so there is no single kernel-attachable
  program that reads all of them. The engine decapsulates in Go, then runs
  the *same compiled program* against the inner frame through
  `golang.org/x/net/bpf`'s own VM (`bpf.NewVM(insts).Run(data)`), which is a
  pure-Go interpreter of the identical instruction stream `SO_ATTACH_FILTER`
  would have loaded. One compiler, two execution engines, chosen by source
  kind. This does not touch the phase's stop condition — the filter still
  compiles to one linear program per session — and a mirror receiver's
  traffic volume is what one device chose to send it, not a promiscuous tap,
  so a userspace VM pass over already-decapsulated frames is not the
  line-rate workload the kernel-attach decision was protecting against.
  Multiple UDP-family encapsulations sharing one `udp_port` (an operator
  configuring both `vxlan` and `tzsp` on one receiver) are tried in a fixed
  order (VXLAN's header first, TZSP's second) and the first one whose header
  validates structurally is accepted; ordinary configuration names one
  UDP-family encapsulation per receiver, and this rule exists only for the
  case where it does not.
- **A GRE-family wrapper around a non-Ethernet payload decodes to an
  envelope with no inner frame, the same treatment a marker gets.** Plain
  GRE and ERSPAN Type I carry whatever `GreFields.protocol_type` names, not
  necessarily Ethernet (`0x6558`); ERSPAN Type II and III have no equivalent
  field but Type III's `ethernet_frame` bit already says whether one is
  present. `mirror.Decode` checks the appropriate signal per arm and returns
  a nil inner frame whenever it is not Ethernet, rather than compiling the
  filter's Ethernet-offset assumptions or the engine's `LINK_TYPE_ETHERNET`
  report against bytes that are not an Ethernet frame. A receiver that only
  ever reports Ethernet is honest about what it captured only if it refuses
  to report anything else.
- **The engine hands the host bytes and records; it does not own a file.**
  The pcapng writer is `func(w io.Writer, ...)`, not a file path or a
  `*CaptureArtifact` builder. Why: `src/services/` holds only a README and
  the host's storage configuration does not exist yet (that is exactly why
  U3 is blocked), so nothing in this phase can decide a path, retention, or
  the `CaptureArtifact.digest`/`expires_at` bookkeeping the schema defines.
  What phase 2 can and does finish is the rendering: a caller (a test today,
  the host in phase 3) drains `Batch` values from the pump and hands each
  record to the writer, and the writer produces bytes Wireshark reads
  regardless of where they land.
- **The module stays in `src/modules/capture/`.** Both the parent plan's
  Decisions and the remote packet capture direction record already commit to
  this location, on the two-host admission rule in `src/modules/README.md`:
  the edge agent and a future central collector both terminate mirrored
  traffic, the same argument that admitted `localnet` before either of its
  hosts existed as `service.Module` wiring. Revisiting it here would be
  re-litigating an accepted decision without new evidence; the "open
  question" was hedging language for a decision two documents up the chain
  already resolved.
- **The engine reports `LINK_TYPE_ETHERNET` only, for both source kinds.**
  `LinkType`'s comment
  (`spec/proto/flowseer/net/capture/v1/link_type.proto`) already states that
  restricting to Ethernet and SLL2 "is an engine limit, not a schema one".
  Determining SLL2 (a non-Ethernet interface, reported via the AF_PACKET
  socket address's `sll_hatype`) is deferred: every source this phase
  targets is an Ethernet-framed tap or a decapsulated inner Ethernet frame,
  and adding SLL2 detection now would be branching on a case this phase has
  no way to test.
- **Package names for the two `capturev1` bindings.** `net/capture/v1` and
  `api/capture/v1` both generate a Go package named `capturev1`. Every file
  that needs both imports `net/capture/v1` under its bare generated name
  (`capturev1`, the value types used throughout every unit) and
  `api/capture/v1` as `apicapturev1` (`CaptureSource`, `CaptureBudget`, used
  only where the engine reads its own configuration). Stated once here so
  every unit uses the same alias rather than each file reinventing one.
- **`Engine`'s input is `CaptureSource` and `CaptureBudget`, not the whole
  `CaptureSessionConfig`.** The config message also carries `name`,
  `description`, and `authorization` — session bookkeeping the engine never
  reads and has no reason to require a test construct. `net/capture/v1`'s
  own README draws this same line for the schema ("the session that owns a
  capture is an entity outside this package"); the engine's constructor
  draws it for Go the same way.

## Requirements

Carried from the parent, restated against this phase's actual surface. R1
(budget-bounded config) and R7 (re-assertion) are protovalidate and RPC
concerns the phase 1 schema and a later host already own; they are not
re-derived here.

1. (R2) A capture filter compiles to a cBPF program the kernel accepts, and
   the program accepts exactly the packets the filter describes. Acceptance:
   the filter `any_of: [{ip_protocol: TCP, dst_port: {exact: 22}}, {ether_type:
   ARP}]` compiles to a program that, run through `bpf.NewVM(insts).Run(data)`,
   accepts a TCP segment to port 22 and an ARP frame and rejects a UDP
   datagram to port 22; the same program installed via `SO_ATTACH_FILTER` on
   a real `AF_PACKET` socket does not fail to load (the `linux`-and-tag-gated
   real-socket test).
2. (R3) The decapsulating receiver unwraps a mirrored frame and records the
   outer wrapper as metadata. Acceptance: an ERSPAN Type II packet (GRE
   protocol 0x88BE, ERSPAN version 0x1, session id 7, truncation bit set)
   built as a fixture yields a `MirrorEnvelope` whose `erspan_type_ii` carries
   `session_id: 7` and `truncated: true`, and a `PacketRecord.data` equal to
   the inner Ethernet frame.
3. (R4) A capture stops at its first satisfied budget and reports why.
   Acceptance: a run with `max_packets: 100` against a fake source that never
   stops on its own ends at 100 records with the last `Batch.Final == true`
   and a stop reason of `CAPTURE_STOP_REASON_PACKET_COUNT` reported through
   the engine's public state, and no 101st record appears in any batch.
4. (R5) Loss is always attributable. Acceptance: a fake source delivers
   records faster than a test consumer drains the pump, forcing
   `TrySendDropOldest` to evict a buffered batch; the consumer sees a
   `FirstSequence` jump past the end of the last batch it actually received,
   and the batch immediately following the eviction (not the one mid-send
   when the eviction happened) carries a `Counters.dropped_by_transport`
   equal to the jump size.
5. (R6) The stored artifact is a pcapng file Wireshark reads without
   complaint. Acceptance: writing 100 synthetic `PacketRecord` values through
   `pcapng.Writer` and closing it with a final `CaptureCounters` produces a
   file whose Section Header, Interface Description, 100 Enhanced Packet
   Blocks, and Interface Statistics Block round-trip through a byte-level
   structural check in the package's own test; `capinfos` or `tshark`
   confirms the same file if either tool is available in the environment
   that runs the check (recorded as a manual verification step, not a gate,
   since neither is a repository dependency).
6. (R8) Captured bytes never reach telemetry. Acceptance: this phase adds no
   log, span, or metric call that takes a `PacketRecord`, `[]byte` frame, or
   `Batch` as an argument; `grep -rn` for `.Attr(` and `slog\.` calls in
   `src/modules/capture/` (excluding tests) finds none that reference packet
   data. The engine has no telemetry to begin with — instrumentation is
   phase 3's job once a host exists to configure it — so this requirement is
   met by absence, not by a redaction mechanism.

## Out of scope

- `service.Module` wiring, gates, and config messages. Those land with the
  host in phase 3.
- Non-Linux capture. The source is build-tagged like `src/edge/netpen/link`;
  every other platform returns an unsupported-platform error from both the
  local-interface and mirror-receiver constructors.
- Wireless link types and radiotap.
- `LINK_TYPE_LINUX_SLL2` detection.
- Recording a non-Ethernet mirrored payload (a GRE or ERSPAN Type I tunnel
  whose `protocol_type` names something other than Ethernet). The receiver
  counts and decodes the envelope; it produces no `PacketRecord` for it,
  matching the ERSPAN-marker treatment.
- Artifact file lifecycle: path, retention, `CaptureArtifact.digest` and
  `expires_at` bookkeeping, and multi-writer fan-out (the "same bytes on the
  wire and on disk" claim from the streaming direction record). The engine
  proves the pcapng bytes are correct; where they are written and for how
  long they live is host territory.
- Lab-hardware validation (the ICX7150 local SPAN and the MikroTik TZSP
  stream from the parent plan's Verification section). That needs the
  switches powered on and is explicitly phase 3's to run.
- A mmapped TPACKET ring. The first cut reads with `recvfrom`; the ring stays
  an option for later if `dropped_by_interface` says it is needed.

## Units

### U1. BPF filter compiler

Files: `src/modules/capture/filter/compile.go`,
`src/modules/capture/filter/doc.go`
After: none
Change: `filter.Compile(f *capturev1.CaptureFilter) ([]bpf.Instruction, error)`
turns a `CaptureFilter` into an assembled linear cBPF program per the
AND-chain / OR-of-clauses, tagged/untagged, and address-family design in
Decisions. An empty or absent
filter compiles to an accept-everything program (matching the schema's stated
semantics). `filter.Assemble` (the `bpf.Assemble` wrapper) and a re-exported
`RawInstruction` give callers the raw form for `SO_ATTACH_FILTER` without
importing `golang.org/x/net/bpf` themselves everywhere.
Tests: table-driven cases through `bpf.NewVM(insts).Run(data)` against
hand-built Ethernet frames — the R2 acceptance example (TCP:22 accept, ARP
accept, UDP:22 reject), a VLAN-tagged frame matched by `vlan.vlan_id`, an
IPv6 frame matched by `dst_prefix`, a 32-clause filter to exercise the
OR-chain's fallthrough, and a filter dense enough to hit the instruction-count
error path (assert the specific error, not just non-nil).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/capture/filter/compile.go src/modules/capture/filter/doc.go src/modules/capture/filter/compile_test.go`

### U2. Mirror envelope decapsulator

Files: `src/modules/capture/mirror/decode.go`,
`src/modules/capture/mirror/doc.go`,
`src/modules/capture/mirror/decode_test.go`
After: none
Change: `mirror.Decode(payload []byte, srcIP, dstIP net.IP)
(*capturev1.MirrorEnvelope, inner []byte, err error)` parses a GRE/ERSPAN-family
packet whose IP header the caller has already stripped and resolved into
`srcIP`/`dstIP` (U5 gets these from `recvfrom`'s peer address and
`IP_PKTINFO`, uniformly for `AF_INET` and `AF_INET6`, so `Decode` never
assumes an IP-header-prefixed buffer); `mirror.DecodeUDP(payload []byte,
srcIP, dstIP net.IP, candidates []capturev1.MirrorEncapsulation)
(*capturev1.MirrorEnvelope, inner []byte, err error)` does the same for a UDP
payload against VXLAN and TZSP, trying `candidates` in the fixed order the
Decisions section states. Both return a nil `inner` and no error for a
decoded envelope that carries no frame — an ERSPAN Type III marker
(`ethernet_frame == false`) or a GRE/ERSPAN Type I payload whose
`protocol_type` is not Ethernet — and the caller (U6) treats that as
"counted, no record produced". Exported per-encapsulation parse functions
(`ParseErspanTypeIII`, etc.) let tests target one header shape directly.
Tests: no golden pcap exists for this arm, per the Decisions correction
above. Hand-built fixtures cover the R3 acceptance example (ERSPAN Type II,
session 7, truncated), a marker-shaped ERSPAN Type III fixture
(`ethernet_frame` false, asserting `inner == nil`), and one case each for
ERSPAN Type I, plain GRE, VXLAN, and TZSP, including a two-candidate
`DecodeUDP` call to prove the fixed-order demultiplex.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/capture/mirror/decode.go src/modules/capture/mirror/doc.go src/modules/capture/mirror/decode_test.go`

### U3. pcapng artifact writer

Files: `src/modules/capture/pcapng/writer.go`,
`src/modules/capture/pcapng/doc.go`
After: none
Change: `pcapng.NewWriter(w io.Writer, linkType capturev1.LinkType, snapLen
uint32) (*Writer, error)` writes the Section Header and one Interface
Description Block immediately; `(*Writer).WriteRecord(*capturev1.PacketRecord)
error` appends one Enhanced Packet Block per call; `(*Writer).Close(counters
*capturev1.CaptureCounters) error` appends one Interface Statistics Block and
flushes. Every block follows `draft-ietf-opsawg-pcapng-05`'s generic block
structure (type, length, body, repeated length trailer).
Tests: a structural test that writes a fixed set of records through a
`bytes.Buffer` and walks the emitted blocks by hand (block type and length
fields, EPB count matching records written, ISB counters matching what was
passed to `Close`), satisfying the R6 acceptance example's 100-record case.
A short manual-verification note (not a test) records how to check the same
output against `capinfos`/`tshark` if installed.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/capture/pcapng/writer.go src/modules/capture/pcapng/doc.go src/modules/capture/pcapng/writer_test.go`

### U4. Local-interface AF_PACKET source

Files: `src/modules/capture/rawsocket/rawsocket.go`,
`src/modules/capture/rawsocket/local_linux.go`,
`src/modules/capture/rawsocket/local_other.go`,
`src/modules/capture/rawsocket/doc.go`
After: U1
Change: `rawsocket.go` declares the platform-neutral pieces shared with U5 —
`ErrCodeSourceOpen`, `ErrUnsupportedPlatform`, `IsUnsupported`, and a `Frame{
Data []byte, OriginalLength uint32, Envelope *capturev1.MirrorEnvelope,
CapturedAt time.Time, Err error }` type — following
`src/edge/netpen/link/link.go`'s `Leg`/`Frame` shape, extended with
`OriginalLength` (the wire length before snap-length truncation, which
`PacketRecord.original_length`'s required, CEL-checked field needs and a
`recvfrom` into an already-bounded buffer does not give for free without
`MSG_TRUNC`; U4 requests it and reads the true length from the returned
`msg_flags`/`n`), `Envelope` (nil for a local-interface frame, set by U5 for
a mirror-receiver frame — U6 needs it to populate `PacketRecord.mirror`),
and `Err` (link.go's own `Frame.Err`, absent from the prior draft's `Frame`,
needed for U6 to reach `CAPTURE_LIFECYCLE_FAILED`/`CAPTURE_STOP_REASON_ERROR`
on a terminal receive error). `local_linux.go`
(`//go:build linux`) opens an `AF_PACKET`/`SOCK_RAW` socket via
`golang.org/x/sys/unix`, binds it to the named interface, sets promiscuous
mode when asked, installs the compiled filter with
`unix.SetsockoptSockFprog` (`SO_ATTACH_FILTER`), and reads with a
short-timeout poll loop that checks `ctx.Done()` each cycle, the same shape
`link_linux.go` uses for its afpacket wrapper. It also exposes
`Stats() (received, droppedByInterface uint64, err error)`, reading
`SOL_PACKET`/`PACKET_STATISTICS` (`tp_packets`/`tp_drops`) so U6 can populate
`CaptureCounters.dropped_by_interface`; the mirror-receiver sockets in U5
have no comparably clean per-socket kernel drop counter on Linux, so that
field stays unpopulated on the mirror-receiver path, a limitation stated
rather than left silently unexplained. `local_other.go`
(`//go:build !linux`) returns `ErrUnsupportedPlatform`.
Tests: unit tests inject a fake socket (an interface covering bind/promisc/
filter-install/read/stats/close) to drive the Receive-loop logic
(cancellation unblocks within one poll, a read error surfaces as a terminal
`Frame{Err: ...}`, `Close` is idempotent) without a real socket. An opt-in
real-interface test (`//go:build linux && capturetest`, gated on a
`FLOWSEER_CAPTURE_IFACE` environment variable naming an interface, mirroring
`link/linktest_test.go`) opens it, attaches a trivial accept-all filter, and
confirms a sent frame comes back; the default race run skips it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/capture/rawsocket/rawsocket.go src/modules/capture/rawsocket/local_linux.go src/modules/capture/rawsocket/local_other.go src/modules/capture/rawsocket/doc.go`

### U5. Mirror-receiver sockets

Files: `src/modules/capture/rawsocket/mirror_linux.go`,
`src/modules/capture/rawsocket/mirror_other.go`,
`src/modules/capture/rawsocket/test/integration/erspan_test.go`,
`src/modules/capture/rawsocket/test/integration/README.md`
After: U1, U2
Change: `mirror_linux.go` (`//go:build linux`) opens the raw
`IPPROTO_GRE` socket and, when the config names a UDP-family encapsulation,
the `SOCK_DGRAM` socket, both optionally bound to `bind_interface` via
`SO_BINDTODEVICE`, both using `recvmsg` with `IP_PKTINFO`/`IPV6_RECVPKTINFO`
enabled. Every receive resolves the source address from `recvfrom`'s peer
address and the destination address from the `IP_PKTINFO`/`IPV6_PKTINFO`
ancillary data, then hands the payload and both addresses to U2's
`Decode(payload, srcIP, dstIP)`/`DecodeUDP(payload, srcIP, dstIP,
candidates)`. The compiled filter (U1's output) is run through `bpf.NewVM`
against the inner frame before the frame is reported, per the
two-execution-engines decision; a frame the filter rejects is not reported
(and is not counted as a drop — it was never going to be accepted).
`mirror_other.go` (`//go:build !linux`) returns `ErrUnsupportedPlatform`.
Tests: unit tests for the UDP path run against a real loopback `SOCK_DGRAM`
socket (unprivileged; VXLAN and TZSP's conventional ports are both above
1024) and prove `IP_PKTINFO` recovers the destination address and the
compiled-filter-on-inner-frame path rejects a frame the filter excludes.
The raw-socket (GRE-family) path is exercised only by the fake-socket seam
in default tests, since opening it needs `CAP_NET_RAW`.
The integration suite lives behind its own explicit build tag,
`capture_mirror_integration` (`//go:build linux &&
capture_mirror_integration`), following `docs/conventions/testing.md`'s rule
that "live integration tiers require an explicit build tag" and every
existing tier's actual precedent (`snmp_integration_t1`, `netpen_t1`,
`service_otel_integration`) rather than `docs/code-style.md`'s more general
`testing.Short()` line, which no existing container- or lab-backed suite in
this repository actually uses on its own. It creates a Linux `erspan` tunnel
(`ip link add … type erspan … erspan_ver 1|2`) and an Open vSwitch
`type=erspan` port inside a scratch network namespace, sends known traffic
across each, and asserts the receiver's output matches. The `erspan_ver 0`
sub-case probes for kernel support first (Linux ≥ 4.18, the release that
added ERSPAN Type I / version 0 to `ip_gre`) and calls `t.Skip` with a clear
message when the running kernel predates it, rather than assuming a
container can select a kernel it does not share with its host — the README
documents this prerequisite and the `GOOS=linux` cross-compile check below,
following `src/edge/netpen/Taskfile.yml`'s "GOOS=linux pass covers linux-only
files the darwin pass skips" precedent, since this repository's default
development and CI checkout is not itself Linux.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/capture/rawsocket/mirror_linux.go src/modules/capture/rawsocket/mirror_other.go`, then `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./src/modules/capture/...` and `GOOS=linux golangci-lint run ./src/modules/capture/...` to cover the linux-tagged files a non-Linux checkout's default build silently skips.

### U6. Engine orchestration

Files: `src/modules/capture/capture.go`, `src/modules/capture/engine.go`,
`src/modules/capture/doc.go`, `src/modules/README.md`
After: U1, U2, U3, U4, U5
Change: `capture.go` declares the public shape: `Config{Source
*apicapturev1.CaptureSource, Filter *capturev1.CaptureFilter, Budget
*apicapturev1.CaptureBudget}`, `Batch{FirstSequence uint64, Records
[]*capturev1.PacketRecord, Counters *capturev1.CaptureCounters, Final bool}`,
`State{Lifecycle ..., StopReason ..., Counters *capturev1.CaptureCounters,
LinkType capturev1.LinkType}` (a package-local mirror of the fields of
`CaptureSessionState` the engine itself produces — again without the ref a
library does not own), and the `Source` interface U4 and U5's concrete types
satisfy structurally (`Receive(ctx) <-chan rawsocket.Frame`, `Stats()
(received, droppedByInterface uint64, err error)`, `Close() error`).
`engine.go` implements `New(Config) (*Engine, error)` (validates the config,
compiles the filter once via U1, constructs the right `rawsocket` source for
the `CaptureSource` oneof arm) and `(*Engine) Run(ctx context.Context)
(*pump.Pump[Batch], error)`, which starts one goroutine owned by the
returned pump's context: it reads frames, applies `snap_length` truncation
(default 128 octets per `CaptureBudget.snap_length`'s documented default),
builds a `PacketRecord` per frame with a dense sequence counter and, for a
mirror-receiver frame, the frame's `Envelope` copied into
`PacketRecord.mirror` (a `Decode` result with no inner frame — a marker or a
non-Ethernet payload — is counted and produces no record, per the Decisions
entries above), batches records (a size and a time-based flush, both bounded
well under `CapturePacketChunk`'s 4096-item cap so a later host wrapping a
`Batch` into that message never has to split it), maintains `CaptureCounters`
(`received` and `dropped_by_interface` polled from the source's `Stats()`,
`accepted` counted per record built, `dropped_by_budget` counted per frame
discarded after a budget bound is hit but still in flight, `dropped_by_transport`
accumulated from the pending-drop bookkeeping the pump decision above
describes and applied to the next batch), evaluates the budget after every
accepted packet, and stops with the matching `CaptureStopReason` when one
bound is hit or the source reports a terminal `Frame.Err` (stop reason
`CAPTURE_STOP_REASON_ERROR`), marking the batch that carries the last record
`Final`. `(*Engine) State() State` gives a caller the live snapshot without
draining the pump. This unit also adds `capture`'s row to
`src/modules/README.md`'s module table, per Definition of done.
Tests: a fake `Source` (a channel of `rawsocket.Frame` a test controls
directly, with a controllable `Stats()`) drives the R4 (budget stop) and R5
(attributable loss, forcing `TrySendDropOldest` to discard by not draining
the pump) acceptance examples from Requirements, plus one test wiring the
fake source's output through U3's `pcapng.Writer` end to end to prove the
whole pipeline integrates (the R6 acceptance example's 100-record case, run
through the actual `Engine` rather than the writer in isolation), and one
test whose fake source yields a mirror-sourced `Frame` carrying an `Envelope`
and confirms it lands on `PacketRecord.mirror`. A `go vet`/build-only check
(no new test) confirms no telemetry call in the package takes packet data,
per the R8 acceptance example's grep.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/capture/capture.go src/modules/capture/engine.go src/modules/capture/doc.go`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/capture/filter/compile.go src/modules/capture/filter/doc.go src/modules/capture/filter/compile_test.go src/modules/capture/mirror/decode.go src/modules/capture/mirror/doc.go src/modules/capture/mirror/decode_test.go src/modules/capture/pcapng/writer.go src/modules/capture/pcapng/doc.go src/modules/capture/pcapng/writer_test.go src/modules/capture/rawsocket/rawsocket.go src/modules/capture/rawsocket/local_linux.go src/modules/capture/rawsocket/local_other.go src/modules/capture/rawsocket/mirror_linux.go src/modules/capture/rawsocket/mirror_other.go src/modules/capture/rawsocket/doc.go src/modules/capture/capture.go src/modules/capture/engine.go src/modules/capture/doc.go src/modules/README.md --full
go test -race ./src/modules/capture/...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./src/modules/capture/...
GOOS=linux golangci-lint run ./src/modules/capture/...
go test -tags=capture_mirror_integration ./src/modules/capture/rawsocket/test/integration/...
```

The `capturetest`-tagged real-interface test (U4) and the
`capture_mirror_integration`-tagged container round-trip (U5) both need a
Linux host with `CAP_NET_RAW` and, for the latter, Docker; neither runs in
this repository's default checks or in this phase's own verification, since
the development and CI environment for this change is not Linux. Both are
cited above with their exact invocation so a Linux runner can execute them,
and the residual risk that no such run has happened yet is named in the
handoff.

Stated plainly, not softened: after the golden-pcap correction above, no
shipping-ASIC bytes exercise any mirror decapsulator in this phase. The
independent-encoder leg is the containerised Linux `erspan` tunnel and the
Open vSwitch `type=erspan` port alone (U5's integration suite), and for the
fields those two senders do not exercise — ERSPAN Type III's security group
tag and non-Ethernet frame type, per the parent plan's own Verification
section — the only check is a fixture this phase wrote against a decoder
this phase wrote, which proves the pair agree with each other and nothing
about a real ASIC's output. That gap is not closed here.

## Definition of done

- [ ] Verifier green for every changed path, including the `GOOS=linux`
      cross-compile and lint pass over the platform-tagged files.
- [ ] `src/modules/README.md` gains its `capture` row, added in the unit that
      adds the module (U6).
- [ ] No plan labels in code, comments, or commit messages.
- [ ] This plan's `status` set to `implemented` with an outcome note under
      its title once the units land; the parent plan's U2 row gets its
      `Landed:` date and this file's path.

## Open questions

None. The three the prior draft left are answered in Decisions above:
dedicated sockets for the mirror receiver, an `io.Writer`-based artifact
renderer with no file ownership, and `src/modules/capture/` confirmed as the
module's home.
