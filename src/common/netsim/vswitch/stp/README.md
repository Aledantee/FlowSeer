# stp

Package `stp` implements the Rapid Spanning Tree Protocol and Multiple
Spanning Tree Protocol (IEEE 802.1D-2004, carried into 802.1Q clause 13) for
the virtual switch. It elects a root bridge, assigns port roles and states,
and answers the bridge's forwarding gate. A `Layer` with no `MST` configured
runs plain RSTP with the CIST as its only tree; configuring `MST` adds region
membership, MST instances, boundary roles, and hop aging on top of it.

The layer runs deterministically in memory without background goroutines or wall
clocks. Time advances through explicit, time-stamped calls to `LinkChange`,
`Receive`, `Wake`, and `Mcheck`.

## Example

Two bridges come up on a point-to-point link, exchange a proposal and an
agreement, and settle into Designated and Root.

```go
package main

import (
	"fmt"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
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
		layer, err := stp.New(stp.Config{
			Priority: priority,
			Address:  address,
			Ports:    map[string]stp.Port{"1/1/1": {}},
		}, ports)
		if err != nil {
			panic(err)
		}

		return layer
	}

	root := newBridge(4096, "00:11:22:33:44:01")
	leaf := newBridge(32768, "00:11:22:33:44:02")

	t0 := time.Unix(1700000000, 0)
	fx := root.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)
	leaf.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)

	// The root's proposal reaches the leaf, which agrees.
	proposal, err := stp.Decode(fx.Emissions[0].Frame)
	if err != nil {
		panic(err)
	}
	agreement := leaf.Receive(t0, "1/1/1", proposal)
	fmt.Printf("leaf: %s/%s\n", leaf.PortInfo("1/1/1").Role, leaf.PortInfo("1/1/1").State)

	reply, err := stp.Decode(agreement.Emissions[0].Frame)
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
the moment it arrived, after which `Wake` expires it and the roles are recomputed.

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
| `LoopGuard` | A port whose stored information expires in silence while it is Root, Alternate, or Backup becomes Alternate and Discarding with reason `loop-inconsistent`, excluded from root-port selection so the tree reconverges around it, and never Designated. The outcome is bridge-global, so every MSTI's own port follows it too: an internal port reads the same guard state the CIST set, and a boundary port mirrors the CIST's role and state outright. | Any BPDU received on the port, or a link down. |

Loop guard is netsim's own design, drawn from Cisco, Juniper, and Arista, which
all apply loop protection only to ports that were receiving BPDUs and recover on
the next BPDU. Because any received BPDU clears the state, including one the
message-age bound discards, a peer that keeps sending information too old to
store is not covered: the guard clears on each such BPDU and never re-arms. It is inactive on a port that is operationally edge and on one
that is not point-to-point, which is where [Cisco][cisco-loop] and
[Arista][arista-stp] rule it out: on a shared link a port that stops hearing
BPDUs is not evidence of a link broken in one direction.

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
the order of `Effects.Flush`.

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
along with two VLANs: the one the switch classified the frame into and the one
the BPDU's trailing TLV names. Both are needed because the disagreement
between them is the PVID check. When they differ the BPDU is not applied, and
the arrival VLAN is held discarding on that port and excluded from
contributing a root vector until a consistent BPDU arrives. The arrival VLAN
is the blocked one, not the VLAN the TLV names, because its local traffic is
what would cross a link the two ends disagree about.

The half of a receive that belongs to the link rather than to any tree, BPDU
guard, the loop-guard clear every BPDU earns, protocol migration, and the loss
of auto-edge status, runs once per frame in `receiveLink`, which both entry
points share.

A separate `Receive` taking a VLAN would read as though an RSTP bridge
classified its BPDUs per VLAN, which it does not.

### Emission

Every tree sends its BPDU to `GroupAddressSSTP` naming its own VLAN through
`Emission.VID`; VLAN 1's tree sends a second, IEEE-addressed and naming no
VLAN. The two are one transmission and spend one budget slot between them. The
layer never builds a VLAN tag: a non-zero `Emission.VID` tells the switch to
put the frame through the port's ordinary egress rules, which is where the
native-versus-tagged decision already lives.

### The boundary this package reports

`PVSTBoundary` reports a port facing a neighbor whose per-VLAN trees this
bridge cannot simulate: an MST BPDU seen by a PVST bridge, or an SSTP BPDU
seen by a bridge that is not one. The second applies nothing, because its CIST
does not run that VLAN's tree and feeding the vector in would elect a root
from a tree it is not running. The mark lives on the CIST port state and
clears on a link down, since only that can replace the neighbor.

## Decoding a version 3 BPDU

A version 3 BPDU carries the CIST fields every RST BPDU does, plus a 51-octet
MST configuration identifier, the CIST's internal root path cost and remaining
hops, and one 16-octet record per instance the sender maps a VLAN into.
`Decode` reads all of it: `BPDU.ConfigID`, `RegionalRootID`,
`InternalRootPathCost`, `RemainingHops`, and `MSTIs` come back filled whenever
the payload holds enough octets for the MST body.

When the payload is too short to hold that body at all — a peer running plain
RSTP that sent a 39-octet RST BPDU with version 3 in the header, or a capture
truncated before the MST body starts — `Decode` still reads the RST prefix
and returns it with `ConfigID` nil and no records, rather than refusing the
frame. A payload long enough for the MST body but truncated inside the MSTI
records is refused, not fallen back to the RST prefix: at that point the
sender meant to carry MST fields and `Decode` cannot tell which ones survived
the truncation. The UNH-IOL MSTP conformance suite is why the version number
alone never disqualifies a BPDU: "A compliant device must not validate an MST
BPDU based on the value encoded in the Protocol Version Identifier field.
This allows future versions of the Spanning Tree Protocol to use this field
while providing support for legacy versions" ([MSTP_conformance.pdf][unh-mstp],
Test MSTP.op.1.3, citing IEEE Std 802.1Q-2011 sub-clause 14.4). This is also
why version 4 and later decode the same way as a short version 3 payload,
with `ConfigID` nil, rather than being rejected outright. Refusing a short
version 3 payload would leave a netsim bridge facing that peer with both ends
Designated and Forwarding, an unbroken loop and a worse answer than the
RST-prefix approximation.

[unh-mstp]: https://www.iol.unh.edu/sites/default/files/testsuites/bfc/MSTP_conformance.pdf

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

`Effects.Flush` is a list of `FlushTarget{Port, FIDs}`, and the bridge's
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
"every FID except these", which `FlushTarget` deliberately cannot.

## Not modeled

- 802.1D spanning tree per VLAN, and the PVST inconsistency states other than
  the PVID check: type inconsistency, and port-VLAN-ID mismatch on an access
  port.
- Cisco's PVST simulation on an MSTP boundary port, which this package reports
  as a boundary rather than models.
- Tagged BPDU emission from this package. `Encode` and `EncodeSSTP` both emit
  untagged frames; a per-VLAN BPDU names its VLAN in `Emission.VID` and the
  switch tags it.
- Automatic BPDU-guard recovery timers, and BPDU filter.
- MSTP L2GP, SPT, SPB, agreement digests, and Cisco pre-standard MSTI
  encoding, none of which `Decode` gives any special handling: a frame in one
  of these shapes either fails to decode or reads as a plain RST or MST BPDU
  with the extension ignored. Version 4 and later BPDUs are not in this list:
  see "Decoding a version 3 BPDU" above for how they actually decode.
