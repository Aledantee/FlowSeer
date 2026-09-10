---
title: Streaming Frame Transport - Direction
type: direction
date: 2026-09-09
topic: streaming-frame-transport
status: proposed-direction
---

# Streaming Frame Transport - Direction

FlowSeer has no streaming RPC and no chunked payload anywhere in `spec/proto/`.
Every landed method is unary, and the one bulk `bytes` field in the tree carries
a single whole protobuf message for the process-local bus
(`spec/proto/flowseer/service/v1/message.proto`). Remote packet capture is the
first consumer that cannot fit its result in one response, so the framing,
the accounting, and the authorization rules are settled here rather than
invented once per feature.

## A stream carries chunks, not packets

A streaming method sends a chunk message. A chunk carries a batch of records, the
sequence number of its first record, and a counters snapshot taken when the chunk
was built. It never carries one record per message.

Per-record messages put a protobuf frame, a Connect frame, and a scheduling
decision on every unit of work. A batch amortizes all three and gives the
receiver a natural checkpoint: it either has the whole chunk or none of it.
The chunk is also what a durable writer writes, so the stored form and the live
form are the same bytes and a consumer written against one reads the other.

Every chunk type carries:

- the reference of the subject the stream belongs to, repeated in every chunk, so
  a chunk is interpretable on its own after a reconnect;
- `first_sequence`, the sequence number of the first record in the batch, with
  sequence numbers dense and monotonic per subject from zero;
- the records, bounded by protovalidate on both item count and total size;
- a counters snapshot, so a receiver can tell a gap from a drop without waiting
  for the stream to end;
- a `final` flag on the last chunk of a well-ended stream.

Sequence numbers are what make loss detectable. A receiver that sees
`first_sequence` jump past the end of the previous batch knows exactly how many
records it lost and where, which no amount of counters alone can tell it.

## Nothing drops silently

A producer faster than its transport drops. Treat that as a condition to report
rather than a failure to prevent. `src/common/pump`'s `TrySendDropOldest` already
implements the bounded-buffer, drop-oldest behavior and returns the count it
dropped, and every streaming producer uses it rather than blocking a capture or
collection loop behind a slow consumer.

The rule that follows: a stream that dropped says so in the counters of the next
chunk it sends, and in the terminal state of its subject. A consumer that
receives a chunk with a sequence gap and no matching drop count treats the stream
as corrupt, not as merely lossy. Drop counts are separated by cause — the kernel
or capture ring, the producer's own budget, and the transport — because the
operator's response differs for each.

## A long stream re-proves the caller

`SignedEdgeAssertion` is valid for at most sixty seconds after it is issued
(`spec/proto/flowseer/api/edge/v1/assertion.proto`), and the edge README records
that streams are checked when they open. A capture that runs for ten minutes was
therefore authorized once by a credential that expired nine minutes ago, and the
verifier has no chance to notice a retired edge until the stream ends.

An edge-originated stream that may outlive the assertion window sends a fresh
`SignedEdgeAssertion` on the stream at an interval below that window. The server
verifies it exactly as it verifies the opening one, including the nonce replay
check, and closes the stream when the interval passes without one. This costs the
edge a signature every half minute, which is nothing next to the packets it is
already moving, and it turns retirement into something that takes effect within
the window rather than at the end of an arbitrarily long call.

The assertion does not bind the RPC method or the body, which the edge README
already names as an open boundary. This record does not settle that; it only
requires that the re-assertion frame be verified under whatever rule the opening
assertion is verified under, so the two cannot drift apart.

## What a chunk may not carry

A chunk that carries captured payload, message bodies, or any bytes FlowSeer did
not author is untrusted data end to end. It is never logged, never attached to a
span, and never used as a metric attribute, which is the existing rule in
`docs/conventions/observability.md` applied to a new shape rather than a new
rule. The counters are the observable surface of a stream; the records are not.

## Consequences

- The first streaming methods land in the capture packages, and their chunk
  shapes are the reference for the next consumer.
- A durable writer for a stream writes chunks, so an interrupted write truncates
  at a chunk boundary and the sequence numbers say where.
- Re-assertion needs a control frame on the stream, which means an edge-originated
  streaming request message is a `oneof` of a data chunk and an assertion, not a
  bare chunk. Server-originated streams to an operator client do not carry this,
  because an operator session is not an edge assertion.

## Sources

Every claim here is checkable in this repository; nothing external is relied on.

- No streaming method and no chunked payload exist yet: every `rpc` under
  `spec/proto/flowseer/` is unary, and the one bulk `bytes` field is
  `spec/proto/flowseer/service/v1/message.proto`.
- The assertion window, the nonce, and the replay check —
  `spec/proto/flowseer/api/edge/v1/assertion.proto`, where the
  `edge_assertion.short_lived` rule holds `expires_at` within 60 seconds of
  `issued_at`.
- Streams are authorized once, and method binding is open —
  `spec/proto/flowseer/api/edge/v1/README.md`, which records that the assertion
  "does not bind the RPC method or the request body" and that "streams are
  checked when they open".
- Bounded buffering that reports what it discarded — `TrySendDropOldest` in
  `src/common/pump/pump.go`.
- Untrusted bytes never reach telemetry — `docs/conventions/observability.md`.
