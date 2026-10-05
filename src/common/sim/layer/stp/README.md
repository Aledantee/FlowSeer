# stp

Package `stp` implements the Rapid Spanning Tree Protocol and Multiple
Spanning Tree Protocol (IEEE 802.1D-2004, carried into 802.1Q clause 13) for
the virtual switch. It elects a root bridge, assigns port roles and states,
and answers the bridge's forwarding gate. A `Layer` with no `MST` configured
runs plain RSTP with the CIST as its only tree; configuring `MST` adds region
membership, MST instances, boundary roles, and hop aging on top of it.

The layer runs deterministically in memory without background goroutines or wall
clocks. Time advances through explicit, time-stamped calls to `LinkChange`,
`Receive`, `Advance`, and `Mcheck`.

## Example

Two bridges come up on a point-to-point link, exchange a proposal and an
agreement, and settle into Designated and Root.

```go
package main

import (
	"fmt"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func main() {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Build()
	if err != nil {
		panic(err)
	}

	newBridge := func(priority uint16, mac string) *stp.Layer {
		address, err := netaddr.Parse(mac)
		if err != nil {
			panic(err)
		}
		l, err := stp.New(stp.Config{
			Priority: priority,
			Address:  address,
			Ports:    map[string]stp.Port{"1/1/1": {}},
		}, layer.Env{Ports: ports})
		if err != nil {
			panic(err)
		}

		return l
	}

	root := newBridge(4096, "00:11:22:33:44:01")
	leaf := newBridge(32768, "00:11:22:33:44:02")

	t0 := time.Unix(1700000000, 0)
	fx := root.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)
	leaf.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)

	// The root's proposal reaches the leaf, which agrees.
	proposal, err := bpdu.Decode(fx.Emissions[0].Frame)
	if err != nil {
		panic(err)
	}
	agreement := leaf.Receive(t0, "1/1/1", proposal)
	fmt.Printf("leaf: %s/%s\n", leaf.PortInfo("1/1/1").Role, leaf.PortInfo("1/1/1").State)

	reply, err := bpdu.Decode(agreement.Emissions[0].Frame)
	if err != nil {
		panic(err)
	}
	root.Receive(t0, "1/1/1", reply)
	fmt.Printf("root: %s/%s\n", root.PortInfo("1/1/1").Role, root.PortInfo("1/1/1").State)
}
```

## How long received information lives

Information a port receives is bounded twice, and both bounds must hold.

The first is a hop count. A BPDU is accepted only while
`MessageAge + 1 <= MaxAge`, taking `MaxAge` from the BPDU being processed rather
than from this bridge's configuration: the received value is the root's, and a
fabric can hold bridges with differing timers. A BPDU that fails the test is
discarded rather than stored, which is what stops a BPDU naming a root that no
longer exists from refreshing the timer on every hop and holding a port blocked
forever. `makeBPDU` adds the second on origination, off the root port.

The UNH-IOL RSTP conformance suite states the rule: "RSTP treats the Message Age
parameter in received BPDUs as an incrementing hop count with Max Age as its
maximum value. Message Age is incremented after being received on the Root Port.
If Message Age is greater than Max Age, the BPDU is discarded"
([RSTP_conformance_Q.pdf][unh-rstp], citing IEEE Std 802.1Q-2011 sub-clauses
13.23.6, 13.27.30, and 13.28).

The second bound is silence. Accepted information lives for `3 × HelloTime` from
the moment it arrived, after which `Advance` expires it and the roles are recomputed.

Internal information — a BPDU whose configuration identifier matches this
bridge's own — ages by hop count instead: it is accepted while
`RemainingHops > 1` and re-originated one hop short, on both the CIST and every
MSTI, so a claim naming a regional root that no longer exists stops refreshing
after `MaxHops` hops rather than after a fixed time. External information (an
RST or Configuration BPDU, or an MST BPDU from a different region) keeps the
message-age test above. Both kinds still expire in silence after
`3 × HelloTime`. `MaxHops` defaults to 20 and is bounded 6 through 40.

[unh-rstp]: https://www.iol.unh.edu/sites/default/files/testsuites/bfc/RSTP_conformance_Q.pdf

## Guards

Each guard is a per-port administrative flag on `Port`, and each produces a port
state rather than an issue code, because a state an operator can see beats an
issue code they have to look for. `PortInfo.BlockReason` names the guard holding
a port, and the trace carries it.

| Guard | What it does | What clears it |
| --- | --- | --- |
| `BPDUGuard` | A BPDU on the port disables it for spanning tree, on the CIST and every MSTI alike: role Disabled, state Discarding, reason `bpdu-guard`. The BPDU is not read. | A `LinkChange` reporting the port down and then up. Nothing else, including further BPDUs. |
| `RestrictedRole` | The port is never selected as root port, so superior information on it makes it Alternate and leaves the bridge's own root unchanged. IEEE calls this restricted role; vendors call it root guard. | Nothing to clear: it is a standing restriction. |
| `RestrictedTCN` | A topology change received on the port propagates to no other port, and does not set the topology-change timer that would carry the flag out on this bridge's own BPDUs. | Nothing to clear. |
| `LoopGuard` | A port whose stored information expires in silence while it is Root, Alternate, or Backup becomes Alternate and Discarding with reason `loop-inconsistent`, excluded from root-port selection so the tree reconverges around it, and never Designated. Under MSTP the outcome is bridge-global, so every MSTI's own port follows it too: an internal port reads the CIST mark, and a boundary port mirrors the CIST's role and state outright. Under PVST each tree arms loop guard on its own expiry. | Under PVST, an SSTP BPDU applied to that tree. An IEEE BPDU admitted by the link clears the CIST mark. A link down clears every tree. |

Loop guard is netsim's own design, drawn from Cisco, Juniper, and Arista. Cisco
blocks inconsistent ports per VLAN, so PVST keeps one mark per tree. A BPDU
recovers only the tree it is allowed to affect: an IEEE BPDU recovers the CIST,
while an SSTP BPDU recovers its arrival VLAN only after the PVID and admission
checks pass. A boundary, unadmitted, untracked, or PVID-inconsistent SSTP BPDU
leaves the mark in place. Cisco does not identify which VLAN's BPDU recovers a
port, so the per-tree recovery rule is modelled on this per-VLAN observation.
Loop guard is inactive on a port that is operationally edge and on one that is
not point-to-point, which is where [Cisco][cisco-loop] and [Arista][arista-stp]
rule it out: on a shared link a port that stops hearing BPDUs is not evidence
of a link broken in one direction.

Two combinations are refused at construction, with the field path
`ports.<name>.loop_guard`:

- `LoopGuard` with `RestrictedRole`, which Cisco and [Juniper][juniper-loop] make
  mutually exclusive. Restricted role denies the port the role loop guard exists
  to protect.
- `LoopGuard` with `AdminEdge`, which asks the guard to watch a port it is
  defined not to watch.

A configuration whose two halves contradict each other has no correct simulated
answer, so refusing it names the conflict where the operator can see it.

[cisco-loop]: https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol-stp-8021d/218321-configure-stp-with-loop-guard-and-bpdu-s.html
[juniper-loop]: https://www.juniper.net/documentation/us/en/software/junos/stp-l2/topics/topic-map/spanning-tree-loop-protection.html
[arista-stp]: https://www.arista.com/en/um-eos/eos-spanning-tree-protocol

## The gate answers per VLAN

`Learns` and `Forwards` take a port and a VLAN, because a port's forwarding
state belongs to a spanning tree and more than one tree can run over one port.
The layer keys its state by tree and maps the VLAN to one.

With neither `MST` nor `PVST` configured there is exactly one tree, the CIST,
and every VLAN maps to it, so the two answers always agree. Once a region is
configured, a VLAN an instance claims maps to that MSTI instead, and a
boundary port's answer for it still traces back to the CIST because an MSTI
takes the CIST port's role there. A VLAN no instance claims keeps mapping to
the CIST on every port. Under `PVST` every VLAN maps to a tree of its own.

`VLANPortInfo` gives the same per-VLAN view of a port that `PortInfo` gives of
the common tree, and works in every mode: on a bridge running one tree every
VLAN answers alike.

The bridge consults the gate after classifying the frame, so a gate-blocked
frame names the VLAN it was classified into, and a frame that fails
classification reports the classification reason instead: a frame the port would
never have admitted is not a spanning-tree question.

Two things stay bridge-global rather than moving onto the tree. The port
identifier, derived from the index in the sorted port names, appears on the wire
and in `PortInfo`. The port key set and its iteration order reach the caller as
the order of `layer.Effects.Flush`.

## Rapid spanning tree per VLAN (PVST)

`Config.PVST` selects per-VLAN rapid spanning tree, and `Config.Validate`
refuses a configuration setting both `MST` and `PVST`: the mode is the
presence of a pointer, so a `Mode` enum would make the same fact readable two
ways and let the two disagree.

Each VLAN runs the landed RSTP machine. `PVST.Trees` holds one `Tree` per
VLAN, with the per-port priority and path cost overrides `InstancePort`
already carries, and `PVST.Normalize` fills a tree that names no priority from
the bridge's own, so a bridge configured to be root is root on every VLAN it
does not override. A tree's bridge identifier carries its VLAN in the low 12
bits of the system-ID extension, the way an MSTI carries its MSTID, which is
what the multiple-of-4096 rule on a tree priority reserves those bits for.

`Normalize` also inserts a default VLAN 1 tree whenever `Trees` omits one,
whether `Trees` is empty or already names other VLANs, and leaves an
explicitly configured VLAN 1 tree alone. `newLayer` builds VLAN 1's tree
unconditionally in PVST mode, so a `Config` that never mentions VLAN 1 still
runs one; `Normalize` has to agree, or `PVST.Canonical` and `PVST.Validate`
would describe a different set of trees than `New` actually builds.

VLAN 1's tree occupies the CIST slot. In PVST+ it *is* the common tree a
neighboring RSTP or MSTP bridge converges with, and `Root`, `PortInfo`,
`TopologyChanges`, `Times` and `BridgeID` all answer from the CIST, so putting
it there keeps every bridge-level accessor answering about the common tree in
all three modes.

Every tree meters its own transmit budget. An MST bridge spends one slot per
port because its MSTI records ride the CIST's BPDU; a PVST bridge puts one
frame per VLAN on the wire, so a bridge carrying more VLANs than its hold
count would starve the VLANs that sort last if the budget stayed
bridge-global.

### Two receive entry points

`Receive` takes an IEEE-addressed BPDU and applies it to the CIST, which is
where such a BPDU belongs in every mode. `ReceiveSSTP` takes an SSTP BPDU
along with an `SSTPArrival{ArrivalVID, TLVVID, Admitted}`: the VLAN the switch
classified the frame into, the VLAN the BPDU's own trailing TLV names, and the
bridge's ingress admission answer for the arrival VLAN on this port. The layer
holds no VLAN table of its own, so `Admitted` is taken as given rather than
derived a second time beside the bridge's own rule.

`ReceiveSSTP` returns an `SSTPOutcome` alongside its `layer.Effects`, naming the
first thing that stopped the frame short of being applied to a tree:
`SSTPGuarded` when BPDU guard fires or already holds the port disabled,
`SSTPBoundary` when this bridge does not run PVST, `SSTPNotAdmitted` when
`Admitted` is false, `SSTPUntrackedVLAN` when this bridge runs PVST but has no
tree for the arrival VLAN, `SSTPPVIDInconsistent` when `TLVVID` disagrees with
`ArrivalVID`, and `SSTPApplied` otherwise; `SSTPPortDown` covers a port the
layer does not track or holds down, ahead of all of the above. `ArrivalVID`
and `TLVVID` are needed together because the disagreement between them is the
PVID check: when they differ the BPDU is not applied, and the arrival VLAN is
held discarding on that port and excluded from contributing a root vector
until a consistent BPDU arrives. The arrival VLAN is the blocked one, not the
VLAN the TLV names, because its local traffic is what would cross a link the
two ends disagree about.

SSTP Configuration BPDUs use the RST body layout with version 0, wire type
`0x00`, and a PVID TLV. SSTP RST BPDUs use the same layout with version 2 or
later and wire type `0x02`. SSTP TCN BPDUs use wire type `0x80`, have no TLV,
and scope their receive effect to the VLAN the switch classified on ingress.
The TCN path therefore runs admission and tree checks but skips the PVID
comparison. PVST and SSTP behavior is modelled on observation from [CISCO],
[PVID], and [EXT], with wire offsets cross-checked against [WS].

The half of a receive that belongs to the link rather than to any tree, BPDU
guard, protocol migration, and the loss of auto-edge status, runs once per
frame in `receiveLink`, which both entry points share. Loop-guard recovery is
owned by the receive entry point because the two entry points select different
trees.

Every property `receiveLink` can change belongs to the link, not to any tree.
`Layer` holds one `linkRecord` per port for physical and administrative link
state: `up`, `pointToPoint`, `edge`, `sendRSTP`, `adminEdge`, `linkPathCost`,
`external`, `bpduGuardDisabled`, `pvstBoundary`, `mdelayWhile`, `edgeDelayWhile`,
`rxBPDUs`, and `badBPDUs`. `portState` holds tree-owned state alone.
`VLANPortInfo`'s `RxBPDUs` and `BadBPDUs` answer from the port's `linkRecord` on
any VLAN. `BlockReason` reads the tree's own PVID and loop-guard marks under
PVST. On an MST bridge an MSTI reads the CIST loop-guard mark, while the CIST
itself reads its own. `PortInfo` reports the same rules for the common tree.
A VLAN with no tree under PVST answers neither kind: `treeFor` says so through
its second return, and `VLANPortInfo` and `ForwardingFact` return the zero value
for it rather than VLAN 1's, because VLAN 1's tree is a tree like any other, not
a stand-in for a VLAN that has none.

A separate `Receive` taking a VLAN would read as though an RSTP bridge
classified its BPDUs per VLAN, which it does not.

### Emission

State transitions do not build frames. `Receive`, `ReceiveSSTP`, `Advance`,
`LinkChange`, and `Mcheck` settle all tree state first, then run one Port
Transmit pass. That pass visits `treeOrder` and then `portNames`, so each
emission is built from the final state of every tree that was changed by the
event.

Before an event is applied, each record whose hello time is already overdue is
advanced from its due instant by whole HelloTime intervals until it is no longer
before the event. This settlement does not request a frame. A hello due exactly
at the event instant is handled by the transmit pass. `NextWake` keeps reporting
only a hello that can set a request.

Each tree and port keeps pending new information, a held transmit count and
tick, and its next hello time. Outside PVST, only the CIST owns a hello timer.
MSTI information is pending on the CIST port and rides its BPDU as an MSTI
record. In PVST, each VLAN tree owns its timer and its transmit budget.

RSTP and MSTP transmit pending information regardless of the port role. Legacy
STP sends a Topology Change Notification from a Root port and a Configuration
BPDU from a Designated port. Under PVST this applies to every tree. A Root
tree sends a TCN, a Designated tree sends a Configuration BPDU, and another
tree sends nothing. An MSTI-only request on a port that does not send RSTP
emits nothing and remains pending. RSTP and MSTP transmission clears both CIST
and MSTI pending information. A legacy Configuration BPDU or TCN clears only
CIST information. A legacy TCN acknowledgment is included in the next
Configuration BPDU for that tree and port rather than emitted immediately.

When a port comes up, every tree's transmit record receives both requests after
roles are computed. This sends one frame per tree, including edge ports. A port
that is down or disabled by BPDU guard sends nothing, retains its requests, and
holds a zero transmit count until the port-up pass.

In PVST every tree sends its BPDU to `bpdu.GroupAddressSSTP()` naming its own
VLAN through `layer.Emission.VID`. While the port sends RSTP, VLAN 1's tree
also sends a second, IEEE-addressed frame naming no VLAN. The two VLAN 1 frames
are one transmission and spend one budget slot between them. The layer never
builds a VLAN tag: a non-zero `layer.Emission.VID` tells the switch to put the
frame through the port's ordinary egress rules, which is where the
native-versus-tagged decision already lives.

A port migrated to legacy STP sends VLAN 1's untagged IEEE Configuration BPDU
alone. Each non-CIST tree still sends its own SSTP Configuration BPDU or TCN on
its VLAN. VLAN 1's tree builds its legacy Configuration BPDU and sends only the
IEEE-addressed copy. The SSTP codec retains its Configuration, RST, and TCN
shapes for the per-VLAN path. PVID supplies the VLAN mapping, and EXT supplies
the observed per-VLAN TCN and acknowledgment behavior.

### The boundary this package reports

`PVSTBoundary` reports a port facing a neighbor whose per-VLAN trees this
bridge cannot simulate: an MST BPDU seen by a PVST bridge, or an SSTP BPDU
seen by a bridge that is not one. The second withholds only the priority
vector, because its CIST does not run that VLAN's tree and feeding the vector
in would elect a root from a tree it is not running. The link half of the
receive, BPDU guard among it, still runs, the same as for any other BPDU the
port hears. The mark lives on the CIST port state and clears on a link down,
since only that can replace the neighbor.

## Decoding a version 3 BPDU

A version 3 BPDU carries the CIST fields every RST BPDU does, plus a 51-octet
MST configuration identifier, the CIST's internal root path cost and remaining
hops, and one 16-octet record per instance the sender maps a VLAN into.
`bpdu.Decode` classifies a type 2 frame by the octets counted from its Protocol
Identifier, as IEEE 802.1Q-2003 clause 14.4 requires:

- From 35 through 101 octets, it reads the RST prefix whatever the length fields say.
- At 102 octets, it reads MST with no records only when Version 1 Length is 0 and Version 3 Length is 64. Every other pair reads as RST.
- At 103 octets or more, Version 1 Length 0 and a Version 3 Length naming 0 to 64 records select MST. Octets after the named records are ignored.

The MST fields come back in `bpdu.BPDU.ConfigID`, `RegionalRootID`,
`InternalRootPathCost`, `RemainingHops`, and `MSTIs`. A frame of 103 or more
octets whose Version 3 Length names records that are absent is refused. That is
this package's rule for an input that clause 14.4 does not define. Version 4 and
later use the same length bands.

## Multiple spanning tree instances (MSTP)

A region is a name (32 octets at most), a 16-bit revision, and a digest,
carried on the wire as a 51-octet configuration identifier with format
selector 0. The digest is HMAC-MD5 over the 4096-entry VID-to-MSTID table, two
big-endian octets per VID, VID 0 through 4095, keyed with

```
13 AC 06 A6 2E 47 FD 51 F9 5D 2B A2 43 CD 03 46
```

The two vectors below are what prove `MST.ConfigID` reproduces the standard
construction rather than some other one: the all-zero table (no instances
configured) digests to `ac36177f50283cd4b83821d8ab26de62`, and VID 10 on
MSTID 1 with VID 20 on MSTID 2, every other VID zero, digests to
`9357ebb7a8d74dd5fef4f2bab50531aa`.

`priorityVector` has six components, compared in order: root, external root
path cost, regional root, internal root path cost, designated bridge,
designated port. There is one comparator for both trees rather than one per
tree, because a constant or shared leading component drops out of a
lexicographic comparison. An RSTP tree, and the CIST on a boundary port, set
the regional root from the root and leave the internal cost at zero,
collapsing the comparison to the four-component root/cost/bridge/port order
with the cost living in the external slot; an MSTI does the same the other
way round, taking its root from its regional root and leaving the external
cost at zero. The CIST on an internal port populates all six components: the
regional root and internal cost compare ahead of bridge and port, which is
what lets a region compute its own internal topology before comparing outward
against the wider network.

A port that has received nothing is treated as internal, since the field
that marks a boundary port only gets set on `Receive`. Once a BPDU has
arrived, a port is internal when it carried this bridge's own configuration
identifier, and a boundary port otherwise; an RST or Configuration BPDU is
always external. Only the CIST computes a boundary port's role; every MSTI
takes the CIST port's role there outright rather than electing one from
information a different region sent. A bridge whose CIST root port is itself
a boundary port names its own bridge identifier the CIST's regional root, not
the peer's: crossing the boundary is what starts a region, so the bridge on
this side of it originates the region's full hop count rather than
re-originating a count borrowed from the peer.
That is also why an MSTI never reports the Master role IEEE 802.1Q's prose
uses for a CIST root that isn't also an MSTI's regional root: both the CIST
and MSTI role MIB tables (`ieee8021MstpCistPortRole`, `ieee8021MstpPortRole`
in `spec/mib/ieee/`) list exactly Root, Alternate, Designated, and Backup, so
a boundary port's MSTI mirrors the CIST's Root under the label the MIB can
express, the same forwarding answer under a different name.

Proposal and agreement are evaluated after the CIST information is stored.
An MSTI Proposal is acted on when its stored record names a Designated sender,
even when the BPDU's CIST information does not match the information already
held. An MSTI Agreement requires the BPDU's CIST root, external path cost, and
regional root to match the vector held after that receive. Role selection then
chooses the role before the port state machine changes state. This follows
IEEE 802.1Q-2003 clauses 13.26.9, 13.26.10, and 13.26.14, and P802.1aq/D1.5
clauses 13.29.16 and 13.29.20.

On a boundary port, MSTI sync reads the CIST port's state and agreement. MSTI
sync leaves that port's state unchanged. CIST sync mirrors its resulting state
to the boundary MSTIs in the same receive call. The layer's boundary role and
state mirror is the limit of this model, so the standard's separate Master and
disputed mechanisms are not represented.

See "How long received information lives" above for hop aging, which applies
to internal information on both the CIST and every MSTI.

A region configuration is refused at construction, with the field path it
names, when: an instance claims a VLAN outside 1 through 4094; the region
name contains a NUL byte; an instance lists a port that is not in this
bridge's spanning tree port set; an instance port's path cost exceeds the
maximum path cost; or the region holds more instances than one BPDU's
version 3 length field can carry. The last one is a wire limit rather than an
arbitrary cap: a region within it always has a BPDU to send, and a region
that validates but cannot be encoded would be a configuration with no
correct simulated answer.

## What a topology change flushes

`layer.Effects.Flush` is a list of `layer.FlushTarget{Port, FIDs}`, and the bridge's
`Flush` deletes a learned entry only when its port matches a target and that
target either names the entry's FID or names none at all. An empty `FIDs` means
every FID on the port.

A topology change on an instance flushes, on the bridge's other ports, only the
VLANs that instance carries. That is the point of the pair: moving VLAN 10's
tree must not discard what the bridge learned about VLAN 20, which did not move.
This propagates across the fabric, not just locally: each MSTI record on an
MST BPDU carries its own instance's topology-change bit, and a bridge that
receives one flushes that instance's VLANs on its other ports the same way a
locally raised change would, so a change on one bridge's instance reaches
every other bridge's copy of it rather than stopping at the first hop.

An IEEE-addressed TCN is scoped to VLAN 1 under PVST. An SSTP TCN is scoped to
the arrival VLAN under PVST and carries no PVID TLV, so a PVID inconsistency
cannot be created by that shape. Under MSTP an IEEE-addressed TCN applies to
the CIST and every active MSTI on the receiving port. The PVST mapping follows
the PVID source's rule for VLAN 1 BPDUs. The MSTP behavior follows IEEE
802.1Q-2003 clause 13.26.19, page 194, and P802.1aq/D1.5 clause 13.29.13,
page 63.

Every active non-edge receiver propagates a TCN to the tree's other active
non-edge ports, even when its own `tcWhile` is already running. A Designated
receiver sets TCAck and starts its own timer only when it is stopped. Each
propagated destination is flushed and starts a stopped timer. An Alternate
receiver propagates nothing. This follows P802.1aq/D1.5 Figure 13-28, page 82,
and clauses 13.29.11, page 63, and 13.29.26, page 67.

Five cases flush every FID instead of a VLAN list. A link going down and a
BPDU-guard disable both leave every entry on that port stale whatever tree it
belonged to. A received topology change notification, and a received CIST
BPDU with the topology-change flag set, both flush every other port with an
empty FID set outright, without going through the per-instance accounting
above: a peer reporting a change at the CIST level is reporting it for
whatever it is carrying, which this bridge cannot narrow down from the
notification alone. The last is a topology change this bridge's own CIST
raises, and it is worth being plain about: the CIST carries every VLAN no
instance claims, which is not a set this layer can enumerate, so a CIST
change names no FIDs and the bridge flushes the port across all of them. On a
boundary port that is what the standard wants anyway, since a CIST change
there reaches every tree. On an internal port it discards more than it
strictly must, costing a round of flooding to relearn entries that were never
stale. Narrowing it would need a target that can say
"every FID except these", which `layer.FlushTarget` deliberately cannot.

## Standards and state machines

The layer implements the state machines defined in IEEE Std 802.1Q-2003 (incorporating IEEE Std 802.1s-2002) and follows the unified RSTP and MSTP state machine consolidation from the IEEE P802.1aq/D1.5 draft (May 2009). Conformance test behavior is drawn from the UNH-IOL Rapid Spanning Tree Conformance Test Suite (referencing IEEE Std 802.1Q-2011). Frame encoding offsets are cross-checked against the Wireshark dissector (`epan/dissectors/packet-bpdu.c`). PVST and SSTP behavior is modelled on observation from [CISCO], [PVID], and [EXT]. Published editions of IEEE Std 802.1D-2004 and IEEE Std 802.1Q-2011 were not directly consulted and are unverified.

[CISCO]: https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol-stp-8021d/218321-configure-stp-with-loop-guard-and-bpdu-s.html
[PVID]: https://web.archive.org/web/20241113152806/https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol/24063-pvid-inconsistency-24063.html
[EXT]: https://documentation.extremenetworks.com/slxos/SW/20xx/l2config/GUID-FC3E8C8E-3930-4777-825D-3ECD12328F51.shtml
[WS]: https://gitlab.com/wireshark/wireshark/-/raw/master/epan/dissectors/packet-bpdu.c

The implementation structures its logic around the standard state machines:

- Port Role Selection (PRS): IEEE 802.1Q-2003 clauses 13.9, 13.10, 13.11, and 13.24. Compares the six-part priority vector (Root ID, External Path Cost, Regional Root ID, Internal Path Cost, Designated Bridge ID, Designated Port ID) to elect the root and assign port roles.
- Port Information (PIM): IEEE 802.1Q-2003 clauses 13.21 and 13.24, and P802.1aq/D1.5 clause 13.29. Stored information expires after 3 x HelloTime of silence or when message age or hop count limits are exceeded.
- Port Role Transitions (PRTM): IEEE 802.1Q-2003 clause 13.26.9 and Figure 13-14, and P802.1aq/D1.5 clauses 13.29.16 and 13.29.20, Figures 13-20 and 13-25. Computes proposal and agreement handshakes and steps the forward delay ladder.
- Port Transmit (PTM): IEEE 802.1Q-2003 Figure 13-13 and P802.1aq/D1.5 Figure 13-19, clauses 13.28.13 and 13.28.14. It settles overdue hello records before an event, then runs one deterministic tree-then-port transmit pass. It sends periodic hellos from each tree's timer, bounds each port and tree by txHoldCount, and clears only the requests carried by the BPDU shape it sent. The layer's own port-up request is made by `LinkChange` after role computation.
- Topology Change (TCM): IEEE 802.1Q-2003 clauses 13.17, 13.21, 13.26, and Figure 13-19, and P802.1aq/D1.5 clauses 13.19, 13.29.11, 13.29.13, and 13.29.26, and Figure 13-28. Detects a non-edge Root or Designated port when it moves to Forwarding, requests a frame on the detecting port in either protocol mode, starts tcWhile only when stopped, and propagates every received TCN to active non-edge ports while flushing their tree VLANs. The layer's own detection request is made by `detectTopologyChange`. Under PVST an IEEE-addressed TCN applies to VLAN 1 only. Under MSTP it applies to the CIST and every active MSTI. A Designated receiver sets TCAck, an Alternate receiver propagates nothing, and RSTP tcWhile is HelloTime + 1 second. Legacy STP tcWhile is Max Age + Forward Delay. PVID supplies the PVST address mapping, and EXT supplies the observed per-VLAN TCN and acknowledgment behavior.
- Port Protocol Migration (PPM): IEEE 802.1Q-2003 clauses 13.24.18, 13.24.23, and Figure 13-12. Manages migration between RSTP/MSTP and legacy STP, tracked by mdelayWhile.

### Limits

The layer deliberately departs from or fixes ambiguous areas of the standards:

- No disputed flag (draft P802.1aq/D1.5 clause 13.29.17) and no Master role (IEEE role MIB tables define only Root, Alternate, Designated, and Backup, so a boundary port MSTI role mirrors the CIST).
- Hello Time remains configurable on the bridge, while later standard text fixes it to 2 seconds. The bridge arms hello transmission using its local HelloTime and transmits its own HelloTime in BPDUs.
- The forward delay ladder steps by the Forward Delay in force on every port. Draft P802.1aq/D1.5 clause 13.28.8 steps an RSTP port by HelloTime, but UNH RSTP.op.4.2 expects a port lacking agreement to hold traffic until forward delay expires. Because sources disagree, the ladder steps by the root Forward Delay in force.
- A port losing auto-edge returns to Discarding and proposes again across all trees, standing in for the disputed mechanism. Draft Figure 13-16 only clears operEdge.
- On a boundary port, MSTI sync keeps the CIST-derived state and agreement. CIST sync mirrors a CIST state change to the boundary MSTIs. The layer does not model the draft's separate disputed or Master paths, so an MSTI cannot discard independently while its CIST port forwards.
- Port priority configurations that are not multiples of 16 are accepted. The layer extracts and transmits the high four bits as port priority (IEEE 802.1Q-2003 clause 13.24.21).

## State retention

`RetentionKey(cfg Config, env layer.Env) string`
encodes every normalized input the spanning tree runtime state depends on: its
own configuration as `Diff` sees it, the administrative and operational state of
configured ports, and resolved physical link speeds. `vswitch.Derive` retains the
runtime layer only when both keys match and rebuilds it otherwise.

## Not modeled

- A bridge configured to run independent 802.1D state machines on every port,
  and the PVST inconsistency states other than the PVID check: type
  inconsistency, and port-VLAN-ID mismatch on an access port.
- Cisco's PVST simulation on an MSTP boundary port, which this package reports
  as a boundary rather than models.
- Tagged BPDU emission from this package. `bpdu.Encode` and `bpdu.EncodeSSTP` both emit
  untagged frames; a per-VLAN BPDU names its VLAN in `layer.Emission.VID` and the
  switch tags it.
- Automatic BPDU-guard recovery timers, and BPDU filter.
- MSTP L2GP, SPT, SPB, agreement digests, and Cisco pre-standard MSTI
  encoding, none of which `bpdu.Decode` gives any special handling: a frame in one
  of these shapes either fails to decode or reads as a plain RST or MST BPDU
  with the extension ignored. Version 4 and later BPDUs are not in this list:
  see "Decoding a version 3 BPDU" above for how they actually decode.
