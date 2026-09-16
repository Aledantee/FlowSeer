---
title: A Round Trip Through Your Own Codec Cannot Locate a Field on the Wire
date: 2026-09-16
last_verified: 2026-09-16
category: conventions
module: src/common/netsim/vswitch/stp
problem_type: bug
component: netsim
severity: high
symptoms:
  - "Every encoder/decoder test passes and simulated peers interoperate, but a real device or a capture reads two fields of the frame as each other"
  - "A wire-layout comment and the code beneath it disagree, and no test notices"
root_cause: "Encode wrote the CIST bridge identifier into the octets the MST shape reserves for the CIST regional root identifier, and the regional root into the bridge identifier's octets. Decode read the same two slots the same way round, so the swap cancelled: the round-trip tests compared a decoded struct against the struct that was encoded, and the fabric tests ran netsim against netsim, which is the same swapped codec on both ends."
resolution_type: code_fix
applies_when:
  - "Adding or changing a packet encoder or decoder under src/common/net/ or src/common/netsim/, including a new encapsulation over an existing frame"
  - "Deciding what tests a wire format needs, or reviewing a codec whose tests are round trips and refusals"
  - "A simulated protocol interoperates with itself but a real peer or a capture disagrees about a field"
related_components: [stp, fabric, packet_capture]
tags: [wire-format, codec, testing, spanning-tree, mstp]
---

# A round trip through your own codec cannot locate a field on the wire

## The situation

`stp.Encode` put the CIST bridge identifier at payload `[20:28]` and the CIST
regional root identifier at `[96:104]`. The MST shape wants them the other way
round, and `encodeMST`'s own doc comment said so. `Decode` read the two slots
with the same swap, so it handed back the struct that went in. Every test in the
package passed, and so did the fabric tests, because in netsim both ends of a
link run this code.

This is not a spanning-tree quirk. It is what a symmetric codec does to a test
suite made of round trips: the test asserts that `Decode(Encode(x)) == x`, which
holds for any bijection, including the wrong one. The frame is the contract with
something outside the tree, and nothing inside the tree is checking it.

## What to do instead

A codec test suite needs at least one assertion whose expected value is a
literal, written from the specification rather than produced by the code under
test. Absolute octet offsets are the cheap form:

```go
// src/common/netsim/vswitch/stp/bpdu_test.go:931
wantCISTRegionalRootOctets := []byte{0x12, 0x34, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
if got := frame.Payload[20:28]; !bytes.Equal(got, wantCISTRegionalRootOctets) {
    t.Errorf("payload[20:28] = % x, want % x (CIST regional root identifier)", got, wantCISTRegionalRootOctets)
}
```

Pick values that cannot collide: the priority and address above differ in every
octet from the bridge identifier's, so a swap fails the comparison instead of
matching by accident. Zero-valued fields and repeated bytes hide exactly the
error this test exists to find.

The same assertion works from the other side. A decode test fed a literal
payload pins the offsets just as well, and a real capture is better than either,
because it also settles questions the prose left open. None was available for
MSTP here, which is the reason the offsets had to be pinned by hand.

Keep this proportional. One placement test per shape is enough; the round trips
still earn their place for the fields the placement test does not name.

## Why the usual safety nets miss it

- **The round trip is symmetric.** `encodeMST` overwrites `[20:28]` with the
  regional root after `putBody` runs (`bpdu.go:461-462`), and `readMSTBody`
  reverses it with `b.RegionalRootID = b.BridgeID` (`bpdu.go:724`). Swap both
  and the pair stays consistent.
- **The fabric is netsim on both ends.** `src/common/netsim/vswitch/stp` is the
  only wire codec netsim has, and every simulated bridge encodes and decodes
  with it, so no end-to-end scenario can disagree with itself.
- **A doc comment is not a test.** `encodeMST`'s comment described the correct
  layout the whole time (`bpdu.go:430-440`). The code under it did something
  else for as long as nothing compared bytes.

## Evidence

- The fix: commit `02ec81b0`, "correct MST BPDU field placement and encode
  bounds", which swapped the two writes back and added
  `TestMSTBPDUEncodePlacesBridgeAndRegionalRootSeparately`
  (`src/common/netsim/vswitch/stp/bpdu_test.go:904`).
- The current placement, matching the comment above it:
  `binary.BigEndian.PutUint16(payload[96:98], b.BridgeID.Priority)`
  (`src/common/netsim/vswitch/stp/bpdu.go:491`) against
  `binary.BigEndian.PutUint16(payload[20:22], b.RegionalRootID.Priority)`
  (`src/common/netsim/vswitch/stp/bpdu.go:461`).
- The gap the bug sat in: `TestBPDUCodecRoundTrip` (`bpdu_test.go:26`) and
  `TestMSTBPDUCodecRoundTrip` (`bpdu_test.go:688`) both compare structs, not
  octets. `go test ./src/common/netsim/vswitch/stp/ -run 'TestMSTBPDU|TestBPDUCodecRoundTrip'`
  passes on 2026-09-16.
- The same gap stands open elsewhere. `src/common/net/{ethernet,igmp,mld,lacp}`
  are tested by round trips and refusals; `igmp_test.go:29` and `mld_test.go:27`
  assert the first octet and the total length, and nothing in any of the four
  asserts where a multi-octet field lands.

## What this does not cover

It says nothing about whether the layout you write the literal from is right.
An offset test freezes your reading of the specification, so a misread stays
misread until a capture or a real peer contradicts it; that is a smaller error
than an internally inconsistent codec, not no error. It also does not apply to a
format whose canonical encoder lives outside this tree, such as protobuf, where
the library owns the layout and the round trip is the right test.
