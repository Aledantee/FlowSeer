---
title: Multicast Snooping and Loop Protection - Plan
type: fix
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-plan.md
---

# Multicast Snooping and Loop Protection - Plan

## Goal

`layer/mcast` gives the same answer whether or not the caller aged it first,
keeps IGMPv3 and MLDv2 source filters through a derive, and follows RFC 3376
and RFC 3810 for the timers it keeps. `layer/loopprotect` arms its timers on
every path and accepts only its own frames. The means is five units: the
restore, expiry at read time, and the compatibility timers with the query
obligations for multicast, then the probe codec and the timers for loop
protection. Stop condition: if a retained multicast layer cannot be rebased
onto a new configuration without replaying reports, retention falls back to
a rebuild that the switch reports as not kept.

## Decisions

`M` is `src/common/sim/layer/mcast`, `LP` is
`src/common/sim/layer/loopprotect`, `V` is `src/common/sim/device/vswitch`,
and `F` is `src/common/sim/fabric`. Source labels are defined under
Inventory, Sources.

- The parent's Decisions apply.
- `mcast` owns the restore of its retained state, as `Restore(prev, keep)`
  on the layer `New` built from the target. Why: the switch replays each
  entry as an IGMPv2 or MLDv1 report (`V/derive.go:298-310`), which discards
  `Entry.Mode` and `Entry.Sources`. A port's state for a group is a mode,
  timers, and pending queries (`M/state.go:51-58`), none of it derived from
  configuration, so it copies without a replay and the stop condition is
  not met.
- A restored record keeps its deadlines, and the target's intervals apply
  to what is learned afterwards. Why: the replay keeps them today
  (`V/derive.go:299`), and `TestMulticastValidationAndDerivation`
  (`V/switch_test.go:6531`) pins a group timer that survives a changed
  `MembershipInterval`.
- The verdict `Derive` reports for multicast stays as it is
  (`V/derive.go:170-178`), and so does the predicate that drops a membership
  on a port spanning tree does not forward (`:195`). Both are phase 9's, as
  its R4 and entry 5. R3 here fixes phase 9's entry 4.
- Every read takes the time. `Resolve`, `Groups`, and `RouterPorts` apply
  the expiry rules as of `now` without writing, and a record is applied to
  state those rules have aged first. Why: `Peek` must not write
  (`TestPeekLeavesMulticastAndMACTablesUntouched`, `V/switch_test.go:6489`),
  the switch forwards a report to `RouterPorts` (`V/switch.go:1613,1641`),
  and only the fabric ages before each arrival (`F/run.go:562`).
- Snooping follows `R4541` for what a switch does with reports and queries,
  and `R3376` and `R3810` for the router state and timers it keeps, the
  mechanism `R4541` 2.1.1 item 6 recommends "on all its non-router ports".
  `R4541` says how a router port is found (2.1.1 item 1) and gives it no
  lifetime, so the 260 second `RouterPortInterval` stays the layer's own.
- IGMP keeps two compatibility timers and MLD one, each started by a report
  only and lasting `MembershipInterval`. Why: `R3376` 7.3.2 keeps an IGMPv1
  and an IGMPv2 Host Present timer and sets each "whenever" a Membership
  Report of that version is received, and `R3810` 8.3.2 says the same of an
  MLDv1 Report. The Older Host Present Interval has the formula of the
  Group Membership Interval (`R3376` 8.13 and 8.4, `R3810` 9.13 and 9.4),
  and the layer configures one interval.
- A Leave or Done heard with no compatibility timer running is applied as
  `TO_IN({})`. `R3376` 7.3.2 and `R3810` 8.3.2 give that translation under
  the older mode and do not say what the message does otherwise. The README
  states this as the layer's reading.
- A pending query lasts while the timer it would have lowered is unlowered.
  It ends when a matching query is observed, however late, when a record
  sets that timer, or when the state ends. Why: `R3376` 6.6.1 lowers the
  timer on any received query with the suppress flag clear, and 6.4.2 has a
  record that expresses interest update the timer during the query period.
  `M/README.md:110-113` already says "until the state changes again".
- Loop protection is the simulator's own protocol, so its README is its
  specification. A behaviour that differs from the README is a defect in
  whichever is wrong, decided per entry. The README gains the frame layout,
  which today only `LP/probe.go` holds.
- `Decode` checks the destination and the EtherType, and the switch keeps
  its destination match (`V/switch.go:1168`). Why: R4 then holds for every
  caller, and the switch already relays a frame `Decode` refuses
  (`V/switch.go:2520-2523`).
- `Encode` returns an error for a port name that is empty or longer than
  255 octets, the two names `Decode` cannot return (entry 7).
- The probe cadence runs only while some port would emit. Why: with every
  port under an applied `Disable`, `NextWake` has the fabric schedule a wake
  each interval that emits nothing (`F/run.go:1468`).
- These shapes break: `mcast.Layer` trades `Retain` and `InstallObserved`
  for `Restore`, `Groups` and `RouterPorts` take a time, `mcast.Configured`
  and `mcast.Aging` are no longer empty, and `Encode` returns an error.

## Requirements

R1. Every Correctness entry has a test that fails before its fix, or is
struck with the clause that makes the current behaviour right.

R2. `Resolve` at a time past an entry's expiry answers as if `Advance` had
run. Example: a router port learned from a query at `t0` and a membership
learned from an IGMPv2 report at `t0`, both on 260 second intervals, are
resolved at `t0` plus 300 seconds with no `Advance` in between. The router
port is absent from the ports and the group resolves as unregistered.

R3. A derive keeps a source-specific membership. Example: on a switch
without spanning tree, port `1/1/1` holds `INCLUDE {10.0.0.9}` for
`239.1.1.1` on VLAN 10, and a derive tags VLAN 20 on port `1/1/3`. The
derived switch reports the entry with mode `Include`, the source, and its
source timer unchanged. It delivers a frame from `10.0.0.9` to `1/1/1` and
does not deliver one from `10.0.0.8`.

R4. A frame is a loop-protection probe only when both its destination and
its EtherType say so. Example: a frame to `03:46:53:4c:50:00` with EtherType
`0x0800`, whose payload is a probe naming the receiving switch and its port
`1/1/1`, leaves `1/1/1` without an action and is relayed.

## Out of scope

- The retention verdict for multicast and the spanning tree predicate in
  `Derive`. Phase 9 owns both.
- A querier, the general query a switch may send on a spanning tree change
  (`R4541` 2.1.1 item 4), and multicast router discovery (item 1a). The
  parent excludes a querier.
- `loopprotect.Decode` reads frames that simulated devices emit and frames
  injected from captures. That input is untrusted for shape: a malformed
  frame is refused and never panics. `mcast.Learn` and `LearnMLD` take
  messages that `src/common/net/igmp` and `src/common/net/mld` decoded, and
  trust their shape.

## Inventory

Line numbers hold at `839c1f9f`. Each entry names its unit and the test that
fails before its fix. Multicast tests go in `M/layer_test.go` and loop
protection tests in `LP/layer_test.go` unless the entry names another file.

### Sources

- `R3376`: RFC 3376, IGMPv3, https://www.rfc-editor.org/rfc/rfc3376.txt.
- `R3810`: RFC 3810, MLDv2, https://www.rfc-editor.org/rfc/rfc3810.txt.
- `R4541`: RFC 4541, Considerations for IGMP and MLD Snooping Switches,
  an Informational RFC, https://www.rfc-editor.org/rfc/rfc4541.txt.
- `IANA`: IEEE 802 Numbers, which lists EtherType `88B5` as "IEEE Std 802 -
  Local Experimental Ethertype",
  https://www.iana.org/assignments/ieee-802-numbers/ieee-802-numbers.txt.
- Vendored: `spec/mib/ietf/IGMP-STD-MIB`, whose defaults for the query
  interval (125), the robustness variable (2), and the last member query
  interval (10 tenths of a second) match `R3376` section 8. No RFC text is
  vendored under `spec/`.
- Not read: IEEE Std 802, which defines the local experimental EtherTypes,
  and the vendor documents `LP/README.md:6-9` names.

The probe has no source outside the repository. Its fixture is the frame
`TestEncodeOffsets` builds (`LP/probe_test.go:13`), 36 octets, which U4
writes into the README as the format's example:

```
03 46 53 4c 50 00 aa bb cc dd ee ff 88 b5 01 02
11 22 33 44 55 01 41 a1 b2 c3 d4 08 65 74 2d 30
2f 30 2f 37
```

It reads as destination `03:46:53:4c:50:00`, source `aa:bb:cc:dd:ee:ff`,
EtherType `0x88b5`, version 1, origin `02:11:22:33:44:55`, VLAN `0x0141`,
sequence `0xa1b2c3d4`, name length 8, and the name `et-0/0/7`.

### Correctness

1. High, U2. `Resolve` admits a learned router port past its expiry and
   counts it in `hasRouter` (`M/layer.go:303-308`). Test: R2's example,
   router half.
2. High, U2. `Resolve` returns `registered` for a group whose every state
   has expired (`M/layer.go:309-313`). The switch then sends the frame to
   the admitted ports only (`V/switch.go:1764-1770`), where an unregistered
   group floods by default (`R4541` 2.1.2 item 3). Test: R2's example, group
   half.
3. High, U5. `Receive` sets a recovery deadline without arming the layer
   (`LP/layer.go:227,236`), so `NextWake` hides it (`:329-331`).
   `LP/README.md:178-179` promises the earlier of the next probe and the
   next recovery. Test: on a layer no other call has touched, with
   `Interval` 60 seconds and a `Timer` recovery of 30, a `Receive` at `t0`
   makes `NextWake` report `t0` plus 30 seconds.
4. Medium, U3. `clearIfMatched` ends an obligation only when the query
   arrives within the last member query time (`M/state.go:342-346`). A later
   query lowers the timers and leaves the group pending. Test: with a router
   port and the default 2 second last member query time, a Leave at `t0`
   and a `Q(G)` at `t0` plus 10 seconds, `Resolve` reports pending at `t0`
   plus 5 and not at `t0` plus 11.
5. Medium, U4. `Decode` reads the payload and ignores the destination and
   the EtherType (`LP/probe.go:92-144`), and the switch matches the
   destination alone (`V/switch.go:1168`). Tests: two `TestDecodeRefusals`
   rows, the fixture with one of the two changed, and R4's example in
   `V/switch_test.go`.
6. Medium, U4. `Config.Validate` does not check `Port.VLANs`
   (`LP/config.go:152-227`). Test: `TestValidate` rows with VLAN 0, 4095,
   and 5000 on an otherwise valid port are refused with field
   `ports.<name>.vlans`.
7. Low, U4. `Encode` writes the name's length into one octet without a
   bound (`LP/probe.go:66`), so a 256-octet name encodes with length 0.
   `Validate` bounds the names a layer probes (`LP/config.go:161`), and a
   direct caller has no guard. The encoder emits what its decoder refuses,
   the gap of
   `docs/solutions/architecture-patterns/a-decoder-wider-than-its-encoder-loses-whatever-you-queue.md`
   seen from the other side. Test: `Encode` refuses a 256-octet name and an
   empty one.
8. Low, U5. `Clear` leaves `interVLAN` set (`LP/layer.go:383-394`). Test:
   after a return on another VLAN and a `Clear`, `PortInfo.InterVLAN` is
   false.
9. Low, U5. `NextWake` keeps returning the probe time when no port would
   emit (`LP/layer.go:333-334`). Test: with one `Disable` port under
   `Manual` recovery and the action applied, `NextWake` reports no timer.
   After a `Clear` at `t1` it reports `t1` plus the interval. A layer
   without ports reports none after `Advance`.
10. Struck. A group-specific query does not end a pending
    group-and-source obligation (`M/state.go:324-331`). `R3376` 6.6.1 has
    `Q(G)` lower the group timer only, so the source timers that `Q(G,A)`
    would lower stay as they were. `TestGroupQueryLeavesSourceObligation`
    pins it: with a router port, after `BLOCK({s})` in `INCLUDE({s})` and a
    `Q(G)`, the group is pending past the last member query time.
11. High, U2. A record is applied to state whose timers have run out
    (`M/layer.go:451-458`), so it takes the wrong row of `R3376` 6.4. Test:
    an IGMPv2 report at `t0`, then at `t0` plus 300 seconds, with no
    `Advance`, an IGMPv3 `IS_EX({10.1.1.1})`. `INCLUDE({})` takes it to
    `EXCLUDE({}, {10.1.1.1})`, so `Resolve` for that source does not admit
    the port. Today the `EXCLUDE` row starts the source at the membership
    interval and admits it.
12. Medium, U2. The switch forwards a report to router ports it reads
    without a time (`V/switch.go:1613,1641`), and builds issue evidence the
    same way (`:1090`). Test in `V/switch_test.go`: a report forwarded 300
    seconds after the only query, with no `Age`, is dropped with
    `no-router-port`.
13. Medium, U3. A Leave or Done restarts the compatibility timer
    (`M/layer.go:436`, `M/state.go:92-93`). `R3376` 7.3.2 and `R3810` 8.3.2
    start it on a report. Test: a port in `INCLUDE({10.1.1.2})` hears an
    IGMPv2 Leave, then `TO_EX({10.1.1.1})`. `Resolve` for `10.1.1.1` does
    not admit the port. Today the Leave starts the timer, the source list
    is dropped, and every source is admitted.
14. Medium, U3. IGMPv1 and IGMPv2 reports share one timer
    (`M/layer.go:205-206`, `M/state.go:55`), so no group is ever in IGMPv1
    mode, where `R3376` 7.3.2 also ignores an IGMPv2 Leave and a `TO_IN`.
    Test: with `FastLeave` set, after an IGMPv1 report a Leave keeps the
    membership, and so does `TO_IN({})`.
15. Medium, U3. A record that sets the timer a query would have lowered
    leaves the obligation pending (`M/state.go:140,229-233`). Tests: with a
    router port, an IGMPv2 report, a Leave at `t0`, and a report at `t0`
    plus 5 seconds, `Resolve` at `t0` plus 6 is not pending. The same after
    `BLOCK({s})` in `INCLUDE({s})` and then `ALLOW({s})`.
16. Medium, U3. The switch refuses every MLD message whose source is not
    link-local (`V/switch.go:1616`). `R4541` section 3 says a report "must
    not be rejected" for the unspecified source, which `R3810` 5.2.13 has a
    host use before it holds a link-local address. Test in
    `V/switch_test.go`: an MLDv2 report from `::` registers the membership.
    The row for `2001:db8::1` (`:6350`) stays refused.

### Completeness

- U1. `mcast.Layer` has no restore, and the switch rebuilds the entries
  from `Groups` and `RouterPorts` (`V/derive.go:290-320`). An `INCLUDE`
  entry's `GroupExpires` is zero (`M/layer.go:50,284`), so the replay
  learns its report 260 seconds before the zero time.
- U1. The virtual-device record says a layer is retained "only when" its
  keys agree (`docs/architecture/2026-09-10-virtual-device-direction.md:271-278`)
  and explains `InstallObserved` through `restoreMulticastState`
  (`:263-266`). Multicast is restored per entry whatever the keys say, and
  the record does not state it.
- Carried to U9. A derive that rebuilds spanning tree drops every
  membership on a spanning tree port (`V/derive.go:195`) and reports
  `Mcast` kept. Phase 9 lists it as its entry 5.
- U2. `M/README.md:91-95` defines `registered` by router state, and the
  code keeps it true after the state expired (entry 2).
- U3. The code and the README cite `R3810` 8.3.2 for the IGMP translation
  (`M/layer.go:420-421`, `M/state.go:50,88`, `M/README.md:52-53`). `R3376`
  7.3.2 is the IGMP source.
- U4. `LP/README.md` is the probe's specification and has no frame layout.

### Design

- U1. `mcast.Layer` clones a whole port table to test `LagParent == ""`
  (`M/layer.go:134,162,386`).
- U3. `Origin` and `Lifetime` use the empty string for `Configured` and
  `Aging` (`M/layer.go:66,78`), so the zero `RouterPort` reads as configured
  and aging at once, a pair no record holds. `routing` names every origin
  (`src/common/sim/layer/routing/neighbor.go:56-57`).

### Tests

- U1. `TestRetainDropsDepartedPortsAndPreservesExpiries`
  (`M/layer_test.go:313`) and `TestInstallObservedRouterPort` (`:186`) test
  the two methods U1 removes.
- U2. No test resolves past an expiry without `Advance`.
  `TestOlderVersionHostBlockIgnoredUntilTimerExpires` (`M/layer_test.go:963`)
  applies a `BLOCK` one second after the group timer ran out and expects
  the `EXCLUDE` row, which pins entry 11.
- U3. `TestBehaviorMatrix` (`M/config_test.go:275-290`) asserts the length
  of a list it just wrote. `TestDiffCoversEveryConfigField` holds the field
  list.
- U3. No test delivers a query after the last member query time.
- U4. Every `TestDecodeRefusals` row (`LP/probe_test.go:109`) has the right
  destination and EtherType.
- U5. `TestInterVLAN` (`LP/layer_test.go:437`) never sets
  `SameUntaggedDomain`.

### Limits

`M/README.md` states each with its section.

- The layer sends no query and takes the last member query time from
  configuration (`M/README.md:99-116`).
- One `MembershipInterval` stands for the Group Membership Interval and the
  Older Host Present Interval.
- A learned router port's lifetime is the layer's own.
- A query or a Done from `::` is refused (Open questions).

## Units

### U1. Multicast restore
Files: src/common/sim/layer/mcast/, src/common/sim/device/vswitch/derive.go, src/common/sim/device/vswitch/derive_internal_test.go, docs/architecture/2026-09-10-virtual-device-direction.md
After: none
Change: `Restore(prev *Layer, keep func(vlan.ID, string) bool)` copies into
the layer, for each VLAN both snoop, every port's state for a group and
every `Observed` router port of `prev` whose port is a logical port of the
layer and that `keep` admits. A state is copied whole: mode, group timer,
source timers, compatibility timer, and pending queries, each deadline as
it was. A router port the layer's configuration makes static stays static,
and a `Configured` record of `prev` is not copied. `Retain` and
`InstallObserved` go. The layer keeps the names of its logical ports, built
by `New` from `env.Ports`, in place of a cloned table. `Derive` calls
`Restore` on the target's layer with the predicate it has today
(`V/derive.go:181-196`), and `restoreMulticastState` goes. `README.md`
describes `Restore` under State retention and Router ports. In the
virtual-device record, the retention paragraph states the multicast rule
(restored per entry, its key naming only the difference), the origin and
lifetime paragraph drops `restoreMulticastState`, and a dated amendment
says why.
Tests: in `layer_test.go`, one case per rule. `INCLUDE {10.0.0.9}` and an
`EXCLUDE` with one running and one blocked source arrive with mode,
sources, and timers equal. A port `keep` refuses, a VLAN the target does
not snoop, and a LAG member in the target's table are dropped. An observed
router port keeps its expiry and yields to a static one. A pending query
survives. A write to the restored layer leaves `prev` unchanged. These
replace the two tests of the removed methods. In
`V/derive_internal_test.go`, R3's example.
`TestMulticastValidationAndDerivation` is the regression check for the
deadlines. `check-prose.py` passes on the README and the record.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/mcast src/common/sim/device/vswitch docs/architecture/2026-09-10-virtual-device-direction.md`

### U2. Expiry at read time
Files: src/common/sim/layer/mcast/, src/common/sim/device/vswitch/switch.go, src/common/sim/device/vswitch/switch_test.go, src/common/sim/device/vswitch/fork_test.go, src/common/sim/device/vswitch/derive_internal_test.go, src/common/sim/fabric/run.go, docs/architecture/2026-09-10-virtual-device-direction.md
After: U1, U4
Change: `Resolve` counts a learned router port only while its expiry is
after `now`, and a port's state for a group only while its group timer runs
in `EXCLUDE` or one of its source timers runs. `registered`, the admitted
ports, and `pending` come from those. `Groups(vid, now)` and
`RouterPorts(vid, now)` return the state the expiry rules leave at `now`.
None of the three writes. `Learn` and `LearnMLD` apply the expiry rules to
a port's state before they apply a record to it. `Advance` removes what the
reads already ignore. The switch's `Groups` and `RouterPorts` take the
time, report forwarding reads `RouterPorts` at `now`
(`V/switch.go:1613,1641`), the unobserved-query hit carries the time of its
lookup for its evidence (`:280-283,1090`), and `Snapshot` passes the fabric
clock (`F/run.go:1878-1879`). `README.md` says so under Timer expiry,
Forwarding, and Aging and snapshots. The record's multicast bullet
(`:497-510`) gains the rule that multicast state is read as of a time.
Tests: R2's example and entries 11 and 12. A table compares the `Resolve`,
`Groups`, and `RouterPorts` answers of a layer that ran `Advance(now)` with
those of a clone that did not, over an `EXCLUDE` with a running and a
blocked source past its group timer, an `INCLUDE` with one expired source,
and an expired router port.
`TestOlderVersionHostBlockIgnoredUntilTimerExpires` keeps the group alive
with an IGMPv3 `IS_EX({})` before its second `BLOCK`.
`TestPeekLeavesMulticastAndMACTablesUntouched` is the check that no read
writes. Existing tests pass `Groups` and `RouterPorts` the time of the step
they check. Nothing here shows `Snapshot` past an expiry: the fabric ages a
switch before each of its arrivals (`F/run.go:562`), so only an arrival at
another device moves the clock past an unaged one.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/mcast src/common/sim/device/vswitch src/common/sim/fabric docs/architecture/2026-09-10-virtual-device-direction.md`

### U3. Compatibility timers, query obligations, and record names
Files: src/common/sim/layer/mcast/, src/common/sim/device/vswitch/switch.go, src/common/sim/device/vswitch/switch_test.go, docs/architecture/2026-09-10-virtual-device-direction.md
After: U2
Change: a port's state for a group keeps an IGMPv1 and an IGMPv2 host
timer, the second also serving MLDv1. A report of that version starts it
for `MembershipInterval`, and a Leave or Done starts none. While the IGMPv1
timer runs, a Leave and a `TO_IN` are ignored, fast leave included, and
`BLOCK` and the source list of `TO_EX` are ignored as in IGMPv2 mode. A
state records the sources a `Send Q(G,X)` row queried. A source leaves that
set when an observed `Q(G,A)` names it, when a record sets its timer, or
when it is deleted, and the obligation ends with the set. The group
obligation ends on an observed `Q(G)` or on a record that sets the group
timer. Neither depends on when the query arrives. `Clone` and `Restore`
copy both timers and the set. `Configured` is `"configured"` and `Aging` is
`"aging"`, so the zero value of either type is no named value.
`TestBehaviorMatrix` goes. The switch checks an MLD message's source after
it decodes the message: a report may come from `::`, and every other
message needs a link-local source. `README.md` cites `R3376` 7.3.2 for IGMP
and `R3810` 8.3.2 for MLD, states the Limits, and describes when an
obligation ends. The record's multicast bullet says the same.
Tests: entries 4 and 13 to 16, and the pin of entry 10.
`TestLeaveTimingFollowsObservedQueries` gains the late query. The zero
`RouterPort` equals no named `Origin` or `Lifetime`.
`TestLayerCloneDeepCopiesSourceRecords` covers the queried set.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/mcast src/common/sim/device/vswitch src/common/sim/fabric docs/architecture/2026-09-10-virtual-device-direction.md`

### U4. Probe codec and configuration
Files: src/common/sim/layer/loopprotect/, src/common/sim/device/vswitch/switch_test.go
After: none
Change: `Decode` refuses a frame whose destination is not the probe group
or whose EtherType is not `0x88b5`, before it reads the payload. `Encode`
returns `(ethernet.Frame, error)` and refuses a name that is empty or over
255 octets. `Advance` encodes through an unexported helper that cannot
fail: `Validate` bounds the length (`LP/config.go:161`), and the port table
holds no empty name (`src/common/sim/port/builder.go:35-36`). `Validate`
refuses a `Port.VLANs` member outside 1 to 4094 (`vlan.ID.Valid`) with
field `ports.<name>.vlans`. `README.md` gains a Probe frame section: the
destination, the EtherType with `IANA`, the payload layout by offset, the
fixture under Sources, and what `Decode` refuses.
Tests: in `probe_test.go`, `Encode`'s frame serialized by
`ethernet.Frame.Encode` equals the fixture's 36 octets, and `Decode` of the
frame built from those literals returns the values under Sources.
`TestDecodeRefusals` gains entry 5's two rows and builds every row from the
fixture with one field changed
(`docs/solutions/conventions/a-refusal-test-needs-an-input-only-the-refusal-rejects.md`).
Entry 7's two refusals. In `config_test.go`, entry 6's rows. In
`V/switch_test.go`, R4's example, and the nine `Encode` calls
(`:2390-3082`) take the error. The format is the simulator's own, so
nothing here can show the README's layout misdesigned.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/loopprotect src/common/sim/device/vswitch src/common/sim/fabric`

### U5. Loop protection timers
Files: src/common/sim/layer/loopprotect/
After: U4
Change: `Receive`, `Advance`, and `LinkChange` arm the layer through one
helper, so `NextWake` reports a deadline `Receive` set. `NextWake` reports
the probe time only while some port would emit, that is, while some port is
not under an applied `Disable`. When `Advance`, `Clear`, or a link-down
`LinkChange` lifts the action that had stopped the last emitting port, the
next probe is one interval after the lift. `Clear` resets `interVLAN` as
it resets the recurrence tracking, and a lift by timer or link cycle keeps
both. `README.md` says so under Recovery and Emission ignores the gate, and the
comment of `PortInfo.InterVLAN` says what clears it.
Tests: entries 3, 8, and 9. `TestInterVLAN` gains a return on another VLAN
with `SameUntaggedDomain` set, which leaves `InterVLAN` false. A clone
taken after `Receive` reports the same `NextWake` as its source.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/loopprotect src/common/sim/device/vswitch src/common/sim/fabric`

Waves: U1 U4 | U2 U5 | U3

## Verification

Per unit, the verifier on its changed paths, never `--full`. For the phase:

```bash
go build ./src/common/sim/...
go test -race ./src/common/sim/... ./test/conformance/sim/...
```

## Definition of done

- [ ] Verifier green for every changed path of every unit, with the contract
      gate in `test/conformance/sim` untouched.
- [ ] Each Correctness entry has its failing-first test or its strike.
- [ ] The READMEs of `M` and `LP` name their sources, sections, and Limits,
      and the virtual-device record is amended.
- [ ] No plan label in code, comments, or commit messages. This plan's
      `status` is set, and the parent's U5 `Landed:` holds the range.

## Open questions

- Unverified: what IEEE Std 802 requires of a frame under a local
  experimental EtherType. `IANA` is the source read, and the README cites
  it.
- Unverified: what a snooping switch does with an MLD query or Done from
  `::`. `R4541` section 3 covers reports and describes a query from `::` as
  one a switch may send. Entry 16 leaves both refused.
- Carried to U9: the multicast retention verdict and the spanning tree
  predicate. After U1, phase 9's entry 4 has a passing test.
- Carried to U9: whether a mirror destination or a loop-protected port
  emits protocol frames (`V/switch.go:3007-3037`).
- Carried to U7 or U9: `bridge.Origin` and `bridge.Lifetime` keep the empty
  string for `Configured` and `Aging`
  (`src/common/sim/layer/bridge/fdb.go:44,56`). Phase 7's inventory does not
  list it.
