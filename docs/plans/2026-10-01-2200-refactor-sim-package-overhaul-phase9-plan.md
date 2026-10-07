---
title: Switch Composition - Plan
type: refactor
date: 2026-10-01
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
---

# Switch Composition - Plan

## Goal

`device/vswitch` handles a frame through one entry that returns the result
with everything the frame caused, drives its layers through one table, forks
and derives every capability it is configured with, and sits in files named
by subject. The means is twelve units: two moves, then the call context, the
output shape, and one unit per pipeline subject.

Stop condition: if the outputs of `Receive` cannot carry what `fabric` reads
after a frame today (`src/common/sim/fabric/run.go:562-628`) without `fabric`
calling a second switch method for the same frame, the output shape of U4 is
wrong and is redrawn before U5 deletes the old entries.

The outline's stop condition is met as written. `Peek` cannot share
`Receive`'s entry, because reception writes the neighbor table
(`routing.Layer.Receive`, `src/common/sim/layer/routing/receive.go:22`) and
policing spends a bucket (`traffic.Layer.Admit`,
`src/common/sim/layer/traffic/layer.go:51`). The two are separate entries
over one pipeline.

## Paths and sources

`V` is `src/common/sim/device/vswitch`, `L` is `src/common/sim/layer`, `Fb`
is `src/common/sim/fabric`, and `ST` is `src/common/sim/internal/simtest`.
Line numbers hold at commit `06c14cac`. After U1 and U2 a symbol keeps its
name and loses its line, so every unit names the function as well.

| Tag | Source |
| --- | --- |
| `Q2003` | IEEE Std 802.1Q-2003, <https://bittwist.sourceforge.io/doc/802.1Q-2003.pdf>, the edition `L/bridge/README.md` cites |
| `D2009` | <https://www.ieee802.org/1/files/public/docs2009/aq-seaman-merged-spanning-tree-protocols-0509.pdf>, the draft `L/stp/README.md` cites |
| `OVS-XML` | <https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/vswitchd/vswitch.xml>, table Mirror, column `output_port` |
| `OVS-BRIDGE` | <https://raw.githubusercontent.com/openvswitch/ovs/73e38c8dfd9ff5f95a92e583780cd996ac11bca7/vswitchd/bridge.c> |
| `OVS-XLATE` | <https://raw.githubusercontent.com/openvswitch/ovs/73e38c8dfd9ff5f95a92e583780cd996ac11bca7/ofproto/ofproto-dpif-xlate.c> |
| `R4541` | RFC 4541, <https://www.rfc-editor.org/rfc/rfc4541.txt> |

What each says, as read:

- `Q2003` 8.5: a frame "addressed to the Bridge Port as an end station shall
  be submitted to the MAC Service user", and such a frame "can also be
  submitted to the Forwarding Process".
- `Q2003` 8.6.1: a frame permitted ingress "shall be forwarded through the
  other Bridge Ports" and "submitted to the Learning Process (8.8)".
- `Q2003` 8.6.2 b): a port is a transmission port only if it "is in a
  forwarding state for that spanning tree".
- `Q2003` 8.8: the Learning Process creates an entry only if "the Port on
  which the frame was received is in a state that allows learning".
- `Q2003` 13.36.1: "as a default behavior, such dynamic changes of data
  rate should not automatically cause changes in Path Cost for the Port
  concerned", written about aggregated links. `D2009` 13.18 repeats it: "it
  would be inappropriate to change topology as a result of one of the links
  in an aggregation failing".
- `OVS-XML`, `output_port`: "Specifying a port for mirror output reserves
  that port exclusively for mirroring. No frames other than those selected
  for mirroring via this column will be forwarded to the port, and any
  frames received on the port will be discarded."
- `OVS-BRIDGE`, `port_configure_stp` and `port_configure_rstp`: a mirror
  output port has spanning tree disabled ("STP on mirror output ports is not
  supported").
- `OVS-XLATE`, `xlate_actions`: a frame taken by `process_special`, which
  handles LACP, still reaches `mirror_ingress_packet`, and that function
  selects mirrors by the input bundle, which for a bond is the bond.
- `R4541` 2.1.1 item 4: a snooping switch "should be aware of link layer
  topology changes caused by Spanning Tree operation" and "a General Query
  may be sent". It removes no membership.

## Decisions

- The parent's Decisions apply. Both tests of the re-plan reference pass on
  this tree: the landed ranges of phases 3 to 7 are ancestors of `HEAD`, and
  `main` holds this phase neither landed nor retired.
- The plan stays whole. Why: every unit edits `V`, and
  `.claude/skills/plan/references/phases.md` keeps one cluster in one plan.
  The units form a chain after the two moves, because each edits
  `V/README.md` in the change that invalidates it (`docs/doc-style.md`) and
  most edit the receive pipeline.
- The two moves come first and change no behaviour. Why: a move reviewed
  alone is a diff of file names, and every later unit then names the file
  its subject lives in.
- Two entries handle a frame. `Receive(now, port, frame)` advances the
  switch to `now`, polices, forwards, and returns an `Output`.
  `ReceiveMirrored` is the same without policing and without copies. Why:
  the fabric repeats police, age, forward, drain, drain failures, and copies
  at each arrival (`Fb/run.go:526-628`), and it exempts a mirror copy from
  both steps (`:521-526,622-628`).
- `Output` embeds `ForwardResult` and adds `Emissions`, `Copies`, and
  `Drops`. `Advance`, `Start`, `LinkChange`, `Mcheck`, and `Derive` return
  the same type with a zero result. Why: the embedding keeps every
  `res.Outcome` at the `Forward` and `Peek` call sites of the tests
  compiling (244 of `Forward` in `switch_test.go` alone), so U5 is a rename
  with judgment where a test drains.
- `NextWake` stays a method and is not a field of `Output`. Why: the package
  shape record lists "advance to a time" and "report the next wake" as two
  verbs of the device seam
  (`docs/architecture/2026-10-01-simulation-package-shape-direction.md:93-95`).
  The outline's Decision put it in the result. The record wins.
- `Receive` runs every layer's `Advance` to `now` before it handles the
  frame. `Age` and `Wake` become one `Advance`. Why: a frame at `now` meets
  the state of `now`, and `L/stp/README.md` (Limits) already records a root
  port whose deadline depends on whether `Advance` ran before `Receive`.
  Probe on this tree: a build in which `Forward` calls `Age` and `Wake`
  first (`go test -overlay`, no tree edit) passes the suites of `Fb`,
  `search`, `netmodel`, and `simload`. Six tests of `V` and one corpus case
  fail, each with `solicitations = 2, want 1`: they forward a reply one
  retransmit interval after the first solicitation without waking the
  switch. U4 and U5 list them.
- U4 adds the new entries beside the old ones, and U5 deletes the old ones.
  Why: the parent forbids a shim that survives the phase, and one unit that
  rewrote the fabric and about 700 test call sites together could not be
  reviewed.
- The switch holds no pending output after U5. Why: outline entry 11 (a
  derive drops undrained emissions, copies, and neighbor failures) cannot
  occur when nothing waits between calls.
- Per-call state lives in a `call` value that each entry creates. The switch
  holds a pointer to the call in progress and nil between calls. Why: the
  relay calls `lagSelector` and `mcastResolver` back with no argument that
  could carry it (`V/switch.go:3649,3679`).
- One table lists the layers a switch holds. `Advance`, `NextWake`, `Fork`,
  and `Derive` loop over it. The switch declares one interface for the two
  timer verbs, as the shape record says
  (the same record, `:123-125`). Why: `Fork` is a hand-written
  literal that omits `filter` (`V/switch.go:497-565`).
- A released frame is filtered at release, by evaluating the ingress and
  egress bindings again on the held frame with `HeldFrame.Ingress`. The
  switch stores nothing per hold token. Why: `filter.Layer` evaluates from
  configuration and the frame's IP header alone
  (`L/filter/filter.go:222,305,382`), and a frame released after
  `Fabric.Configure` then meets the new configuration, as the virtual-device
  record says of a frame that reaches the switch afterwards (`:228-229`).
- Learning precedes reception. Why: `Q2003` 8.6.1 submits a frame permitted
  ingress to the Learning Process, and 8.5 hands it to the service user of
  the port it is addressed to. Today reception runs first
  (`V/switch.go:1125-1129`, before `bridge.Ingress` at `:1264`), so the
  answer is relayed on an empty filtering database and floods.
- An ARP or Neighbor Discovery frame addressed to a routed interface's own
  MAC is taken by that interface, as `routing.Layer.Owns` already takes IP
  (`L/routing/layer.go:744-754`). The test is the destination: a frame the
  layer then fails to decode is taken and ignored, as
  `routing.Layer.Receive` ignores it today (`L/routing/receive.go:34-36`).
  `Q2003` 8.5 delivers such a frame to the service user and permits
  relaying it as well. No source read says a routed VLAN interface's address
  is a filtering entry, so the rule is the switch's, stated in the README.
  A frame of any other EtherType to that MAC is relayed as today, which the
  README states as a limit.
- A mirror output port sends copies, and the LACPDUs of its own members
  when it is a LAG. It sends no BPDU, no probe, and no relayed or released
  frame. Why: `OVS-XML` reserves the port, and `OVS-BRIDGE` names a mirror
  output in three places: spanning tree off, rapid spanning tree off, and
  the choice of the bridge address. It leaves LACP on, and a mirror output
  LAG needs an enabled member to carry a copy (`L/traffic/README.md:38`).
  No source read covers a loop-protection probe, so excluding it is the
  switch's rule, drawn from the reservation. Today a BPDU, a probe, a
  solicitation, and a released frame can leave through one
  (`V/switch.go:2884,2934,3007,3144`).
- A port under a loop-protection action keeps sending protocol frames. Why:
  `L/loopprotect/README.md` (Emission ignores the gate, Not modeled) states
  that a blocked port keeps probing and that the layer never takes the port
  down. No vendor source was read for BPDUs on such a port.
- A mirror reads the logical ingress port. Why: `OVS-XLATE`, and
  `L/traffic/README.md:12-13` ("Port selectors name logical ports"). An
  LACPDU's result names the member (`V/switch.go:2363,2400`), and
  `finishForward` passes that name to `Copies` (`:1717-1721`).
- A LAG reports its fastest enabled member's speed to spanning tree, as
  today (`V/switch.go:3194-3201`). It is the simulator's rule and the README
  states it as a limit. `Q2003` 13.36.1 and `D2009` 13.18 permit a cost
  that reflects an aggregate and advise against one that follows every
  member failure. Neither names a rule. The fastest member holds the cost
  still while members of one speed come and go, where a sum would move it
  each time. It still moves when the only fastest member is lost
  (`L/stp/link.go:117-133`). What IEEE 802.1AX says of an aggregate's rate
  was not read for this plan.
- The relay learns no source from a BPDU or an LACPDU. It stays the Limit
  `L/bridge/README.md` states. Why: the switch hands a BPDU to spanning tree
  ahead of the gate so that a Discarding port still hears its peer
  (`V/switch.go:2553-2558`), and `Q2003` 8.8 a) learns nothing on such a
  port. On a port that does learn, `Q2003` 8.5 with 8.6.1 would learn the
  sender. The README says so with the clause.
- `Derive` no longer drops a multicast membership because spanning tree
  does not forward its port. Why: `R4541` removes none, the relay already
  keeps a frame off a port that is not forwarding (`Q2003` 8.6.2 b), and a
  tree rebuilt by the same derive starts with every port Discarding
  (`V/derive.go:191`), which empties the table.
- `Kept` has one rule per kind of carry. A layer carried whole (spanning
  tree, loop protection, LAG, routing) is kept when its keys are equal, as
  today. A layer carried per record (multicast, the bridge) is kept when no
  record of the source is absent from the target afterwards, whatever the
  keys say. Traffic is kept when its keys are equal, as its tests pin
  (`V/derive_internal_test.go:977-1056`), and the buckets of unchanged
  policers are carried either way. A layer absent on both sides reports
  `Kept: true`. Why: outline R4 asks for the multicast verdict, the three
  key-changing multicast edits of `TestLayerDependencyMutationMatrix` keep
  every record and assert kept (`:656-690`), and a probe that reported
  traffic by buckets failed the two traffic subtests named above.
  `Retention` covers every layer that declares `New`.
- A filter requires routing in the switch (`V/config.go:383-386`). Why: the
  switch evaluates a filter on routed paths alone (`V/switch.go:1230,1297`,
  `assembleRouteResult`), and `Validate` already requires every binding to
  name a routing interface (`:396-402`).
- A switch started alone treats every port as point-to-point unless its
  configuration forces shared (`V/switch.go:2827-2834`). Why: it has no
  peer to resolve duplex against, and the fabric never takes this path
  (`Fb/fabric.go:860-869` reports each port).
- An unanswered next hop still raises `neighbor-unresolved`
  (`V/switch.go:1849-1851`). Why: a fabric host answers no solicitation
  until phase 10 gives it a stack (`Fb/config.go:329-332`), so a timeout is
  no proof that the address is absent.
- Outline entry 1 was confirmed by reading: `filter` is set only in
  `newSwitch` (`V/switch.go:431`). Entries 2 to 13 were read on this tree,
  not run, apart from the probe above.

## Requirements

R1. Every open Correctness entry has a test that fails before its fix. A
struck entry has a test that passes and the reason it is struck.

R2. A fork behaves as its source for every capability. Example: a switch
with a `Default: Drop` filter bound inbound on `vlan10` drops a routed
frame, and so does `sw.Fork()`.

R3. A filter decides a routed frame whether it is forwarded at once or held
first. Example: an outbound `Default: Drop` set on `vlan20` turns a frame
released after its neighbor resolves into a `NeighborDrop` with reason
`filter-drop`, as it drops one whose neighbor was static.

R4. A derive reports what it kept. Example: a derive that changes the
spanning tree priority keeps a membership on an STP port and reports
`Mcast` kept. A derive that removes VLAN 20 drops the memberships of VLAN
20 and reports `Mcast` not kept.

R5. No non-test file in `V` exceeds 1,200 lines and no function 150, unless
a comment at its head states why it is one unit (parent R9). Example:
`switch.go`, 3,823 lines, is the files of U1, and `forward` (291 lines),
`assembleRouteResult` (251), `Derive` (249), `interceptSSTP` (205), and
`forwardHub` (166) are cut by the unit that owns each.

R6. One call returns everything it caused. Example: `Receive` of an ARP
reply that resolves a neighbor returns the reply's result, the released
frame in `Emissions` with `Protocol` false, and no pending state: a second
`Receive` of an unrelated frame returns no emission.

R7. A released frame and a drop name the frame that was held. The message
is `Output`. `Output.Hold` on a `Held` result, `Emission.Hold`, and
`NeighborDrop.Hold` carry `routing.HoldToken`, and `Emission.Ingress` and
`NeighborDrop.Ingress` carry the interface the frame arrived on. None of
these fields exists today. Example: two frames held for two neighbors
return tokens 1 and 2, and the release of the second carries 2.

R8. A neighbor frame is learned before it is answered, and a neighbor frame
to an interface's own MAC is not relayed. Example: an ARP request on `1/1/1`
in VLAN 10 for the address of `vlan10` returns one emission, the reply, on
`1/1/1` alone. A unicast ARP reply to the MAC of `vlan10` is `Consumed`.

R9. A mirror output port emits copies and, as a LAG, its members' LACPDUs
and Marker responses.
Example: with `1/1/4` a mirror output and a spanning tree port, `Start` and
`Advance` across three hello times return no emission on `1/1/4`.

R10. An issue belongs to the call that raised it. Example: a frame held
for a neighbor raises `neighbor-unresolved`, and a policed drop of the next
frame carries no such issue.

## Out of scope

- `State()` and the accessors the fabric reads (`V/switch.go:574-712,
  3269-3386,3689-3705`). The parent writes no interface for one
  implementation, and the value that crosses `sim/device` is drawn in phase
  10 with the host as its second reader. For that phase's open question:
  `Switch.Roles` already names the CIST, since `stp.Layer.PortInfo` answers
  from the common tree in every mode (`L/stp/layer.go:117-121`).
- `SelectMember`, `QueueMaxRate`, `QueueBuffer`, and `PortMTU`. The fabric's
  transmit path calls them (`Fb/run.go:898`), and phase 10 decides where the
  queue discipline lives.
- The three forms of point-to-point: the administrative setting
  (`stp.PointToPoint`), the report (`vswitch.PointToPoint`), and the
  layer's bool. They are three facts, and the report's `Unknown` is what
  raises `protocol-link-unknown`.
- The return type of `RetentionKey`. The sections `diffDependency` parses
  (`V/retention.go:39-74`) name a difference in a report and decide no
  verdict beyond whole-key equality. A typed key changes nine landed
  packages and the conformance gate for that name.
- What `traffic.Layer.Copies` takes. It reads a switchport summary and an
  egress list the switch builds (`V/switch.go:453-469,1709-1721`) because a
  capability imports no sibling (shape record, `:82`). The switch checks
  a copy's output itself, since that needs the port table and a LAG
  selection (`readyMirrorCopies`, `:1730-1786`).
- Hosts that answer a solicitation, and the removal of
  `neighbor-unresolved` that follows. Phase 10.
- `V/README.md` sections that restate the relay's ladder, standards, and
  drop reasons. Phase 12 took them from phase 7.
- A non-mutating policer check. `Peek` does not police, so
  `vswitch.Compare` cannot see a policer change. `Diff` and
  `fabric.Compare` do. U11 states it in the README.
- The switch reads frames that simulated devices emit and frames injected
  from captures. That input is untrusted for shape: a frame a codec refuses
  never panics. An intercept drops it with the layer's reason
  (`V/switch.go:2341-2365,2496-2521`), and `routing.Layer.Receive` ignores
  it.

## Inventory

Entries keep the outline's numbers. Each names the unit that closes it.

### Correctness

1. High, U6. `Fork` drops the packet filter (`V/switch.go:497-565`, the
   comment at `:494` says so). A forked switch skips every
   `s.filter != nil` guard (`:1230,1297,1879,1906`). `fabric.Fork` and
   `fabric.Compare` fork every switch, so a filter change compares
   `Equivalent`. The fork gate asserts the fork's `filter` is zero
   (`V/fork_internal_test.go:57,117-118`).
2. High, U7. A held frame bypasses the egress filter and a deferred ingress
   decision. `assembleRouteResult` returns `Held` (`V/switch.go:1848-1874`)
   before `ResolveDeferred` (`:1879-1903`) and `EvaluateEgress`
   (`:1905-1932`), and `releaseHeldFrame` (`:3111-3177`) evaluates no
   filter.
3. Medium, U3. Per-call hit sets leak into the next result.
   `ComposeForwardResult` (`V/switch.go:774-780`) reaches `wrapResult`,
   which appends the four hit-set issues (`:841-844`), and only `forward`
   resets them (`:1050-1053`).
4. Struck. A derive turned every retained membership into any-source.
   Phase 5 replaced the replay with `mcast.Layer.Restore`
   (`L/mcast/layer.go:127`), and `TestDeriveKeepsSourceSpecificMulticast`
   (`V/derive_internal_test.go:29`) passes.
5. Medium, U10. A derive that rebuilds spanning tree drops memberships on
   STP ports and reports `Mcast` kept (`V/derive.go:171-175,191`).
6. Medium, U9. `protocol-link-unknown` is raised for ports spanning tree
   does not track. `recomputeProtocolLinkIssues` walks every port that is
   no member (`V/switch.go:3791-3822`), and the layer tracks
   `cfg.STP.Ports` alone (`L/stp/layer.go:115`, `sortedKeys(cfg.Ports)`).
   `TestUnknownLinkOnUnconfiguredSTPPortMarksSpanningTreePorts`
   (`V/protocol_link_test.go:390-407`) pins it.
7. Medium, U10. `Forget` leaves the seed in the spec
   (`V/switch.go:695-701`), so a rebuild from `Spec()` reinstalls it and a
   later `Learn` of the same key fails as a duplicate (`:679-683`).
8. Struck. A derive that changes only the node identity kept a routing
   layer bound to the old one. `routing.RetentionKey` now holds the
   identity (`L/routing/layer.go:1235`), so the keys differ and the layer
   is rebuilt (`V/derive.go:203-214`).
   `TestDeriveRoutingRebuildsAcrossNodeIdentities`
   (`V/derive_internal_test.go:1371`) passes.
9. Low, U9. A BPDU on a port the layer does not track is traced as consumed
   and admitted (`interceptBPDU`, `V/switch.go:2523-2544`). The SSTP path
   reports `port-down` for the same port (`:2668-2669,2687-2703`).
10. Low, U10. A derive can leave `portSpeed` nil beside a non-nil `portP2P`
    (`V/derive.go:84-89`), and `updateLagState` then writes to a nil map
    (`V/switch.go:3212-3218`).
11. Low, U5. A derive drops undrained emissions, copies, neighbor failures,
    and the oper error (`V/derive.go:31`).
12. Low, U5. `LinkChange` on an unknown port stores point-to-point and
    speed entries for it (`V/switch.go:3551-3556`) and records the refusal
    where nothing reads it (`:3736-3748`). The first half of the outline's
    entry is closed: `SetOperStatus` refuses an unknown port
    (`:3714-3718`).
13. Low, U11. `Compare` cannot see a mirror copy's content. It compares
    decision facts read from trace steps (`V/compare.go:182-211`), which
    carry no VLAN and no frame.

### Completeness

- U6. The virtual-device record promises an assertion per class for every
  `Switch` field (`:232-247`). The gate passes for a field that is nil on
  the source (`V/fork_internal_test.go:137-174`), and the probes of routing
  and traffic have no guard (`V/fork_test.go:152-189`).
- U9. An MSTI-only transition on a BPDU is invisible, because
  `interceptBPDU` records the CIST before and after
  (`V/switch.go:2493,2527`).
- U9. `Switch.ClearLoopProtect` (`V/switch.go:3463`) has no caller and no
  test.
- U9. A refused LACPDU's decode fact is not asserted on `Peek` and
  `Forward` (`V/switch.go:2346`, `TestLACPDUHandlingAtSwitch` at
  `V/switch_test.go:5696`).
- U10. `V/README.md:585-587` says a layer is retained when both keys match.
  Traffic runs `Retain` whatever the keys say (`V/derive.go:49-51`), and
  `Mcast` is reported by layer presence (`:171-175`).
- U11. `V/README.md:623-625` defines `Inconclusive` as observables that
  matched. The code returns it whenever a side is not complete, with a
  difference filled (`V/compare.go:30-34,45`). `:631` omits `Flooded` and
  `Held`.
- U12. `V/README.md:335-339` says `SetOperStatus` is followed by
  `LinkChange`, which already rewrites the status. `:489-490` promises
  exact reproducibility from `Spec`. `:323-325` names three of the timed
  layers.
- U12. Doc comments that understate the code: the package
  (`V/config.go:1`), `Switch` (`V/switch.go:207-209`), `NextWake`
  (`:3410-3411`), `Drain` (`:3245`), `Diff` (`V/diff.go:38-42`), `Derive`
  (`V/derive.go:24-28`), `Compare` (`V/compare.go:52`).
- Struck. `neighborObservationAllowed` is gone from the tree.

### Design

- U4 and U5. Three output shapes with three drains (`Emission`,
  `traffic.Copy`, `NeighborDrop`) become one `Output`.
- U4. Policing is split: the switch owns the buckets, the fabric builds the
  drop (`Fb/run.go:526-560`), and the switch wraps it.
- U3. Issue codes are five constants (`V/switch.go:3489-3513`) and three
  literals (`:741,792,830`).
- U1 and U2. `switch.go` and `switch_test.go` split by subject.
- U12. `V/README.md:375-468` is spanning tree, loop protection, and LACP
  detail, `:249-261,470-479` is PoE and spanning tree timing, and
  `:263-281` is loader defaults. Each moves to its package.

### Tests

- U6. `TestDiffCoversEveryConfigField` seeds no filter
  (`V/diff_coverage_test.go:42-96`), and the helper returns on a nil
  pointer (`ST/diffcoverage.go:183-185`).
- U11. `Compare` has no test for the observables `reason`, `fid`,
  `egress.port`, `egress.dropped`, `frame.src`, `frame.ethertype`,
  `frame.tags`, and `frame.payload`.
- U12. Five assertions accept either `Forwarded` or `Flooded`
  (`V/switch_test.go:4894,5272,5437,5522,7049`).
- U10. `TestConformanceDeriveInvalidation`
  (`V/conformance_test.go:189-246`) never calls `Derive`.
- U2. One two-VLAN routed configuration literal is written inline in twenty
  tests of `switch_test.go` beside the helper `buildBaseRoutingSwitch`
  (`:3275`), which returns a switch and no `Config`.
- U12. History in comments (`V/switch_test.go:4765,8055,8477,8498,8700,
  8766,8774,8816`, `V/switch.go:3324-3325`) and in a file name
  (`V/acceptance_review_test.go`).
- U12. `time.Now()` in `V/construction_result_test.go:467,542` and
  `V/acceptance_review_test.go:505`.

## Carried items

Each retired phase plan was read at the commit before its retire commit
(`d7ac43ad`, `a51692c5`, `e152c4ed`, `6c4f1915`, `492f3488`, `830736a9`,
`cf429b87`, `06c14cac`). Phases 1 and 13 carry nothing here.

| Item | Source | Disposition |
| --- | --- | --- |
| The switch's `Age`, `Wake`, `NextWake`, `Fork`, and `Derive` loops, its receive entry, and `LinkChange` | phase 2, Out of scope | U4, U5, U6, U10 |
| `bridge.RetentionKey`, `filter.RetentionKey`, and `filter.Layer.Clone` have no production caller | phase 2, Decisions | U6 calls `Clone`. U10 calls both keys |
| `Kept` for multicast decided from the key | phase 2, U6 text | U10, decided from what `Restore` left behind |
| An MSTI transition on a BPDU is not recorded | phase 3, Out of scope | U9 |
| The point-to-point default of a switch run alone | phase 3, Open questions | Decided: stays, with its reason in the README (U9) |
| The speed a LAG reports to spanning tree | phase 4, Inventory and Open questions | Decided: fastest enabled member, `Q2003` 13.36.1. README in U9 |
| Retention reports `Kept: true` for a LAG layer absent on both sides | phase 4, Open questions | Decided: stays, defined in U10 |
| A refused LACPDU's decode fact is not asserted | phase 4, Review gaps | U9 |
| The multicast retention verdict and the spanning tree predicate in `Derive` | phase 5, Decisions and Open questions | U10 |
| Whether a mirror destination or a loop-protected port emits protocol frames | phase 5, Open questions | U9: a mirror output emits copies and its members' LACPDUs and Marker responses alone, a loop-protected port emits as today |
| `bridge.Origin` and `bridge.Lifetime` use the empty string | phase 5, Open questions | No longer applies: the four constants are non-empty strings (`L/bridge/fdb.go:46-61`), and `NormalizeSeeds` maps an empty value to `Configured` and `Aging` (`L/bridge/seed.go:28-32`) |
| The filter on a released frame | phase 6, Out of scope | U7 |
| The hold token and the ingress interface on the switch's result, emission, and drop | phase 6, Open questions | U7 |
| An applier for routing emissions in the loop over layers | phase 6, Open questions | U4 |
| A unicast ARP or Neighbor Discovery frame addressed to an interface MAC is relayed, or dropped on a routed port | phase 6, Open questions | U8 |
| Whether an unanswered next hop still raises `neighbor-unresolved` | phase 6, Open questions | Decided: stays until hosts answer. Phase 10 |
| A release charges a multicast query hit to the observing frame | phase 6, Review gaps | U3 |
| Whether a filter requires routing in the switch | phase 6, Decisions | Decided: it does |
| The inline membership tests of `V` | phase 7, Inventory | U10 |
| The fallback for a lost ingress name | phase 7, Open questions | U9, one function resolves the mirror's ingress |
| The inputs the switch builds for `Copies` | phase 7, Inventory | U9 for the ingress name. The summary and egress list stay (Out of scope) |
| Whether the relay learns the source of a frame to a reserved address | phase 7, Limits and Open questions | Decided: stays a Limit. README reason in U9 |
| Entry 12, first half: `SetOperStatus` on an unknown port | phase 7, U5 text | Closed by phase 7. Second half in U5 |
| Reception runs before ingress learning, so an answer on a VLAN interface floods | named by the coordinator, confirmed at `V/switch.go:1125-1129,1264` | U8 |
| Mirror inputs for an LACP-intercepted frame name the member | named by the coordinator, confirmed at `V/switch.go:2363,1717-1721` | U9 |
| `Ingress` runs twice for a control candidate | outline, Open questions | U8 |

The Limits sections of the layer READMEs add two items the retired plans
do not carry. `L/routing/README.md` says the switch's reception predicate
checks IPv6 Next Header 58 (`V/switch.go:1121-1124`), which U8 keeps. The
relay's seed rule can drop a learned entry in `Derive`
(`L/bridge/README.md`, Limits), which U10 reports through
`Retention.Bridge`.

## Units

### U1. Split the switch source by subject
Files: src/common/sim/layer/bridge/README.md, src/common/sim/layer/routing/README.md, src/common/sim/port/README.md, docs/architecture/2026-09-10-virtual-device-direction.md, src/common/sim/device/vswitch/switch.go, src/common/sim/device/vswitch/spec.go, src/common/sim/device/vswitch/fork.go, src/common/sim/device/vswitch/issues.go, src/common/sim/device/vswitch/forward.go, src/common/sim/device/vswitch/hub.go, src/common/sim/device/vswitch/route.go, src/common/sim/device/vswitch/mcast.go, src/common/sim/device/vswitch/mirror.go, src/common/sim/device/vswitch/intercept.go, src/common/sim/device/vswitch/link.go, src/common/sim/device/vswitch/timers.go, src/common/sim/device/vswitch/lag.go, src/common/sim/device/vswitch/inspect.go
After: none
Change: `switch.go` keeps the `Switch` type, the result types, and
construction. Every other declaration moves whole, with its comment, to the
file of its subject. No declaration changes. The three READMEs that link
`switch.go` for a moved declaration (`L/bridge/README.md:233`,
`L/routing/README.md:466`, `src/common/sim/port/README.md:88`) and the
record (`:534`) name the new file. `V/README.md` has no file table until
U12.

| File | Declarations, by their line in `switch.go` today |
| --- | --- |
| `spec.go` | `ConstructionSpec` and its methods (`:62-139`), `Spec`, `Learn`, `Forget` |
| `fork.go` | `Fork` |
| `issues.go` | `wrapResult`, the hit types, the `*Issues` and `record*` functions, the `Issue*` constants, `recomputeProtocolLinkIssues` |
| `forward.go` | `Forward`, `Peek`, `ComposeForwardResult`, `forward`, `commitBridgeLearning`, `finishForward`, `forwardingDependencies` and its kin |
| `hub.go` | `forwardHub` |
| `route.go` | `observationInterface`, `receiveAndRelease`, `subInterfaceTag`, `assembleRouteResult`, `emitRouting`, `applyRoutingExits`, the held-frame functions, `NeighborDrop` |
| `mcast.go` | the multicast control path (`:1341-1670`) and `mcastResolver` |
| `mirror.go` | `readyMirrorCopies`, the mirror output lookups, `buildTrafficSwitchports`, `Copies`, `Police`, the queue lookups |
| `intercept.go` | the four `intercept*` functions and `sstpVLANFact` |
| `link.go` | `Start`, `linkSpeed`, `LinkChange`, `updateLagState`, `SetOperStatus`, `PointToPoint`, `untaggedVID`, `sameUntaggedDomain` |
| `timers.go` | `Age`, `Wake`, `NextWake`, the three `apply*Effects`, `Drain`, `DrainNeighborFailures`, `Mcheck`, `ClearLoopProtect`, `Emission` |
| `lag.go` | `SelectMember`, the selection helpers, `lagSelector`, `aggregatorScope` |
| `inspect.go` | the read accessors (`:569-756`, `:3269-3386`, `:3689-3705`) |

Tests: the suites of `V`, `Fb`, `ST`, `netmodel`, and `search` pass
unchanged. `go doc -all ./src/common/sim/device/vswitch` prints the same
text before and after, compared with `diff`. No test changes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/device/vswitch src/common/sim/layer/bridge/README.md src/common/sim/layer/routing/README.md src/common/sim/port/README.md docs/architecture/2026-09-10-virtual-device-direction.md`

### U2. Split the switch tests by subject
Files: src/common/sim/device/vswitch/switch_test.go, src/common/sim/device/vswitch/helpers_test.go, src/common/sim/device/vswitch/relay_test.go, src/common/sim/device/vswitch/hub_test.go, src/common/sim/device/vswitch/route_test.go, src/common/sim/device/vswitch/held_test.go, src/common/sim/device/vswitch/filter_test.go, src/common/sim/device/vswitch/mcast_test.go, src/common/sim/device/vswitch/mirror_test.go, src/common/sim/device/vswitch/lag_test.go, src/common/sim/device/vswitch/stp_test.go, src/common/sim/device/vswitch/loopprotect_test.go, src/common/sim/device/vswitch/link_test.go, src/common/sim/device/vswitch/construction_test.go, src/common/sim/device/vswitch/inspect_test.go, src/common/sim/device/vswitch/derive_test.go, src/common/sim/device/vswitch/diff_test.go, src/common/sim/device/vswitch/compare_test.go
After: none
Change: `switch_test.go` (10,787 lines, 167 tests) is deleted and each test
moves whole to the file of its subject. Helpers used by more than one
subject move to `helpers_test.go`, and a helper one subject uses moves with
it. `helpers_test.go` gains `baseRoutingConfig()`, the `Config` that
`buildBaseRoutingSwitch` builds, and the helper calls it. No test body
changes.

| File | Tests, by their line in `switch_test.go` today |
| --- | --- |
| `relay_test.go` | `698-735`, `1608-1670` |
| `hub_test.go` | `308-324`, `513-605`, `906-951`, `1830-1854`, `5167-5196` |
| `route_test.go` | routing and filters of `3666-5166`, `5197-5336`, `7249-7386` |
| `held_test.go` | neighbor resolution and held frames of `6985-7088`, `9016-10347`, `10640-10787` |
| `filter_test.go` | `10388-10639` |
| `mcast_test.go` | `6131-6844`, `7089-7200` |
| `mirror_test.go` | `606-697`, `736-905` |
| `lag_test.go` | `127-197`, `5696-6004`, `6875-6984` |
| `stp_test.go` | `1717-1829`, `1902-2170`, `2284-2370`, `5634-5695`, `7489-7657`, `7738-8912` |
| `loopprotect_test.go` | `2406-3086` |
| `link_test.go` | `66-102` |
| `construction_test.go` | `103-126`, `437-512`, `1671-1716`, `3312-3665` |
| `inspect_test.go` | `344-367`, `1495-1607`, `5568-5633` |
| `derive_test.go`, `diff_test.go`, `compare_test.go` | the Derive, Diff and Equal, and Compare tests of `198-307`, `368-436`, `999-1494`, `1855-1901`, `2171-2283`, `3087-3211`, `5337-5567` |

Tests: `go test -list '.*' ./src/common/sim/device/vswitch` prints the same
sorted test names before and after, and the count is the literal 167 for
the moved tests. The suite passes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/device/vswitch`

### U3. A call owns its issues
Files: src/common/sim/device/vswitch/switch.go, src/common/sim/device/vswitch/issues.go, src/common/sim/device/vswitch/forward.go, src/common/sim/device/vswitch/route.go, src/common/sim/device/vswitch/mcast.go, src/common/sim/device/vswitch/lag.go, src/common/sim/device/vswitch/timers.go, src/common/sim/device/vswitch/link.go, src/common/sim/device/vswitch/inspect.go, src/common/sim/device/vswitch/fork.go, src/common/sim/device/vswitch/fork_internal_test.go, src/common/sim/device/vswitch/switch_internal_test.go, src/common/sim/device/vswitch/issues_internal_test.go, src/common/sim/device/vswitch/held_test.go, src/common/sim/device/vswitch/dependency_metadata_test.go, src/common/sim/device/vswitch/README.md
After: U1, U2
Change: the four hit sets (`V/switch.go:244-259`) leave `Switch` for an
unexported `call` type that also holds `now` and whether the call commits.
`Forward`, `Peek`, `ComposeForwardResult`, `Age`, `Wake`, `Start`,
`LinkChange`, and `Mcheck` each create one, set `Switch.call` to it, and
clear the pointer on return. `wrapResult` takes the call as an argument. A
release of a held frame and an emission of a routing frame run under a
child call of their own, which replaces the save and restore of
`receiveAndRelease` (`:1413-1426`) and `emitRouting` (`:2996-3027`).
`Switch.call` is `resetOnFork`. `SelectMember`, which the fabric's transmit
path calls outside any frame (`Fb/run.go:898`), runs under a call of its
own and discards its hits, since it returns no result to carry an issue.
The three literal issue codes become exported constants beside the five,
one of them in `Power` (`inspect.go`). Rules:

- I1 (entry 3, R10). An issue is raised on the result of the call whose
  frame met the condition, and on no other.
- I2 (phase 6, Review gaps). A released frame's hits belong to no result.
  Source for both: the comments of the hit fields, which promise a reset
  "at the start of every forward call" (`V/switch.go:244-259`).

Tests: `issues_internal_test.go`, each failing first.
`TestPolicedDropCarriesNoEarlierIssue` forwards a frame that is held, then
composes a policed drop on another port and finds no `neighbor-unresolved`.
`TestReleaseChargesNoMulticastQueryToTheObservingFrame` holds a frame to a
group address whose query is unobserved, releases it with an ARP reply, and
finds no `mcast-query-unobserved` on the reply's result.
`TestNoCallOutlivesItsEntry` asserts `sw.call == nil` after each entry.
`TestSelectMemberOutsideACallRaisesNothing` ages a balanced selection past
the rebalance interval, calls `SelectMember`, and finds no panic and no
issue on the next frame's result. Existing tests that change: the internal
test that calls `assembleRouteResult` on a bare switch and reads the hit
set (`switch_internal_test.go:28-39`) passes a call and reads it. The two
tests that pin the save
and restore (`TestObservationReleaseDoesNotChargeLAGRebalanceToObservingFrame`
and its neighbor case, `held_test.go`) keep passing. Mutation: creating the
child call from the parent's sets fails the second test.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/device/vswitch`

### U4. One output per call
Files: src/common/sim/device/vswitch/switch.go, src/common/sim/device/vswitch/output.go, src/common/sim/device/vswitch/layers.go, src/common/sim/device/vswitch/issues.go, src/common/sim/device/vswitch/forward.go, src/common/sim/device/vswitch/route.go, src/common/sim/device/vswitch/mirror.go, src/common/sim/device/vswitch/timers.go, src/common/sim/device/vswitch/intercept.go, src/common/sim/device/vswitch/link.go, src/common/sim/device/vswitch/lag.go, src/common/sim/device/vswitch/derive.go, src/common/sim/device/vswitch/output_test.go, src/common/sim/device/vswitch/README.md, src/common/sim/fabric/run.go, src/common/sim/internal/simtest/cases.go, src/common/sim/internal/simtest/stp_cases.go, src/common/sim/internal/simtest/filter_cases.go, src/common/sim/internal/simtest/mcast_cases.go
After: U3
Change: `output.go` declares `Output`, which embeds `ForwardResult` and adds
`Emissions []Emission`, `Copies []traffic.Copy`, and `Drops []NeighborDrop`.
The call of U3 collects the three lists, and every site that appends to
`s.emissions`, `s.copies`, or `s.neighborFailures` appends to the call.
`layers.go` declares the interface `timed` (`Advance(time.Time)
layer.Effects` and `NextWake() (time.Time, bool)`) and the table of the
layers the switch holds, each with the function that applies its effects:
`applySTPEffects`, `applyLAGEffects`, `applyLoopProtectEffects`, and for
routing `emitRouting` followed by `applyRoutingExits`. `bridge` and `mcast`
have `Advance` and no wake. New entries:

- `Advance(now) Output` runs the table in the order `Wake` and `Age` use
  today (spanning tree, LAG, loop protection, bridge, multicast, routing)
  and returns what the layers emitted.
- `Receive(now, port, frame) Output` runs `Advance`, then polices the frame
  against the port's bucket with `frame.WireOctets()`, then forwards. A
  refused frame returns the drop the fabric builds today
  (`Fb/run.go:532-548`), wrapped by the same call.
- `ReceiveMirrored(now, port, frame) Output` is `Receive` without the
  policer and with no `Copies`.

`Forward`, `Age`, `Wake`, `Drain`, `DrainNeighborFailures`, `Copies`,
`Police`, and `ComposeForwardResult` stay for U5 to delete. `Forward`,
`Age`, and `Wake` run the same pipeline with the semantics they have today
(`Forward` neither advances nor polices). Every entry that keeps its old
signature in this unit (`Forward`, `Age`, `Wake`, `Start`, `LinkChange`,
`Mcheck`) moves its call's lists into the pending fields the three drains
read when it returns, and so does `Derive`, which creates a call on the
new switch around the frames it fails (`V/derive.go:210-213`). `Fb/run.go` calls `Receive` or
`ReceiveMirrored` at `:526-628` and `Advance` at `:411-417`, and reads the
three lists from the value returned. It keeps its calls of `Drain` and
`DrainNeighborFailures` there until U5, because `Derive`, `Start`,
`LinkChange`, and `Mcheck` still leave their output pending and the next
step is where the fabric collects it today. The corpus calls `Receive` and
`Advance`. Rules:

- O1 (R6). A call returns every emission, copy, and drop it caused, and
  the new entries leave none pending.
- O2. A frame handled at `now` meets every timer due at or before `now`.
  Source: Decisions, with the probe.
- O3. A mirror copy arriving from another device is not policed and yields
  no copy. Source: `Fb/run.go:521-524,623-628`, moved.
- O4. `NextWake` is the earliest wake of the table, and no wake when no
  layer reports one.

Tests: `output_test.go`, each failing first. `TestReceiveReturnsTheRelease`
(R6's example). `TestReceiveAdvancesBeforeTheFrame` holds a frame, calls
`Receive` of an unrelated frame one retransmit interval later, and finds
the second solicitation in its `Emissions`.
`TestReceiveAdvancesAgedInformationBeforeABPDU` builds two switches from
identical inputs following `L/stp/README.md` (Limits), advances one at `T`
before handing both the same BPDU at `T`, and compares each tree's port
roles and states, `Output` including `Emissions`, and `NextWake` at `T`,
`T+5s`, and `T+16s` after a reroot that would cut the former root port.
`TestPolicedArrivalStillAdvances` refuses a
frame while a hello is due and finds the BPDU in `Emissions` beside the
drop. `TestReceivePolicesAndReceiveMirroredDoesNot` exhausts a bucket,
compares the two entries, and asserts that `ReceiveMirrored` returns no
copy where `Receive` returns one. `TestNextWakeIsTheEarliestOfTheTable`
arms two layers and asserts the earlier time, and asserts no wake on a
switch with no timed layer. `TestOldEntriesStillDrain` calls `Start`,
`LinkChange`, `Mcheck`, and `Derive` and finds their output in the three
drains, which pins the state U5 starts from. `TestAdvanceVisitsEveryTimedLayer` builds a
switch with all four timed layers and asserts an emission from each within
one call, and fails when a layer is left out of the table. Existing
expectations that change:
the corpus case `troubleshooting/neighbor-resolution-pending`
(`ST/cases.go`), which counts one solicitation where the advance now sends
the retransmission. The fabric's suite is expected to pass unchanged, as in
the probe. A fabric expectation that moves is a deviation to report, since
the probe showed none.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/device/vswitch src/common/sim/fabric/run.go src/common/sim/internal/simtest`

### U5. The old entries go
Files: src/common/sim/device/vswitch/switch.go, src/common/sim/device/vswitch/forward.go, src/common/sim/device/vswitch/timers.go, src/common/sim/device/vswitch/mirror.go, src/common/sim/device/vswitch/link.go, src/common/sim/device/vswitch/fork.go, src/common/sim/device/vswitch/derive.go, src/common/sim/device/vswitch/compare.go, src/common/sim/device/vswitch/README.md, src/common/sim/device/vswitch/helpers_test.go, src/common/sim/device/vswitch/relay_test.go, src/common/sim/device/vswitch/hub_test.go, src/common/sim/device/vswitch/route_test.go, src/common/sim/device/vswitch/held_test.go, src/common/sim/device/vswitch/filter_test.go, src/common/sim/device/vswitch/mcast_test.go, src/common/sim/device/vswitch/mirror_test.go, src/common/sim/device/vswitch/lag_test.go, src/common/sim/device/vswitch/stp_test.go, src/common/sim/device/vswitch/loopprotect_test.go, src/common/sim/device/vswitch/link_test.go, src/common/sim/device/vswitch/construction_test.go, src/common/sim/device/vswitch/inspect_test.go, src/common/sim/device/vswitch/derive_test.go, src/common/sim/device/vswitch/diff_test.go, src/common/sim/device/vswitch/compare_test.go, src/common/sim/device/vswitch/protocol_link_test.go, src/common/sim/device/vswitch/acceptance_review_test.go, src/common/sim/device/vswitch/fork_test.go, src/common/sim/device/vswitch/fork_internal_test.go, src/common/sim/device/vswitch/dependency_metadata_test.go, src/common/sim/device/vswitch/dependency_invariants_test.go, src/common/sim/device/vswitch/conformance_test.go, src/common/sim/device/vswitch/construction_result_test.go, src/common/sim/device/vswitch/marker_test.go, src/common/sim/device/vswitch/neighbor_test.go, src/common/sim/device/vswitch/derive_internal_test.go, src/common/sim/device/vswitch/switch_internal_test.go, src/common/sim/device/vswitch/oper_status_internal_test.go, src/common/sim/device/vswitch/fdb_peek_test.go, src/common/sim/device/vswitch/trace_fact_semantics_test.go, src/common/sim/device/vswitch/issues_internal_test.go, src/common/sim/device/vswitch/output_test.go, src/common/sim/fabric/run.go, src/common/sim/fabric/fabric.go, src/common/sim/fabric/configure.go, src/common/sim/fabric/derive.go, src/common/sim/fabric/README.md, docs/architecture/2026-09-10-virtual-device-direction.md, src/common/sim/fabric/routing_test.go, src/common/sim/fabric/configure_test.go, src/common/sim/fabric/link_test.go, src/common/sim/internal/simtest/filter_cases.go, src/common/sim/internal/simtest/cases.go, src/common/sim/internal/simtest/stp_cases.go, src/common/sim/netmodel/report_test.go, src/common/sim/netmodel/stp_test.go, src/common/sim/netmodel/routing_test.go, src/common/sim/netmodel/netmodel_test.go, src/common/sim/netmodel/conformance_test.go, src/common/sim/netmodel/lacp_test.go, src/common/sim/netmodel/export_test.go, src/common/sim/netmodel/forward_metadata_test.go, src/common/sim/netmodel/acceptance_pass10_test.go, src/common/sim/fabric/topology_state_test.go, src/common/sim/internal/simtest/comparison_cases.go
After: U4
Change: `Forward`, `Age`, `Wake`, `Drain`, `DrainNeighborFailures`,
`Copies`, `Police`, and `ComposeForwardResult` are deleted with the fields
`copies`, `emissions`, `neighborFailures`, and `operErr`. `Start` and
`Mcheck` return `Output`. `Peek` returns `Output` with the result and the
copies it would make, and runs no `Advance`, no policer, and no reception.
`LinkChange` returns `(Output, error)` and
refuses a port the table lacks and a state outside `port.LinkState` before
it writes anything. `SetOperStatus` loses its exported form, and
`Fb/fabric.go:1459` drops its call, since `LinkChange` writes the status
(`V/switch.go:3540,3558`). `Derive` returns `(*Switch, Output, error)`, and
the frames it fails (`V/derive.go:210-213`) are that output's `Drops`. The
fabric reads the returned values at `Fb/fabric.go:591,862-875,1403-1406,
1464-1468` and `Fb/configure.go:103-108`, and `Fb/run.go` drops the two
drain calls U4 kept. `fabric.Derive` records each derived switch's `Drops`
at the derived fabric's clock, as `Configure` does. Today it leaves them
inside the switch for the next step to find (`Fb/derive.go:35-43`). Every
test calls the new entries. The virtual-device record names `Forward` and
`Switch.Wake` as the calls that release and settle (`:852-858`) and says
that `Peek` and `Forward` agree (`:802`). Both sentences are amended to
`Receive` and `Advance`, and `Fb/README.md:679-681` with them. Rules:

- E1 (entry 11). A switch holds no output between calls.
- E2 (entry 12). `LinkChange` on a port the table lacks returns an error
  and changes nothing.
- E3. A frame a derive fails is a drop journey of the derived fabric from
  its first instant. Source: `Fb/configure.go:106-108`, the same rule for
  one switch.

Tests: `TestSwitchHoldsNoPendingOutput` (`switch_internal_test.go`) walks
the fields of `Switch` by reflection and fails on a field whose type is a
slice of `Emission`, `traffic.Copy`, or `NeighborDrop`.
`TestLinkChangeRefusesAnUnknownPort` (`link_test.go`) asserts the error,
an unchanged `Ports()`, and no entry for the name in `portP2P` or
`portSpeed`, and the same for a known port with a state outside
`port.LinkState`. `TestPeekReturnsCopiesAndLeavesNoState` (`output_test.go`)
compares a fork's outputs before and after a `Peek`. All three fail first.
Existing tests that change their
expectation, each because `Receive` advances first:
`TestARPObservationReleasesHeldFrameOnRoutedPort`,
`TestSubInterfaceObservationBindsOnlyItsOuterVID`,
`TestARPObservationReleasesHeldFrameOnVLANInterface`,
`TestReleasedFrameCarriesIngressPCPAndDEI`,
`TestNDPReachesNeighborPathNotMulticastPath`, and
`TestReleaseOntoRefusingRoutedPortRecordsThePortsOwnReason`
(`held_test.go`). Each sends its reply inside the first retransmit
interval, or counts the retransmission. The tests of `operErr`
(`switch_internal_test.go:88-121`) and of exported `SetOperStatus`
(`oper_status_internal_test.go`, `conformance_test.go:235`) are rewritten
against `LinkChange`'s error. The derive test of `Fb/routing_test.go`
(`:1751-1761`) reads the failed frame from the switch's drain and now reads
the drop journey of the derived fabric (E3). The nine `Police` sites assert through
`Receive`. Every other test changes its calls and no expectation.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/device/vswitch src/common/sim/fabric src/common/sim/internal/simtest src/common/sim/netmodel`

### U6. A fork copies every layer
Files: src/common/sim/device/vswitch/fork.go, src/common/sim/device/vswitch/layers.go, src/common/sim/device/vswitch/fork_internal_test.go, src/common/sim/device/vswitch/fork_test.go, src/common/sim/device/vswitch/fork_matrix_test.go, src/common/sim/device/vswitch/diff_coverage_test.go, src/common/sim/fabric/compare_filter_test.go, src/common/sim/device/vswitch/README.md, docs/architecture/2026-09-10-virtual-device-direction.md
After: U5
Change: the table of U4 gains each layer's clone and the binding it needs
on the relay (gate, selector, resolver). `Fork` copies the immutable fields
and then loops over the table, so `filter` and every later layer is cloned.
Rules:

- F1 (entry 1, R2). A fork holds its own clone of every layer its source
  holds.
- F2 (Completeness). The gate proves a class only on a field that is set on
  the source. Source: the virtual-device record, `:232-247`, and
  `docs/solutions/conventions/a-perturbation-gate-that-copies-structs-by-field-passes-vacuously.md`.
- F3. A fork and its source return equal outputs for equal calls, for every
  set of capabilities.

The gate runs over a list of fixtures. The first configures every
capability with state: a filter with a rule and a binding, PoE, a learned
neighbor, a spent policer bucket. A second has no spanning tree and trust
metadata scoped to it, which is the one construction that sets
`missingSTP` (`V/switch.go:350-352`).
`TestSwitchFieldsAreClassifiedAndChecked` fails when a classified field is
zero on the source in every fixture, unless the class table names why the
field has no non-zero value between calls (`call`). `filter` moves from `resetOnFork` to `deepCopied`.
An `immutableShared` struct compares its value fields as well. The record's
paragraph on the three classes is amended to say that the loop over the
table is what copies a layer. `TestDiffCoversEveryConfigField` seeds a
filter and asserts, by reflection over `Config`, that no capability pointer
of its seed is nil. The shared helper keeps returning on a nil pointer
(`ST/diffcoverage.go:183-185`), since nested optional pointers such as a
switchport's `Tunnel` are nil in every layer's seed.

Tests: `TestForkKeepsTheFilter` (R2's example) and
`TestFilterChangeComparesDifferent` in `Fb/compare_filter_test.go`, where
`fabric.Compare` of two fabrics that differ in one filter default returns
`Different`. Both fail first. Mutation: deleting one layer's clone from the
table fails the gate for that field.

The generated check is `fork_matrix_test.go`. It enumerates all 256
subsets of the eight optional capabilities (`Phy`, `LAG`, `STP`,
`LoopProtect`, `Mcast`, `Routing`, `Filter`, `Traffic`) over one base
bridge, skips the 64 that hold `Filter` without `Routing`, and asserts the
literal 192 built
(`docs/solutions/conventions/a-combinatorial-table-count-guard-must-assert-a-literal.md`).
For each it runs `Start` and one frame per pipeline branch (a unicast, a
broadcast, an IGMP report, an ARP request for an interface, a routed IPv4
frame, a BPDU, an LACPDU, a loop-protection probe) through the source and
a fork taken first, and requires equal `Output` at every step. It then
advances the fork alone by an hour and requires the source's next output
to equal that of a second fork taken before the hour. Existing tests that
change: the gate's `filter` assertion, and the routing and traffic probes
of `fork_test.go`, which gain a guard against a nil source.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/device/vswitch src/common/sim/fabric/compare_filter_test.go docs/architecture/2026-09-10-virtual-device-direction.md`

### U7. One routed egress, filtered on both paths
Files: src/common/sim/device/vswitch/route.go, src/common/sim/device/vswitch/forward.go, src/common/sim/device/vswitch/output.go, src/common/sim/device/vswitch/timers.go, src/common/sim/device/vswitch/route_test.go, src/common/sim/device/vswitch/held_test.go, src/common/sim/device/vswitch/filter_test.go, src/common/sim/device/vswitch/routed_egress_matrix_test.go, src/common/sim/device/vswitch/README.md, docs/architecture/2026-09-10-virtual-device-direction.md
After: U6
Change: one function puts a routed frame on its egress interface: through
the relay for a VLAN interface, and directly for a routed port, a
sub-interface, and a routed LAG. `assembleRouteResult` and
`releaseHeldFrame` both call it, and each stays under 150 lines. The step
assembly repeated five times in `assembleRouteResult`
(`V/switch.go:1852-1857,1883-1886,1910-1916,1935-1943,1961-1969`) becomes
one function. Rules:

- G1 (entry 2, R3). A released frame passes the deferred ingress decision
  and the egress binding before it leaves. The switch evaluates
  `EvaluateIngress(hf.Ingress, hf.Frame)`, resolves a deferred result
  against `hf.Interface`, and evaluates `EvaluateEgress(hf.Interface,
  hf.Ingress, hf.Frame)`. A refusal is a `NeighborDrop` with the filter's
  reason and steps. An originated frame (`hf.Ingress` empty) meets the
  egress binding alone. Source: `L/filter/README.md:15-19`. A rule matches
  protocol, addresses, ports, ICMP, and TCP flags
  (`L/filter/config.go:89-97`), none of which routing rewrites, so the
  second evaluation of the ingress binding reads what the first read.
- G2 (R7). `Output.Hold` is `routing.Result.Hold` on a `Held` result.
  `Emission.Hold` and `NeighborDrop.Hold` are `HeldFrame.Token`, and both
  carry `Ingress`. A protocol emission has a zero token. Source:
  `L/routing/neighbor.go:89-113`, `L/routing/layer.go:132-133`, and
  `docs/solutions/architecture-patterns/a-settled-aggregate-record-is-freed-at-settle-keep-its-identity-in-a-key.md`.
- G3. A frame leaves by the same port, member, tags, and priority whether
  it was held or not.

The record's section on held frames gains the filter at release and the
token on the output.

Tests: `filter_test.go` gains R3's example and its deferred twin: a
stateful inbound set on `vlan10` whose decision defers, and an inbound
binding on `vlan20` that does not accept the reversed tuple, so
`ResolveDeferred` refuses (`L/filter/filter.go:305-335`). It gains a frame
held under an accepting filter and released after a `Derive` that keeps
routing and changes the inbound default to `Drop`, which is a
`NeighborDrop`, and an originated frame that meets the egress binding
alone. `held_test.go` gains R7's example, a timed-out frame whose
`NeighborDrop` carries its token and ingress, and a solicitation whose
token is zero. Each fails first. The generated check is
`routed_egress_matrix_test.go`: four egress kinds (VLAN interface, routed
port, sub-interface, routed LAG) by three filter cases (none, deferred
ingress drop, egress drop), the literal 12. Each cell sends one frame to a
static neighbor and one to a neighbor that resolves later, and requires the
released frame's port, member, tags, PCP, and reason to equal the live
frame's. Existing tests expected to change: none.
`TestFilterEgressBindingAppliesToForwardedFramesOnly` (`filter_test.go`)
tells a routed frame from one the switch consumes and keeps passing.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/device/vswitch docs/architecture/2026-09-10-virtual-device-direction.md`

### U8. Learn, then receive
Files: src/common/sim/device/vswitch/forward.go, src/common/sim/device/vswitch/route.go, src/common/sim/device/vswitch/mcast.go, src/common/sim/device/vswitch/route_test.go, src/common/sim/device/vswitch/held_test.go, src/common/sim/device/vswitch/neighbor_test.go, src/common/sim/device/vswitch/forward_internal_test.go, src/common/sim/layer/bridge/ingress.go, src/common/sim/layer/bridge/ingress_learn_test.go, src/common/sim/layer/bridge/README.md, src/common/sim/layer/routing/receive.go, src/common/sim/layer/routing/receive_test.go, src/common/sim/layer/routing/README.md, src/common/sim/internal/simtest/cases.go, src/common/sim/device/vswitch/README.md
After: U7
Change: the relay gains `Learn(in Ingress, f ethernet.Frame) Ingress`, the
learning step of `Ingress` for a frame already classified
(`L/bridge/ingress.go:129`). `commitBridgeLearning` calls it, so `Ingress`
runs once for a control candidate. `forward` is cut into the stages it
already has (mirror output, intercepts, routed port, bridge, hub), each a
function under 150 lines. `routing.Layer` gains `Receives(iface, frame)
bool`, the destination test `Receive` applies before it decodes
(`L/routing/receive.go:31,64`): the interface's MAC or broadcast for ARP,
and `ndpReceiver` for IPv6. Reception moves behind classification:
`observationInterface` and its second `Ingress` pass
(`V/switch.go:1369-1386`) go, and the routed-port stage and the bridge
stage each hand a neighbor frame to `routing.Layer.Receive` once they know
its interface. Rules:

- N1 (R8). On a VLAN interface a neighbor frame's source is learned before
  the layer answers it. Source: `Q2003` 8.6.1 and 8.5.
- N2 (R8). A neighbor frame whose destination is the interface's own MAC is
  `Consumed` on a VLAN interface and on a routed port. Source: Decisions.
- N3. On a routed port every neighbor frame `Receives` reports is
  `Consumed`. Any other frame that is not IP to the interface stays
  `not-bridged`. Source: the virtual-device record, `:843-847`, which lists
  what the layer admits.
- N4. On a VLAN interface a broadcast or group neighbor frame `Receives`
  reports is relayed as today and received as well.
- N5. For a frame `Ingress` admits, `Ingress` with `learn` false followed
  by `Learn` equals `Ingress` with `learn` true in entries, counters,
  steps, and consulted scopes. On a port that learns and does not forward,
  `Ingress` with `learn` true learns and refuses as today
  (`L/bridge/ingress.go:129-143`).
- N6. The reception predicate still requires IPv6 Next Header 58
  (`V/switch.go:1121-1124`), which `L/routing/README.md` (Limits) relies
  on.

Tests: `held_test.go` gains R8's two examples, the second with an encoded
ARP reply and again with a payload `arp.Decode` refuses, and `route_test.go`
gains N3 on a routed port. Each fails first. N4 is pinned by a broadcast
ARP request that is answered and also leaves on the VLAN's other ports, a
solicited-node Neighbor Solicitation with the same two results, and a
unicast reply to another station that is relayed and changes no neighbor
entry. N6 is pinned by an IPv6 frame to the interface with a hop-by-hop
header ahead of ICMPv6, which the layer does not receive. `ingress_learn_test.go` pins N5 over
a table of a learning port, a port that does not learn, a port that learns
and does not forward, a full database, and a tunnel port. `receive_test.go` of `routing` pins `Receives` against
`Receive` over the frames of its existing table: the predicate is true for
every frame the layer answers or learns from. `forward_internal_test.go`
counts calls of the relay's
ingress through a counting gate and requires one for an IGMP report.
Existing tests that change: `TestARPToVLANInterfaceFloodedByRelay`
(`route_test.go`, `switch_test.go:5281` today) expects `Flooded` for a
frame of EtherType ARP to the router's MAC whose payload is no ARP message
(`:5315-5319`). It now expects `Consumed` under a name that says so, and is
the undecodable case above. The sixteen lines of the tests that name `not-bridged` are
read one by one, and one that sends ARP or Neighbor Discovery to a routed
port moves to `Consumed`. The corpus case
`troubleshooting/neighbor-resolution-pending` sends a unicast ARP reply to
the router's MAC and discards that frame's result
(`ST/cases.go:2288-2301`), so its expectation does not move. Mutation: calling `Receive` before `Learn` fails R8's
first example.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/device/vswitch src/common/sim/layer/bridge src/common/sim/layer/routing src/common/sim/internal/simtest/cases.go`

### U9. Protocol frames in and out
Files: src/common/sim/device/vswitch/intercept.go, src/common/sim/device/vswitch/forward.go, src/common/sim/device/vswitch/timers.go, src/common/sim/device/vswitch/mirror.go, src/common/sim/device/vswitch/route.go, src/common/sim/device/vswitch/issues.go, src/common/sim/device/vswitch/link.go, src/common/sim/device/vswitch/stp_test.go, src/common/sim/device/vswitch/lag_test.go, src/common/sim/device/vswitch/mirror_test.go, src/common/sim/device/vswitch/loopprotect_test.go, src/common/sim/device/vswitch/protocol_link_test.go, src/common/sim/device/vswitch/emission_matrix_test.go, src/common/sim/device/vswitch/README.md, src/common/sim/layer/bridge/README.md
After: U8
Change: `interceptSSTP` is cut into decode, VLAN resolution, and the step,
each under 150 lines, and `interceptBPDU` shares its port check and its
step builder. Every emission passes one function of the call before it
joins `Output.Emissions`. Rules:

- P1 (entry 9). A BPDU on a port spanning tree does not track gets the
  outcome the SSTP path gives that port: `Dropped`, `stp.RuleStatusDown`,
  reason `port-down`. Source: `stp.Layer.PortLinked`
  (`L/stp/info.go:235-242`), which `interceptSSTP` already reads.
- P2 (entry 6). `protocol-link-unknown` names the ports of `cfg.STP.Ports`
  and the LAG members, and no other port.
- P3 (Completeness). A BPDU's step records every tree whose role or state
  the frame changed on that port, once. The switch reads the CIST through
  `PortInfo` and each other tree through `VLANPortInfo` of a configured
  VLAN, keeps one snapshot per `PortInfo.Tree` (`L/stp/info.go:54-58`), and
  adds one `stp.PortTransitionFact` output per tree that differs. Sampling
  VLAN 1 for the CIST fails when VLAN 1 is in an MSTI
  (`V/switch.go:3323-3327`).
- P4 (R9). No BPDU, probe, routing frame relayed into a VLAN, or released
  frame names a mirror output port. The emission's port is the logical
  port, which is what `traffic.Layer.MirrorForOutput` is keyed by. A
  released frame refused there is a `NeighborDrop` with reason
  `mirror-output`. An LACPDU or Marker response names a member and is sent.
  Source: Decisions.
- P5. A port under a loop-protection action emits what its layers emit.
  Source: `L/loopprotect/README.md`.
- P6. A mirror selects on the logical ingress port. One function resolves
  it from the call's ingress name through `port.Table.Resolve`, and falls
  back to the given name for a port the table lacks. `finishForward` stops
  reading `Result.Ingress` for it. Source: `OVS-XLATE`,
  `L/traffic/README.md:12-13`.
- P7. A refused LACPDU carries the LACP decode fact, and a refused Marker
  PDU the Marker decode fact, on `Peek` and on `Receive`.
- P8. `ClearLoopProtect` returns `Output`, and a cleared port forwards on
  the next frame.

The README states the point-to-point default of `Start`, the speed a LAG
reports with `Q2003` 13.36.1, and P4 and P5. `L/bridge/README.md` gives the
reserved-address Limit its reason (Decisions).

Tests, each failing first unless marked. `stp_test.go`: P1 on an untracked
up port, and P3 with an MST switch whose CIST role is unchanged while MSTI
1 moves from Designated to Root, one where VLAN 1 is in an MSTI and the
CIST moves, and one where two VLANs share an MSTI and the step holds one
fact for it. `protocol_link_test.go`: P2, where the
issue names `1/1/1` and no issue names the unlisted `1/1/3`.
`mirror_test.go`: R9's example, a mirror with `SelectSrcPorts: lag1` that
copies an LACPDU received on a member, a released frame that is not
flooded to a mirror output, and a mirror output LAG whose members still
send LACPDUs and answer a Marker PDU, on a member not yet enabled for data
as well (both pass today). `lag_test.go`: P7 in
`TestLACPDUHandlingAtSwitch` (mutation: swapping the two fact functions
fails it). `loopprotect_test.go`: P8, and P5 as a test that passes today.
The generated check is `emission_matrix_test.go`: over the subsets of
`STP`, `LAG`, `LoopProtect`, and `Routing` (the literal 16) with one mirror
output port that is no LAG and is also a spanning tree port, a
loop-protected port, and a VLAN member, it runs `Start`, sixty seconds of
`Advance` at each `NextWake`, and the frame list of U6, and requires that
no emission names the mirror output. Existing tests that change:
`TestUnknownLinkOnUnconfiguredSTPPortMarksSpanningTreePorts` pins entry 6
and is rewritten to P2. No other expectation moves.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/device/vswitch src/common/sim/layer/bridge/README.md`

### U10. Derive reports what it kept
Files: src/common/sim/device/vswitch/derive.go, src/common/sim/device/vswitch/retention.go, src/common/sim/device/vswitch/layers.go, src/common/sim/device/vswitch/spec.go, src/common/sim/device/vswitch/config.go, src/common/sim/device/vswitch/derive_test.go, src/common/sim/device/vswitch/derive_internal_test.go, src/common/sim/device/vswitch/derive_matrix_test.go, src/common/sim/device/vswitch/conformance_test.go, src/common/sim/device/vswitch/inspect_test.go, src/common/sim/device/vswitch/README.md, src/common/sim/layer/bridge/seed.go, src/common/sim/layer/bridge/fdb_test.go, src/common/sim/layer/mcast/layer.go, src/common/sim/layer/mcast/layer_test.go, src/common/sim/fabric/fabric.go, docs/architecture/2026-09-10-virtual-device-direction.md
After: U9
Change: `Derive` loops over the table of U4. Each entry gains its retention
key and its carry: clone on equal keys (spanning tree, loop protection,
LAG, routing), `Retain` as today (traffic), `Restore` (multicast), reseed
(bridge),
and nothing for the filter, which holds no runtime record. `Derive` is
under 150 lines. `Retention` gains `Bridge` and `Filter`.
`mcast.Layer.Restore` returns the number of records it left behind. For
the bridge, `Derive` compares the source's dynamic entries with the
target's after the reseed and counts each key the target lacks, so an
entry the target's `MaxEntries` evicts during the replay
(`L/bridge/fdb.go:162-168`) is counted with one the seed rule refuses.
`bridge.SwitchportAdmitsFID` is exported (`L/bridge/seed.go:125`), and the
three inline membership tests call it or `Switchport.CarriesVID`
(`V/config.go:271`, `V/derive.go:186,252`). Rules:

- D1 (R4). `Kept` follows the Decision: key equality for a layer carried
  whole and for traffic, and no lost record for multicast and the bridge.
  A lost record with equal keys names `config`. Any other difference is the
  dependency `diffDependency` names.
- D2 (entry 5). A membership is restored when its port exists, forwards,
  is no LAG member, and carries the VLAN. Spanning tree is not asked.
  Source: `R4541` 2.1.1 item 4, `Q2003` 8.6.2 b).
- D3 (entry 7). `Forget` removes the seed of the same FID and MAC from the
  spec. Source:
  `docs/solutions/architecture-patterns/a-reconfigure-path-stores-configured-state-never-derived.md`.
- D4 (entry 10). A derive never leaves one of the two report maps nil
  beside the other.
- D5. For a switch whose ports hold the states its spec holds,
  `Derive(sw, sw.Spec())` keeps every layer and behaves as `sw.Fork()`. A
  switch moved by `LinkChange` is outside the rule: `Spec` returns the
  ports as constructed (`V/switch.go:474-484`), and a derive rebuilds from
  them where a fork continues.

The record's paragraph on retention (`:270-292`) is amended: traffic and
the bridge joins multicast as a per-record layer, the spanning tree
predicate leaves the multicast rule, and `Kept` has D1's meaning. `Fb/fabric.go:1107-1108`
updates its comment for the two new fields.

Tests, each failing first unless marked. `derive_test.go`: R4's two
examples, and for the first a frame to the group that leaves the STP port
only once spanning tree forwards it. `TestForgetRemovesTheSeed` learns a
seed, forgets it, finds no seed in `Spec()`, rebuilds, and learns the same
key again. `TestDeriveCarriesLinkReportsTogether` derives a switch
whose LAG member's port changed, sends `LinkChange` for the member, does
not panic, and finds the member's point-to-point report `Unknown` with no
speed. `TestDeriveCountsAnEvictedEntry` lowers `MaxEntries` from two to one
and reports `Bridge` not kept. `TestConformanceDeriveInvalidation` calls `Derive` and
asserts the retention it reports. `fdb_test.go` of `bridge` pins the
exported seed rule, and `layer_test.go` of `mcast` asserts the count. The generated
check is `derive_matrix_test.go`: the 192 switches of U6, each derived from
its own spec after `Start` and the frame list and before any `LinkChange`,
with every field of `Retention` kept (walked by reflection,
so a new field is covered) and outputs equal to a fork's over the frame
list. It sits beside `TestDeriveSwitchIdempotenceAndFiftyRandomConstructions`
(`derive_internal_test.go:1077`) and enumerates the capability dimensions
that test samples
(`docs/solutions/conventions/a-fixed-seed-property-generator-must-enumerate-boolean-dimensions.md`).
Existing tests that change: two subtests of `TestLayerDependencyMutationMatrix`
(`derive_internal_test.go:224`), "port admin status drops entry per-entry"
and "port oper status drops entry per-entry". Each drops a membership and
asserts `Mcast.Kept` true, and under D1 asserts false with the dependency
named. `TestMulticastValidationAndDerivation`, the three key-changing
multicast subtests, and the traffic subtests (`:977-1056`) keep passing.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/device/vswitch src/common/sim/layer/bridge src/common/sim/layer/mcast src/common/sim/fabric/fabric.go docs/architecture/2026-09-10-virtual-device-direction.md`

### U11. Compare reads the copies
Files: src/common/sim/device/vswitch/compare.go, src/common/sim/device/vswitch/compare_test.go, src/common/sim/device/vswitch/compare_internal_test.go, src/common/sim/internal/simtest/comparison_cases.go, src/common/sim/internal/simtest/corpus.go, src/common/sim/device/vswitch/README.md, docs/architecture/2026-09-10-virtual-device-direction.md
After: U10
Change: `Comparison` holds two `Output` values, and `compareResults`
compares `Output.Copies` in place of `mirrorCopies`
(`V/compare.go:195-211`). Copies are ordered by mirror, port, and VLAN and
compared by those three, the member, and the frame. The corpus passes a
comparison's two sides to helpers that take a `ForwardResult`
(`ST/corpus.go:921-922,1101-1102`) and now passes the embedded result.
Rules:

- C1 (entry 13). Two results whose copies differ in VLAN, member, or frame
  are `Different` with observable `mirror`.
- C2. Each observable `diffForwardResult` names is reachable as the first
  difference.
- C3. `Inconclusive` is returned when either side is not complete, whether
  or not an observable differs, and `Difference` then names the first one
  found.

The README's comparison section states C3, lists all five outcomes, and
says that `Peek` polices nothing. The record's list of switch observables
(`:399-403`) names the copy's content.

Tests: `compare_test.go` gains C1 three times, each failing first: an
output VLAN changed from 98 to 99, the same VLAN with another LAG member,
and the same copy with another snap length and one test per observable without one: `reason`, `fid`,
`egress.port`, `egress.dropped`, `frame.src`, `frame.ethertype`,
`frame.tags`, `frame.payload`. Each asserts the observable string and both
values. `compare_internal_test.go` gains C3 with an incomplete side that
also differs. Existing tests that change:
`TestCompareDetectsMissingMirrorCopy` (`compare_test.go:152`) asserts the
decision fact's text as the difference and now asserts the copy.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/device/vswitch src/common/sim/internal/simtest docs/architecture/2026-09-10-virtual-device-direction.md`

### U12. README, comments, and test hygiene
Files: src/common/sim/device/vswitch/README.md, src/common/sim/device/vswitch/config.go, src/common/sim/device/vswitch/switch.go, src/common/sim/device/vswitch/timers.go, src/common/sim/device/vswitch/diff.go, src/common/sim/device/vswitch/derive.go, src/common/sim/device/vswitch/compare.go, src/common/sim/device/vswitch/inspect.go, src/common/sim/device/vswitch/hub.go, src/common/sim/device/vswitch/size_test.go, src/common/sim/device/vswitch/acceptance_review_test.go, src/common/sim/device/vswitch/mirror_test.go, src/common/sim/device/vswitch/lag_test.go, src/common/sim/device/vswitch/construction_test.go, src/common/sim/device/vswitch/construction_result_test.go, src/common/sim/device/vswitch/route_test.go, src/common/sim/device/vswitch/held_test.go, src/common/sim/device/vswitch/derive_test.go, src/common/sim/device/vswitch/stp_test.go, src/common/sim/layer/stp/README.md, src/common/sim/layer/loopprotect/README.md, src/common/sim/layer/lag/README.md, src/common/sim/layer/phy/README.md, src/common/sim/layer/bridge/README.md, src/common/sim/netmodel/README.md
After: U11
Change: `V/README.md` keeps the composition: the entries, the pipeline as a
Mermaid flowchart of the stages U8 names, the output, fork, derive, and
compare. The spanning tree, SSTP, loop protection, and LACP detail
(`:375-468` today) moves to the README of its layer where that README lacks
it and is deleted where it repeats it. PoE (`:249-253,470-479`) moves to
`L/phy/README.md` and loader defaults (`:263-281`) to
`netmodel/README.md`, and the link from `L/bridge/README.md` follows. The
README's file table lists the files of U1. The sentence on exact
reproducibility says what `Spec` holds: configuration, seeds, node
identity, and trust metadata, with ports as constructed. Each doc comment
under Inventory, Completeness says what its code does. `forwardHub` is cut
under 150 lines. The tests of `acceptance_review_test.go` move to the file
of their subject and the file goes. The five either-outcome assertions
name the one outcome the test produces. The comments under Inventory,
Tests lose their history and keep the rule they explain. The three
`time.Now()` calls become fixed instants.
Tests: `uv run tools/scripts/run.py verify check-prose` passes on every
README named. `go test -list` shows the tests of
`acceptance_review_test.go` under the same names. A test fails when a
non-test file of `V` exceeds 1,200 lines or a function 150 without the
comment R5 allows: `TestSourceSizeLimits` in `size_test.go`, written with
`go/parser`. Existing tests that change: the five
assertions, each to the outcome read from a run of the test before the
edit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/device/vswitch src/common/sim/layer/stp/README.md src/common/sim/layer/loopprotect/README.md src/common/sim/layer/lag/README.md src/common/sim/layer/phy/README.md src/common/sim/layer/bridge/README.md src/common/sim/netmodel/README.md`

Waves: U1 U2 | U3 | U4 | U5 | U6 | U7 | U8 | U9 | U10 | U11 | U12

## Verification

Per unit, the verifier on that unit's paths, never `--full`. For the phase:

```bash
go build ./src/common/sim/... ./src/edge/simload/...
go test -race ./src/common/sim/... ./src/edge/simload/... ./test/conformance/...
golangci-lint run ./src/common/sim/...
uv run tools/scripts/run.py verify check-plan-state
```

The four generated checks print their counts: 192 forks, 12 routed egress
cells, 16 emission subsets, 192 derives.

## Definition of done

- [ ] Verifier green for every changed path of every unit.
- [ ] Every open Inventory entry has its test, and each struck entry its
      reason.
- [ ] Every row of Carried items is closed by its unit or is decided, with
      the README sentence its row names.
- [ ] `V/README.md`, the layer READMEs U12 names, and the virtual-device
      record match the tree.
- [ ] No plan label in code, comments, or commit messages.
- [ ] The outcome is recorded with `uv run tools/scripts/run.py plan record
      implemented <plan> --units 12 --from <t> --to <t>`, or `partial`.

## Open questions

- Unverified: IEEE 802.1Q after 2003 and IEEE Std 802.1D-2004. Every rule
  above that cites `Q2003` rests on the 2003 text alone, as
  `L/bridge/README.md` says of the relay.
- Unverified: that a device with a routed VLAN interface keeps a unicast
  frame for that interface off the VLAN's other ports. `Q2003` 8.5 permits
  the relay. N2 is the switch's rule and the README says so.
- Unverified: what a vendor's loop-protected port does with BPDUs and
  LACPDUs. P5 keeps today's behaviour.
- Unverified: IEEE 802.1AX on an aggregate's data rate and on whether a
  Slow Protocols frame reaches the Learning Process. The draft `L/lag/README.md` cites was not read for
  this plan, and the relay's Limit covers the LACPDU.
- U5, U6, U7, U10, and U11 amend the virtual-device record, and U5 edits
  `Fb/README.md`. Phase 8's last unit edits both files. Each amendment here
  is a dated section under Amendments plus the sentence it corrects, so a
  merge keeps both.
- The implementer decides the names inside `call`, the table's entry type,
  and the files a moved README paragraph lands in. The rules and the
  exported names above are fixed.
