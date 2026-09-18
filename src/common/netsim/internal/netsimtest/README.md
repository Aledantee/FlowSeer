# netsimtest

Package `netsimtest` provides the versioned conformance test corpus and execution
helpers for verifying network simulation contracts.

The package is internal to `src/common/netsim`. It is test support owned by the
simulation library, not a public service, wire schema, benchmark, or UI. The
representative scale fixture (`RepresentativeFabric`) is a shared topology
envelope; scale measurement and allocation gating live in `fabric`, preserving
this boundary.

## Admitted cases

The corpus maintains executable scenarios locking the simulation contracts
established across the library:

- `planning/candidate-fork-diverges`: A candidate simulation is forked mid-run and
  diverges (faulting a link and running candidate arrivals). Asserts that the
  candidate's execution leaves the source simulation's forwarding state, clock,
  and delivery trace untouched.
- `planning/port-vlan-change`: Reconfigures an access switchport from VLAN 10 to
  VLAN 20 and asserts both forwarding sides, their ordered traces, and typed
  diff facts ([bridge.PVIDFact], [bridge.VLANsFact]) without string parsing.
- `topology-shadowing/partial-model-unknown-port`: Loads a device model with one
  unknown operational port via [netmodel.Load]. Proves that the construction
  specification remains usable, readiness is Incomplete strictly for the
  affected port, and sibling known-up ports retain Complete readiness.
- `topology-shadowing/unresolved-transceiver`: A [fabric.Fabric] journey crosses
  a cable with no stated medium. Both ends still agree on an observed speed, so
  the frame is delivered, but the journey is Incomplete with
  `propagation-unknown`: its timing rests on an unidentified transceiver. One
  [Case] asserts one journey, so the sibling delivery the acceptance example
  describes is a second, registered case,
  `topology-shadowing/unresolved-transceiver-known-delivery`, sharing the same
  fixture. It sends between two fully resolved hosts on the same switch and
  stays Complete.
- `topology-shadowing/uncabled-port-definite-drop`: A switch port named in
  `Config.Uncabled` drops a known-unicast frame Complete with `port-down`.
  `Fabric.Metadata` also carries `adjacency-unresolved` for a sibling port
  that is merely omitted; the journey never depends on that port, and its own
  metadata stays clear of it.
- `topology-shadowing/unreported-negotiation`: A host reports no Ethernet facts
  at all, so its link stays Unknown with `capability-unknown` instead of the
  false answer of an assumed 1 Gb/s full-duplex default. A known-unicast frame
  toward it drops Incomplete.
- `topology-shadowing/unknown-uplink-stp`: A redundant uplink between two
  spanning-tree switches is Unknown because one end reports no Ethernet facts.
  A journey forwarded over the other uplink stays Incomplete. It carries
  `protocol-link-unknown` for every STP port its hops consulted, even the ones
  the unknown link never touches.
- `topology-shadowing/mst-region-boundary`: Two switches name the same MST
  region at different revisions, so every link between them is a boundary
  port. sw2's per-instance path cost, which would flip MSTI 1 onto l2 when
  the region matches, has no effect here: MSTI 1 takes the CIST's own
  blocking decision on the boundary and drops on l2, disproving the false
  answer that an MSTI computes an independent role there that could
  disagree with the CIST's. One [Case] asserts one journey, so VLAN 20's
  unaffected delivery is a second, registered case,
  `topology-shadowing/mst-region-boundary-vlan20-crosses-l1`, sharing the
  same fixture. It crosses l1, the link the CIST forwards on, unaffected by
  VLAN 10's block.
- `troubleshooting/host-rejects-foreign-unicast`: A fully resolved, Complete
  network path carries a known-unicast frame onto a host's port for a MAC
  that is not the host's own address. The host refuses it under
  `host.mac.unicast_not_addressed`: a rejection decision, isolated from any
  topology uncertainty.
- `troubleshooting/unicast-fdb-forwarding`: Evaluates an access-to-trunk frame
  traversal. Asserts that the decisive lookup rule (`unicast-hit`), VLAN
  classification, egress tag rewrite, and Complete readiness are exposed in
  the trace.
- `planning/lag-member-fault-keeps-surviving-flows`: Two flows land on
  distinct members of a BalanceSLB bond (bucket 28 on one member, bucket 253
  on the other). Faulting the first member's flow reassigns only its bucket;
  the surviving flow's next selection keeps the bucket it already held, with
  cause `kept`, disproving the false answer that a member fault remaps every
  flow.
- `planning/mstp-vlan-instances-diverge`: VLAN 10 runs on its own MST
  instance between two switches, with sw2's per-instance path cost inflated
  on l1. MSTI 1 roots through l2, so the VLAN 10 frame crosses it, disproving
  the false answer that spanning tree computes one shape for the bridge and
  sends every VLAN over whatever link that single tree elects. One [Case]
  asserts one journey, so VLAN 20's opposite-link delivery is a second,
  registered case, `planning/mstp-vlan-instances-diverge-vlan20-crosses-l1`,
  sharing the same fixture: with its own per-instance path cost inflated on
  l2, MSTI 2 roots through l1 instead, and the VLAN 20 frame crosses that
  opposite link. A third case sharing the fixture,
  `planning/mstp-vlan-instances-diverge-instance-blocks-alternate`, proves
  MSTI 1 does not merely leave l1 unused: a frame seeded behind l1 for VLAN
  10 is dropped `port-blocked` there, carrying a gate fact for `mstid=1` in
  state Alternate and Discarding, disproving the false answer that MSTI 1
  elected l2 without ever putting l1 into a blocking state.
- `troubleshooting/active-backup-no-failback`: An active-backup bond with no
  configured `Primary` moves from member `a` to member `b` when `a` goes
  down, and stays on `b` with cause `last-active` once `a` recovers,
  disproving the false answer that traffic returns to the lowest-named
  member.
- `troubleshooting/ssm-rejects-unjoined-source`: A port joins a group with a
  source-specific IGMPv3 report naming one source. A frame from a second,
  unjoined source to the same group resolves zero admitted ports and drops
  `unregistered`, disproving the false answer that a group-only model would
  admit it.
- `troubleshooting/leave-last-member-query`: A non-fast leave with an
  observed group-specific query stops forwarding to the departed member at
  the last member query time, two seconds after the leave, well short of the
  260 s membership interval a full run would use, disproving the false
  answer that forwarding continues for the full interval regardless.
- `troubleshooting/stale-root-ages-out`: One uplink hears the root bridge
  directly; a second bridge claims the same root on the other uplink at a
  better cost, in a BPDU whose message age has already reached the max age it
  carries. Storing that claim would make the second uplink Alternate and block
  the frame; discarding it leaves the uplink Designated and Forwarding and the
  frame is delivered, disproving the false answer that every circulating copy
  refreshes the timer and holds the port blocked forever.
- `troubleshooting/bpdu-guard-disables-edge`: An unexpected bridge announces
  itself on an access port configured with BPDU guard. The port is disabled for
  spanning tree with reason `bpdu-guard` and the frame drops `port-blocked`,
  disproving the false answer that an edge port is not part of the tree and so
  keeps forwarding.
- `troubleshooting/loop-guard-unidirectional-link`: The port that became root
  stops receiving BPDUs. Loop guard holds it Alternate and Discarding with
  reason `loop-inconsistent`, disproving the false answer that it becomes
  Designated and forwards, which on a link broken in one direction is a loop.
- `troubleshooting/loop-protect-contains-access-loop`: Two switches joined by
  two cables run no spanning tree, so the cables form a real loop; loop
  protection with Block on one switch's two looped ports acts on exactly one
  of them once a probe returns, and a broadcast the loop-protected switch's
  own host sends afterward reaches the host on the other switch exactly once
  instead of circulating, disproving the false answer that the fabric floods
  the broadcast forever because no spanning tree runs.
- `planning/ecmp-candidates-recorded`: Two equal-cost static routes reach one
  prefix. The lookup fact names both next hops in canonical order and the index
  of the one the flow hash chose, disproving the false answer that one route
  wins and the alternatives are invisible.
- `troubleshooting/neighbor-resolution-pending`: A routed forward's next hop
  has no configured neighbor and none has been observed yet. The frame holds
  with outcome Held and reason `neighbor-pending`, and the forward's metadata
  carries `neighbor-unresolved` at Incomplete scoped to the neighbor lookup,
  disproving the false answer that an unresolved next hop is a definite drop.
  The case goes on to observe an ARP reply, proving the hold is releasable
  rather than a disguised, permanent drop; the observing `Forward` already
  releases the frame, so the case's own `Wake` call is a no-op kept to make
  a future move of the release onto `Wake` visible here.
- `troubleshooting/recursive-route-not-installed`: A `/24` static route names a
  next hop no other route reaches. The device still constructs, the `/24` is
  withdrawn instead of installed, and the packet takes the less specific `/8`
  with a Complete result, disproving the false answer that a broken recursive
  route silently forwards or fails construction.
- `troubleshooting/mdns-ipv4-floods-under-snooping`: A snooping switch with
  `FloodUnregistered` disabled still floods an mDNS query addressed to
  224.0.0.251, because that destination sits in the 224.0.0.0/24 range
  RFC 4541 section 2.1.2 exempts from admission, disproving the false answer
  that snooping drops unregistered mDNS.
- `troubleshooting/mdns-ipv6-unregistered-router-ports`: The same switch
  receives an mDNS query addressed to the link-scope group `ff02::fb` with no
  member ever joined. The frame reaches only the configured router port,
  disproving the false answer that the switch floods link-scope groups the
  way it floods IPv4's reserved range.
- `troubleshooting/mdns-reflected-across-vlans`: An mDNS reflector sits on a
  trunk carrying VLAN 10 and VLAN 20, behind a switch that snoops both VLANs
  with `FloodUnregistered` off. A query from VLAN 10 still reaches the
  reflector, which originates one copy addressed from its VLAN 20 attachment,
  and a VLAN 20 host decodes and delivers it, disproving the false answer
  that a switch locked down against unregistered multicast also blocks the
  reflected copy. One [Case] asserts one journey, so this pins the copy's,
  not the injected query's.
- `troubleshooting/mdns-two-reflectors-loop`: Two reflectors share VLAN 10
  and VLAN 20 over one direct trunk. A copy bouncing between them re-enters
  an endpoint its own ancestry already carries, recording an `EntryLoop`,
  and the run stops on its step budget with work still queued rather than
  circulating forever or overflowing the call stack. That same arrival is
  also where the reflector's own acceptance step, rule ID, and
  `fabric.udp_ports` fact are pinned, since re-entry does not stop the frame
  from being decided, disproving the false answer that re-entry detection
  keyed on the injected query's own frame would catch this.

The fourteen `fabric`-based cases execute a [fabric.Fabric] and populate
[ExecutionResult.Journey], seven of them alongside
[ExecutionResult.FabricMetadata]. Journey is the recorded traversal; its own
`Metadata` is what the case's `ExpectedMetadata` asserts. FabricMetadata is
[fabric.Fabric.Metadata], scoped over the whole topology, so it can carry an
issue a given journey never depended on. Both fields are informational: only
[AssertCase]'s determinism check covers them, not admission's exact-match
expectations.

## Admission bar

Every admitted case must define:

- Stable case identifier.
- Use-case class (`planning`, `topology-shadowing`, or `troubleshooting`).
- Evaluated question and false answer prevented.
- Exact primary-result metadata and the domain outcome. The metadata expectation
  includes the evaluated scope, derived status, issues, evidence contents, and
  assumptions.
- Non-empty decisive trace rules, subjects, and semantic facts. Each is bound
  to an exact [StepExpectation] or [ChangeExpectation].
- The complete ordered semantic trace, including each operation, rule,
  subject, input facts, output facts, and evidence references.
- Exact status, scope, issues, evidence contents, and assumptions for each
  returned comparison, model, or forwarding axis. A non-Complete side must
  declare at least one expected issue.
- Executable fixture binding to library packages.

[AssertCase] executes each fixture twice. It compares outcome and reason,
ordered steps and changes, the evaluated metadata scope, canonical issues,
assumptions, and every evidence entry. Composite results compare both sides of
a switch comparison and the model-loading and forwarding metadata separately.

## Diff coverage

[AssertDiffCoversConfig] walks a seeded `Config` by reflection — structs,
non-nil pointers, map values, and slice elements — and for every leaf it
finds, perturbs a fresh copy at that leaf alone and asserts the package's
`Diff` reports at least one change. A leaf whose perturbation produced the
same value fails outright rather than being skipped, because a check that
plants a value proves nothing unless the plant is known to differ from what
was there:

```go
seed := bridge.Config{VLAN: &bridge.VLAN{Table: map[vlan.ID]string{10: "ten"}}}
netsimtest.AssertDiffCoversConfig(t, seed, bridge.Config.Normalize, bridge.Diff, nil)
```

The value chosen for each leaf matters: normalization can silently absorb a
perturbation into a default that happens to equal the seed's own value (a
non-default enum toggled to its zero value, a priority normalized from
absence), which reads as "no arm" when the real defect is the fixture's
choice of value, not a gap in `Diff`. `lag.Config.Normalize` additionally
takes the port table and the switch's base MAC, unlike its niladic siblings;
its diff_coverage_test.go supplies both through the `normalize` closure.

`port.Diff` takes a `Table` whose fields are unexported and built only
through `NewBuilder`, so [AssertDiffCoversPort] walks the exported
`port.Port` instead and builds each side's table through the builder,
alongside fixed companion ports a perturbed field needs to stay valid (a
paired LAG port for a member's `lag_parent`, for one).

A leaf legitimately outside `Diff`'s contract — evidence that is provenance
rather than compared configuration, one arm of a value that only applies
under a specific discriminant like `Fault.Kind` — is named in the
`exemptions` map with a reason, not silently skipped: an exemption naming a
leaf the walk did not find fails, so a renamed or removed field cannot hide
behind a stale exemption.

[AssertEveryDiffPackageIsCovered] walks `src/common/netsim/vswitch/`, its
direct subdirectories, and `src/common/netsim/fabric/` for a file named
`diff.go` and fails on any path absent from the package's own
`diffCoveredPackages` literal, and on any entry in that literal the walk did
not find. Go builds one test binary per package, so no single package's
`diff_coverage_test.go` can see whether a sibling's exists; this enumeration,
run once from `TestEveryDiffPackageIsCovered`, is what proves the full set
ran. The literal is what a person edits deliberately when a package gains a
`diff.go`; the walk is what would otherwise let that package ship silently
uncovered.

## Retention key coverage

[AssertRetentionKeyCoversConfig] walks a seeded `Config` by reflection and, for
every leaf not named in `exemptions`, perturbs a fresh copy at that leaf alone
and asserts the layer's `RetentionKey` changes. An exemption naming a leaf the
walk did not find fails, ensuring exemption tables do not go stale.
