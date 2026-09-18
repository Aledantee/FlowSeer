---
title: Offered-Load Streams Phase 2 - Stated Egress Buffers and Tail Drop - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-18-0000-feat-netsim-offered-load-streams-plan.md
---

# Offered-Load Streams Phase 2 - Stated Egress Buffers and Tail Drop - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

An egress queue whose configuration states a buffer drops the frame that would
overflow it, and a queue with no stated buffer says that its loss figure is
unknown once it backs up. The means: a per-PCP buffer size on
`traffic.PortQueues`, an accessor on the switch, octet accounting in the
fabric's `egressQueue`, a `queue-full` drop with a typed fact, and a
`queue-buffer-unstated` issue. The plan is wrong if the lab switches share one
buffer pool across ports, because a per-queue size then models nothing they
report.

## Decisions

- The parent's decisions and the direction record apply.
- The field sits beside `MaxRateBPS` on `traffic.PortQueues`
  (`src/common/netsim/vswitch/traffic/config.go:59-61`). Why: it is the only
  per-port, per-PCP surface egress scheduling reads. The sites a new field
  there touches are `Clone`, `Validate`, the accessor beside `MaxRate`, a fact
  and diff loop in `traffic/diff.go` mirroring `MaxRateFact`, a
  `Switch.QueueBuffer` beside `QueueMaxRate` (`vswitch/switch.go:465-471`), the
  `vswitch` README, and `enqueueEgress` in `fabric/run.go`.
- The buffer counts encoded frame octets, not wire octets. Why: a switch
  buffers the frame, not its preamble and gap. Unconfirmed; the re-plan checks
  a vendor buffer specification under `spec/` before keeping it.
- The drop constructs a `trace.Step` with a fact naming depth and limit. Why:
  `trace_producer_conformance_test.go` fails a step literal with no facts.
- A host's cable end is an egress queue like any other (`run.go:284`). Hosts
  have no `traffic.Config`, so a host queue is always unstated. The re-plan
  decides whether `fabric.Host` gains a buffer field or whether a host above
  line rate is simply reported by its issue.
- The unstated-buffer issue has status `Incomplete` and the port's scope, the
  shape of `propagation-unknown` (`fabric/fabric.go:866-874`). It is raised
  once per queue when depth first passes one maximum-size frame for the port's
  MTU, and the cache phase 1 added is cleared when it is.

## Requirements

6. Two hosts send 1 Gbit/s each to a third over gigabit links for 10 ms, with
   a 64,000-octet buffer on the egress port's PCP 0 queue. The port's
   `OutDiscards` and `Discards["queue-full"]` are equal and nonzero, the flow
   statistics carry the same count, and delivered plus dropped equals offered.
7. The same run with no buffer stated drops nothing, `Snapshot` reports the
   queue's peak depth in octets, and the flows' metadata carries
   `queue-buffer-unstated` with status `Incomplete`.
8. A buffer of 0 for a PCP is refused by `Validate`; `Diff` of two configs
   that differ only in a buffer yields one change with the buffer fact;
   `Clone` of a config is independent of the original's map.

## Open questions

- Host queues, as above.
- Whether peak depth belongs on `Snapshot`, on `Counters`, or on both.
- A new conformance corpus case needs the admission bar in
  `src/common/netsim/internal/netsimtest/README.md`; phase 3 claims the
  cases, and this phase decides whether one lands earlier.
