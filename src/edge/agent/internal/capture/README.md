# Edge capture assignment handler and upload client

The capture package provides the edge agent's capture assignment handler and
upload streaming client. It subscribes to central's capture assignments on
`SubscribeCaptureAssignments`, drives active capture sessions through
`src/modules/capture`, and streams packet chunks to central on `UploadCapture`.

## How it works

When central assigns a capture session to this edge:

1. `Open` adapts `CaptureEdgeServiceClient.SubscribeCaptureAssignments` into the
   `subscribeloop.Opener` contract.
2. `Handler.Handle` receives `SubscribeCaptureAssignmentsResponse` messages. To
   keep the stream loop from stalling, `Handle` returns immediately after
   registering the session and launching a supervised runner goroutine.
3. The runner goroutine opens `UploadCapture`, delivers the opening
   `SignedEdgeAssertion`, and sends an initial chunk associating the stream
   with the session ID in central's store.
4. It initializes `capture.Engine`, runs it, and reads batches from the engine's
   pump.
5. While uploading, the runner sends fresh `SignedEdgeAssertion` frames every
   30 seconds, well within central's 60-second assertion window.
6. When a session reaches its budget, the engine marks the trailing batch as
   final; the runner uploads the chunk with `final: true`, closes the stream,
   and unregisters the session. The engine marks no batch final for a run its
   source killed off, so a capture that died mid-stream leaves the same trace
   as one that timed out.

## A capture that did not finish must not send a final chunk

The final chunk is the whole of what central has to go on. On one it finalizes
the artifact, sets `COMPLETED`, and derives a stop reason from the budget —
and `deriveStopReason` falls back to `PACKET_COUNT` when nothing else fits, so
an empty capture reported as final becomes a completed capture that reached
its packet budget. Without one, `failStream` marks the session `FAILED` with
`CAPTURE_STOP_REASON_ERROR`.

Two things end a session short, and neither sends one:

- A budget that bounds packets or bytes but not duration never completes on an
  idle interface. The runner bounds that with an inactivity timer and closes
  `UploadCapture` when it elapses.
- A source that fails mid-capture. `capture.Engine` leaves `Final` unset on
  every batch of a failed run and closes its pump with the error, so the
  runner sees the pump close with no final batch and ends the stream the same
  way.

Blocking kernel socket reads (`AF_PACKET`) observe cancellation because
`rawsocket` configures `SO_RCVTIMEO` (100ms) polling and descriptor closure in
`Source.Close()`, allowing the engine to stop promptly.

## Operator cancellation

When an operator cancels an in-flight capture, central delivers a `Stop`
assignment on the assignment stream. `Handler` looks up the active session and
cancels the engine's context. The engine halts packet intake, flushes buffered
packets into the pump marked with `Final: true`, and signals completion. The
runner transmits this final batch to central, which records lifecycle
`CANCELED` with stop reason `OPERATOR` and preserves the recorded pcapng
artifact.

## Telemetry privacy

What this package records about a chunk is its first sequence, its packet
count, and whether it is final; what it records about a session is its ID. No
packet byte reaches a log record, and none can: the records are built from the
batch's metadata, never from `batch.Records`. The package opens no spans of its
own — the stream's counters live in the `flowseer.edge.capture.*` metrics the
host registers.
