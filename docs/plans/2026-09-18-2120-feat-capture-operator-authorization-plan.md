---
title: Remote Packet Capture Operator Authorization - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
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
- The choice of interim versus unified authorization architecture belongs to
  the user and is recorded as an Open question. Why: Deciding whether to block
  capture authorization until OpenFGA direction is accepted, introduce an
  edge-level policy handle (`CapturePolicyHandle`), or wire an interim
  JWT/subject validator interceptor commits the repository to an architectural
  direction across multiple services.
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

1. Which authorization architecture should govern operator actions on `CaptureService`?
   - Option 1 (Recommended): Repository-wide OpenFGA direction. Defer
     service-level enforcement until the OpenFGA architecture record lands for
     all operator and admin services (`DeviceService`, `EdgeAdminService`, and
     `CaptureService`), as established in `GOALS.md` line 29 and
     `src/services/device/README.md` lines 162-167. In this phase, implement
     schema alignment (U1) to adopt `flowseer.model.access.v1.OperatorRef` and
     document the OpenFGA relations for capture (`edge#capture`,
     `session#download`, `tenant#full_payload`). This avoids inventing a
     disposable authorization engine.
   - Option 2: Interim Capture Policy Handle. Add `CapturePolicyHandle` to
     `flowseer.model.policy.v1` (analogous to `AccessPolicyHandle` on device
     mutations) and attach policy constraints to `EdgeConfig` or tenant
     configuration. Each capture request must pin a policy version defining
     allowable filters, max durations, and full-payload permissions.
   - Option 3: Process-level identity interceptor. Add Connect middleware to
     `src/services/device/internal/host/` that inspects HTTP headers (such as
     `Authorization: Bearer <jwt>`), verifies caller subject with Zitadel, and
     enforces a static role-based check prior to OpenFGA.
   Reason this decision belongs to the user: Committing to an authorization
   architecture commits the project to a direction across multiple services and
   policy surfaces.
