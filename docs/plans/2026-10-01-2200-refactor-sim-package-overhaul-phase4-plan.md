---
title: Link Aggregation and Physical Layer to Standard - Plan
type: fix
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: fixes needed
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-plan.md
---

# Link Aggregation and Physical Layer to Standard - Plan

> Implemented. 6 units, 2026-10-04T13:02:48Z to 2026-10-04T14:30:22Z.

## Goal

`layer/lag` runs LACP as IEEE 802.1AX specifies, and `layer/phy` resolves
speed, duplex, and PoE inside the bounds its inputs state. The means is six
units: the LACPDU codec, the LACP state machines in three steps, the
layer's remaining fixes with its documents, and the physical layer. Stop
condition: if the standard's machines cannot run behind the entry points
the switch already drives (`LinkChange`, `Receive`, `Advance`, `NextWake`)
without a new call from it other than the Marker dispatch of U4, the phase
stops and the switch's side is planned first.

## Decisions

`L` is `src/common/sim/layer/lag`, `P` is `src/common/sim/layer/phy`, `C` is
`src/common/net/lacp`, `V` is `src/common/sim/device/vswitch`, and `F` is
`src/common/sim/fabric`. Source labels are defined under Inventory, Sources.

- The parent's Decisions apply. Protocols are implemented to their standards.
- The reference is IEEE Std 802.1AX-2014, read from `AX`, and the layer is a
  Version 1 implementation. Why: R2 names a Periodic machine, which the next
  revision folds into the Transmit machine (`AX2018`, introduction), and
  Version 1 LACPDUs are what a conformant System must send (`AX` 5.3 c). The
  published text was not read (Open questions).
- The layer claims `AX` 5.3 a to d: the sublayer of 6.2, LACP as 6.3 and 6.4
  give it, Version 1 LACPDUs, and the Marker Responder. This settles the
  parent's Marker question: the responder is mandatory and "the use of the
  Marker protocol is optional" (`AX` 6.5.1). The options of 5.3.1 and the
  separate class of 5.4 stay unmodelled (Inventory, Limits).
- A LAG keeps one Aggregator. Why: `AX` 6.7.4.2 allows fewer Aggregators
  than ports, and a port without one waits DETACHED and out of sync. `AX`
  6.4.14.1 does not say which group takes the Aggregator when members reach
  different partners, so the layer keeps its rule (the partner
  `comparePartner` orders first, `L/lacp.go:80-95`) and the README states it
  as the layer's own.
- The Mux machine is the coupled diagram (`AX` Figure 6-22). Why: a member
  has one `enabled` flag for both directions (`L/layer.go:136`). `AX` 6.4.15
  accepts either diagram and recommends the other.
- A Defaulted member is an Individual link. Why: the Partner administrative
  values are not configurable and stay zero, which `AX` 6.3.6.1 allows only
  for an Individual port, and such a port shares its Aggregator with no
  other (6.4.14.1 h). So `Fallback: true` attaches one Defaulted member, and
  entry 4 is struck.
- `Fallback: false` stays as a departure the README states. `AX` has no such
  switch: a Defaulted port runs on its administrative Partner values
  (6.4.12). The model is `OVS`, where a Defaulted member attaches only under
  `lacp-fallback-ab` (`lib/lacp.c:718-723`), which defaults to false
  (`vswitchd/bridge.c:4562-4563`).
- `UpDelay`, `DownDelay`, and `MinLinks` are outside `AX`, and the README
  says so. The delays are `OVS` bond settings (`ofproto/bond.c:1920`). Under
  LACP an up delay holds a member STANDBY, the state `AX` 6.4.14.1 k gives a
  port that a further constraint keeps from attaching. A down delay does
  not apply under LACP: `port_enabled` follows the carrier, and a link whose
  MAC_Operational is FALSE is removed from its group (6.3.12). That strikes
  entry 1. `MinLinks` gates the step into COLLECTING_DISTRIBUTING, as
  `applyMinLinks` gates `enabled` today (`L/lacp.go:266-275`): members
  attach and signal in sync whatever the count. Why: a gate on attachment
  would leave two ends that both set it waiting for each other.
- When a member gains carrier, the Periodic machine runs before the Receive
  machine. `AX` does not order them. In this order the machine reaches
  SLOW_PERIODIC on the administrative Partner's Long timeout, and Expired's
  Short timeout then takes it through PERIODIC_TX (Figure 6-19), so an
  Active member sends a LACPDU at once, as it does today.
- The default key stays the LAG's position (`L/config.go:189-190,217-219`),
  which settles the question carried from phase 2. A key "is meaningful
  only in the context of the System that allocates it", every value but
  zero is usable, and an operational key may change (`AX` 6.3.5). A change
  costs a detach (6.4.14 NOTE 2), which adding a LAG costs already: one
  retention key covers every LAG (`L/layer.go:832-870`), so `Derive`
  rebuilds the layer (`V/derive.go:136-152`).
- Bond modes, the bucket table, and the rebalance signal stay modelled on
  `OVS`. `AX` 6.2.4 "does not mandate any particular distribution
  algorithm" beyond frame order, so the selection model needs no redesign.
- `C` is in this phase, as `src/common/net/bpdu` is in the spanning tree's.
- These shapes break: `lacp.Decode` accepts frames it refused, `lag.Status`
  gains `PortDisabled`, port numbers are unique in the switch, the member
  subject kind is `lag_member`, `phy.GroupAllocation` carries two
  remainders, and `phy.Diff` drops `resolve_source`.
- The review's `fixes needed` verdict at the three-round cap is answered
  with a fourth round. It resumes from `parked/sim-p4-review`, fixes the
  two tests that cannot fail, and runs the gap pass. (decided by the user,
  2026-10-04)

## Requirements

R1. Every Correctness entry has a test that fails before its fix or is
struck with the clause that makes the current behaviour right.

R2. The LACP Receive, Periodic, Mux, and Selection machines match 802.1AX.
Example: a partner that stops sending LACPDUs moves the member through
Expired to Defaulted on the standard's timers, with the actor state bits the
standard gives each step.

R3. A forced link and an observed link respect the cable's top speed and
report a duplex mismatch. Example: two ends observed at 10 Gb/s over a cable
whose top speed is 100 Mb/s do not resolve at 10 Gb/s.

## Out of scope

- The switch, apart from the Marker dispatch in U4 and the tests of `V`
  that hold old timers. The speed a LAG reports to spanning tree and the
  retention verdict for an absent layer go to U9 (Open questions).
- What Inventory, Limits lists.
- `lacp.Decode` and `lacp.MarkerResponse` read frames that simulated devices
  emit and frames replayed from captures. That input is untrusted for
  shape: a malformed frame is refused and never panics.

## Inventory

Line numbers hold at `a5d2bb97`. Each entry names its unit and the test that
fails before its fix. The `lag` tests go in `L/layer_test.go` unless the
entry names another file.

### Sources

- `AX`: IEEE P802.1AX-REV/D4.54, 15 October 2014, the draft of IEEE Std
  802.1AX-2014 that the IETF publishes as an attachment to IEEE 802.1's
  liaison on completing the revision,
  https://www.ietf.org/lib/dt/documents/LIAISON/liaison-2014-11-08-ieee-8021-rtg-completion-of-8021ax-rev-link-aggregation-to-ietf-routing-area-and-routing-area-wg-attachment-2.pdf.
  An unapproved draft, read for every subclause cited.
- `AX2018`: IEEE P802.1AX-Rev/D1.0, 5 December 2018, a working group ballot
  draft of the revision published in 2020,
  https://1.ieee802.org/wp-content/uploads/2019/03/802-1AX-Rev-d1-0.pdf.
  Read for its introduction only.
- `CAP`: Wireshark's sample capture `lacp1.pcap.gz`, ten identical LACPDUs
  from a device at `00:04:96:1f:50:6a`,
  https://wiki.wireshark.org/uploads/__moin_import__/attachments/SampleCaptures/lacp1.pcap.gz
  (SHA-256 `a295f267a41fde8e21867b1d5c09cd2d4c35c17b02017395f03e04ec3af26725`).
- `WS`: Wireshark's dissectors on `master` as fetched on 2026-10-03,
  https://gitlab.com/wireshark/wireshark/-/raw/master/epan/dissectors/packet-lacp.c
  and `packet-marker.c` beside it.
- `OVS`: Open vSwitch at `73e38c8dfd9ff5f95a92e583780cd996ac11bca7`, the
  commit `L/README.md:98` names, read from
  https://raw.githubusercontent.com/openvswitch/ovs/73e38c8dfd9ff5f95a92e583780cd996ac11bca7/lib/lacp.c
  with `ofproto/bond.c` and `vswitchd/bridge.c` under the same prefix.
- `UNH`: UNH-IOL Clause 28 Auto-Negotiation Management System Test Suite,
  9 September 1999, which cites IEEE Std 802.3, 1998 Edition, 28.2.3.1,
  https://www.iol.unh.edu/sites/default/files/testsuites/ethernet/Management_System_Suite/Clause28_Aneg_System_test_suite_v1.0.pdf.
- Vendored: `spec/mib/ieee/IEEE8021-AX-MIB-202005290000Z.mib` and
  `spec/mib/ietf/POWER-ETHERNET-MIB` (RFC 3621).
- Not read: the published IEEE Std 802.1AX-2014 and 802.1AX-2020, IEEE Std
  802.3 (clauses 28, 33, 57, and 145), and IEEE Std 802.1D-2004. No public
  copy was found. A statement that rests on one is marked unverified.

`CAP`'s first frame, 124 octets, is the fixture of U1:

```
01 80 c2 00 00 02 00 04 96 1f 50 6a 88 09 01 01
01 14 91 f4 00 04 96 1f 50 6a 80 00 00 00 00 12
47 00 00 00 02 14 00 00 00 00 00 00 00 00 00 00
00 00 00 00 3b 00 00 00 03 10 00 02 00 00 00 00
00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00
00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00
00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00
00 00 00 00 00 00 00 00 00 00 00 00
```

It decodes to an Actor with system priority `0x91f4`, system
`00:04:96:1f:50:6a`, key `0x8000`, port priority 0, port `0x0012`, and state
`0x47`, a Partner that is zero but for state `0x3b`, and a collector delay
of 2. Two facts fix the Actor's field positions without the repository's
codec: the frame's source address equals the Actor's system, and `WS` reads
the fields in this order (`packet-lacp.c:229-276`). The Partner's zero
fields locate nothing, so a second fixture sets them (U1).

### Correctness

1. Struck. Carrier loss under LACP disables the member at once and bypasses
   `DownDelay` (`L/layer.go:526-537`, `L/lacp.go:156-165`). `AX` 6.3.12 and
   the PORT_DISABLED state of Figure 6-18 make that right.
   `L/README.md:188-194` promises the delay without the limit. U3 corrects
   it, and `TestCarrierLossIgnoresDownDelayUnderLACP` pins a converged
   member with a 1 second down delay disabled in the call that reports the
   loss.
2. High, U6. `checkObserved` (`P/negotiate.go:225-238`) compares observed
   speeds only. It ignores the cable's top speed and a duplex conflict, and
   the fabric's `observedLink` passes no top speed at all
   (`F/fabric.go:1306-1310`). Tests: R3's example in `P/negotiate_test.go`
   (want `LinkFailed`), a row with observed Full against Half (want
   `duplex-mismatch`), and a row in `F/topology_state_test.go` with both
   ends observed at 10 Gb/s over a 100 Mb/s cable of unstated medium.
3. Medium, U5. `inspectTCPHashInput` (`L/hash.go:62-80`) decodes the payload
   as IP without checking the EtherType. `L/README.md:168-171` says a non-IP
   EtherType stops hashing at layer 2. The fixture hides it: `makeARPFrame`
   (`L/layer_test.go:84-93`, used at `:262`) is all zeros and fails decoding
   by accident. Test: in a new `L/hash_internal_test.go`, an ARP-typed frame
   whose payload is a well-formed IPv4 header with UDP ports reports
   `ipDecoded` false. `makeARPFrame` takes that payload.
4. Struck. LACP fallback enables one member (`L/lacp.go:183-201`), which
   `AX` 6.3.6.1 and 6.4.14.1 h require of a Defaulted member (Decisions).
   `L/README.md:252-254` says it forwards over every member with carrier.
   U3 corrects it, and `TestFallbackAttachesOneMember` pins one enabled
   member of two with carrier, and none with `MinLinks` 2.
5. Medium, U6. `Config.Allocate` reports `MaxNanowatts` for an unknown
   powered device without clamping to the group budget
   (`P/poe.go:349-356,400-406`). Test: a `TestPoeAllocateTruthTable` row
   whose class power exceeds the budget wants the budget.
6. Medium, U6. `Negotiate` returns `SourceNegotiated` when both ends have
   auto-negotiation off (`P/negotiate.go:148-171`). `sourceSetting`
   (`P/ethernet.go:153`) names that case and is unexported. Test: the forced
   rows at `P/negotiate_test.go:182-245` want `SourceSetting`.
7. Low, U5. With LACP off, `updateLag` skips `updateActorInfo`
   (`L/lacp.go:134-154`), so an enabled member never shows Collecting or
   Distributing. `L/README.md:215-216` says it does. Test: a static LAG's
   enabled member has both bits in `PortInfo.Actor.State`.
8. Low, U6. `Ethernet.Resolve` treats an observation of speed 0 as observed
   (`P/ethernet.go:176-178`), against `Negotiate` (`P/negotiate.go:227`).
   Test: a fixed setting with an observation of speed 0 resolves from the
   setting.
9. Low, U6. A disabled PSE port with an unknown device reports
   `PowerUnknown` (`P/poe.go:338-339`), which raises `poe-demand-unknown` on
   the switch (`V/switch.go:734-741`) for a port that delivers nothing.
   Test: a truth-table row wants `PowerDenied` with `ReasonDisabled`.
10. Low, U6. `Config.Validate` rejects a fixed speed when supported speeds
    are unreported (`P/validate.go:74-75`), a case `Negotiate` supports.
    Test: a `TestValidate` row that is valid but for unreported supported
    speeds is accepted.
11. Struck. `activeBackupSelect` fills `Selection.Prior`
    (`L/layer.go:424-429`), against the field's doc comment (`:285-287`).
    The comment is the wrong one: the corpus reads the field as the member
    last active (`src/common/sim/internal/simtest/cases.go:1601`). U5
    corrects the comment.
12. High, U2 and U4. The two rates are swapped. `Receive` arms the receive
    timer from the partner's advertised timeout (`L/layer.go:606-611`), and
    transmission runs at the LAG's own `Fast` (`L/layer.go:205-208,705`).
    `AX` 6.4.12 starts `current_while_timer` from the Actor's LACP_Timeout,
    and 6.4.13 takes the periodic rate from the Partner's. `OVS` agrees
    with the standard (`lib/lacp.c:391-392,641-645`). Tests: R2's example
    with a partner that advertises the other timeout (U2), and a slow LAG
    whose partner advertises Short sends one LACPDU a second (U4).
13. High, U2. EXPIRED lasts three of the member's periods
    (`L/layer.go:665-668`), 90 seconds when slow, and leaves the member
    enabled (`L/lacp.go:236-245`). `AX` Figure 6-18 starts the timer at
    Short_Timeout_Time and clears the Partner's Synchronization, which
    takes the port out of COLLECTING_DISTRIBUTING (Figure 6-22). Test: R2's
    example on a slow LAG.
14. High, U2. A member enters Current at link up with a full timeout
    (`L/layer.go:540-544`). `AX` Figure 6-18 enters EXPIRED from
    PORT_DISABLED, so a port that hears no partner is Defaulted 3 seconds
    after link up. Today a slow LAG takes 180. Test: a slow LAG without a
    partner is Expired with the Expired bit at link up and Defaulted 3
    seconds later.
15. High, U3. Attachment is recomputed in place (`L/lacp.go:226-250`): no
    member detaches when its group changes, and none waits. `AX` 6.4.14
    NOTE 2 requires the detach, and Figure 6-22 holds a selected port in
    WAITING for Aggregate_Wait_Time, 2 seconds (6.4.4). Tests: of two
    members selected one second apart, both attach 2 seconds after the
    second. After a partner's key changes, the next LACPDU has the Actor's
    Synchronization clear.
16. High, U2. The Partner's Synchronization is the received bit alone
    (`L/layer.go:602`, `L/lacp.go:240`), and `pdu.Partner` is never read.
    `recordPDU` (`AX` 6.4.9) sets it only when the received Actor claims
    Synchronization, one end is Active, and either the LACPDU's Partner
    fields match the Actor's port, port priority, system, system priority,
    key, and Aggregation, or the received Actor is Individual. Test: a
    LACPDU that claims Synchronization and echoes a key that is not ours
    leaves the member disabled.
17. Medium, U4. A LACPDU is sent when the Actor's own information changed
    (`L/layer.go:560,621,684`). `update_NTT` (`AX` 6.4.9) also asks for one
    when the Partner's view of the Actor is stale. Test: a LACPDU with a
    stale echo is answered at once.
18. Medium, U4. Nothing limits transmission (`L/layer.go:432-452`). `AX`
    6.4.16 allows three LACPDUs in any Fast_Periodic_Time and delays the
    next. Test: of four state changes inside one second, the fourth LACPDU
    waits and carries the latest state.
19. Medium, U2 and U3. Carrier loss discards the Partner's information
    (`L/layer.go:527-532`). PORT_DISABLED keeps it and the selection, so a
    port that returns to the same partner causes no reconfiguration (`AX`
    6.4.12). With `Fallback`, a member without carrier can be the chosen
    one while a down delay runs (`L/lacp.go:185-190` reads `linkUp`). Tests:
    `PortInfo.Partner` survives carrier loss (U2), and the fallback case of
    entry 1's test (U3).
20. Medium, U3. Two members of a LAG cabled to each other aggregate
    (`L/lacp.go:234-250` has no check). `AX` 6.4.14.1 g forbids it. Test:
    at most one of the two is enabled.
21. Medium, U3. A partner that advertises Individual shares the Aggregator
    (`L/lacp.go:236-238` ignores the Aggregation bit), against `AX`
    6.4.14.1 h. Test: of two members with such a partner, one attaches.
22. Medium, U3. Members join on the partner's system address and key
    alone (`L/lacp.go:237`). The System Identifier includes the System
    Priority (`AX` 6.3.2), and a port selects only an Aggregator with its
    own operational key (6.4.14.1 e), which a member whose `Member.Key`
    differs from the LAG's does not have. Tests: one for each half.
23. Medium, U2. Port numbers restart at 1 in each LAG (`L/layer.go:215`).
    `AX` 6.3.4 requires them unique within a System. Test: no two members
    of two LAGs share one.
24. Medium, U2. A Defaulted partner is recorded as state `0x40`, the
    Defaulted bit (`L/layer.go:220,529,542,672`), which is the Actor's bit.
    `recordDefault` (`AX` 6.4.9) records the administrative state with
    Synchronization TRUE, and 6.4.7 sets its Collecting the same. Test: a
    Defaulted member's LACPDU carries Partner state `0x18`.
25. High, U1. `lacp.Decode` refuses a Version other than 1, a TLV type it
    does not expect, and a terminator that is not at octet 58
    (`C/lacp.go:127-193`). `AX` 6.4.12: the Receive machine "shall not
    validate the Version Number, TLV_type, or Reserved fields". A Version 2
    LACPDU, which holds further TLVs at octet 58 (6.4.2.3 y), is refused.
    Test: the fixture with Version 2, with Actor type `0x07`, and with a
    Port Algorithm TLV (`0x04`, length 6) at octet 58 decodes unchanged.
26. High, U4. A Marker PDU gets no response: the switch intercepts Slow
    Protocols subtype 1 only (`V/switch.go:1133`). `AX` 5.3 d and 6.5.4.2
    require the Marker Responder. Tests: the layer answers on a member that
    is not collecting, and in `V/switch_test.go` the Marker PDU of U1
    entering a member port puts its response among the switch's emissions.
27. Medium, U2. Nothing notices a cable moved between members. `AX` 6.4.8
    sets `port_moved` when a PortDisabled member's Partner system and port
    are heard on another member, and Figure 6-18 then returns the first to
    its administrative Partner. Test: after such a move the first member's
    `PortInfo.Partner` is the default.

### Completeness

- U6. `phy` has no README. Its sibling layers each have one.
- U6. `GroupAllocation` exposes one remainder (`P/poe.go:270-274`) where
  the allocation computes a minimum and a maximum (`:324-325`).
- U1. The codec's only layout evidence is a round trip
  (`C/lacp_test.go:13`). Parent R7 asks for bytes from a second source,
  which `CAP` supplies.
- U1. `C/README.md:132` cites "IEEE 802.1AX-2008 clause 6.4.2". That
  edition was not read, and 6.4.2 is the 2014 numbering (`AX`).
- U5. The virtual-device record still says LACP runs "as Open vSwitch runs
  it, without the marker protocol"
  (`docs/architecture/2026-09-10-virtual-device-direction.md:929-931`).

### Design

- U6. `phy.Diff` reports `resolve_source`, a derived value, beside the
  settings that produce it (`P/diff.go:208-216`).
- U6. `Normalize` defaults `Duplex` only when auto-negotiation is off
  (`P/phy.go:56-60`), so an unset duplex differs from an explicit `Unknown`.
- U5. A member change is a subject of kind `port` whose key is not a port
  name (`L/diff.go:327,365`, held by `L/config_test.go:310`). Sibling layers
  name such a subject after both parts (`pvst_tree_port`).
- Carried to U9. The switch reports a LAG's speed to spanning tree as the
  fastest enabled member (`V/switch.go:3261-3270`). Open questions has the
  evidence.

### Tests

- `TestDelays` (`L/layer_test.go:332`) and `TestMinLinksDisablesAll`
  (`:758`) run with LACP off only.
- `TestFallback` (`L/layer_test.go:503`) brings two members up and checks
  the one `Select` returns, never that the other stays disabled. It and
  `TestExpiredHoldsForThreePeriods` (`:737`) pin entry 14: Expired at 3
  seconds after link up and Defaulted at 6.
- `L/layer_test.go:605-607,790-792` require a LACPDU at carrier up, which
  the Periodic decision keeps.
- `TestDecodeRefusals` (`C/lacp_test.go:101`) pins entry 25.
- No `Negotiate` case has an observed speed above the cable's top speed
  (`P/negotiate_test.go:427-470`).
- `TestPoeAllocateTruthTable` (`P/phy_test.go:778`) has no port whose
  maximum exceeds the group budget.
- `F/lag_test.go:190,587,709,766,871` and
  `src/common/sim/netmodel/lacp_test.go:299,400` hold convergence times.

### Limits

`L/README.md` states each with its clause, and `P/README.md` the last two.

- No Marker Generator or Receiver (`AX` 6.5.4.1), no churn detection
  (6.4.17), no Version 2 TLVs or conversation-sensitive distribution (6.6),
  and no Distributed Resilient Network Interconnect (clause 9).
- A conversation moved off a detached link is sent on its new link at once.
  `AX` 6.3.14 requires its frame order preserved, by the Marker protocol or
  other means. The layer sees no frame in transit, so it does nothing.
- Every member link is taken as point-to-point, so LACP_DISABLED (`AX`
  6.4.8, 6.7.3) is never entered.
- Timers run at their nominal values. `AX` 6.4.4 allows 250 ms either way.
- Load-driven rebalancing is reported and not performed. `AX` Annex B.3
  describes it as one option of an annex its heading calls informative.
- Parallel detection covers 10 and 100 Mb/s and resolves the detecting end
  at half duplex (`UNH`, Group 3). A faster forced end against an
  auto-negotiating one stays unsupported.
- The class power table (`P/poe.go:197-202`) is unverified against IEEE Std
  802.3.

## Units

### U1. LACPDU and Marker codec
Files: src/common/net/lacp/
After: none
Change: `Decode` reads the three TLVs at their Version 1 offsets and nothing
from payload octet 58 on. It still refuses a wrong EtherType, a payload
under 110 octets, a subtype other than 1, and a TLV length other than 20,
20, and 16. `SubtypeLACP` and `SubtypeMarker` name the subtypes.
`MarkerResponse(f, src)` returns a Marker PDU (subtype 2, TLV type `0x01`,
length 16, 110 octets or more) with the TLV type changed to `0x02`, the
group address as destination, `src` as source, and every other octet as
received (`AX` 6.5.3.3, Figure 6-28). It reads no other field and refuses
any other frame. `README.md` cites `AX` 6.4.2 and 6.5.3 and names `CAP`.
Tests: in `lacp_test.go`, the fixture under Sources decodes to the values
given there and those values encode to its octets. A second fixture with
distinct Partner values at the offsets `WS` reads (`packet-lacp.c:282-318`)
does the same. Entry 25's rows change one field of the fixture each.
`TestDecodeRefusals` keeps one row per remaining refusal, the fixture with
only the refused octet changed
(`docs/solutions/conventions/a-refusal-test-needs-an-input-only-the-refusal-rejects.md`).
In `marker_test.go`, a Marker PDU built from `AX` Figure 6-27 (Version 2,
port `0x0012`, system `00:04:96:1f:50:6a`, transaction `0x01020304`,
non-zero pad and reserved octets) is compared with its response octet by
octet. `WS` reads the same offsets (`packet-marker.c:78-141`). No capture
of a Marker PDU was found, so nothing here catches a misreading that `AX`
and `WS` share.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/lacp`

### U2. Receive machine and identification
Files: src/common/sim/layer/lag/, src/common/sim/fabric/lag_test.go, src/common/sim/device/vswitch/, src/common/sim/netmodel/lacp_test.go
After: none
Change: a member's receive state is `PortDisabled`, `Expired`, `Defaulted`,
or `Current`, moved as `AX` Figure 6-18 moves it with the timers of 6.4.4
and the functions of 6.4.9, `port_moved` included. The Actor's timeout is
Short when the LAG is `Fast`. The administrative Partner is zero and
Individual with Synchronization and Collecting set. A member starts in
PortDisabled with it, and carrier loss returns there. `update_Selected` and
`update_Default_Selected` record that the member must reselect, which U3
acts on. Until then `updateLag` treats PortDisabled as it treats Defaulted
and a member holding the administrative Partner as it treats
`partnerDefaulted`, so a member is enabled only while its Partner is in
sync. A member's port number is its 1-based position among all member
ports of the layer, by name. `README.md` describes the machine with its
clauses.
Tests: R2's example at both rates: the member is Current with
Synchronization, Collecting, and Distributing until 3 seconds (`Fast`) or
90 after the last LACPDU, then Expired for 3 seconds with the Expired bit
and without Collecting and Distributing, then Defaulted with the Defaulted
bit and without Expired. The tests of entries 12 to 14, 16, 19, 23, 24, and
27. `TestFallback`, `TestExpiredHoldsForThreePeriods`, and the dependent
tests that held the old timers take the standard's.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/lag src/common/sim/fabric src/common/sim/device/vswitch src/common/sim/netmodel`

### U3. Selection Logic and Mux machine
Files: src/common/sim/layer/lag/, src/common/sim/fabric/lag_test.go, src/common/sim/device/vswitch/, src/common/sim/netmodel/lacp_test.go
After: U2
Change: each member carries `Selected` and a Mux state, moved as `AX`
6.4.14.1 and Figure 6-22 move them, with a 2 second wait in WAITING that
members waiting for the Aggregator end together. Selection gives the LAG's
Aggregator to one group. A candidate is a member with carrier whose own key
is the LAG's. The lead is the candidate whose Partner came from a LACPDU
(Current, or Expired after Current) and that `comparePartner` orders first.
Its group is every candidate whose Partner has the lead's System Identifier
and key with Aggregation set. A lead Partner that is Individual takes the
Aggregator alone. With no lead and `Fallback`, the group is one Defaulted
candidate, the Primary or else the lowest name. A member without carrier
keeps its selection while its Partner belongs to the group. Of two members
cabled to each other, only the lower name is a candidate. A running up
delay holds STANDBY, and `MinLinks` gates COLLECTING_DISTRIBUTING
(Decisions). `Info.Attached` and the Actor's Synchronization follow
ATTACHED. `Info.Enabled`, Collecting, and Distributing follow
COLLECTING_DISTRIBUTING. A member in WAITING reports the pending cause
`aggregate-wait`, and `NextWake` reports its timer. With LACP off, nothing
changes. `README.md` describes both, the delays under LACP, and fallback.
Tests: the tests of entries 1, 4, 15, and 19 to 22. A member that gains
carrier beside an attached group leaves the group enabled. Two layers that
both set `MinLinks` 2 converge, and none of their members is enabled while
one link's partner is out of sync. `TestUpDelayDoesNotHoldTheProtocol` also
requires the Actor's Synchronization clear while the delay runs. The
dependent tests take the 2 second wait.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/lag src/common/sim/fabric src/common/sim/device/vswitch src/common/sim/netmodel`

### U4. Periodic and Transmit machines, Marker Responder
Files: src/common/sim/layer/lag/, src/common/sim/fabric/lag_test.go, src/common/sim/device/vswitch/, src/common/sim/netmodel/lacp_test.go
After: U1, U3
Change: the Periodic machine is `AX` Figure 6-19: it runs while the member
has carrier and the Actor or the Partner is Active, every second when the
Partner's timeout is Short and every 30 otherwise, and without it nothing
is sent. At carrier up it runs before the Receive machine (Decisions). A
member needs to transmit when a machine of U2 or U3 says so, when the
Periodic machine does, and when `update_NTT` finds the Partner's view
stale. A fourth LACPDU inside one second waits for the window and carries
the state of the moment it is sent (`AX` 6.4.16), and `NextWake` reports
it. `ReceiveMarker(now, member, f)` returns the frame `lacp.MarkerResponse`
builds, from the address the member's LACPDUs use, whatever the member's
Mux state (`AX` 6.5.1). The switch intercepts subtype 2 beside subtype 1 at
`V/switch.go:1133`, calls it, and consumes the frame under the new rule
`lag.marker.respond`. A frame `MarkerResponse` refuses is dropped as an
unsupported LACPDU is today. `AX` Figure 6-28 writes `SA` for the
response's source without saying whose address it is, and nothing in this
unit settles that.
Tests: the tests of entries 12 (periodic half), 17, 18, and 26.
`TestPassive` and the carrier-up assertions at `L/layer_test.go:605-607,790-792`
keep passing.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/lag src/common/sim/fabric src/common/sim/device/vswitch src/common/sim/netmodel`

### U5. Distribution, reporting, and the layer's documents
Files: src/common/sim/layer/lag/, docs/architecture/2026-09-10-virtual-device-direction.md
After: U4
Change: `inspectTCPHashInput` decodes the payload only under the IPv4 and
IPv6 EtherTypes. With LACP off, `updateLag` refreshes each member's Actor
information. The doc comment of `Selection.Prior` says what the field holds
for active-backup. A member change in `Diff` has subject kind `lag_member`
with the same key. `README.md` names `AX` with the clauses each section
follows, `OVS` with its files for what `AX` leaves open, the Limits, and
the default key with `AX` 6.3.5. In the virtual-device record, the Protocol
depth gap is rewritten and a dated amendment says why: LACP follows IEEE
Std 802.1AX-2014 as read from `AX`, as a Version 1 implementation with the
Marker Responder, and the gaps are the Limits.
Tests: the tests of entries 3 and 7. `TestDiff` (`config_test.go:268`) and
`diff_injectivity_test.go` take the new kind. `check-prose.py` passes on
the README and the record.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/lag docs/architecture/2026-09-10-virtual-device-direction.md`

### U6. Physical layer bounds
Files: src/common/sim/layer/phy/, src/common/sim/fabric/fabric.go, src/common/sim/fabric/fingerprint.go, src/common/sim/fabric/fingerprint_test.go, src/common/sim/fabric/topology_state_test.go, src/common/sim/fabric/journey_metadata_test.go, src/common/sim/netmodel/testdata_icx7150_test.go, src/common/sim/internal/simtest/cases.go
After: none
Change: `checkObserved` takes the cable's top speed, and `observedLink`
passes the cable's. Equal observed speeds above it fail with
`speed-mismatch`, as a forced speed above it does, and two stated observed
duplexes that differ resolve with `duplex-mismatch`. Two forced ends
resolve with `SourceSetting`, exported again. `Ethernet.Resolve` treats an
observation of speed 0 as absent. `Validate` refuses a fixed speed only
when supported speeds are reported and lack it. `Normalize` writes
`Unknown` for an empty duplex in every `Setting` and `Observed`. `Allocate`
clamps an unknown device's `MaxNanowatts` to the group's maximum remainder.
A disabled port with an unknown device is `PowerDenied` with
`ReasonDisabled`. `GroupAllocation` carries `RemainderMinNanowatts` and
`RemainderMaxNanowatts`, and the fabric's fingerprint writes both. `Diff`
drops `resolve_source`. A new `README.md` gives the two truth tables, a
runnable example, `UNH` for parallel detection, RFC 3621 for the priority
and group vocabulary, and its two Limits.
Tests: the tests of entries 2, 5, 6, and 8 to 10. Entry 5's row also wants
two remainders that differ. A `TestNormalize` row covers the duplex.
`TestDiff` drops its `resolve_source` expectations (`P/phy_test.go:500,513`).
The dependent files take the new shapes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/phy src/common/sim/fabric src/common/sim/netmodel src/common/sim/internal/simtest`

Waves: U1 U2 U6 | U3 | U4 | U5

## Verification

Per unit, the verifier on its changed paths, never `--full`. For the phase:

```bash
go build ./src/common/net/lacp/... ./src/common/sim/...
go test -race ./src/common/net/lacp/... ./src/common/sim/... ./test/conformance/sim/...
```

## Definition of done

- [x] Verifier green for every changed path of every unit, with the contract
      gate in `test/conformance/sim` untouched.
- [x] Each Correctness entry has its failing-first test or its strike.
- [x] The READMEs of `L`, `P`, and `C` name their sources, clauses, and
      Limits, and the virtual-device record is amended.
- [x] No plan label in code, comments, or commit messages. This plan's
      `status` is set, and the parent's U4 `Landed:` holds the range.

## Open questions

- Unverified: that the published IEEE Std 802.1AX-2014 has the text and
  clause numbers of `AX` (the READMEs cite `AX` and say so), the class
  power table against IEEE Std 802.3, and whose address `SA` is in `AX`
  Figure 6-28 (U4).
- Carried to U9: retention reports `Kept: true` for a LAG layer absent on
  both sides, because both keys are empty (`V/derive.go:136-146`). Whether
  that verdict is right is the switch's contract.
- Carried to U9: the speed a LAG reports to spanning tree. `AX` 7.3.1.1.16
  defines the aggregate's data rate as "the sum of the data rate of each
  link in the aggregation", and the switch reports the fastest member
  (`V/switch.go:3261-3270`). Which one the path cost table of IEEE Std
  802.1D takes is unverified, since that text was not read.
- Carried to U11: whether `lacpv1.LacpStatus` gains a value for
  `PortDisabled`. Until then `netmodel/export.go:529-538` maps it to the
  unspecified value.

## Review gaps

- docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase4-plan.md:559: review commits b6cde757 and b3e894c4 use phase and U1 labels; fails: commit messages must name the change without plan labels.
- src/common/sim/device/vswitch/switch.go:2479: record a Marker decode fact for every refused Slow Protocols frame; fails: a refused LACPDU must carry an invalid LACP decode input on Peek and Forward.
- src/common/sim/layer/lag/layer.go:675: change the defaulting guard to `m.enabled || !sameAggregationPort(...)`; fails: a Fallback member whose learned Partner equals the administrative values, advanced in one call from enabled past both receive timeouts, must stay attached and enabled and never enter WAITING.
- src/common/sim/layer/lag/layer_test.go:2004: the test never asserts that 1/1/1 forwards as the fallback choice before the Primary gains carrier, and its comment says the Primary returns; fails: the backup state must be asserted before the outcome and the comment must say the Primary first gains carrier.
- src/common/sim/layer/lag/layer_test.go:2384: the test crosses both receive timeouts in one call, asserts only empty lists, and says "want no selected member" for a member that is selected and waiting; fails: it must assert the attachment before the timeouts, the Expired step, and the reselected member's attachment after the aggregate wait.
- src/common/sim/layer/lag/layer_test.go:1875: the comment says reselection becomes eligible only after the aggregate wait; fails: selection is redone in the Receive call and attachment is what waits.
- src/common/sim/layer/lag/README.md:379: the Mux diagram's only exit from DETACHED is WAITING; fails: a member that kept its selection across carrier loss goes from DETACHED to STANDBY or ATTACHED when carrier returns.
- src/common/sim/layer/lag/README.md:406: Primary is said to win with carrier and Defaulted alone; fails: the sentence must also require the LAG's key.
- src/common/sim/layer/lag/layer.go:43: "records when:" is followed by the four causes; fails: the comment must say the entry names the cause and the time.
