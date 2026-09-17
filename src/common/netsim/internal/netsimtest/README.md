# netsimtest

Package `netsimtest` provides the versioned conformance test corpus and execution
helpers for verifying network simulation contracts.

The package is internal to `src/common/netsim`. It is test support owned by the
simulation library, not a public service, wire schema, benchmark, or UI.

## Admitted cases

The corpus maintains executable scenarios locking the simulation contracts
established across the library:

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
  The case goes on to observe an ARP reply and wake the switch, proving the
  hold is releasable rather than a disguised, permanent drop.
- `troubleshooting/recursive-route-not-installed`: A `/24` static route names a
  next hop no other route reaches. The device still constructs, the `/24` is
  withdrawn instead of installed, and the packet takes the less specific `/8`
  with a Complete result, disproving the false answer that a broken recursive
  route silently forwards or fails construction.

The twelve `fabric`-based cases execute a [fabric.Fabric] and populate
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
