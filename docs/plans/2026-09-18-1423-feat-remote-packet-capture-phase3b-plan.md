---
title: Remote Packet Capture Phase 3b, Command Channel and Central Capture Leg - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
execution: mixed
amends: docs/architecture/2026-09-09-remote-packet-capture-direction.md
parent: docs/plans/2026-09-09-1213-feat-remote-packet-capture-plan.md
---

# Remote Packet Capture Phase 3b, Command Channel and Central Capture Leg - Plan

> Implemented. 5 units, 2026-09-18T13:44:55Z to 2026-09-18T15:03:08Z.

## Goal

An operator's `CreateCaptureSession` on the device service reaches the edge as
an assignment, and the packets the edge uploads come back to that operator. The
means is one new streaming RPC on `CaptureEdgeService`, a central assignment
outbox, and a central capture leg that serves the operator `CaptureService`,
stores pcapng artifacts on disk under the service's state directory with bounded
retention, and serves `TailCaptureSession` and `DownloadCaptureSession`. Stop
condition: This plan is wrong if the device service's state directory and
supervision cannot host multi-megabyte binary artifact storage under the
capture direction's bounded-retention rule without an external storage
dependency, in which case the artifact store must be planned as a separate
service foundation first.

## Decisions

The parent plan's Decisions apply; this phase settles its command-channel and
central-storage choices.

- An amendment to `docs/architecture/2026-09-09-remote-packet-capture-direction.md`
  covers central artifact storage; no separate direction record is written. Why:
  the capture direction already settled bounded retention, pcapng
  wire-versus-storage separation, and the departure from retire-is-not-purge
  (purging artifact bytes on expiry while preserving session records and
  counters for audit). That record left central storage open only because central
  had no implementation when written, explicitly anticipating reconciliation
  when central landed ("Whoever writes that store reconciles it against the device
  service record rather than starting fresh"). Central persistence in
  `src/services/device` combines JetStream KeyValue for session metadata with
  disk files under `<StateDir>/captures/` for multi-megabyte pcapng payloads.
  Because this mechanism is internal to the device service, touches no other
  services, and introduces no cross-service contracts, the promotion rule in
  `SKILL.md` directs keeping it as an amendment to the capture direction record.
- `CaptureEdgeService` gains
  `rpc SubscribeCaptureAssignments(SubscribeCaptureAssignmentsRequest) returns (stream SubscribeCaptureAssignmentsResponse)`,
  carrying a `oneof assignment` of `CaptureSessionConfig start = 1` and
  `CaptureSessionGlobalRef stop = 2`. Why: the assignment stream requires only
  start and stop instructions; `CaptureSessionConfig` already encapsulates the
  ref, source, filter, budget, and authorization.
- Central derives owed assignments from session lifecycle states: a `start`
  assignment is owed while a session is `CAPTURE_LIFECYCLE_PENDING` (prior to
  upload attachment), and a `stop` assignment is owed while an operator
  cancellation is in flight for an active session. Once the edge connects
  `UploadCapture` and delivers the first chunk, central transitions the session to
  `CAPTURE_LIFECYCLE_RUNNING` and withdraws the owed `start` assignment.
- The edge deduplicates re-originated assignments using an in-memory capture
  registry keyed by session UUID (`CaptureSessionGlobalRef.capture_session.id`).
  Why: unlike a device mutation that follows a submit-and-terminate lifecycle
  with unary report acknowledgements, a packet capture is a long-running
  streaming operation. If central re-sends `start` while a session is already
  active (`RUNNING`), the edge suppresses starting a second capture and meets the
  duplicate with silence; the existing `UploadCapture` stream serves as the
  continuous proof of progress. If the session has already completed locally, the
  edge drops the re-originated start to avoid repeating a finished capture. A
  `stop` assignment is idempotent: it cancels an active capture context, flushes
  the final chunk on `UploadCapture`, and is a no-op if the session is already
  terminal or unknown.
- A `CaptureSessionConfig` is bound to the edge by the `EdgeGlobalRef` embedded
  in its `CaptureSessionGlobalRef` (`ref.edge`), authenticated on both streams
  via `SignedEdgeAssertion`. Why: unlike device mutations whose routing requires
  an inventory lookup (`Resolver.Devices`/`Resolver.Hosts`) because devices are
  hosted on integrations, capture sessions are edge-scoped entities whose ref
  pair contains `EdgeGlobalRef` by construction
  (`spec/proto/flowseer/model/capture/v1/capture_session.proto`). On
  `SubscribeCaptureAssignments`, the calling edge is authenticated via
  `edgeapi.EdgeIDFromContext(ctx)`, and central delivers only assignments where
  `ref.edge.edge.id == edgeID`. The edge refuses any received assignment whose
  `ref.edge.edge.id` does not match its own identifier. On `UploadCapture`,
  central checks `chunk.session.edge.edge.id == callingEdgeID`, rejecting
  mismatches with `CodePermissionDenied` (`ErrCodeForbidden`) to enforce edge
  boundary isolation on ingress.
- Session metadata records (`CaptureSessionRecord`) live in the `CENTRAL`
  JetStream KeyValue bucket `captures` with CAS lifecycle transitions, while
  pcapng artifacts live on disk as `<StateDir>/captures/<session_id>.pcapng`.
  Why: JetStream KV values are subject to 1MB size bounds and fsync penalties
  for large payloads, while pcapng artifacts can reach tens of megabytes bounded
  by the session budget. Writing chunks directly to an appendable file on disk
  using `src/modules/capture`'s pcapng renderer amortizes I/O and lets
  `DownloadCaptureSession` stream file slices in 1MB chunks without holding the
  full artifact in memory.
- Bounded retention is enforced by a periodic sweeper that unlinks
  `<StateDir>/captures/<session_id>.pcapng` once `time.Now().After(artifact.expires_at)`,
  leaving the session record and its counters intact in the KV bucket and
  clearing the artifact descriptor or marking payload expired. Why: this
  fulfills Directive 2002/58/EC Article 5(1) and the capture direction's rule
  that communication payload must be purged after retention expires, while
  preserving the audit record and counters for operations.
- `UploadCapture` enforces the 60-second assertion window by requiring periodic
  `SignedEdgeAssertion` frames mid-stream, verified identically to opening
  assertions (procedure, body hash, nonce replay check). Why:
  `SignedEdgeAssertion` expires in 60 seconds by schema invariant
  (`model/edge/v1/assertion.proto`). If 60 seconds elapse without a valid
  re-assertion, central terminates the upload stream with
  `connect.CodeUnauthenticated` and transitions the session state to
  `CAPTURE_LIFECYCLE_FAILED` with stop reason `CAPTURE_STOP_REASON_ERROR`.
- `TailCaptureSession` subscribes to an in-memory broadcaster attached to the
  active `UploadCapture` session relay in `internal/captureapi`. Why: live
  tailing is an ephemeral observation of an in-flight stream for an operator.
  Coupling tailing to an in-memory broadcast channel avoids write amplification
  through JetStream or disk during live capture, while pcapng disk persistence
  satisfies subsequent `DownloadCaptureSession` calls.

## Requirements

1. `CaptureEdgeService.SubscribeCaptureAssignments` delivers owed assignments
   isolated by edge identity. Acceptance: an edge authenticating with
   `EdgeGlobalRef` edge-1 on `SubscribeCaptureAssignments` receives `start` for
   sessions configured with `ref.edge` edge-1; a session configured for edge-2
   is not delivered on edge-1's stream.
2. Central re-originates start assignments while a session is pending, and stops
   re-originating once upload starts. Acceptance: a session in
   `CAPTURE_LIFECYCLE_PENDING` is sent as a `start` assignment on stream open;
   upon receipt of the first chunk on `UploadCapture` for that session, central
   marks the session `CAPTURE_LIFECYCLE_RUNNING`, and a subsequent reconnect of
   `SubscribeCaptureAssignments` does not re-send the start assignment.
3. The edge deduplicates start and stop assignments without re-executing active
   or finished work. Acceptance: a second `start` assignment received for an
   already-running session produces a warning log and zero additional capture
   processes; a `stop` assignment for an active session cancels the capture
   context and triggers the upload of the final chunk, while a `stop` for an
   unknown session is discarded cleanly.
4. `UploadCapture` terminates and marks the session failed when re-assertion
   lapses past 60 seconds. Acceptance: an `UploadCapture` stream that sends
   packet chunks but delivers no `SignedEdgeAssertion` for 61 seconds is closed
   by central with Connect code `Unauthenticated`, and `GetCaptureSession`
   reports lifecycle `CAPTURE_LIFECYCLE_FAILED` with stop reason
   `CAPTURE_STOP_REASON_ERROR`.
5. `UploadCapture` rejects packet chunks whose session edge does not match the
   authenticated edge. Acceptance: an upload stream authenticated as edge-1 that
   submits a `CapturePacketChunk` naming `ref.edge` edge-2 is refused with
   Connect code `PermissionDenied`, leaving session edge-2's state untouched.
6. The stored artifact is written as a valid pcapng file and served by
   `DownloadCaptureSession`. Acceptance: a completed session that uploaded 100
   Ethernet packets produces `<StateDir>/captures/<session_id>.pcapng` whose
   SHA-256 digest, byte count, and packet count (100) match `CaptureArtifact`;
   calling `DownloadCaptureSession` yields `CaptureArtifactChunk`s <=1MB whose
   concatenated bytes pass `capinfos` with 100 packets and link type Ethernet.
7. `TailCaptureSession` streams live packet chunks from active uploads with
   identical sequences and counters. Acceptance: an operator calling
   `TailCaptureSession` during an active upload receives the stream of
   `CapturePacketChunk`s with monotonic sequences matching what the edge sent,
   including counters snapshots on each chunk.
8. Artifact expiry purges disk payload while retaining session records and
   counters. Acceptance: running the expiry pass on a session whose
   `artifact.expires_at` is in the past removes
   `<StateDir>/captures/<session_id>.pcapng` from disk; `GetCaptureSession`
   returns the record with intact counters and metadata, while
   `DownloadCaptureSession` returns Connect code `NotFound`.
9. Captured packet payload bytes never appear in logs, spans, or metrics on
   central. Acceptance: processing an `UploadCapture` stream with raw packet
   data under a captured `slog` handler and OTel exporter produces logs and spans
   containing session IDs, sequence numbers, and packet counts, with zero
   occurrences of the packet payload bytes.

## Out of scope

- Edge-side assembly of `src/modules/capture` and the upload client (U3c).
- Lab validation with physical switch SPAN/TZSP traffic (U3d).
- Fan-out of live tailing across multiple concurrent operator consumers or
  distributed central replicas.
- Cross-session indexing, packet search, or protocol dissection on central.

## Units

### Ub1. Schema and direction record amendments

Files: `spec/proto/flowseer/edge/capture/v1/capture_edge_service.proto`,
`spec/proto/flowseer/edge/capture/v1/README.md`,
`docs/architecture/2026-09-09-remote-packet-capture-direction.md`,
`docs/architecture/2026-08-20-network-model-structure-direction.md`
After: none
Change: `CaptureEdgeService` gains `SubscribeCaptureAssignments` with
`SubscribeCaptureAssignmentsRequest` and `SubscribeCaptureAssignmentsResponse`
(carrying a `oneof assignment` of `CaptureSessionConfig start = 1` and
`CaptureSessionGlobalRef stop = 2`); `spec/proto/flowseer/edge/capture/v1/README.md`
documents the assignment stream and resolves the open question on reaching the
edge; `docs/architecture/2026-09-09-remote-packet-capture-direction.md` is
amended to reverse the store-on-edge decision and specify central pcapng storage
under `<StateDir>/captures/` with bounded retention;
`docs/architecture/2026-08-20-network-model-structure-direction.md` is amended to
name both streams for `edge/capture`.
Tests: `buf format -d`, `buf lint`, and `test/conformance/proto/layering_test.go`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/edge/capture/v1 docs/architecture/2026-09-09-remote-packet-capture-direction.md docs/architecture/2026-08-20-network-model-structure-direction.md`

### Ub2. Capture session and artifact store

Files: `src/services/device/internal/captureapi/store.go`,
`src/services/device/internal/captureapi/store_test.go`,
`src/modules/edgebus/hub.go`
After: Ub1
Change: `src/modules/edgebus` defines `CapturesBucket = "captures"` in the
`CENTRAL` account; `src/services/device/internal/captureapi/store.go` implements
`Store`, which manages `CaptureSessionRecord` persistence in JetStream KV with
compare-and-set lifecycle updates, appends `PacketRecord` batches into
`<StateDir>/captures/<session_id>.pcapng` via `src/modules/capture`'s pcapng
renderer, computes artifact SHA-256 digests and packet totals upon
finalization, provides chunked artifact reads for download, and sweeps expired
artifacts past `expires_at` by deleting the on-disk file while preserving session
metadata and counters.
Tests: `store_test.go` verifies session record creation and CAS transitions,
pcapng artifact rendering and digest calculation checked against `capinfos`,
chunked artifact reading with 1MB offset slicing, and `SweepExpired` unlinking
disk files while keeping session records readable.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/captureapi/store.go src/services/device/internal/captureapi/store_test.go src/modules/edgebus/hub.go`

### Ub3. Edge capture service: assignments relay and upload receiver

Files: `src/services/device/internal/captureapi/edge_service.go`,
`src/services/device/internal/captureapi/edge_service_test.go`
After: Ub2
Change: `edge_service.go` implements
`captureedgev1connect.CaptureEdgeServiceHandler`. `SubscribeCaptureAssignments`
identifies the edge via `edgeapi.EdgeIDFromContext`, derives owed assignments
(start for `PENDING`, stop for cancellation), streams them, and re-derives on
store updates or resend ticks. `UploadCapture` authenticates opening and
mid-stream `SignedEdgeAssertion`s (closing with `CodeUnauthenticated` after 60
seconds without re-assertion), verifies `chunk.session.edge.edge.id == callingEdgeID`
(refusing mismatches with `CodePermissionDenied`), transitions `PENDING`
sessions to `RUNNING` on first chunk, broadcasts live chunks to active tails,
appends packets to the artifact store, and finalizes terminal state (`COMPLETED`
or `CANCELED`) on `final: true`.
Tests: `edge_service_test.go` verifies edge assignment isolation, withdrawal of
owed start upon first chunk upload, stream termination after 60 seconds without
re-assertion, rejection of foreign edge uploads, and artifact finalization on
stream completion.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/captureapi/edge_service.go src/services/device/internal/captureapi/edge_service_test.go`

### Ub4. Operator capture service: session CRUD, live tail, and download

Files: `src/services/device/internal/captureapi/operator_service.go`,
`src/services/device/internal/captureapi/operator_service_test.go`
After: Ub2
Change: `operator_service.go` implements
`capturev1connect.CaptureServiceHandler`. `CreateCaptureSession` validates bounds
and records `PENDING` sessions; `StopCaptureSession` transitions active sessions
toward cancellation; `GetCaptureSession`, `ListCaptureSessions`, and
`DeleteCaptureSession` manage records and purge artifact files;
`TailCaptureSession` streams live chunks via the in-memory broadcaster;
`DownloadCaptureSession` streams pcapng bytes in chunks <=1MB from the artifact
store, returning `CodeNotFound` when artifact payload has expired or been
deleted.
Tests: `operator_service_test.go` tests budget validation, CRUD operations,
pagination in listing, live chunk streaming on `TailCaptureSession`, 1MB chunked
downloading on `DownloadCaptureSession`, and `NotFound` responses on expired
payloads.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/captureapi/operator_service.go src/services/device/internal/captureapi/operator_service_test.go`

### Ub5. Device service host wiring and integration test

Files: `src/services/device/internal/host/host.go`,
`src/services/device/internal/captureapi/doc.go`, `src/services/device/README.md`,
`src/services/device/test/integration/capture_test.go`
After: Ub3, Ub4
Change: `host.go` initializes the captures KV bucket from the hub, constructs
`captureapi.Store` rooted at `filepath.Join(cfg.StateDir(), "captures")`,
mounts `CaptureServiceHandler` on the unauthenticated operator router, mounts
`CaptureEdgeServiceHandler` behind the assertion middleware, registers the
background artifact expiry sweeper module under the service supervisor, and
documents capture in `src/services/device/README.md`.
Tests: `capture_test.go` executes an end-to-end integration test: an operator
creates a session, an edge subscribes and receives the assignment, uploads
chunks with periodic re-assertion, an operator tails the live stream and
downloads the final pcapng file, and the artifact expiry sweeper purges the
payload file while retaining the session record.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device`

Waves: Ub1 | Ub2 | Ub3 Ub4 | Ub5

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/edge/capture/v1 docs/architecture src/services/device src/modules/edgebus
go test -race ./src/services/device/...
```

## Definition of done

- [x] Verifier green for every changed path in this phase.
- [x] `spec/proto/flowseer/edge/capture/v1/capture_edge_service.proto` updated with `SubscribeCaptureAssignments` and `spec/proto/flowseer/edge/capture/v1/README.md` updated with the open question resolved.
- [x] `docs/architecture/2026-09-09-remote-packet-capture-direction.md` amended to record central pcapng storage and reverse store-on-edge.
- [x] `docs/architecture/2026-08-20-network-model-structure-direction.md` amended to name both streams for `edge/capture`.
- [x] `src/services/device/internal/captureapi` implements `CaptureService` and `CaptureEdgeService`.
- [x] The device service host mounts both services and manages `<StateDir>/captures/`.
- [x] This plan's `status` set with an outcome note under its title, and parent plan U3b filled.
- [x] No plan labels in code, comments, or commit messages.

## Open questions

None.
