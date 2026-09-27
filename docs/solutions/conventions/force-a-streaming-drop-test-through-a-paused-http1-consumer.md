---
title: Force a streaming drop test through a paused HTTP/1.1 consumer, not the real HTTP/2 transport
date: 2026-09-27
last_verified: 2026-09-27
category: conventions
module: src/services/device/internal/captureapi/operator_service_test.go
problem_type: convention
component: testing_framework
severity: medium
applies_when:
  - "Writing a test that must make a best-effort streaming RPC actually drop, so a slow consumer overflows a server-side buffer"
  - "A drop/overflow test never sees a drop, or only drops after flooding tens of megabytes, and the threshold moves between machines"
related_components: [service_layer, edge]
tags: [testing, streaming-rpc, connect, back-pressure, http2, packet-capture]
---

# Force a streaming drop test through a paused HTTP/1.1 consumer, not the real HTTP/2 transport

## The situation

`TailCaptureSession` drops chunks under consumer lag and reports the loss as a
`TailGap`. To test that, a consumer must fall far enough behind that the
broadcaster's 128-item buffer overflows. Whether it overflows depends entirely
on the transport between the handler and the test client, not on the handler.

Driving the drop through the full central stack (an operator client over its
HTTPS listener, which negotiates HTTP/2) is unreliable and heavy. Go's HTTP/2
`Transport` runs a background read loop that pulls DATA frames into a per-stream
pipe and returns flow-control credit as they arrive, up to a large connection
window — so the server keeps sending, and the client buffers, long after the
application stopped reading. Measured on this host (darwin, 2026-09-27): a flood
of 300 chunks of 32 KiB (~9.6 MiB) through central was fully delivered with zero
drops, and only a 3000-chunk (~96 MiB) flood forced a gap. The exact threshold
sits somewhere in between and moves with the machine.

## What is true and why

A plain `httptest.NewServer` (HTTP/1.1, no TLS) back-pressures the server as
soon as the socket buffers fill, because an HTTP/1.1 client reads the response
body only when the application calls `Read` — it returns no credit ahead of the
app. A consumer that reads the attach marker and then stops keeps its receive
window small, so the handler's `Send` blocks after a small, machine-independent
amount, the broadcaster buffer fills, and the flood drops deterministically.
Measured on the same host: 400 chunks of 32 KiB (~12.8 MiB) reliably dropped,
delivering ~129 before the handler blocked.

## How to apply

- Test the drop over a plain `httptest` listener with a real Connect client, not
  the TLS/HTTP-2 stack. Have the consumer receive the attach frame, then pause
  (stop calling `Receive`) while a fixed flood of large chunks is broadcast past
  the 128-item buffer, then drain.
- Assert an invariant that holds for any split between delivered and dropped, so
  the test does not depend on where back-pressure kicks in: the delivered chunk
  sequences and the gap-covered spans must tile the whole range in order, with no
  hole and no overlap. Then assert the final chunk's loss is the last frame
  before EOF, which exercises the terminal flush.
- See `TestTailCaptureSession_InBandGapOnSlowConsumer` in
  `src/services/device/internal/captureapi/operator_service_test.go`.
- Do not try to make the full-central overflow deterministic; the plan's
  Decisions record why that test was omitted
  (`docs/plans/2026-09-18-2122-feat-capture-tail-gap-signal-plan.md`).

## What this does not cover

This is about forcing back-pressure to make a drop happen, not about the drop
accounting itself. The per-subscriber gap metrics are covered directly at the
broadcaster in `edge_service_test.go` (`TestBroadcaster_*`), which needs no
transport at all.
