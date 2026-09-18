---
title: Remote Packet Capture In-Band Tail Gap Signal - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
---

# Remote Packet Capture In-Band Tail Gap Signal - Plan

## Goal

Provide an in-band gap notification message on `TailCaptureSession` so that an
operator tailing a live packet capture can distinguish a contiguous sequence of
packets from one where consumer lag dropped chunks. The means is adding
`TailGap` to `TailCaptureSessionResponse.body` in `capture_service.proto`,
updating `captureapi.Broadcaster` to track dropped chunks per subscriber rather
than discarding them silently, and sending the gap signal before delivering
subsequent chunks or closing the stream. Stop condition: This plan is wrong if
`TailCaptureSession` is required to buffer indefinitely or provide lossless
durable replay instead of in-memory best-effort broadcast with explicit drop
signaling.

## Decisions

- Tailing remains an ephemeral in-memory observation that drops chunks under
  subscriber lag rather than stalling upload or buffering to disk. Why:
  [docs/architecture/2026-09-09-remote-packet-capture-direction.md](../architecture/2026-09-09-remote-packet-capture-direction.md)
  lines 295-301 explicitly decides: "A tail is an observation of a capture in
  flight, so it is not worth a durable stream or a second write to disk; the
  price is that a tail slower than the upload loses chunks rather than stalling
  it... Both are acceptable while the artifact on disk is the record of what
  was captured." What was missing was the in-band signal for that price:
  `operator_service.go` currently sends only `attached: true` and
  `chunk: CapturePacketChunk`. When a subscriber channel fills (capacity 128),
  `Broadcaster.Broadcast` discards the chunk and only logs a server-side
  warning. The operator on the other end receives subsequent chunks without
  knowing bytes were lost.
- `TailCaptureSessionResponse` gains `TailGap gap = 3` in its `oneof body`.
  Why: `TailCaptureSessionResponse` already defines `oneof body` with
  `attached = 1` and `chunk = 2`. Adding `TailGap gap = 3` preserves the oneof
  design where every stream frame is an explicit semantic event: attachment,
  packet chunk, or gap notification.
- `TailGap` carries dropped chunk count, dropped packet count, first dropped
  sequence, and last dropped sequence. Why: A subscriber needs to know both the
  magnitude of the gap (how many chunks and packets were missed) and the exact
  packet sequence bounds (`first_dropped_sequence` to `last_dropped_sequence`)
  so it can correlate the live tail with Wireshark or the downloaded pcapng
  artifact.
- `Broadcaster` tracks drop state per subscriber subscription. Why: Different
  subscribers on the same capture session have different consumption speeds;
  drops are per-subscriber, not session-wide. A subscriber object tracks
  pending gap metrics. When space in the subscriber's channel opens up, the
  broadcaster delivers a gap notification item before the next packet chunk.
- If chunks are dropped at the very end of a capture (including the final
  chunk), the gap signal is delivered before stream termination. Why: If the
  final chunk (`final: true`) was dropped due to lag, closing the channel
  without a signal would cause the operator to see a clean stream termination
  (EOF) and assume all packets were observed. Delivering the pending gap before
  stream termination ensures the operator is notified of terminal loss.

## Requirements

1. `spec/proto/flowseer/api/capture/v1/capture_service.proto` defines `TailGap`
   and adds `TailGap gap = 3` to `TailCaptureSessionResponse.body`. Acceptance:
   Protovalidate passes for a response carrying `TailGap` with
   `dropped_chunks: 1`, `dropped_packets: 256`, `first_dropped_sequence: 100`,
   `last_dropped_sequence: 355`.
2. `Broadcaster` records drops per subscriber when the subscriber channel is
   saturated. Acceptance: In a test with two subscribers—one fast, one
   artificially stalled—broadcasting chunks beyond channel capacity increments
   the stalled subscriber's dropped chunk and packet counters, while the fast
   subscriber receives all chunks without drops.
3. `TailCaptureSession` delivers `TailGap` in-band before subsequent packet
   chunks. Acceptance: When a subscriber lags and drops chunk 2 (sequences
   256..511) and then resumes reading, it receives chunk 1, then a `TailGap`
   message reporting 1 dropped chunk, 256 dropped packets, first sequence 256,
   last sequence 511, followed by chunk 3 (first sequence 512).
4. Terminal drops are communicated before stream closure. Acceptance: When a
   subscriber drops the final chunk of a session, the stream sends a `TailGap`
   message before closing, rather than terminating with empty EOF.

## Out of scope

- Durable JetStream or disk persistence for live tailing.
- Cross-replica tail fan-out.
- Automatic slow-consumer stream disconnection.

## Units

### U1. Schema definition for TailGap and TailCaptureSessionResponse

Files: `spec/proto/flowseer/api/capture/v1/capture_service.proto`
After: none
Change: Adds `message TailGap` with `dropped_chunks`, `dropped_packets`,
`first_dropped_sequence`, and `last_dropped_sequence`; adds `TailGap gap = 3`
to `TailCaptureSessionResponse.body`.
Tests: `test/conformance/proto/layering_test.go`, `buf lint`, and
`buf format -d`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api/capture/v1/capture_service.proto`

### U2. Broadcaster per-subscriber gap tracking

Files: `src/services/device/internal/captureapi/edge_service.go`,
`src/services/device/internal/captureapi/edge_service_test.go`
After: U1
Change: `Broadcaster` replaces raw chunk channels with subscriber handles that
record dropped chunk counts, packet counts, and sequence ranges during capacity
overflow, and delivers queued gap notifications when channel capacity frees or
on session close.
Tests: `edge_service_test.go` unit tests verifying that overflowing a
subscriber records accurate gap metrics, that non-overflowing subscribers are
unaffected, and that gaps are delivered upon channel drain.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/captureapi/edge_service.go src/services/device/internal/captureapi/edge_service_test.go`

### U3. OperatorService.TailCaptureSession streaming with in-band gaps

Files: `src/services/device/internal/captureapi/operator_service.go`,
`src/services/device/internal/captureapi/operator_service_test.go`,
`src/services/device/test/integration/capture_test.go`
After: U2
Change: `TailCaptureSession` translates subscriber items (chunks and gaps)
into `TailCaptureSessionResponse`, delivering `TailGap` frames when gaps
occurred and ensuring terminal gaps are flushed before stream exit.
Tests: `operator_service_test.go` tests slow consumer scenarios verifying
in-band gap receipt and sequence continuity; `capture_test.go` integration
test tests end-to-end tail gap delivery when a consumer pauses reading.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/captureapi/operator_service.go src/services/device/internal/captureapi/operator_service_test.go src/services/device/test/integration/capture_test.go`

Waves: U1 | U2 | U3

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api/capture/v1/capture_service.proto src/services/device/internal/captureapi src/services/device/test/integration/capture_test.go
go test -race ./src/services/device/...
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `spec/proto/flowseer/api/capture/v1/capture_service.proto` updated with `TailGap`.
- [ ] `src/services/device/internal/captureapi` Broadcaster and OperatorService updated with gap handling.
- [ ] Conformance, unit, and integration tests green.
- [ ] No plan labels in code.

## Open questions

None.
