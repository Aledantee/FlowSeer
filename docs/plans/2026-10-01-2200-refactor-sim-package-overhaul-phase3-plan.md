---
title: Spanning Tree to Standard - Plan
type: fix
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: fixes needed
execution: code
parent: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-plan.md
---

# Spanning Tree to Standard - Plan

> Implemented. 6 units, 2026-10-05T07:28Z to 2026-10-05T09:39Z.

## Goal

`layer/stp` follows IEEE 802.1D and IEEE 802.1Q for RSTP, MSTP, and
interoperation with legacy STP, and keeps its PVST and SSTP behaviour
consistent with them. The means is six units: a file split, one owner for
link state, then one unit per state machine the inventory touches. Stop
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
- Ruled: `bpdu.Decode` refuses an MST BPDU whose payload is longer than its
  Version 3 Length names, as it refuses a shorter one. Why: `Q2003` 14.4
  addresses neither mismatch, and the decoder refused both before this
  phase (`B/bpdu.go` doc comment at `1af2f975`, "disagrees with the
  payload"). Cost if wrong: one condition in `readMSTBody` and
  `TestMSTBPDUDecodeRefusesOverlongPayload`.
- Ruled: the layer masks received MSTI bridge and port priority octets with
  `0xF0` as well as the codec. Why: a `bpdu.BPDU` built in a test or by a
  caller reaches `receiveMSTIs` without passing `Decode`. Cost if wrong: two
  masks in `S/receive.go`.
- Ruled: a received Hello Time below 1 second is stored as 1 second, and
  the 1-second minimum is marked unverified in code and README. Why: `D2009`
  takes it from IEEE 802.1D Table 17-1, which was not read (Sources). Cost
  if wrong: `receivedHelloTime` and its test.
- Ruled: priority inheritance runs in `Config.Normalize` through
  `inheritPortPriorities`, and `MST.Normalize` and `PVST.Normalize` keep
  their signatures. Why: only `Config` holds the bridge ports an instance
  or VLAN port inherits from. Cost if wrong: one helper in `S/config.go`.
- Ruled: an MSTI record's Proposal is acted on without the CIST-message
  gate that its Agreement needs. Why: `Q2003` 13.26.14 states no such
  condition, and 13.26.10 a) gates the agreement alone. Cost if wrong: one
  condition in `answerProposals`.
- Ruled: `recordAgreement` keeps `agreed` only on a port that is Designated
  when the message arrives. Why: the flag answers that port's own proposal,
  and a stale true would open a port that becomes Designated later without
  a handshake. Cost if wrong: one condition in `recordAgreement`.
- Ruled: an agreement emission sets the Agreement flag from tree state:
  always on an Alternate or Backup port, on a Root port only when the tree
  is in sync, for the CIST and every MSTI record. Cost if wrong: `agrees` in
  `S/transmit.go`.
- Ruled: a superior vector from a sender that conveys a Root role is still
  stored, so the agreement test's "Root sender with a better vector" row
  asserts Discarding and no forward transition rather than the role.
  `Q2003` would treat it as other information. Cost if wrong: the storage
  rule in `applyBPDU`, which no unit of this phase names.
- Ruled: a port stays active for topology change while it is Discarding in
  the same role, so a sync-blocked Designated port that reopens detects no
  second change. Why: `Q2003` Figure 13-19 leaves ACTIVE only on a role
  change or `operEdge`. Cost if wrong: the detect condition in
  `settleTopology`.
- Ruled: only a received TCN sets the acknowledgment, as the unit says,
  and `S/README.md` marks as unverified whether a flag should also set it.
  Why: the figure's NOTIFIED_TC was read as setting it for a flag as well,
  which the unit text does not ask for. Cost if wrong: one condition in
  `applyTopologyChange`.
- Ruled: a `RestrictedTCN` port starts no timer and propagates nothing, and
  still acknowledges a TCN. Cost if wrong: the acknowledgment line in
  `applyTopologyChange`.
- Ruled: a Root port's topology-change report is sent once per call, and
  any other emission on its budget key stands for it, since every BPDU
  carries the flag from the running timers. Outside PVST the report is the
  CIST's BPDU, and toward an STP peer it is a TCN from the CIST alone.
  Cost if wrong: the owed-report mark in `S/topology.go` and its callers.
- Ruled: under PVST a tree's loop-guard mark clears only when a BPDU is
  applied to that tree (`ReceiveSSTP` for a VLAN, `Receive` for VLAN 1's
  tree) or on link down, so an SSTP frame that is not admitted, untracked,
  or PVID-inconsistent leaves the mark. Outside PVST any BPDU on the port
  clears the CIST's mark. The clear moved out of `receiveLink`, which
  cannot tell the tree. Why: the unit names "a BPDU applied to that tree".
  `S/README.md` states the PVST behaviour as modelled on observation.
  Cost if wrong: one condition in `ReceiveSSTP` and the rows of
  `TestReceiveSSTPRunsTheLinkHalfForEveryOutcome`.
- Ruled: `PortInfo.Tree` is a `TreeRef` (`Kind TreeKind`, `ID uint16`),
  since `Tree` already names the PVST configuration type, and an untracked
  port reports an empty kind. The MST decision fact appends `config_id`,
  `regional_root`, `internal_cost`, `remaining_hops`, and an `mstis` list
  inside the BPDU braces. Cost if wrong: `S/info.go`, `S/fact.go`, and the
  fact tests.

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
acknowledgment stops the port's timer.
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

Waves: U1 | U2 | U3 | U4 | U5 | U6

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

- [x] Verifier green for every changed path.
- [x] Every Inventory entry has its failing-first test or is struck.
- [x] `S/README.md` and `B/README.md` name the edition, sources, clauses,
      and limits, and say nothing the code no longer does.
- [x] No non-test file in `layer/stp` exceeds 1,200 lines and no function
      150, unless a comment at its head states why it is one unit.
- [x] No plan label appears in code, comments, or commit messages.
- [x] This plan's `status` is set with an outcome note under its title, and
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
- Open: `applyBPDU` stores a superior vector whatever role the message
  conveys. Unverified: that `Q2003`'s received-message classification
  treats a superior message conveying a Root role as other information, as
  the agreement worker read it. No unit of this phase names it, and
  `S/README.md` does not list it yet.
- Open: an agreement held back by the transmit budget is released only for
  a CIST Root or Alternate port, so an answer only an MSTI owed is dropped
  until the peer's next proposal. `S/README.md`, Not modeled, states it.
- Open: a port that becomes Root does not sync the tree's forwarding
  Designated ports, so it opens by the forward-delay ladder rather than at
  once (the Root case of the state loop in `S/roles.go`). The review found
  that `TestMSTITopologyChangeFlushesOnlyItsOwnVLAN` sees its flush on the
  second of its 16 hellos, so the 32 seconds it runs do not show the ladder.
- Open: a Designated port whose topology-change timer starts sends at its
  next hello, not at once. `D2009` 13.29.11 also sets `newInfo` there, and
  the unit text names Root ports only.
- Open, predates this phase: under MSTP a port that is Designated for an
  MSTI and Root or Alternate for the CIST sends no periodic BPDU
  (`armHelloTimers` in `S/link.go`, the hello loop in `S/advance.go`). With
  sw1 the CIST root, sw2 the MSTI 1 root, and two links between them, the
  peer's MSTI 1 information expires and VLAN 10 forwards on all four port
  ends 90 seconds after convergence, on this branch and on `main`. R2's
  example holds because there one bridge roots every tree. Unverified:
  that `Q2003` Figure 13-13 TRANSMIT_PERIODIC counts MSTI Designated ports.
- Open, Requirement question for U3's unit text: `Config.Normalize` marks
  an inherited instance or VLAN port priority present, so a normalized
  configuration read back from `Switch.Config` or `Fabric.Config` and
  reconfigured with a new bridge port priority keeps the old one for that
  tree (`S/config.go`, `inheritPortPriorities`). Filling `Priority` while
  leaving `PriorityPresent` as written would keep one value for the layer,
  `Canonical`, and `Diff` and re-inherit on each normalization.
## Review gaps

Recorded by the first review of this phase. None of these holds the
verdict.

- `src/common/sim/layer/stp/wire_format_test.go:128`: `addTree` ignoring `treePort.Priority`; fails: none, the test claims the layer reads the resolved priority and builds no layer; class: false test
- `src/common/sim/layer/stp/agreement_test.go:35`: `!lk.pointToPoint` removed from `recordAgreement`; fails: none, the "shared link" row passes; class: false test
- `src/common/sim/layer/stp/agreement_test.go:38`: `order >= 0` replaced by `true` in `recordAgreement`; fails: none, the "Root sender with a better vector" row ends Alternate by role election; class: false test
- `src/common/sim/layer/stp/topology_test.go:314`: arrival-port timer started for a TCN before the `!p.tcActive` skip; fails: none, `b.step(time.Second)` reaches no hello; class: false test
- `src/common/sim/device/vswitch/switch_test.go:7372`: MSTI 1 Root port opening at once instead of by the ladder; fails: none, the flush is read after 32 s; class: false test
- `src/common/sim/fabric/result_test.go:264`: a BPDU still in flight at the cut; fails: none, 1 or 2 pending journeys pass; class: false test
- `src/common/sim/layer/stp/agreement.go:37`: `order >= 0` to `order > 0`; fails: a Root, Alternate, or Backup message with the same vector as the port's; class: gap
- `src/common/sim/layer/stp/agreement.go:73`: `RootPathCost` comparison removed from `cistHolds`; fails: an MSTI agreement whose CIST message names another external cost; class: gap
- `src/common/sim/layer/stp/receive.go:360`: `mirrorAgreement` call removed; fails: a boundary port's MSTIs taking the CIST's agreement; class: gap
- `src/common/net/bpdu/bpdu.go:625`: `version >= mstProtocolVersion` to `version >= 2`; fails: a version 2 frame carrying a whole MST body decodes as RST; class: gap
- `src/common/net/bpdu/sstp.go:157`: a zero Hello Time refused in `DecodeSSTP` alone; fails: an SSTP BPDU with Hello Time 0 decodes; class: gap
- `src/common/sim/layer/stp/vector.go:85`: `addCost` reverted to `+` in the internal CIST and MSTI sums; fails: the 100 against `0xfffffff0` election on an internal MST port and on an MSTI record; class: gap
- `src/common/sim/layer/stp/mst_test.go:402`: comment says the layer checks whether an instance overrides the CIST port priority; class: convention
- `src/common/sim/layer/stp/README.md:345`: "the version number alone never disqualifies a BPDU", while `Decode` refuses type 2 at version 0 or 1; class: convention
- `src/common/sim/layer/stp/README.md:362`: "the layer sends its high nibble" for any port priority, while a CIST, RST, or SSTP Port Identifier carries the low nibble in the port-number bits (`S/layer.go:203,343`, predates this phase), also at `:604`; class: convention
- `src/common/sim/layer/stp/README.md:572`: Hello Time "Fixed at 2 seconds", while the layer sends the configured one; class: convention
- `src/common/sim/layer/stp/transmit.go:234`: `gatherMSTIRecords` puts MSTID bits 9 to 12 in the low nibble of an emitted record's bridge priority before `Encode` masks it; class: hardening
