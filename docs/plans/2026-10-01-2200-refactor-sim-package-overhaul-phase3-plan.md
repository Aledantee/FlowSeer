---
title: Spanning Tree to Standard - Plan
type: fix
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
parent: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-plan.md
---

# Spanning Tree to Standard - Plan

> **Implemented.** 7 units, 2026-10-03T19:45:04Z to 2026-10-04T19:03:28Z.

## Goal

`layer/stp` follows IEEE 802.1D and IEEE 802.1Q for RSTP, MSTP, and
interoperation with legacy STP, and keeps its PVST and SSTP behaviour
consistent with them. The means is seven units: a file split, one owner for
link state, one unit per state machine the inventory touches, and one that
replaces the transmit path with the standard's Port Transmit machine. Stop
condition: if a per-tree machine in U4 or U5 needs a link fact that the
per-port record of U2 cannot give it without a second copy, the ownership
decision below is wrong and the phase is re-planned from U2.

## Decisions

`S` is `src/common/sim/layer/stp`, `B` is `src/common/net/bpdu`, and other
short paths are relative to `src/common/sim`. Clause labels are defined
under Inventory, Sources.

- The parent's Decisions apply. Protocols are implemented to their standards.
- The reference is IEEE Std 802.1Q-2011, clauses 13 and 14. Why:
  `S/README.md:98,312` already cites it through `UNH`, and `UNH` tests
  RSTP against it, so its clause 13 holds both protocols. Its text was not
  read. Each claim cites what it was read from: `Q2003` for clause 14 and
  wherever `D2009` agrees, `D2009` and `UNH` where the later text differs,
  which wins (Inventory, Sources). `S/README.md` names the edition and
  these sources, and marks IEEE 802.1D-2004 as not read.
- PVST and SSTP have no IEEE standard. `S/README.md` marks their behaviour
  as modelled on observation and names the Cisco documents it links.
- Link state has one owner per port and tree state one owner per tree.
  Why: the link flags are copied into every tree
  (`S/layer.go:140-145,1204`) and repaired after the fact (`:1208-1213`),
  which is the cause of Correctness 2, 3, and 11. `Q2003` 13.24 and 13.21
  draw the same line: `operEdge`, `sendRSTP`, `tcAck`, `txCount`,
  `infoInternal`, `mdelayWhile`, and `helloWhen` have one instance per port,
  and `role`, `agreed`, `proposing`, `forward`, `fdWhile`, `tcWhile`, and
  `rcvdInfoWhile` have one per port for each tree.
- Transitions are computed before any frame is built. Why: Correctness 13,
  and `Q2003` Figure 13-13 qualifies every transmit transition by
  `selected && !updtInfo`.
- A topology change is detected when a non-edge Root or Designated port
  starts forwarding, and at no other time. Why: `Q2003` 13.17 says MSTP
  "only detects topology changes following a change of Port Role to Root
  Port, Master Port, or Designated Port", its Figure 13-19 enters DETECTED on
  `forward && !operEdge` alone, and `D2009` 13.19 says a notification "is
  sent when a Bridge Port joins the active topology, and not before".
- The units form a chain and the plan stays whole. Why: one cluster. Every
  unit edits `S/README.md` in the change that invalidates it, and U2
  through U6 all edit the receive and transmit paths. U1 moves code and
  changes nothing else, so its review is a diff of file names.
- What the layer keeps short of the standard is stated in `S/README.md`
  with its reason, as the parent allows (Inventory, Limits).
- `Config.Validate` gains no refusal in this phase, so a port priority that
  is not a multiple of 16 still loads, and the README says the layer sends
  its high nibble. Why: `netmodel` passes collected priorities through
  (`netmodel/netmodel.go:1446`) and promises to turn bad input into issues,
  so a refusal needs a guard in U11's package. `Q2003` 13.24.21 makes the
  priority the four most significant bits of the Port Identifier.
- These shapes break: `bpdu.MaxMSTIRecords` is 64, `bpdu.Decode` accepts
  frames it refused, `PortInfo.MSTID` goes, and the BPDU decision fact grows.
- R3's example is a port that starts forwarding, not one that goes down,
  since the sources above raise no topology change when a port leaves
  Forwarding. (decided by the user, 2026-10-03)
- The review's `rework` verdict after three fix rounds is answered with a
  fourth round. It resumes from `parked/sim-p3-review` and is limited to
  the seven findings that verdict lists. (decided by the user, 2026-10-04)
- The fourth round's `rework` verdict is answered with a re-plan. The
  topology-change emission becomes one new unit, specified from the
  standard's state machines, then implemented and reviewed from
  `parked/sim-p3-review`. (decided by the user, 2026-10-04)
- U7 replaces how a transmission is requested and built, and when the
  Topology Change machine requests one. A role transition keeps the request
  it makes today. Why: the re-plan above is scoped to the topology-change
  emission, and both open behavior findings under Review gaps have one
  cause, a frame whose kind and content are fixed where it is requested
  (`S/transmit.go:20-44`, `S/topology.go:144-189`). `D2009` 13.29.29 builds
  a BPDU from the port's variables when it is sent.
- The Port Transmit and Topology Change machines follow `D2009` Figures
  13-19 and 13-28 where they differ from `Q2003` Figures 13-13 and 13-19,
  by the rule under Inventory, Sources. Five differences reach U7.
  `newTcWhile` requests a frame on a port that sends RSTP (`D2009`
  13.29.11), where `Q2003` 13.26.6 only sets the timer. DETECTED sets
  `newInfoXst`. `helloWhen` restarts on every entry to IDLE, where `Q2003`
  restarts it in TRANSMIT_PERIODIC alone. A port that sends RSTP transmits
  whatever its role, where `Q2003` asks for a Root or Designated role.
  TRANSMIT_INIT is entered while the port is not enabled and sets both
  flags, where `Q2003` enters it on BEGIN and clears them. `UNH`
  RSTP.op.4.5 Part A supports the first and third: it expects exactly two
  flagged RST BPDUs, which a hello on its own phase turns into three when
  the change arrives under a second before it.
- Under PVST each VLAN's tree keeps its own transmit record for each port.
  Why: PVST has no IEEE standard, each VLAN runs the RSTP machine
  (`S/README.md:188`), and the budget is already metered per tree
  (`S/README.md:209`).
- `Effects.Emissions` lists frames by tree, then by port. Why: the standard
  orders nothing between ports, and the layer's walks are already fixed as
  `treeOrder` and `portNames` (`S/layer.go:44-62`).

## Requirements

R1. Every Correctness entry below has a test that fails before its fix.
Example: a PVST Designated port that earned an agreement, then loses and
regains link, stays Discarding until the new peer agrees.

R2. MSTI ports run proposal and agreement. Example: two bridges in one
region on a point-to-point link bring an MSTI Designated port to Forwarding
in the same exchange that brings the CIST there.

R3. A topology change reaches the root. Example: a settled non-root bridge
whose non-edge downstream Designated port starts forwarding transmits a
BPDU with the topology-change flag on its Root port within one hello time.

R4. Legacy STP interoperation acknowledges a TCN and uses the legacy
topology-change timer. Example: a TCN BPDU received on a Designated port is
answered by a Configuration BPDU with the acknowledgment flag set.

R5. BPDU encoding is checked against bytes from a second source, including
an MST BPDU with at least one MSTI record whose bridge and port priority are
not the defaults.

R6. A port transmits at most once in a call, and under PVST once for each
tree, after every tree has finished its transitions. The frame says what
the port is at that instant.
Example: an MST bridge holds p1 as Root for the CIST and for MSTI 1, and p2
as Designated and Discarding for the CIST and Alternate for MSTI 1. A BPDU
on p1 names a better CIST root and an MSTI 1 record worse than the one p2
holds, so p2 becomes Root and Forwarding for MSTI 1 and p1 Alternate. p2
emits one frame in that call. It names the Designated role with a proposal
for the CIST, and the Root role with the topology-change flag for MSTI 1.

R7. A topology change requests a frame at once on every port that sends
RSTP and whose timer it starts, and every frame the port sends while the
timer runs carries the flag. With a count below the limit and no other
request, that is two flagged frames. Example: a settled non-root bridge
with stopped timers has p1 Root and p2 and p3 Designated, all Forwarding
and none an edge. An RST BPDU with the Root role and the flag arrives on p3
half a second before p2's hello is due. p1 and p2 each emit one flagged
frame in that call and one more a HelloTime later. No frame after those
carries the flag, and p3 never sends one that does.

R8. A transmission held at the hold count is a request, and its frame is
built when it is released. Example: with a hold count of 1, an MST port
that is Designated for the CIST and Root for MSTI 1 has spent its count when
a change on MSTI 1 reaches it. When the count falls, with no hello due, it
emits one frame whose MSTI 1 record carries the flag.

R9. A port that does not send RSTP reports a change that reaches it at its
hello. Example: a Root port has migrated to STP and receives a
Configuration BPDU every two seconds, and a flagged RST BPDU arrives on
another port. The Root port emits nothing in that call, a TCN BPDU at its
next hello, and one at each hello until 35 seconds have passed at default
timers.

## Out of scope

- The switch recording an MSTI transition on a BPDU. U9 owns it
  (`device/vswitch/switch.go:2599,2633`), and `VLANPortInfo` gives the view.
- Loading MSTP or PVST from the network model. U11 owns it.
- What Inventory, Limits lists, and the L2GP and SPT machines.
- `bpdu.Decode` reads frames that simulated bridges emit and frames replayed
  from captures. That input is untrusted for shape: a malformed frame is
  refused or decoded as `Q2003` 14.4 says, and never panics.

## Inventory

Line numbers hold at `a51692c5`. U1 moves the code, so a later unit finds
its entry by the function named. Each entry names its unit and the test
that fails before its fix.

### Sources

- `Q2003`: IEEE Std 802.1Q, 2003 Edition (incorporates IEEE Std 802.1s-2002),
  clauses 13 and 14, https://bittwist.sourceforge.io/doc/802.1Q-2003.pdf.
  The published standard, read for every subclause cited.
- `D2009`: P802.1aq/D1.5 with suggested changes, 12 May 2009, clause 13 with
  RSTP and MSTP merged,
  https://www.ieee802.org/1/files/public/docs2009/aq-seaman-merged-spanning-tree-protocols-0509.pdf.
  An unapproved working draft. Its subclause numbers are its own: it puts
  the timers in 13.25 where `UNH` cites 13.23 of the published text.
- `UNH`: UNH-IOL Rapid Spanning Tree Conformance Test Suite, which cites
  IEEE Std 802.1Q-2011 per test,
  https://www.iol.unh.edu/sites/default/files/testsuites/bfc/RSTP_conformance_Q.pdf.
- `WS`: Wireshark's BPDU dissector, `epan/dissectors/packet-bpdu.c` on
  `master`,
  https://gitlab.com/wireshark/wireshark/-/raw/master/epan/dissectors/packet-bpdu.c.
  It reads the offsets of `Q2003` Figures 14-1 and 14-2 (`:33-57`), which
  makes that octet table R5's second source. No device capture was found.
- `CISCO`: Cisco's loop guard document, which `S/README.md` already links,
  https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol-stp-8021d/218321-configure-stp-with-loop-guard-and-bpdu-s.html.
- Not read: IEEE Std 802.1D-2004 and the published IEEE Std 802.1Q-2011.
  The IEEE GET program needs an account, and no public copy was found. A
  statement that rests on either is marked unverified.

`Q2003` and the later text differ in four places, and the later text
wins each. `newTcWhile` is HelloTime plus one second (`D2009` 13.29.11,
`UNH` RSTP.op.4.5), not twice HelloTime (`Q2003` 13.26.6). Received
information lives three hello times (`D2009` 13.29.32) without the Max Age
bound of `Q2003` 13.26.23. Hello Time is fixed at 2 seconds (`D2009` Table
13-5, `UNH` RSTP.op.4.3), not managed per port (`Q2003` 13.22 e). A
proposal is recorded from a Designated sender and left unchanged otherwise
(`D2009` 13.29.20), where `Q2003` 13.26.13 tests for a point-to-point link
and clears the flag otherwise.

### Limits

`S/README.md` states each with its reason. None gets a fix in this phase.

- No `disputed` flag (`D2009` 13.29.17) and no Master role
  (`S/README.md:363`).
- Hello Time stays configurable where the later text fixes it.
- The forward-delay ladder steps by Forward Delay on every port. `D2009`
  13.28.8 steps an RSTP port by HelloTime, and `UNH` RSTP.op.4.2 expects a
  port that got no agreement to pass no traffic 20 seconds after it comes
  up. The sources disagree, so the ladder is left alone.
- A port that loses auto-edge returns to Discarding and proposes again, on
  every tree after U2. It is the layer's rule for the CIST
  (`S/layer.go:2037-2044`) and stands in for `disputed`. `D2009` Figure
  13-16 (Port Receive) only clears `operEdge`.
- A port priority that is not a multiple of 16 loads, and the layer sends
  its high nibble (Decisions).

### Correctness

1. High. MSTI bridge priority is sent in the wrong nibble. `gatherMSTIRecords`
   (`S/layer.go:1324`) shifts the priority right by 12 and `receiveMSTIs`
   (`:1367`) shifts the octet back, so `0x8000` goes out as `0x08`. The codec
   carries the octet as given (`B/bpdu.go:505,747`). `Q2003` 14.6.1 d): bits
   5 through 8 of octet 14 hold the priority, bits 1 through 4 are sent as 0
   and ignored on receipt. `WS` agrees (`packet-bpdu.c:846`). U3. Test: a
   layer with instance priority `0x4000` emits a frame whose first record
   has `0x40` at octet 14, read from the payload with no call to `Decode`.
2. High. An agreement survives a link bounce on a non-CIST tree.
   `syncInstancePorts` (`S/layer.go:1208-1213`) clears role, state, and
   received information and leaves `agreed`, `proposing`, and
   `fwdDelayTimer`. The CIST clears them (`:1860-1863`). The port forwards at
   link up through `:1779`. `Q2003` Figure 13-14, state DISABLED, clears
   `proposing`, `proposed`, `agree`, and `agreed` per tree per port. U2.
   Test: the R1 example on PVST VLAN 10, and the same on an MSTI.
3. High. Losing auto-edge leaves other trees Forwarding. `receiveLink`
   (`S/layer.go:2037-2044`) returns the CIST to Discarding, `:2051` copies
   only link flags, and `:1786` keeps a Designated port that is already
   Forwarding. Judged against the layer's own rule (Limits). U2. Test:
   an MST port that auto-edge opened receives a BPDU, and `VLANPortInfo`
   shows the MSTI's port Discarding.
4. High. Any agreement opens a Designated port. `applyBPDU`
   (`S/layer.go:2363`) checks neither point-to-point status nor the sender's
   role and vector. `Q2003` 13.26.9: `agreed` is set for a message on a
   point-to-point link with the Agreement flag that conveys a Root Port role
   with priority the same as or worse than the port's, or a Designated Port
   role the same or better, and is cleared otherwise. `D2009` 13.29.16 adds
   `rstpVersion`. U4. Test: four agreements that leave a Designated port
   Discarding, each one property away from one that opens it: a shared
   link, a port in STP mode, a Designated sender with a worse vector, a Root
   sender with a better one.
5. High. A topology change is not sent toward the root, and its flag belongs
   to the tree. `raiseTopologyChange` (`S/layer.go:1120-1133`) sets one timer
   per tree (`S/tree.go:60`), the hello loop (`:2493-2498`) sends on
   Designated ports only, and `makeBPDU` (`:1443`) sets the flag from the
   tree's timer, so the port a change arrived on sends it back. `Q2003`
   13.21 keeps `tcWhile` per port per tree, 13.26.20 propagates to every port
   except the one that invoked it, 13.26.22 sets the flag "if (tcWhile != 0)
   for the Port", and Figure 13-13 TRANSMIT_PERIODIC transmits on a Root port
   while its `tcWhile` runs. `UNH` RSTP.op.4.5 Part A expects two RST BPDUs
   with the flag from the Root port after a change is notified. U5. Test: a
   non-root bridge whose Designated port reaches Forwarding emits, in that
   call, a BPDU on its Root port with the flag set, sets it on that port's
   hellos while the timer runs, and on none once HelloTime plus one second
   has passed. The port a flagged BPDU arrived on sends none back.
6. High. Loop guard watches the CIST only (`S/layer.go:2549`). A port that
   is Root for a PVST VLAN other than 1 becomes Designated when that tree's
   information expires. `CISCO` says the guard blocks inconsistent ports per
   VLAN under per-VLAN spanning tree, and `S/README.md:127` promises the
   outcome on every tree. Modelled on observation. U6. Test: a PVST port
   that is Root for VLAN 10 with loop guard stays Discarding with the block
   reason set after that tree's information expires, while VLAN 1 forwards.
7. High. A BPDU that is not stored rewrites how the stored vector is read.
   `applyBPDU` sets `external` before it decides whether to keep the BPDU
   (`S/layer.go:2265-2267,2281-2283,2328-2330`), and `candidateVector`
   (`:1494-1504`) then reads fields the kept vector never set. `Q2003`
   13.24.10 ties `infoInternal` to the information the port holds, and
   Figure 13-14 assigns it only in SUPERIOR_DESIGNATED and
   REPEATED_DESIGNATED. U6. Test: an MST port holding internal information
   receives an inferior RST BPDU, and `Root` and the port's role are
   unchanged.
8. High. Two priorities share a retention key. `InstancePort.Canonical`
   (`S/mst.go:41-45`) omits `PriorityPresent` and writes 128 for an unset
   priority, while the runtime inherits the bridge port's
   (`S/layer.go:439-442,576-579`). No standard is involved. U3. Test: with
   bridge port priority 32, instance port `{0, false}` against `{128, true}`
   gives two retention keys and a `Diff` from 32 to 128, and `{32, false}`
   against `{32, true}` gives one key and no change.
9. High. Path-cost addition wraps (`S/layer.go:1499,1504,1507`). Costs 100
   and `0xfffffff0` over a 20,000 link elect the second path. `Q2003` 14.2.4
   and 14.2.5 give the cost four octets and say nothing of overflow, so the
   sum saturates. U6. Test: that election picks cost 100.
10. Medium. Forward delay is scheduled from the local timer
    (`S/layer.go:1772,1787,1953,2520`) while `times` (`:737-745`) reports the
    root's as the one in force. `D2009` 13.28.9 defines FwdDelay as the
    Forward Delay component of the CIST `designatedTimes`, which 13.29.33 f)
    sets from the root times. Draft text, published wording unverified.
    The hello half of this entry is struck: hello is the bridge's own and
    fixed (`UNH` RSTP.op.4.3, `D2009` Table 13-5), so arming it from the
    local timer (`:1228,2492`) is right. What departs is
    the Hello Time field, which carries the root's (`:1406,1426`) where
    RSTP.op.4.3 Parts B through E expect 2 seconds whatever the root sent.
    U6. Test: a bridge whose root advertises Forward Delay 4 and Hello Time
    1 moves a port to Learning after 4 seconds and sends Hello Time 2.
11. Medium. An SSTP outcome clears loop guard and never recomputes roles.
    `receiveLink` clears the mark (`S/layer.go:1999`) and `ReceiveSSTP`
    returns at `:2202,2210,2215,2220` with no recompute, so the port stays
    Alternate. U2. Test: a loop-inconsistent PVST port receives an SSTP BPDU
    with `Admitted` false and leaves Alternate in that call.
12. Medium. A speed-only update restarts the CIST handshake. The guard at
    `S/layer.go:1919` requires an unchanged cost, so a link-derived cost that
    follows the new speed falls through to `:1931-1959`, against the contract
    in the comment at `:1912-1918`. U2. Test: a forwarding RSTP port with no
    fixed cost takes a speed change, its `PathCost` follows, and its state,
    `ForwardTransitions`, and emissions show no new handshake.
13. Medium. `Advance` releases held BPDUs and sends hellos
    (`S/layer.go:2460-2499`) before it expires received information
    (`:2541-2556`). `Q2003` Figure 13-13 (Decisions). U6. Test: a bridge
    whose root information expires at the instant its hello is due emits a
    hello naming itself root.
14. Medium. The MST record limit is the 16-bit length's capacity, 4,091
    (`B/bpdu.go:346`, enforced at `:443` and `S/mst.go:232`). `Q2003` 13.14:
    "No more than 64 MSTI Configuration Messages may be encoded in an MST
    BPDU, and no more than 64 MSTIs may be supported by an MST Bridge."
    14.4 d) 3) and 14.6 v) repeat it. U3. Test: 65 records are refused where
    64 encode, and a version 3 length naming 65 decodes as an RST BPDU.
15. High. A port that leaves Forwarding raises a topology change
    (`S/layer.go:1797-1800,1879-1881,2037-2040,2395-2400`). The sources in
    Decisions detect one only when a port starts forwarding. `D2009` 13.19
    still removes the entries learned on a port that leaves the active
    topology, which `:1878` does. U5, subject to Open questions. Test: a
    forwarding Designated port goes down, the count does not move, and no
    emission carries the flag.
16. Medium. A topology-change flag is acted on whatever the port's role
    and state (`S/layer.go:2375-2382,1356-1364,2087-2098`). `Q2003` Figure
    13-19 acts on `rcvdTc`, `rcvdTcn`, and `tcProp` only in ACTIVE, which a
    port enters by starting to forward as Root, Designated, or Master and
    leaves when it loses the role. In INACTIVE the flags are cleared
    unprocessed. An edge port never enters ACTIVE (`D2009` Figure 13-28
    propagates on `tcProp && !operEdge`), so a propagated change does not
    flush it, where `raiseTopologyChange` flushes every port but the origin.
    U5. Test: an Alternate port, and a Designated port still Discarding,
    receive the flag and flush nothing, and a change on another port leaves
    an edge port's entries alone.
17. Medium. `receiveMSTIs` keeps the low nibble of the MSTI port priority
    octet and 8 bits of the CIST port number (`S/layer.go:1370`). `Q2003`
    14.6.1 e): bits 1 through 4 of octet 15 are sent as 0 and ignored on
    receipt. 14.2.3: the remainder of the identifier comes from the CIST
    Port Identifier, 12 bits. U3. Test: a received MSTI port octet `0x8F`
    over CIST port `0x8103` stores `0x8103`.
18. Medium. `bpdu.Decode` departs from `Q2003` 14.4. It decodes the MST
    shape for version 3 only (`B/bpdu.go:619`) where e) says 3 or greater.
    It refuses a version 3 length that is not a whole number of records
    (`:692`) and never reads the Version 1 Length, where d) says to decode
    such a frame as an RST BPDU. It refuses a Configuration or TCN BPDU
    whose version is above 1 (`:577,596`), where a), b), and NOTE 2 do not
    test the version. `S/README.md:300-317` describes the first two as
    intended. U3. Test: a nonzero Version 1 Length decodes as RST, version 4
    with a whole MST body as MST, and a version 2 Configuration BPDU is
    accepted, each one property away from a frame decoded the other way.
19. Medium. A BPDU with a Hello Time of zero is refused (`B/bpdu.go:666`).
    `UNH` RSTP.op.4.3 Part C expects it accepted as root information.
    `D2009` 13.29.21 raises a received Hello Time below the minimum to the
    minimum, and takes the minimum from Table 17-1 of IEEE Std 802.1D, which
    is unverified. U3 uses 1 second, the lowest value RSTP.op.4.3 sends as a
    valid one (Part B). Test: the BPDU decodes, the layer stores it, and the
    information expires 3 seconds later.

### Completeness

- MSTI records carry no Proposal or Agreement. `gatherMSTIRecords`
  (`S/layer.go:1311-1317`) sets role, learning, forwarding, and topology
  change, and `receiveMSTIs` (`:1345-1402`) reads only the last. `Q2003`
  14.6.1 a) gives each record a Proposal and an Agreement flag, and
  13.26.10 and 13.26.14 record them per MSTI. `S/README.md:424` does not
  list the gap. U4. Test: the R2 example, as two frames exchanged at one
  instant with no `Advance`. An MSTI agreement whose CIST message names
  another regional root is not recorded.
- A received TCN is not acknowledged (`S/layer.go:2087-2106`).
  `BPDU.TopologyChangeAck` and its setter (`B/bpdu.go:292,297`) have no
  caller outside tests. Every topology-change timer is HelloTime plus one
  second (`S/layer.go:1123,1357,2092,2376`), legacy links included. `Q2003`
  Figure 13-19 sets `tcAck` on a Designated port that received a TCN, and
  13.26.21 sends it. `D2009` 13.29.11 gives a port that does not send RSTP
  Max Age plus Forward Delay. U5. Test: the R4 example, where the next
  Configuration BPDU has bit 8 of the flags octet set and the one after has
  it clear, and the topology-change flag stays set for 35 seconds at
  default timers. A Root port in STP mode emits TCN BPDUs until an
  acknowledgment arrives.
- `BPDUDecisionFact` (`S/fact.go:24`, `bpduSnapshot` at `:69-81`) drops the
  configuration identifier, regional root, internal cost, remaining hops,
  and every MSTI record. U6. Test: two BPDUs that differ in one MSTI
  record's cost give two fact texts.
- The switch records only the CIST before and after an IEEE BPDU. Out of
  scope.

### Design

- `S/layer.go` is 2,621 lines, and `recompute` (`:1570-1814`) and
  `applyBPDU` (`:2246-2423`) pass 150. The parent's R9 bounds both. U1
  splits the file, and the unit that rewrites a function brings it under
  the bound.
- Priority inheritance is resolved at two runtime sites
  (`S/layer.go:439-442,576-579`) and a third rule in `Canonical`
  (`S/mst.go:41-45`), and `Diff` reports equal From and To when only
  presence changed (`S/diff.go:419-423,515-519`). Resolve once, in
  `Config.Normalize`. U3, with Correctness 8.
- `PortInfo.MSTID` carries a VLAN for a PVST tree (`S/layer.go:58,861`,
  `S/tree.go:10-14`). `fabric/fingerprint.go:321` and the facts print it.
  U6. Test: a PVST VLAN 10 snapshot and an MSTI 10 snapshot differ.

### Tests

- `TestSyncInstancePortsOnLinkDownFollowsTheLinkDownClearsFlag`
  (`S/link_state_internal_test.go:381`, rows at `:99-101`) requires `agreed`,
  `proposing`, and `fwdDelayTimer` to survive link down, which pins
  Correctness 2. U2 rewrites the file for the new ownership.
- `TestSSTPOnANonPVSTBridgeRunsTheLinkHalfAndAppliesNoVector`
  (`S/layer_test.go:2609-2616`) overwrites the previous counter before
  comparing, so the assertion cannot fail. U6.
- `TestMSTInstancesSelectIndependentRoots` (`S/layer_test.go:1592`) asserts
  MSTI roles and no MSTI state, and `convergeLayers` (`:1487`) returns
  without failing when its 400 rounds run out. U4 asserts state and
  `Forwards` and fails on no convergence.
- `TestSpeedOnlyLinkChangeReachesEveryTreesCostWithoutBouncing`
  (`S/layer_test.go:3055`) fixes VLAN 1's path cost (`:3063`), so only the
  preserving branch runs. U2.
- `TestReceiveSSTPRunsTheLinkHalfForEveryOutcome` (`S/layer_test.go:2445`,
  assertion at `:2542-2547`) checks the block reason and never the role. U2.
- `TestMSTBPDUCodecRoundTrip` (`B/bpdu_test.go:692`) hands the codec octets
  already placed (`:719-720,742-743`). `TestMSTBPDUEncodeRecordCountBoundary`
  (`:994`) mirrors the old limit at `:1019`, `S/mst_test.go:380-383` holds
  4,092, and `S/diff_coverage_test.go:59,75` seeds a present priority. No
  fixture in `B/bpdu_test.go` names an origin outside the test. U3.
- `TestBPDUGuardCountsOneTopologyChange` (`S/guard_test.go:202`) and
  `TestThreeBridgeRingConvergence` (`S/layer_test.go:197`, flush at `:414`)
  pin Correctness 15. `TestMSTITopologyChangeBitReachesAndFlushesThePeer`
  (`S/tree_internal_test.go:582`) sets the per-tree timer at `:633`. U5.
- `TestMSTITopologyChangeFlushesOnlyItsOwnVLAN`
  (`device/vswitch/switch_test.go:7266`) downs MSTI 1's Root port and
  expects VLAN 10 flushed on `p3`, and its comment credits the port that
  went down. After U5 the change comes from the new Root port starting to
  forward, and it reaches `p3` only if `p3` is active. U5 keeps the
  assertion, brings `p3` to Forwarding in the setup, and rewrites the
  comment.
- `simtest/scenario_cases.go`, `simtest/stp_cases.go`, and
  `simtest/README.md` hold the `mstid=` token. U6.

## Units

A unit's Tests are those of the Inventory entries that name it, plus what
its Tests line adds. A refusal case differs from an accepted input in one
property (`docs/solutions/conventions/a-refusal-test-needs-an-input-only-the-refusal-rejects.md`).

### U1. Split `layer.go` by subject
Files: src/common/sim/layer/stp/
After: none
Change: `layer.go` keeps `Layer`, construction, `Clone`, and `RetentionKey`.
The rest moves, unchanged, to `portstate.go` (port and transmit records),
`info.go` (`PortInfo` and the read accessors), `vector.go` (priority
vectors), `roles.go` (`recompute` and its helpers), `link.go` (`LinkChange`,
`receiveLink`, `Mcheck`, `syncInstancePorts`), `receive.go` (`Receive`,
`ReceiveSSTP`, `applyBPDU`, `receiveMSTIs`), `transmit.go` (`emit`, `frames`,
the BPDU builders), `topology.go` (`raiseTopologyChange`,
`mergeFlushTarget`), and `advance.go` (`Advance`, `NextWake`). No
declaration, comment, or test changes.
Tests: the existing suites and `go test ./test/conformance/sim/` pass with
no test file in the diff. `wc -l` shows no non-test file over 1,200 lines.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/stp`

### U2. One owner for link state
Files: src/common/sim/layer/stp/
After: U1
Change: `Layer` holds one link record per port for what today is copied
per tree or written on the CIST alone: the four `linkState` flags, admin
edge, the link path cost, `external`, the BPDU-guard and PVST-boundary
marks, `mdelayWhile`, `edgeDelayWhile`, and the receive counters.
`portState` keeps what a tree owns, the loop-guard mark included, armed on
the CIST alone until U6. `linkState` and `syncInstancePorts` are gone. A
link down or a BPDU-guard disable clears each tree's handshake state,
timer, and received information in one loop (Correctness 2). Auto-edge
loss returns the port to Discarding and proposing on every tree (3). Every
`ReceiveSSTP` return after the link half recomputes roles (11).
`LinkChange` on a port that is up with unchanged point-to-point status
updates the cost, recomputes, and resets no handshake state (12).
`LinkChange` comes under 150 lines.
Tests: entries 2, 3, 11, 12. `link_state_internal_test.go` asserts that no
`portState` field repeats a link-record field and that link down clears
`agreed`, `proposing`, and `fwdDelayTimer` on every tree.
`TestReceiveSSTPRunsTheLinkHalfForEveryOutcome` asserts the role. A clone's
link record changes without changing its source's.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/stp src/common/sim/device/vswitch src/common/sim/fabric`

### U3. Wire format and configuration identity
Files: src/common/net/bpdu/, src/common/sim/layer/stp/
After: U2
Change: the codec masks both MSTI priority octets with `0xF0` on encode and
decode, and the layer hands it `Priority >> 8` and rebuilds the bridge
priority as `octet << 8` (Correctness 1). The MSTI port identifier is the
octet's high nibble over the low 12 bits of the CIST Port Identifier (17).
`bpdu.MaxMSTIRecords` is 64, for `Encode`, `Decode`, and `MST.Validate`
(14). `Decode` follows `Q2003` 14.4 a) through f). Type 0 with at least 35
octets is a Configuration BPDU and type `0x80` with at least 4 is a TCN,
whatever the version. Version 3 or above with a zero Version 1 Length and
0 to 64 whole records is an MST BPDU, and any other type 2 frame of that
version and at least 35 octets is an RST BPDU. A payload shorter than its
version 3 length claims is refused, which 14.4 does not address (18).
`Decode` accepts a Hello Time of zero, and the layer stores a received
Hello Time below 1 second as 1 second (19). `Config.Normalize` gives an
instance or VLAN port without a priority the bridge port's and marks it
present, and `newLayer`, `addTree`, `Canonical`, and `Diff` read that one
value (8, Design).
Tests: entries 1, 8, 14, 17, 18, 19. For R5, `B/bpdu_test.go` gains one byte
literal with a comment per field naming its octets in `Q2003` Figure 14-1
or 14-2: an MST BPDU with two MSTI records, bridge priorities `0x40` and
`0x10`, port priorities `0x20` and `0xE0`, every multi-octet field distinct
in each octet. `Decode` returns the fields and `Encode` the bytes. Nothing
here compares the fixture with a device capture. The tests under
Inventory, Tests take the new values.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/bpdu src/common/sim`

### U4. Proposal and agreement on every tree
Files: src/common/sim/layer/stp/, src/common/sim/device/vswitch/, src/common/sim/fabric/, src/common/sim/internal/simtest/
After: U3
Change: one rule records an agreement for the CIST and for each MSTI. It
sets `agreed` when the link is point-to-point, the port sends RSTP, the
Agreement flag is set, and the message conveys a Root, Alternate, or Backup
role with a vector the same as or worse than the port's own, or a
Designated role the same or better. Any other message it is run for clears
`agreed` (`Q2003` 13.26.9, 13.26.10). A proposal is acted on only when the
message conveys a Designated role (`D2009` 13.29.20, Sources). The layer
keeps no `proposed` flag, so there is nothing to clear. Each MSTI record
carries its tree's Proposal and Agreement flags, recorded only when the
CIST message in the same BPDU names the CIST root, external cost, and
regional root the port holds (13.26.10 a). On a boundary port the MSTIs
take the CIST's `agreed` and `proposed` (13.26.9, 13.26.13). A proposal on
a tree's Root or Alternate port syncs that tree's other ports, and the
answer carries every tree's agreement. `applyBPDU` comes under 150 lines.
Tests: entry 4, the MSTI entry under Completeness, and the rewrite of
`TestMSTInstancesSelectIndependentRoots`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim`

### U5. Topology change per port
Files: src/common/sim/layer/stp/, src/common/sim/device/vswitch/, src/common/sim/fabric/, src/common/sim/internal/simtest/
After: U4
Change: each tree's port has its own topology-change timer and an active
mark, and the per-tree timer is gone. A non-edge Root or Designated port
that starts forwarding becomes active, starts its timer, and propagates to
the tree's other active ports, which start theirs and are flushed. A port
that loses the role, becomes an edge, or goes down is flushed, stops its
timer, leaves active, and raises nothing (15). The timer runs HelloTime
plus one second on a port that sends RSTP and Max Age plus Forward Delay of
the root's times on one that does not. A BPDU, and each MSTI record, sets
its flag from the sending port's own timer. A Root port whose timer runs
transmits when it starts and at each hello: an RST or MST BPDU with its
role, its agreement state, and the flag when it sends RSTP, a TCN BPDU
when it does not (5). On receipt (`Q2003` 13.26.19) a TCN counts for the
CIST and every MSTI, a flag from outside the region for every tree, and a
flag from inside for the trees that set it. A port that is not active
ignores all three (16). A TCN on a Designated port sets an acknowledgment
that the next Configuration BPDU on that port carries once. A received
acknowledgment stops the port's timer. U7 supersedes when a port whose
timer runs transmits, and when a timer restarts.
Tests: entries 5, 15, 16, and the TCN entry under Completeness. `NextWake`
returns the earliest port timer. The tests named for U5 under Inventory,
Tests, and `TestTCNReceiveRaisesTopologyChange` (`S/layer_test.go:1249`),
take the new behaviour.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim`

### U6. Received information, timers, and what the layer reports
Files: src/common/sim/layer/stp/, src/common/sim/fabric/fingerprint.go, src/common/sim/fabric/fingerprint_test.go, src/common/sim/device/vswitch/, src/common/sim/internal/simtest/
After: U5
Change: the internal or external mark changes only when a BPDU's
information is stored (Correctness 7). Path-cost sums saturate at the
largest 32-bit value (9). The forward-delay ladder steps by the Forward
Delay in force, the Hello Time field carries the bridge's own, and `Times`
reports the bridge's own hello (10). `Advance` expires information, runs
the ladder, and recomputes before it releases a held BPDU or sends a hello
(13). Under PVST each tree arms loop guard on its own expiry and clears it
on a BPDU applied to that tree or a link down. Under MSTP the CIST's mark
holds every instance, as `S/README.md:127` says (6). For a BPDU with a
configuration identifier, the decision fact also prints it, the regional
root, internal cost, remaining hops, and each MSTI record. Any other
BPDU's text is unchanged. `PortInfo.Tree` replaces `MSTID`: a kind
(`TreeCIST`, `TreeMSTI`, `TreeVLAN`) and an identifier, with VLAN 1's tree
reported as `TreeVLAN` 1. The fact prints `tree_kind`, quoted like `role`,
and `tree_id` in place of `mstid`, and the fabric fingerprint prints both
through `escapeFingerprint`, since `:` and `=` are its delimiters.
`S/README.md` gains a section naming the edition, the sources, the clause
each machine follows, and the limits in the Inventory. `recompute`,
`Advance`, and `newLayer` come under 150.
Tests: entries 6, 7, 9, 10, 13, the fact entry under Completeness, and the
`PortInfo` entry under Design. `layer_test.go:2609` stops overwriting the
counter.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim`

### U7. Port transmit and topology-change emission
Files: src/common/sim/layer/stp/, src/common/sim/device/vswitch/, src/common/sim/fabric/, src/common/sim/internal/simtest/
After: U6
Change: line numbers hold at `18c7187e`. A request to send is a flag, and
`Receive`, `ReceiveSSTP`, `Advance`, `LinkChange`, and `Mcheck` each end
with one transmit pass. `Layer` keeps one transmit record per port, and
under PVST one per tree and port, keyed as the budget is today
(`S/portstate.go:74-117`). A record holds the CIST flag, the MSTI flag, the
count, and the hello timer. A PVST tree's record uses the CIST flag alone,
and "the tree" below is then that VLAN's. Each rule names its source and
the test that fails without it.

Transmit rules:

- T1, one pass. Every tree finishes its transitions before a frame is
  built. Nothing sets a flag once the pass has begun, so a record transmits
  at most once in a call. Source: `D2009` Figure 13-19 qualifies every
  transition but UCT by `allTransmitReady`, which 13.28.3 defines as
  `selected && !updtInfo` for all trees on the port. `Q2003` 13.30 NOTE 1
  recommends processing a BPDU whole before encoding. Running the pass
  last is how the layer meets both. Test `TestTransmitOnceFromFinalState`:
  the R6 example.
- T2, built at that instant. A port that sends RSTP sends an RST BPDU, or
  an MST BPDU under MSTP, whatever its roles. A port that does not answers
  its CIST flag with a TCN BPDU when it is Root for the CIST, with a
  Configuration BPDU when it is Designated for the CIST, and with nothing
  otherwise, and then the flag stays. Under PVST a tree other than VLAN 1's
  sends nothing on such a port, as today (`S/transmit.go:23-30`), and keeps
  its flag. Each tree's role, Proposal, Agreement, Learning, and Forwarding
  bits are the ones U4 landed (`S/transmit.go:58,215-224,311-331`), read
  for that tree when the frame is built, so every frame of a port carries
  each tree's own. `emissionKind`, `emit`, and `makeAgreementBPDU`
  (`S/transmit.go:12-99,308-334`) go. Source: `D2009` Figure 13-19 (the
  three transitions out of IDLE), 13.29.28 to 13.29.30. `Q2003` 14.5. Test
  `TestHeldRequestIsBuiltAtRelease`: the R8 example. A Root port that holds
  the CIST flag and migrates to STP before the count falls releases one TCN
  BPDU. A port that is Alternate for the CIST and Root for MSTI 1 answers a
  change on MSTI 1 with one MST BPDU naming both roles.
- T3, which flag sends. The CIST flag sends on any port. The MSTI flag
  sends on a port that sends RSTP, unless the port is Root for the CIST on
  an external link, the standard's Master Port. An RST or MST BPDU clears
  both flags. A TCN or Configuration BPDU clears the CIST flag alone.
  Source: `D2009` Figure 13-19 (`sendRSTP && (newInfo || (newInfoMsti &&
  !mstiMasterPort))` and the assignments of the three transmit states),
  13.28.14. `Q2003` 13.30 NOTE 2. Test
  `TestMSTIFlagAloneSendsOnlyInsideTheRegion`: a change on MSTI 1 alone
  reaches two Forwarding ports of an MST bridge. The internal port emits
  one MST BPDU in that call, and the boundary Root port nothing. A port
  that is Alternate for the CIST holds the MSTI flag at the hold count and
  migrates to STP before the count falls. It emits nothing when the count
  falls, and one MST BPDU in the `Mcheck` that returns it to RSTP.
- T4, count. A transmission needs a count below `TxHoldCount` and adds one
  to it, and the count falls by one a second, with the arithmetic of
  `S/transmit.go:46-54,83-88`. Under PVST the two frames of VLAN 1 are one
  transmission. A record at the limit keeps its flags and nothing else, and
  the first pass that finds the count below the limit acts on them.
  `pendingDesignated`, `pendingAgreement`, and `pendingTCN`
  (`S/portstate.go:86-92`) go. Source: `D2009` Figure 13-19 (`txCount <
  TxHoldCount`, `txCount += 1`), 13.27.70, Figure 13-15, Table 13-5
  (default 6, range 1 to 10). `UNH` RSTP.op.4.6 allows `TxHoldCount` plus
  one BPDUs in any second. Test: `TestTransmitHoldCountGating`
  (`S/layer_test.go:1151`) and `TestPVSTEveryVLANKeepsItsOwnTransmitBudget`
  (`:2306`) keep their assertions.
- T5, hello. Each record has its own hello timer. It restarts when the
  record transmits and when it expires, from the instant of that call. At
  expiry the CIST flag is set when the port is Designated for the CIST, or
  Root for it with its CIST timer running. The MSTI flag is set when the
  port is Designated for an MSTI, or Root for one whose timer runs on the
  port. `tree.helloTimer` (`S/tree.go:56`), `armHelloTimers`
  (`S/roles.go:40-54`), and `sendDueTransmissions` (`S/advance.go:179-230`)
  go. Source: `D2009` Figure 13-19 (TRANSMIT_PERIODIC, and IDLE's
  `helloWhen = HelloTime`), 13.28.13. `UNH` RSTP.op.4.3 expects a
  Designated port's BPDUs every two seconds. Test
  `TestHelloRestartsWhenThePortTransmits`: the R7 example. A request and a
  due hello in one `Advance` give one frame.
- T6, a port that cannot send. While a port is down or disabled by BPDU
  guard its records send nothing, and they hold a zero count and both
  flags. The call that brings a port up therefore emits one frame on it
  once its roles are computed, edge port or not, which replaces the
  proposal `LinkChange` builds (`S/link.go:180-195`). `clearPending`
  (`S/portstate.go:124-134`) goes. Source: `D2009` Figure 13-19 (`BEGIN ||
  !portEnabled || !enableBPDUtx` enters TRANSMIT_INIT, which sets both
  flags and zeroes `txCount`). BPDU guard is the layer's own and is read as
  `!portEnabled`. Test `TestPortComingUpTransmitsOnce`: an `AdminEdge` port
  and a shared-link port each emit one frame in the `LinkChange` that
  brings them up. A port that held a request, went down, and came up emits
  one.
- T7, order. The pass walks `treeOrder`, then `portNames`, and outside
  PVST only the CIST's walk transmits. Source: none, the layer's own
  (Decisions). Test `TestEmissionsLeaveByTreeThenPort`: in one call that
  owes a Designated frame on p1 and a Root frame on p2, p1's comes first.
  Under PVST with VLANs 1, 10, and 20, VLAN 1's frames on p1 and p2 come
  before VLAN 10's on p1.

Topology change rules:

- C1, starting a timer. Detection, propagation, and a received TCN start a
  port's timer for a tree only when it is stopped. A start on a port that
  sends RSTP sets that tree's flag, whatever the port's role. A start on a
  port that does not sets no flag and runs Max Age plus Forward Delay. A
  running timer is left as it is and requests nothing, where
  `S/topology.go:198,220` and `S/receive.go:222,233` restart it. Source:
  `D2009` 13.29.11, Figure 13-28 (NOTIFIED_TCN, PROPAGATING). `UNH`
  RSTP.op.4.5 Part A expects exactly two flagged RST BPDUs from the Root
  port and from a Designated port. Test
  `TestTopologyChangeLeavesAtOnceAndTwice`: the R7 example. A second
  flagged BPDU one second after the first emits nothing on p1 or p2, and
  no frame carries the flag later than HelloTime plus one second after the
  first. Under PVST the same BPDU for VLAN 10 through `ReceiveSSTP` gives,
  in that call, one flagged frame for VLAN 10 on the Root port and none for
  VLAN 1.
- C2, detection. A port that detects a change (U5) also sets its tree's
  flag itself, whether or not its timer ran and whether or not it sends
  RSTP. Source: `D2009` Figure 13-28 (DETECTED: `newTcWhile()`,
  `setTcPropTree()`, `newInfoXst = TRUE`). Test
  `TestDetectionTransmitsOnTheDetectingPort`: a Designated port that an
  agreement brings to Forwarding emits one flagged frame in that `Receive`.
  A Root port under STP that reaches Forwarding at the end of the ladder,
  in an `Advance` where its hello is also due, emits one TCN BPDU.
- C3, a port that does not send RSTP. Its running CIST timer shows at each
  hello: a TCN BPDU from a Root port, a flagged Configuration BPDU from a
  Designated port. Source: `D2009` 13.29.11 (no flag when `sendRSTP` is
  FALSE), Figure 13-19 (TRANSMIT_PERIODIC, TRANSMIT_TCN,
  TRANSMIT_CONFIG). `UNH` RSTP.op.2.3 expects the TCN BPDUs within two
  seconds, and RSTP.op.4.5 Part B for 30 to 35 seconds. Test
  `TestLegacyPortReportsAChangeAtItsHello`: the R9 example.
- C4, flags in the frame. The CIST flag bit is set when the port's CIST
  timer runs, and each MSTI record's bit when that MSTI's timer runs on the
  port. Source: `D2009` 13.29.28, 13.29.29. `Q2003` 14.6 a), 14.6.1 a).
  Test `TestTopologyChangeFlagIsPerTree`: a change on MSTI 1 of an MST
  bridge gives a frame whose MSTI 1 record has bit 1 set, while octet 5 and
  the MSTI 2 record have it clear, read from the payload. Octet 5 of the
  BPDU is index 7 of the frame's payload, after the LLC header
  (`B/bpdu.go:373-386`).
- C5, acknowledgment. Bit 8 of octet 5 is set only in a Configuration
  BPDU, from `tcAck`. A Configuration, RST, or MST BPDU clears `tcAck`, and
  a TCN BPDU leaves it, where `S/transmit.go:283-286` sets the bit in any
  frame. A received TCN requests no frame beyond C1, so
  `S/receive.go:217-220` stops emitting. Source: `D2009` Figure 13-19
  (`tcAck = FALSE` in TRANSMIT_CONFIG and TRANSMIT_RSTP), 13.29.28,
  13.29.29 ("never used and is set to zero"), Figure 13-28 (NOTIFIED_TCN,
  NOTIFIED_TC). `Q2003` 14.6 g). Test
  `TestAcknowledgmentLeavesInTheNextConfigurationBPDU`: a TCN BPDU on a
  Designated port under STP emits nothing on it in that call. Its next
  hello is a Configuration BPDU with bits 8 and 1 set, and the one after
  has bit 8 clear and bit 1 set. A TCN BPDU on a Designated port within
  MigrateTime of an `Mcheck` emits one RST BPDU with bit 8 clear. When a
  Configuration BPDU migrates that port to STP once MigrateTime has
  passed, its next Configuration BPDU has bit 8 clear too.

A role transition requests a frame where it does today. It sets the flag
of its tree in place of building a frame: the CIST flag for the CIST, the
MSTI flag for an MSTI. The request sites:

- A tree's root information changed, on each of its Designated,
  point-to-point, Discarding, unagreed ports (`S/roles.go:91-99`). The
  `emit` gate that kept this to the CIST (`S/roles.go:63-71`) goes, since
  T1 spends the count once. `D2009` Figure 13-25 (DESIGNATED_PROPOSE).
- A proposal answered on a Root or Alternate port (`S/receive.go:519-520`).
  `answerProposals` (`S/receive.go:581-607`) returns one boolean for all
  trees today, and reports which tree answered after this unit. `D2009`
  Figures 13-24 and 13-26 (ROOT_AGREED, ALTERNATE_AGREED).
- A BPDU without the Agreement flag whose information is worse than the
  port's own, on a Designated port, when no proposal was answered
  (`S/receive.go:521-524`). The sender's role is not tested. The layer's
  own. It stands in for the dispute path of `D2009` Figure 13-20
  (INFERIOR_DESIGNATED), which the layer does not have (Limits).
- `Mcheck` on a port Designated for the CIST (`S/link.go:29-45`). The
  layer's own. `D2009` Figure 13-17 assigns only `mcheck`, `sendRSTP`, and
  `mdelayWhile` in CHECKING_RSTP.

`topologyChangeEmissions`, `emitTopologyChangeEmissions`, and
`rootTopologyChangeActive` (`S/topology.go:25-189`) go, with the `changes`
parameter and the emission slices that `recompute`, `recomputeAll`,
`receiveLink`, and `applyBPDU` pass around. `frames`, `gatherMSTIRecords`,
`tcWhileDuration`, `propagateTopologyChange`, `initiateTopologyChange`, and
`deactivatePort` stay. `NextWake` reports a record's hello only while its
expiry would set a flag, and the instant a count next falls only for a
record at the limit with a flag it can act on. `S/README.md` states the
record, the rules with their sources, the two request sites that are the
layer's own, and the two gaps under Open questions, in its Emission,
Standards, and Limits sections.
Tests: the tests named above go in a new `S/transmit_test.go` (package
`stp_test`). They drive the exported calls and read `Effects.Emissions`.
`S/topology_change_property_test.go` is deleted: its oracle reads the
pending fields this unit removes and counts a held request as a frame
sent, and Review gaps lists four of its cases as vacuous or built on a
state no call sequence reaches. `TestLinkDownClearsHeldTCNAndAcknowledgment`
and `TestHeldTCNIsNotReleasedOnAnRSTPPort`
(`S/link_state_internal_test.go:117,152`),
`TestPVSTDoesNotInheritCISTAgreement`,
`TestPVSTDoesNotInheritCISTTopologyChange`,
`TestAgreementClearsOnRoleChangesAndUnknownSenderRoles`, and
`TestMSTTopologyChangeUsesTreeOrderForEmissions`
(`S/tree_internal_test.go:678,708,762,784`) set fields or call helpers
that go. Each keeps its assertion, set up through what remains, and none
is deleted: the tests above repeat only the ordering half of the last.
`TestHeldTCNIsNotReleasedOnAnRSTPPort` also asserts the RST BPDU that is
released, since it passes today when nothing is.
`TestTransmitBudgetKeyingFollowsTheMode` (`S/tree_internal_test.go:1161`)
keeps its assertion on the record that replaces `portTx`. Three kinds of
existing test take new values. One that pins a hello instant takes the
port's own, HelloTime after its last frame. One that counts the frames of
a call takes T1, T6, and C1. One that expects a frame in the call that
receives a TCN BPDU takes C5, as `TestLegacyTCNHandshakeAndTimer`
(`S/layer_test.go:4273`) does. That fixture also delivers its TCN BPDU at
the instant the port starts forwarding (`:4288-4301`), while the timer
detection started is running, so by C1 the flag ends three seconds later.
The TCN BPDU moves to after that timer has stopped. Nothing in this unit
compares a frame with the published IEEE Std 802.1Q-2011, or with a device
capture.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim`

Waves: U1 | U2 | U3 | U4 | U5 | U6 | U7

## Verification

```bash
go build ./src/common/sim/... ./src/common/net/bpdu/...
go test -race ./src/common/sim/... ./src/common/net/bpdu/... ./test/conformance/sim/...
golangci-lint run ./src/common/sim/layer/stp/... ./src/common/net/bpdu/...
python3 .claude/skills/prose/scripts/check-prose.py src/common/sim/layer/stp/README.md src/common/net/bpdu/README.md
```

No unit changes a signature the contract gate in `test/conformance/sim`
requires (`New`, `Advance`, `NextWake`, `RetentionKey`, `Diff`, `Config`).

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] Every Inventory entry has its failing-first test or is struck.
- [ ] `S/README.md` and `B/README.md` name the edition, sources, clauses,
      and limits, and say nothing the code no longer does.
- [ ] No non-test file in `layer/stp` exceeds 1,200 lines and no function
      150, unless a comment at its head states why it is one unit.
- [ ] No plan label appears in code, comments, or commit messages.
- [ ] This plan's `status` is set with an outcome note under its title, and
      the parent's U3 `Landed:` line holds the range.

## Open questions

- The edition is decided above. Carried: the published IEEE Std 802.1Q-2011
  and IEEE Std 802.1D-2004 are unverified, and with them the forward-delay
  step on an RSTP port (Limits). U6's README marks each `D2009` clause draft.
- Whether U11 loads MSTP and PVST stays with U11, which owns `netmodel`.
  The schema carries MSTP (`PROTOCOL_VERSION_MSTP` and the `Mst*` messages
  under `spec/proto/flowseer/net/protocol/stp/v1/`) and nothing for PVST,
  and `netmodel` skips every version but RSTP (`netmodel/netmodel.go:1279`).
- The fabric never uses the switch's point-to-point default: it does not
  call `Switch.Start`, reports each port with the resolved value
  (`fabric/fabric.go:860-862`), and injects emissions only after every port
  is reported (`:873-875`). The default of true serves a switch run alone
  (`device/vswitch/switch.go:2929-2945`), which U9 owns.
- U6 and Correctness 11 disagree under PVST. U6 clears a tree's loop-guard
  mark "on a BPDU applied to that tree or a link down". Correctness 11's test
  has a loop-inconsistent PVST port leave Alternate on an SSTP BPDU with
  `Admitted` false, which is applied to no tree. The code clears every
  tree's mark on any frame (`S/link.go`, `receiveLink`), so a VLAN 1 BPDU
  reopens VLAN 10's held port within one hello, and
  `TestLoopInconsistentPVSTPortReceivesUnadmittedSSTPLeavesAlternate` and
  `TestReceiveSSTPRunsTheLinkHalfForEveryOutcome` fail once the clear follows
  U6. One of the two texts has to change. `S/info.go` `blockReason` reads the
  CIST's mark for every tree and follows the same answer.
- U7's rules T2 (a port that sends RSTP transmits whatever its role), T6,
  and C2 rest on `D2009` alone, a working draft. `UNH` neither supports nor
  contradicts them, and the published IEEE Std 802.1Q-2011 is unverified.
- Carried, and not changed by U7: a Designated port whose root information
  changes while it forwards or holds an agreement sends the new information
  at its next hello. `D2009` Figure 13-20 (UPDATE) with 13.29.33 j) and k)
  requests a frame at once, and `UNH` RSTP.op.4.6 drives its hold-count
  test that way. It is a role-transition request, outside the
  topology-change emission (Decisions).
- Carried, and not changed by U7: a Designated port that a sync returns to
  Discarding (`S/receive.go:23-45`) proposes at its next hello. `D2009`
  Figure 13-25 (DESIGNATED_PROPOSE) requests a frame at once, so each
  bridge between a new root and a leaf can add one HelloTime to
  convergence. Outside the topology-change emission for the same reason.

## Review gaps

- src/common/sim/layer/stp/receive.go:71: Root-like branch of `recordAgreement` narrowed to the Root role; fails: an Alternate or Backup sender's agreement with a worse vector opens the port
- src/common/sim/layer/stp/receive.go:96: root or external-cost term dropped from `cistConsistent`; fails: an MSTI agreement whose CIST message names another root or cost is not recorded
- src/common/sim/layer/stp/receive.go:113: `mp.tcActive` dropped; fails: an inactive MSTI port ignores its record's topology-change flag
- src/common/sim/layer/stp/receive.go:118: `<< 8` to `<< 12`; fails: a received record priority octet `0x40` gives designated bridge priority `0x4000`
- src/common/sim/layer/stp/receive.go:150: MSTI record hello floor removed; fails: an MSTI record under Hello Time zero expires after 3 seconds
- src/common/sim/layer/stp/receive.go:165: Designated-role test dropped from the MSTI and CIST proposal checks (`:560`); fails: a proposal from a Root-role sender syncs nothing
- src/common/sim/layer/stp/receive.go:48: `syncTree` call removed; fails: a proposal on a Root port returns a forwarding, unagreed Designated port to Discarding
- src/common/sim/layer/stp/receive.go:478: boundary MSTIs no longer take the CIST's `agreed`; fails: an MSTI Designated port on a boundary forwards on the CIST's agreement
- src/common/sim/layer/stp/receive.go:509: acknowledgment no longer stops the timer; fails: a Root port in STP mode emits TCN BPDUs at each hello until a Configuration BPDU with the acknowledgment flag arrives, and none after
- src/common/sim/layer/stp/topology.go:69: `deactivatePort` keeps `tcWhile`; fails: a port that loses its role reports no wake for the timer
- src/common/sim/layer/stp/roles.go:306: `!link.edge` dropped; fails: an edge port that starts forwarding raises no topology change
- src/common/sim/layer/stp/advance.go:31: `update(p.tcWhile)` removed; fails: `NextWake` returns the earliest port timer
- src/common/sim/layer/stp/advance.go:67: held release moved above expiry; fails: a held BPDU released at the instant root information expires names the bridge itself root
- src/common/sim/layer/stp/advance.go:143: Learning step armed with the local delay; fails: a port reaches Forwarding 4 seconds after Learning under a root advertising Forward Delay 4
- src/common/sim/layer/stp/vector.go:91: `saturatingAdd` to `+` on the internal slot and the non-CIST slot (`:94`); fails: the cost 100 path wins on an internal CIST port and on a PVST tree
- src/common/sim/layer/stp/link.go:271: `proposing` not restored after auto-edge loss; fails: the port proposes again on every tree
- src/common/sim/layer/stp/config.go:235: PVST tree-port inheritance removed; fails: a VLAN port without a priority takes the bridge port's and marks it present
- src/common/sim/layer/stp/mst.go:230: `>` to `>=`; fails: 64 instances validate
- src/common/sim/layer/stp/fact.go:84: `config_id`, `regional_root`, `internal_cost`, or `remaining_hops` dropped; fails: two BPDUs that differ in that field give two fact texts
- src/common/sim/layer/stp/info.go:285: VLAN 1's tree reported as CIST under PVST; fails: `PortInfo.Tree` is `TreeVLAN` 1
- src/common/sim/layer/stp/layer_test.go:3264: the MSTI half of the link-bounce case is missing; fails: an MSTI Designated port that earned an agreement stays Discarding after a bounce
- src/common/sim/layer/stp/layer_test.go:4021: the emission loop never runs on a one-port fixture; fails: a forwarding Designated port goes down and the Root port emits no flagged frame in that call or at the next hello
- src/common/sim/layer/stp/layer_test.go:4166: assertions skipped when there are no emissions or Decode errors, and no check of `TypeConfiguration`; fails: the acknowledging frame is a Configuration BPDU with bit 8 set
- src/common/net/bpdu/bpdu.go:501: either encode mask removed (`:502`); fails: priorities `0x4F` and `0x2F` encode as octets `0x40` and `0x20`
- src/common/net/bpdu/bpdu.go:735: either decode mask removed; fails: wire octets `0x4F` and `0x2F` decode as `0x40` and `0x20`
- src/common/net/bpdu/bpdu.go:588: version refusal restored for TCN, or for Configuration above version 2; fails: version 4 Configuration and TCN frames decode
- src/common/net/bpdu/bpdu.go:623: whole-record test replaced by `true`; fails: a Version 3 Length of 65 decodes as RST
- src/common/net/bpdu/sstp.go:157: zero Hello Time refusal restored; fails: an SSTP frame with Hello Time zero decodes
- src/common/sim/fabric/fingerprint.go:321: tree kind replaced by a constant; fails: VLAN 10 and MSTI 10 snapshots give two fingerprints
- src/common/sim/layer/stp/receive.go:11: `receiveMSTIs` comment sits on `syncTree` and says the flag is carried unconditionally; `:297` names `syncInstancePorts`; `:391` says the classification is written on the CIST's port state
- src/common/sim/layer/stp/info.go:76: link fields described as written on the CIST's port state (also `src/common/sim/layer/stp/roles.go:119`); `:160` says hello is the root's; `:220` `PVSTBoundary` comment duplicated
- src/common/sim/layer/stp/link.go:93: says a topology change follows link down (also `:236` for BPDU guard)
- src/common/sim/layer/stp/topology.go:25: says `initiateTopologyChange` emits on p
- src/common/sim/layer/stp/mst.go:176: says the layer reads `PriorityPresent`; `:226` explains the instance cap by the 16-bit length
- src/common/sim/layer/stp/transmit.go:117: `p.sendRSTP`
- src/common/sim/layer/stp/README.md:129: loop-guard clear stated two ways (`:127`, `:240`); `:250` `BlockReason` said to come from the link record; `:285` boundary mark on the CIST port state; `:297` and `:458` version 4 decodes with no `ConfigID`; `:374` instance cap by length field; `:387` a change flushes every other port; PVST and SSTP not marked as modelled on observation
- src/common/sim/layer/stp/guard_test.go:201: `TestBPDUGuardCountsOneTopologyChange` asserts zero; `src/common/sim/layer/stp/mst_test.go:368` and `:398`, `src/common/sim/layer/stp/layer_test.go:699`, `:728`, `:4121` state replaced behavior; `:3301` `t.Logf` leftovers
- src/common/net/bpdu/README.md:73: says Hello Time is clamped on decode; `:64` promises RST fallback for a truncated body
- src/common/net/bpdu/bpdu.go:511: `Decode` comment keeps legacy versions 0 or 1, fallback above version 3, and the zero Hello Time refusal; `:365`, `:443`, and `src/common/net/bpdu/bpdu_test.go:994` give the 16-bit length as the reason for 64; `:676` says the caller checked 105 octets
- src/common/net/bpdu/bpdu_test.go:1777: golden frame EtherType is `len(wire) - 3`, the encoder writes `len(wire)`, and only payloads are compared
- src/common/sim/internal/simtest/stp_cases.go:721: comment keeps `mstid=1`
- src/common/net/bpdu/bpdu.go:601: `version >= mstProtocolVersion` to `==`; fails: version 4 and 255 frames with a 35-octet body decode as RST
- src/common/sim/layer/stp/receive.go:222: `l.treeOrder` back to `range l.trees` here, at `:476`, or at `:569`; fails: a TCN through `Receive` on a PVST bridge with five VLANs emits in ascending VLAN order on repeated runs
- src/common/sim/layer/stp/roles.go:195: boundary branch keeps `agreed` across a role change; fails: an MSTI boundary port that changes role must earn a new agreement
- src/common/sim/layer/stp/tree_internal_test.go:924: sets `tcActive` on an Alternate port, a state `updatePortStates` never leaves; fails: with reachable state no emission names the expired root
- src/common/net/bpdu/bpdu_test.go:1584: a second fixture with EtherType `len(Payload) - 3`
- src/common/sim/layer/stp/receive.go:141: `changes` argument to `recordAgreement` replaced by nil here or at `:167`; fails: an MSTI agreement on a Designated port sends the flagged record on the MSTI's Root port in that call
- src/common/sim/layer/stp/link.go:108: deferred emission dropped on link down; fails: a Root port goes down and the Alternate that becomes Root and Forwarding sends a flagged frame in that call
- src/common/sim/layer/stp/tree_internal_test.go:814: `TestMSTTopologyChangeUsesTreeOrderForEmissions` calls `propagateReceivedTC` directly and tests no order; fails: a flagged BPDU through `Receive` on a boundary port gives one frame on the Root port
- src/common/sim/layer/stp/tree_internal_test.go:957: Root port with `rcvInfoValid` false and an Alternate holding a better root than the tree's, states no call sequence leaves
- src/common/sim/layer/stp/layer_test.go:4094: no hello is due at the instant checked, so the loop body never runs
- src/common/sim/layer/stp/transmit.go:33: open behavior finding of round four. A second frame on a budget in one call is held, and the deferred topology-change frame is queued as an agreement or TCN on the CIST port, which `advance.go:196` and `:208` drop at release when that port is Designated for the CIST. On an MST bridge whose port is CIST Designated and becomes Root for an MSTI in a `Receive` that also changes the CIST root, the proposal built in the CIST's turn (`roles.go:91`) names the MSTI port Alternate with no flag, and the owed record waits for the next hello; fails: the flagged record leaves in that call, built from the final role
- src/common/sim/layer/stp/topology.go:183: open behavior finding of round four. Deferred frames are prepended, so a flagged Designated reply on p1 follows the Root frame on p2, and under PVST a flagged acknowledgment on VLAN 1's p2 follows VLAN 10 to 40 on p1; fails: frames leave in tree order, then port order
- src/common/sim/layer/stp/transmit.go:81: `markBuilt` skipped for Designated frames, or the hold at `:33` deleted; fails: an MST port Designated for the CIST whose MSTI timers start in the call (the fixture's p2 in Learning, superior BPDU before the ladder ends) sends one frame
- src/common/sim/layer/stp/topology_change_property_test.go:97: `DesignatedRestartRootReturn` starts no timer and owes nothing, and p1's information expires in the call under test; fails: p2 Designated and Discarding takes an agreement that forwards it and makes it Root, and owes the CIST pair on p2
- src/common/sim/layer/stp/topology_change_property_test.go:235: no forward-delay timer is due in `ladderEffects` and the TCN comes from the hello; fails: a legacy Root port that finishes the ladder where a hello is due sends one TCN
- src/common/sim/layer/stp/topology_change_property_test.go:546: `expected` is never read, a TCN skips the role check (`:567`), and a held flag counts as sent with budget to spare (`:634`); fails: `p.role != bpdu.RoleRoot` dropped at `topology.go:162` sends a flagged frame from a Designated port
- src/common/sim/layer/stp/topology_change_property_test.go:330: the MST Mcheck BPDU is a Configuration BPDU that keeps `ConfigID` and MSTI records, and the MST Receive BPDU (`:34`) raises the external cost under an unchanged regional root, shapes `bpdu.Decode` or a conformant sender never gives; not confirmed by a run
- src/common/sim/layer/stp/topology.go:162: guards a nil port and link no caller can pass, and `:175` reads the link again
- Parked by drive: the review of the phase with U7 recorded `fixes needed`
  and ran no fix round, because five findings need a plan decision. They
  are the short Version 3 BPDU (U3 against `Q2003` 14.4), the legacy Root
  port's hello (R9 against U7's T5), the MSTI proposal gate (U4 against
  both sources), TCN propagation under PVST, and the loop-guard clear (U6
  against Correctness 11). The record is on `parked/sim-p3-review`.
  Options: answer the five, then run the fix loop (the review continues on
  settled text) | record all five under Limits and fix only the rest
  (faster, leaves five known departures). Recommended: answer the five,
  because two of them are the review's High findings.
