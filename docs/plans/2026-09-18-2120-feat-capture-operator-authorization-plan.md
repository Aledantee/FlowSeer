---
title: Remote Packet Capture Operator Authorization - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-09-09-remote-packet-capture-direction.md
---

# Remote Packet Capture Operator Authorization - Plan

## Goal

Authorize operator requests on `CaptureService` (`CreateCaptureSession`,
`StopCaptureSession`, `GetCaptureSession`, `ListCaptureSessions`,
`DeleteCaptureSession`, `TailCaptureSession`, `DownloadCaptureSession`) so that
operators may only initiate, observe, or download captures on edges,
interfaces, and filters they are permitted to access. The means is establishing
an operator identity and authorization boundary aligned with FlowSeer's
architecture rather than an ad-hoc scheme, updating schema provenance to cite
structured operator identities, and emitting audit events on capture artifact
downloads. Stop condition: This plan is wrong if capture authorization is
decoupled from the repository-wide OpenFGA authorization architecture for
operator and admin services.

## Decisions

- Capture authorization must not invent an ad-hoc authorization scheme for
  packet capture alone. Why: `GOALS.md` line 29 and
  `src/services/device/README.md` lines 162-167 establish that operator and
  admin surfaces (`DeviceService`, `EdgeAdminService`, and `CaptureService`)
  share an authorization roadmap centered on OpenFGA. In the interim, all three
  services run on the device service API port without Connect-level caller
  authentication, relying on deployment network boundary isolation. Creating an
  isolated authorization check solely inside `src/services/device/internal/captureapi`
  would diverge from `DeviceService` and `EdgeAdminService` and introduce a
  throwaway authorization mechanism.
- `CaptureAuthorization.operator` aligns with
  `flowseer.model.access.v1.OperatorRef` rather than remaining a raw
  unstructured string. Why:
  `spec/proto/flowseer/model/access/v1/operation.proto` defines `OperatorRef`
  carrying the identity provider's stable subject (`subject`). Using
  `OperatorRef` aligns capture provenance with `model/access/v1` and prepares
  for identity provider integration (Zitadel subject propagation).
- Capture follows the repository-wide OpenFGA direction rather than carrying
  an authorization mechanism of its own. Service-level enforcement waits for
  the OpenFGA record that covers `DeviceService`, `EdgeAdminService`, and
  `CaptureService` together; this plan does schema alignment only, adopting
  `OperatorRef` and naming the relations capture will need (`edge#capture`,
  `session#download`, `tenant#full_payload`) so that record has them to work
  from. Why: [GOALS.md](../../GOALS.md) already decides that the operator and
  admin API surfaces are authorized through OpenFGA, and
  [the device service README](../../src/services/device/README.md) records the
  present gap as an accepted deferral behind the deployment's network
  boundary. An interim policy handle or JWT interceptor would be a second
  mechanism to remove once that record lands. The user took this decision on
  2026-09-18, choosing it over an interim `CapturePolicyHandle` and over a
  process-level identity interceptor.
- Artifact download must emit a durable audit event when served. Why:
  [docs/architecture/2026-09-09-remote-packet-capture-direction.md](../architecture/2026-09-09-remote-packet-capture-direction.md)
  line 182 specifies that an artifact is served only to a caller authorized for
  that session, and a download is itself an event. Emitting an audit event on
  download provides accountability for access to raw traffic bytes.

## Requirements

1. `CaptureAuthorization` references `flowseer.model.access.v1.OperatorRef`.
   Acceptance: A `CreateCaptureSessionRequest` whose `authorization.operator`
   specifies `subject: "zitadel|usr_123"` validates successfully; a request
   missing `operator` or with an empty `subject` fails with Connect code
   `InvalidArgument`.
2. `CreateCaptureSession` evaluates authorization against caller subject, target
   edge, filter, and payload mode. Acceptance: An operator without permission
   to perform promiscuous capture or read full payloads on `EdgeGlobalRef`
   edge-1 receives Connect code `PermissionDenied`.
3. Read and control RPCs (`GetCaptureSession`, `ListCaptureSessions`,
   `StopCaptureSession`, `DeleteCaptureSession`, `TailCaptureSession`,
   `DownloadCaptureSession`) enforce session access boundaries. Acceptance: An
   operator calling `DownloadCaptureSession` for a session they are not
   authorized to view receives Connect code `PermissionDenied`.
4. Artifact download emits a durable audit event on completion. Acceptance: A
   successful `DownloadCaptureSession` stream triggers an audit event recording
   the operator subject, session ID, bytes served, and timestamp.

## Out of scope

- Edge-side packet capture execution and filtering (already landed in
  `src/modules/capture`).
- Implementation of the central OpenFGA store and engine.
- Device mutation authorization or device credential delegation.

## Units

### U1. Schema alignment for CaptureAuthorization

Files: `spec/proto/flowseer/model/capture/v1/capture_session.proto`,
`spec/proto/flowseer/model/capture/v1/README.md`
After: none
Change: `CaptureAuthorization` replaces `string operator = 1` with
`flowseer.model.access.v1.OperatorRef operator = 1 [(buf.validate.field).required = true]`.
Tests: `test/conformance/proto/layering_test.go`, `buf lint`, and
`buf format -d`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model/capture/v1`

### U2. Operator authorization evaluation in OperatorService

Files: `src/services/device/internal/captureapi/operator_service.go`,
`src/services/device/internal/captureapi/operator_service_test.go`
After: U1
Change: `OperatorService` checks caller authorization on `CreateCaptureSession`,
`StopCaptureSession`, `GetCaptureSession`, `ListCaptureSessions`,
`DeleteCaptureSession`, `TailCaptureSession`, and `DownloadCaptureSession`,
returning Connect code `PermissionDenied` when unauthorized.
Tests: `operator_service_test.go` exercises unauthorized creation, forbidden
full-payload capture, unauthorized session tailing and download, and valid
authorized sessions.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/captureapi`

### U3. Download audit event generation

Files: `src/services/device/internal/captureapi/operator_service.go`,
`src/services/device/internal/captureapi/operator_service_test.go`
After: U2
Change: `DownloadCaptureSession` emits an audit record via the service audit
publisher upon successfully streaming artifact chunks.
Tests: `operator_service_test.go` verifies that an audit record is published
upon download completion.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/captureapi`

Waves: U1 | U2 | U3

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model/capture/v1 src/services/device/internal/captureapi
go test -race ./src/services/device/...
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `spec/proto/flowseer/model/capture/v1/capture_session.proto` updated with `OperatorRef`.
- [ ] `docs/architecture/2026-09-09-remote-packet-capture-direction.md` amended to reflect the decided authorization model.
- [ ] `src/services/device/internal/captureapi/operator_service.go` enforces authorization and publishes download audit events.
- [ ] Package READMEs updated.
- [ ] No plan labels in code.

## Open questions

None. The authorization architecture question was decided by the user on
2026-09-18 and is recorded in Decisions.
