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
   and unregisters the session.

## Silent captures and inactivity timeouts

When a capture session's budget specifies only packet count or byte count without
a duration limit, an idle network interface would keep the session running
indefinitely.

The runner bounds silent captures using an inactivity timer. If no packets arrive
before the timeout elapses, the runner terminates `UploadCapture` without
sending a final chunk. Terminating the stream prematurely triggers central's
`failStream` cleanup, marking the session `FAILED` with stop reason
`CAPTURE_STOP_REASON_ERROR`. Sending a final chunk upon timeout would instead
cause central to fall back to `PACKET_COUNT` and report success for an empty
capture.

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

In accordance with repository privacy standards, telemetry emitted by this
package (slog attributes and span attributes under `flowseer.edge.capture.*`)
records only session IDs, sequence numbers, batch counts, and counter snapshots.
Zero bytes of captured packet data are ever written to telemetry.
