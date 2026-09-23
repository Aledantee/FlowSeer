---
title: Offered-Load Streams Phase 1 - Engine Scale and Flow Statistics - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
parent: docs/plans/2026-09-18-0000-feat-netsim-offered-load-streams-plan.md
---

# Offered-Load Streams Phase 1 - Engine Scale and Flow Statistics - Plan

> Implemented. 3 units, 2026-09-23T16:08:00Z to 2026-09-23T17:08:35Z.

## Goal

A fabric run holds a million injected frames without keeping a million
journeys, and reports what happened to them per flow. The means: a heap-backed
arrival queue, a cached fabric metadata for `record`, a settle count per
frame, and an `Injection.Retention` that folds a settled frame into
`Fabric.Flows()` and frees it. The plan is wrong if settling cannot be decided
from the fabric's own queues, that is if some path keeps a frame alive under
its `FrameID` outside `f.queue` and `f.egress`; the neighbor hold was checked
and does not (`src/common/netsim/fabric/run.go:1185-1187`).

## Decisions

- The parent's decisions and the direction record apply; this phase lands the
  record's retention and engine bullets.
- The heap keeps `compareArrival` as its order and tracks indexes for removal.
  At most one `ArrivalDequeue` exists per endpoint, which the `dequeuePending`
  fault guard keeps (`run.go:989-991`), and one `ArrivalWake` per device, which
  `scheduleWake`'s remove-then-add keeps (`run.go:1223-1234`). Two maps from
  that key to the heap item replace the `slices.DeleteFunc` scans, and the pop
  in `Step` deletes the popped item's key from its map, beside the existing
  `f.wakes` and `dequeuePending` clearing; a stale index would remove an
  unrelated arrival. Why not tombstones: they would leak into `Snapshot.Queue`
  and into the `serve` peek at `run.go:890`.
- `compareArrival` is already a total order for distinct queue members: copies
  of one frame share `Seq` and differ in `Device` or `Port`. Why this matters:
  a heap is not stable, so pop order equals the old sorted order only under a
  total order. U1 adds the test that pins it.
- `Snapshot.Queue` sorts a copy. Why: tests and the corpus read it at 16
  sites, three of them by index (`run_test.go:390`, `938`, `1489`).
- `record` reads a cached `Fabric.Metadata()`. `Metadata()` reads
  `f.linkTrust`, `f.byEnd` and `f.uncabled` through `derivedEnd`, the port
  tables in `f.cfg.Switches`, and `f.evidence` (`fabric.go:675-727`). After
  construction only `SetFault` writes any of them (`fabric.go:1056-1057`), so
  the cache has one invalidation site. Switch-raised issues such as
  `protocol-link-unknown` reach a journey through `Entry.Result.Metadata`, not
  through `Metadata()`, and stay uncached. Why cache: `Metadata()` walks every
  link and port, and `record` calls it for every entry that has dependencies
  (`journey.go:216-217`). The implementer confirms the writer list before
  editing and puts it in the commit message.
- A frame is in flight while it has an `ArrivalFrame` in the queue or an item
  in an egress queue, and `f.inflight[FrameID]` equals the number of both. The
  count changes only where an item enters or leaves one of those two
  containers: the pushes behind `run.go:284`, `296`, `773`, `852`, and `1123`,
  the frame pop in `Step`, and the removal from `q.pending` in `serve`
  (`run.go:936-943`). A delivery to a host, a host's refusal, a corrupt arrival
  at a host, and a cable loss need no site of their own, because each follows
  the `q.pending` removal and pushes nothing (`run.go:1092-1107`).
- Settling is decided at the end of a call, never at a decrement. `Inject` and
  `Step` collect the frame IDs whose count they touched, plus the ID `Inject`
  just created, and on return settle each one whose count is zero. Why: `Step`
  pops the arrival at `run.go:320-321` and pushes the frame's copies later
  (`run.go:535`, `569`), so a check at the pop would fold and free a journey
  that `run.go:496` still records into, and the copies would fold it twice. An
  injection at a host whose link is `Down` pushes nothing (`run.go:268-281`)
  and settles when `Inject` returns.
- A frame a switch holds for neighbor resolution settles at the hold. The
  released frame gets a new `FrameID` today and carries no flow. Its flow
  counts it under `Held`, keyed off an `EntryHop` whose `Result.Outcome` is
  `trace.Held` (`src/common/netsim/vswitch/switch.go:1796-1799`). Why: no `FrameID` travels with a held frame, and
  threading one through `vswitch` is a change to the hold queue, outside this
  phase.
- `Injection` gains `Retention` and `Flow`. `RetainJourney` is the zero value.
  `RetainAggregate` requires `Flow != 0`; `Inject` refuses it otherwise. A
  `RetainJourney` injection may also name a flow and is folded at settle
  without being freed. Why: a caller debugging one stream wants both.
- An aggregated frame still gets a `Journey` while in flight, and `record`
  still runs on it. Why: `Step`, `serve`, and `transmitCable` dereference the
  pointer unguarded (`run.go:361`, `951`, `1082-1121`), and the trust metadata
  is folded from it. At settle the fabric folds, then deletes
  `f.journeys[fid]` and `f.entered[fid]`.
- Mirror and reflector copies inherit the parent's retention and flow and fold
  into `FlowStats.Copies`, keyed by mirror name or `"reflection"`, never into
  `Delivered`. Why: a mirrored frame reaching an analyzer is not the stream
  being delivered.
- Latency is `Delivery.At` minus `Injection.At`, per delivery. The fold keeps
  minimum, maximum, sum, and count. Jitter waits for phase 3, which knows
  frame order within a stream.
- `FlowStats.Metadata` is the merge of every folded journey's metadata, built
  with the `keepIssue` and `keepAssumption` logic of `record`, extracted into
  a shared helper.

## Requirements

Numbers are the parent's; letters are this phase's acceptance examples.

1a. Pop order. Enqueue arrivals at the same `At` with kinds wake, frame,
   dequeue in reverse; `Step` processes wake, frame, dequeue, and
   `Snapshot().Queue` is sorted by `compareArrival`.
1b. Removal. Schedule a dequeue for an endpoint, reschedule it earlier; the
   queue holds one dequeue for that endpoint, at the new time.
2a. Settle. Inject a broadcast in flow 3 at `h1` on a three-host VLAN; after
   the run drains, `f.inflight` is empty and `Flows()[3].Offered` is 1, so the
   frame folded exactly once.
2b. No flight. Inject 10 `RetainAggregate` frames in flow 4 at a host whose
   link is `Down`; with no step run, `Report()` holds none of them and
   `Flows()[4]` has `Offered` 10 and 10 drops under the link's reason.
3a. Free. Inject 1,000 `RetainAggregate` frames in flow 7 from `h1` to `h2`;
   after the run drains `len(f.journeys)` and `len(f.entered)` count only
   protocol journeys, and `Report()` holds none of the 1,000.
4a. Fold. The same run gives `Flows()[7]`: `Offered` 1000, `Delivered["h2"]`
   1000, no drops, latency count 1000, minimum equal to the single-frame
   latency of the path.
4b. Drop fold. With `traffic.Policer{RateBPS: 1, BurstOctets: 420}` on the
   ingress port and 10 frames of 84 wire octets injected back to back, the
   bucket admits 5 and refills nothing measurable:
   `Drops[traffic.ReasonPoliced]` 5 and `Delivered["h2"]` 5. For the
   1,000-frame run under the same policer, drops plus deliveries equal
   `Offered`.
4c. Trust fold. With the trunk's medium unspecified and a length set,
   `Flows()[7].Metadata` carries `propagation-unknown`.
4d. Refusal. `RetainAggregate` with `Flow` 0 returns an error from `Inject`.
1c. Unchanged default. Every existing test under `src/common/netsim` passes
   unedited.
5a. Scale. `BenchmarkAggregateMillion` injects 1,000,000 64-octet aggregated
    frames from `h1` across `sw1` and `sw2` to `h2` in batches of 10,000. Each
    batch is injected at `Snapshot().Clock`, because `Inject` refuses a time
    before the clock (`run.go:245-250`), and is followed by `Run` with a
    budget of 1,000,000 steps and a check that `Snapshot().Queue` is empty. A
    second benchmark runs the same load with `RetainJourney`, at 100,000
    frames, so a missed budget can be laid at `record` or at retention. The
    unit records its time and allocation in the
    `fabric` README. The budget is 120 s and 2 GiB on the development host; a
    result over it is reported, not tuned around.

## Out of scope

- Buffers and tail drop (phase 2), streams and lazy pull (phase 3). The
  benchmark batches its injections by hand.
- Freeing protocol journeys (BPDUs, LACPDUs). They stay retained.
- Joining a released held frame to its flow.

## Units

### U1. Heap arrival queue
Files: `src/common/netsim/fabric/queue.go`, `src/common/netsim/fabric/run.go`,
`src/common/netsim/fabric/fabric.go`, `src/common/netsim/fabric/queue_internal_test.go`
After: none
Change: `f.queue` is a `container/heap` of items that carry their index.
`enqueue` pushes; `Step` pops the minimum; the `serve` peek reads the root;
`removeDequeue` and `removeWake` remove by the tracked item for their key;
`Snapshot` copies and sorts.
Tests: `queue_internal_test.go` with 1a and 1b, plus a property test: 10,000
arrivals whose fields come from a linear congruential step written in the
test, each with a unique `Seq`, at most one dequeue per endpoint and one wake
per device, and 1,000 removals by key. They pop in the order `slices.SortFunc`
with `compareArrival` gives, which is well defined because unique `Seq` values
make the order total. A test pops a wake and a dequeue and asserts their keys
left the index maps. The existing
`replay_test.go` and `run_test.go` cover unchanged behavior.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U2. Cached fabric metadata
Files: `src/common/netsim/fabric/fabric.go`, `src/common/netsim/fabric/journey.go`,
`src/common/netsim/fabric/metadata_cache_internal_test.go`
After: U1
Change: `Metadata()` returns a cached value when one exists, and `SetFault`
clears it. The unit's commit message lists the writers of `Metadata()`'s
inputs that were checked. `mergeMetadata` is extracted from `record` for U3.
Tests: `Metadata()` before and after `SetFault` on a cable differs; two calls
with no `SetFault` between them return equal values; `journey_metadata_test.go`
passes unedited, which pins that a journey recorded before `SetFault` keeps
its metadata. Nothing in this unit proves the writer list complete; the
enumeration in the commit is what a reviewer checks.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U3. Settle count, retention, and flow statistics
Files: `src/common/netsim/fabric/run.go`, `src/common/netsim/fabric/journey.go`,
`src/common/netsim/fabric/flow.go`, `src/common/netsim/fabric/fabric.go`,
`src/common/netsim/fabric/flow_test.go`, `src/common/netsim/fabric/flow_internal_test.go`,
`src/common/netsim/fabric/bench_test.go`, `src/common/netsim/fabric/README.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`
After: U2
Change: `f.inflight` counts as the Decisions state. `Injection` has
`Retention` and `Flow FlowID`. `flow.go` holds `FlowID`, `FlowStats`
(`Offered`, `Delivered map[string]uint64`, `Drops map[trace.Reason]uint64`,
`Lost`, `Unresolved`, `Rejected`, `Held`, `Copies map[string]uint64`,
`Latency` with min, max, sum, count, `Metadata`), its `Clone`, and the fold.
`Fabric.Flows()` returns clones keyed by flow. At settle the fabric folds a
journey that names a flow and frees it when its retention is
`RetainAggregate`. The README documents retention, flows, the benchmark
result, and the scale limit. The virtual device record's run bullet states
retention. The sentence "Every frame's processing is a journey: hops, cable
crossings, deliveries, and drops with reasons"
(`docs/architecture/2026-09-10-virtual-device-direction.md:80-81`) is
rewritten, not appended to, since aggregation makes it false as it stands.
Tests: `flow_test.go` (`package fabric_test`) with 2b and 4a through 4d;
`flow_internal_test.go` (`package fabric`, as `run_internal_test.go` is) with
2a, the map sizes of 3a, a flooded frame in a flow that folds once and not
twice, and a count that matches the two containers after every step across
the existing loop, mirror, reflector, and cable-loss fixtures; `bench_test.go`
with 5a. A fixture routes a flow frame through a neighbor hold and asserts
`Held` 1.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric docs/architecture/2026-09-10-virtual-device-direction.md`

Waves: U1 | U2 | U3

The three units share `run.go`, `fabric.go`, or `journey.go`, so the graph is
a chain and the files do not allow a wider one.

## Verification

```bash
go test -race ./src/common/netsim/...
go test -run '^$' -bench BenchmarkAggregateMillion -benchtime 1x ./src/common/netsim/fabric
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture docs/plans
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `fabric` README and the virtual device record updated in U3's change.
- [ ] This plan's `status` set with an outcome note under its title, and the
      parent's `Landed:` line for P1 filled.
- [ ] No plan labels in code.

## Open questions

- The benchmark budget of 120 s and 2 GiB is a guess made without a profile.
  U3 reports the measured figure, and the owner decides whether it holds.
