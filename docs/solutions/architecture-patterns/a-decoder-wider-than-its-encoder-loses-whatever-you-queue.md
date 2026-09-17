---
title: A Decoder That Accepts More Than Its Encoder Emits Loses Whatever You Queue and Re-Encode
date: 2026-09-17
last_verified: 2026-09-17
category: architecture-patterns
module: src/common/net/ip
problem_type: architecture_pattern
component: netsim
severity: high
applies_when:
  - "Storing a decoded value and re-encoding it later — a hold queue, a retry buffer, a scheduler, a replay log — rather than encoding it at the moment its caller is still there to be told"
  - "Writing or reviewing a codec where Decode and Encode do not validate the same set, such as an address, an enum, or a length one side normalizes and the other refuses"
  - "A frame, message, or record leaves a queue with no emission and no error, and the queue's own accounting says it left"
related_components: [netsim, routing, fabric, packet_capture]
tags: [codec, wire-format, queue, validation, silent-drop]
---

# A decoder that accepts more than its encoder emits loses whatever you queue

## The situation

`ip.Decode` accepts an IPv6 header whose source or destination is an
IPv4-mapped address. `ip.Header.Encode` refuses one. Nothing is wrong with
either half on its own: the decoder reads what the wire can carry, and the
encoder refuses to emit a form that is not a legal IPv6 address. The defect
lives in the gap between the two accept sets, and it only becomes reachable
when something stores a decoded value and encodes it later.

`routing.Layer` did exactly that. A frame whose next hop was unresolved went
into a neighbor hold queue as a decoded header plus payload, and `finishHeld`
re-encoded it when the neighbor answered. The re-encode failed, `finishHeld`
returned `(HeldFrame{}, false)`, and the frame left the queue with no
emission, no exit and no drop record — while the same header on the direct,
already-resolved path reported `bad-header` to its caller. One input, two
answers, decided by whether a neighbor happened to be resolved.

## What is true, and why

A decoder's accept set and an encoder's emit set are separate contracts. Where
the first is wider, every value in the difference is a value you can hold but
never hand back. A queue makes that gap reachable, and a queue is also the
worst place to discover it: the caller that could have been told is gone, and
the only remaining options are to drop the value or to invent an error nobody
asked for.

The fix is not to widen the encoder or narrow the decoder — either may be
right, but neither is local to the queue. The fix is to **encode at admission,
while the caller is still on the stack**, and refuse there:

```go
// Encode before resolving, in the egress form both paths use.
heldHdr := hdr
heldHdr.HopLimit--
pktBytes, err := heldHdr.Encode(payload)
if err != nil {
    res.Reason = ReasonBadHeader
    // ... drop step ...
    return res
}
```

The encoded bytes then serve the direct path, so the eager encode costs
nothing on the path that was going to encode anyway, and the queue holds only
values it is known to be able to emit.

## Evidence

The two accept sets, in the same file:

- `src/common/net/ip/ip.go:181-182` — `decodeV6` builds both addresses with
  `netip.AddrFrom16`, which leaves an IPv4-mapped address as an IPv6 address.
- `src/common/net/ip/ip.go:286` and `:293` — `encodeV6` refuses exactly that:
  `if !h.Src.Is6() || h.Src.Is4In6()`.

Observed directly: `ip.Decode` returns a nil error for a hand-built header
whose Src is `::ffff:10.0.0.1`, and `Encode` on the decoded header returns
`IPv6 source address ::ffff:10.0.0.1 is not an IPv6 address`.

The silent path and the fix:

- `src/common/netsim/vswitch/routing/neighbor.go:306-310` — `finishHeld`
  re-encodes and returns `(HeldFrame{}, false)` when that fails, which appends
  nothing to the effects the caller drains.
- `src/common/netsim/vswitch/routing/layer.go:816-822` (`Route`) and
  `:1004-1010` (`Originate`) — both now encode before resolving the next hop.

The property that catches it, rather than the instance:

- `src/common/netsim/vswitch/routing/neighbor_internal_test.go` —
  `TestHoldQueueConservesEveryFrame` asserts that the frames observed in a
  hold queue and the frames the layer reported leaving one are the same
  multiset. Sequence `"an unencodable datagram via route never enters"`
  (`:151`) fails against the unfixed code with `entered [1 2 3], exited
  [1 3]`. `encodeUnencodableIPv6Packet` (`:111`) hand-builds the wire bytes,
  because the packet cannot be produced by the encoder under test.

## What this does not cover

Whether `ip.Decode` should reject an IPv4-mapped address outright, or
`ip.Header.Encode` should accept and normalize one, is a question about the
`ip` package's contract and is open. This entry is about the queue: until the
two accept sets agree, anything that holds a decoded value owes its caller a
refusal at admission.

It also does not cover a codec whose two halves agree with each other but
disagree with the wire — that is
[A Round Trip Through Your Own Codec Cannot Locate a Field on the Wire](../conventions/a-codec-round-trip-cannot-locate-a-field-on-the-wire.md),
which a round-trip test cannot catch for the opposite reason.
