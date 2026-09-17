---
title: Network Simulation Analysis Completeness, Phase 4c - Plan
type: fix
date: 2026-09-17
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
amends: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-phase4b-plan.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 4c: a held frame's exit is accounted for - Plan

## Goal

A frame that enters a neighbor hold queue leaves it exactly once, and something
records which of the ways it left: released onto the wire, timed out, or
evicted by queue overflow. A frame the switch then fails to put on a wire is
recorded with the reason the egress itself gave, not with a reason the reader
supplied. The means are a cause carried out of the routing layer with each
frame, an egress identity carried with it instead of recovered from a trace
subject, a drop record that carries its own reason to the fabric, and a
conservation test over the routing layer's queue that fails when a frame leaves
it unaccounted for.

Stop condition: the plan is wrong if the conservation property cannot be
asserted from inside `routing` without an exported hook whose only caller is
the test. If counting entries in and out needs such a hook, the property stays
a review rule and this phase shrinks to the point fixes in U1, U3, U4 and U5,
with the reason recorded here.

This phase claims no new parent requirement. It corrects phase 4b, which
claimed parent R20 and R21 and extended R9, R37 and R39, and whose review found
these defects in the fixes for its own review findings.

## Decisions

Phase 4b's decisions stand and are not repeated: the five-state lifecycle, the
per-VRF policy, `trace.Held` as an outcome, observation without solicitation,
and a host stack that resolves nothing.

### The property, and why it is one property and not four defects

Phase 4b's review found a held frame could vanish on eviction. That was fixed,
and the re-review found it could still vanish on an encode failure, and that
the fix had made the surviving slot ambiguous. Two rounds, one mechanism, each
round's fix producing the next round's finding. The shape is the one
`docs/solutions/architecture-patterns/one-slot-two-roles-is-a-defect-class-not-a-defect.md`
describes, and phase 3f is the precedent for how this repository answers it:
name the property, make it fail a test, stop patching instances.

The property, stated so a test can hold it:

> For any sequence of queue operations, the multiset of frames observed in a
> neighbor's hold queue equals the multiset the layer later reported leaving
> it, each leaving exactly once, each carrying a cause.

Two halves of that sentence are accounting rules the test cannot infer, so they
are stated here:

- **Entered** means observed in `vs.neighbors[key].queue` after the operation,
  not "a call was made". A frame U1 refuses before the queue was never in it.
- **`DiscardHeld` removes a frame from the entered multiset rather than
  producing an exit.** Phase 4b decided a derive boundary discards held frames
  unconditionally and documented it (`routing/neighbor.go:427-439`);
  `TestDiscardHeldThenWakePastDeadlineFailsTheEntry` pins it
  (`neighbor_test.go:554-557`). A discarded frame belongs to the run that
  queued it, and the fork that discarded it is a different run. Reopening that
  is not this phase's work.

Why a multiset rather than a count: two frames for one neighbor are
distinguishable only by payload, and a fix that reported the wrong one of a
pair would pass a count.

### The routing layer observes three exits and names them

`Effects` carries `Released` and `Failed` (`routing/neighbor.go:270-275`).
`Failed` holds both a timed-out frame and a frame evicted by `appendHeld`'s
overflow, and `Switch.applyRoutingEffects` (`switch.go:2816-2824`) stamps every
element `routing.ReasonNeighborMiss`. An evicted frame is therefore traced as a
neighbor miss on a neighbor that may have resolved on the same `Wake`.

`HeldFrame` gains `Cause HeldCause`, and `Effects` collapses to one ordered
`Exits []HeldFrame`. The vocabulary is exactly the three exits the layer
observes: `HeldReleased`, `HeldTimedOut`, `HeldEvicted`.

A frame the switch later fails to transmit is deliberately **not** a fourth
cause. It is a frame the layer already reported as `HeldReleased`, and giving
it a second value of the same field would make `Cause` answer two questions —
how the frame left the queue, and what happened to it afterwards — distinguished
only by which stage the reader is in. That is the defect class this phase
exists to remove, and the Definition of done forbids a reader restamping a
writer's cause. The switch records a post-release failure in its own record
type, U3.

Why one slice rather than three: the cause makes the slices redundant, and a
reader that switches on a cause cannot silently inherit a meaning from which
slice it drained. This is not justified by frame numbering — the fabric drains
every emission and then every failure in two separate passes
(`fabric/run.go:331-336`, `:452-458`), so ordering inside `Exits` reaches
neither.

### Three causes, three reasons, and the timeout keeps its name

The reasons are contract: `trace.Reason` is the key of `Counters.Discards`
(`fabric/counters.go:29`) and part of typed trace comparison. So they are named
here rather than left to the implementer:

| Cause | Rule ID and egress-fact reason |
| --- | --- |
| `HeldReleased` | none; the frame becomes an emission, not a step |
| `HeldTimedOut` | `routing.ReasonNeighborMiss`, unchanged |
| `HeldEvicted` | `routing.ReasonNeighborHoldOverflow`, new in `routing/layer.go` |

`HeldTimedOut` keeps `neighbor-miss` on purpose. The direction record states
that a timed-out held frame is reported dropped with `neighbor-miss`
(`docs/architecture/2026-09-10-virtual-device-direction.md:590-591`), and that
sentence is correct; renaming the timeout reason would falsify a record this
phase has no other cause to amend.

### The encode that must happen before the queue, not after it

`Originate` calls `resolveNeighbor` (`routing/layer.go:997`) and encodes the
header only at `:1039`. On the held path the header is queued having never been
encoded, and `finishHeld` (`neighbor.go:283`) returns `(HeldFrame{}, false)`
when its encode fails, so `Wake` appends the frame nowhere.
`fabric.Fabric.Inject` passes a caller's payload straight into `Originate`
(`fabric/run.go:200`), and `ip.Header.encodeV4` refuses a total length over
65535 (`src/common/net/ip/ip.go:251`).

`Originate` encodes to validate before it resolves and **discards the result**,
queuing header and payload unchanged. Why discard rather than carry the bytes:
`heldEntry` holds `header` and `payload` and `finishHeld` re-encodes from them
(`neighbor.go:62-69`, `:284`), so carrying encoded bytes in `payload` would
double-encode and carrying them in a new field would leave `finishHeld` with
two paths. Validation costs one encode on a path that is about to queue a frame
for milliseconds of logical time.

### The egress identity travels with the frame

`recordNeighborFailure` (`fabric/run.go:936-955`) reads `step.Subject.Key` as a
port name. A timeout step names `Subject{Kind: "interface", Key: hf.Interface}`
(`switch.go:2821`), which on an SVI is `vlan20`, never a port. The counter goes
to `Endpoint{Node: dev, Port: "vlan20"}`, and `snapshotCounters`
(`fabric/counters.go:132-140`) iterates the device's real port table, so the
discard never appears in a snapshot.

`HeldFrame` carries the egress port beside the interface, empty for a VLAN
interface, which has no single port until the bridge picks one. The layer can
answer this: it already resolves `l.ifaces[h.iface]` to build the frame's
source MAC (`neighbor.go:291`).

**An exit with no port is counted against no port at all**: the fabric skips
the counter call rather than writing `Endpoint{Node: dev, Port: ""}`, which
`f.counter` would create (`fabric/counters.go:65-77`) and `snapshotCounters`
would never show. A counter nothing can read is worse than an absent one,
because a later reader finds the endpoint and believes it means something.

### The VLAN arm has a silent exit the switch never looks at

`releaseHeldFrame` iterates `res.Egress` only (`switch.go:2843-2857`).
`bridge.replicate` drops a candidate with no `Egress` entry when the port is
down, when it is not a member, and when LAG selection fails, and with no
surviving candidate it returns `res.Reason = noCandidateReason` and an empty
`res.Egress` (`bridge/bridge.go:1222-1234`). A held frame released onto an SVI
whose only member port is down therefore produces no emission and no failure —
the same silent vanish this phase exists to remove, on the arm whose SVI
handling motivates U4.

`releaseHeldFrame` also returns bare when `routing.Interface` does not know the
interface (`switch.go:2837-2840`), which is a third way out with no record.

### Priority survives the whole path, not just the tagged half

Phase 4b's second round carried PCP and DEI onto `HeldFrame` and into the
synthetic `bridge.Ingress` (`switch.go:2843`), fixing the tag written on a
tagged egress port. `Emission` has no priority field (`switch.go:152-163`), so
`Fabric.injectEmission` re-derives with `framePCP(em.Frame)`
(`fabric/run.go:925`), zero for an untagged frame, while the live path
transmits `eg.PCP` (`run.go:508`). A frame arriving tagged at PCP 5 and
released onto an untagged access port or a routed port queues at 0 where the
live frame queues at 5.

### `framePCP` is not folded in here

`fabric/run.go:121` is `ethernet.Frame.Priority` minus the DEI return, and
`traffic/mirror.go:90-97` and `bridge/bridge.go:534-535,590-591` derive the
same pair inline with S-Tag handling that differs. Consolidating them changes
the bridge's classification for every frame, not just held ones. U5 deletes the
fabric's copy only, because U5 gives the fabric a priority to read instead.

### No corpus case

Parent R39 asks each phase for a corpus case naming its current false answer,
and phase 3f's precedent added one. This phase adds none: the answers it
corrects are a counter that does not appear, a reason string on a drop, and a
priority on a queued frame, none of which the corpus compares — its cases
assert outcome, reason, steps and metadata on a `ForwardResult`. The phase-4b
case already pins the release path it shares. Adding a case that cannot see the
change would be a case passing for the wrong reason, which is what this phase
exists because of.

## Requirements

1. A frame that enters a hold queue leaves exactly once with a stated cause.
   Acceptance: over a fixed list of generated sequences of queue, resolve,
   timeout and evict operations on several neighbors, every payload observed in
   a queue appears exactly once across the run's reported exits with a cause,
   and none appears twice. A `DiscardHeld` in a sequence removes its payloads
   from the expected set rather than demanding an exit.
2. An evicted frame is not reported as a neighbor miss. Acceptance: with
   `HoldDepth` 2, three frames queued for one next hop, then an observation
   resolving it, the first frame's step names
   `routing.ReasonNeighborHoldOverflow` and the other two are released; no step
   for that neighbor names `neighbor-miss`.
3. An unencodable datagram never reaches a hold queue. Acceptance: `Originate`
   with an IPv4 payload of 65516 octets toward an unresolved next hop returns
   `ReasonBadHeader` with a drop step, leaves `vs.neighbors` without an entry
   for that next hop, and a following `Wake` reports no exit for it.
4. A released frame the egress refuses is recorded with the egress's own
   reason. Acceptance: on the routed arm, a release onto a down port produces a
   record whose reason is the port's refusal and whose `Step.RuleID` agrees
   with it; on the VLAN arm, a release onto an SVI whose only member port is
   down produces a record carrying the bridge's own no-candidate reason, where
   today it produces nothing at all.
5. A held frame's failure is counted against a real port, or against no port.
   Acceptance: a timeout on a routed-port interface increments that port's
   discard counter in `Snapshot()`; a timeout on a VLAN interface increments no
   counter and creates no `Endpoint` whose `Port` is not a port.
6. A released frame keeps its ingress priority on every egress shape.
   Acceptance: a frame arriving tagged with PCP 5, held, then released onto an
   untagged access port and onto a routed port, transmits at PCP 5 in both,
   equal to the same frame with the neighbor already resolved.
7. `fabric/README.md` says where a release happens, agreeing with the direction
   record. Acceptance: `fabric/README.md`'s released-held-frame sentence names
   the observing `Forward` as the releasing call, as
   `docs/architecture/2026-09-10-virtual-device-direction.md:586-589` does; it
   today says the release happens during a wake-up.

## Out of scope

- Consolidating `traffic/mirror.go` and `bridge/bridge.go`'s inline priority
  derivations with `ethernet.Frame.Priority`; their S-Tag handling differs and
  changing it moves priority for every frame.
- The neighbor observation mode gate (`switch.go:790-810`), which guards a
  state phase 4b established is not constructible. Leaving an inert documented
  guard is this repository's existing practice; removing it is a separate call.
- `DiscardHeld`'s silence, which is a decision phase 4b took and pinned, not a
  defect. See Decisions.
- `Layer.Observe`'s handling of `adv.Router`, set at the seam and read nowhere.
- Journey identity linking a released frame to the journey that held it, which
  parent U6 owns.

## Units

### U1. `Originate` validates encodability before it queues
Files: `src/common/netsim/vswitch/routing/layer.go`,
`src/common/netsim/vswitch/routing/neighbor.go`,
`src/common/netsim/vswitch/routing/layer_test.go`
After: none
Change: `Originate` calls `hdr.Encode(payload)` before `resolveNeighbor`,
discards the encoded bytes, and on error returns `ReasonBadHeader` with a drop
step at that position, so nothing unencodable reaches a queue. The existing
drop step at `layer.go:1042-1049` cannot be reused verbatim: its Inputs take
`neighborSnapshot(targetIface, targetAddr, lookup.mac, lookup.state)`, and no
lookup has happened yet. The new step carries
`Subject{Kind: "interface", Key: targetIface}`, Inputs
`packetSnapshot(targetIface, ethernet.Frame{EtherType: etherType}, hdr, true, "")`
and Outputs the same snapshot with `false, ReasonBadHeader` — the pre-lookup
half of what the later step carries. The later step stays for the
already-resolved path. `finishHeld`'s doc comment says the caller validated
encodability before queuing, replacing the claim that the header "decoded
successfully moments before" and "nothing about it changes here", false for
`Originate` (constructed, never decoded) and for `Route` (its closure
decrements the hop limit).
Tests: `layer_test.go` — `Originate` with a 65516-octet IPv4 payload toward an
unresolved next hop returns `ReasonBadHeader`, leaves no entry for that next
hop, and a following `Wake` reports nothing; watched failing against the
current code, where it queues and then vanishes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/routing/`

### U2. One exit slice, three causes, and the conservation test
Files: `src/common/netsim/vswitch/routing/neighbor.go`,
`src/common/netsim/vswitch/routing/layer.go`,
`src/common/netsim/vswitch/routing/README.md`,
`src/common/netsim/vswitch/routing/neighbor_test.go`,
`src/common/netsim/vswitch/routing/neighbor_internal_test.go`,
`src/common/netsim/vswitch/routing/layer_test.go`,
`src/common/netsim/vswitch/switch.go`
After: U1
Change: `HeldFrame` gains `Cause HeldCause` and `Port string`; `HeldCause` is a
string type in `neighbor.go` with `HeldReleased`, `HeldTimedOut` and
`HeldEvicted`. `Effects` collapses `Released` and `Failed` into one
`Exits []HeldFrame` in `Wake`'s existing sorted order, each element stamped
with its cause. `finishHeld` fills `Port` from `l.ifaces[h.iface].Port`, empty
for a VLAN interface. `routing.ReasonNeighborHoldOverflow` joins the reasons in
`layer.go:60-83`. `Switch.applyRoutingEffects` is left compiling against
`Exits` with today's behaviour — release on `HeldReleased`, one step per other
cause — and U3 is what makes it read the cause; `layer_test.go:336` moves to
the new shape. The README's hold-queue section says an evicted frame surfaces
in the next `Wake` under its own reason, and its `Wake` section states the
(VRF, interface, address) ordering.
Tests: `neighbor_internal_test.go` — a conservation test driving a fixed list
of sequences over three neighbors with `HoldDepth` 1 to 3, each step queuing a
uniquely marked payload or resolving, timing out, or discarding a neighbor.
After each sequence it asserts that every payload observed in
`vs.neighbors[key].queue` appears exactly once across the run's `Exits` with a
cause, that none appears twice, and that a discarded payload appears in
neither. Driven from a fixed list, not a random source, so a failure
reproduces. Watched failing by reverting U1, which restores the silent encode
exit. A separate assertion per cause catches a fix that stamps every exit the
same, which the multiset alone would not.
`neighbor_test.go` — `TestWakeReleasesInDeterministicOrder` wraps its
build-observe-`Wake` sequence in a loop of 50 with a fresh layer each
iteration, because `Wake` drains and the layer cannot be reused; the current
single-shot form passes about half the time with the sort reverted, so it is
not a gate. Add a case whose entries differ by VRF and by interface, neither of
which any test covers.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/`

### U3. The switch reports a post-release failure with its own reason
Files: `src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/switch_test.go`,
`src/common/netsim/vswitch/README.md`
After: U2
Change: `applyRoutingEffects` switches on `hf.Cause`, releasing `HeldReleased`
and recording the other two with the reasons the Decisions table names.
`Switch.DrainNeighborFailures` returns `[]NeighborDrop`, a new exported type
holding `Step trace.Step`, `Port string` and `Reason trace.Reason`, so a
consumer is told the reason rather than assuming one; the pre-1.0 rule in
`AGENTS.md` permits the break, and `netsimtest/cases.go:2173` only reads
`len`, so it survives. `releaseHeldFrame`'s three existing refusal sites
(`switch.go:2846`, `:2864`, `:2879`) fill `Reason` from the refusal they
already hold. Two silent exits gain records: the VLAN arm when
`res.Egress` names no port for the frame and `res.Reason` is set, carrying that
reason; and the bare return when `routing.Interface` does not know the
interface (`switch.go:2837-2840`), carrying a stated reason. `Port` is the
egress port where one is known and `hf.Port` otherwise, empty for an SVI.
`DrainNeighborFailures`'s doc comment and `vswitch/README.md:327-330` say the
records are every exit that is not a release, not timeouts alone.
Tests: `switch_test.go` — a release refused by a down routed port records the
port's refusal, not `neighbor-miss`; a release onto an SVI whose only member
port is down records the bridge's no-candidate reason, where today nothing is
recorded at all; an eviction records `ReasonNeighborHoldOverflow`. Each watched
failing first.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/`

### U4. The fabric counts against a real port and keeps the reason
Files: `src/common/netsim/fabric/run.go`,
`src/common/netsim/fabric/journey.go`,
`src/common/netsim/fabric/routing_test.go`,
`src/common/netsim/fabric/README.md`
After: U3
Change: `recordNeighborFailure` takes a `vswitch.NeighborDrop` and reads its
`Port` and `Reason` instead of `step.Subject.Key` and a constant. A drop whose
`Port` is empty increments no counter and creates no `Endpoint`; one naming a
port counts against it. `Entry.Reason` comes from the drop.
`fabric/journey.go:70-72`'s `Entry.Step` comment covers the switch-side drop it
now carries. `fabric/README.md:413-416` says the observing `Forward` releases a
held frame, agreeing with
`docs/architecture/2026-09-10-virtual-device-direction.md:586-589`, which the
phase-4b fix pass corrected while this file kept the old claim.
Tests: `fabric/routing_test.go` — a timeout on a VLAN interface creates no
counter endpoint and the journey entry's `Reason` matches its `Step.RuleID`; a
timeout on a routed port increments that port's discard counter in
`Snapshot()`. The existing `TestFabricRecordsNeighborFailureOnHeldFrameTimeout`
names its interfaces after its ports, which is why the mapping defect is
invisible; the new case uses an SVI.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric/`

### U5. A released frame's priority reaches the wire
Files: `src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/switch_test.go`,
`src/common/netsim/fabric/run.go`,
`src/common/netsim/fabric/routing_test.go`
After: U4
Change: `Emission` gains `PCP vlan.PCP`, set from the bridge's egress decision
on `releaseHeldFrame`'s VLAN arm and from `hf.PCP` on its routed arm. The four
protocol append sites (`switch.go:2688`, `:2696`, `:2745`, `:2799`) set
`PCP: 0` explicitly, which is what `framePCP` returns for them today because
`bridge.OriginateFrame` tags at priority 0 (`bridge/bridge.go:1544-1546`) and
the two untagged sites carry no tag. `Fabric.injectEmission` transmits `em.PCP`
instead of `framePCP(em.Frame)`, and `framePCP` (`fabric/run.go:121`) is
deleted, having no other caller.
Tests: `switch_test.go` — a frame arriving tagged at PCP 5, held, released onto
an untagged access port and onto a routed port, egresses at PCP 5 in both;
watched failing against the current code, where both give 0.
`fabric/routing_test.go` — the same frame in a fabric run queues on the PCP 5
queue, equal to the same frame with the neighbor already resolved.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/ src/common/netsim/fabric/`

Waves: U1 | U2 | U3 | U4 | U5

The graph is a chain. U2 changes the type U3 reads; U3 changes the type U4
reads; U4 and U5 both edit `fabric/run.go`, and U3 and U5 both edit
`switch.go`. Re-cutting to run any pair together would put two workers in one
file, which the phase-4b fix pass showed produces two guards for one property
and a test that pins neither. U5 has no semantic dependency on U2 — `hf.PCP`
exists today (`neighbor.go:80`) — and is last only for the file overlap.

## Verification

```bash
go test -race ./src/common/net/... ./src/common/netsim/...
go vet ./src/common/net/... ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
go test -race -count=20 ./src/common/netsim/vswitch/routing/
```

The last run is not decoration: two of this phase's defects are order- and
timing-dependent, and a single-shot run of the ordering test passes about half
the time against the code it guards.

No lab or device check applies; the whole phase runs in `go test` with
in-memory inputs and a logical clock.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] The conservation test fails when U1's or U2's production change is
      reverted, and the report names which revert produced which failure.
- [ ] The two silent exits U3 names produce records, each proved by a test
      watched failing first.
- [ ] `routing/README.md`, `vswitch/README.md`, `fabric/README.md` and
      `fabric/journey.go`'s `Entry.Step` comment land with their code.
- [ ] `fabric/README.md` and the direction record agree on where a release
      happens.
- [ ] No cause is stamped by a reader that did not observe it: `HeldCause`
      carries only the three exits `routing` observes, and a post-release
      failure is a `NeighborDrop`, not a fourth cause.
- [ ] This plan's `status` set with an outcome note under its title, and
      `U4c. Landed:` filled in the parent.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

1. **Should the switch pass its classified `in.PCP` into `Route` rather than
   letting `routing` re-derive it from the frame?** `routing/layer.go:817`
   calls `f.Priority()` while the live VLAN path feeds `bridge.Ingress.PCP`
   into `assembleRouteResult` (`switch.go:1137`). They agree today only because
   the bridge reads the same outer C-tag. A port default priority or a
   regeneration table in the bridge would make a held frame and a live frame
   diverge silently. Recommendation: leave it and revisit when the bridge gains
   either; the change is a `Route` signature change across roughly 35 test call
   sites for a divergence not reachable today. Unconfirmed.
2. **Does an evicted frame deserve a trace step from the routing layer
   itself?** `Route` emits a step for every other decision it makes, but an
   eviction happens to a frame whose own `Route` call already returned. The
   switch is the only thing that turns `Exits` into steps today. Left open
   because answering it decides whether `Exits` is the only channel or one of
   two.
3. **`Fabric.Inject` reports an error whenever `res.Reason != ""`
   (`fabric/run.go:200-207`), after `resolveNeighbor` has already queued the
   frame.** A host packet toward an unresolved neighbor is therefore refused to
   its caller while sitting in a hold queue that belongs to no journey. It is
   outside every unit here, and it is a frame entering a hold queue whose exit
   no journey can account for. Whether `Inject` should refuse before committing,
   or own a journey, is a question for parent U6, which holds journey identity.
4. **Does `bridge.replicate`'s no-candidate return
   (`bridge/bridge.go:1222-1234`) leave the same hole in other callers?** It
   sets `Reason` and appends a step but returns no `Egress` entry, so any
   caller reading only `res.Egress` inherits U3's defect. The live forwarding
   path also reads `res.Outcome`, so it is covered; whether every other caller
   does was not checked here.
