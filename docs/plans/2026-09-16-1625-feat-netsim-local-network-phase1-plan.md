---
title: Local Network Analysis Phase 1 - Transport Codecs and Multicast Conformance - Plan
type: feat
date: 2026-09-16
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
execution: code
parent: docs/plans/2026-09-16-1625-feat-netsim-local-network-plan.md
---

# Local Network Analysis Phase 1 - Transport Codecs and Multicast Conformance - Plan

> Implemented. 3 units, 2026-09-16T16:13Z to 2026-09-16T16:13Z.

## Goal

`src/common/net` gains `udp`, `tcp`, and `icmp` codecs the filter layer and
the reflector can build on, and the conformance corpus pins how a snooping
switch treats the two mDNS groups. The means: three small packages in the
shape of `igmp` and `mld`, and two troubleshooting cases in
`src/common/netsim/internal/netsimtest`. The plan is wrong if the IPv6 case
shows `ff02::fb` flooding or dropping outright, which would mean the
resolver's scope rules differ from what
`src/common/netsim/vswitch/switch.go:1115-1121` reads as today.

## Decisions

The parent's decisions on codec placement and on the flooding rules apply.
In addition:

- `udp.Encode` takes the source and destination addresses and always writes
  a checksum. Why: IPv6 forbids a zero UDP checksum (RFC 8200 section 8.1),
  and the reflector rewrites the source address, so every copy needs a
  fresh checksum anyway. A computed checksum of zero is sent as `0xffff`
  (RFC 768).
- `udp.Decode` refuses a datagram shorter than its own length field or
  shorter than 8 octets, and does not verify the checksum. Why: the filter
  and the reflector need ports, and a switch does not verify transport
  checksums. `udp.Verify` exists for tests and callers that want it.
- `tcp` and `icmp` decode only. Why: nothing in this plan originates a TCP
  segment or an ICMP message, and an encoder without a producer is untested
  surface.
- The corpus cases are single-switch troubleshooting cases in the shape of
  `troubleshooting/ssm-rejects-unjoined-source`
  (`src/common/netsim/internal/netsimtest/cases.go:1562-1696`). Why: the
  behavior under test is the switch's resolver; a fabric adds nothing.
- Ruled: the IPv4 case pins twenty trace steps, not the seventeen this plan
  estimated. Why: the rendered trace and `switch.go` agree that 224.0.0.0/24
  never reaches the multicast resolver, so the case floods to eight ports
  with a `vlan-tag-form` and a `transmit` each. Cost if wrong: the two
  `ExpectedSteps` lists in `mcast_cases.go`.
- Ruled: `TestCapture` is a documented skip. Why: reading a real mDNS
  datagram needs privileges to open `/dev/bpf*` that the test host does not
  grant, and this plan allows recording the absence. Cost if wrong: one test
  body, once a capture is taken.
- Ruled: `TestZeroChecksumSentAsAllOnes` pins two boundary payloads instead
  of searching for one at test time. Why: a search whose predicate is the
  behavior under test cannot fail. Cost if wrong: the two payload literals,
  `{0xe1, 0x05}` and `{0xe1, 0x04}`.

## Requirements

1. Parent R1: UDP decode and encode. Example: the IPv4 fixture below decodes
   to ports 5353 and 5353, length 54, checksum `0xaa94`, and encoding the
   decoded header and payload with source 10.0.10.7 and destination
   224.0.0.251 yields the same 54 bytes.
2. Parent R2: TCP and ICMP decode. Example: `00 50 1f 90 00 00 00 01 00 00
   00 02 50 12 ff ff 00 00 00 00` decodes to source 80, destination 8080,
   sequence 1, acknowledgement 2, data offset 5, SYN and ACK set; `08 00 f7
   ff 00 00 00 00` decodes to type 8, code 0.
3. Parent R3: IPv4 mDNS floods, IPv6 mDNS with no member goes to router
   ports only.

Fixtures, computed on 2026-09-16 from the RFC 768 algorithm independently
of the code this plan produces (a Python one's-complement sum over the
pseudo-header, header, and payload). Both carry a minimal PTR query for
`_services._dns-sd._udp.local`, ports 5353 to 5353, length 54:

```text
IPv4 10.0.10.7 -> 224.0.0.251, checksum 0xaa94
14e914e90036aa94000000000001000000000000095f7365727669636573075f646e732d7364045f756470056c6f63616c00000c0001

IPv6 fe80::1 -> ff02::fb, checksum 0xa117
14e914e90036a117000000000001000000000000095f7365727669636573075f646e732d7364045f756470056c6f63616c00000c0001
```

The implementer captures one real mDNS datagram as well, for example
`tcpdump -i en0 -x udp port 5353 -c 1` on any machine with a Bonjour
service, and pins its ports and checksum in a decode test beside the
fixtures above; a capture settles what the fixtures cannot, as
`docs/solutions/conventions/a-codec-round-trip-cannot-locate-a-field-on-the-wire.md`
explains. If no capture is possible, the test file says so in a comment.

## Out of scope

- UDP checksum offload semantics, UDP-Lite, and IPv4 zero checksums on
  decode (accepted, not flagged).
- TCP options, ICMP bodies, and ICMPv6 neighbor discovery messages beyond
  type and code.
- Any change to `Switch.Resolve` or `mcast.Layer.Resolve`.

## Units

### U1. UDP codec
Files: `src/common/net/udp/udp.go`, `src/common/net/udp/udp_test.go`, `src/common/net/udp/README.md`
After: none
Change: `udp.Header{SrcPort, DstPort, Length, Checksum uint16}`;
`Decode(b []byte) (Header, []byte, error)` returns the header and the
payload bounded by `Length`, and refuses fewer than 8 octets or a `Length`
under 8 or over `len(b)`; `Encode(h Header, payload []byte, src, dst
netip.Addr) ([]byte, error)` sets `Length` from the payload, computes the
checksum over the IPv4 (RFC 768) or IPv6 (RFC 8200 section 8.1)
pseudo-header, refuses mixed address families, and sends a computed zero
as `0xffff`; `Verify(b []byte, src, dst netip.Addr) bool` recomputes the
checksum of a datagram. Errors use `errs` with `Attr("field", ...)` and no
scope prefix. The README shows the mDNS query fixture decoded and
re-encoded.
Tests: `TestDecodeFixtures` decodes both fixtures and checks every field
against literals and the payload against `b[8:]`; `TestDecodeDistinctPorts`
decodes `c3 50 00 35 00 0c 00 00 ...` (source 50000, destination 53, length
12, four payload octets) so that a decoder reading the two port slots the
wrong way round fails, which the 5353-to-5353 fixtures cannot catch
(`docs/solutions/conventions/a-codec-round-trip-cannot-locate-a-field-on-the-wire.md`); `TestEncodeMatchesFixture`
encodes header and payload with the fixture addresses and compares all 54
bytes; `TestChecksumOffsets` asserts `out[6:8]` equals the literal checksum
and `out[4:6]` the literal length; `TestZeroChecksumSentAsAllOnes` pins the
payload `{0xe1, 0x05}`, whose checksum computes to zero, and asserts it is
sent as `0xffff`; its sibling `TestChecksumOneSentAsOne` pins the adjacent
payload `{0xe1, 0x04}`, whose checksum computes to one, and asserts it is
sent unchanged as `0x0001`; `TestDecodeRefusals` covers short input, `Length` under 8, and
`Length` over the buffer; `TestEncodeRefusesMixedFamilies` covers an IPv4
source with an IPv6 destination and the reverse; `TestCapture`
pins the captured datagram or documents its absence.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/udp`

### U2. TCP and ICMP decoders
Files: `src/common/net/tcp/tcp.go`, `src/common/net/tcp/tcp_test.go`, `src/common/net/tcp/README.md`, `src/common/net/icmp/icmp.go`, `src/common/net/icmp/icmp_test.go`, `src/common/net/icmp/README.md`
After: none
Change: `tcp.Header{SrcPort, DstPort uint16, Seq, Ack uint32, DataOffset
uint8, Flags tcp.Flags, Window uint16}` with `Flags` a `uint16` bit set
naming FIN, SYN, RST, PSH, ACK, URG, ECE, CWR (RFC 9293 section 3.1);
`tcp.Decode(b []byte) (Header, []byte, error)` refuses fewer than 20
octets, a data offset under 5, or an offset past the buffer, and returns
the payload after the offset. `icmp.Header{Type, Code uint8, Checksum
uint16}` and `icmp.Decode(b []byte) (Header, []byte, error)` refusing
fewer than 4 octets; one decoder serves ICMPv4 and ICMPv6 because the
first four octets have the same layout (RFC 792, RFC 4443 section 2.1).
Tests: `tcp`: `TestDecodeLiteral` on the R2 segment checking every field
and `Flags.Has(SYN|ACK)`; `TestDecodeOffsets` asserts source port from
`b[0:2]`, flags from `b[13]&0x3f` and the two ECN bits; `TestDecodeRefusals`.
`icmp`: `TestDecodeLiteral` on the R2 echo request; `TestDecodeICMPv6Literal`
on `80 00 ... ` (echo request, type 128); `TestDecodeRefusals`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/tcp src/common/net/icmp`

### U3. mDNS multicast conformance cases
Files: `src/common/netsim/internal/netsimtest/mcast_cases.go`, `src/common/netsim/internal/netsimtest/cases.go`, `src/common/netsim/internal/netsimtest/corpus_test.go`, `src/common/netsim/internal/netsimtest/README.md`, `src/common/netsim/vswitch/conformance_test.go`
After: none
Change: two cases in a new `mcast_cases.go`, registered in
`DefaultRegistry`, in the troubleshooting ID list at `corpus_test.go:628-639`
(they sort between `loop-protect-contains-access-loop` and
`recursive-route-not-installed`), and in the total at `corpus_test.go:579`,
which becomes 26: the two cases take the corpus from 23 to 25, and merging
`main` brought a third, `troubleshooting/loop-protect-contains-access-loop`;
described in the README's case list.

`troubleshooting/mdns-ipv4-floods-under-snooping`: switch `sw1`, ports
`p1..p9`, VLAN 10 with `FloodUnregistered=false` and `RouterPorts: ["p9"]`
(`mcast.VLANSnooping`, a static router port, so no query has to be
learned), then the IPv4 fixture from U1, as literal bytes rather than an
import, inside an IPv4 packet with hop limit 255 from 10.0.10.7 to
224.0.0.251, destination MAC `01:00:5e:00:00:fb`, on `p1`. Expected outcome
`Flooded` to `p2..p9`, readiness `Complete`, false answer "snooping drops
unregistered mDNS". `ExpectedSteps` is the complete ordered trace, as
`corpus.go:434` requires: the bridge's classify and learn steps,
`group-destination` on layer `relay`, `flood` on layer `relay`, then per
egress port in port order a `vlan-tag-form` rewrite and a `transmit` step
(`src/common/netsim/vswitch/bridge/bridge.go:1330-1346`), twenty
expectations. `Switch.Resolve` exempts 224.0.0.0/24 before the multicast
resolver runs, so the IPv4 case has no `group-members` step and floods to
all eight other ports. The implementer takes the exact list from a first
run's rendered trace and checks each rule against the source before
pinning it.

`troubleshooting/mdns-ipv6-unregistered-router-ports`: same switch, the
IPv6 fixture from `fe80::1` to `ff02::fb`, destination MAC
`33:33:00:00:00:fb`, on `p1`, no MLD report. Expected outcome `Flooded`
with `p9` as the only egress: `replicate` reports `Flooded` whenever at
least one port transmitted (`bridge.go:1356-1358`), so the IPv4 and IPv6
cases differ in their steps, not their outcome. Decisive step RuleID
`group-members` on layer `mcast` with a `vswitch.mcast_membership` fact
reading `registered=false`; the full step list is classify, learn,
`group-destination`, `group-members`, then one `vlan-tag-form` and one
`transmit` for `p9`. False answer "the switch floods link-scope groups
like IPv4".

The corpus test in `vswitch/conformance_test.go` gains a subtest asserting
both cases' forwarded port sets.
Tests: the corpus runner (`AssertCase` twice per case),
`TestRegistryDeterministicOrdering` with the count and the two IDs added,
and the new `conformance_test.go` subtest. Nothing in this unit covers the resolver's `pending` path
(`mcast-query-unobserved`); a case for it belongs with the reflector phase
if that phase needs it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/internal/netsimtest src/common/netsim/vswitch/conformance_test.go`

Waves: U1 U2 U3

## Verification

```bash
go test -race ./src/common/net/udp/... ./src/common/net/tcp/... ./src/common/net/icmp/... ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/udp src/common/net/tcp src/common/net/icmp src/common/netsim/internal/netsimtest src/common/netsim/vswitch/conformance_test.go
```

## Definition of done

- [x] Verifier green for every changed path.
- [x] `src/common/netsim/README.md` lists the three codecs beside `igmp`
      and `mld`; each package has a README with a working example.
- [x] The corpus README lists both cases; `TestRegistryDeterministicOrdering`
      names them.
- [x] This plan's `status` set with an outcome note under its title; the
      parent's U1 `Landed:` line carries the commit range.
- [x] No plan labels in code.

## Open questions

Three review rounds each found a UDP codec test that passed for the wrong
reason: first that no test bounded the decoded payload by the length
field, then that the zero-checksum test searched for its input by calling
the encoder, and now that the two negative cases added to `TestVerify`
return false whether or not the guards they were added for are present.
Each round fixed the instance in front of it and the next round found
another. The property these rounds have been reaching for by hand is that
every guard in the package should be discriminated by at least one test,
which is what mutation testing checks mechanically. Whether to add such a
gate, and at what scope, is a decision for a plan rather than another
round of hand-written assertions.
