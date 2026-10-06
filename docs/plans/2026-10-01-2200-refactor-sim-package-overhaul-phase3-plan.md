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

> Implemented. 15 units total. Final 8 units, 2026-10-05T16:30:47Z to 2026-10-05T20:11:58Z.

## Goal

`layer/stp` follows IEEE 802.1D and IEEE 802.1Q for RSTP, MSTP, and
interoperation with legacy STP, and keeps its PVST and SSTP behaviour
consistent with them. The means is fifteen units. Seven have landed: a file
split, one owner for link state, one unit per state machine the inventory
touches, and one that replaces the transmit path with the standard's Port
Transmit machine. Eight bring the code to the five decisions of 2026-10-04
and to what the review of the first seven found. Stop
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
  so a refusal needs a guard in the package of the parent's U11. `Q2003` 13.24.21 makes the
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
- A Version 3 or later BPDU of type 2 with 35 to 101 octets decodes as RST
  whatever its length fields say, and one with more octets than its Version
  3 Length names decodes as MST (`Q2003` 14.4 d) 1) and e)). One of 103
  octets or more whose Version 3 Length names records that are absent is
  refused, as the layer's own rule, since 14.4 gives no reading for them.
  (decided by the user, 2026-10-04)
- R9 stands. An overdue hello is settled before the call's event is
  applied: it sets no flag and advances by whole HelloTimes from the
  instant it was due. `NextWake` gains no wake for it. (decided by the
  user, 2026-10-04)
- An MSTI record's Proposal flag is recorded with no CIST condition. Its
  Agreement flag is tested against the CIST vector the port holds after
  that BPDU's CIST information is stored (`Q2003` 13.26.10 a) and 13.26.14,
  `D2009` 13.29.16 and 13.29.20). (decided by the user, 2026-10-04)
- Under PVST an IEEE-addressed BPDU of any type belongs to VLAN 1's tree,
  so a TCN BPDU starts a change there alone. The layer also models a TCN
  per VLAN at the SSTP address, so a change on another VLAN crosses a link
  in STP mode. Cisco's pages do not document that TCN, so it is modelled
  on observation. (decided by the user, 2026-10-04)
- U6 holds as written: a tree's loop-guard mark clears on a BPDU applied
  to that tree or a link down. A PVID-inconsistent SSTP frame is applied
  to no tree and clears nothing. The test of Correctness 11 changes to
  match. (decided by the user, 2026-10-04)
- U8 to U15 carry the code changes the five decisions above require and
  the review's findings that need no decision. U1 to U7 stay landed, and
  their text states the decided behaviour where a decision changed it.
  Why: the review judges each landed unit by its text, and a unit whose
  text and code disagree names the later unit that closes the difference.
- At exactly 102 octets a type 2 frame of version 3 or above decodes as an
  MST BPDU with no records when its Version 1 Length is 0 and its Version
  3 Length is 64, and as an RST BPDU otherwise. Why: `Q2003` 14.4 d) 1)
  and e) 1) both match 102 octets, and the decision above names 35 to 101
  and 103 or more. Figure 14-1 ends the CIST part at octet 102, so that
  frame is the whole BPDU of a region with no instance, and reading it as
  RST would put two bridges of one region on a boundary.
- A BPDU is applied to a tree when the layer hands it to that tree. An
  IEEE-addressed BPDU of any type that BPDU guard lets through is applied
  to the CIST, and an SSTP BPDU for which `ReceiveSSTP` returns
  `SSTPApplied` to its arrival VLAN's tree. Why: the loop-guard decision
  above turns on the word. `S/README.md:131` already has a BPDU that the
  message-age bound discards clear the mark, and `SSTPApplied` is the
  outcome the layer documents as applied (`S/receive.go:265-267`).
- A VLAN's tree gets the Configuration BPDU at the SSTP address as well as
  the TCN. Why: a TCN leaves a Root port (`D2009` Figure 13-19,
  TRANSMIT_TCN needs `cistRootPort`). On a port in STP mode a tree other
  than VLAN 1's sends nothing today (`S/transmit.go:133-135`), and an SSTP
  BPDU in the RST shape returns the port to RSTP (`S/link.go:234-236`), so
  the tree's information on that link ages out and it has no Root port to
  send a TCN from. `PVID` documents the per-VLAN BPDU at that address. A
  bridge configured to run 802.1D on every port stays unmodelled
  (`S/README.md:465`).
- On a port in STP mode VLAN 1's tree keeps sending to the IEEE address
  alone. Why: it is what landed (`S/README.md:288-294`), and `EXT` sends "a
  standard IEEE TCN BPDU" for VLAN 1. `PVID` also sends VLAN 1's BPDUs to
  the SSTP address, which the layer does only while the port sends RSTP
  (Inventory, Limits).
- An MSTI's sync changes nothing on a boundary port, and counts that port
  synced when the CIST port is Discarding or agreed. Why: at a boundary
  the MSTI's role is the CIST's (`S/README.md:372-373`) and so is its state
  (`S/roles.go:255-260`), so a state the sync writes there is overwritten
  by the next recompute, which counts a forward transition and detects a
  topology change (`S/roles.go:262-266`). `Q2003` 13.26.9 gives every MSTI the CIST's
  `agreed` on such a port, and `D2009` Figure 13-25 enters DESIGNATED_SYNCED
  on `agreed && !synced` without discarding. Where the CIST port forwards
  with no agreement the standard discards the MSTI's port, which the layer
  cannot do (Inventory, Limits).
- These shapes break as well: `bpdu.DecodeSSTP` accepts Configuration and
  TCN BPDUs it refused, `bpdu.EncodeSSTP` writes them, and `bpdu.Decode`
  reads by length a frame it refused.
- U8, U11, and U14 amend the paragraphs of
  `docs/architecture/2026-09-10-virtual-device-direction.md` they make
  false: how a version 3 BPDU decodes (`:583-593`), what clears loop guard
  (`:538-543,690-693`), and what an SSTP BPDU is (`:653-656`). Why: a
  record and the code change together (`docs/doc-style.md:113-114`), and
  the simulation package shape record amends that one, so later plans read
  it. It holds no statement about a port in STP mode for U15 to change.
- An MSTI agreement on a port that stays Designated for the CIST is judged
  against that port's designated vector, as `D2009` Figure 13-20 runs
  `recordAgreement()` in NOT_DESIGNATED with no `recordPriority()`.
  (decided by the user, 2026-10-06)
- A Designated port that a sync cuts requests its frame where the sync
  sets `proposing`, as `D2009` Figure 13-25 (DESIGNATED_PROPOSE) does. The
  carried limit on that proposal ends. (decided by the user, 2026-10-06)
- Bridge detection behavior that predates this phase stays as it is. Each
  case the review named is a Limits entry in `S/README.md`, and a later
  plan brings detection to `D2009` Figures 13-18 and 13-25. (decided by
  the user, 2026-10-06)
- The review ends with a closing round that applies the three decisions
  above, then one re-review. It is accepted unless that re-review finds a
  behavior defect or a test that cannot fail. The six Review gaps items no
  exported call shows stay recorded, as does any new comment or convention
  item. (decided by the user, 2026-10-06)

## Requirements

R1. Every Correctness entry below has a test that fails before its fix.
Example: a PVST Designated port that earned an agreement, then loses and
regains link, stays Discarding until the new peer agrees.

R2. MSTI ports run proposal and agreement. Example: two bridges in one
region on a point-to-point link bring an MSTI Designated port to Forwarding
in the same exchange that brings the CIST there. The first BPDU from a
better CIST sender that carries an MSTI proposal is answered in that call.

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
timers. When the flagged BPDU arrives one second after that port's hello
fell due, with no call in between, the port emits nothing in that call
either, and the TCN BPDU leaves one second later.

R10. Under PVST a topology change on a VLAN other than 1 crosses a link in
STP mode. Example: a PVST bridge has p1 in STP mode and Root for VLAN 10,
kept so by a Configuration BPDU for VLAN 10 at the SSTP address every two
seconds. A flagged SSTP BPDU for VLAN 10 arrives on p3. At VLAN 10's next
hello p1 emits one TCN BPDU at the SSTP address with `Emission.VID` 10, and
nothing at the IEEE address.

## Out of scope

- The switch recording an MSTI transition on a BPDU. Its snapshots around
  a BPDU read the CIST alone (`device/vswitch/switch.go:2626,2660,2672` at
  `7de7c8ea`). The parent's U9 owns that, and `VLANPortInfo` gives the
  view.
- Loading MSTP or PVST from the network model. The parent's U11 owns it.
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
  U8, U14, and U15 cite it as read at `4e3f8264`, the last commit to the
  file on `master`.
- `CISCO`: Cisco's loop guard document, which `S/README.md` already links,
  https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol-stp-8021d/218321-configure-stp-with-loop-guard-and-bpdu-s.html.
  "Understand STP Loop Guard and UDLD Features", document 218321,
  revision 2.0 of 18 December 2023, read from
  https://web.archive.org/web/20260103171445/https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol-stp-8021d/218321-configure-stp-with-loop-guard-and-bpdu-s.html.
  Its Configuration Considerations: "if BPDUs are not received on the trunk
  port for only one particular VLAN, only that VLAN is blocked". Its
  unblock message names a port and a VLAN. It does not say which VLAN's
  BPDU recovers a port, and never mentions SSTP or a PVID-inconsistent
  frame.
- `PVID`: Cisco, "Troubleshoot Spanning Tree PVID- and
  Type-Inconsistencies", document 24063, revision 3.0 of 14 March 2024,
  section "Theory Behind PVID- and Type-Inconsistencies", read from
  https://web.archive.org/web/20241113152806/https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol/24063-pvid-inconsistency-24063.html.
  With native VLAN 1: "VLAN 1 STP BPDUs are sent to the IEEE STP MAC
  address (0180.c200.0000), untagged", and "Non-VLAN 1 STP BPDUs are sent
  to the PVST+ MAC address (also called the Shared Spanning Tree Protocol
  (SSTP) MAC address, 0100.0ccc.cccd), tagged with a corresponding IEEE
  802.1Q VLAN tag". It names no BPDU type, and no TCN.
- `EXT`: Extreme SLX-OS Layer 2 Switching Configuration Guide, 20.1.1,
  March 2020, "TCN BPDUs",
  https://documentation.extremenetworks.com/slxos/SW/20xx/l2config/GUID-FC3E8C8E-3930-4777-825D-3ECD12328F51.shtml.
  A second vendor describing its own PVST+ implementation, not a Cisco
  document: "TCN BPDUs are sent per VLAN", "On a trunk port, a tagged TCN
  BPDU is sent to Cisco or Extreme proprietary MAC address for a tagged
  VLAN", and "the Topology Change and Topology Change Acknowledgment flags
  are set in all configuration BPDUs corresponding to the VLAN for which
  the TCN was received".
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
- An MSTI's Root port is not synced while a boundary port forwards for the
  CIST with no agreement, so it sends no agreement and reaches Forwarding
  through the forward-delay ladder. `D2009` Figure 13-25 would discard that
  MSTI's boundary port (DESIGNATED_DISCARD on `sync && !synced`). The
  layer gives an MSTI no state of its own at a boundary (Decisions).
- On a port in STP mode VLAN 1's tree sends to the IEEE address alone,
  where `PVID` also sends its BPDUs to the SSTP address. A native VLAN
  other than 1 is not modelled: the IEEE-addressed BPDU is VLAN 1's
  (`S/receive.go:179-184`).

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
11. Medium. An SSTP outcome never recomputes roles, and the loop-guard
    clear belongs to no tree. `ReceiveSSTP` returns at
    `S/layer.go:2202,2210,2215,2220` with no recompute, so a port whose
    mark or admission changed keeps its role. `receiveLink` clears the mark
    for any frame (`:1999`), before the frame is judged, so a frame applied
    to no tree releases a held port. `CISCO` blocks and unblocks by VLAN.
    U2 for the recompute, U11 for the clear. Test: a PVST port that is
    loop-inconsistent for VLAN 10 stays Alternate with the reason set when
    it receives an SSTP BPDU with `Admitted` false or an IEEE BPDU. An SSTP
    BPDU whose TLV names another VLAN leaves the mark in place under the
    `pvid-inconsistent` reason. The port leaves Alternate in the call that
    applies an SSTP BPDU to VLAN 10's tree.
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
    intended. It also refuses a frame of 35 to 101 octets whose length
    fields name a whole MST body (`B/bpdu.go:682` at `7de7c8ea`), where
    d) 1) says RST. U3, and U8 for the length bands. Test: a nonzero
    Version 1 Length decodes as RST, version 4 with a whole MST body as
    MST, and a version 2 Configuration BPDU is accepted, each one property
    away from a frame decoded the other way. A 60-octet version 3 frame
    with a zero Version 1 Length and a Version 3 Length of 80 decodes as
    RST.
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
- `TestLoopInconsistentPVSTPortReceivesUnadmittedSSTPLeavesAlternate`
  (`S/layer_test.go:3411` at `7de7c8ea`) and
  `TestReceiveSSTPRunsTheLinkHalfForEveryOutcome` (`:2568`, assertion at
  `:2668-2669`) require a frame applied to no tree to clear the loop-guard
  mark, which pins the old test of Correctness 11. U11.
- Eight tests in `S/transmit_test.go` do not run the example U7 names for
  their rule (U12, U13).
- `TestSSTPDecodeRefusals` (`B/bpdu_test.go:1335`, rows `version below 2`
  and `wire type not 0x02`) and
  `TestPVSTMigrationReachesEveryTreeAndSilencesSSTP`
  (`S/layer_test.go:3128`) pin an SSTP address with no legacy shape. U14,
  U15.

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
whatever the version. A type 2 frame of version 3 or above is read by its
length. With 35 to 101 octets it is an RST BPDU whatever its length fields
say (d) 1)). With 103 or more it is an MST BPDU when its Version 1 Length
is zero and its Version 3 Length names 0 to 64 whole records, octets past
those records ignored (e)), and an RST BPDU otherwise (d) 2) and 3)). One
of 103 octets or more whose Version 3 Length names records that are absent
is refused, as the layer's own rule, since 14.4 gives no reading for them.
At 102 octets d) 1) and e) both match (Decisions) (18). U8 brings the code
to this text.
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
carries its tree's Proposal and Agreement flags. The Proposal flag is
recorded with no CIST condition (`Q2003` 13.26.14, `D2009` 13.29.20). The
Agreement flag is recorded only when the CIST message in the same BPDU
names the CIST root, external cost, and regional root of the vector the
port holds once that BPDU's CIST information is stored (`Q2003` 13.26.10
a), `D2009` 13.29.16). U9 brings the code to this text. On a boundary port the MSTIs
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
CIST and, under MSTP, every MSTI. Under PVST it counts for VLAN 1's tree
alone (Decisions), and U10 brings the code to that. A flag from outside
the region counts for every tree, and a flag from inside for the trees
that set it. A port that is not active
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
on a BPDU applied to that tree or a link down. A frame applied to no tree
clears nothing, an unadmitted or PVID-inconsistent SSTP frame among them,
and `blockReason` reads the tree's own mark. U11 brings the code to those
two sentences. Under MSTP the CIST's mark
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
  its flag, until U15 gives it its own frames. Each tree's role, Proposal, Agreement, Learning, and Forwarding
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
  record transmits, and when it expires in an `Advance` at that instant,
  from the instant of that call. A hello found overdue at the start of a
  call is settled before the call's event is applied: it sets no flag and
  advances by whole HelloTimes from the instant it was due (Decisions).
  U12 brings the code to that sentence. At
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

U8 to U15 bring the code to the decisions of 2026-10-04 and to what the
review of U1 to U7 found. Their line numbers hold at `7de7c8ea`. Page
numbers are the printed ones. Each rule names its source and a test, and a
mutation named for a test is one that test fails under.

### U8. Decode by the length bands of 14.4
Files: src/common/net/bpdu/, src/common/sim/layer/stp/README.md, docs/architecture/2026-09-10-virtual-device-direction.md
After: U7
Change: `bpdu.Decode` reads a type 2 frame of version 3 or above as U3
states. Octets count from the Protocol Identifier, which is
`len(f.Payload) - 3`. Three readings change. A frame of 35 to 101 octets is
an RST BPDU whatever its length fields say, where `readMSTBody` refuses one
whose fields name a whole MST body (`B/bpdu.go:621-626,682`). A frame of
103 octets or more with octets past the records its Version 3 Length names
is an MST BPDU with those records, where `:688` refuses it. A frame of 102
octets is an MST BPDU with no records when its Version 1 Length is 0 and
its Version 3 Length is 64, and an RST BPDU otherwise (Decisions), where
`:682` refuses one whose length names a record. One refusal stays, as the
layer's own: 103 octets or more with records the length names absent.
Sources: `Q2003` 14.4 d) and e), pp. 210 to 211. 14.6 q), p. 212: the
Version 3 Length counts the octets after octet 38. Figure 14-1, p. 214:
octet 102 ends the CIST part. `WS` puts the two length fields at offsets 35
and 36 and the first record at 102 (`:39-40,48`).
The comments on `Decode` and `readMSTBody` (`B/bpdu.go:507-533,617-620,673-677`),
`B/README.md`, "Decoding a version 3 BPDU" in `S/README.md` (`:307-331`),
and the `Decode` paragraph of the virtual device direction record
(`:583-593`) state the bands, the 102-octet reading, and the refusal as the
layer's own. `B/README.md` stops claiming a UNH-IOL cross-check that no
fixture holds (`:60`), a Hello Time floor in the codec (`:68,73`), and an
RST reading for every truncated body (`:64`).
Tests: `TestDecodeVersion3ByLength` in `B/bpdu_test.go`. Its rows are cut
from U3's R5 byte literal by truncating it, padding it with zero octets,
and rewriting the two length fields. Each row differs in one property from
a row decoded the other way.

| Octets | Version 1 Length | Version 3 Length | Decodes as |
| --- | --- | --- | --- |
| 34 | none | none | refused |
| 35 | none | none | RST, for versions 3, 4, and 255 |
| 60 | 0 | 80 | RST |
| 101 | 0 | 64 | RST |
| 102 | 0 | 64 | MST, no records |
| 102 | 0 | 80 | RST |
| 102 | 1 | 64 | RST |
| 103 | 0 | 64 | MST, no records |
| 117 | 0 | 80 | refused |
| 118 | 0 | 80 | MST, one record |
| 119 | 0 | 80 | MST, one record |
| 118 | 1 | 80 | RST |
| 118 | 0 | 81 | RST |
| 118 | 0 | 1104 | RST |

An RST row asserts a nil `ConfigID` and the CIST fields of the prefix. A
second test decodes every length from 0 to 150 octets at version 3 and
requires a result or `ErrUnsupported`, never a panic. Mutation: the call to
`readMSTBody` restored for a frame under 102 octets. Nothing here compares
a frame with a device capture.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/bpdu src/common/sim/layer/stp docs/architecture/2026-09-10-virtual-device-direction.md`

### U9. Agreement and proposal in the standard's order
Files: src/common/sim/layer/stp/
After: U8
Change: four rules in `S/receive.go` and `S/roles.go`.

- A1, an MSTI proposal. A stored MSTI record with the Designated role and
  the Proposal flag is a proposal whatever the CIST part of its BPDU says.
  Today `receiveMSTIs` keeps it only under `cistConsistent`
  (`S/receive.go:166-170`), and a port that held no CIST information from
  the sender fails that test. Source: `Q2003` 13.26.14, p. 194. `D2009`
  13.29.20, p. 66. Test `TestMSTIProposalNeedsNoCISTMatch`: an MST bridge
  has p2 Designated and Forwarding for MSTI 1 with no agreement. The first
  MST BPDU on p1 comes from its own region with a better CIST root, and its
  MSTI 1 record has a better regional root, the Designated role, and the
  Proposal flag. In that call p2 returns to Discarding for MSTI 1, p1 is
  Root and Forwarding for it, and the frame p1 emits has the Agreement flag
  in its MSTI 1 record. That case is the second example of R2, and A2 is
  what makes its CIST part match. A second case pins A1 alone: p1 holds
  CIST information, and a BPDU from another bridge of its region carries a
  worse CIST vector with another CIST root, so its CIST part is not stored,
  and an MSTI 1 record better than the one p1 holds, with the Designated
  role and the Proposal flag. p2 returns to Discarding for MSTI 1 and the
  frame p1 emits has the Agreement flag in its MSTI 1 record. The same
  record with the Root role syncs nothing, and so does a CIST proposal from
  a Root-role sender. Mutation, against the second case: the
  `cistConsistent` test restored around the proposal.
- A2, an MSTI agreement. The Agreement flag of an MSTI record counts only
  when the CIST part of its BPDU matches the CIST vector the port uses for
  that role. A CIST port that remains Designated compares the BPDU's CIST
  root, external cost, and regional root with its designated vector. A Root
  port keeps comparing with the vector it holds once the BPDU's CIST part is
  stored. Source: `Q2003` 13.26.10 a),
  p. 193. `D2009` 13.29.16, p. 65, and the NOTE under 13.28.24, p. 60: "The
  state machines ensure that the CIST parameters from received BPDUs are
  processed and updated prior to processing MSTI information." Test
  `TestMSTIAgreementIsJudgedAfterTheCISTIsStored`: a port that is
  Designated and Discarding for MSTI 1 on a point-to-point link receives
  its first MST BPDU, with a better CIST root and an MSTI 1 record that has
  the Root role, the Agreement flag, and a worse vector than the port's.
  The port is Forwarding for MSTI 1 after that call. Three BPDUs whose CIST
  part is not stored record no agreement, one for each of another CIST
  root, another external cost, and another regional root than the port
  holds. An MSTI record that is not stored still records its agreement
  when the CIST part matches. A Designated port and a Root port each judge
  the CIST part against their own vector. Test `TestMSTIAgreementUsesTheCISTPortVector`
  covers both cases. Mutation: the CIST comparison
  selected from the port's role and designated vector rather than the newly
  stored BPDU alone.
- A3, role before state. A received agreement sets `agreed` and clears
  `proposing`, and changes no state. The port advances in
  `updatePortStates`, after `assignRoles` has selected its role for this
  BPDU, and a topology change is detected there
  (`S/roles.go:294-296,307-311`). Today `recordAgreement` forwards a port
  by the role it had before the BPDU (`S/receive.go:76-84`), and the copy
  to a boundary port's MSTIs does the same (`:486-496`). Source: `D2009`
  Figure 13-20, p. 75: SUPERIOR_DESIGNATED runs `recordAgreement()` and
  then sets `reselect = TRUE; selected = FALSE`. Figure 13-25, p. 79: "All
  transitions, except UCT, are qualified by `&& selected && !updtInfo`."
  Test `TestAgreementWaitsForRoleSelection`: p1 is Designated and
  Discarding on a point-to-point link, and p2 is Designated and Forwarding
  with no agreement. An RST BPDU on p1 comes from a Designated sender with
  a better root, the Agreement flag set, and the Proposal flag clear. After
  the call p1 is Root and Discarding, its `ForwardTransitions` and the
  bridge's `TopologyChanges` are unchanged, `Effects.Flush` is empty, and
  no emission carries the topology-change flag. On an MST bridge the same
  BPDU leaves p1 Discarding for MSTI 1 as well. An agreement from a
  Root-role sender with a worse vector on a boundary Designated port brings
  it to Forwarding for the CIST and for MSTI 1 in that call. Mutation: the
  state change restored in `recordAgreement`. Nothing here shows that a
  boundary port's MSTIs take the CIST's `agreed` (`S/receive.go:486`),
  since their state follows the CIST's either way. Review gaps keeps that
  item.
- A4, a sync at a boundary. An MSTI's sync changes nothing on a boundary
  port, and `isSynced` reads the CIST port's state and `agreed` for it
  (Decisions). It reads the CIST port's and not the MSTI's copy because
  the copy is written only when a BPDU arrives on the port
  (`S/receive.go:479-486`). A sync of the CIST gives each MSTI's boundary
  port the CIST port's new state in that call. Today `syncTree` returns an MSTI's
  boundary port to Discarding while the CIST port forwards
  (`S/receive.go:33-42`). The next recompute brings it back, counts a
  forward transition, and detects a change (`S/roles.go:255-269`). Source:
  `Q2003` 13.26.9, p. 193. `D2009` Figure 13-25, p. 79. The limit is the
  layer's own (Inventory, Limits). Test `TestMSTISyncLeavesABoundaryPort`:
  on an MST bridge p1 is internal and Root for the CIST and MSTI 1, and p2
  is a boundary port that reached Forwarding as Designated through the
  ladder. An MST BPDU on p1 repeats the CIST information, and its MSTI 1
  record has the Designated role and the Proposal flag. Over that call and
  the next `Advance`, `VLANPortInfo` shows p2 Forwarding for MSTI 1, no
  flush names p2, and no emitted MSTI 1 record carries the
  topology-change flag. The frame p1 emits has the Agreement flag clear in
  its MSTI 1 record. Where p2 holds the CIST's agreement, that flag is set
  and p2 still forwards. A CIST proposal on a boundary Root port returns a
  forwarding, unagreed Designated port to Discarding for the CIST and for
  MSTI 1 in that call. Mutation: the boundary test removed from
  `syncTree`.

`S/README.md` states A4 and its limit in its MSTP and Limits sections, and
names the figures of A3 for Port Role Transitions (`:440`).
Tests: the four named above and the cases listed with them, in a new
`S/agreement_test.go` (package `stp_test`). They drive `Receive` and read
`Effects`, `PortInfo`, and `VLANPortInfo`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim`

### U10. A TCN BPDU's tree and its propagation
Files: src/common/sim/layer/stp/
After: U9
Change: two rules in `Receive` and `S/topology.go`.

- N1, the tree. Under PVST a TCN BPDU at the IEEE address starts a change
  on VLAN 1's tree alone. Under MSTP it counts for the CIST and for every
  MSTI the port is active for. The loop at `S/receive.go:220-228` takes the
  `l.mst != nil` test that `:520` has. Source: Decisions and `PVID` for
  PVST. `Q2003` 13.26.19, p. 194, and `D2009` 13.29.13, p. 63, for MSTP.
  Test `TestIEEETCNUnderPVSTIsVLAN1s`: a PVST port active for VLANs 1 and
  10 receives a TCN BPDU through `Receive`. `Effects.Flush` names VLAN 1 on
  the other active ports and never VLAN 10, and no frame for VLAN 10
  carries the flag in that call or at its next hello. On an MST bridge the
  same BPDU puts the flag in the MSTI 1 record of the frame each other
  active internal port emits in that call. Mutation: the mode test removed
  from the loop.
- N2, every TCN propagates. On an active port a TCN BPDU sets the
  acknowledgment when the port is Designated, starts the port's own timer
  only when it is stopped, and propagates to the tree's other active ports
  whether or not that timer ran. Each of those is flushed and starts a
  stopped timer, as C1 of U7 has it. Today `initiateTopologyChange`
  propagates only where it starts the receiving port's timer
  (`S/topology.go:29-37`), so a second TCN BPDU within Max Age plus Forward
  Delay of the first flushes nothing. Source: `D2009` Figure 13-28, p. 82:
  NOTIFIED_TCN runs `newTcWhile()` and passes to NOTIFIED_TC, which runs
  `setTcPropTree()`, and PROPAGATING runs `newTcWhile(); fdbFlush = TRUE`.
  13.29.11, p. 63. 13.29.26, p. 67. Test `TestEveryTCNPropagates`: p1 is
  Designated and in STP mode, p2 is Root, p3 is Designated, and all three
  forward and none is an edge. A TCN BPDU on p1 flushes p2 and p3, and each
  emits one flagged frame. A second TCN BPDU on p1 ten seconds later, while
  p1's timer runs and theirs have stopped, flushes them again, and each
  emits one flagged frame in that call. `TopologyChanges` moves once, with
  the first. A TCN BPDU on an Alternate port flushes nothing, and no
  emission in that call or at the next hello carries the flag. Mutation:
  the propagation put back under the test of the receiving port's timer.

The comment on `initiateTopologyChange` says what it does (`S/topology.go:24-26`).
`S/README.md` states both rules under "What a topology change flushes"
and in the Topology Change line of its Standards section (`:442`).
Tests: the two named above, in a new `S/tcn_test.go` (package `stp_test`).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim`

### U11. Loop guard clears per tree
Files: src/common/sim/layer/stp/, docs/architecture/2026-09-10-virtual-device-direction.md
After: U10
Change: `receiveLink` clears no mark (`S/link.go:187-197`). `Receive`
clears the CIST port's mark once `receiveLink` lets the frame through,
whatever the BPDU's type. `ReceiveSSTP` clears the arrival tree's mark on
the path that returns `SSTPApplied`, and no other outcome clears one. A
link down clears every tree's, as today (`S/link.go:83`). `blockReason`
reports `loop-inconsistent` from the tree's own mark under PVST and from
the CIST's under MSTP, where today it reads the CIST's for every tree
(`S/info.go:93`). Source: Decisions. `CISCO`, Configuration Considerations
and Feature Description. Modelled on observation, since `CISCO` does not
say which VLAN's BPDU recovers a port.
`S/README.md` states the per-tree clear and where `BlockReason` comes from
in its Guards table (`:127`), the paragraph under it (`:129-136`), and the
PVST section (`:241,250-251`). The comment on `blockReason`
(`S/info.go:76-86`) follows. The virtual device direction record states
the same where it has any BPDU clear the guard (`:538-543`) and has the
clear run on both sides of a PVST boundary (`:690-693`).
Tests: the test of Correctness 11, as
`TestLoopGuardClearsOnABPDUAppliedToItsTree`, which replaces
`TestLoopInconsistentPVSTPortReceivesUnadmittedSSTPLeavesAlternate`.
`TestReceiveSSTPRunsTheLinkHalfForEveryOutcome` asserts that the marked
tree keeps its mark under `SSTPBoundary`, `SSTPNotAdmitted`, and
`SSTPUntrackedVLAN`, and that `SSTPApplied` clears the arrival VLAN's
alone. Its `SSTPGuarded` row keeps the outcome and reason checks it has,
since a guarded port never holds a mark (`S/layer_test.go:2556-2567`).
Under `SSTPPVIDInconsistent` the reason reads `pvid-inconsistent` and both
marks give Alternate (`S/info.go:89-94`, `S/roles.go:210-220`), so a case in
`S/link_state_internal_test.go` reads the tree's mark after that outcome.
`TestBlockReasonReadsTheTreesOwnMark`: on a PVST
port whose VLAN 1 information expired while VLAN 10's did not, `PortInfo`
reports Alternate with `loop-inconsistent`, and `VLANPortInfo` for VLAN 10
reports the Root role and no reason. On an MST bridge an MSTI's port
reports the CIST's mark. Mutation: the clear restored in `receiveLink`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim docs/architecture/2026-09-10-virtual-device-direction.md`

### U12. The requests the Port Transmit machine makes
Files: src/common/sim/layer/stp/
After: U11
Change: four rules of U7 that the code does not meet, in `S/transmit.go`,
`S/topology.go`, `S/link.go`, and `S/portstate.go`. U7 holds each rule's
text. The four tests named here are among the eight of Inventory, Tests.

- T5, an overdue hello. Every call first settles each record whose hello
  fell due before the call's instant: `helloWhen` advances by whole
  HelloTimes from the instant it was due until it is no longer before the
  call's, and no flag is set. The call's event is applied after that. A
  hello due at the instant of the call is judged in the pass, as today.
  `NextWake` is unchanged. It reports a hello only while its expiry would
  set a flag (`S/advance.go:46-48`), so a caller that advances at every
  wake finds no flag-setting hello overdue. Today the pass judges an
  overdue hello with the state the event left and restarts it from the
  call (`S/transmit.go:81-103`), so a Root port in STP mode sends a TCN
  BPDU in the call that starts its timer. Source: Decisions. `D2009`
  Figure 13-19, p. 74: IDLE runs `helloWhen = HelloTime`, and
  TRANSMIT_PERIODIC is entered on `helloWhen == 0`. Figure 13-15, p. 71:
  every tick decrements `helloWhen`, whatever the port's role. `UNH`
  RSTP.op.2.3 Part A waits two seconds for the TCN BPDUs. Test
  `TestOverdueHelloIsSettledBeforeTheEvent`: the second example of R9. A
  Root port p1 in STP mode has its hello due at an instant H, and no call
  follows until a flagged RST BPDU arrives on p3 one second after H. p1
  emits nothing in that call, `NextWake` is no later than H plus two
  seconds, and the `Advance` at H plus two seconds emits p1's first TCN
  BPDU. Mutations: the settle step removed, and the hello restarted from
  the call's instant.
  `TestHelloRestartsWhenThePortTransmits` runs the R7 example, where it
  transmits today only on the hello's own phase (`S/transmit_test.go:158-172`).
  Mutation: `S/transmit.go:64` deleted.
- T3, the MSTI flag on a port that does not send RSTP. It sends nothing
  there and stays set, and a TCN or Configuration BPDU clears the CIST flag
  alone. Today `transmitRequested` answers for the MSTI flag whatever
  `sendRSTP` is (`S/transmit.go:109-118`), `transmitBPDU` then builds a TCN
  or Configuration BPDU (`:137-144`), and the pass clears both flags
  (`:65-66`). Source: `D2009` Figure 13-19, p. 74: TRANSMIT_RSTP needs
  `sendRSTP`, and TRANSMIT_TCN and TRANSMIT_CONFIG need `newInfo` and clear
  it alone. Test `TestMSTIFlagAloneSendsOnlyInsideTheRegion` runs the
  example of T3 on a bridge with an internal port and a boundary Root
  port, where its fixture today has no boundary port
  (`S/transmit_test.go:103-116`). Mutation: the `mstiMasterPort` test
  removed from `transmitRequested`. Test
  `TestMSTIFlagOnAPortInSTPModeWaits`: p2 is in STP mode, and Designated
  and Discarding for the CIST. An MST BPDU on the internal port p1 changes
  MSTI 1's root and repeats the CIST information, and p2 emits nothing in
  that call. p2 sends its Configuration BPDU at its hello. Once MigrateTime
  has passed, an MST BPDU of p2's own region with no Proposal flag makes p2
  Alternate for the CIST, and p2 emits one MST BPDU in that call.
  Mutations: `sendRSTP` not tested for the MSTI flag, and both flags
  cleared after a Configuration BPDU.
- C2, detection on a port that does not send RSTP. It sets its tree's flag
  as any detecting port does. Today `detectTopologyChange` requests only
  where the port sends RSTP (`S/topology.go:45-47`), and no test in the
  package fails with that request deleted. Source: `D2009` Figure 13-28,
  p. 82: DETECTED ends with `newInfoXst = TRUE`, after `newTcWhile()` and
  `setTcPropTree()`. Figure 13-19, p. 74: TRANSMIT_TCN is entered on
  `!sendRSTP && newInfo && cistRootPort`. Test
  `TestDetectionTransmitsOnTheDetectingPort` runs the two cases of C2,
  each at an instant when no hello is due on the detecting port, where its
  one case today coincides with a hello (`S/transmit_test.go:240-253`). A
  Designated port that an agreement brings to Forwarding emits one flagged
  frame in that `Receive`. A Root port in STP mode that ends the ladder
  emits one TCN BPDU in that `Advance`. Mutation: the `sendRSTP` test kept
  around detection's request. The comment on `detectTopologyChange`
  (`S/topology.go:40-42`) says what it does.
- T6, a port coming up. A record whose port is down or disabled by BPDU
  guard holds both flags and a zero count, from construction on, and
  `LinkChange` makes no request of its own. Today the pass and
  `clearTransmit` clear both flags of such a record
  (`S/transmit.go:25-33`, `S/portstate.go:127-136`), and `LinkChange`
  requests for the CIST's record alone (`S/link.go:166`), so under PVST
  every other VLAN's tree first sends at its hello. Source: `D2009` Figure
  13-19, p. 74: TRANSMIT_INIT is entered while the port is not enabled,
  sets `newInfo` and `newInfoMsti`, and zeroes `txCount`.
  Decisions for a record for each VLAN's tree. Test
  `TestPortComingUpTransmitsOnce` keeps its two cases and adds three. On a
  PVST bridge with VLANs 1, 10, and 20 the `LinkChange` that brings p1 up
  emits VLAN 1's two frames and one SSTP frame each for VLAN 10 and VLAN
  20. On an MST bridge it emits one MST BPDU. A port that held a request
  at the hold count, went down, and came up emits one frame in that
  `LinkChange`. Mutation: the flags cleared for a port that is down.
  `TestLinkDownClearsHeldTCNAndAcknowledgment`
  (`S/link_state_internal_test.go:117`) keeps its assertion, since a zero
  count reports no wake.

`S/README.md` states the four in its Emission section (`:260-294`), and its
Standards section names `D2009` Figures 13-19 and 13-28 for the transmit
and topology-change machines, the two request sites that are the layer's
own, and the limits U7 carries (`:441-442,445-453`).
Tests: the six named above, in `S/transmit_test.go`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim`

### U13. The transmit tests run the examples of their rules
Files: src/common/sim/layer/stp/
After: U12
Change: the intended change is to `*_test.go` alone. Four tests in
`S/transmit_test.go` run less than the example U7 names for their rule,
and pass under the mutation given here. Each is rewritten to that example.
A rewritten test that fails on the landed code shows its rule unmet. Only
then does the unit change `S/transmit.go`, `S/topology.go`, or
`S/advance.go`, to the rule as U7 states it, and its commit says which
rule.

| Test | What it runs today | What it runs after | Mutation |
| --- | --- | --- | --- |
| `TestTransmitOnceFromFinalState` (`:33`) | an RSTP bridge, one tree, and no frame content beyond p1's role | the R6 example, and an `Advance` before any hello that emits nothing | a sent record keeps its flags (`S/transmit.go:65-66` deleted) |
| `TestHeldRequestIsBuiltAtRelease` (`:77`) | an RSTP port, asserting the frame's type | the three cases of T2: the R8 example, the Root port that migrates to STP before the count falls, and the port that is Alternate for the CIST and Root for MSTI 1 | the Root port in STP mode answers with a Configuration BPDU (`S/transmit.go:138-139`) |
| `TestTopologyChangeLeavesAtOnceAndTwice` (`:219`) | two shared-link ports that end the ladder together | the R7 example, the second flagged BPDU one second later, the last flagged frame within HelloTime plus one second, and the PVST case of C1 | a running timer restarts and requests again (`started` always true at `S/topology.go:67`) |
| `TestLegacyPortReportsAChangeAtItsHello` (`:255`) | one port that is Designated by the time it is checked, asserting a Configuration BPDU | the first example of R9, with the Configuration BPDUs that keep the port Root, and no TCN BPDU after 35 seconds | the Root-port term removed from `setHelloRequests` (`S/transmit.go:87`) |

Three more tests gain the half of their rule they leave out.
`TestTopologyChangeFlagIsPerTree` (`:271`) gets a fixture of its own and
reads octet 5 at index 7 of the payload and each MSTI record's flags octet
at index 105 plus 16 for each record before it (`B/bpdu.go:724,732`), with
no call to `Decode`. `TestEmissionsLeaveByTreeThenPort` (`:194`) adds the
first case of T7. `TestAcknowledgmentLeavesInTheNextConfigurationBPDU`
(`:275`) adds the last two cases of C5, and fails with `p.tcAck = false`
deleted at `S/transmit.go:322`.
Tests: the seven above. Each commit quotes the mutation and its failing
line.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/stp`

### U14. Legacy BPDUs at the SSTP address, and receiving them
Files: src/common/net/bpdu/, src/common/sim/layer/stp/, src/common/sim/device/vswitch/, docs/architecture/2026-09-10-virtual-device-direction.md
After: U13
Change: `bpdu.EncodeSSTP` and `bpdu.DecodeSSTP` gain two shapes, chosen by
`BPDU.Type` and by the wire type. A Configuration BPDU keeps the 50-octet
layout of the RST shape (`B/sstp.go:35-42`) with version 0, type `0x00`,
and the flags masked to the two `Encode` keeps (`B/bpdu.go:386`). `Q2003`
14.4 a) does not test its version, so the refusal of a version below 2
(`B/sstp.go:129-134`) applies to type 2 alone. A TCN BPDU is the 8-octet
LLC and SNAP header, a zero Protocol Identifier, version 0, and type
`0x80`, with no TLV, padded to the 46 octets `Encode` pads a TCN to
(`B/bpdu.go:322,372`) under a length field of 12. `DecodeSSTP` accepts a
TCN of 12 octets or more and returns VLAN 0 for it. Every other wire type
stays refused.
`ReceiveSSTP` takes both. A Configuration BPDU goes the way an RST BPDU
goes today, PVID test included. A TCN BPDU carries no VLAN, so it runs the
link half, the admission and tree tests, and no PVID test, and it neither
sets nor clears `pvidInconsistent`. Then the arrival VLAN's tree takes it
as the CIST takes one in U10: on an active port it sets the acknowledgment
when the port is Designated, starts a stopped timer, and propagates on
that tree alone. The outcome is `SSTPApplied`. On a bridge that does not
run PVST the outcome stays `SSTPBoundary`. Either shape migrates the port
to STP as its IEEE form does (`S/link.go:231-233`).
The switch's `stp.sstp.vlans` fact compares the TLV's VLAN with the arrival
VLAN for every SSTP BPDU (`device/vswitch/switch.go:2813-2816,2915-2920`),
which would print `tlv=0` and `consistent=false` for a TCN. For a TCN it
prints `tlv=none,arrival=<vid>` and no `consistent` term.
Sources: Decisions, modelled on observation. `PVID` for the per-VLAN BPDU
at the SSTP address. No Cisco document read names a TCN there. `EXT`, a
second vendor, sends "a tagged TCN BPDU" to that address for a tagged
VLAN. `WS` reads the TLV at offset 36 after a Configuration BPDU under
the Cisco PID (`:50,622-624,1360`), and ends a TCN at four octets before
any TLV (`:61,511-514`). `WS` shows what a dissector reads. It does not
show that a device sends a TCN with no TLV, which stays unverified.
`B/README.md` and the PVST section of `S/README.md` name both shapes, mark
PVST and SSTP as modelled on observation, and name `CISCO`, `PVID`, and
`EXT` with what each was read for. The virtual device direction record
names the three shapes where it calls an SSTP BPDU an RST BPDU
(`:653-656`).
Tests: `B/bpdu_test.go` gains a byte literal for an SSTP Configuration
BPDU on VLAN 10 with both flags set and every multi-octet field distinct
in each octet, each field commented with its `WS` offset. `DecodeSSTP`
returns the fields and `EncodeSSTP` the bytes, and a BPDU with role bits
set encodes with them masked. A second literal is the TCN: `AA AA 03 00 00
0C 01 0B 00 00 00 80` and 34 zero octets. It decodes as a TCN with VLAN 0,
its first 12 octets alone decode too, and its first 11 are refused. In
`TestSSTPDecodeRefusals` the row `wire type not 0x02` takes type `0x81`,
and the row `version below 2` gains a neighbour, a version 0 Configuration
BPDU that decodes. An SSTP frame with a Hello Time of zero decodes.
`S/layer_test.go`: an SSTP TCN for VLAN 10 on a port that is Designated
and Forwarding for VLAN 10 flushes VLAN 10 on that tree's other active
ports and leaves `TopologyChanges` and VLAN 1's frames alone. With
`Admitted` false it returns `SSTPNotAdmitted` and flushes nothing, for a
VLAN with no tree `SSTPUntrackedVLAN`, and on an RSTP bridge
`SSTPBoundary`. An SSTP TCN on a port that is PVID-inconsistent for VLAN
10 leaves the reason set. An SSTP Configuration BPDU whose TLV names
another VLAN returns `SSTPPVIDInconsistent`. Each of the two shapes turns
`PortInfo.SendRSTP` false once MigrateTime has passed. On a PVST bridge
with VLANs 1 and 10, a Configuration BPDU for VLAN 10 with a better root
every two seconds keeps p1 Root for VLAN 10 past ten seconds.
`device/vswitch/switch_test.go`: a TCN at the SSTP address tagged for VLAN
10 on a PVST switch flushes VLAN 10's entries on another port, traces as
applied, and carries the fact `tlv=none,arrival=10`. An RST BPDU there
keeps the fact it has.
Mutations: the PVID test run for a TCN, and the TCN applied to VLAN 1's
tree. Nothing here compares either frame with a device capture.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/bpdu src/common/sim docs/architecture/2026-09-10-virtual-device-direction.md`

### U15. A VLAN tree's own frames on a port in STP mode
Files: src/common/sim/layer/stp/
After: U14
Change: under PVST, on a port that does not send RSTP, every VLAN's tree
answers its flag as T2 has VLAN 1's answer it: a TCN BPDU when the tree's
port is Root, a Configuration BPDU when it is Designated, and nothing
otherwise. A tree other than VLAN 1's sends its frame to the SSTP address
and names its VLAN in `Emission.VID`. VLAN 1's tree sends to the IEEE
address alone, as today (Decisions). `transmitBPDU` loses its PVST
exception (`S/transmit.go:133-135`), `frames` builds the SSTP frame for
either mode (`:176-187`), and a hello sets a tree's flag whatever the port
sends (`:86`, `S/advance.go:182`). The comment on `frames` (`:155-165`)
follows. C3 and C5 of U7 then hold for each tree: a
running timer shows at the tree's hello, and a tree's acknowledgment
leaves once, in its next Configuration BPDU on that port.
Sources: Decisions, modelled on observation. `PVID`. `EXT`: "TCN BPDUs are
sent per VLAN", and the acknowledgment is set "in all configuration BPDUs
corresponding to the VLAN for which the TCN was received". `D2009` Figure
13-19, p. 74, for which role sends which BPDU.
`S/README.md` states this in its Emission section, in place of the two
paragraphs that say a tree other than VLAN 1's sends nothing there
(`:272-276,288-294`). Its "Not modeled" entry for 802.1D per VLAN (`:465`)
names what is left: a bridge configured to run it on every port.
Tests, in `S/transmit_test.go`. `TestVLANTreeReportsAChangeInSTPMode`: the
R10 example. p1 emits nothing in the call that receives the flagged BPDU. At
VLAN 10's next hello it emits one frame, to the SSTP address with
`Emission.VID` 10, whose payload is the TCN literal of U14. It emits one
at each hello until a Configuration BPDU for VLAN 10 with the
acknowledgment flag arrives, and none after. No frame leaves for VLAN 1.
`TestVLANTreeAcknowledgesInSTPMode`: p2 is Designated and Forwarding for
VLAN 10 in STP mode. At its hello it emits a Configuration BPDU at the
SSTP address whose TLV names VLAN 10. After an SSTP TCN for VLAN 10 its
next such BPDU has bits 8 and 1 of the flags octet set, and the one after
has bit 8 clear. `TestPVSTMigrationReachesEveryTreeAndSilencesSSTP`
(`S/layer_test.go:3128`) takes the new frames and a name that says so.
Mutations: the exception restored in `transmitBPDU`, and a tree's TCN sent
to the IEEE address.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim`

Waves: U1 | U2 | U3 | U4 | U5 | U6 | U7 | U8 | U9 | U10 | U11 | U12 | U13 | U14 | U15

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
- [x] Every rule of U8 to U15 has its named test, and the commit that adds
      the test quotes the mutation and the failing line.
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
- Whether the parent's U11 loads MSTP and PVST stays with that unit, which
  owns `netmodel`.
  The schema carries MSTP (`PROTOCOL_VERSION_MSTP` and the `Mst*` messages
  under `spec/proto/flowseer/net/protocol/stp/v1/`) and nothing for PVST,
  and `netmodel` skips every version but RSTP (`netmodel/netmodel.go:1279`).
- The fabric never uses the switch's point-to-point default: it does not
  call `Switch.Start`, reports each port with the resolved value
  (`fabric/fabric.go:860-862`), and injects emissions only after every port
  is reported (`:873-875`). The default of true serves a switch run alone
  (`device/vswitch/switch.go:2956-2968` at `7de7c8ea`), which the parent's
  U9 owns.
- U7's rules T2 (a port that sends RSTP transmits whatever its role), T6,
  and C2 rest on `D2009` alone, a working draft. `UNH` neither supports nor
  contradicts them, and the published IEEE Std 802.1Q-2011 is unverified.
  The same holds for A3 of U9 and N2 of U10, which rest on `D2009` Figures
  13-20, 13-25, and 13-28.
- Unverified, the length bands: whether a later edition corrects "less
  than 103" in `Q2003` 14.4 d) 1), which overlaps e) 1) at 102 octets. Only
  the 2003 text was read, and the reading at 102 octets is this plan's.
- Unverified, the overdue hello: the standard decrements `helloWhen` every
  second and has no expiry that waits for a call, so settling one before
  the event is the layer's own rule for its event-driven clock. The Port
  Transmit figure of IEEE Std 802.1Q-2011 was not read.
- Unverified, the MSTI flags: the wording of `recordProposal` and
  `recordAgreement` in IEEE Std 802.1Q-2011. `Q2003` and `D2009` agree that
  the proposal has no CIST condition and the agreement has one.
- Unverified, the TCN at the SSTP address. No Cisco document read names a
  TCN BPDU on a trunk at either address. That a device sends one for each
  VLAN, that it carries no TLV, and that the acknowledgment returns in that
  VLAN's Configuration BPDU rest on `EXT`, a second vendor, and on what
  `WS` reads. No device capture was found. `PVID` sends VLAN 1's BPDUs to
  the SSTP address as well, which the layer does not do on a port in STP
  mode (Limits).
- Unverified, the loop-guard clear. `CISCO` blocks and unblocks by VLAN and
  does not say that the recovering BPDU must be that VLAN's. It says
  nothing of an SSTP BPDU on a VLAN the port does not admit or of a
  PVID-inconsistent frame. `CISCO` and `PVID` were each read from one
  Internet Archive capture, so a later revision is unverified.
- Carried, and not changed by U7: a Designated port whose root information
  changes while it forwards or holds an agreement sends the new information
  at its next hello. `D2009` Figure 13-20 (UPDATE) with 13.29.33 j) and k)
  requests a frame at once, and `UNH` RSTP.op.4.6 drives its hold-count
  test that way. It is a role-transition request, outside the
  topology-change emission (Decisions).

## Review gaps

Line numbers hold at `949832ea`. Each item below survived the gap pass: the
fix worker found no sequence of exported calls that shows the difference,
so closing one needs either an internal test or the removal of a branch
nothing reaches, which is a source change.

- src/common/sim/layer/stp/receive.go:164: `rec.RemainingHops <= 1` to `<= 0` or to `<= 2`; fails: a record with one remaining hop is discarded and one with two is stored
- src/common/sim/layer/stp/receive.go:524: boundary MSTIs no longer take the CIST's `agreed`; fails: an MSTI Designated port on a boundary forwards on the CIST's agreement
- src/common/sim/layer/stp/roles.go:200: boundary branch keeps `agreed` across a role change; fails: an MSTI boundary port that changes role must earn a new agreement
- src/common/sim/layer/stp/roles.go:313: `!link.edge` dropped; fails: an edge port that starts forwarding raises no topology change
- src/common/sim/layer/stp/receive.go:559: `mp.tcActive` dropped; fails: a boundary flag leaves an inactive MSTI alone
- src/common/sim/layer/stp/roles.go:19: boundary branch of `isSynced` removed; fails: the frame p1 emits has the Agreement flag clear in its MSTI 1 record while the boundary port forwards unagreed
