---
title: Edge Delivery over Connect - Plan
type: refactor
date: 2026-10-04
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md
---

# Edge Delivery over Connect - Plan

## Goal

An edge delivers its ingest records and its own telemetry to central through
Connect calls, buffers them in segment files while central is unreachable,
and starts no NATS server. The means are a segmented log in the agent, two
delivery calls on the edge-facing services, and the removal of the leaf
link with everything that exists only to serve it.

Stop condition: the plan is wrong if the ingest call cannot sustain the load
the device service record states, as the benchmark in the second phase
measures it.

## Decisions

The shared decisions are in the
[edge delivery record](../architecture/2026-10-04-edge-delivery-over-connect-direction.md),
proposed and awaiting acceptance. The ones the user settled:

- The leaf link is replaced, not kept. Why: since the device service
  record's 2026-09-06 amendment it carries one-way traffic only, and it
  costs a second listener, a second credential system, and an account per
  edge. (decided by the user, 2026-10-04)
- The edge buffer is a segmented log written in this repository. Why: no
  maintained standalone library fits, as the record's Alternatives list.
  (decided by the user, 2026-10-04)
- Telemetry travels in a typed Connect call, and central keeps the retry and
  drop rules. Why: the rules then change without updating agents in the
  field. (decided by the user, 2026-10-04)

The ones this plan adds:

- The log lives in `src/edge/agent/internal/seglog`. Why: `src/common`
  takes a package only when two unrelated trees import it
  (`src/common/README.md`, "What belongs here"), and the agent is the only
  consumer today.
- Device records and telemetry get separate logs and separate drains. Why:
  telemetry volume cannot then evict a device record, and a collector
  outage cannot hold device records back.
- Central's embedded server keeps its operator, system, and CENTRAL
  accounts. Only the edge accounts, the leaf listener, and the WebSocket
  listener go. Why: the CENTRAL store layout stays readable, and the central
  high availability plan replaces the embedded server anyway.
- `src/modules/edgebus` keeps its name through this plan. Why: renaming it
  touches every central package and belongs with the change that moves NATS
  out of the device service.
- Four phases. The log and the central handlers share no file and run at
  once. The agent switch needs both, and the removal needs the switch. Why:
  until the agent has switched, the tests that prove the leaf link must
  keep passing.
- The log lands in a phase of its own, ahead of its only importer. Why: it
  depends on nothing central, so it is built while the handlers are, and
  the agent switch would not fit one session with it.

## Requirements

1. A record appended while central is unreachable is delivered after it
   returns. Example: with the handler refusing for ten seconds, a syslog
   datagram received in that window is in `FLOWSEER_INGEST_SYSLOG` within
   one backoff ceiling of the handler accepting again.
2. Each log is bounded by bytes and by age and discards its oldest segment
   first. Example: with a 2 MiB bound and 3 MiB appended, the log holds at
   most 2 MiB and its discard counter is above zero.
3. A damaged byte costs at most one segment and is counted. Example: with
   one byte flipped in the middle segment of three, the records of the other
   two are delivered and the corrupt-segment counter reads 1.
4. Central takes the edge from the assertion and the tenant from its edge
   store. Example: a record delivered by an edge of tenant A is published on
   `flowseer.<A>.ingest.syslog.<device-id>`, whatever its payload names. The
   assertion is the `SignedEdgeAssertion` in the Authorization header, the
   handler reads the edge with `edgeIDFromContext`, and the tenant comes
   from `busResources.edgeTenant`
   (`src/services/device/internal/host/host.go`).
5. A batch delivered twice is stored once inside the stream's duplicate
   window. Example: the same request sent twice leaves one message per
   record in the typed stream.
6. A collector outage does not delay device records. Example: with the
   collector answering 503, a syslog record still reaches its typed stream,
   and the telemetry log's depth grows.
7. The agent starts no NATS server and dials no remote address other
   than `central_url` and its devices. Example: the end-to-end test passes
   with no bus listener bound anywhere, and the agent holds no NATS
   connection.
8. Central binds no leaf or WebSocket listener, and `AttachBus` is gone.
   Example: `ServiceListeners` has no `bus` field, and
   `spec/proto/flowseer/edge/attach/v1/bus.proto` does not exist.

## Out of scope

- The process-local bus in `src/common/service`. Neither host sets
  `service.Config.Bus`, and the agent keeps linking `nats-server` through
  that package until someone decides its fate.
- Renaming `src/modules/edgebus`.
- Moving NATS out of the device service, which
  `docs/plans/2026-10-03-1533-feat-central-high-availability-plan.md` owns.
  Its second phase is re-planned after this plan's last phase, since it
  currently plans per-edge accounts on an external server.
- Delivery from an adapter host. `GOALS.md` leaves open how its
  observations get a tenant, and the handler here resolves Edges only.

## Units

### U1. Segmented log

Files: docs/plans/2026-10-04-2232-refactor-edge-delivery-over-connect-phase1-plan.md
After: none
Landed:

### U2. Delivery calls and central handlers

Files: docs/plans/2026-10-04-2232-refactor-edge-delivery-over-connect-phase2-plan.md
After: none
Landed:

### U3. The agent switches to the logs and the drains

Files: docs/plans/2026-10-04-2232-refactor-edge-delivery-over-connect-phase3-plan.md
After: U1, U2
Landed:

### U4. Removal of the leaf link and the record amendments

Files: docs/plans/2026-10-04-2232-refactor-edge-delivery-over-connect-phase4-plan.md
After: U3
Landed:

Waves: U1 U2 | U3 | U4

## Verification

Each phase runs the verifier on its changed paths. After the last phase,
`go test -race ./...` passes, and the lab run in `deploy/lab` enrolls one
agent, receives a syslog datagram from a lab device, and shows the record
in the typed stream with the agent holding one connection to central.

## Definition of done

- [ ] Verifier green for every changed path in every phase.
- [ ] The edge delivery record is accepted, and the five records it amends
      say what the tree does.
- [ ] `GOALS.md` no longer states NATS as the fabric between edge and
      central.
- [ ] `src/modules/edgebus/README.md`, `src/edge/agent/README.md`, and
      `src/services/device/README.md` describe the delivery calls.
- [ ] This plan's `status` is set, with an outcome note under its title.
- [ ] No plan labels in code.

## Open questions

- `docs/plans/2026-10-04-2023-fix-central-intake-review-items-plan.md`
  edits the edge follower and the edgebus prose that the last phase
  deletes. Whether it lands first or its first unit is dropped is the
  user's call before the second phase is re-planned.
- The record's open questions carry over. None blocks the first phase.
