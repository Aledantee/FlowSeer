---
title: Operator Authorization Phase 3, Enforcement On, Projector, and Stamped Identity - Plan
type: feat
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-30-1139-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 3, Enforcement On, Projector, and Stamped Identity - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`DeviceService`, `EdgeAdminService`, and `CaptureService` serve behind the
authentication and authorization interceptors, the edge-facing services do
not. A projector keeps OpenFGA's relationships for edges, devices, and
capture sessions derived from their records in JetStream KV.
`CreateCaptureSession` requires `tenant#full_payload` when full payload is
requested. Every call of `EdgeAdminService` and every full-payload capture
lands in an operator action trail naming the principal, in the same change
that turns enforcement on. Handlers whose
rule defers the check call `Require` or `Filter`, and every identity a
handler records comes from the authenticated principal.

## Decisions

The parent plan's Decisions apply. To settle when this phase is planned:

- The interceptor order on operator handlers: telemetry, authentication,
  validation, authorization. Authorization reads request fields, so it runs
  after validation.
- The operator action trail's record shape, storage, and retention, and how
  it differs from the device-scoped audit stream.
- The projector's relationship shapes, how it detects and repairs drift,
  and how a revoking removal deletes relationships before the RPC returns.
- The `OperatorRef` change to issuer plus subject, the request fields that
  stop being caller-written (`MutationIntent.actor`,
  `CaptureAuthorization.requested_by`, `AbandonMutationRequest.actor`,
  `ResolveDesynchronizationRequest.actor`), and the journal digest's
  `actorPart` (`src/services/device/internal/journal/journal.go`).
- Streaming rules for `TailCaptureSession` and `DownloadCaptureSession`.
- A denied-edge case in every list handler's tests, since the interceptor
  cannot see whether a handler used `Filter`'s answer.

## Requirements

Parent requirements 1, 2, 4, 5, 7, 9, 10, and 11 hold against the running device
service, and the edge-facing services behave as before.
