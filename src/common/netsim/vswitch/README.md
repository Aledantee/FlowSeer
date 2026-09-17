# vswitch

A virtual switch evaluates standard Ethernet forwarding and physical-layer
allocations in pure Go. It executes synchronously in memory without goroutines
or wall clocks.

## Example

The example builds a switch with two access ports and one trunk port, forwards
an untagged frame, derives an updated state, and compares forwarding behavior
across the two states.

```go
package main

import (
	"fmt"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func main() {
	ports, err := port.NewBuilder().
		Range("1/1/%d", 1, 3, port.Port{
			AdminStatus: port.Up,
			OperStatus:  port.Up,
		}).
		Build()
	if err != nil {
		panic(err)
	}

	vid10 := vlan.ID(10)
	swCfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			AgingTime: bridge.DefaultAgingTime,
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "prod"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/3": {Tagged: []vlan.ID{10}},
				},
			},
		},
	}

	now := time.Unix(1700000000, 0)
	sw, err := vswitch.New(swCfg)
	if err != nil {
		panic(err)
	}

	frame := ethernet.Frame{
		Dst:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("hello"),
	}

	res := sw.Forward(now, "1/1/1", frame)
	fmt.Printf("Outcome: %s, Egress count: %d, Status: %s\n", res.Outcome, len(res.Egress), res.Metadata.Status())

	nextCfg := swCfg.Clone()
	b := port.NewBuilder()
	for _, p := range swCfg.Ports.Ports() {
		if p.Name == "1/1/2" {
			p.OperStatus = port.Down
		}
		b.Add(p)
	}
	nextCfg.Ports, err = b.Build()
	if err != nil {
		panic(err)
	}

	nextSpec := sw.Spec()
	nextSpec.Config = nextCfg
	nextSw, err := vswitch.Derive(sw, nextSpec)
	if err != nil {
		panic(err)
	}

	cmp := vswitch.Compare(sw, nextSw, now, "1/1/1", frame)
	fmt.Printf("Same forwarding: %t\n", cmp.Same)
}
```

## Capability ladder

The virtual switch uses a ladder of architectural layers:

- **Hub**: Configured with a `port.Table` alone. Frames received on an active
  port repeat to all other active forwarding ports. The hub does not inspect
  VLAN tags or learn MAC addresses.
- **Bridge**: Configured with a `port.Table` and `bridge.Config` without a VLAN
  subsystem. Learns source addresses into a single filtering database (FID 0),
  filters frames destined to known ports, and drops IEEE bridge management
  group addresses unless `ForwardBPDU` is set to forward them like any group address.
  A table bound (`MaxEntries`) evicts the oldest dynamic entry when full, and
  `Counters` tracks learned, expired, evicted, and moved entries. A bridge can
  designate `ProtectedPorts` that never forward traffic to one another. All tags
  are treated as payload.
- **Switch**: Configured with a `port.Table` and `bridge.Config` containing a
  `bridge.VLAN`. Performs 802.1Q ingress classification, admission checks,
  ingress VLAN filtering, per-VLAN learning, and egress tag rewrites.
  Configuring `FloodVLANs` disables learning and floods every frame in those
  VLANs. An untagged egress port can emit priority tags under a `PriorityTags`
  policy (`Never`, `IfNonzero`, `Always`). A port configured with a `Tunnel`
  pushes an outer service tag on ingress and pops it on egress; for example, a
  customer-tagged frame arriving on a tunnel port egresses a trunk port carrying
  an outer S-tag with TPID 0x88A8 plus the customer C-tag.
- **Link aggregation**: Configured with a `port.Table` containing LAG ports and
  optional `lag.Config`. Manages bond modes (ActiveBackup, BalanceSLB,
  BalanceTCP), member link transitions with up/down delays, and the LACP
  exchange. Chooses enabled member ports for frames egressing a LAG, intercepts
  LACPDUs on member ports, and derives the LAG row's operational state from its
  members' links.
- **Spanning tree**: Configured with a `port.Table`, `bridge.Config`, and
  `stp.Config`. Intercepts RSTP BPDUs (01-80-C2-00-00-00), and per-VLAN BPDUs
  (01-00-0C-CC-CC-CD) when the configuration runs a tree per VLAN, to elect the
  root bridge and compute loop-free port states. Implements the bridge gate to
  block traffic on Discarding ports while learning on Learning ports. The gate
  is asked about a port and a VLAN, and the bridge asks it after classifying
  the frame, so a drop names the VLAN it was classified into and a frame the
  port would never have admitted reports the classification reason rather than
  `port-blocked`. Outside PVST there is exactly one tree, the CIST, and every
  VLAN maps to it, so the two answers agree; under PVST each VLAN maps to a
  tree of its own, and a VLAN's answer can differ from the common tree's.
- **Loop protection**: Configured with a `port.Table`, `bridge.Config`, and
  `loopprotect.Config`. Independent of spanning tree, it periodically emits a
  probe frame addressed to its own group MAC out each protected port, naming
  the sending switch and port in the payload. A returning probe naming this
  switch is classified through the bridge's ordinary VLAN and gate pipeline
  first, exactly like any other frame, so a gated ingress port still denies
  it; only once that pipeline admits it does the port named in the payload
  receive the configured action (`Block`, `NoLearn`, or `Disable`) and, when
  the classified VLAN disagrees with the one the probe was sent on, an
  inter-VLAN finding. A probe naming another switch is left untouched and
  floods like any other frame addressed to an unregistered multicast group.
  Implements the same bridge gate interface as spanning tree, under its own
  scope, so the two coexist on one bridge without one replacing the other's
  gate.
- **Multicast snooping**: Configured with VLAN-aware `bridge.Config` and
  `mcast.Config`. IGMP and MLD reports register group members, while queries
  identify router ports. `Switch.Resolve` filters admitted member ports by
  the frame's decoded IP source, so an `(S,G)` join admits that source and no
  other; router ports always receive registered traffic. Each VLAN chooses
  whether an unregistered group floods or reaches router ports only.
  Link-local control groups remain on the ordinary flood path. A non-fast
  leave keeps forwarding until an observed query lowers its timer to the
  last member query time or, without one, until the full membership interval
  elapses; the runtime issues below cover the completeness signal that gap
  raises.
- **Routing**: Configured with `routing.Config` containing VRFs and routed
  interfaces. An interface has one of three shapes: a VLAN interface (routed
  presence of a classified VLAN), a routed port (physical or LAG port that
  belongs to no VLAN and bridges nothing), or a routed sub-interface (both
  fields set), which classifies frames arriving on its port by their outer
  VLAN tag rather than by bridge VLAN membership. A routed port bypasses the
  relay entirely; the frame's outer tag, or its absence, must name an
  interface configured on that port — a sub-interface at that tag's VID, or
  the untagged interface when the frame carries none — or the frame drops
  with `not-bridged` before ownership is even checked. This applies to every
  routed port, so a tagged frame on a plain untagged routed port drops the
  same way a frame at an unconfigured VID on a trunk of sub-interfaces does;
  a frame that does name a configured interface but is not addressed to its
  MAC drops with the same reason afterward, once ownership is checked. For
  frames arriving on a VLAN or routed port, routing
  evaluates interface ownership (`Owns`), classification, local destination
  consumption (`not-routed`), hop limit verification, route lookup, and neighbor
  resolution. A lookup takes the longest matching prefix, then the lowest
  preference, then the lowest metric; whatever ties on all three is a candidate
  set an RFC 2992 hash-threshold over a layer-3 flow hash picks from, and the
  trace fact names every candidate beside the chosen one, so a reader can see the
  path a flow would move to. A static route whose next hop is not on-link is
  resolved against the table when the table is built; one that self-recurses,
  exceeds the recursion bound, or reaches no connected interface is withdrawn
  rather than installed, so its packets fall through to whatever else matches
  and `routing.Layer.WithdrawnRoutes` names the route, the reason, and the chain
  walked. `routing/README.md` gives the rules in full. The hop limit is
  decremented by one and packets reaching
  zero drop without forwarding per RFC 1812 section 5.3.1. Routed packets are
  re-encapsulated with the egress interface source MAC and next-hop neighbor
  destination MAC, handing off to the relay (for VLAN egress) or the port
  layer (for a routed port or a sub-interface). A plain routed port leaves
  the frame untagged; a sub-interface's egress carries one tag naming its
  VLAN, the ingress priority code point, and the ingress drop eligible
  indicator, on the live and the released path alike. A sub-interface resolves
  neighbors under its own interface name, and an arriving ARP or Neighbor
  Discovery advertisement binds the sub-interface its own outer VLAN tag names
  and no other; on a routed port whose lookup misses, it binds nothing, since
  such a port has no bridge membership to fall through to.
- **Traffic**: Configured with `traffic.Config`. The switch reserves mirror
  output ports from ordinary ingress and egress, then creates selected mirror
  copies after bridge, hub, or routed forwarding. A copy is exposed only when
  its output port and any selected LAG member are known up. Unknown output state
  suppresses the copy and makes the forwarding result incomplete; known-down
  state suppresses it with complete readiness. `Copies` returns and clears the
  admitted copies so the fabric can enqueue them as separate transmissions.
  `Police` applies the switch-owned token bucket when the fabric checks a frame
  at arrival, while `QueueMaxRate` gives the fabric scheduler the maximum rate
  for an egress port and PCP.

Optional physical subsystems (`phy.Config`) provide physical Ethernet speed
resolution, auto-negotiation, and Power over Ethernet budget allocation.

## Standards and rules

Forwarding and allocation behavior follows standard specifications:

- **Switchport admission default**: Ports default to `admitAll` frame types per
  `spec/mib/ietf/Q-BRIDGE-MIB:1413` (`dot1qPortAcceptableFrameTypes`).
- **Ingress filtering default**: Ingress filtering defaults to false per
  `spec/mib/ietf/Q-BRIDGE-MIB:1437` (`dot1qPortIngressFiltering`).
- **Aging time default**: Bridge aging time defaults to 300 seconds per the
  802.1D recommendation in `spec/mib/ietf/BRIDGE-MIB:778` (`dot1dTpAgingTime`).
- **Priority-tagged classification**: Frames with C-TAG (0x8100) and VID 0
  classify to the port PVID per `spec/mib/ietf/Q-BRIDGE-MIB:1379`.
- **Reserved group addresses**: Destination MAC addresses in the range
  `01-80-C2-00-00-00` through `01-80-C2-00-00-0F` are reserved for bridge
  management protocols. The bridge drops them without learning their sources,
  unless `ForwardBPDU` is set, when the bridge forwards them like any group address
  (see the IEEE Registration Authority at
  https://standards.ieee.org/products-programs/regauth/grpmac/public/).
- **Service tag TPID**: A tunnel port pushes an S-tag with TPID 0x88A8
  (IEEE 802.1ad; the loader's `qinq_ethtype` default follows Open vSwitch's
  `qinq-ethtype` option).
- **Base MAC assignment**: A switch has one base MAC (`Config.MAC`). When omitted,
  `New` assigns the first unused locally administered unicast address
  (`02:00:00:xx:xx:xx` per IEEE Std 802-2014 clause 8.2.2) not used by any
  explicit interface or bridge address in the configuration. The base MAC fills
  zero-valued routed interface addresses and spanning tree bridge addresses.
  Two standalone switches may assign the same address; a fabric assigns across nodes.
- **Routed frame classification**: A forwarded routed frame's `Result.FID`
  records the egress VLAN on a VLAN interface, or zero on a routed port; a
  refused one keeps the ingress VLAN, zero when it arrived on a routed port.
- **PoE power budget**: PSE groups allocate power against nominal capacity per
  `spec/mib/ietf/POWER-ETHERNET-MIB:420` (`pethMainPsePower`). Ports
  allocate power by priority using IEEE 802.3 standard class limits (classes
  0 and 3 allocate 15.4 W; 1 allocates 4.0 W; 2 allocates 7.0 W; 4 allocates
  30.0 W; 5 through 8 allocate 45 W, 60 W, 75 W, and 90 W).
- **Spanning tree timing and migration**: Protocol migration and bridge detection
  follow IEEE 802.1D-2004 clauses 17.24 and 17.25 and Table 17-1. Transmit rate
  limiting defaults to 6 frames per second per `spec/mib/ietf/RSTP-MIB:73`
  (`dot1dStpTxHoldCount`), management protocol migration check follows
  `spec/mib/ietf/RSTP-MIB:130` (`dot1dStpPortProtocolMigration`), and CIST
  auto-edge follows `spec/mib/ieee/IEEE8021-MSTP-MIB-201806210000Z.mib:1426`
  (`ieee8021MstpCistPortAutoEdgePort`).

## Loader defaults

When importing from network model protobuf messages, `netmodel.Load` populates
omitted values with standard defaults and records each in the `Report`:

| Field               | Default value    | Standard or source                  |
| ------------------- | ---------------- | ----------------------------------- |
| `aging_time`        | 300s             | BRIDGE-MIB:778 dot1dTpAgingTime     |
| `frame_admission`   | `admitAll`       | Q-BRIDGE-MIB:1413 AcceptableFrame   |
| `ingress_filtering` | `false`          | Q-BRIDGE-MIB:1437 IngressFiltering  |
| `pvid`              | untagged VLAN ID | Port has exactly one untagged VID   |
| `qinq_ethtype`      | 0x88A8           | Open vSwitch qinq-ethtype, 802.1ad  |
| `mtu`               | unlimited (0)    | Loader fallback when MTU is absent  |
| `tx_hold_count`     | 6                | RSTP-MIB:73 dot1dStpTxHoldCount     |
| `max_class`         | 8                | No net/phy message carries one      |
| `priority`          | none (last)      | PoeSettings without a priority      |
| `power_milliwatts`  | 0 mW             | PseBudget without a budget          |
| `bond_mode`         | `active-backup`  | Open vSwitch bond_mode default      |
| `lacp`              | `off`            | Open vSwitch lacp default           |

## Drop reasons

Drop reasons recorded in traces and egress records:

| Reason             | Meaning                                                 |
| ------------------ | ------------------------------------------------------- |
| `port-down`        | Ingress or egress port is administratively or operationally down |
| `reserved-address` | Frame destination is in the IEEE reserved bridge range  |
| `admission`        | Tag format rejected by port admission filter            |
| `ingress-filter`   | Ingress port is not a member of the classified VLAN     |
| `undefined-vlan`   | Classified VLAN ID is missing from the VLAN table       |
| `customer-vlan`    | Customer VLAN not in the tunnel port's list             |
| `no-pvid`          | Untagged frame arrived on port without a PVID           |
| `same-port`        | Destination MAC learned on ingress port (no reflection) |
| `protected`        | Frame between two protected ports                       |
| `mtu-exceeded`     | Frame payload length exceeds egress port MTU            |
| `not-member`       | Known unicast's port is not a member of the VLAN        |
| `no-egress`        | No forwarding member port other than the ingress port   |
| `no-member`        | LAG has no enabled member port to transmit egress frame |
| `port-blocked`     | Port is blocked from learning or forwarding by spanning tree or loop protection |
| `unsupported-bpdu` | Frame could not be decoded as a BPDU                    |
| `unsupported-lacpdu` | Frame could not be decoded as an LACPDU               |
| `no-route`         | No route in the VRF table matches the destination IP    |
| `ttl-expired`      | Ingress IP hop limit is 1 or less (RFC 1812 section 5.3.1) |
| `neighbor-miss`    | Next hop's VRF resolves no neighbors, or the entry already failed |
| `neighbor-pending` | Next hop's neighbor entry is newly or still unresolved (not a drop; outcome `Held`) |
| `neighbor-hold-overflow` | Held frame evicted to make room in the hold queue for a newer one |
| `held-interface-unknown` | Held frame released onto an interface the routing configuration no longer resolves |
| `held-cause-unknown` | Held frame released under a routing hold-queue exit cause this package does not recognize |
| `not-routed`       | Frame addressed to local interface address (consumed)   |
| `bad-header`       | IP packet header failed decoding or checksum validation |
| `not-bridged`      | Frame on a routed port not addressed to interface MAC, or its outer VLAN tag (or the lack of one) names no interface configured on the port, or names one but at a tag protocol the port's interfaces cannot classify |
| `policed`          | Ingress frame exceeded the port's token bucket           |
| `mirror-output`    | Ordinary frame used a port reserved for mirror copies    |
| `unregistered`    | Unregistered group had flooding disabled and no router port |
| `no-router-port`  | Membership report or leave had no router port destination |
| `bad-control`     | IGMP or MLD failed its outer-header or message validation |

## Protocol schedule

The spanning tree, loop protection, and link aggregation layers operate
deterministically without background timers. The host drives them through
explicit calls:

- `Start(now)` initializes link state across all ports from the port table.
- `LinkChange(now, port, state, pointToPoint, speed)` tells the protocol layers
  one link moved; `state` ([port.LinkState]) preserves unknown operational state
  while informing protocol layers that the link is non-operational. `pointToPoint`
  ([PointToPoint]) conveys operational point-to-point duplex status. For a LAG
  member, it notifies the aggregation layer, updates the LAG port's operational
  state, and informs spanning tree of the LAG's link and speed from the enabled
  members.
- `SetOperStatus(port, state)` rewrites the port in the switch's and the
  relay's tables and tells the protocol layers nothing, since only the caller
  knows whether a member's change moves its LAG; it follows with `LinkChange`.
  An invalid link state returns a structured error without changing either
  table.
- `Mcheck(now, port)` forces protocol migration checking on the named port.
- `ClearLoopProtect(now, port)` manually lifts the loop-protection action
  applied to the named port.
- `Wake(now)` fires due timers across spanning tree, loop protection, link
  aggregation, and neighbor resolution, flushing bridge entries, triggering
  periodic transmissions, releasing a held frame whose entry resolved since
  the last wake, and failing one whose resolution deadline passed. A
  sub-interface's held frames leave tagged on its parent port rather than
  through the bridge.
- `NextWake()` reports the earliest deadline when the switch needs a wake
  across all four layers.
- `Drain()` returns and clears pending frame emissions produced by the
  protocol layers and by a released held frame.
- `DrainNeighborFailures()` returns and clears the `NeighborDrop` records
  `Wake` made for held frames that reached no wire, the released half's
  counterpart: a frame that vanished with neither a record nor an emission
  would be the same silent answer the neighbor lifecycle exists to remove.
  It is every exit from a hold queue that is not an emission, not timeouts
  alone — a frame the queue pushed out to make room under
  `routing.ReasonNeighborHoldOverflow`, and a released frame the bridge or
  the port table then refused, are both here. Each record carries the reason
  the refusing stage gave and the port it is counted against, empty when no
  single port owns it, so a consumer is told what happened rather than
  assuming a neighbor miss and counting it against a name that is not a port.
- `Roles()` exposes current port roles and forwarding states.
- `LagInfo(lag)` returns the runtime aggregation status of the named LAG.
- `MemberInfo(member)` returns the runtime aggregation status of the member port.
- `SelectMember(now, lag, frame, vid)` commits an enabled member choice for a
  frame egressing a LAG outside the bridge pipeline, such as a fabric
  transmission; `PeekMember` computes the same choice without committing it.
  The bridge's own LAG egress commits exactly when the forwarding call that
  produced it does (`Forward` commits, `Peek` does not).

On a switch configured with `stp.Config`, a frame addressed to
01-80-C2-00-00-00 is intercepted before relay processing; its trace ends with
outcome `Consumed`, or `port-down` when the port it arrived on is not up. A
switch without the layer drops it as a reserved address, unless the bridge's
`ForwardBPDU` is set.

A frame addressed to 01-00-0C-CC-CC-CD is intercepted the same way, with one
more source of `port-down`: the layer's own per-port link state, tracked
separately from the port table's up/down check above, refuses a port it has
not configured or has not yet seen a link-up event for, even though the port
table itself calls the port up. `Forward` and `Peek` agree on this: `Peek`
asks the layer's `PortLinked` directly rather than deriving the answer from a
snapshot, so it renders `port-down` for exactly the ports `Forward` would.
Its VLAN and whether it counts as tagged both come from one test of the outer
tag's TPID, the same test the bridge's own ingress classification makes,
rather than from the bridge's ingress pipeline itself: that pipeline applies the spanning tree gate, and the ports a tree
holds discarding are exactly the ones whose blocking depends on continuing to
hear their peer. A VID of 0 under a dot1Q TPID is a priority tag, not a VLAN
selection, and an outer tag whose TPID names neither dot1Q nor the codec's
untagged zero value — an 802.1ad/QinQ tag, say — is not a VLAN tag at all;
either way the frame resolves onto the port's untagged VLAN the same as no
tag at all, and the frame `PriorityTags: Always` emits on an otherwise
untagged port must not be read as a VLAN 0 BPDU. Bypassing the spanning tree
gate does not bypass the bridge's own notion of which VLANs a port speaks:
the switch computes `bridge.VLAN.AdmitsVIDOnIngress` for the resolved VLAN on
the ingress port and hands the answer to the layer as `SSTPArrival.Admitted`,
rather than refusing the frame itself. The layer decides what a refusal
means — BPDU guard still fires on a VLAN the port does not admit, because the
link half of a receive always runs before the tree half is judged — and a
frame for a VLAN the port does not admit is traced as
`stp.sstp.vlan-not-admitted` rather than as a decode failure, whatever the
layer went on to do with the link half. A VLAN the port does admit but this
bridge runs no tree for is traced the same way, as `stp.sstp.vlan-untracked`.
Every other outcome — the BPDU applied to its tree, BPDU guard firing, a PVST
boundary neighbor, or a PVID mismatch — is traced as `stp.sstp.admit`: the
frame's own VLAN was admitted and tracked, so whatever the tree half decided,
the layer processed the frame. The trace step names both the VLAN the BPDU
claims and the VLAN it arrived on, so a PVID inconsistency is readable from
the journey.

Emissions run the other way: a `stp.Emission` naming a VLAN goes out through
`bridge.OriginateFrame`, so a per-VLAN BPDU is tagged where the VLAN is
tagged, untagged where it is the port's untagged VLAN, and withheld where the
port does not carry it. An emission naming no VLAN leaves untagged and
unchecked, which is what the IEEE-addressed frame needs on a trunk with no
native VLAN. A switch carrying a VLAN with no tree is refused at construction,
naming `stp.pvst.trees`: that VLAN would have no defined forwarding state. A
`PVST` configuration on a bridge with no VLAN table is refused outright,
naming `stp.pvst`: with no VLAN table every port's untagged VLAN resolves to
0, so two such switches read each other's BPDUs as arriving on the wrong
VLAN and block VLAN 1 permanently. There is nothing per-VLAN about a bridge
that carries no VLANs.

On a switch configured with `loopprotect.Config`, a frame addressed to the
loop-protection probe group is intercepted before relay processing, but only
once decoded as a probe this switch itself sent: a probe naming another
switch is left alone and falls through to the ordinary relay path, where it
floods as unregistered multicast, so it is classified exactly once either
way.

A probe goes out where an ordinary frame would: the port must be
operationally forwarding, and a spanning tree on the same switch must forward
the VLAN over it, so a port the tree holds discarding raises no loop the tree
has already broken. Putting a probe on the wire applies the emitting port's
own egress VLAN tagging, the same as any other frame the switch originates.
The one gate transmission ignores is loop protection's own, which is what
lets a blocked port keep probing and a `LoopCleared` recovery watch the loop
persist. The returning probe is classified like any other frame and dies on
a gated ingress port — that is what stops the reciprocal probe of a two-port
loop from acting on the second port. Once a returned probe applies a
forwarding-denying action (`Block` or `Disable`), the switch flushes that
port's learned forwarding entries, the same as spanning tree flushes on a
topology change, so traffic to a host the loop taught that port floods to
relocate it instead of following a stale entry into the now-blocked port.

On a switch with link aggregation, a frame with EtherType 0x8809 whose first
payload octet is 1 arriving on an up member port is intercepted before relay
processing. It decodes as an LACPDU and enters the aggregation layer with
outcome `Consumed`, or drops with `unsupported-lacpdu` if decoding fails. On a
port that is not a LAG member, it drops as a reserved address.

A port that hears a version 0 BPDU after its 3 s migration delay sends
Configuration BPDUs until `Mcheck` or an RST BPDU after another delay returns it
to RSTP. Migration is a property of the link, not of one VLAN's tree: under
PVST a migrated port sends VLAN 1's untagged IEEE Configuration BPDU alone,
and every other VLAN's tree sends nothing for that port until the port
migrates back, because the codec SSTP shares with RSTP has no legacy form to
carry a per-VLAN Configuration BPDU in. Under `AutoEdge`, a proposing
point-to-point port becomes an edge port after 3 s without receiving a BPDU;
any received BPDU revokes that edge status.
The transmit hold count (`TxHoldCount`, default 6 per second per port) caps
transmission rates; held BPDUs leave at the next tick. `PortInfo` reports
cumulative per-port counters for transmitted, received, and undecodable
(`BadBPDUs`) frames alongside `SendRSTP`.

## Denied PoE port status

The exported `PoeFacet.status` follows the allocation: a powered port is
`DELIVERING_POWER`; a port with no attached device, or one denied for the
group's budget or its own limit, is `SEARCHING`, the state RFC 3621's
`pethPsePortDetectionStatus` gives a PSE port that is enabled and probing
for a device, which fits a port that would be powered when capacity returns;
a port whose delivery is disabled is `DISABLED`; a device of a class the
port cannot source is `FAULT`. A port with no attached device exports no
`power_class` and draws nothing from the budget.

## Construction specifications and trust metadata

Exported constructors validate and normalize configurations:

- [New] normalizes and validates the input configuration, returning an error
  if port tables or subsystem invariants fail.
- [NewWithSpec] constructs a switch from a [ConstructionSpec], restoring preloaded
  forwarding database seeds and construction trust metadata alongside normalized
  configuration. [Switch.Spec] extracts an independent deep copy of this
  specification for exact reproducibility.
- [Derive] takes the target [ConstructionSpec]. Static forwarding entries, node
  identity, and trust metadata come only from that target; they are not copied
  from the current switch. A target dynamic seed yields to the current switch's
  learned entry for the same FID and MAC, while a target static seed remains
  authoritative. Seed timestamps normalize to their UTC wall-clock instant.
  LAG runtime state is retained only while every member's administrative and
  operational state matches the target. To retain existing target trust while
  changing its configuration, start with [Switch.Spec] and replace its `Config`
  field.
- [Switch.Forward] and [Switch.Peek] return [ForwardResult], combining the domain
  [bridge.Result] with [analysis.Metadata] recording scoped issues, operational
  readiness, and evidence. A port with unknown operational status never forwards
  and attaches an Incomplete issue scoped to that port, while known-down ports
  drop traffic authoritatively with Complete readiness.
- [Switch.Power] returns [PowerResult], combining [phy.Allocation] with
  [analysis.Metadata]. The metadata records an Incomplete issue
  (`poe-demand-unknown`) for each port whose power demand is uncertain,
  keeping PoE uncertainty scoped to the port's PoE field and out of forwarding
  metadata.
- Protocol layers maintain `protocol-link-unknown` Incomplete issues. When
  spanning tree computes topology while any STP port's operational state or
  point-to-point duplex status is unknown, every STP port on the switch receives
  an issue. When a LAG computes membership or operational state with an unknown
  member, the LAG and all its member ports receive an issue. Results that consult
  those ports attach these issues to their forwarding metadata.
- A forward or peek that used a balanced-mode LAG selection old enough that
  unmodeled rebalancing could have moved it raises `lag-rebalance-unmodeled`
  on that LAG's aggregator scope, only for the journeys that went through it.
- A forward or peek that found a next hop pending — a neighbor netsim never
  asked about, rather than one it knows has no answer — raises
  `neighbor-unresolved` on `routing.NeighborLookupScope(nodeID, vrf, iface,
  addr)`.
- A forward or peek that resolved multicast membership while an expected
  group-specific or group-and-source-specific query had gone unobserved past
  its last member query time raises `mcast-query-unobserved` on
  `analysis.ProtocolScope(nodeID, "mcast", "<vid>/<group>")`.
- A forward or peek that crossed a port facing a spanning tree neighbor whose
  per-VLAN trees this switch cannot simulate raises `stp-pvst-boundary` on
  `analysis.ProtocolScope(nodeID, "stp", "<port>/<vid>")`, for every VLAN but
  VLAN 1. VLAN 1 is excluded because it does converge across the boundary: a
  per-VLAN bridge sends VLAN 1's tree to the IEEE bridge group address as
  well, and that frame reaches an RSTP or MSTP neighbor's common tree
  unchanged. The scope names port and VLAN together rather than nesting a VLAN
  inside a port scope, because scope containment is a key-prefix test and the
  bridge consults the port's own spanning tree scope on every gated frame. A
  VLAN the ingress port does not admit does not suppress this issue:
  `ReceiveSSTP` marks the boundary before it reads `SSTPArrival.Admitted`, so
  a non-PVST bridge still reports the neighbor it cannot simulate even for a
  VLAN it would otherwise refuse on ingress.

`ConstructionSpec.NodeID` is the stable node key used to construct node and port
scopes. An empty key identifies an anonymous standalone switch and uses the
anonymous node scope. Non-empty construction metadata must evaluate the named or
anonymous node, or one of its children. Issue and assumption scopes may also be
whole-analysis scopes because they affect every node. Canonical zero metadata
remains valid. Every evidence reference on an issue or assumption must resolve
in the metadata's evidence catalog. A fabric uses its switch map key as the node
identity. `netmodel.Load` uses `SourceContext.DeviceID`.

Forwarding database seeds require a bridge relay. Their MAC addresses must be
usable unicast addresses, their ports must resolve to an admitted logical port,
and their FIDs must belong to that port. Duplicate FID and MAC pairs are rejected.
[Switch.Learn] applies the same validation and leaves the switch unchanged when
it returns an error.

Forwarding combines runtime issues with construction issues on the exact
dependencies the result consulted. These include ports, spanning tree ports,
routing lookups, forwarding database source-learning and destination-lookup
keys, and LAG aggregators used by member processing or egress selection. An
issue on a sibling dependency is left out, while node and whole-analysis issues
are included in every result.
Relevant assumptions and evidence references travel with the retained issues.
The combined issues, assumptions, and evidence catalog use their canonical
ordering, so repeated forwarding and cloned specifications produce the same
metadata. An issue's message is retained for people but does not change
construction identity or generated runtime evidence; code, status, scope, and
stable facts carry its semantics. An ingress name absent from the port table
remains a consulted, normalized port with explicit Unknown states, so its scoped
construction issues still reach hub, bridge, routing, and protocol-interception
results.

## Concurrency contract

A `vswitch.Switch` holds the forwarding database and is not safe for
concurrent use. A caller that needs parallel runs serializes its calls or
builds one switch per goroutine. `Config` holds maps and slices, so a copy
that will be shared comes from `Clone`.
