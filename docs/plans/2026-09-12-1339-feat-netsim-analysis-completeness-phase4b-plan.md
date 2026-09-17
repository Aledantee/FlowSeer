---
title: Network Simulation Analysis Completeness, Phase 4b - Plan
type: feat
date: 2026-09-16
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 4b: neighbor lifecycle and address resolution - Plan

> Implemented. 6 units, 2026-09-16T17:39Z to 2026-09-17T08:47Z. The neighbor
> lifecycle, both codecs, the hold queue, the switch and fabric seam, the
> direction record, and the corpus case all landed. The plan's `CurrentResult`
> for the corpus case described a drop the tree stopped giving once the seam
> landed; Decisions carries the ruling that replaced it.

## Goal

A routed frame whose next hop has no link-layer address gets an answer that
distinguishes "nobody has observed this neighbor yet" from "resolution ran out
of time" from "this VRF does not resolve neighbors at all". The neighbor table
gains a state and a time per entry, an observed ARP or Neighbor Advertisement
moves an entry, and a frame held during resolution either leaves on a later
logical tick or fails for a stated reason. The means are a neighbor state
machine and a per-neighbor hold queue in `src/common/netsim/vswitch/routing`,
two new codecs under `src/common/net`, and the switch's existing
`Wake`/`NextWake`/`Drain` timer facility.

Stop condition: the plan is wrong if a released frame cannot re-enter the
fabric as ordinary data. `Fabric.injectEmission` labels everything it injects
`Protocol: true` (`src/common/netsim/fabric/run.go:890-919`), which four tests
rely on to separate protocol frames from data frames
(`src/common/netsim/fabric/stp_test.go:327`, `lag_test.go:883`). If that label
cannot be made to follow the frame's nature rather than its injection path,
holding is a switch-level answer only and the fabric half belongs to parent U6.

This phase claims parent R20 and R21 and extends R9, R37, and R39. Route
selection, recursion, and withdrawal (R19, R22) landed in
`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-phase4-plan.md`
as `847b503a..0dd267d7`.

## What the landed tree does today

Read before the decisions, because the previous draft of this phase plan cited
line numbers that phases 3b through 4 have since moved.

- `routing.Neighbor` is a static configuration record of interface, address,
  and MAC with no state and no time
  (`src/common/netsim/vswitch/routing/config.go:43`). `newLayer` loads the
  configured entries into a map keyed by interface and address
  (`src/common/netsim/vswitch/routing/layer.go:283`).
- A lookup miss is an immediate terminal drop with `ReasonNeighborMiss`, in
  `Route` (`src/common/netsim/vswitch/routing/layer.go:755`) and in
  `Originate` (`:897`). `assembleRouteResult` turns any reason other than
  `ReasonNotRouted` into `trace.Dropped`
  (`src/common/netsim/vswitch/switch.go:1441`), and nothing raises an analysis
  issue for it, so the result is `Dropped` with status `Complete`. That is the
  false answer parent R20 names: netsim reports a definite failure for a
  question it never asked.
- `routing.Layer` has no `Clone`, no `Age`, and no `Wake`, and `Route` takes
  neither a `now` nor a commit flag. It is the only forwarding layer with no
  notion of time, and the only one `Peek` cannot ask without mutating, once it
  has anything to mutate.
- The routing layer is not only the switch's. `fabric` builds one per host as
  that host's IP stack (`src/common/netsim/fabric/fabric.go:473-482`, from
  `HostRoutingConfig` at `src/common/netsim/fabric/config.go:262`), and
  `Inject` calls its `Originate` and returns an error when the result carries a
  reason (`src/common/netsim/fabric/run.go:200-207`). `Fabric.scheduleWake`
  walks `f.switches` only (`run.go:922-927`), so nothing ever wakes a host
  stack.
- `vswitch.Derive` has no routing arm at all
  (`src/common/netsim/vswitch/derive.go:30-160`). Every `Derive` rebuilds the
  routing layer from configuration, which is correct only while the layer holds
  nothing but configuration.
- `Layer.Owns` admits a frame only when its destination MAC equals the
  interface MAC and its EtherType is IPv4 or IPv6
  (`src/common/netsim/vswitch/routing/layer.go:599`). An ARP frame fails on
  EtherType, so it never reaches the routing layer. A Neighbor Advertisement
  addressed to the interface MAC passes `Owns` and is routed as an ordinary
  IPv6 packet, and on a routed port the block at `switch.go:888-950` returns
  before the bridge path is reached at all.
- `src/common/net` holds `ethernet`, `igmp`, `ip`, `lacp`, `mld`, `netaddr`,
  and `vlan`. There is no `arp` and no `ndp`, and no ICMPv6 constant outside
  `mld`'s unexported `icmpv6Protocol = 58`
  (`src/common/net/mld/mld.go:16`). `ethernet.EtherTypeARP` does exist
  (`src/common/net/ethernet/ethernet.go:25`).
- The timer facility a released frame needs is already built.
  `Switch.Wake(now)` drives each layer's `Wake` and turns the returned
  `Effects.Emissions` into `Emission{Port, Frame}` values
  (`src/common/netsim/vswitch/switch.go:2554`, `:2304`, `:2361`, `:2415`);
  `Switch.NextWake` aggregates the layers' next timer (`:2571`); `Drain`
  returns and clears the emissions (`:2489`); and the fabric run loop calls
  `Wake`, drains, and reschedules on every `ArrivalWake`
  (`src/common/netsim/fabric/run.go:330-341`, `:927`). The parent's worry that
  a hold queue would need a wake the simulation does not otherwise have is void
  against this tree. What is not built is a way for an emission to be anything
  other than a protocol frame.
- `bridge.Egress` is exported and takes all its state through its `Ingress`
  argument, including `Now` and `Commit`
  (`src/common/netsim/vswitch/bridge/bridge.go:890`), and
  `assembleRouteResult` already calls it with a synthetic ingress whose `Port`
  is empty (`src/common/netsim/vswitch/switch.go:1474-1484`). Releasing a held
  frame onto a VLAN interface needs no new egress path.

## Decisions

- **One state machine serves IPv4 and IPv6.** RFC 4861 section 7.3.2 defines
  the state set for IPv6 and RFC 826 defines none for IPv4, and Linux runs
  both families through one neighbour table. Netsim keeps `Unobserved` (no
  entry), `Incomplete`, `Reachable`, `Stale`, and `Failed`, which is parent
  R20's five names mapped onto the RFC's vocabulary rather than a second
  vocabulary invented for ARP.
  <https://www.rfc-editor.org/rfc/rfc4861#section-7.3.2>
- **`Delay` and `Probe` are deliberately absent.** Both exist in RFC 4861 only
  to schedule a unicast solicitation, and netsim never solicits. Carrying two
  states no input can leave would mean carrying two states no test can reach.
  The package comment says so, so a reader does not take the omission for an
  oversight.
- **A `Stale` entry forwards on its cached MAC.** RFC 4861 section 7.3.2 says
  of `STALE` that "until traffic is sent to the neighbor, no attempt should be
  made to verify its reachability", and the cached link-layer address stays
  usable. Netsim forwards on it and leaves the entry `Stale`, where real gear
  would move to `Delay` and start probing. The routing README says so, because
  an implementer reading the state name alone would reasonably drop instead,
  and a drop here would be exactly the invented failure this phase removes.
- **The switch never solicits, and `Failed` says what netsim means by it.**
  Nothing here generates an ARP request or a Neighbor Solicitation, which is
  the same decision phase 3 took for multicast queries: "the switch does not
  emit that query itself" (`docs/architecture/2026-09-10-virtual-device-direction.md:258`).
  An entry therefore reaches `Failed` when nothing was observed within
  `ResolutionTimeout`, not after MAX_MULTICAST_SOLICIT retransmissions, and the
  README and the state's doc comment say that in those words. Why: an
  autonomous host stack is a parent omission, a capture often holds the reply
  and not the request, and a state whose name promises retransmissions netsim
  never sent would be the same kind of false answer R20 exists to remove.
- **Codecs are pure, and the state machine takes a family-neutral record.**
  `src/common/net/arp` and `src/common/net/ndp` encode and decode. The state
  machine takes `routing.Advertisement`, holding the interface, the address,
  the MAC, whether a link-layer address was supplied, and the solicited,
  override, and router flags; the switch decodes a frame into one.
  `mcast.Learn` takes an `igmp.Message` and is the precedent for the other
  choice, but multicast needs the whole record list where address resolution
  reduces to those seven fields, and a neutral record keeps one entry point for
  two wire formats and lets the codecs and the state machine be built in the
  same wave.
- **ARP maps onto the RFC 4861 section 7.2.5 table through the override flag.**
  An ARP reply becomes `{Solicited: true, Override: true, Router: false}` and
  an ARP request's sender fields become
  `{Solicited: false, Override: true, Router: false}`. RFC 826's merge rule
  overwrites the hardware address of a known sender unconditionally, and
  `Override: true` is what makes rule II of section 7.2.5 produce exactly that,
  while `Solicited` carries the difference between a reply, which confirms the
  forward path, and a request, which only refreshes the binding. Without this
  mapping every IPv4 observation is the implementer's guess, and an ARP reply
  mapped to `Solicited: false` would land in `Stale` and fail R21a.
- **Resolution is per VRF and configured.** `VRF` gains a `NeighborPolicy`
  with a `Mode` (`NeighborObserved` or `NeighborDisabled`), `ReachableTime`,
  `ResolutionTimeout`, and `HoldDepth`. Defaults come from RFC 4861 section 10
  where it has a number: REACHABLE_TIME 30,000 milliseconds, and
  MAX_MULTICAST_SOLICIT 3 transmissions times RETRANS_TIMER 1,000 milliseconds
  for a 3 second resolution timeout. `HoldDepth` is 3.
  <https://www.rfc-editor.org/rfc/rfc4861#section-10>
- **`Validate` refuses a policy the layer cannot run.** A `Mode` outside the
  two named values, a `HoldDepth` below 1 under `NeighborObserved`, and a
  `ResolutionTimeout` of zero under `NeighborObserved` are construction errors,
  because each makes the hold path unreachable while the configuration claims
  it. A `ReachableTime` shorter than `ResolutionTimeout` is valid: the two
  timers govern different entries and no invariant couples them.
- **The hold queue is per neighbor, small, and drops the oldest.** RFC 4861
  section 7.2.2: "The queue MUST hold at least one packet, and MAY contain
  more. However, the number of queued packets per neighbor SHOULD be limited
  to some small value. When a queue overflows, the new arrival SHOULD replace
  the oldest entry." `HoldDepth` defaults to 3 rather than the permitted
  minimum of 1, because an analysis library is asked which of several frames
  arrived and a depth of 1 answers that for the last one only.
  <https://www.rfc-editor.org/rfc/rfc4861#section-7.2.2>
- **Holding and observing happen only on a commit.** `Route` and `Originate`
  take a trailing `commit bool`, and with it false they return
  `ReasonNeighborPending` without creating an entry and without queueing the
  frame. `Switch.Peek` is `s.forward(now, ingress, f, false)`
  (`src/common/netsim/vswitch/switch.go:557`) and `vswitch.Compare` runs `Peek`
  on both sides so that a preview cannot perturb live state
  (`src/common/netsim/vswitch/compare.go:23-25`). Today `mutate` never reaches
  the routing layer because the layer has no state to protect; the moment it
  has, a peek that queued a frame would make a second `Compare` answer
  differently from the first.
- **A held frame is a new outcome, not a drop.** `trace.Held` joins
  `Forwarded`, `Flooded`, `Dropped`, and `Consumed`, paired with
  `routing.ReasonNeighborPending`. R21's acceptance example requires a frame
  that neither arrived nor failed, and reusing `Dropped` with a softer reason
  would make every existing drop consumer count it
  (`src/common/netsim/fabric/run.go:465`). No existing outcome consumer needs a
  branch for the new value: `run.go:465`, `vswitch/compare.go:27`,
  `trace/render.go:19-26`, and the corpus comparisons are all equality or
  emptiness tests, and a `Held` result carries no egress, so the mirror
  override at `switch.go:1296-1299` does not see it.
- **An emission says whether it is a protocol frame.** `vswitch.Emission`
  gains `Protocol bool`, set for the BPDUs, LACPDUs, and loop-protect probes
  that set it implicitly today, and clear for a released held frame.
  `Fabric.injectEmission` reads it instead of hardcoding `Protocol: true`
  (`src/common/netsim/fabric/run.go:906`). Without this a released user frame
  would count as a protocol frame in every test that separates the two
  (`src/common/netsim/fabric/stp_test.go:327`, `lag_test.go:883`). Linking the
  released journey back to the journey that was held is a different question
  and belongs to parent U6, which owns journey result states.
- **A frame the hold queue gives up on is reported, not discarded.**
  `Effects` carries `Failed []HeldFrame`, each holding the frame, its egress
  interface, and `ReasonNeighborMiss`, beside `Released`. `Switch.Wake` turns
  each into a trace step and a counted drop rather than dropping it silently.
  A frame that vanishes with no step, no entry, and no issue is the same class
  of silent answer R20 exists to remove, and the corpus admission bar requires
  every case to bind its expected rules and facts to something
  (`src/common/netsim/internal/netsimtest/corpus.go:562-569`).
- **Pending is `Incomplete`; disabled and failed are `Complete`.** A pending
  result raises `IssueNeighborUnresolved` at
  `routing.NeighborLookupScope(...)` with `analysis.Incomplete`, through the
  per-journey hit mechanism `mcastQueryUnobservedIssues` already uses
  (`src/common/netsim/vswitch/switch.go:685`). A VRF with `NeighborDisabled`,
  and an entry already in `Failed`, keep `ReasonNeighborMiss` and status
  `Complete`, because there netsim does know the outcome. This is the
  distinction R20 exists to draw and the decision most likely to be argued
  with.
- **A host stack resolves no neighbors.** `HostRoutingConfig`
  (`src/common/netsim/fabric/config.go:262`) writes `NeighborDisabled` into
  every host's VRF. `Inject` is synchronous and answers with an error
  (`src/common/netsim/fabric/run.go:200-207`), and `scheduleWake` never walks
  `f.hostStacks` (`run.go:922-927`), so a host stack that held a frame would
  hold it forever and a rejected injection would leave an `Incomplete` entry
  behind it. The switch is where resolution is modelled; a host is a packet
  source.
- **A model-loaded device does resolve, and the downgrade is intended.**
  `netmodel` keeps the default `NeighborObserved`. It already builds
  `routing.Neighbor` entries from the model
  (`src/common/netsim/vswitch/netmodel/netmodel.go:1651`) and already degrades
  a neighbor without a MAC through `IssueMissingNeighborMAC`
  (`netmodel.go:68`), so a next hop the model never reported becoming `Held` at
  `analysis.Incomplete` is the honest answer for a shadowed topology rather
  than a regression. The two assertions that expect `ReasonNeighborMiss`
  (`src/common/netsim/vswitch/netmodel/routing_test.go:737` and
  `acceptance_pass10_test.go:155`) move to the new answer in U5.
- **An observation never creates an entry from an advertisement.** RFC 4861
  section 7.2.5: "If no entry exists, the advertisement SHOULD be silently
  discarded." RFC 826's merge rule is the same on this axis: a reply updates an
  existing binding, and it is the request's sender fields that may create one
  for a target that is the receiving interface itself. Keeping both makes an
  injected reply for an address nothing ever routed to a no-op rather than a
  silent way to populate the table.
  <https://www.rfc-editor.org/rfc/rfc4861#section-7.2.5>
- **Ruled during implementation: `Encode` takes the Ethernet destination
  rather than deriving it from the message.** The payload's target hardware
  address and the frame's destination are independent, and coincide only for a
  reply: a request carries zeros in the payload (the sender is asking for that
  address) and goes to the broadcast address. Deriving one from the other
  cannot express a request at all, and returning a frame whose destination the
  caller must overwrite is a trap for the next one. `lacp` gets away with a
  derived destination because it always sends to one group address; ARP does
  not. Cost if wrong: one signature and its two call sites.
- **Ruled during implementation: zero means "unset", not invalid.** The
  Decisions called a `HoldDepth` below 1 and a zero `ResolutionTimeout`
  construction errors. Implemented literally that refuses a `VRF` whose
  optional fields are left at their Go zero value, which is how the rest of
  this package is configured and tested: `Route.Preference` already treats 0
  as "unset" and normalizes it to 1 rather than refusing it. `Validate` now
  refuses only a negative value, and zero normalizes to the RFC default.
  Validation judges what the constructor builds, not what the caller wrote.
  Cost if wrong: two comparisons and their refusal cases.

- **Open: a rewritten cross-package test.** `TestSwitchPeekAndForwardAgree`
  proved `Peek` and `Forward` agree through a `Switch`; it was rewritten as
  `TestPeekAndCommitAgreeOnSelection` against `routing.Layer` alone, because
  `vswitch` does not compile until the switch unit lands. The seam it covered
  is the one this phase most changes, so the switch-level test is restored in
  that unit rather than left as the narrower one.
- **Observation is a side effect, not an interception.** An ARP frame keeps
  its ordinary bridged path after the routing layer has read it, unlike an
  IGMP report, which `forwardMulticastControl` redirects to router ports
  (`src/common/netsim/vswitch/switch.go:1083`). An ARP request is a broadcast
  the rest of the segment must still receive, and a switch that swallowed it
  would break the very resolution it is modelling.
- **`Derive` retains the observed table and never the held frames.** The
  retention arm is keyed on `len(routing.Diff(...)) == 0`, built exactly like
  the spanning tree arm at `src/common/netsim/vswitch/derive.go:43-45`,
  comparing both sides as `New` left them and nil-guarding both the layer and
  the configuration the way that arm does. This matters concretely: `New`
  stamps the assigned base MAC onto every zero routed interface
  (`src/common/netsim/vswitch/switch.go:77`), so diffing a raw input against a
  filled one would rebuild the routing layer on every derive
  (`docs/solutions/architecture-patterns/validate-and-derive-judge-what-new-builds.md`).
  Held frames are dropped on derive: a frame in flight belongs to a run, and
  parent U5 owns run-state retention. A derived switch that silently carried
  someone else's in-flight frames would make two forks compare unequal for a
  reason neither configuration shows.
- **Every new configuration field gets a `Diff` arm in the same unit that adds
  it.** `routing.Diff` reports neighbor policy changes under a
  `neighbor-policy` subject kind alongside the existing `vrf`, `interface`,
  `route`, and `neighbor` kinds (`src/common/netsim/vswitch/routing/diff.go:213`,
  `:254`, `:345`, `:415`). Without the arm, `Derive`'s reuse test reads every
  policy change as no change and reuses a layer configured differently.
- **Every codec ships one literal golden vector.** A round trip through one's
  own codec passes for any bijection, including a swapped one, which is how an
  MST BPDU shipped with two identifiers in each other's octets
  (`docs/solutions/conventions/a-codec-round-trip-cannot-locate-a-field-on-the-wire.md`).
  Each vector's swappable fields differ in every octet, and the ICMPv6
  checksum appears as a hand-computed literal with its working in a comment,
  the way `src/common/net/ip/ip_test.go:16-42` does it.

Ruled: the corpus case records `Held` with `neighbor-pending` as the tree's
present answer, and the drop this plan wrote into `CurrentResult` moves to
`FalseAnswer`. Why: `CurrentResult` was written before the switch and fabric
seam landed, when an unresolved next hop was still a `Complete` drop, and a
case whose `CurrentResult` describes an answer the tree stopped giving tells
a later reader the corpus disagrees with the code. Cost if wrong: two strings
in one case function.

Ruled: the case's neighbor-unresolved evidence `Context` is a literal string,
not one built from the values the switch writes. Why: an expectation computed
from the production side follows a renamed issue code or a reshaped scope
wherever it goes and still passes, which is the one thing the exact-match
corpus exists to catch; every other case writes the string out. Cost if
wrong: the literal has to be re-read off a failure message when the scope
rendering changes on purpose.

## Requirements

1. **R20a.** A neighbor entry carries one of `Unobserved`, `Incomplete`,
   `Reachable`, `Stale`, or `Failed`, and a time at which it next changes.
   Acceptance example: with `NeighborObserved` and nothing observed, a routed
   frame to `10.0.99.7` creates an `Incomplete` entry expiring at `now + 3s`
   rather than resolving to an absent entry. Second acceptance example: an
   entry that reached `Reachable` at `t0` and `Stale` at `t0 + 30s` still
   forwards a frame at `t0 + 31s` on its cached MAC and stays `Stale`.
2. **R20b.** An absent entry is a terminal drop only when the VRF resolves no
   neighbors or the entry already failed. Acceptance example: the same frame
   under `NeighborDisabled` returns `trace.Dropped` with
   `routing.ReasonNeighborMiss` and metadata status `analysis.Complete`; under
   `NeighborObserved` it returns `trace.Held` with
   `routing.ReasonNeighborPending` and status `analysis.Incomplete`.
3. **R20c.** A preview leaves the neighbor table and the hold queues
   untouched. Acceptance example: `Peek` of that same frame returns `Held`
   with `ReasonNeighborPending`, and the entry for `10.0.99.7` is still
   `Unobserved` afterwards, so two consecutive `vswitch.Compare` calls over the
   same pair of switches return equal results.
4. **R21a.** An observed ARP reply or Neighbor Advertisement moves an entry
   without any frame being generated. Acceptance example: an ARP reply from
   `10.0.99.7` at `02:11:22:33:44:55` arriving on `out-a` moves the entry from
   `Incomplete` to `Reachable` with expiry `now + 30s`, and `Switch.Drain`
   holds no request frame at any point in the exchange.
5. **R21b.** A held frame leaves at a stated logical time or fails with a
   recorded reason. Acceptance example: a frame held at `t0`, an ARP reply
   observed at `t0+1s`, and `Switch.Wake(t0+1s)` produce one
   `Emission{Port: "out-a", Protocol: false}` whose frame's destination MAC is
   `02:11:22:33:44:55`; with no reply, `Switch.Wake(t0+3s)` produces no
   emission, moves the entry to `Failed`, and yields one trace step with
   `trace.OpDrop` and `routing.ReasonNeighborMiss` naming the frame that was
   held, rather than discarding it silently.
6. **R21c.** The hold queue is bounded and drops the oldest. Acceptance
   example: with `HoldDepth: 3`, four frames held for one neighbor release
   three, and the released set is frames two through four in arrival order.
7. **R9.** `NeighborPolicy` and every field of it participate in `Validate`,
   `Normalize`, `Clone`, and `Diff`. Acceptance example: changing only
   `ReachableTime` from 30s to 60s yields exactly one `trace.Change` with
   subject kind `neighbor-policy`, and `Derive` rebuilds the routing layer
   rather than reusing it.
8. **R37.** The same injected sequence on two separately constructed switches
   yields equal held-frame outcomes, equal released frames, and equal traces.
   Acceptance example: the corpus fixture's two executions compare equal under
   `AssertCase`.
9. **R39.** The corpus admits a case whose current false answer is the
   absent-entry terminal drop. Acceptance example:
   `troubleshooting/neighbor-resolution-pending` records `CurrentResult` as
   `Dropped` with `neighbor-miss` at status `Complete`, and expects `Held` with
   `neighbor-pending` at status `Incomplete`.

## Out of scope

- Generating ARP requests or Neighbor Solicitations, and any other autonomous
  host behavior. `Failed` is reached by timeout, never by retransmission.
- Address resolution on a host stack, which this phase disables outright.
- Duplicate address detection, unsolicited advertisement conflict handling
  beyond the RFC 4861 section 7.2.5 override rule, and proxy ARP.
- Neighbor Unreachability Detection, which is what `Delay` and `Probe` exist
  for and which needs the solicitations this phase does not send.
- Router Solicitation, Router Advertisement, Redirect, and the prefix and MTU
  options of RFC 4861. The `ndp` package covers types 135 and 136 only.
- Linking a released frame's journey back to the journey that was held, and
  retention of held frames across `Derive` and across a fork, which parent U5
  and U6 own.
- Comparison of the new outcome across forks, which parent U7 owns.

## Units

### U1. Amend the direction record with the neighbor lifecycle

Files: `docs/architecture/2026-09-10-virtual-device-direction.md`
After: none
Change: the routing decision gains a "Neighbor resolution and held frames"
subsection. It defines `neighbor-miss`, which the record names at `:533`
without ever saying what it is; states the five-state lifecycle and why `Delay`
and `Probe` are absent; names the new `arp` and `ndp` packages under
`src/common/net`, which the record refers to collectively at `:64` and does not
enumerate anywhere, so the value-types sentence at `:121-124` is left alone;
states that the switch observes and never solicits, in the same words as the
multicast decision at `:258`, and that a host stack does not resolve at all;
and states that a frame held during resolution rides the wake and emission
facility the run model already describes at `:82-84`, and that an emission now
says whether it is a protocol frame. The "Remaining capability gaps" list at
`:622-624` gains solicitation and neighbor unreachability detection, beside the
dynamic routing entry already there.
Tests: none; the record is prose, and the behavior it describes is tested in
U4, U5, and U6.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture/2026-09-10-virtual-device-direction.md`

### U2. ARP codec

Files: `src/common/net/arp/arp.go`, `src/common/net/arp/arp_test.go`,
`src/common/net/arp/README.md`
After: none
Change: `Encode(m Message, dst netaddr.MAC) (ethernet.Frame, error)` and
`Decode(f ethernet.Frame) (Message, error)`, following `src/common/net/lacp`,
which is the existing codec that rides Ethernet directly and checks the
EtherType itself (`src/common/net/lacp/lacp.go:88`, `:108`). `Message` carries
`HardwareType`, `ProtocolType`, `Operation`, `SenderMAC`, `SenderAddr`,
`TargetMAC`, and `TargetAddr`, with `Request` and `Reply` as 1 and 2 per RFC
826. Two sentinels, `ErrMalformed` and `ErrUnsupported`, declared with
`errors.New` exactly as `src/common/net/igmp/igmp.go:82-87` declares its pair,
and wrapped at each call site with the `errs.From(...)` builder carrying
structured attributes, which is what `docs/code-style.md:212` asks of an error
that carries attributes. Decode refuses an EtherType other than
`ethernet.EtherTypeARP`, a payload shorter than 28 octets, a hardware type
other than 1, a protocol type other than `0x0800`, a hardware length other than
6, a protocol length other than 4, and an address that is not IPv4.
The README follows the `igmp` and `mld` shape: an example, an offset table, an
errors section, and a sources section citing RFC 826.
Tests: `TestARPEncodePlacesEveryFieldAtItsOffset` asserts absolute offsets
against a literal written from RFC 826, with sender MAC `02:11:22:33:44:55`
against target MAC `06:aa:bb:cc:dd:ee` and sender address `10.1.2.3` against
target address `192.168.9.7`, so that swapping either pair fails every octet:
`[0:2]` hardware type, `[2:4]` protocol type, `[4]` hardware length, `[5]`
protocol length, `[6:8]` operation, `[8:14]` sender hardware address,
`[14:18]` sender protocol address, `[18:24]` target hardware address,
`[24:28]` target protocol address. `TestARPDecodeReadsTheLiteralVector` feeds
the same literal back. `TestARPRoundTrip` covers request and reply.
`TestARPDecodeRefuses` is a table over the seven refusals above, asserted with
`errors.Is`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/arp/`

### U3. Neighbor Discovery codec

Files: `src/common/net/ndp/ndp.go`, `src/common/net/ndp/ndp_test.go`,
`src/common/net/ndp/README.md`
After: none
Change: `Encode(hdr ip.Header, m Message) ([]byte, error)` and
`Decode(hdr ip.Header, payload []byte) (Message, error)`, following
`src/common/net/mld`, which takes the header because the ICMPv6 checksum covers
the IPv6 pseudo-header (`src/common/net/mld/mld.go:95`, `:349`). `Message`
carries `Type` (`NeighborSolicitation` 135, `NeighborAdvertisement` 136),
`Target`, `Router`, `Solicited`, `Override`, and an optional source or target
link-layer address with its presence flag. The checksum repeats the
pseudo-header construction at `src/common/net/mld/mld.go:609-624`, because each
codec package in `src/common/net` carries its own rather than sharing one, and
the header precondition check repeats `validateIPv6Header` (`mld.go:383`).
Decode refuses a payload shorter than 24 octets, which is the length RFC 4861
sections 7.1.1 and 7.1.2 make part of message validation and without which the
target and option reads slice out of range; a hop limit other than 255, which
sections 4.3 and 4.4 both specify; a code other than 0; a multicast target; an
option length of 0 or one that overruns the payload; a source link-layer
address option on a solicitation from the unspecified address; and a bad
checksum. The README follows the `mld` shape and cites RFC 4861 sections 4.3,
4.4, 4.6.1, 7.1.1, 7.1.2, and 7.2.5, and RFC 8200 section 8.1 for the
pseudo-header.
Tests: `TestNeighborAdvertisementEncodePlacesEveryFieldAtItsOffset` asserts
`[0]` type, `[1]` code, `[2:4]` checksum, `[4]` flags with R `0x80`, S `0x40`,
O `0x20`, `[5:8]` reserved, `[8:24]` target, `[24]` option type 2, `[25]`
option length 1, `[26:32]` link-layer address, against a literal whose target
address and link-layer address differ in every octet from the header addresses
the checksum is built over. The checksum octets are a hand-computed literal
with the one's-complement working in a comment above the vector, in the style
of `src/common/net/ip/ip_test.go:16-42`, so that neither the encoder nor the
decoder is the authority for it.
`TestNeighborSolicitationEncodePlacesEveryFieldAtItsOffset` pins the
solicitation's own shape, which is not the advertisement's: RFC 4861 section
4.3 gives it a 4-octet Reserved field at `[4:8]` and no R, S, or O flags, so
the test asserts `[4:8]` zero and does not name a flags octet, then `[8:24]`
target, `[24]` option type 1, `[25]` option length 1, `[26:32]` link-layer
address. `TestNDPRoundTrip` covers both types with and without the option.
`TestNDPDecodeRefuses` is a table over the seven refusals, asserted with
`errors.Is`, and includes a 23-octet payload and a hop limit of 254 as their
own cases because those are the two checks an implementation most often omits.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/ndp/`

### U4. Neighbor lifecycle and hold queue in the routing layer

Files: `src/common/netsim/vswitch/routing/config.go`,
`src/common/netsim/vswitch/routing/diff.go`,
`src/common/netsim/vswitch/routing/neighbor.go`,
`src/common/netsim/vswitch/routing/layer.go`,
`src/common/netsim/vswitch/routing/fact.go`,
`src/common/netsim/vswitch/routing/README.md`
After: none
Change: `VRF` gains `NeighborPolicy`, holding `Mode` (`NeighborObserved` zero
value, `NeighborDisabled`), `ReachableTime`, `ResolutionTimeout`, and
`HoldDepth`, normalized to the RFC 4861 section 10 defaults when zero and
validated as the Decisions set out. `NeighborState` and a runtime entry
carrying state, MAC, expiry, and origin (configured or observed) live in the
new `neighbor.go`; a configured `routing.Neighbor` enters the table as
`Reachable` with no expiry, so a static binding never ages out. `Layer` gains
`Observe(now, Advertisement)` applying the RFC 4861 section 7.2.5 rules over
both families, `Age(now)`, `Wake(now) Effects` carrying `Released []HeldFrame`
and `Failed []HeldFrame`, `NextWake()`, and `Clone()`, matching the `Effects`
shape of `src/common/netsim/vswitch/lag/layer.go:25`. `Route` and `Originate`
take a leading `now time.Time` and a trailing `commit bool`; on a miss under
`NeighborObserved` with `commit` set they create an `Incomplete` entry, append
the frame to that neighbor's bounded queue with drop-oldest, and return
`ReasonNeighborPending`, and with `commit` clear they return the same reason
and change nothing. Under `NeighborDisabled`, or for an entry in `Failed`, both
keep `ReasonNeighborMiss`. `neighborSnapshot` (`fact.go:76`) reports the state,
so a trace says which of the answers it was, and `Originate`'s neighbor lookup
starts calling `res.consult(NeighborLookupScope(...))` the way `Route` does at
`layer.go:754`, which it does not today. The README's worked example at
`README.md:73` is updated for the new `Route` signature, and the README gains a
"Neighbor lifecycle" section next to the existing "Withdrawn routes" section,
which already separates a withdrawal from a neighbor miss (`README.md:259-263`)
and now also separates both from pending.
Tests: `routing/config_test.go` extends the validate, normalize, clone, and
diff matrix over every `NeighborPolicy` field, including
`TestDiffReportsNeighborPolicyChange`, which is the case that fails if the
`Diff` arm is missing while `Validate`, `Normalize`, and `Clone` are all
present, and one refusal case per `Validate` rule. `routing/neighbor_test.go`
covers one case per transition of the RFC 4861 section 7.2.5 table, including
the override-clear case that moves `Reachable` to `Stale` without updating the
MAC and the override-clear case in any other state that updates nothing; the
ARP reply and ARP request flag mappings reaching `Reachable` and `Stale`
respectively; an advertisement for an address with no entry, which changes
nothing; the hold queue dropping the oldest at depth 3; the timeout to `Failed`
yielding its held frames in `Effects.Failed`; a `Stale` entry forwarding on its
cached MAC without changing state; release ordering; and `Clone` isolation.
`routing/layer_test.go` covers pending against miss against disabled, `commit`
false changing nothing, and the hop from `Failed` back to `Reachable` on a
later observation.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/routing/`

### U5. Switch, netmodel, and fabric seam

Files: `src/common/netsim/trace/trace.go`,
`src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/derive.go`,
`src/common/netsim/vswitch/README.md`,
`src/common/netsim/vswitch/netmodel/netmodel.go`,
`src/common/netsim/fabric/run.go`,
`src/common/netsim/fabric/config.go`,
`src/common/netsim/fabric/README.md`
After: U2, U3, U4
Change: `trace.Held` joins the outcome constants. In `Switch.forward`, two new
guards sit beside the LACP, BPDU, and loop-protect guards
(`switch.go:836-876`), which is the one place that sees a frame before either
the routed-port block at `:888-950` or the bridge path at `:952` claims it. The
first matches `f.EtherType == ethernet.EtherTypeARP` and decodes with
`arp.Decode`; the second matches `f.EtherType == ethernet.EtherTypeIPv6` with
`Payload[6] == 58` and `Payload[ip.V6HeaderLen]` of 135 or 136, and decodes
with `ndp.Decode`. Neither touches `multicastControlCandidate` or
`forwardMulticastControl`: that predicate requires a Hop-by-Hop header at
`Payload[6]` (`switch.go:1009-1021`) and its consumers demand hop limit 1, a
link-local source, and a Router Alert (`switch.go:1086`), every one of which
rejects a valid Neighbor Solicitation or Advertisement. Each guard hands the
routing layer an `Advertisement` when `mutate` is set and then lets the frame
continue its ordinary path, so an ARP broadcast still floods. `mutate` is also
threaded into both `s.routing.Route` call sites (`switch.go:943`, `:986`) and
into `Originate`, so `Peek` neither observes nor holds. `assembleRouteResult`
maps `ReasonNeighborPending` to `trace.Held` (`switch.go:1441`).
`vswitch.Emission` gains `Protocol bool`, set true at the three existing append
sites (`switch.go:2304`, `:2361`, `:2415`) and false for a released frame, and
`Fabric.injectEmission` reads it rather than hardcoding `Protocol: true`
(`src/common/netsim/fabric/run.go:906`). `Switch.Age` calls `routing.Age`,
`Switch.Wake` calls `routing.Wake`, turns each `Released` frame into an
`Emission` and each `Failed` frame into a trace step with `trace.OpDrop` and
`ReasonNeighborMiss`, and `Switch.NextWake` includes the routing layer's next
timer. A released frame whose egress is a VLAN interface goes out through
`bridge.Egress` with the same synthetic ingress the routed path already builds
(`switch.go:1474-1484`), and each non-dropped entry of its `Result.Egress`
becomes one `Emission`; an entry the bridge reports dropped becomes a trace
step carrying that entry's reason, because a released frame the bridge refuses
is a different answer from one it never got. `IssueNeighborUnresolved` is added
beside `IssueMcastQueryUnobserved` (`switch.go:2656`) and raised per journey
through the same hit-and-`runtimeIssue` mechanism (`switch.go:685`), scoped
with `routing.NeighborLookupScope`. `Derive` gains a routing arm modelled on
the spanning tree arm at `derive.go:43-45`, nil-guarding both sides the way
that arm does, retaining the observed neighbor table through `Clone` when
`len(routing.Diff(...)) == 0` over both sides as `New` left them, and
discarding held frames unconditionally. `HostRoutingConfig`
(`src/common/netsim/fabric/config.go:262`) writes `NeighborDisabled`, so
`Inject` keeps answering with `neighbor-miss` and leaves no entry behind
(`run.go:200-207`). `netmodel` keeps the default policy; the two assertions
expecting `ReasonNeighborMiss`
(`src/common/netsim/vswitch/netmodel/routing_test.go:737`,
`acceptance_pass10_test.go:155`) move to `Held` and
`ReasonNeighborPending`. The drop-reason table at
`src/common/netsim/vswitch/README.md:284` gains `neighbor-pending` and
restates `neighbor-miss` as the disabled or failed answer, and the `Inject`
refusal list at `src/common/netsim/fabric/README.md:360-367` says why a host
stack never reports pending.
Tests: `vswitch/switch_test.go` covers an ARP reply observed on a routed port
and on a VLAN interface, the same reply under `Peek` leaving the table
unchanged, a `Peek` of an unresolved destination leaving no `Incomplete` entry
and no queued frame, a hold at `t0` released by a reply at `t0+1s` through
`Wake` and `Drain` with the released frame's destination MAC and
`Protocol: false` asserted, a hold that times out to `Failed` and yields a drop
step rather than silence, a Neighbor Advertisement with hop limit 254 changing
nothing, an MLD report reaching the multicast path and not the neighbor path
while a Neighbor Advertisement reaches the neighbor path and not the multicast
one, and the `Held` result carrying `IssueNeighborUnresolved` at status
`Incomplete` while a disabled VRF carries `Dropped` at `Complete`.
`vswitch/derive_test.go` adds
`TestDeriveRebuildsRoutingWhenNeighborPolicyChanges`, which fails without U4's
`Diff` arm, and `TestDeriveKeepsObservedNeighborsAndDropsHeldFrames`.
`vswitch/compare_test.go` adds a test that two consecutive `Compare` calls over
an unresolved destination return equal results. `fabric/routing_test.go` adds a
two-node run where an injected ARP reply releases a held frame, the released
frame's journey is not marked `Protocol`, and the peer records a delivery, plus
a test that `Inject` on a host with no neighbor still fails with
`neighbor-miss`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/trace/ src/common/netsim/vswitch/ src/common/netsim/fabric/`

### U6. Corpus case

Files: `src/common/netsim/internal/netsimtest/cases.go`,
`src/common/netsim/internal/netsimtest/corpus_test.go`,
`src/common/netsim/internal/netsimtest/README.md`
After: U5
Change: `CaseTroubleshootingNeighborResolutionPending` is added and registered
in `RegisterRoutingCases` (`cases.go:2174`). Its fixture builds a routed-port
switch with a route whose next hop has no configured neighbor, forwards an IPv4
frame at `t0`, and asserts that result: outcome `trace.Held`, reason
`routing.ReasonNeighborPending`, metadata carrying `IssueNeighborUnresolved` at
`analysis.Incomplete` scoped to the neighbor lookup, and a complete step list
ending at the hold. `CurrentResult` records the tree's present answer, `Dropped`
with `neighbor-miss` at `Complete`, and `FalseAnswer` records what that answer
claims. The fixture goes on to observe an ARP reply at `t0+1s` and call `Wake`,
so the case's own execution proves the hold is releasable, in the style of
`CaseTroubleshootingLeaveLastMemberQuery` (`cases.go:1702`).
The README's admitted-case catalogue gains its entry.
Tests: `TestRegistryDeterministicOrdering` moves from 25 to 26 cases and gains
`troubleshooting/neighbor-resolution-pending` between
`troubleshooting/loop-protect-contains-access-loop` and
`troubleshooting/recursive-route-not-installed`;
`TestCorpusAdmitsAndExecutesEveryCaseDeterministically` picks the case up
without a change.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/internal/netsimtest/`

Waves: U1 U2 U3 U4 | U5 | U6

## Verification

```bash
go test -race ./src/common/net/... ./src/common/netsim/...
go vet ./src/common/net/... ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

No lab or device check applies; the whole phase runs in `go test` with
in-memory inputs and a logical clock, as parent R38 requires.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `docs/architecture/2026-09-10-virtual-device-direction.md` describes the
      neighbor lifecycle, and `neighbor-miss` is defined where it is used.
- [ ] `src/common/net/arp/README.md`, `src/common/net/ndp/README.md`, the
      neighbor section of `src/common/netsim/vswitch/routing/README.md`, the
      drop-reason table in `src/common/netsim/vswitch/README.md`, and the
      `Inject` refusal list in `src/common/netsim/fabric/README.md` land with
      their code.
- [ ] Each codec has a literal golden vector whose swappable fields differ in
      every octet, and the ICMPv6 checksum in the vector is hand-computed.
- [ ] `NeighborPolicy` appears in `Validate`, `Normalize`, `Clone`, `Diff`,
      and `Derive`, with a test that fails if the `Diff` arm is removed.
- [ ] Pending, failed, and disabled give three different answers, and only
      pending is `Incomplete`.
- [ ] `Peek` and `Compare` change no neighbor state and queue no frame.
- [ ] No frame leaves a hold queue without an emission or a trace step.
- [ ] Nothing in `Switch.Drain` is a solicitation at any point in any test.
- [ ] This plan's `status` is set with an outcome note under its title.
- [ ] No plan labels appear in code, comments, or commit messages.

## Open questions

1. **What does a released frame's journey say about the journey that held
   it?** U5 makes the released frame a data-frame journey rather than a
   protocol one, which is enough for R21b, but the fabric still records two
   journeys where a person sees one frame. Linking them needs a journey
   identity that survives a hold, which parent U6 introduces with its journey
   result states
   (`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md:632`).
   Left open because deciding it here would pre-empt U6's vocabulary.
2. **Does `trace.Held` survive parent U6?** The same U6 may want to own the
   outcome vocabulary this phase adds. This phase adds it anyway, because
   R21's acceptance example has no other way to say what happened, and accepts
   that U6 may rename it. The alternative, waiting for U6, would leave R20 and
   R21 unclaimed for two phases.

## Where the parent plan's U4b description missed the landed tree

- The parent's `Verify` line asked for a re-plan because "later phases have
  since changed the tree", and the change that matters is the timer facility.
  `Switch.Wake`, `NextWake`, `Drain`, and `Fabric.scheduleWake` already carry
  exactly the release mechanism a held frame needs
  (`src/common/netsim/vswitch/switch.go:2554`, `:2571`, `:2489`,
  `src/common/netsim/fabric/run.go:927`), so the previous draft's stop
  condition, that the fabric would have to grow a wake it does not otherwise
  need, does not apply. What the draft did not see is that every emission is
  labelled a protocol frame, which is the real seam this phase has to open.
- The previous draft left open whether a withdrawn route and a neighbor
  failure should stay separate. The landed tree already decided it, in
  `src/common/netsim/vswitch/routing/README.md:259-263` and in the direction
  record at `:533-534`, so this plan inherits the answer instead of asking it.
- The parent's `Change` line says "injected logical-time transitions", which
  reads as an API a scenario calls directly. `Layer.Observe` is that API, but
  the switch also reaches it from a decoded frame, and the record-level entry
  point is what makes one state machine serve two wire formats.
- The parent treats neighbor resolution as a switch concern. `routing.Layer`
  is also every host's IP stack in a fabric
  (`src/common/netsim/fabric/fabric.go:473-482`), and nothing wakes a host
  stack (`src/common/netsim/fabric/run.go:922-927`), so the phase has to say
  what a host does before it changes `Originate` at all.
- The parent did not anticipate that a corpus case now costs a change to
  `TestRegistryDeterministicOrdering`'s literal count and per-class ID lists
  (`src/common/netsim/internal/netsimtest/corpus_test.go:575-649`). U6 names
  it.
- `Layer.Owns` rejects ARP outright and routes a Neighbor Advertisement as an
  ordinary IPv6 packet (`src/common/netsim/vswitch/routing/layer.go:599`). Any
  reading of the parent that assumed the routing layer would simply start
  seeing these frames is wrong; the switch needs two new pre-bridge guards,
  which U5 adds.
