---
title: A Packet Fixture Generator Must Compute the Protocol Checksums gopacket Leaves Zero on a Raw Payload
date: 2026-09-25
last_verified: 2026-09-25
category: architecture-patterns
module: src/edge/netpen/attacks/routing
problem_type: bug
component: test_fixtures
severity: medium
symptoms:
  - regenerating a committed packet fixture with `go run harvest_*.go` changes it,
    and a checksum-validity test then fails with the fixture checksum reading zero
  - a fixture generator and the runtime it doubles diverge only after a
    regeneration; the byte-for-byte fixture pin passes on the committed tree
    because the fixtures were captured while the two still agreed
  - a protocol whose checksum sits inside an application payload (OSPF, an LSA)
    ships with a zero checksum from the generator but a valid one from the runtime
root_cause: >
  the generator hands the inner protocol bytes to gopacket as
  `gopacket.Payload`, and gopacket's `SerializeOptions{ComputeChecksums: true}`
  only fills checksums for layers it models (Ethernet, IPv4, TCP/UDP). It cannot
  see a checksum field inside an opaque payload, so that field stays whatever the
  generator wrote — zero. The runtime computes it explicitly, so the two drift
  the moment either side's bytes change.
resolution_type: design_fix
applies_when:
  - writing or editing a `harvest_*.go`-style fixture generator that crafts a
    protocol whose checksum lives inside the bytes handed to gopacket as a raw
    Payload (OSPF, an OSPF LSA, or any L4+ protocol gopacket does not model)
  - a change forces a committed packet fixture to be regenerated and a
    checksum-validity or fixture-pin test then fails
  - deciding whether a fixture generator is a trustworthy independent double of
    the runtime that emits the same frames
related_components: [netpen, test_fixtures, protocol_client]
tags: [gopacket, checksum, test-fixtures, harvest-generator, ospf, review-finding]
---

# A Packet Fixture Generator Must Compute the Protocol Checksums gopacket Leaves Zero on a Raw Payload

## The situation

`src/edge/netpen/attacks/routing/harvest_routing.go` (`//go:build ignore`, run
with `go run`) regenerates the committed OSPF fixtures
(`attacks/testdata/routing/ospf*.pcap`) that `TestOSPF_FixturePins` compares the
runtime OSPF attack TX against byte-for-byte. It builds each OSPF frame as
Ethernet + IPv4 + `gopacket.Payload(ospfBytes)` and serializes with
`ComputeChecksums: true`. gopacket fills the Ethernet and IPv4 checksums, but the
OSPF header checksum (`hdr[12:14]`) and the Router-LSA Fletcher checksum
(`lsaHdr[16:18]`) live inside that opaque payload, so gopacket never touches them
— the generator shipped them zero.

The runtime `src/edge/netpen/attacks/routing/ospf.go` computes both (`ospfChecksum`
RFC 2328 §D.4, `ospfLSAChecksum` §12.1.7). So the generator and the runtime
agreed only because the committed fixtures had been captured while they matched;
a fresh `go run harvest_routing.go` regressed the fixtures to zero checksums and
broke `TestOSPF_ChecksumValid`. A live IOS-XE peer rejects a zero-checksum OSPF
packet, so the divergence was not cosmetic.

## What is true and why

**A fixture generator that reimplements the wire bytes must also reimplement
every checksum the serializer will not compute for it.** gopacket computes only
the checksums of layers it models; anything inside a `gopacket.Payload` is
opaque. The generator therefore has to compute the inner-protocol checksums
itself, with the same algorithm the runtime uses, so the two stay a genuine
independent double rather than silently drifting.

```go
// harvest_routing.go, after building the OSPF payload and before craft():
binary.BigEndian.PutUint16(payload[12:14], ospfChecksum(payload)) // RFC 2328 D.4
binary.BigEndian.PutUint16(lsa[16:18], ospfLSAChecksum(lsa))      // RFC 2328 12.1.7
```

Reimplement the algorithm in the generator rather than importing the runtime's
unexported function: the generator is a standalone `package main` under a build
tag, and an independent copy is what lets the fixture pin catch a future runtime
regression. Prove the two agree with a fixture-pin test (byte-for-byte against
the runtime) and prove the frames are valid on the wire with a checksum-validity
test that recomputes the residue independently (ones-complement → `0xffff`,
Fletcher → `(0,0)`), not by re-calling the same function.

## How to apply

When a change forces a packet fixture to be regenerated, regenerate it with the
canonical generator, then run both the fixture-pin test and a checksum-validity
test. If the validity test fails with a zero checksum, the generator is missing
an inner-payload checksum gopacket cannot compute — add it, matching the
runtime's algorithm and field offsets. Confirm only the intended frames changed
(here, only the OSPF pcaps; eigrp/wpad were untouched).

## Evidence

- `src/edge/netpen/attacks/routing/harvest_routing.go`: `craft` uses
  `SerializeOptions{ComputeChecksums: true}`; the OSPF payload is a raw
  `gopacket.Payload`; `ospfChecksum`/`ospfLSAChecksum` now write `hdr[12:14]` and
  `lsa[16:18]`.
- `src/edge/netpen/attacks/routing/ospf.go`: the runtime `ospfChecksum` and
  `ospfLSAChecksum` the generator must match.
- `src/edge/netpen/attacks/routing/routing_test.go`: `TestOSPF_FixturePins`
  (byte-for-byte against the runtime), `TestOSPF_ChecksumValid` /
  `TestOSPF_LSAChecksumValid` (independent residue recomputation).

## What this does not cover

This is about a checksum the serializer cannot reach, not about a wrong value the
generator and runtime share: a constant that is wrong in both (here the attacker
router ID `0x0a000099`, meant to be `10.0.0.99` but equal to `10.0.0.153`) passes
every fixture pin because the fixtures encode the same wrong bytes, and only a
live vendor run reveals it — see
[a permissive synthetic tier passes attacks a real vendor rejects](../conventions/a-permissive-synthetic-tier-passes-attacks-a-real-vendor-rejects.md).
