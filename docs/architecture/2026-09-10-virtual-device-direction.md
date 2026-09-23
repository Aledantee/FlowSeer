---
title: Virtual Device - Direction
type: direction
date: 2026-09-10
topic: virtual-device
status: proposed-direction
amends: docs/architecture/2026-09-09-mutation-shadow-projection-direction.md
---

# Virtual Device - Direction

## Context

FlowSeer holds a typed model of what a device is configured to do
(`net/interface`, `net/switching`) and, through the mutation journal, what it
is expected to do after an intent applies. Two questions keep coming back:
what does a frame do on this device as it stands, and what would it do after
the change. A third follows once links between devices are known: what does
it do across the network. The shadow projection record deferred the second
to a projection over `Config` checked by named invariants and ruled out Open
vSwitch and Batfish. It did not say who computes the projection or how a
forwarding question is answered.

## Decision

`src/common/netsim` is the home of network simulation. The library divides
ownership across specialized packages:
- `trace` is an import leaf defining typed execution steps, configuration diff
  changes, producer-owned rule IDs, and semantic facts.
- `analysis` defines the trust contract: analysis readiness status, scoped
  issues, evidence catalogs, and assumptions.
- `vswitch` composes capabilities over a port table, validates and normalizes
  configurations, and returns forwarding results combining domain outcomes with
  analysis trust metadata.
- Capability packages under `vswitch/` (`port`, `phy`, `bridge`, `lag`, `stp`,
  `mcast`, `routing`, `traffic`) own their respective configurations, validation,
  normalization, cloning, diffs, and rule identifiers.
- `vswitch/netmodel` translates FlowSeer network model messages into virtual
  switch construction specifications, loading reports, and readiness metadata.
- `fabric` composes switches, hosts, and cables into a stepped network simulation
  recording traversal journeys.
- `search` enumerates finite scenario domains against current-versus-candidate
  fabrics, minimizes causal counterexamples, and aligns traces.
- `internal/netsimtest` maintains an admitted, versioned analysis conformance
  corpus.

- The device is sized by its caller. A port table with caller-chosen names
  is the one place a port exists; every layer keys its attributes by port
  name and validates against the table. Port count, naming, speeds, and
  PoE budgets are inputs, because the lab alone holds three vendors that
  agree on none of them.
- A device is built from capabilities, and a capability is the presence of
  a layer's configuration with no boolean beside it: a relay, VLAN
  awareness, Ethernet speeds, PoE, link aggregation, spanning tree, and
  routing. The
  switch derives its capability set from its configuration and rejects
  configuration of a capability it lacks. The ladder is hub, bridge,
  switch: a port table alone repeats every frame to every other port; the
  relay adds one filtering database with every tag payload; VLAN
  awareness adds classification and tag forms. This is the
  facet rule of the network model applied to the simulator, and it keeps
  one code path per capability rather than one per device model.
- One package per capability over the port table: `phy` for speeds and
  PoE, `bridge` for the relay and VLANs, `routing` for routed interfaces
  and per-VRF tables. A layer imports the port table, the shared trace package,
  and shared network value or codec packages under `src/common/net`. Sibling
  capability layers do not import one another; `vswitch` composes them, and
  `netmodel` owns the protobuf boundary. A new layer is a new package and a new
  field.
- A simulator holds one state. Comparison, diff, and deriving an expected
  state from a current one are functions over two values, on a switch and
  on a fabric alike, so a pair of fabrics composes pairs of switches.
- A simulation is a run: injected frames, each with a time and an origin,
  sit in a queue of pending arrivals ordered by time and then by a fixed
  tie-break; a step takes one arrival, forwards it on its device, and
  enqueues one copy per egress cable; a copy is a transmission on the
  egress port that starts when the port is free and its queue's turn comes,
  in strict priority by PCP, takes the frame's serialization at the negotiated
  rate, and arrives after the cable's propagation. The run halts after a
  caller's step budget, and a snapshot exposes the clock, the frames in flight,
  and every device's forwarding database, port states, and counters as values.
  A snapshot exposes none of it as something a caller can step forward: it
  names what the run holds at that instant and nothing else. A fork is what a
  caller steps instead, a running copy of the whole fabric that continues the
  same run from that instant rather than starting a new one the way `Derive`
  does; "State ownership, forking, and snapshots" below states the copy rule.
  Every frame's processing is a journey — hops, cable crossings, deliveries,
  and drops with reasons — and the caller chooses what survives it: a
  `RetainJourney` injection keeps the settled journey, while a
  `RetainAggregate` injection frees it and folds what became of the frame into
  the flow's statistics, so a run can offer millions of frames without holding
  millions of journeys. Those statistics report what the flow offered,
  delivered per host, dropped by reason, lost, unresolved, rejected, and held
  for resolution, and how late each delivery arrived, with the trust metadata
  of everything the answer rests on. An egress queue states a buffer in
  encoded frame octets or leaves it unstated: a frame that would make the
  queue's depth exceed a stated buffer is tail-dropped with reason
  `queue-full`, and a queue with none never drops but its endpoint reports an
  `Incomplete` `queue-buffer-unstated` issue once the queue backs up past one
  maximum-size frame, so a loss figure taken without a stated buffer is marked
  as resting on nothing.
  This is the event model of ns-3 without goroutines. The queue also
  holds the wake-ups a layer schedules and the frames a device emits on
  its own, spanning tree first, under the same total order, so the single
  device stays a function of time, port, and frame and the run stays
  deterministic. A loop is a queue that does not drain; the run marks the
  re-entry and the budget halts it.
- A cable is a model of its own: a length and a medium that give a
  propagation time and bound the negotiated speed, an optional delay that
  replaces the propagation term, a top speed, and a declared fault (cut,
  one direction dead, a deterministic loss or corruption rule). Nothing in
  a run is random, so a run reproduces, and the journey names the cable
  where a frame was lost.
- The shapes come from prior art, recorded in
  [the prior art research](2026-09-10-network-simulation-prior-art-research.md):
  a trace is a list of per-layer operations as Packet Tracer shows them,
  ingress decides and egress rewrites as bmv2 splits them, the relay,
  forwarding table, tagger, and ports are separate parts as INET composes
  them, and a comparison carries a current and an expected trace side by
  side as Batfish's differential questions do. The switch is one `c-vlan`
  component of the IEEE 802.1Q YANG bridge model, so components and
  filtering-database ids are where a provider bridge or a multi-domain
  device grows later.
- The model is the standard's. Ingress classification,
  admission, ingress filtering, learning, aging, flooding, and egress tag
  form follow IEEE 802.1Q as the Q-BRIDGE-MIB and BRIDGE-MIB under
  `spec/mib/ietf/` describe them. A vendor deviation is a documented
  difference the trace can name.
- The library is pure. No goroutines, no wall clock, no I/O; the caller
  passes the time and holds the lock. A trace is a value, so two states can
  be compared and a result can be stored or shown.
- The core holds plain Go types; protobuf messages appear only in
  `netmodel`. The generated messages are shaped for what a device reports,
  with presence meaning unknown, while a rule needs every value decided
  and every default reported. They are pointer graphs that cannot key a
  map, and a forwarding evaluation is a few map lookups by port name, VLAN
  id, and a six-byte address. The model also lacks what only the simulator
  needs: links, hosts, a capability set, a trace. A `flowseer/netsim/v1`
  schema is written when a service carries a scenario or a trace over a
  wire, and it maps onto the same plain types through the boundary
  package. The value types the simulator computes over are common Go
  packages for every tree: `netaddr` for EUI addresses, `vlan` for VLAN
  ids and tags, `ethernet` for the frame and its codec. The collectors and
  the capture pipeline move to them in their own changes.
- It is the engine of the shadow projection. The projection record's
  "no emulator runs" means no third-party emulator; the virtual device is
  the projection, and a frame query joins the invariants as part of the
  preview once the device service wires it.
- It lives under `src/common` because both the device service, for the
  preview, and the edge, for replaying captured frames against the device
  they came from, import it, and neither wires it as a service module. The
  simulators keep `src/common`'s rule of importing nothing from
  `generated/`; `netmodel` is the documented exception, because a
  simulator nobody can load from the network model is a test fixture.

### Result contract axes

Every analysis evaluation separates four orthogonal axes:

1. **Input validity**: Whether caller inputs satisfy structural and invariant
   checks. Invalid inputs (such as non-existent port references or malformed
   VLAN IDs) are rejected with a Go `error` before simulation begins.
2. **Domain outcome**: What the simulated dataplane did with the frame:
   `Forwarded`, `Flooded`, `Dropped`, or `Consumed`, along with a domain reason
   (such as `ingress-filter`, `port-down`, or `unicast-hit`).
3. **Analysis readiness**: The trust placed in the answer: `Complete`,
   `Incomplete`, `Exhausted`, `Unstable`, or `Unsupported`, derived from an
   untruncated collection of scoped issues.
4. **Semantic trace**: The sequence of capability-owned operations (`Step`) and
   configuration transitions (`Change`) recording producer rule IDs, affected
   subjects, typed facts, and opaque evidence references.

None of these axes may be inferred from another:
- An input can be valid while yielding an `Incomplete` analysis if live state
  was unobserved.
- A frame drop in the domain (for instance, an ingress filter drop or a known-down
  port) is an authoritative, fully trusted answer (`Complete`), not an analysis
  failure.
- A frame forwarded in the domain can rely on assumed fallback defaults,
  rendering the answer `Incomplete`.
- An execution stop reason or human prose string cannot substitute for typed
  rule IDs or semantic facts in traces.

### Construction specifications and singular normalization

Every capability package exposes one normalization function (`Normalize`).
Public constructors validate only after normalization.

To reproduce simulations without drift, `vswitch.ConstructionSpec` and
`fabric.ConstructionSpec` pair normalized configuration with non-configuration
seeds (such as preloaded forwarding database entries). Re-running a simulation
from its construction specification yields identical state and trace records.

### State ownership, forking, and snapshots

A snapshot, a fork, and `Derive` answer three different questions about a
switch or a fabric's execution history. A snapshot is a read-only report: it
exposes what a device or a run holds — the clock, the frames in flight, each
device's forwarding database, port and neighbor state, and held-frame depth
— as values a caller can compare but never step forward. A fork is an
executable copy: a second `Switch` or `Fabric` that starts identical to its
source and diverges under its own steps from there. It is the input a
current-against-candidate comparison will take once one exists;
`fabric.Compare`'s present precondition that neither fabric has injected a
frame is the next phase's to lift, not this one's. `Derive` is neither. It
rebuilds a switch or a fabric from a target construction specification,
carries forward only the runtime state each capability layer's retention key
says still applies, and starts a fresh run rather than continuing one.

`Fork`'s copy rule follows from what each field is, not from a rule written
once and trusted. Construction fixes some fields and execution mutates the
rest, so every field of `Switch`, `Bridge`, and `Fabric` carries one of three
classes. An `immutableShared` field is unwritten after construction; the
fork points at the same value the source does. A `deepCopied` field is
mutated by execution; the fork gets its own copy the source's later writes
cannot reach. A `resetOnFork` field is a binding back to the switch or
bridge that holds it — the bridge's spanning-tree gate, its LAG selector,
its multicast resolver — and is rebound to the fork's own layers rather than
copied, because a copied binding would have the fork asking the source for a
LAG member or a multicast resolution. Each class has its own assertion:
pointer identity for the first, a mutation probe for the second, a
zero-value check for the third before the switch rebinds it. A table
recording a class per field and never checking it back is a defect this
phase has already seen once; see [one slot, two
roles](../solutions/architecture-patterns/one-slot-two-roles-is-a-defect-class-not-a-defect.md).

An `ethernet.Frame` is an immutable value: nothing in the library writes
into one after it is built. Every copy path shares the frame instead of
copying it — `Fork`'s queues, egress queues, hold queues, and journeys, and
`Snapshot`'s reported frames — because copying every queued and held payload
would be the dominant cost at the declared scale and nothing needs the copy.
`Report()` is the one exception: it deep-copies frames because it hands a
journey to a caller outside the library, a contract already paid for before
this phase.

A retained record's provenance and its lifetime are two separate axes.
`Origin` is `Configured` or `Observed`, naming who installed the record.
`Lifetime` is `Static` or `Aging`, naming whether it expires on its own. The
two vary independently: a configured record can still age out, and an
observed one can be pinned past the interval that would otherwise expire it.
A single `Static` boolean cannot say both at once, which is why
`restoreMulticastState` used to synthesize a fabricated IGMP query just to
get an aging router port back — there was no way to install an observed
record with its own expiry directly. A record with only one reachable
origin, such as `mcast.Entry`, keeps no `Origin` field; its package README
says why, because splitting an axis nothing ever disagrees about would add a
field with one value forever.

Retention is keyed by an explicit per-layer dependency key, not by comparing
a layer's own configuration. A layer's runtime state can depend on more than
its own `Config`: spanning tree's roles depend on link state and speed that
live on the port table, and LAG's member selection depends on the switch's
base MAC, which reaches the layer only as a normalization input. Each
capability layer exports `RetentionKey(...) string`, a canonical encoding of
every normalized input its runtime state actually depends on, and `Derive`
retains a layer only when the current switch's key and the target's agree.
Both keys are computed from constructed switches, never from a raw target
specification, because a raw spec has not yet run through the normalization
and MAC assignment that decide what a field becomes; see [validate and
derive judge what New
builds](../solutions/architecture-patterns/validate-and-derive-judge-what-new-builds.md).

### Uncertainty localization and model loading

`netmodel.Load` translates network model records into virtual switch construction
specifications:
- Errors are reserved for unconstructible inputs, such as empty interface lists
  or duplicate port names.
- Partial, unobserved, or conflicting data produces a constructible `Result`
  with non-Complete readiness metadata and detailed loading reports.
- Port operational status distinguishes `Up`, `Down`, and `Unknown`. Zero values
  normalize to `Unknown`. A port with `Unknown` operational status never forwards
  traffic (`port.Forwards() == false`) and reduces readiness to `Incomplete`
  strictly for its own scope.
- A port known to be `Down` is an authoritative domain drop with `Complete`
  readiness.
- Sibling scopes remain independent. An unobserved port on a switch does not
  degrade the `Complete` readiness of an unrelated known-up port on the same
  switch.
- Standard defaults (such as aging time or unconfigured VLAN priority tags) are
  recorded as explicit `Assumption` values linked to evidence in the catalog.

### Fabric link and port state, host acceptance, and journey metadata

The readiness rules above extend from one device to the fabric connecting
them.

- **Effective link and port state.** Every non-LAG switch port is cabled,
  listed in `Config.Uncabled`, or unresolved. A cabled port's link decides
  its state; `Uncabled` is `Down` with `no-cable`, a stated absence; neither
  is `Unknown` with `adjacency-unresolved`, since nothing says what, if
  anything, is attached. A configured `OperStatus` that disagrees with the
  derived state is kept for the reader (`Config()`, `Spec()`), but the
  derived state executes, and the disagreement adds an `oper-status-conflict`
  issue instead of the derived value silently overwriting it.
- **`Config.PhyAssumption`.** An opt-in medium and Ethernet profile that
  fills only what an end or cable leaves unreported: empty speeds, an
  unknown capability, a nil setting, or an unspecified medium. Filled values
  appear in `Fabric.Links()`; `Config()`, `Spec()`, and `Diff` show only the
  knob itself, and each link it fills carries one `Assumption` naming the
  facts, so a standards default runs only on the record.
- **Protocol link-state input.** `Switch.LinkChange` takes `port.LinkState`,
  not a boolean, so an `Unknown` link leaves the port `Unknown` instead of
  being coerced to `Down`; STP and LAG still hear it as not operational. The
  switch raises `protocol-link-unknown` on every port whose role or
  membership those protocols compute once one of their inputs is unknown; a
  later `Up` report clears it. Without this, an unknown redundant uplink
  would silently re-elect a spanning tree root with every dependent journey
  reading `Complete`.
- **Host acceptance.** A frame the pipeline delivers to a host is not
  necessarily one the host keeps. After arrival, the host checks VLAN
  form, destination MAC, and, for a host with an IP stack, the IP
  destination, each under a named clause (own address, broadcast,
  solicited-node group, promiscuous mode). `Journey.Deliveries` holds only
  frames a host accepted; a rejection or an undecodable header is a
  recorded outcome of its own.
- **Journey dependency metadata.** `Journey.Metadata` is captured per entry
  at record time, not read live from `Fabric.Metadata()`. Each entry folds
  in the issues and assumptions of its own dependencies: its endpoint, its
  cable, and, for a hop, the ports and scopes its forwarding result
  consulted. A later `SetFault` changes `Fabric.Metadata()` but leaves an
  already-recorded journey's trust as it was. A flood that skips an
  `Unknown` port still consulted it and still carries that port's issue,
  because the flood could have reached one more host had the port resolved.

### Scenarios, run completion, and convergence

A fabric run ends for one of six typed stop reasons: `StopNotRun`,
`StopQueueDrained`, `StopBudget`, `StopConverged`, `StopOscillating`, or
`StopFault`. The stop reason is an execution outcome, distinct from the
`analysis.Status` the run result carries: a run can stop at `StopConverged` while
its status is `Incomplete` because an unobserved adjacency was consulted, or
stop at `StopBudget` while its status is `Unsupported` because of an engine
fault. The two axes never substitute for each other.

Every recorded journey receives exactly one result state. `JourneyPending` is the
sole nonterminal state, assigned when work on the frame remains queued, in an
egress buffer, or held awaiting neighbor resolution at the run boundary. All other
states are terminal: `JourneyDelivered`, `JourneyRejected`, `JourneyDropped`,
`JourneyLooped`, `JourneyTruncated`, `JourneyUnresolved`, and `JourneyReleased`.
Host rejection is separated from delivery at this layer: a physical arrival that
the destination host refuses (due to MAC mismatch, VLAN filter, or IP rejection)
is terminal `JourneyRejected` and is excluded from deliveries.

A journey's origin is a typed relation rather than an overloaded parent pointer.
Its kind records how the frame entered the simulation: a caller's direct
injection (`OriginInjection`), a mirror copy produced by port mirroring
(`OriginMirror`), or a frame released from a neighbor hold queue
(`OriginRelease`). For mirror copies and releases, the origin links back to the
holding or source frame ID without adding forward-pointing pointers that could
drift out of agreement.

Convergence and oscillation are evaluated on a canonical state fingerprint of
protocol-relevant snapshot state across consecutive periodic wake arrivals. The
fingerprint explicitly excludes the clock, the arrival queue, and counters.
Without that exclusion, periodic protocol events (such as spanning tree BPDUs)
would advance the clock and refresh counters on every step, making a settled
fabric look perpetually changing or disguising budget exhaustion as normal
progress. A scenario declares its observation window and positive step budget as
an in-memory Go value; it carries no file persistence, no packet-capture parser,
and no pseudo-random seed.

### Current-against-candidate comparison

A caller compares a current network or switch against a candidate to detect
regressions and verify equivalence. Comparison evaluates behavioral observables
rather than whole-state fingerprints or shallow boolean equality:

- **Switch observables.** For `vswitch.ForwardResult`, comparison checks
  forwarded frames per egress port with rewritten fields, the selected LAG
  member, mirror copies, PCP, and the typed forwarding outcome (`Forwarded`,
  `Flooded`, `Dropped`, or `Consumed`, with domain reason). Semantic trace
  steps and metadata are diagnostic and never cause a `Different` verdict.
- **Fabric observables.** Per paired journey in `fabric.Journey`, comparison
  checks `State`, `Origin`, ordered `Entries` (path hops, cable crossings,
  deliveries, and drops with reason and location), `Deliveries`, `Protocol`, and
  carried frame content. Across the run, it compares `Stop` reason, `Status`,
  `Pending` work count, and `Issues`, followed by the final `Snapshot`
  behavioral state. Metadata, evidence, semantic trace text, and raw
  `Fingerprints`/`Cycle` diagnostics are diagnostic only.
- **Three honest dispositions.** Comparison returns `analysis.Disposition`,
  defined as `Equivalent`, `Different`, or `Inconclusive`. Dispositions are
  non-empty strings with no zero value. The disposition is derived rather than
  stored:
  - `Equivalent` requires both sides' `Status` to be `Complete` and every
    behavioral observable to match.
  - `Different` requires a proven behavioral observable mismatch, naming the
    first differing observable and carrying an immutable `ReplaySpec`.
  - `Inconclusive` occurs when either side reports `Incomplete`, `Exhausted`,
    `Unstable`, or `Unsupported`, or when budget stopped a run that still held
    pending work. Equivalence over partial evaluation is never assumed.
- **Internal forking and lifted precondition.** `fabric.Compare` forks both
  inputs internally via `a.Fork()` and `b.Fork()`, injects the scenario, runs
  the forks to budget, and compares. The caller's `a` and `b` fabrics are never
  stepped, injected, or learned into. This lifts the former restriction that
  fabrics must not have injected frames prior to comparison; mid-run fabrics
  are directly comparable without consuming caller state.
- **Injection-ordinal journey pairing.** Forks of running fabrics assign frame
  identifiers from divergent local histories, so new scenario journeys cannot
  pair by frame ID. Instead, journeys pair by injection ordinal: the nth
  scenario injection's journey on side A pairs with the nth injection's journey
  on side B. Pre-scenario journeys represent shared history and are excluded
  from the diff.
- **Separation from convergence fingerprints.** `Snapshot.Fingerprint()` remains
  the convergence oracle covering device and link convergence across periodic
  protocol boundaries. It does not capture per-journey paths, arrival timing, or
  multiplicity. Comparison is an independent field-level walk over all
  behavioral observables.

### Bounded differential search

A caller searches a declared finite domain of candidate scenarios against a
current-versus-candidate fabric pair to identify behavioral divergence or confirm
bounded equivalence under a resource contract:

- **Finite domain enumeration.** A search domain implements the `Domain`
  interface with `Size() int` and an internal iterator `Enumerate(yield func(Candidate) bool)`.
  Candidates represent test scenarios paired with domain tuple identities.
  Enumeration follows a total deterministic order independent of map iteration.
  When a search halts early on its candidate budget, it returns an exact slice of
  untested tuples alongside tested coverage (`Coverage` and `Remainder`).
- **Domain scopes.** Two concrete domains exist: `L2TrafficDomain` (cross
  product of declared source, destination, VLAN, and frame-shape tuples) and
  `TimedFaultDomain` (declared cable faults at declared times). `L3Domain` is an
  interface only this phase, deferring routed traffic enumeration to a later
  increment without altering the search pipeline.
- **Comparison per candidate.** `search.Search(current, candidate, dom, lim)`
  evaluates each candidate via `fabric.Compare(current, candidate, cand.Scenario, lim.Budget)`.
  `fabric.Compare` forks both fabrics internally. Neither the current nor the
  candidate fabric is stepped or consumed across the search, so callers can reuse
  mid-run fabrics directly.
- **Resource contract and asymmetric retention.** Discarded candidates (equivalent
  or inconclusive) record coverage accounting and a summary count only, without
  full execution traces. Only retained `Different` candidates (bounded by
  `lim.MaxDifferences`) preserve their tuple identity, immutable `[2]ReplaySpec`,
  and first differing `Difference` observable.
- **Deterministic counterexample minimization.** When a behavioral difference is
  found, `Minimize` performs deterministic reduction within the domain by
  removing scenario elements (injections, then faults) in a fixed order. A
  removal is accepted only if `fabric.Compare` replayed on the reduced candidate
  still reproduces the exact same `Difference.Observable`. Minimization reports
  `Minimal` or `LimitReached`.
- **First-divergence trace alignment.** For a retained comparison, `Align`
  walks paired journeys in deterministic order and locates the first entry index
  where causal trace facts diverge. Trace facts remain diagnostic and never
  alter the behavioral disposition.
- **Package dependency direction.** The `search` package imports `analysis`,
  `vswitch`, and `fabric`. None of `analysis`, `vswitch`, or `fabric` imports
  `search`.

### LAG bucket selection and multicast query observation

- **LAG buckets are runtime state, not a stateless hash.** A `BalanceSLB` or
  `BalanceTCP` selection buckets `hash & 0xff` into 256 slots, and each slot
  remembers which member last carried it. A bucket keeps its member while
  that member stays enabled, so a member fault reassigns only the buckets
  that were on the faulted member; the other buckets, and the flows hashing
  to them, are untouched (`ofproto/bond.c` `choose_output_member` and
  `get_enabled_member`, OVS `branch-3.3` at `73e38c8d`). A newly enabled
  member joins the back of the enabled list rather than the front, so
  reassignment after a fault cycles through survivors in a fixed order
  instead of piling every orphaned bucket onto one member. `Select` commits
  a bucket's assignment and the enabled list's rotation; `Peek` computes the
  same choice without committing either, so a preview cannot perturb the
  live bond. Active-backup keeps the member it last chose rather than
  failing back once the member it moved away from recovers; only a
  configured `Primary` returns traffic to a specific member. A
  `RebalanceInterval` elapsing on an unchanged, balanced bucket with two or
  more enabled members raises `lag-rebalance-unmodeled`: OVS would have
  measured load and could have moved the bucket, and netsim does not
  measure load.
- **The switch never queries, so an unobserved query it depended on degrades
  readiness instead of silently keeping full timers.** RFC 3376 §6.4's leave
  and report tables call for a "Send Q" action in some rows, but the switch
  does not emit that query itself, so nothing lowers a member's timer to the
  last member query time on the switch's own initiative. When a table row
  calls for a query, the VLAN has a router port, and no matching
  group-specific or group-and-source-specific query with the suppress flag
  clear arrives within the last member query time, `mcast-query-unobserved`
  marks the group's forwarding result Incomplete on
  `ProtocolScope(node, "mcast", "<vid>/<group>")` until state changes: a real
  querier's query would have pruned the group sooner, and the switch cannot
  tell whether one was sent and lost or never existed. With no router port
  there is no querier to have sent one, so the full membership interval is
  the real behavior and the result stays Complete.

### Spanning-tree information lifetime, guards, and the gate

- **Received information is bounded twice, and the hop count uses the BPDU's
  own max age.** A BPDU is accepted only while `MessageAge + 1 <= MaxAge`
  against the `MaxAge` the BPDU carries, not this bridge's configured one,
  because the received value is the root's and a fabric holds bridges with
  differing timers. A BPDU that fails the test is discarded rather than
  stored, so a BPDU naming a root that no longer exists stops refreshing the
  port's timer on every hop; the port's own information then expires on the
  `3 × HelloTime` silence bound and the roles are recomputed. UNH-IOL's RSTP
  conformance suite states the rule as a hop count with max age as its
  maximum, citing IEEE Std 802.1Q-2011 sub-clauses 13.23.6, 13.27.30, and
  13.28.
- **Internal information ages by remaining hops instead of message age.** A
  BPDU whose configuration identifier matches this bridge's own is internal;
  it is accepted while `RemainingHops > 1` and re-originated one hop short, on
  the CIST and every MSTI alike, so a claim naming a regional root that no
  longer exists stops refreshing after `MaxHops` hops rather than a fixed
  time. External information (an RST or Configuration BPDU, or an MST BPDU
  from a different region) keeps the message-age test above, and both kinds
  still expire in silence after `3 × HelloTime`. `MaxHops` defaults to 20,
  bounded 6 through 40.
- **A guard's outcome is a port state, not an issue code.** A port state an
  operator can see beats an issue code they have to look for, so BPDU guard
  disables the port with reason `bpdu-guard` until a link bounce, restricted
  role keeps a port out of root selection, restricted TCN stops a received
  change from propagating, and loop guard holds a port whose information
  expired in silence while it was Root, Alternate, or Backup in a discarding
  Alternate role with reason `loop-inconsistent` until any BPDU arrives, one
  the message-age bound discards included, so a peer sending only stale
  information is not covered. Both BPDU guard and loop guard hold a guarded
  port out of every tree, not only the CIST: the outcome is bridge-global, so
  an MSTI's own port is Disabled or Alternate right alongside the CIST's.
  Loop guard is netsim's own design drawn from Cisco, Juniper, and Arista, and
  is inactive on an operationally edge port and on a shared link, where a port
  that stops hearing BPDUs is not evidence of a link broken in one direction.
  `LoopGuard` beside `RestrictedRole` or beside `AdminEdge` is refused at
  construction: a configuration whose halves contradict each other has no
  correct simulated answer.
- **The layer keys its state by tree and the gate answers per VLAN.** A port's
  forwarding state belongs to a spanning tree, and more than one tree can run
  over one port, so `Learns` and `Forwards` take a port and a VLAN and the
  layer maps the VLAN to a tree. With no region configured, one tree, the
  CIST, carries every VLAN, so the answers agree; with a region configured, a
  VLAN an instance claims maps to that MSTI instead, and a boundary port's
  answer for it still traces back to the CIST because an MSTI takes the CIST
  port's role there. The port identifier and the port key set stay
  bridge-global: the identifier appears on the wire, and the key set's order
  reaches the caller as the order of `Effects.Flush`.

  A third mode runs rapid spanning tree once per VLAN, and VLAN 1's tree
  holds the CIST slot. In PVST+ that tree *is* the common tree a neighboring
  RSTP or MSTP bridge converges with, and every bridge-level accessor,
  `Root`, `PortInfo`, `TopologyChanges`, `Times`, answers from the CIST, so
  putting it there keeps all three modes answering about the common tree
  without a per-mode branch in any accessor. Each tree carries its VLAN in
  the low 12 bits of its bridge identifier's system-ID extension, the way an
  MSTI carries its MSTID, and each meters its own transmit budget, since a
  PVST bridge puts one frame per VLAN on the wire where an MST bridge puts
  one frame carrying every instance's record. The two boundary rules below
  are MSTP's alone: on a PVST bridge every port reads external, because the
  classification that sets it needs a region, so a VLAN's tree left to follow
  them would mirror VLAN 1 instead of electing its own root.
- **Classification precedes the ingress gate.** Active topology enforcement
  belongs to the forwarding process that follows ingress classification, so
  the bridge classifies first and asks the gate with the classified VLAN. A
  gate-blocked frame therefore names the VLAN it was classified into, and a
  frame that fails classification reports the classification reason rather
  than `port-blocked`, because a frame the port would never have admitted is
  not a spanning-tree question. A frame dropped in classification consults no
  spanning-tree scope, since no tree state could have changed its outcome.
- **`Decode` reads a version 3 BPDU's MST body, and falls back to its RST
  prefix only when the payload is too short for one.** The configuration
  identifier, internal root path cost, remaining hops, and MSTI records come
  back filled whenever the payload holds enough octets; an RSTP peer's
  39-octet version 3 BPDU, a capture truncated before the MST body starts, and
  every version above 3 all still decode as the RST prefix rather than being
  refused. A payload long enough for the MST body but truncated inside the
  MSTI records is refused outright, not fallen back. UNH-IOL's MSTP suite
  states that a compliant device must not validate a BPDU on its protocol
  version identifier (Test MSTP.op.1.3, citing IEEE Std 802.1Q-2011
  sub-clause 14.4), which is why the fallback exists at all: refusing a short
  version 3 payload would leave a netsim bridge facing that peer with both
  ends Designated and Forwarding, an unbroken loop and a worse answer than the
  RST-prefix approximation.
- **A region is a name, a revision, and a digest over the VID-to-MSTID
  table, carried as a 51-octet configuration identifier.** The digest is
  HMAC-MD5 over the 4096-entry table, two big-endian octets per VID, keyed
  with a fixed constant; the all-zero table digests to
  `ac36177f50283cd4b83821d8ab26de62` and VID 10 on MSTID 1 with VID 20 on
  MSTID 2 to `9357ebb7a8d74dd5fef4f2bab50531aa`, which is what proves the
  construction matches the standard rather than some other one. A port that
  has received nothing is treated as internal, since the field marking a
  boundary port is set only on `Receive`; once a BPDU has arrived, a port is
  internal when it carried this bridge's own configuration identifier, and a
  boundary port otherwise, with an RST or Configuration BPDU always external.
- **One priority vector type serves the CIST and every MSTI.** Its six
  components compare in order: root, external root path cost, regional root,
  internal root path cost, designated bridge, designated port. An RSTP tree,
  and the CIST on a boundary port, set the regional root from the root and
  leave the internal cost zero, collapsing to the four-component
  root/cost/bridge/port order with the cost in the external slot; an MSTI
  does the same the other way round, taking its root from its regional root
  and leaving the external cost zero. The CIST on an internal port populates
  all six components, comparing regional root and internal cost ahead of
  bridge and port so a region settles its own internal topology before
  comparing outward. None of this needs a branch, because a constant or
  shared leading component drops out of a lexicographic comparison, so a
  second vector type would only duplicate the comparator.
- **Only the CIST computes a boundary port's role; every MSTI takes the
  CIST's role there outright.** Electing an independent role from information
  a different region sent would let an MSTI disagree with the CIST about
  which link is blocked, which is the boundary a region name and revision
  exist to detect. A bridge whose CIST root port is itself a boundary port
  names its own bridge identifier the CIST's regional root and originates the
  region's full hop count, rather than naming the peer's root: crossing the
  boundary is what starts the region. netsim also reports no Master role:
  both the CIST and MSTI role MIB tables (`ieee8021MstpCistPortRole`,
  `ieee8021MstpPortRole`) list exactly Root, Alternate, Designated, and
  Backup, so a boundary port's MSTI mirrors the CIST's Root under the label
  the MIB can express, the same forwarding answer under a different name.
- **A topology change flushes by tree, and the CIST flushes everything.**
  `Effects.Flush` pairs a port with the FIDs stale on it, so a change on an
  instance discards what the bridge learned about that instance's VLANs and
  leaves the other instances' entries alone. A per-instance change also
  propagates across the fabric: each MSTI record on an MST BPDU carries its
  own instance's topology-change bit, and a receiving bridge flushes that
  instance's VLANs on its other ports the same way a locally raised change
  would. An empty FID set means every FID, which a link down, a BPDU-guard
  disable, a received topology change notification, a received BPDU with the
  CIST topology-change flag set, and a topology change this bridge's own CIST
  raises all produce. The CIST case is a deliberate over-flush: the CIST
  carries every VLAN no instance claims, a set the layer cannot enumerate, so
  it names no FIDs. On a boundary port that is correct, since a CIST change
  there reaches every tree; on an internal port it costs a round of flooding
  to relearn entries that were not stale. The alternative is a target that
  can express "every FID except these", which is a wider contract than one
  over-flush justifies. That argument does not carry to PVST, where VLAN 1's
  tree occupies the CIST slot but carries VLAN 1 alone: the set is
  enumerable, so the flush names it, and a VLAN 1 change leaves every other
  VLAN's learned entries in place.
- **A per-VLAN BPDU rides its own VLAN through the port's ordinary egress
  rules, and the switch is what tags it.** An SSTP BPDU is an RST BPDU in
  LLC/SNAP addressed to `01:00:0c:cc:cc:cd` with the originating VLAN in a
  trailing TLV. The spanning tree layer names the VLAN and leaves the frame
  untagged; the switch passes it through the same `OriginateFrame` every
  other frame it originates goes through, which already implements tagged
  where the VLAN is tagged and untagged where it is the port's untagged VLAN.
  That is Cisco's native-versus-tagged rule with no second implementation,
  and it is why a port that does not carry a VLAN simply sends nothing for
  it. VLAN 1's tree additionally emits one untagged IEEE-addressed frame per
  port, whatever the native VLAN is, which is the frame an RSTP or MSTP
  neighbor converges with.
- **A BPDU is admitted past the spanning tree gate the tree itself set, and
  separately judged against the bridge's ingress admission rule.** Reception
  resolves the arrival VLAN from the frame's own tag, or the port's untagged
  VLAN. The spanning tree gate — a port a tree holds discarding — is still
  bypassed: those are exactly the ports whose blocking depends on continuing
  to hear their peer, so running a BPDU through that gate would drop the
  frames that keep the topology converged. The bridge's ingress admission
  rule is not bypassed: `bridge.VLAN.AdmitsVIDOnIngress` answers whether the
  arrival VLAN is admitted on the port, the same question an ordinary data
  frame on that VLAN has to answer, and a BPDU the port does not admit
  reaches no tree.
- **A PVID inconsistency blocks the VLAN the frame arrived on, not the one it
  names.** When an SSTP BPDU's TLV names a different VLAN than the one the
  switch classified it into, the two ends disagree about what the link
  carries. The BPDU is not applied, and the arrival VLAN is held discarding
  on that port and excluded from contributing a root vector until a
  consistent BPDU arrives, the same two effects loop guard has. Cisco blocks
  the traffic of the corresponding VLAN, and the arrival VLAN is the one
  whose local traffic would cross a link the two ends disagree about.
- **A boundary between per-VLAN trees and one-tree-for-many-VLANs is
  reported, not modeled.** A PVST bridge meeting an MST BPDU, and a non-PVST
  bridge meeting an SSTP BPDU, marks the port and keeps its existing
  behavior: the first applies the MST BPDU's RST prefix to VLAN 1's tree, the
  second counts the SSTP BPDU and withholds only its priority vector, because
  its CIST does not run that VLAN's tree and feeding the vector in would
  elect a root from a tree it is not running. The link-level half of a
  receive — BPDU guard, the loop-guard clear, protocol migration, and
  auto-edge loss — runs on both sides of the boundary the same as for any
  other BPDU the port hears; the boundary withholds the vector alone. The
  neighbor relationship still converges over the IEEE-addressed frame both
  sides exchange, so the report covers every VLAN but VLAN 1. It is raised
  per port and VLAN through a hit set, scoped
  `protocol["stp","<port>/<vid>"]` rather than a VLAN scope nested inside a
  port scope: scope containment is a key-prefix test and the bridge consults
  the port's own spanning tree scope on every gated frame, so a nested scope
  would hand a VLAN 1 journey another VLAN's issue.

### Loop protection outside spanning tree

- **The mechanism is netsim's own, not a vendor's.** It is drawn from H3C
  loop detection and the Aruba and Huawei features of the same shape — a
  multicast probe, a returned frame read as a loop, a per-port action — but it
  emulates none of their frames: each vendor's probe format is proprietary, so
  a netsim probe carries only what the layer needs to recognize its own
  return, sent to a locally scoped multicast address no bridge treats
  specially. The recovery modes come from a wider read: Cisco's errdisable
  recovery, Juniper's revert interval, and the timers MikroTik, Extreme, and
  TP-Link expose.
- **An action is a port state, not an issue code.** The same reasoning that
  puts BPDU guard's and loop guard's outcome on the port applies here: `Block`
  and `NoLearn` land on the port a real device would report, where an
  operator already looks, rather than in a separate issue an operator has to
  go find.
- **A probe leaves wherever an ordinary frame would, not wherever loop
  protection judges the topology clear.** It goes out a port that is
  operationally forwarding and that a spanning tree on the same switch also
  forwards, so loop protection never raises anything the tree has already
  broken; it exists for the loop a tree does not cover, whether because none
  runs or because the loop sits outside every tree's view. Loop protection's
  own verdict on the port is deliberately not one of the gates: a port a
  `Block` action already covers keeps probing, which is what lets a
  `LoopCleared` recovery watch the loop persist.
- **A returning probe is an ordinary frame to the bridge that receives it.**
  It is classified through the same ingress pipeline, gates included, as any
  other frame, so a probe returning into a port a `Block` action already
  denies dies at that gate before the loop-protection layer ever sees it.
  That ingress check, not anything loop protection does itself, is what stops
  a two-port loop from acting on both ports: whichever probe is processed
  first blocks its own port, and the second probe is still recognized as this
  switch's own on the blocked port and classified like any other frame, but
  the gate then denies the port both learning and forwarding, so the bridge
  drops it at ingress before detection runs.
- **`NoLearn` contains the symptom without removing the cause.** It stops the
  MAC flapping between two ports that a loop causes, but keeps forwarding, so
  the loop itself is not broken; only `Block` and `Disable` do that. `NoLearn`
  suits a port an operator wants to keep passing traffic on while diagnosing
  what is looping.
- **Not emulated: vendor probe formats, per-VLAN recovery, and carrier loss.**
  No vendor's on-the-wire probe is reproduced, an applied action covers the
  whole port rather than recovering independently per VLAN, and a real
  errdisabled port drops carrier where netsim's blocked port stays
  operationally up and simply stops learning and forwarding.

### Route selection and recursive next hops

- **A lookup yields a candidate set, not a single winner.** Routes are ordered
  by prefix length, then by preference (the Cisco administrative distance,
  lower wins, with 0 reserved for connected routes so a static route floors at
  1), then by metric within one preference. Everything that matches the
  destination and ties on all three is kept, ordered by next hop and then
  egress interface, and recorded in the lookup fact beside the member that
  carried the packet. A lookup that kept only the winner could not answer
  "what else would have carried this", which is the planning question a
  failure analysis starts from. The set is capped at 64, FRR's limit; the
  rest are withdrawn as `max-paths`.
- **One member of the set carries the flow, chosen by a layer-3 hash.** An
  FNV-1a hash over the source address, the destination address, and for IPv6
  the flow label — no protocol octet, no ports — is reduced by RFC 2992
  hash-threshold. That is what an unconfigured router does, and netsim has no
  field saying someone configured it otherwise. Hash-threshold rather than
  modulo-N because a withdrawn or added path must move as few flows as
  possible: "which flows move when this path fails" is answered wrongly by an
  algorithm that moves nearly all of them. The hash is a pure function of the
  packet, so `Peek` and `Forward` agree and neither commits anything.
- **Recursion resolves when the table is built, and a route that cannot
  resolve is withdrawn rather than left to fail per packet.** A static route
  whose next hop is not on-link is resolved against its own VRF's table, to a
  depth of 8, and installs carrying the on-link pair resolution reached while
  `Route.NextHop` keeps the configured value for the fact and the diff. A
  route that self-recurses, exceeds the depth, resolves to nothing, or
  resolves only through a default route is not installed, and its packets fall
  through to whatever else matches with a Complete result: the configuration
  stays valid on real gear, so this is neither a construction error nor
  `Incomplete`, which is reserved for an input netsim does not have.
  `Layer.WithdrawnRoutes` reports each withdrawal with its reason and the
  chain of prefixes walked, because "why is my static route not being used" is
  the question this decision creates. A withdrawn route and a `neighbor-miss`
  stay separate failures.

### Neighbor resolution and held frames

- **`neighbor-miss` and `neighbor-pending` are two different answers to "who
  is at this next hop", not degrees of the same failure.** `neighbor-miss` is
  a routing VRF that will never resolve the next hop to a MAC address, either
  because it does not observe neighbors at all or because it already tried
  and gave up; `neighbor-pending` is a next hop the routing table reached
  whose neighbor entry is newly or still unresolved, and it is not a drop —
  the frame is held. A withdrawn route is a third, earlier failure: no chain
  of routes reaches the next hop at all, decided once when the table is
  built, before any neighbor lookup runs.
- **A neighbor entry has five states, and two states of RFC 4861's machine
  are missing on purpose.** `Unobserved`, `Incomplete`, `Reachable`, `Stale`,
  and `Failed` are the states a lookup can report; `Delay` and `Probe` exist
  in the RFC only to schedule a unicast solicitation toward Neighbor
  Unreachability Detection, and netsim never solicits, so no input can ever
  drive them. A state nothing in the simulator can reach is worse than no
  state, so it is left out rather than sitting dead in the type. The same
  five states serve ARP and NDP alike: section 7.3.2's set already covers
  everything an ARP binding has to say, so a vocabulary invented separately
  for IPv4 would name the same five things a second time.
- **The wire formats live in two packages, `arp` and `ndp` under
  `src/common/net`, alongside `netaddr`, `vlan`, and `ethernet`.** Each rides
  Ethernet or ICMPv6 directly and encodes or decodes one message shape; they
  hold no neighbor table and no lifecycle of their own; that state lives in
  `routing.Layer`, which applies one RFC 4861 section 7.2.5 merge rule to
  both families. ARP has no solicited or override flags of its own, so the
  switch maps a reply or a request onto the flags the layer takes.
- **The switch observes and never solicits, so an unresolved neighbor a
  lookup depended on degrades readiness instead of silently blocking the
  frame forever.** It reads ARP replies and requests, and Neighbor
  Solicitation and Advertisement messages, off the wire as they pass, and
  applies RFC 4861 section 7.2.5 to what they say, without ever sending a
  solicitation of its own. A host's routing layer does not resolve at all:
  `HostRoutingConfig` writes `NeighborDisabled` into every host VRF, because
  nothing wakes a host stack the way `Fabric` wakes a switch, and a frame a
  host held would hold forever. A miss on a host is always `neighbor-miss`,
  never `neighbor-pending`.
- **A frame held during resolution rides the same wake and emission facility
  the run model already gives every protocol layer.** A committing `Route`
  or `Originate` call that lands on an `Incomplete` neighbor queues the
  frame instead of dropping it, bounded by a configured hold depth; the
  `Forward` that observes the advertisement resolving that neighbor releases
  the frames that observation freed, and `Switch.Wake` settles a resolution
  timeout, the same call that fires spanning tree hellos and LACP timers. A
  resolved entry releases its held frames as ordinary emissions, and a
  timed-out one reports them dropped with `neighbor-miss`. `Emission`
  carries a `Protocol` field, because a released frame is exactly the kind of
  emission that is not one: everything else the switch emits on its own is a
  BPDU, an LACPDU, or a loop-protect probe, and the fabric seam that injects
  emissions needs to tell a held user frame apart from a frame the switch
  originated.

### Semantic traces and producer-owned facts

Prose strings are rejected as trace comparison keys. Capabilities define typed
value types implementing `trace.Fact` (`TypeID()` and `Canonical()`). Diff
records (`trace.Change`) carry typed `From` and `To` facts. Rule identifiers
(`trace.RuleID`) are owned by capability producers without central registry or
capability enums. Tooling compares traces and diffs using typed equality.

### Conformance corpus admission

The test corpus under `src/common/netsim/internal/netsimtest` locks simulation
contracts through executable cases rather than golden-file snapshots. Every
admitted case must define:
- Stable case identifier.
- Use-case class (`planning`, `topology-shadowing`, or `troubleshooting`).
- Evaluated question.
- False answer prevented by the case.
- Current result description.
- Expected analysis status and domain outcome.
- Decisive trace rules, subjects, and semantic facts.
- Expected issue codes, scopes, evidence references, and assumptions when
  readiness is non-Complete.
- Deterministic reproducibility invariants.

## Alternatives

- Open vSwitch or a kernel bridge in a namespace. They answer for their own
  dataplane from their own configuration, need root and Linux, and cannot
  be diffed against a typed expectation. Rejected in the shadow record; this
  record does not reopen it.
- Compute forwarding inside the device service's projection package. It
  would work for the gate and be unreachable from the edge, where a capture
  replay is the second consumer.
- Model vendor dataplanes. Every vendor in the lab differs somewhere
  (FastIron dual-mode ports, LANCOM management VLAN handling), and a model
  that tries to follow each becomes untestable; the standard's model with
  named deviations is checkable.
- Use the generated network model messages as the simulator's own types.
  One shape and free validation, at the cost of unknown-versus-decided
  semantics in every rule, allocation on every lookup, and a schema for
  links, hosts, and traces that nothing on a wire would read yet.
- A device model per feature set (an access switch, a core switch, an
  unmanaged bridge). Each would be a copy of the pipeline with steps
  removed; a capability set expresses the same devices as one pipeline
  with stages present or absent.

## Consequences

- `src/common/net/netaddr`, `src/common/net/vlan`, and `src/common/net/ethernet` hold
  the value types and the codec; `src/common/netsim/trace` holds the step,
  trace, and change records; `src/common/netsim/analysis` holds the trust
  metadata, issue scopes, and evidence catalog; `src/common/netsim/vswitch`
  holds `port`, `phy`, `bridge`, `stp`, `netmodel`, and the switch itself;
  `src/common/netsim/fabric` holds switches, hosts, cables, the run,
  journeys, snapshots, comparison, diff, and derivation;
  `src/common/netsim/search` holds bounded differential search, counterexample
  minimization, and trace alignment;
  `src/common/netsim/internal/netsimtest` holds the versioned conformance
  corpus. A `service.Module` leaf is written with the first host.
- The forwarding scope grows by capability: rapid spanning tree with
  `net/protocol/stp` as the first protocol layer, multicast filtering
  with its table, routing with `net/interface`'s VLAN interfaces and
  routed ports, and `net/ip`'s neighbor entries, LLDP and LACP on the
  same timer facility. Each is a new package under `vswitch` or a
  new rule set, not a rewrite.
- Links load from LLDP neighbors once the full-network view resolves a
  chassis and port id to a device; until then a fabric is built from a
  spec.
- An exhaustive differential, "every flow whose fate changes", is finite
  for a customer bridge: ingress port, classified VID, and destination
  class enumerate it. It is a later question over the same two traces.
- The virtual switch can later stand behind an SNMP or gNMI front so
  FlowSeer's own collectors have a fixture, the way OpenConfig's lemming
  serves its tests.
- The typed effects in `flowseer.device.access.v1` are applied to the
  expected `Config` by an adapter that lands when the first switching intent
  does.

### Remaining capability gaps

The following areas remain outside the foundation established here:

- **Physical media fidelity and PoE dynamics**: transceiver-dependent speed
  resolution, link downshift behavior, and PoE transient allocation
  dynamics. Autonegotiation, reach, and PoE allocation over reported facts
  are modeled, and unreported facts stay unknown.
- **Topology identity and adjacency ambiguity**: resolving links from noisy,
  conflicting, or unmanaged LLDP and CDP neighbor records.
- **Protocol depth**: rapid spanning tree convergence state machines (RSTP and
  MSTP), LACP dynamic aggregation negotiations, IGMP/MLD querier election,
  dynamic IP routing (BGP and OSPF), and transport protocol behavior.
- **Neighbor solicitation and unreachability detection**: the switch observes
  ARP and NDP traffic but never sends a solicitation of its own, and a stale
  or incomplete neighbor entry never moves through `Delay` or `Probe` toward
  a fresh answer; the `Reachable` cache is trusted until it ages out or a
  received advertisement changes it.
- **Packet-generation search spaces**: automated input generation and
  multi-journey exploration budgets for reachability search.
- **The runtime cable fault lives in `fabric.Config`**: `SetFault` writes
  `Config.Cables[idx].Fault` in place, which is exactly why a fork cannot
  share `cfg` and must deep-copy it instead of treating it as construction
  input. Moving the fault out of the configuration is worth doing and is not
  this phase's subject.
- **Comparison, fingerprints, failure cones, and blame**: dependency graphs,
  blast-radius cones, configuration change fingerprints, and automated blame
  attribution for forwarding regressions.
- **System boundaries**: control-plane service wiring, OpenTelemetry span and
  metric instrumentation, persistent run storage, and user interfaces remain
  out of scope.

## Amendments

### 2026-09-17 — device.access moved to model.access

`flowseer.device.access.v1` in Consequences above now reads
`flowseer.model.access.v1`. See [the network model structure
record](2026-08-20-network-model-structure-direction.md#the-package-tree).

### 2026-09-18 — Current-against-candidate comparison

Landed with phase 7a exact comparison (`docs/plans/2026-09-18-2129-feat-netsim-exact-comparison-phase7a-plan.md`).
Added the [Current-against-candidate comparison](#current-against-candidate-comparison)
subsection establishing exact switch and fabric comparison over behavioral
observables with `Equivalent`, `Different`, and `Inconclusive` dispositions,
internal forking of inputs, and injection-ordinal journey pairing.

### 2026-09-19 — Bounded differential search

Landed with phase 7b bounded search (`docs/plans/2026-09-18-2129-feat-netsim-bounded-search-phase7b-plan.md`).
Added the [Bounded differential search](#bounded-differential-search) subsection
establishing finite domain enumeration with exact coverage and remainder
accounting, deterministic replay-checked counterexample minimization,
first-divergence trace alignment, and asymmetric retention under the search
resource contract.
