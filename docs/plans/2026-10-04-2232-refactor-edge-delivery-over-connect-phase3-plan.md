---
title: Edge Delivery over Connect, Phase 3, Agent Switch - Plan
type: refactor
date: 2026-10-04
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Edge Delivery over Connect, Phase 3, Agent Switch - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The agent writes syslog records and its own OTLP bodies into two segmented
logs and drains each through its delivery call. It no longer calls
`AttachBus` and starts no leaf node. The means are a drain loop over
`seglog`, a loopback OTLP receiver that appends to a log, and a narrower
publisher interface for the syslog source.

Stop condition: the plan is wrong if the end-to-end test cannot pass
without a leaf link.

## Decisions

The parent's decisions apply
([parent plan](2026-10-04-2232-refactor-edge-delivery-over-connect-plan.md)).
This phase adds:

- One drain per log. It takes a segment with `Next`, sends its records in
  one call, drops the first `settled` records from its copy when the count
  is short, retries the rest with backoff, and calls `Ack` when all are
  settled. Why: the resume point then lives in memory and a restart resends
  one segment at most, which central deduplicates for records.
- The syslog source's `Publisher` loses its subject argument and its
  `Subject` method. Why: the subject carried the tenant and the edge, and
  central now takes both from the assertion.
- The agent no longer learns its tenant. Why: `AttachBus` was the only
  call that told it (`src/edge/agent/internal/busattach/busattach.go`,
  `TenantFromSubjects`), and nothing else on the edge reads it.
- The loopback receiver moves from `src/modules/edgebus` into the agent
  and keeps its rule of accepting only uncompressed protobuf. Why: the
  agent is its only user, and the bodies are still forwarded unchanged.
- The pinned TLS configuration moves out of `src/modules/edgebus` to where
  `src/edge/agent/internal/identity` can use it without importing the hub.
- Buffer depth and oldest age come from `seglog.Stats` and are reported as
  the metrics `Leaf.BufferState` feeds today.

## Requirements

The parent's requirements 1, 2, 3, the edge half of 6, and 7 are claimed
here.

1. A syslog datagram reaches its typed stream with no leaf link. Example:
   the syslog source's tests pass against a central fixture that serves the
   ingest call and no bus listener.
2. A short `settled` count resumes at the right record. Example: with the
   handler settling two of five and then all, the handler has seen records
   three to five twice and one and two once.
3. A collector outage leaves device records flowing. Example: with the
   telemetry handler failing, a syslog record is delivered and the
   telemetry log's depth grows.
4. The agent's telemetry still starts before its first export. Example:
   the receiver's endpoint is bound before `service.Run` is called, as
   `src/edge/agent/host/host.go` orders it today.

## Out of scope

- Removing `StartLeaf`, the hub's edge accounts, and `AttachBus` from
  central and the schema, which the last phase does. This phase only stops
  the agent from using them.

## Open questions

- How `AgentBuffer` in `spec/proto/flowseer/store/agent/v1/agent_config.proto`
  splits its bounds between the two logs.
- Which tests under `src/services/device/test/integration` and
  `src/edge/agent/internal/syslogsource` start a hub and a leaf today, and
  what fixture replaces them.
- Whether the drain reuses `src/edge/agent/internal/subscribeloop`'s
  backoff, which doubles from 1 s to 30 s. The ceiling it picks is the
  bound the parent's first requirement names.
- What `MaxRecordBytes` each log gets, given the bounds the second phase
  sets on the two calls.
