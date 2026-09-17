# Packet Capture Primitives

The `flowseer.net.capture.v1` package defines the ref-free values a packet
capture produces and the values it selects on: `LinkType`, `CaptureCounters`,
`CaptureFilter` (with `CaptureFilterClause` and `VlanMatch`), the mirror
encapsulation metadata (`MirrorEncapsulation`, `MirrorEnvelope`, and its six
wrapper arms for ERSPAN Type I, II, and III, GRE, VXLAN, and TZSP), and
`PacketRecord`. None of them carry a session ref, a device ref, or a
lifecycle; the session that owns a capture is an entity outside this package.

## Boundaries

Imports: net/addr, net/packet, net/switching

Imported by: api/capture, model/capture

Deliberately absent:

- Session identity, device refs, and lifecycle. The CaptureSession entity lives
  in `model/capture/v1`.
- An arbitrary expression grammar for filters. `CaptureFilter` compiles to
  linear cBPF instructions.
- Dissected protocol fields beyond packet headers.

The package deliberately does not hold a session identity, provenance beyond
the packet's own timestamp, dissected protocol fields, or a general packet
matcher. `CaptureFilter` is scoped by its own compile contract instead of a
general expression grammar. Every `CaptureFilterClause` combines its
populated fields with AND and compiles to one linear cBPF instruction
sequence; a filter's clauses are evaluated as alternatives, combined with OR.
Nothing in the package may need an expression compiler to evaluate — that is
what keeps a filter running on the capture path rather than falling back to a
userspace loop. An absent `CaptureFilter` and an empty one mean the same
thing: accept every packet.

`PacketRecord.captured_at` is the one exception to the rule that a Primitive
holds no observation time. A packet's timestamp is part of what was captured
rather than a fact about the capturing: a pcapng Enhanced Packet Block cannot
be written without it, and a batch of packets sharing one envelope timestamp
would lose the inter-packet timing that is the reason to look at a capture.

## Sources

- The pcap [LINKTYPE registry](https://www.tcpdump.org/linktypes.html) for
  `LinkType`.
- [`draft-foschiano-erspan-03`](https://datatracker.ietf.org/doc/draft-foschiano-erspan/),
  an expired, Informational individual submission, for the ERSPAN Type I, II,
  and III wrappers.
- [RFC 2784](https://www.rfc-editor.org/rfc/rfc2784.html) and
  [RFC 2890](https://www.rfc-editor.org/rfc/rfc2890.html) for the GRE
  wrapper.
- [RFC 7348](https://www.rfc-editor.org/rfc/rfc7348.html) for the VXLAN
  wrapper.
- The [TZSP header description](https://www.wireshark.org/docs/dfref/t/tzsp.html)
  for the TZSP wrapper.
