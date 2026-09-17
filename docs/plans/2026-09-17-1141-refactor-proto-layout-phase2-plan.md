---
title: Protobuf Tree Phase 2 - The Edge Plane, the Northbound API, and the Event Root - Plan
type: refactor
date: 2026-09-17
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
parent: docs/plans/2026-09-17-1141-refactor-proto-layout-plan.md
---

# Protobuf Tree Phase 2 - The Edge Plane, the Northbound API, and the Event Root - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Every Connect service sits under the root that names its plane: `api/` for
what an operator calls, `edge/` for what central and an enrolled edge
exchange. `DeviceOperationEvent` lives in `event/access`, `integration/`
holds only its README, and the `edge/` and `event/` roots and each new
package carry the README shape phase 1 fixed. The means: five `git mv`
units that rename the Connect route prefixes the agent's tests spell out,
and one that reserves `integration/`.

## Decisions

The parent's Decisions hold. The moves this phase makes:

| From | To | Holds |
| --- | --- | --- |
| `api/edge/v1` (`edge_service`, `bus`, `device`, `credential`) | `edge/attach/v1` | `EdgeService` and its request, response, and stream messages |
| `api/edge/v1` (`edge_admin_service`) | `api/edge/v1` | `EdgeAdminService`, unchanged path, now the only file |
| `integration/device/v1` | `edge/dispatch/v1` | `DispatchService`, the execution envelope |
| `event/device/v1` (`audit_service`) | `edge/audit/v1` | `AuditService.Deliver` |
| `event/device/v1` (`operation_event`) | `event/access/v1` | `DeviceOperationEvent` and its nine kinds |
| `api/capture/v1` (`capture_edge_service`) | `edge/capture/v1` | `CaptureEdgeService.UploadCapture`; the chunk is in `model/capture` since phase 1 |

- The Go package names become `attachv1`, `dispatchv1`, `auditv1`,
  `capturev1`, and `accessv1` for the event, which collides with
  `model/access`'s `accessv1`; the event package is aliased `accesseventv1`
  where both are imported. Why: the parent accepts cross-root collisions.
- The Connect route prefixes change with the package, so
  `src/edge/agent/internal/identity/client_test.go` and the assertion's
  audience derivation in `assertion.go` are updated in the same unit as the
  service that moves. Why: the audience is the route, and a test spelling
  the old one would pass against nothing.
- `importOrder` after this phase has `edge/attach`, `edge/dispatch`,
  `edge/audit`, `edge/capture`, `api/edge`, `api/device`, and `api/capture`
  as sinks importing `model/*`, `event/access`, `errs`, and `net/*` as each
  needs; `event/access` imports `model/access` and `model/inventory`;
  `orderedRoots` adds `flowseer/edge` and keeps `flowseer/integration`,
  which holds no `.proto` and which the walk tolerates.
- The route change is the wire break the parent states: every edge signs
  the new route as its audience, and `src/edge/agent/internal/identity/assertion_test.go`,
  `test/conformance/proto/api_edge_rules_test.go`, and
  `src/services/device/internal/edgeapi/middleware_test.go` spell the old
  one and change in the unit that moves `EdgeService`.

## Requirements

1. `spec/proto/flowseer/edge/` holds `attach`, `dispatch`, `audit`, and
   `capture`; `spec/proto/flowseer/integration/` holds only `README.md`;
   `spec/proto/flowseer/event/` holds `access`. Example: `buf lint` passes
   and `find spec/proto/flowseer/integration -name '*.proto'` prints nothing.
2. The agent enrolls, heartbeats, and subscribes against the new route
   names. Example: the device service integration test under
   `src/services/device/test/integration` passes with a request path of
   `/flowseer.edge.attach.v1.EdgeService/Heartbeat`.
3. The five conformance rules tests whose names encode the old paths are
   renamed and pass unchanged in body: `api_edge_bus_credential_rules_test.go`
   to `edge_attach_rules_test.go`, `integration_device_rules_test.go` to
   `edge_dispatch_rules_test.go`, `event_device_rules_test.go` split into
   `edge_audit_rules_test.go` and `event_access_rules_test.go`.
4. Both README gates pass with the new directories, and the structure
   record's tree marks `edge/` and `event/access` as landed.

## Out of scope

- Any new RPC, including the capture command the remote capture record
  leaves open.
- `store/edge` and `service/v1`. Phase 3.
