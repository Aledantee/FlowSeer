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
	sw := vswitch.New(swCfg)

	frame := ethernet.Frame{
		Dst:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("hello"),
	}

	res := sw.Forward(now, "1/1/1", frame)
	fmt.Printf("Outcome: %s, Egress count: %d\n", res.Outcome, len(res.Egress))

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

	nextSw, err := vswitch.Derive(sw, nextCfg)
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
- **Spanning tree**: Configured with a `port.Table`, `bridge.Config`, and
  `stp.Config`. Intercepts RSTP BPDUs (01-80-C2-00-00-00) to elect the root
  bridge and compute loop-free port states. Implements the bridge gate to
  block traffic on Discarding ports while learning on Learning ports.
- **Routing**: Configured with `routing.Config` containing VRFs and routed
  interfaces. An interface has one of two shapes: a VLAN interface (routed
  presence of a classified VLAN) or a routed port (physical or LAG port that
  belongs to no VLAN and bridges nothing). A routed port bypasses the relay
  entirely; frames arriving on it must be addressed to the interface MAC or drop
  with `not-bridged`. For frames arriving on a VLAN or routed port, routing
  evaluates interface ownership (`Owns`), classification, local destination
  consumption (`not-routed`), hop limit verification, longest-prefix route lookup,
  and neighbor resolution. The hop limit is decremented by one and packets reaching
  zero drop without forwarding per RFC 1812 section 5.3.1. Routed packets are
  re-encapsulated with the egress interface source MAC and next-hop neighbor
  destination MAC with no tags, handing off to the relay (for VLAN egress) or
  the port layer (for a routed port).

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
| `mtu`               | unlimited (0)    | Interface reporting an MTU of 0     |
| `tx_hold_count`     | 6                | RSTP-MIB:73 dot1dStpTxHoldCount     |
| `max_class`         | 8                | No net/phy message carries one      |
| `priority`          | none (last)      | PoeSettings without a priority      |
| `power_milliwatts`  | 0 mW             | PseBudget without a budget          |

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
| `port-blocked`     | Port is blocked from learning or forwarding by spanning tree |
| `unsupported-bpdu` | Frame could not be decoded as a BPDU                    |
| `no-route`         | No route in the VRF table matches the destination IP    |
| `ttl-expired`      | Ingress IP hop limit is 1 or less (RFC 1812 section 5.3.1) |
| `neighbor-miss`    | Next-hop IP address has no matching neighbor MAC entry  |
| `not-routed`       | Frame addressed to local interface address (consumed)   |
| `bad-header`       | IP packet header failed decoding or checksum validation |
| `not-bridged`      | Frame on a routed port not addressed to interface MAC   |

## Protocol schedule

The spanning tree layer operates deterministically without background timers.
The host drives it through explicit calls:

- `Start(now)` initializes link state across all ports from the port table.
- `LinkChange(now, port, up, pointToPoint, speed)` tells the layer one link
  moved; a report of the state the port already has is ignored.
- `SetOperStatus(port, state)` rewrites the port in the switch's and the
  relay's tables and tells the layer nothing, since only the caller knows
  whether a member's change moves its LAG; it follows with `LinkChange`.
- `Mcheck(now, port)` forces protocol migration checking on the named port.
- `Wake(now)` fires due hello, forward delay, edge delay, and topology change
  timers and releases BPDUs the transmit hold count held.
- `NextWake()` reports the earliest deadline when the switch needs a wake.
- `Drain()` returns and clears pending frame emissions produced by the layer.
- `Roles()` exposes current port roles and forwarding states.

On a switch configured with `stp.Config`, a frame addressed to
01-80-C2-00-00-00 is intercepted before relay processing; its trace ends with
outcome `Consumed`, or `port-down` when the port it arrived on is not up. A
switch without the layer drops it as a reserved address, unless the bridge's
`ForwardBPDU` is set.

A port that hears a version 0 BPDU after its 3 s migration delay sends
Configuration BPDUs until `Mcheck` or an RST BPDU after another delay returns it
to RSTP. Under `AutoEdge`, a proposing point-to-point port becomes an edge port
after 3 s without receiving a BPDU; any received BPDU revokes that edge status.
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

## Concurrency contract

A `vswitch.Switch` holds the forwarding database and is not safe for
concurrent use. A caller that needs parallel runs serializes its calls or
builds one switch per goroutine. `Config` holds maps and slices, so a copy
that will be shared comes from `Clone`.
