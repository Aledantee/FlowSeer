# stp

Package `stp` implements the Rapid Spanning Tree Protocol (IEEE 802.1D-2004,
carried into 802.1Q clause 13) for the virtual switch. It elects a root bridge,
assigns port roles and states, and answers the bridge's forwarding gate.

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
| `BPDUGuard` | A BPDU on the port disables it for spanning tree: role Disabled, state Discarding, reason `bpdu-guard`. The BPDU is not read. | A `LinkChange` reporting the port down and then up. Nothing else, including further BPDUs. |
| `RestrictedRole` | The port is never selected as root port, so superior information on it makes it Alternate and leaves the bridge's own root unchanged. IEEE calls this restricted role; vendors call it root guard. | Nothing to clear: it is a standing restriction. |
| `RestrictedTCN` | A topology change received on the port propagates to no other port, and does not set the topology-change timer that would carry the flag out on this bridge's own BPDUs. | Nothing to clear. |
| `LoopGuard` | A port whose stored information expires in silence while it is Root, Alternate, or Backup becomes Alternate and Discarding with reason `loop-inconsistent`, excluded from root-port selection so the tree reconverges around it, and never Designated. | Any BPDU received on the port, or a link down. |

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

With no `MST` configured there is exactly one tree, the CIST, and every VLAN
maps to it, so the two answers always agree. Once a region is configured, a
VLAN an instance claims maps to that MSTI instead, and a boundary port's
answer for it still traces back to the CIST because an MSTI takes the CIST
port's role there. A VLAN no instance claims keeps mapping to the CIST on
every port.

The bridge consults the gate after classifying the frame, so a gate-blocked
frame names the VLAN it was classified into, and a frame that fails
classification reports the classification reason instead: a frame the port would
never have admitted is not a spanning-tree question.

Two things stay bridge-global rather than moving onto the tree. The port
identifier, derived from the index in the sorted port names, appears on the wire
and in `PortInfo`. The port key set and its iteration order reach the caller as
the order of `Effects.Flush`.

## Decoding a version 3 BPDU

A version 3 BPDU carries the CIST fields every RST BPDU does, plus a 51-octet
MST configuration identifier, the CIST's internal root path cost and remaining
hops, and one 16-octet record per instance the sender maps a VLAN into.
`Decode` reads all of it: `BPDU.ConfigID`, `RegionalRootID`,
`InternalRootPathCost`, `RemainingHops`, and `MSTIs` come back filled whenever
the payload holds enough octets for the MST body.

When it does not — a truncated capture, or a peer running plain RSTP that sent
a 39-octet RST BPDU with version 3 in the header — `Decode` still reads the
36-octet RST prefix and returns it with `ConfigID` nil and no records, rather
than refusing the frame. The UNH-IOL MSTP conformance suite is why the version
number alone never disqualifies a BPDU: "A compliant device must not validate
an MST BPDU based on the value encoded in the Protocol Version Identifier
field. This allows future versions of the Spanning Tree Protocol to use this
field while providing support for legacy versions" ([MSTP_conformance.pdf][unh-mstp],
Test MSTP.op.1.3, citing IEEE Std 802.1Q-2011 sub-clause 14.4). Refusing a
short version 3 payload would leave a netsim bridge facing that peer with
both ends Designated and Forwarding, an unbroken loop and a worse answer than
the RST-prefix approximation.

[unh-mstp]: https://www.iol.unh.edu/sites/default/files/testsuites/bfc/MSTP_conformance.pdf

## Multiple spanning tree instances (MSTP)

A region is a name (32 octets at most), a 16-bit revision, and a digest,
carried on the wire as a 51-octet configuration identifier with format
selector 0. The digest is HMAC-MD5 over the 4096-entry VID-to-MSTID table, two
big-endian octets per VID, VID 0 through 4095, keyed with

```
13 AC 06 A6 2E 47 FD 51 F9 5D 2B A2 43 CD 03 46
```

That key is in no other file in this repository, so the two vectors below are
what prove `MST.ConfigID` reproduces the standard construction rather than
some other one: the all-zero table (no instances configured) digests to
`ac36177f50283cd4b83821d8ab26de62`, and VID 10 on MSTID 1 with VID 20 on MSTID
2, every other VID zero, digests to `9357ebb7a8d74dd5fef4f2bab50531aa`.

`priorityVector` has six components, compared in order: root, external root
path cost, regional root, internal root path cost, designated bridge,
designated port. There is one comparator for both trees rather than one per
tree, because a constant leading component drops out of a lexicographic
comparison: an RSTP or CIST vector takes its regional root from its root and
leaves the internal cost zero, collapsing to the landed four-component RSTP
order, and an MSTI vector takes its root from its regional root and leaves
the external cost zero, collapsing to clause 13.11's MSTI order.

A port is internal when the BPDU it last received carries this bridge's own
configuration identifier, and a boundary port otherwise; an RST or
Configuration BPDU is always external. Only the CIST computes a boundary
port's role; every MSTI takes the CIST port's role there outright rather than
electing one from information a different region sent. That is also why an
MSTI never reports the Master role IEEE 802.1Q's prose uses for a CIST root
that isn't also an MSTI's regional root: both the CIST and MSTI role MIB
tables list exactly Root, Alternate, Designated, and Backup, so a boundary
port's MSTI mirrors the CIST's Root under the label the MIB can express, the
same forwarding answer under a different name.

See "How long received information lives" above for hop aging, which applies
to internal information on both the CIST and every MSTI.

## What a topology change flushes

`Effects.Flush` is a list of `FlushTarget{Port, FIDs}`, and the bridge's
`Flush` deletes a learned entry only when its port matches a target and that
target either names the entry's FID or names none at all. An empty `FIDs` means
every FID on the port.

A topology change on an instance flushes, on the bridge's other ports, only the
VLANs that instance carries. That is the point of the pair: moving VLAN 10's
tree must not discard what the bridge learned about VLAN 20, which did not move.

Three cases flush every FID instead. A link going down and a BPDU-guard disable
both leave every entry on that port stale whatever tree it belonged to. The
third is a topology change raised from the CIST, and it is worth being plain
about: the CIST carries every VLAN no instance claims, which is not a set this
layer can enumerate, so a CIST change names no FIDs and the bridge flushes the
port across all of them. On a boundary port that is what the standard wants
anyway, since a CIST change there reaches every tree. On an internal port it
discards more than it strictly must, costing a round of flooding to relearn
entries that were never stale. Narrowing it would need a target that can say
"every FID except these", which `FlushTarget` deliberately cannot.

## Not modeled

- Per-VLAN RSTP: the SSTP encapsulation, per-VLAN trees, and the PVID
  consistency check.
- Tagged BPDU emission. `Encode` emits untagged LLC frames.
- Automatic BPDU-guard recovery timers, and BPDU filter.
- MSTP L2GP, SPT, SPB, version 4 BPDUs, agreement digests, and Cisco
  pre-standard MSTI encoding, all of which decode as unsupported.
