---
title: Edge Delivery over Connect, Phase 4, Removal and Record Amendments - Plan
type: refactor
date: 2026-10-04
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md
---

# Edge Delivery over Connect, Phase 4, Removal and Record Amendments - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Nothing in the tree serves or describes the leaf link. The means are
deleting the leaf node, the per-edge accounts, the per-edge streams with
their follower and forwarder consumers, `AttachBus`, and the bus listener,
and amending the records and documents that describe them.

Stop condition: the plan is wrong if a central module still needs a
per-edge stream after the agent has switched.

## Decisions

The parent's decisions apply
([parent plan](2026-10-04-2232-refactor-edge-delivery-over-connect-plan.md)).
This phase adds:

- `EdgeService.AttachBus` and `bus.proto` are deleted without a reserved
  placeholder beyond what `docs/code-style-proto.md` requires. Why:
  `AGENTS.md` says a breaking change needs no compatibility path before the
  first stable release.
- `ServiceListeners.bus` is removed and the hub binds no leaf or WebSocket
  listener. The embedded server keeps its operator, system, and CENTRAL
  accounts.
- The intake module becomes the ingest handler alone, and the forwarder
  module is removed in favor of the telemetry handler.
- Each record named in the edge delivery record's `amends` is edited in
  this phase, and the user is asked to accept the edge delivery record.

## Requirements

The parent's requirement 8 is claimed here.

1. Central binds one edge-facing listener. Example: a configuration with a
   `listeners.bus` field is refused at load.
2. The schema holds no bus attachment. Example: `buf lint` passes with
   `bus.proto` deleted, and no file under `src/` references `AttachBus`.
3. No per-edge NATS state is created. Example: after an edge enrolls and
   delivers, the hub's key directory holds no edge account key and the
   server reports no `FLOWSEER_EDGE_` stream.
4. The documents match the tree. Example: no file under `docs/`, no
   `README.md`, and no `.proto` comment describes a leaf node as present.

## Out of scope

- The process-local bus in `src/common/service`.
- Renaming `src/modules/edgebus`.

## Open questions

- Which captured solutions are deleted and which are refreshed:
  `docs/solutions/architecture-patterns/a-restarted-hub-must-re-attach-every-persisted-edge-account.md`
  and
  `docs/solutions/architecture-patterns/per-account-jetstream-disk-budgets-reserve-against-the-server-store-ceiling.md`
  describe state this phase removes.
- Whether the dependency statements for `github.com/nats-io/jwt/v2` and
  `github.com/nats-io/nkeys` under `docs/dependencies/statements/` still
  hold once only the CENTRAL account is signed.
- How the open central high availability, deployment, and edge high
  availability plans are re-planned against the amended records.
