---
title: Offered-Load Streams Phase 2 - Stated Egress Buffers and Tail Drop - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
parent: docs/plans/2026-09-18-0000-feat-netsim-offered-load-streams-plan.md
---

# Offered-Load Streams Phase 2 - Stated Egress Buffers and Tail Drop - Plan

> Implemented. 3 units, 2026-09-23T18:49Z to 2026-09-23T19:47Z.

> Re-planned 2026-09-23 against the tree that holds phase 1's landed units.

## Goal

An egress queue whose configuration states a buffer drops the frame that would
overflow it, and a queue with no stated buffer says that its loss figure is
unknown once it backs up. The means: a per-PCP buffer on `traffic.PortQueues`,
octet accounting in the fabric's `egressQueue`, a `queue-full` drop with a
typed fact, and a `queue-buffer-unstated` issue on the port's scope. The plan is
wrong if the lab switches share one buffer pool across ports, because a
per-queue size then models nothing they report; the phase-5 lab comparison
would show it.

## Decisions

- The parent's decisions and the offered-load direction record apply; this phase
  lands the record's buffer bullet.
- `traffic.PortQueues` gains `BufferOctets map[vlan.PCP]uint64`, beside
  `MaxRateBPS` (`src/common/netsim/vswitch/traffic/config.go:59-61`), and only
  that field. Why: it is the per-port, per-PCP surface egress scheduling already
  reads, so a caller states a buffer where it states a queue rate. Absent PCP
  means unbounded, matching `MaxRateBPS` and the direction's "a buffer when the
  configuration states one". Every site the field touches is named in U1.
- The buffer counts encoded frame octets: the length `ethernet.Frame.Encode`
  returns, the same figure `transmit` passes to `countEgress` (`run.go:877-878`)
  and the `frameOctets` helper the mirror facts use
  (`src/common/netsim/vswitch/switch.go:1896-1897`). Why: a switch buffers the
  frame, not the wire's preamble, start delimiter, and interpacket gap. The
  re-plan checked the vendor sources under `spec/`: no buffer size there states
  its unit. Cisco IOS-XE expresses a queue buffer as a relative `ratio` or a
  unitless `max-buffers` count
  (`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-policy.yang:2393`, `:3131`), and the
  IETF DiffServ target reports `queue-size-bytes` as "Number of bytes currently
  buffered" (`spec/yang/cisco/iosxe/2611/ietf-diffserv-target.yang:145`), which
  is the frame's own bytes. The field comment states the unit.
- Depth and peak are tracked per (endpoint, PCP) on the `egressQueue`, in the
  same encoded octets. `enqueueEgress` adds, the pop in `serve` subtracts, and
  the peak keeps the maximum. Why here: the queue is the only place that knows
  what is waiting, and its pop (`run.go:991-999`) is the only site an item
  leaves it.
- A stated buffer tail-drops in `enqueueEgress` before the item is appended: the
  frame that would make `depth+octets` exceed the buffer records an `EntryDrop`
  with `traffic.ReasonQueueFull`, counts through `countEgressDrop` so
  `OutDiscards` and `Discards["queue-full"]` move together, and carries a
  `trace.Step` on layer `traffic` whose input is a `traffic.QueueDropFact`
  naming the depth, the buffer, and the frame's octets. Why the Step: the
  producer audit fails a step literal with no facts
  (`src/common/netsim/fabric/trace_producer_conformance_test.go`). The dropped
  frame joins no in-flight count, so it settles at the end of the call and folds
  its drop into its flow like any other.
- `Snapshot` gains `EgressDepths map[Endpoint]map[vlan.PCP]EgressDepth`, the
  depth and peak in octets. Why not `Counters`: a peak is a state reading, the
  `Counters` shape follows RFC 2863, and requirement 7 reads it from
  `Snapshot`. It stays out of `Snapshot.Fingerprint` and out of `Compare`'s
  `diffSnapshot`, because queue depth is transient and would defeat convergence
  and equivalence the way a kept timer does
  (`docs/solutions/conventions/a-convergence-fingerprint-lies-when-it-omits-a-resolution-axis-or-keeps-a-timer.md`).
- A host's cable end stays an egress queue with no buffer field on `fabric.Host`
  and no host queue configuration. Why: the parent names this alternative, a
  host has no `traffic.Config` to state one, and an invented default is exactly
  what the direction rejects. A host above its line rate is reported by its
  issue, as any unstated queue is.
- A buffer stated on a LAG name is enforced on each member's egress queue, the
  way `QueueMaxRate` already meters each member's `rateClock` separately
  (`run.go:1017-1022`). `Snapshot.EgressDepths` is therefore keyed by the
  member's `Endpoint`, not the LAG name.
- The unstated-buffer issue lives in `Fabric.Metadata()`, scoped to the
  endpoint (`analysis.PortScope` for a switch port, `analysis.NodeScope` for a
  host), with status `analysis.Incomplete` and code `queue-buffer-unstated`. It
  is recorded once per endpoint, when the depth of one of its PCP queues,
  counted with the frame just enqueued included, first passes one maximum-size
  frame for the port's MTU; the same call attaches the issue to the journey of
  that frame and clears `f.metadataCache`. Why: this is the shape of
  `propagation-unknown` (`fabric.go:1104`), and `record` merges the scoped
  `Metadata()` into every entry whose dependencies overlap, so the issue reaches
  a journey and folds into its flow. The direct attach covers a run whose
  crossing frame is the last one, which no later hop would carry the issue for.
  This amends phase 1's decision that `SetFault` is the one
  writer of `Metadata()`'s inputs: a queue crossing is a second invalidation
  site, and the fabric README's "so a later `SetFault` changes it" sentence
  (`README.md` Topology metadata) is corrected in U3's change.
- One maximum-size frame is the port's MTU plus 18 octets (a 14-octet header
  and one 4-octet VLAN tag). An unset MTU (the forwarding path treats `MTU == 0`
  as no limit, `switch.go:2303`) and a host queue use the standard 1500-octet
  MTU, so the threshold is 1518. Why MTU-based: the direction says "one
  maximum-size frame for the port's MTU", and a fixed octet bound would ignore
  jumbo ports. The value is pinned by a test, and Open questions names it as the
  plan's least-certain number.
- `traffic.RetentionKey` encodes each queue's buffer alongside its rate. Why:
  `vswitch.Derive` retains a token bucket when the key is unchanged, and a
  buffer is part of the traffic configuration `Derive` judges
  (`docs/solutions/architecture-patterns/validate-and-derive-judge-what-new-builds.md`).
- No conformance corpus case lands in this phase; phase 3 claims the load cases
  (parent requirement 13). Why: an admitted case carries an exact ordered
  semantic trace and exact metadata, and a depth-dependent issue makes that
  brittle, while `fabric` tests prove this phase's behavior directly.

## Requirements

Numbers are the parent's; letters are this phase's acceptance examples.

6. A queue with a stated buffer tail-drops with reason `queue-full` and counts
   it in `OutDiscards`.
   6a. Tail drop. One switch `sw1` joins hosts `h1`, `h2`, `h3`. Port
   `sw1:1/1/3` (toward `h3`) carries
   `Queues: {"1/1/3": {BufferOctets: {0: 2000}}}`. At `t0`, `h1` and `h2` each
   inject 20 untagged 1000-octet-payload frames to `h3`, `RetainAggregate`, as
   flows 1 and 2. After the run drains, for the `1/1/3` counters `OutDiscards`
   equals `Discards["queue-full"]` and both are nonzero;
   `Flows()[1].Drops["queue-full"] + Flows()[2].Drops["queue-full"]` equals
   that count; for each flow `Delivered["h3"] + Drops["queue-full"] == Offered`;
   and `EgressDepths[sw1:1/1/3][0].Peak <= 2000`.
   6b. Under capacity. The same fabric and flows with
   `BufferOctets: {0: 8000}` deliver every frame to `h3`, leave `Drops` empty,
   and report the peak below 8000.
   6c. The count is the frame's own octets. Two untagged 1000-octet-payload
   frames queued together while the endpoint is busy are 1014 encoded octets
   each and 2076 wire octets together (`run.go:110-119`). A stated buffer of 2028
   admits the second frame because `1014 + 1014 == 2028`; a buffer of 2027
   refuses it. A wire-octet count would refuse at 2028, so the admit separates
   the two units.

7. A queue with no stated buffer never drops, reports peak depth, and raises an
   `Incomplete` issue once its depth passes one maximum-size frame.
   7a. No drop. The 6a fabric with no `Queues` entry delivers every frame and
   drops none.
   7b. Peak. Ten 1000-octet-payload (1014-encoded-octet) frames enqueued on one
   endpoint at a single instant make `Peak` at least 9×1014 and at most
   10×1014, and equal to the greatest `Depth` seen; after the queue drains,
   `Depth` reaches 0 and `Peak` is unchanged.
   7c. Issue. `Flows()[1].Metadata.Issues()` holds `queue-buffer-unstated` at
   `Incomplete` scoped to `analysis.PortScope("sw1", "1/1/3")`, and
   `Fabric.Metadata().IssuesFor` that scope holds it once. A `Fabric.Metadata()`
   read before the crossing does not.
   7d. Below threshold. One 1000-octet-payload frame on an unstated port (1014
   encoded octets, below 1518) raises no issue for that port; the second frame
   queued behind it, at an appended depth of 2028, raises one, because the
   crossing is measured on the depth with the enqueued frame counted.

8. The buffer field clones, validates, and diffs like `MaxRateBPS`.
   8a. `Validate` refuses `PortQueues{BufferOctets: {0: 0}}` with an error whose
   `field` attribute is `queues.1/1/1.buffer_octets.0`; a positive buffer for a
   valid PCP passes.
   8b. `Diff` of two configs that differ only in `BufferOctets[0]` reports
   exactly one change: subject `{port, "1/1/1/0"}`, field `buffer_octets`,
   `From QueueBufferFact(2000)`, `To QueueBufferFact(4000)`.
   8c. `Clone` is independent: writing `clone.BufferOctets[0] = 9` leaves the
   original at its value and does not share the map.
   8d. `traffic.RetentionKey` differs between two configs that differ only in a
   buffer.

## Out of scope

- Weighted or deficit scheduling, WRED, and shared buffer pools (the parent's).
- A buffer field on `fabric.Host` and any per-host queue configuration.
- Peak depth on `Counters`; the snapshot is the only reader.
- A conformance corpus case; phase 3 claims the load cases.
- Streams, attached sources, and the pull loop (phase 3).

## Units

### U1. Stated queue buffers in the traffic configuration
Files: `src/common/netsim/vswitch/traffic/config.go`,
`src/common/netsim/vswitch/traffic/config_test.go`,
`src/common/netsim/vswitch/traffic/diff.go`,
`src/common/netsim/vswitch/traffic/diff_coverage_test.go`,
`src/common/netsim/vswitch/traffic/fact.go`,
`src/common/netsim/vswitch/traffic/policer.go`,
`src/common/netsim/vswitch/traffic/README.md`,
`src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/switch_test.go`
After: none
Change: `PortQueues` carries `BufferOctets map[vlan.PCP]uint64` with a comment
stating the unit is encoded frame octets. `Config.Clone` deep-copies it;
`Config.Validate` walks the union of each port's rate PCPs and buffer PCPs,
refusing a zero or invalid buffer for a PCP and naming
`queues.<port>.buffer_octets.<pcp>` the way the rate loop names its own field;
`Config.QueueBuffer(name, pcp) (uint64, bool)` returns the stated buffer beside
`MaxRate`. `Diff` walks the same union and reports a `buffer_octets` change with
`QueueBufferFact` binary facts.
`QueueBufferFact uint64` with type ID `traffic.queue_buffer_octets` joins
`MaxRateFact` in `diff.go`. `traffic.ReasonQueueFull` (`"queue-full"`),
`traffic.RuleQueueDrop` (`"traffic.queue.drop"`), and
`QueueDropFact(depthOctets, bufferOctets uint64, frameOctets int) trace.Fact`
with type ID `traffic.queue_decision` join `fact.go`. `RetentionKey` walks the
union too and writes each PCP's buffer in its queue arm.
`Switch.QueueBuffer` sits beside `QueueMaxRate` (`switch.go:570-577`) and reads
the live configuration.
Tests: `config_test.go` gains 8a, 8c, and a `QueueBuffer` present/absent case
beside the existing `MaxRate` case; `config_test.go`'s diff test gains 8b;
`diff_coverage_test.go` seeds `BufferOctets` in its `Queues` entry so
`AssertDiffCoversConfig` reaches the new leaf (an unseeded map is a leaf the walk
cannot find); `policer_test.go` gains 8d, which is also the retention cover,
because `AssertRetentionKeyCoversConfig` has no caller for this package;
`switch_test.go` gains the `QueueBuffer` accessor case beside `QueueMaxRate`
(`switch_test.go:754-758`).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/traffic src/common/netsim/vswitch`

### U2. Egress octet accounting and the stated-buffer tail drop
Files: `src/common/netsim/fabric/run.go`, `src/common/netsim/fabric/fabric.go`,
`src/common/netsim/fabric/fingerprint_test.go`,
`src/common/netsim/fabric/egress_buffer_test.go`,
`src/common/netsim/fabric/egress_buffer_internal_test.go`,
`src/common/netsim/fabric/README.md`
After: U1
Change: `queued` carries `octets`, the encoded frame length, computed once in
`enqueueEgress` from `frame.Encode()`. `egressQueue` carries
`depth [8]uint64` and `peak [8]uint64`; `enqueueEgress` adds and raises the peak,
and the pop in `serve` (`run.go:991-999`) subtracts. `enqueueEgress` looks up
`f.switches[txEnd.Node].QueueBuffer(item.egressPort, pcp)` before appending;
when a buffer is stated and `depth[pcp]+octets > buffer` it records an
`EntryDrop` on the journey with `Reason: traffic.ReasonQueueFull`,
`Device: txEnd.Node`, `Port: egressPort`, and a `Step` of layer `traffic.Layer`,
op `trace.OpDrop`, rule `traffic.RuleQueueDrop`, subject
`{Kind: "port", Key: egressPort + "/" + pcp}`, and input
`traffic.QueueDropFact(depth, buffer, octets)`; it calls `countEgressDrop` for
`egressPort` and for `txEnd.Port` when they differ, adds nothing to in-flight,
and returns before `serve`. A stated buffer that fits appends as today.
`Snapshot` gains `EgressDepths map[Endpoint]map[vlan.PCP]EgressDepth`, where
`EgressDepth{Depth, Peak uint64}`, filled from `depth` and `peak` for every
queue whose depth or peak is nonzero, so a drained queue still reports its peak.
`cloneEgressQueue` already copies the queue by value (`fabric.go:736-757`), so
a fork gets its own arrays. The README's Egress queues section states the
buffer, the encoded-octet unit, the tail drop and its counters, and the new
snapshot field, and says depth and peak are absent from the fingerprint and the
comparison. `fingerprint_test.go` classifies `EgressDepths` as `fieldExcluded`
in `snapshotFieldClasses` (`fingerprint_test.go:32-39`), because
`TestFingerprintFieldClassificationWalk` fails any exported `Snapshot` field
with no entry.
Tests: `egress_buffer_test.go` (package `fabric_test`) with 6a, 6b, 6c, 7a, 7b,
and a case asserting the drop entry's `Reason`, its `Step` rule and subject, and
its `QueueDropFact`; `egress_buffer_internal_test.go` (package `fabric`) with the
depth after a pop returning to zero, the peak surviving the drain unchanged, the
buffer boundary at `depth+octets == buffer` admitting and `== buffer+1` refusing,
and a fork whose later enqueue raises its own peak without moving the source's;
`fingerprint_test.go` with the `EgressDepths` classification.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U3. The unstated-buffer issue
Files: `src/common/netsim/fabric/config.go`,
`src/common/netsim/fabric/fabric.go`, `src/common/netsim/fabric/run.go`,
`src/common/netsim/fabric/queue_buffer_internal_test.go`,
`src/common/netsim/fabric/README.md`, `src/common/netsim/vswitch/switch.go`,
`docs/architecture/2026-09-10-virtual-device-direction.md`
After: U2
Change: `fabric.IssueQueueBufferUnstated analysis.IssueCode =
"queue-buffer-unstated"` joins `IssuePropagationUnknown` (`config.go:95-97`).
`Fabric` carries `unstatedBacked map[Endpoint]struct{}`, and `Fork` clones it.
`enqueueEgress` computes the threshold for a queue with no stated buffer: the
live switch port's MTU via a new `Switch.PortMTU(name) (int, bool)`, or 1500 for
a host end and for an unset MTU, plus 18; when the depth with the frame just
enqueued included (`depth[pcp]+octets`) strictly exceeds it and the endpoint is
not yet marked, that one call marks the endpoint, folds the same port-scoped
issue into the enqueued frame's journey through a small `f.raise` helper built
on `mergeMetadata`, and nils `f.metadataCache`. `Metadata()` appends, for each
marked endpoint in sorted order, an
`analysis.Issue{Code: IssueQueueBufferUnstated, Status: analysis.Incomplete,
Scope: endpointScope(ep)}`, before it caches. The README's Topology metadata
table gains the row and its Journey metadata section notes that a marked queue's
issue reaches a journey that depends on the endpoint; the phase-1 sentence that
`SetFault` is the only cache invalidator is corrected in the same change. The
virtual device record's run bullet (`2026-09-10-virtual-device-direction.md`
lines 73-95) is rewritten to state that an egress queue with a stated buffer
tail-drops a frame that would overflow it and that a queue with none reports an
`Incomplete` issue once it backs up, the part this phase makes true.
Tests: `queue_buffer_internal_test.go` (package `fabric`) with 7c, 7d, an issue
set that is identical across two `Metadata()` calls with no new crossing, a
crossing that leaves an earlier `Metadata()` value unchanged while a later one
carries the issue, a mark whose issue appears once for two crossing PCPs on one
port, a host queue that marks its node scope, a run that ends on the crossing
frame and still reports the issue from the flow, and a fork that marks without
changing the source's `Metadata()`. The full `src/common/netsim` suite runs; a
fixture whose undefined-buffer switch queue crosses the threshold has its
expectation updated or its port given a stated buffer in this change, with the
choice reported (for example the single 1600-octet frame forwarded in
`replay_test.go:481-558`, whose switch egress queue reaches about 1638 octets,
and the corpus cases handled per Open questions).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric src/common/netsim/vswitch docs/architecture/2026-09-10-virtual-device-direction.md`

Waves: U1 | U2 | U3

The graph is a chain: U2 reads U1's `Switch.QueueBuffer`, `traffic` reason, and
facts; U3 needs U2's depth accounting to decide the crossing, and U1 and U3 both
edit `vswitch/switch.go`, so no wider cut is available.

## Verification

```bash
go test -race ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim
```

No lab check: the phase-5 lab comparison is where a real buffer size is
checked, and it needs the owner's approval per run.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `traffic` README, `fabric` README, the phase-1 cache sentence, and the
      virtual device record's run bullet updated in the units that invalidate
      them.
- [ ] This plan's `status` set with an outcome note under its title, and the
      parent's `Landed:` line for U2 filled.
- [ ] No plan labels in code.

## Open questions

- The 1518-octet fallback for an unset MTU and for host queues is the plan's
  least-certain number; no vendor source states it. The implementer confirms no
  existing test depends on a different one and reports the figure.
- A fixture whose undefined-buffer queue crosses the threshold has one of two
  answers: state a buffer on the fixture's port so the run keeps its old
  metadata, or keep the queue unstated and update the expectation. The
  implementer picks per fixture and reports the choice. The corpus's exact
  journey `ExpectedMetadata` (`src/common/netsim/internal/netsimtest/README.md`
  admission bar) is where this is most likely to surface.
