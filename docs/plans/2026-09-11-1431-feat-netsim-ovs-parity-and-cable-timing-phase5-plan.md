---
title: Network Simulation, Phase 5: Mirrors, Policing, and Queues - Plan
type: feat
date: 2026-09-11
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
parent: docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-plan.md
---

# Network Simulation, Phase 5: Mirrors, Policing, and Queues - Plan

> Implemented.

## Goal

A switch copies selected frames to a mirror port or a mirror VLAN with a
snap length, refuses ingress above a token bucket's rate and burst with
the reason `policed`, and sends from each port in strict priority by PCP
with a maximum rate per queue, all over the serialization clock phase 1
added. The means is a `netsim/vswitch/traffic` package holding the
configuration, the mirror selection, the policer buckets, and the queue
table; the switch applying mirrors after the relay's egress half and
reserving mirror output ports; and the fabric policing at arrival and
holding each port's pending copies in a queue it serves by priority and
rate. The phase is wrong if a consumer needs a minimum-rate guarantee
between queues, which stays out, or a mirror that follows a frame through
a tunnel.

## Decisions

The parent's Decision on mirrors, policing, and queues holds, narrowed in
one place: per-queue minimum rates are out (see Out of scope), since a
strict-priority scheduler with no measured load has nothing to guarantee
against. This phase settles the shapes its stub left open:

- `traffic.Config{Mirrors []Mirror; Policers map[string]Policer; Queues
  map[string]PortQueues}` keyed by port name. `Mirror{Name string;
  SelectAll bool; SelectSrcPorts, SelectDstPorts []string; SelectVLANs
  []vlan.ID; OutputPort string; OutputVLAN *vlan.ID; SnapLen int}`; a
  frame is selected when `SelectAll` is set, or its ingress port is in
  `SelectSrcPorts`, or any of its egress ports is in `SelectDstPorts`, and
  in every case, when `SelectVLANs` is non-empty, its classified VLAN is
  in it; exactly one of `OutputPort` and `OutputVLAN` is set;
  `SnapLen` 0 means untruncated. `Policer{RateBPS uint64; BurstOctets
  int}`; `PortQueues{MaxRateBPS map[vlan.PCP]uint64}` with an absent PCP
  unlimited. `Validate(ports)` refuses a mirror with both or neither
  output, an unknown port anywhere, an output port that is also a source
  or destination selector, a VLAN outside 1..4094, a policer with
  `RateBPS` set and `BurstOctets` below 1, and a max rate of 0. `Diff`
  reports each mirror by name and each policer and queue by port; `Clone`
  deep-copies. Why: these are Open vSwitch's Mirror columns (`select_all`:
  "every packet arriving or departing on any port"; `select_src_port`:
  "Ports on which arriving packets are selected"; `select_dst_port`:
  "Ports on which departing packets are selected"; `select_vlan`: "An
  empty set selects packets on all VLANs"; `output_port`, `output_vlan`,
  `snaplen`: "A mirrored packet with size larger than snaplen will be
  truncated"), its Interface `ingress_policing_rate` ("Data received
  faster than this rate is dropped") and `ingress_policing_burst`, and
  its Queue `other_config:max-rate` ("the queue's rate will not be
  allowed to exceed the specified value, even if excess bandwidth is
  available") and `priority`
  (https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/vswitchd/vswitch.xml);
  the rates here are bits per second and the burst octets, since the
  tree's cable timing counts wire octets and bits.
- Mirroring is a pure function of the traffic package, `Copies(cfg,
  vlans *bridge.VLAN, ingress string, vid vlan.ID, received
  ethernet.Frame, egress []bridge.Egress) []Copy` (the bridge's VLAN
  configuration says which ports carry the output VLAN and how; a port
  table cannot, so the first draft's `ports` parameter gave way to it) with `Copy{Mirror, Port
  string; Frame ethernet.Frame}`: a mirror to a port yields one copy of
  the received frame, tags and all, truncated so the encoded frame is at
  most `SnapLen` octets; a mirror to a VLAN yields one copy per port that
  carries the VLAN other than the ingress port, tagged with the output
  VLAN where the port carries it tagged (replacing the received outer tag)
  and untagged where it carries it untagged, and skips frames to the
  reserved bridge addresses; a frame selected by several mirrors gets one
  copy per mirror. Why: this is Open vSwitch's `output_port` (SPAN) and
  `output_vlan` (RSPAN, "the frame's VLAN tag will be set to output_vlan,
  replacing any existing tag; when it is sent out an implicit VLAN port,
  the frame will not be tagged"), and a function over values is what the
  relay is.
- The switch owns the traffic configuration (`vswitch.Config.Traffic
  *traffic.Config`) and applies mirrors after the relay: `Switch.forward`
  drops a frame received on a mirror output port with
  `traffic.ReasonMirrorOutput` ("mirror-output"), removes normal egress on
  such a port from `Result.Egress` as a drop with the same reason, and
  computes the copies with `traffic.Copies` over the frame as received
  and the egress that survived, exposing them through `Switch.Copies()
  []traffic.Copy`, which returns and clears the copies of the last
  `Forward` the way `Drain` does for emissions, since `bridge` cannot
  import `traffic` (which imports `bridge`) to carry them on the result; a host-emitted or routed
  frame is mirrored the same way by its egress ports. Why: OVS reserves
  the output port ("No frames other than those selected for mirroring
  will be forwarded to the port, and any frames received on the port
  will be discarded"), and the copies belong on the result so the fabric
  transmits them like egress.
- Policing is the fabric's, at arrival, over the traffic package's
  bucket: `traffic.Bucket` per (switch, port) with `Admit(now, octets)
  bool` that refills `RateBPS × elapsed / 8` octets up to `BurstOctets`
  since the last call and takes the frame's wire octets when they fit;
  a refused frame is a whole-frame drop `traffic.ReasonPoliced`
  ("policed") counted as an ingress discard, before the relay sees it, so
  a policed frame learns nothing and mutates no protocol; a corrupt
  arrival is `bad-frame` and spends no tokens; a loop re-entry is a new
  arrival and is policed; `Peek` never polices. The bucket counts the
  frame's wire octets (the encoded frame padded to 60 plus 24, as the
  cable model counts), starts full, refills lazily from the run's clock,
  and a refused frame consumes nothing. The switch exposes `Police(now,
  port, octets) bool` so the bucket's state stays with the switch's
  traffic layer and a derive keeps it.
  Why: the parent's Decision, a token bucket over the run's clock, and
  the rate that matters is the wire rate the cable model already counts.
- Egress queueing is the fabric's over the busy clock: each endpoint that
  transmits holds a queue of pending copies (`Copy`, `Egress`, and
  emissions alike) with the frame's classified PCP, which `bridge.Egress`
  gains as `PCP vlan.PCP` from the relay's ingress classification (an
  untagged egress has lost its tag, so the wire form cannot say), the
  hub and routed results filling it the same way and an emission or a
  host injection taking the outer C-tag's PCP or 0, and its enqueue
  order; a transmission is no longer scheduled
  at `transmit` but when the port is free: `transmit` enqueues and, when
  nothing is being serialized on the endpoint and no dequeue is pending,
  dequeues at once; otherwise the dequeue is an `Arrival` with `Kind` `Dequeue` (the
  arrival gains a kind: frame, wake, or dequeue, replacing the `Wake`
  flag) at the endpoint's busy end, one pending per endpoint, naming the
  endpoint and no frame, since the frame is chosen when it runs; at one
  instant the queue orders a wake, then frame arrivals, then dequeues,
  so two frames arriving together are both waiting when the port is
  served. A dequeue consumes a step and returns an `EntryDequeue` entry
  the way a wake returns `EntryWake`. A switch with no traffic
  configuration and a host's cable end still go through the queue, so
  the order at one instant is one rule; a lone frame on an idle endpoint
  dequeues in the same call and starts at once, so phase 1's numbers
  hold. A dequeue serves the highest PCP with
  an eligible head (FIFO within a PCP); a queue's head is eligible when
  the queue's rate clock is not after now, where a queue with a maximum
  rate sets its clock to the start of each frame it sends plus `wire bits
  / MaxRateBPS` rounded up to the nanosecond, so shaping is measured start
  to start and the first frame on an idle queue starts at once; when no
  head is eligible the dequeue is rescheduled at the earliest rate clock. The served frame's start is now,
  its end one serialization at the link rate later, `busyUntil` takes the
  end, the far end's arrival is the end plus propagation, the crossing
  entry's `Wait` is the start minus the enqueue time and gains `PCP`, and
  the next dequeue is scheduled at the end while the queue holds anything.
  A host's cable end is a queue like any port. `Snapshot` gains `Queued
  map[Endpoint]int`, the pending count per endpoint with anything
  pending. Why: strict priority needs the pending copies in one place to
  reorder them, which phase 1's start-at-transmit rule cannot do, and the
  rate clock composes with the busy clock by delaying eligibility rather
  than stretching serialization, so a rate-limited frame still occupies
  the wire for its true serialization.
- Mirror copies are their own journeys: the fabric creates a `Journey`
  per copy with a new `FrameID`, `Mirror` set to the mirror's name,
  `Parent` the original's `FrameID`, `Injection` naming the switch and
  the output port at the hop's time, and `Protocol` false, so `Report`
  lists them and a reader follows the copy to its delivery. A copy enters
  its output port's queue directly: the reserved-port rule applies to
  ordinary ingress and relay egress, never to a copy, and a copy is never
  mirrored again or policed. `Compare` includes copies as deliveries,
  since a mirror is behavior; it stays blind to timing as phase 1 left
  it. Why: the stub's
  open question; an entry in the original's journey could not carry the
  copy's own crossings and deliveries.
- Records: the vswitch README's ladder gains the traffic layer, its drop
  reasons gain `policed` and `mirror-output`; the fabric README's busy
  clock section becomes an egress queue section with the priority and
  rate rules and a worked example; the direction record's run bullet
  changes "a copy is a transmission on the egress port that starts when
  the port is free" to "... that starts when the port is free and its
  queue's turn comes" (the record's `amends` in the parent covers it).

## Requirements

Every example uses the fabric README's topology from phase 1 (`h1` on
`sw1:1/1/1`, trunk `sw1:1/1/24` to `sw2:1/1/24` over 300 m of multimode
fiber, `h2` on `sw2:1/1/1`, VLAN 10, 1 Gbit/s everywhere) plus a host `h3`
on `sw1:1/1/4` and, where named, a tagged port `sw1:1/1/2` in VLAN 10 for
injected tagged frames; `t0` is the first injection.

1. Mirror to a port with a snap length. Acceptance: `sw1` has mirror
   `m1` with `SelectSrcPorts` {1/1/1}, `OutputPort` 1/1/4, `SnapLen` 64; a
   frame from `h1` with a 200-octet payload to `h2` produces a second
   journey with `Mirror` "m1" delivered to `h3` whose frame encodes to 64
   octets and whose payload is the first 50 octets of the original; a
   frame from `h3` is dropped at `sw1` with `mirror-output`; `h3` receives
   no copy of the flood to `h2`'s reply, since 1/1/4 carries no normal
   egress.
2. Mirror to a VLAN. Acceptance: `m2` with `SelectSrcPorts` {1/1/1},
   `OutputVLAN` 99, `sw1:1/1/24` tagged in VLAN 99 and `sw1:1/1/4`
   untagged in VLAN 99; a frame from `h1` produces a copy journey on
   1/1/24 whose frame carries one C-tag with VID 99 and a copy journey on
   1/1/4 with no tag, and none on 1/1/1; a frame from `h1` to
   01-80-C2-00-00-0E produces no copy.
3. Policing. Acceptance: `Policers` {1/1/1: {1 Mbit/s, 10000 octets}};
   ten frames from `h1` with a 1000-octet payload (1038 wire octets each)
   injected at `t0` pass nine and drop the tenth at `sw1` with `policed`,
   counted in `Counters.Discards["policed"]` and `InDiscards` on 1/1/1;
   a frame at `t0 + 10ms` (1250 octets refilled) passes.
4. Strict priority. Acceptance: two frames with a C-tag VID 10 are
   injected at `sw1:1/1/2` at the same instant `t0`, PCP 0 first and PCP 7
   second, both unknown unicast flooding to the trunk; the arrivals are
   two steps at `t0` and the dequeue follows both, so the PCP 7 copy
   starts at `t0` and its hop on `sw2` is at `t0 + 2198ns` (704 ns plus
   1494 ns), while the PCP 0 copy starts at `t0 + 704ns` with its hop at
   `t0 + 2902ns`; `Snapshot().Queued[sw1:1/1/24]` reads 2 after the two
   arrival steps and before the dequeue step; the PCP 7 copy's crossing
   reads `PCP` 7 although the trunk emits it tagged and a copy to an
   untagged port would carry no tag.
5. Queue maximum rate. Acceptance: `Queues` {1/1/24: {0: 100 Mbit/s}};
   two frames from `h1` with a 46-octet payload back to back (the second
   injected 1 ns after the first) reach `sw1` at `t0 + 672ns` and
   `t0 + 1344ns` and cross the trunk with starts 7040 ns apart (704 wire
   bits at 100 Mbit/s): the first starts at `t0 + 672ns` and arrives at
   `sw2` at `t0 + 2870ns`, the second starts at `t0 + 7712ns` and arrives
   at `t0 + 9910ns`, each serializing in 704 ns; with PCP 7 unlimited, a
   PCP 7 frame injected at `sw1:1/1/2` between them leaves before the
   second PCP 0 frame.
6. Diff and Validate. Acceptance: `Diff` reports a mirror's `snap_len` and
   a policer's `rate` by name and port; `Validate` refuses a mirror with
   both outputs, an output port that is also a source selector, a policer
   with rate and burst 0, and a queue max rate of 0.

## Out of scope

Everything the parent lists; per-queue minimum rates (narrowing the
parent's Decision, as a fact: strict priority with no load model has no
guarantee to enforce); a QoS-level maximum below the link rate; mirroring
of frames the switch itself emits; a mirror output port that is a LAG;
egress policing.

## Units

### U1. The traffic package

Files: `src/common/netsim/vswitch/traffic/config.go`, `mirror.go`,
`policer.go`, `diff.go`, `config_test.go`, `mirror_test.go`,
`policer_test.go`, `README.md`
After: none
Change: the configuration, `Validate`, `Clone`, `Diff`, `Copies`, `Copy`,
`Bucket` with `Admit`, `ReasonPoliced`, `ReasonMirrorOutput`, and a
`MaxRate(port, pcp) (uint64, bool)` lookup; the README with the selection
rule, the copy forms, the bucket, and the sources.
Tests: `mirror_test.go`, requirements 1 and 2 at the function (copies,
truncation, tag forms, reserved addresses, several mirrors);
`policer_test.go`, requirement 3's bucket arithmetic; `config_test.go`,
requirement 6. Each is evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/traffic`

### U2. The switch

Files: `src/common/netsim/vswitch/bridge/result.go`,
`src/common/netsim/vswitch/config.go`, `switch.go`, `diff.go`, `derive.go`,
`switch_test.go`, `README.md`
After: U1
Change: `Config.Traffic`, `Capabilities` adding `port.LayerTraffic` (a
new trace layer "traffic" in `port`) when set; `bridge.Egress.PCP` filled
from the ingress classification in the relay, the hub, and the routed
result; `Switch.Copies()`;
the switch dropping frames received on a mirror output port, removing
normal egress on one, and filling copies for bridged, hub, and routed
results; `Police(now, port, octets) bool`; `QueueMaxRate(port, pcp)`;
`Diff` and `Derive` covering the layer with the buckets carried over.
Tests: `switch_test.go`, a mirrored flood filling `Copies`, the output
port's two drops, and `Police` refusing the eleventh frame. Each is
evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch`

### U3. The fabric

Files: `src/common/netsim/fabric/run.go`, `journey.go`, `queue.go`,
`fabric.go`, `compare.go`, `traffic_test.go`, `run_test.go`,
`replay_test.go`, `README.md`, `src/common/netsim/vswitch/README.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`
After: U2
Change: policing at arrival; the per-endpoint egress queue, the arrival
kind with `Dequeue`, `EntryDequeue`, the priority and rate rules,
`Entry.PCP`, `Snapshot.Queued`; copy journeys with `Journey.Mirror` and
`Journey.Parent`; every existing test asserting a
transmission start moves to the queue's numbers where they differ (a
lone frame on an idle port starts at once, so phase 1's numbers hold);
the records of the last Decision.
Tests: `traffic_test.go`, requirements 1 through 5 end to end, the
priority case watched failing first with FIFO service; `replay_test.go`,
a replayed run with queues reproducing its times. Each is evidence for
this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture
go test -race ./src/common/netsim/...
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] The traffic, vswitch, and fabric READMEs and the direction record
      match the landed API and numbers.
- [ ] This plan's `status` set with an outcome note under its title, and
      the parent's `Landed:` line for this phase filled.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Whether a mirror copy should be policed or queued differently from the
  original; the plan queues it like any copy at its PCP.
- The dequeue sorts after frame arrivals at the same instant, so frames
  arriving together are both waiting when the port is served; a wake
  still sorts first. This is the order the strict-priority example
  needs, and the one a reader of the queue section will find stated.
