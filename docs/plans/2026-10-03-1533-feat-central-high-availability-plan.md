---
title: Central High Availability - Plan
type: feat
date: 2026-10-03
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md
---

# Central High Availability - Plan

## Goal

Two or more device service processes serve one deployment, and losing one of
them or one NATS server loses no acknowledged write and no edge. The means
are a separate edge listener, an upstream NATS cluster in place of the
embedded hub, and an election for the background work that must run once.

Stop condition: the plan is wrong if an edge's dispatch stream cannot be
served by a replica other than the one that admitted the mutation.

## Decisions

The shared decisions are in the
[central high availability record](../architecture/2026-10-03-central-high-availability-direction.md),
proposed and awaiting acceptance. The ones the user settled:

- The edge-facing services move to their own listener. Why: the process then
  enforces the boundary and edges keep pinning central's key. (decided by the
  user, 2026-10-03)
- NATS leaves the device service and runs as an upstream `nats-server`
  cluster. Why: upstream handles clustering and upgrades, and R3 streams
  become possible. (decided by the user, 2026-10-03)
- Central runs as several replicas. (decided by the user, 2026-10-03)
- Every central component is ready for high availability, as the record
  defines it. (decided by the user, 2026-10-03)
- Three phases in a chain. Why: each changes
  `src/services/device/internal/host` and the one before it decides what the
  next finds there.

## Requirements

1. An operator procedure is not served on the edge listener, and an edge
   procedure is not served on the API listener. Example: a call to
   `DeviceService/GetDeviceAccessStatus` on the edge address returns HTTP 404.
2. Central starts against an external NATS cluster and starts no server of
   its own. Example: with `nats-server` stopped, central exits with a NATS
   connection error and writes nothing under `state_dir`.
3. Two central replicas serve one deployment without doing background work
   twice. Example: with two replicas and one managed interface, one drift
   read is dispatched per interval.
4. Losing one central replica or one NATS server loses no acknowledged write
   and no edge. Example: with one of each killed, an admitted mutation still
   reaches its edge and completes.
5. A replica that cannot serve reports not ready. Example: the readiness
   endpoint returns 503 while the NATS connection is down.

## Out of scope

- Kubernetes manifests and the NATS chart, which the deployment plan writes.
- Edge groups of one to n nodes, which the edge high availability plan
  decides.
- Authorization for the operator services.
- More than one region or cluster.

## Units

### U1. Edge listener split

Files: docs/plans/2026-10-03-1533-feat-central-high-availability-phase1-plan.md
After: none
Landed:

### U2. External NATS

Files: docs/plans/2026-10-03-1533-feat-central-high-availability-phase2-plan.md
After: U1
Landed:

### U3. Central as several replicas

Files: docs/plans/2026-10-03-1533-feat-central-high-availability-phase3-plan.md
After: U2
Landed:

Waves: U1 | U2 | U3

## Verification

Each phase runs the verifier on its changed paths. After the last phase, run
two device service processes against a three-server NATS cluster on one
host, enroll one agent, kill one process and one server in turn, and confirm
after each that a mutation completes.

## Definition of done

- [ ] Verifier green for every changed path in every phase.
- [ ] The central high availability record is accepted.
- [ ] `src/services/device/README.md` and `src/modules/edgebus/README.md`
      describe the external cluster and the replicas.
- [ ] This plan's `status` is set, with an outcome note under its title.
- [ ] No plan labels in code.

## Open questions

The record's open questions carry over. None blocks the first phase.
