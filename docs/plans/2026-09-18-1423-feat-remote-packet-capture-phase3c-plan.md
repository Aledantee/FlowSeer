---
title: Remote Packet Capture Phase 3c, Edge Capture Wiring - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-09-1213-feat-remote-packet-capture-plan.md
---

# Remote Packet Capture Phase 3c, Edge Capture Wiring - Plan

## Goal

The agent host assembles the capture engine and runs a capture end to end: it
holds a capture-assignment stream open on the U3a transport, drives a session
from a received `CaptureSessionConfig` through `src/modules/capture`, and
uploads chunks on `UploadCapture` with a fresh assertion inside every window,
until a budget stops it. The means is a capture handler and an upload client in
the agent, assembled by `host` as a `service.Module` beside the access lane and
its dispatch loop. The proof is the host end-to-end test standing a live device
service next to a live agent and reading back the artifact, using the same harness
the agent's enrollment and dispatch tests already use. This phase is wrong if
the capture module's engine cannot be driven from a long-lived goroutine under
the host's supervision tree without blocking the dispatch loop, which would
mean the engine needs a different concurrency contract than U2 gave it.

## Decisions

- Substitute the packet source via an `OpenCaptureSource` seam in `host.Options`
  and export `capture.NewWithSource` in `src/modules/capture`. Why: real network
  interfaces (`AF_PACKET`) and `veth` pair creation require Linux and
  `CAP_NET_RAW`/`CAP_NET_ADMIN`, which are unavailable in standard unprivileged
  test runs and absent on macOS development checkouts. Existing real-interface
  tests under `src/modules/capture/rawsocket/local_linktest_test.go` and
  `src/modules/capture/rawsocket/test/integration/` are gated behind opt-in
  build tags (`capturetest`, `capture_mirror_integration`) that skip in
  `go test -race ./...`. Following the pattern of `OpenSNMP` and `OpenShell` in
  `src/edge/agent/host/options.go` (tested in
  `src/services/device/test/integration/agent_seams_test.go`), substituting the
  packet source factory via `host.Options` allows the live central and live agent
  end-to-end test in `src/services/device/test/integration/` to inject synthetic
  frames and run captures to budget on every platform without elevated
  privileges.
- Bound silent captures using an inactivity timer and duration ceiling; on
  expiration, abort the upload stream without sending a final chunk. Why: when a
  session budget bounds only packet count or byte count without `max_duration`,
  an idle interface never satisfies the budget condition. Sending a final chunk
  (`final: true`) upon timing out would cause central's `deriveStopReason` to
  fallback to `PACKET_COUNT` and falsely record the session as `COMPLETED`.
  Terminating the upload stream without a final chunk triggers central's
  `failStream`, transitioning the session to `FAILED` with `stop_reason:
  CAPTURE_STOP_REASON_ERROR`. To ensure the timeout is observable despite
  blocking socket reads (the defect central resolved in
  `src/services/device/internal/captureapi/deadline.go` via HTTP transport read
  deadlines), the edge relies on `rawsocket`'s existing `SO_RCVTIMEO` (100ms)
  polling and `Source.Close()` descriptor teardown so blocking
  `recvfrom`/`recvmsg` syscalls unblock and observe context cancellation within
  100ms, while the upload runner uses a non-blocking channel `select` loop.
- Assemble the capture module unconditionally as a `service.Module` in
  `host.Run`. Why: `src/edge/agent/host/config.go` and `options.go` provide no
  feature flags or module gating; the agent host is designed as an integrated
  appliance whose modules (`lane`, `capture`) are assembled unconditionally into
  the supervision tree. Subscribing to `SubscribeCaptureAssignments` is an idle,
  low-overhead stream when no captures are active, and heavyweight resources
  (raw sockets, BPF filters, packet buffers) are allocated dynamically only
  when a `Start` assignment arrives. This allows an operator to initiate a packet
  capture against any enrolled edge without modifying edge configuration files
  or restarting the agent.
- Dedicate one runner goroutine per active capture session, managed by an
  in-memory registry in the capture handler. Why: `subscribeloop.Run` requires
  its `Handler.Handle` method to return promptly so subsequent stream messages
  are not delayed. When a `start` assignment arrives, the handler launches a
  runner goroutine via `src/common/spawn.Go` and records its cancel function in a
  mutex-guarded map keyed by session ID. When a `stop` assignment arrives, the
  handler invokes the matching cancel function.
- Interleave packet chunks and periodic re-assertions on `UploadCapture` at a
  30-second cadence. Why: central requires a fresh `SignedEdgeAssertion` within
  every 60-second window (`assertionWindow` in
  `src/services/device/internal/captureapi/edge_service.go`). A 30-second
  re-assertion interval gives a 2x safety margin against network latency and
  scheduling jitter, ensuring the upload stream is never closed by central's read
  deadline during quiet or long-running captures.
- Isolate capture transport telemetry under the `flowseer.edge.capture`
  namespace and exclude packet payloads. Why: following the dispatch loop
  pattern in `src/edge/agent/internal/dispatch/subscribe.go` and conventions in
  `docs/conventions/observability.md`, capture stream metrics and events live in
  `flowseer.edge.capture` (`connections`, `messages`, `failures`). In accordance
  with privacy requirements, no captured packet data is ever emitted in log
  attributes or span attributes; only session IDs, sequence numbers, and counter
  summaries are recorded.

## Requirements

1. A `start` assignment received on `SubscribeCaptureAssignments` initiates
   packet capture and streaming on `UploadCapture`. Acceptance: an agent
   subscribed to assignments receives a `start` assignment with a
   `CaptureSessionConfig` naming `max_packets: 10`; the agent opens
   `UploadCapture`, delivers the initial assertion, uploads packet chunks, and
   completes the run when 10 packets are delivered.
2. The edge sends a fresh `SignedEdgeAssertion` inside every 60-second assertion
   window while uploading. Acceptance: a capture session lasting 75 seconds
   transmits at least two mid-stream `SignedEdgeAssertion` messages on
   `UploadCapture` at 30-second intervals; central does not reject the stream for
   an expired assertion window.
3. An operator stop assignment halts an in-flight capture and uploads the final
   chunk. Acceptance: while a capture session is running, central sends a `stop`
   assignment naming the session ref; the agent cancels the engine, flushes
   buffered packets with `final: true`, and closes `UploadCapture`; central
   records lifecycle `CANCELED` with `stop_reason: OPERATOR` and an attached
   artifact.
4. A capture session where no packets arrive terminates via inactivity timeout
   and is reported as failed. Acceptance: an agent starts a capture configured
   with `max_packets: 100` against an idle interface; after the inactivity
   timeout elapses, the agent terminates `UploadCapture` without sending a final
   chunk; central transitions the session to `FAILED` with `stop_reason: ERROR`.
5. Telemetry emitted by the agent excludes packet payload bytes. Acceptance: a
   capture session uploading 100 Ethernet frames produces log records and span
   attributes containing sequence numbers and counter values, but zero bytes of
   packet payload data.
6. Capture assignment stream contact counters are exported to metrics.
   Acceptance: opening the assignment stream increments
   `flowseer.edge.capture.connections` by 1; receiving an assignment increments
   `flowseer.edge.capture.messages` by 1; a connection refusal increments
   `flowseer.edge.capture.failures` by 1.

## Out of scope

- Schema definitions, central storage, and operator service: landed in U3b.
- Shared subscribe-loop transport: landed in U3a.
- Lab validation against physical switches: deferred to U3d.
- Live multi-consumer tail fan-out or payload indexing: handled centrally.

## Units

### U1. Capture engine source seam and edge capture handler
Files: `src/modules/capture/engine.go`, `src/modules/capture/engine_test.go`, `src/edge/agent/internal/identity/assertion.go`, `src/edge/agent/internal/identity/assertion_test.go`, `src/edge/agent/internal/capture/subscribe.go`, `src/edge/agent/internal/capture/session.go`, `src/edge/agent/internal/capture/upload.go`, `src/edge/agent/internal/capture/capture_test.go`, `src/edge/agent/internal/capture/README.md`
After: none
Change: `src/modules/capture` exports `NewWithSource(src Source, budget *modelcapturev1.CaptureBudget, reportsInterfaceDrops bool) *Engine`, exposing the existing engine constructor for callers supplying their own `Source`. `src/edge/agent/internal/capture` provides the agent's capture assignment and streaming client: `Open(client)` adapts `CaptureEdgeServiceClient.SubscribeCaptureAssignments` to `subscribeloop.Opener`; `Handler` implements `subscribeloop.Handler` to demultiplex incoming `SubscribeCaptureAssignmentsResponse` assignments into active capture sessions. On a `Start` assignment, it launches a session runner goroutine that opens `UploadCapture`, sends an initial `SignedEdgeAssertion`, executes `capture.Engine`, forwards batches as `CapturePacketChunk` messages, and sends mid-stream assertions every 30 seconds. On a `Stop` assignment, it cancels the running engine, flushes the final batch with `Final: true` to upload the terminating chunk, and releases the session. If no packets arrive within the configured inactivity timeout, the runner cancels the engine and closes the upload stream without a final chunk, causing central to mark the session failed. `Events` defines `flowseer.edge.capture.*` telemetry names.
Tests: `src/edge/agent/internal/capture/capture_test.go` covers start assignment handling and chunk upload, periodic mid-stream re-assertion every 30 seconds, operator stop cancellation and final chunk flushing, inactivity timeout aborting upload without final chunk, and custom `capture.Source` execution via `capture.NewWithSource`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/capture src/edge/agent/internal/capture`

### U2. Agent host capture module assembly and test substitution seam
Files: `src/edge/agent/host/options.go`, `src/edge/agent/host/host.go`, `src/edge/agent/host/host_test.go`, `src/edge/agent/host/capture_test.go`
After: U1
Change: `src/edge/agent/host/options.go` adds `OpenCaptureSource CaptureSourceOpener` to `host.Options`, where `CaptureSourceOpener` is `func(ctx context.Context, cfg capture.Config) (capture.Source, bool, error)`. Nil defaults to production `capture.New`. `src/edge/agent/host/host.go` constructs `captureedgev1connect.NewCaptureEdgeServiceClient` using the agent's signed client and central URL, and registers a second `service.Module` named `"capture"` in `service.Run`. In `captureAssembly.setup`, it initializes the capture assignment handler, registers observable counter metrics for `flowseer.edge.capture.connections`, `flowseer.edge.capture.messages`, and `flowseer.edge.capture.failures`, and starts `subscribeloop.Run` in its attempt runner.
Tests: `src/edge/agent/host/capture_test.go` and `src/edge/agent/host/host_test.go` verify that `host.Run` initializes the `"capture"` module alongside `"lane"`, that `host.Options.OpenCaptureSource` is passed to the capture runner when set, and that `flowseer.edge.capture.connections`, `messages`, and `failures` observable counters are registered on the meter.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/agent/host`

### U3. Live central and agent end-to-end capture test
Files: `src/services/device/test/integration/capture_test.go`, `src/services/device/test/integration/agent_test.go`
After: U2
Change: The end-to-end integration test suite runs a live agent against a live device service central and verifies complete remote packet capture workflows without real network interfaces: (1) full capture to budget where an operator creates a session, the agent captures synthetic packets from `OpenCaptureSource`, uploads chunks, and reaches `COMPLETED` at budget with pcapng artifact downloadable by operator; (2) operator cancellation where an in-flight capture is canceled, the agent flushes the final chunk, and central records `CANCELED` with artifact attached; (3) inactivity timeout where an idle capture terminates without final chunk and central transitions the session to `FAILED` with `stop_reason: ERROR`; and (4) privacy verification asserting that agent logs and telemetry contain zero packet payload bytes.
Tests: `src/services/device/test/integration/capture_test.go` covers `TestRemotePacketCapture_EndToEndWithAgent`, `TestRemotePacketCapture_OperatorCancellation`, `TestRemotePacketCapture_InactivityTimeout`, and `TestRemotePacketCapture_TelemetryPrivacy`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/test/integration`

Waves: U1 | U2 | U3

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-09-18-1423-feat-remote-packet-capture-phase3c-plan.md
go test -race ./...
```

## Definition of done

- Verifier green for every changed path across all units.
- Package READMEs under `src/edge/agent/internal/capture/` written in U1.
- `src/edge/agent/README.md` updated to document the capture assignment loop and upload client in U2.
- This plan's `status` set to `implemented` with an outcome note once all units land.
- No plan labels in code, comments, or commit messages.

## Open questions

None. The three blocking design questions were resolved in Decisions.
