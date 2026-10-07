---
title: Relay, Port Table, and Traffic - Plan
type: fix
date: 2026-10-01
artifact_contract: flowseer-plan/v2
execution: code
---

# Relay, Port Table, and Traffic - Plan

## Goal

`layer/bridge` keeps a static entry authoritative, reads its filtering
database as of the frame's time, reports a down egress port the same way on
every path, and validates in a fixed order. `sim/port` changes one port's
status without a rebuild. `layer/traffic` copies a stacked-tag frame with the
tags the relay left alone and refuses a queue or policer nothing reads. The
means is six units: a file split with the relay's README, the port table,
three units down the relay, and the traffic layer beside them. Stop
condition: if the corpus pins a step order that one shared per-port egress
function cannot reproduce, U4 keeps the two loops and shares the down-port
record alone.

## Decisions

`B` is `src/common/sim/layer/bridge`, `PT` is `src/common/sim/port`, `T` is
`src/common/sim/layer/traffic`, `V` is `src/common/sim/device/vswitch`, `Fb`
is `src/common/sim/fabric`, and `N` is `src/common/sim/netmodel`. Source
labels are defined under Inventory, Sources.

- The parent's Decisions apply. The relay's reference is `Q2003` clause 8.
  Why: it is the edition phase 3 read for clauses 13 and 14, a public copy
  exists, and the Q-BRIDGE-MIB the virtual-device record names cites its
  tables (`QMIB:846`). Flood VLANs, protected ports, forwarded BPDUs,
  tunnel ports, and priority tags follow `OVS`, which `Q2003` does not hold.
  Later editions of IEEE 802.1Q and IEEE 802.1ad were not read.
- The relay keeps its ingress and egress split. Why: `Q2003` 8.6.1 and 8.8
  run once at the reception Port, and 8.6.2 b) to d), 8.6.3, and 8.6.4 run
  for each transmission Port, which is the split the virtual-device record
  takes from bmv2 (`:127-131`). The record needs no amendment.
- An aging seed for a key that holds a static entry changes nothing and
  counts nothing. Why: `Q2003` 8.10.1 c) 1) has a static entry forward
  "independently of any dynamic filtering information", Table 8-5 gives
  Forward whatever the dynamic entry says, and the table holds one entry
  per key.
- `Ingress` and `Egress` read the filtering database as of their time. Why:
  `Q2003` 8.10.3 has a dynamic entry "automatically removed after a
  specified time", and phases 5 and 6 read `mcast` and `routing` state the
  same way.
- A down egress port has one layer and one rule on both paths, and an
  `Egress` record where the filtering database named the port. A flood
  records the step and no `Egress`, as today. Why: the fabric turns every
  dropped `Egress` into an `OutDiscards` count and a journey drop
  (`Fb/run.go:603-616`, `Fb/counters.go:120-124`), so a record for each down
  member port would count discards on ports no frame was sent to and add
  drops to every flood's journey, which `fabric.Compare` reads. Whether a
  flood lists those ports is carried to U10.
- One function decides a transmission port for both paths. Why: the checks
  are written twice (`B/bridge.go:1028-1163,1295-1373`), which is how entry 5
  arose.
- `Configured` is `"configured"` and `Aging` is `"aging"`. Why: a stored
  entry then prints its origin and lifetime, phase 5 names `mcast`'s the
  same way, and `routing` names every origin
  (`src/common/sim/layer/routing/neighbor.go:55-58`). A seed that omits
  either takes the named default.
- `Clone` clears each gate and keeps its scope, as it clears the selector
  and the resolver. Why: a copied gate reads the source's spanning tree
  after the two diverge, and `Fork` rebinds every gate it holds
  (`V/switch.go:546-552`).
- `port.Table` returns a copy with one port's operational status changed.
  Why: the switch and the relay each rebuild the whole table through the
  builder (`V/switch.go:3785-3795`, `B/bridge.go:305-316`) and each carry a
  copy of the status check (`V/switch.go:3827-3838`, `B/bridge.go:321-332`).
- A policer belongs to a physical port and a queue to a logical one, and a
  LAG's queue statement answers for its members. Why: `OVS` puts
  `ingress_policing_rate` on the Interface, "One physical network device in
  a Port", and `qos` on the Port. The fabric polices the port a frame
  arrived on (`Fb/run.go:525`), and the offered-load record has "a buffer
  stated on a LAG" apply "to each member's queue" (`:70-71`). This answers
  the outline's first question.
- `QueueMaxRate` returns no zero rate. Why: `Validate` refuses one
  (`T/config.go:307-313`) and `New` validates (`T/layer.go:21-24`), so the
  division at `Fb/run.go:143` is safe for a constructed switch. This
  answers the outline's second question.
- A VLAN-output copy removes the tag the relay classified on and no other.
  Why: `OVS` replaces "any existing tag", and for the relay an outer tag is
  the frame's VLAN tag only when its TPID is `0x8100` on a port that is no
  tunnel (`B/bridge.go:574-607,632-647`).
- No refusal added here reaches `netmodel`: it builds no traffic
  configuration, and it sets a seed's origin and lifetime from the named
  constants (`N/netmodel.go:2145-2148`).
- The relay's units form a chain, since each edits `B/README.md` in the
  change that invalidates it. `PT` shares no file with them. U6 lands
  before U3, since both may change tests under `V` and `Fb`.
- These shapes break: the values of `bridge.Configured` and `bridge.Aging`,
  the gates of a cloned relay, `SetOperStatus` on a port the table lacks,
  the layer and rule of a flood's down-port step, a VLAN-output copy of a
  stacked frame, and two configurations `traffic` accepted.
- Every entry below was re-read at `378104ba`. None was run.

## Requirements

R1. Every Correctness entry has a test that fails before its fix, or is
struck with its reason.

R2. A static entry survives any aging seed for its key. Example: learn
`(FID 10, MAC A)` static on `1/1/1`, apply an aging seed for the same key on
`1/1/2`, advance 301 seconds, and `Entries()` holds one entry, static on
`1/1/1`, with `Counters().Learned` at zero.

R3. A down egress port is reported under one layer and one rule on both
paths. Example: ports `1/1/1` to `1/1/3` carry VLAN 10 and `1/1/3` is down.
A frame from `1/1/1` to an address static on `1/1/3` and a broadcast from
`1/1/1` each record a step with layer `relay`, rule `port-down`, and subject
port `1/1/3`. The first lists `1/1/3` in `Egress` with `port-down`, and the
second lists `1/1/2` alone.

R4. `Config.Validate` reports the same field for the same input on every
run. Example: a VLAN table holding 0 and 5000 names `vlan.table.0` on each
of 100 calls.

R5. A lookup at a time past an entry's aging time answers as if `Advance`
had run. Example: `A` is learned on `1/1/1` at `t0` with the default aging
time. A frame for `A` from `1/1/2` at `t0` plus 301 seconds, with no
`Advance` between, floods under rule `unicast-miss`.

R6. A VLAN-output copy keeps every tag the relay did not classify on.
Example: a frame with an outer tag of TPID `0x88a8`, VID 100, and PCP 5 over
a C-tag for VID 20 arrives on a port that is no tunnel and is mirrored to
VLAN 99. The copy on a tagged port carries a C-tag for VID 99 with PCP 0
above both received tags, and the copy on an untagged port carries both.

R7. A queue or policer that nothing reads is refused, and a member answers
with its LAG's queue. Example: with `lag1` over `1/1/1` and `1/1/2`,
`Validate` refuses a policer keyed `lag1` at field `policers.lag1` and a
queue keyed `1/1/1` at `queues.1/1/1`. With a 3,000 octet buffer for PCP 0
on `lag1`, `QueueBuffer("1/1/1", 0)` returns 3,000 and true.

## Out of scope

- The switch's own copies of the membership test, its fallback for a lost
  ingress name, and the per-frame inputs it builds for `Copies`. Phase 9
  owns `V` (Open questions).
- Where the egress queue discipline lives. Phase 10 decides it from what
  this phase leaves in `T`: the configuration, its lookups, and no
  scheduler.
- What Inventory, Limits lists.
- `Ingress` and `Copies` read frames that simulated devices emit and frames
  injected from captures. That input is untrusted for shape: any tag
  stack, TPID, or address is classified or refused, and none panics.
  Configurations come from callers and from `netmodel`, which turns a
  refusal into an issue.

## Inventory

Line numbers hold at `378104ba`. U1 moves the relay's code, and phases 3 to
6 edit `V/switch.go`, so a later unit finds its site by the function named.
Each entry names its unit and its test. After U1 the relay's tests sit in
the file for their subject.

### Sources

- `Q2003`: IEEE Std 802.1Q, 2003 Edition,
  https://bittwist.sourceforge.io/doc/802.1Q-2003.pdf. Read for 8.4.3 to
  8.4.5, 8.5, 8.6.1 to 8.6.4, 8.8, 8.10 with 8.10.1, 8.10.3, 8.10.8, and
  8.10.9, and 8.14.6.
- `OVS`: the Open vSwitch database schema on `branch-3.3`,
  https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/vswitchd/vswitch.xml.
  Read for the Port, Interface, and Mirror tables and the Bridge columns
  `flood_vlans` and `forward-bpdu`.
- `QMIB`: `spec/mib/ietf/Q-BRIDGE-MIB`. `dot1qPortAcceptableFrameTypes`
  (`:1389-1413`) has two values, and `dot1qStaticUnicastAllowedToGoTo`
  (`:826-847`) cites Table 8-5.
- `BMIB`: `spec/mib/ietf/BRIDGE-MIB`, `dot1dTpAgingTime` (`:770`).
- `TCMIB`: `spec/mib/ieee/IEEE8021-TC-MIB-201202150000Z.mib:446`, the
  source of the third admission value, `admitUntaggedAndPriority`.
- Not read: IEEE 802.1Q after 2003 and IEEE 802.1ad. No IEEE text is
  vendored under `spec/`.

### Correctness

1. High, U3. `Learn` replaces a static entry with an aging seed and counts
   it learned (`B/bridge.go:356-362`). `Switch.Learn` refuses the pair as a
   duplicate first (`V/switch.go:675-679`) and `Derive` filters it
   (`V/derive.go:227-242`), so the layer's own callers reach it. Test: R2's
   example.
2. Medium, U4. An ingress port the table lacks loses its name: `res.Ingress`
   takes the resolved port's, which is empty (`B/bridge.go:521`,
   `PT/port.go:296-301`). Test: `Ingress` on `absent` returns a result with
   `Ingress` `absent`.
3. Medium, U5. `Validate` ranges over the VLAN table map
   (`B/config.go:384-391`). Test: R4's example.
4. Medium, U6. `vlanCopyFrame` removes the outer tag whatever its TPID and
   whatever the ingress port (`T/mirror.go:112-119`). A frame under an
   S-tag loses it, and a frame from a tunnel port loses the customer's
   C-tag, where the relay keeps both (`B/bridge.go:606-607,644-646`).
   `TestCopiesToVLANUsesTunnelAndKeepsInnerTags` (`T/mirror_test.go:172`)
   pins the first loss. Tests: R6's example, and a frame with a C-tag for
   VID 10 and PCP 5 from a tunnel port, whose tagged copy carries VID 99
   with PCP 5 above the customer's tag.
5. Low, U4. A down egress port is layer `relay`, rule `port-down`, with an
   output fact on the unicast path (`B/bridge.go:1044-1058`), and layer
   `port`, rule `port.status.down`, with none on the replication path
   (`:1253-1260`). Test: R3's example.
6. Low, U6. A mirror with an output port copies a frame that arrived on
   that port back out of it (`T/mirror.go:52-56`). The switch refuses such
   a frame before it asks for copies (`V/switch.go:1113-1132`), so
   `Layer.Copies` alone shows it. `OVS` has "any frames received on the
   port" discarded. Test: `Copies` with the mirror's output port as ingress
   returns none.
7. Medium, U3. A lookup reads an entry without the time
   (`B/bridge.go:840,967`). `Switch.Forward` and `Switch.Peek` run no
   `Advance` (`V/switch.go:757-765`). The fabric ages first
   (`Fb/run.go:562`), so a direct caller sees it. Test: R5's example, and
   the same frame under `Peek` leaves `Entries()` unchanged.
8. Low, U3. The relay learns in a VLAN no port carries
   (`B/bridge.go:835`). `Q2003` 8.8 d) learns only when "the Member set for
   the frame's VID includes at least one Port". Test: VLAN 10 is in the
   table, `1/1/1` has PVID 10, and no switchport lists 10. An untagged
   frame on `1/1/1` leaves `Entries()` empty.
9. Medium, U6. `Validate` accepts a policer on a LAG and a queue on a LAG
   member (`T/config.go:271-296`). Nothing polices by a LAG's name
   (`Fb/run.go:525`). A data frame reads the LAG's queue (`:619,974`), and a
   protocol frame sent on a member reads the member's (`:1376`), so one
   physical queue answers to two statements, and an LACPDU on a member of a
   LAG with a stated buffer is queued as if none were stated
   (`:998-1003`). Test: R7's example.
10. Struck. `NormalizeSeeds` admits a seed on a port whose PVID is the FID
    and which does not carry the VID on egress (`B/seed.go:109-114`), and a
    frame for that address drops as `not-member`. `Q2003` specifies both
    halves: 8.8 learns on the reception Port of any frame permitted
    ingress, and 8.6.4 filters a frame whose transmission Port "is not
    present in the Member set".

### Completeness

- U1 and U2. `bridge` and `port` have no README. Their contracts sit in
  `V/README.md:104-123,223-239,282-318`.
- Struck. `VLAN.AdmitsVIDOnIngress` exists (`B/config.go:191`), and
  `TestAdmitsVIDOnIngressAgreesWithBridgeIngress`
  (`B/ingress_admission_test.go:39`) holds it to `Ingress`.
- U6. `T/README.md` does not say which port a policer or a queue names, and
  cites `OVS` without the two places it departs from it (Limits).
- Carried to U10. `PortQueues` states a rate and a buffer for each priority
  (`T/config.go:70-73`), and the scheduler that uses them lives in `Fb`.
- Carried to U9. `Copies` takes a switchport summary and an egress list the
  switch builds for it (`V/switch.go:450-466,1832-1838`), and the switch
  checks each copy's output itself (`:1851-1908`).

### Design

- U3. `Origin` and `Lifetime` use the empty string for `Configured` and
  `Aging` (`B/fdb.go:44,56`), so an entry prints neither
  (`Fb/fingerprint.go:118-120`, `Fb/diff.go:668-669`). Phase 5 carried this
  here.
- U4. Two branches cannot run. The `mtu-exceeded` fallback
  (`B/bridge.go:1409-1411`) needs a candidate that neither transmits nor
  records a drop, and every such exit records one
  (`:1315,1334,1352,1456`). The absent-port branch (`:1029-1040`) needs an
  entry on a port the table lacks, and learning and `NormalizeSeeds` store
  table ports alone (`:855`, `B/seed.go:33-40`).
- U5. `Clone` copies gates that still name the source's layers
  (`B/bridge.go:197-199`), and `V/README.md:505-507` says bindings reset.
- U2 and U5. `SetOperStatus` rebuilds the table through the builder in the
  switch and in the relay (Decisions).
- Struck. `PriorityTagPolicy` reads its zero as `Never`. `Normalize` names
  it (`B/config.go:280-282`), `Canonical` prints it so (`:54-59`), and
  `docs/code-style.md:189` asks for a useful zero value. `Admission` has
  the same shape.
- Carried to U9. The membership test is written inline in `V` three times
  (`V/config.go:271-272`, `V/derive.go:190-191,257-259`). The first two are
  `Switchport.CarriesVID` (`B/config.go:115`), and the third is the seed
  rule (`B/seed.go:109`), which U9 can call once it is exported.
- U1, U4, U5, and U6. `B/bridge.go` has 1,676 lines. `Ingress` has 418,
  `Diff` 326 (`B/diff.go:135`), `Egress` 265, `Validate` 237
  (`B/config.go:313`), `replicate` 196, and `traffic`'s `Validate` 192
  (`T/config.go:147`), against the parent's R9.

### Tests

- U2. `TestPortRuleConstants` (`PT/port_test.go:767-771`) checks one of
  seven rule constants against its own literal. What can break is three
  packages each declaring `port.status.down`
  (`PT/port.go:20`, `src/common/sim/layer/stp/layer.go:27`,
  `src/common/sim/layer/routing/layer.go:37`) and three
  `lag.egress.no_member`.
- U1. `B/bridge_test.go` has 3,367 lines and 60 tests on validation,
  ingress, egress, the gate, the table, diff, and the group resolver.
- No test applies an aging seed over a static entry, looks an entry up past
  its aging time, validates a table with two invalid VLANs, or mirrors a
  stacked frame from a port that is no tunnel.

### Limits

`B/README.md` states the first seven with their clause, and `T/README.md`
the last two.

- One filtering database for each VLAN. `Q2003` 8.10.7 lets VLANs share
  one.
- A static entry names one port and forwards to it. `Q2003` 8.10.1 c) gives
  each outbound Port a control of forward, filter, or use dynamic
  information.
- The member set is the configuration. `Q2003` 8.10.9 combines it with
  dynamic VLAN registration, and group registration comes from `mcast`
  through the resolver.
- A port with no PVID drops an untagged frame as `no-pvid`. `Q2003` 8.4.4
  gives every Port one. The loader leaves it unset for a port with several
  untagged VLANs (`V/README.md:272`).
- A reserved destination is refused before the frame is classified, so the
  relay learns no source from one (`B/bridge.go:535-547`). `Q2003` 8.6.1
  submits every frame permitted ingress to the Learning Process. The switch
  hands BPDUs and LACPDUs to their layers first, and the order of that
  intercept is phase 9's.
- An outer tag whose TPID is not `0x8100` is payload on a port that is no
  tunnel, since the switch is one C-VLAN component (virtual-device record,
  `:133-136`). Tunnel ports follow `OVS`'s `dot1q-tunnel`.
- A seed is refused on a port the configuration does not relate to the FID
  by PVID, tagged or untagged set, or tunnel (`B/seed.go:97-114`), where
  learning admits a tagged frame on any port without ingress filtering.
  `Derive` replays learned entries as seeds (`V/derive.go:255-263`) and so
  drops such an entry. The loader relies on the refusal to report a
  forwarding entry that contradicts the collected VLANs
  (`N/netmodel.go:2231-2248`).
- A VLAN-output copy never leaves the port the frame arrived on
  (`T/mirror.go:63-65`). `OVS` sends it there, as its note on unmanaged
  switches describes.
- Two mirrors with one destination each produce a copy. `OVS` mirrors a
  packet "to a particular destination only once". A copy names the mirror
  that produced it (`T/mirror.go:35-41`), and a merged copy has no single
  name.

## Units

### U1. Split the relay by subject and write its README
Files: src/common/sim/layer/bridge/
After: none
Change: `bridge.go` is three files: `layer.go` holds the type, `New`,
`Clone`, the bindings, `SetOperStatus`, and `RetentionKey`, `ingress.go`
holds the `Ingress` descriptor and method, and `egress.go` holds `Egress`,
`EgressTo`, `replicate`, member selection, and the tag form. `fdb.go` takes
`Learn`, `Entries`, `Forget`, `Flush`, `Advance`, and eviction.
`bridge_test.go` is one file for each subject the Inventory names, with the
helpers in their own. Every function and test moves whole and nothing else
changes. `README.md` states what the layer does at this commit: the
pipeline as a diagram with the `Q2003` clause each stage follows, the
configuration with the source of each field, the filtering database, the
`relay` and `vlan` rules and reasons, the bindings, the files, and the
first six Limits with the sources not read.
Tests: `go test -list '.*' ./src/common/sim/layer/bridge` prints the same
names before and after, and each passes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/bridge`

### U2. One status change on the port table
Files: src/common/sim/port/, src/common/sim/layer_names_test.go
After: none
Change: `Table.WithOperStatus(name, state)` returns a table equal to the
receiver with that port's operational status changed, an empty state stored
as `Unknown`, and leaves the receiver as it was. It refuses a name the
table lacks at field `ports.<name>` and a state outside `Unknown`, `Up`,
and `Down` at `ports.<name>.oper_status`. `README.md` describes the table,
its name order, how a member resolves to its LAG, what `Receive` and
`Transmit` decide for an `Unknown` port, the files, and which identifiers
the hub path of `V` emits under layer `port`. `TestPortRuleConstants` goes.
Tests: a changed port reads `Down` while the receiver reads `Up`, and the
other ports and the order are equal. `absent` is refused at `ports.absent`.
A state of `Sideways` on a known port is refused at
`ports.1/1/1.oper_status`, an input the name check passes
(`docs/solutions/conventions/a-refusal-test-needs-an-input-only-the-refusal-rejects.md`).
In `layer_names_test.go`, the `port.status.down` constants of `port`,
`stp`, and `routing` are equal, and so are the `lag.egress.no_member`
constants of `port`, `lag`, and `routing`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/port src/common/sim/layer_names_test.go`

### U3. The filtering database
Files: src/common/sim/layer/bridge/, src/common/sim/device/vswitch/, src/common/sim/fabric/, src/common/sim/netmodel/, src/common/sim/internal/simtest/
After: U1, U6
Change: `Learn` leaves a static entry in place when an aging seed names its
key and counts nothing for that seed. `Egress` treats an aging entry older
than the aging time at the descriptor's `Now` as absent. `Ingress` does the
same for the source's entry: a learning call removes it and learns anew,
which counts one expiry and one learn, and a call that does not learn
writes nothing. `Entries` and `Counters` report the table as of its last
write. `Ingress` learns only when a switchport carries the frame's VLAN,
and a relay without VLAN awareness learns as today. `Configured` and
`Aging` take their names. `NormalizeSeeds` fills an empty `Origin` with
`Configured` and an empty `Lifetime` with `Aging`. It refuses an origin
outside `Configured` and `Observed` at `seeds.<i>.origin` and a lifetime
outside `Aging` and `Static` at `seeds.<i>.lifetime`. `README.md` states the four rules with `Q2003`
8.10.1, 8.10.3, and 8.8, and gains the seed Limit. Outside `B`, only tests
and corpus cases change, where one compares an entry against the empty
string, looks an entry up past its aging time, or learns in a VLAN no
switchport carries.
Tests: entries 1, 7, and 8. A seed of origin `learned`, otherwise valid, is
refused at `seeds.0.origin`. A seed with neither field set is stored as
configured and aging, and an entry learned from a frame as observed and
aging. In `Fb`, two configurations whose one seed omits both fields on one
side and names the defaults on the other compare equal and diff empty
(`docs/solutions/architecture-patterns/validate-and-derive-judge-what-new-builds.md`).
An entry one second inside its aging time is a hit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/bridge src/common/sim/device/vswitch src/common/sim/fabric src/common/sim/netmodel src/common/sim/internal/simtest`

### U4. One egress decision for each port
Files: src/common/sim/layer/bridge/
After: U3
Change: one function decides a transmission port for the unicast and the
replication path, in this order: the port is down, the port does not carry
the VLAN, a gate does not forward, both ports are protected, the payload
exceeds the MTU, a LAG has no member. A flood hands it the ports that carry
the VLAN and no other, as it selects them today
(`B/bridge.go:1247-1250`), so a port outside the VLAN gets no step on a
flood whatever its state, and the second check decides on the unicast path
alone. A down port is a `relay` step with rule `port-down`, the port's
forwarding fact as input, and an egress decision as output on both paths.
The unicast path records each refusal in `Egress`. A flood records a down
port's step without an `Egress`, and its reason when nothing is
transmitted stays the first candidate's drop, or the reason it reports
today when no candidate remains. The same-port rule stays ahead of the
function on both paths. The two branches that cannot run go. A drop before classification
names the port the caller passed when the table holds none. `Ingress`,
`Egress`, and `replicate` are each under 150 lines, cut at the stages the
README's diagram names. `README.md` states the order with `Q2003` 8.4.1
for a down port and 8.6.2 to 8.6.4 for the rest. No test outside `B` pins the flood's old step: `port.status.down`
appears in `V/switch_test.go` under layers `stp` and `routing` alone.
Tests: entries 2 and 5. The flood of R3 with `1/1/2` blocked by a gate
reports `port-blocked`, and with every other member down reports
`no-egress`. With `1/1/3` down and outside VLAN 10, the flood records no
step for it. The tests U1 moved pass unchanged, and so does every test
under `V`, `Fb`, and the corpus, which the verifier runs as importers of
`B` and the stop condition watches.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/bridge`

### U5. Relay configuration and bindings
Files: src/common/sim/layer/bridge/, src/common/sim/device/vswitch/
After: U2, U4
Change: `Validate` checks the VLAN table in ascending order and is cut into
a function for the relay's own fields and one for a switchport. `Diff` is
cut the same way. Each is under 150 lines and reports what it reports
today. `Clone` keeps each gate's scope and clears its gate.
`Layer.SetOperStatus` and `Switch.SetOperStatus` call `WithOperStatus`, and
both copies of `validateOperStatus` go, so each refuses a port the table
lacks. That gives phase 9's entry 12 its first half. `B/README.md` states
what a clone keeps, and `V/README.md:505-507` stays as written. In `V`,
tests change where one sets the status of a port the table lacks.
Tests: entry 3. A relay whose gate refuses `1/1/2` is cloned: the clone
floods to `1/1/2`, still consults the gate's scope, and answers the same
after the source's gate changes its answer.
`TestBridgeFieldsAreClassifiedAndChecked` keeps `gates` deep-copied, with
a probe for the cleared gate. `SetOperStatus` on `absent`
returns an error at `ports.absent` from the layer and from the switch, and
leaves both tables unchanged. `TestDiff` and `TestValidationRules` pass
unchanged.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/bridge src/common/sim/device/vswitch`

### U6. Mirror copies and what a queue or policer names
Files: src/common/sim/layer/traffic/, src/common/sim/device/vswitch/, src/common/sim/fabric/
After: none
Change: a VLAN-output copy removes the outer tag only when its TPID is
`0x8100` or unset and the ingress port is no tunnel, and takes its priority
from an outer C-tag on either kind of port. The ingress port's switchport
decides that, which `copies` holds beside the output's
(`T/mirror.go:46,66`). The output's tunnel decides the output form alone,
as today. A mirror with an output port
makes no copy of a frame that arrived on it. `Validate` refuses a policer
on a LAG at `policers.<name>` and a queue on a LAG member at
`queues.<name>`, and is cut into one function each for mirrors, policers,
and queues, each under 150 lines. `Layer.MaxRate` and `Layer.QueueBuffer`
answer a member's name with its LAG's statement. `New` keeps each member's
LAG from `env.Ports`, which it drops today (`T/layer.go:20-37`), and
`Clone` copies it. `README.md` states the tag
rule, which port a policer and a queue name with `OVS`'s tables, and the
two Limits. Under `V` and `Fb` only tests change, where one pins
`queue-buffer-unstated` for a protocol frame on a member of a LAG that
states a buffer. Every policer there names a physical port and every queue
a port that is no member, and the two tests that mirror into a tunnel
(`V/acceptance_review_test.go:251`, `Fb/traffic_test.go:388`) put the
tunnel on the output.
Tests: entries 4, 6, and 9. `TestCopiesToVLANUsesTunnelAndKeepsInnerTags`
expects both received tags. The policer row gives the LAG a positive burst
and the queue row a valid PCP with a positive rate, so only the new rule
refuses either. A policer on a member and a queue on a LAG are accepted.
`TestCopiesToVLANUsesEachSwitchportTagForm` passes unchanged.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/traffic src/common/sim/device/vswitch src/common/sim/fabric`

Waves: U1 U2 U6 | U3 | U4 | U5

## Verification

For each unit, the verifier on its changed paths, never `--full`. For the
phase:

```bash
go build ./src/common/sim/...
go test -race ./src/common/sim/... ./test/conformance/sim/...
```

## Definition of done

- [ ] Verifier green for every changed path of every unit, with the contract
      gate in `test/conformance/sim` untouched.
- [ ] Each Correctness entry has its failing-first test or its strike.
- [ ] No non-test file under `B`, `PT`, or `T` exceeds 1,200 lines, and no
      function 150.
- [ ] `B/README.md`, `PT/README.md`, and `T/README.md` name their sources,
      clauses, files, and Limits.
- [ ] No plan label in code, comments, or commit messages. This plan's
      `status` is set, and the parent's U7 `Landed:` holds the range.

## Open questions

- Carried to U10: whether a flood lists a down member port in
  `Result.Egress`. The outline asked for it. The fabric would then count an
  `OutDiscards` on each such port and add a drop to the journey of every
  flooded frame (Decisions).
- Carried to U10: the home of the egress queue discipline. `T` holds the
  queue configuration, its lookups, and the member rule, and no scheduler.
- Carried to U9: the inline membership tests of `V`, the fallback for a
  lost ingress name (`V/switch.go:1839-1842`), which the hub path still
  needs (`:2253-2263`), and the inputs the switch builds for `Copies`.
- Carried to U9: whether the relay should learn the source of a frame to a
  reserved address, which turns on where the switch intercepts protocol
  frames.
- Carried to U12: `V/README.md` repeats the relay's ladder, standards, and
  drop reasons that `B/README.md` now states.
- Unverified: what IEEE 802.1Q after 2003 and IEEE 802.1ad say about any
  rule above.
