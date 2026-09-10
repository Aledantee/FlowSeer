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
  group addresses. All tags are treated as payload.
- **Switch**: Configured with a `port.Table` and `bridge.Config` containing a
  `bridge.VLAN`. Performs 802.1Q ingress classification, admission checks,
  ingress VLAN filtering, per-VLAN learning, and egress tag rewrites.

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
  management protocols. The bridge drops them without learning their sources
  (see the IEEE Registration Authority at
  https://standards.ieee.org/products-programs/regauth/grpmac/public/).
- **PoE power budget**: PSE groups allocate power against nominal capacity per
  `spec/mib/ietf/POWER-ETHERNET-MIB:420` (`pethMainPsePower`). Ports
  allocate power by priority using IEEE 802.3 standard class limits (classes
  0 and 3 allocate 15.4 W; 1 allocates 4.0 W; 2 allocates 7.0 W; 4 allocates
  30.0 W; 5 through 8 allocate 45 W, 60 W, 75 W, and 90 W).

## Loader defaults

When importing from network model protobuf messages, `netmodel.Load` populates
omitted values with standard defaults and records each in the `Report`:

| Field               | Default value    | Standard or source                  |
| ------------------- | ---------------- | ----------------------------------- |
| `aging_time`        | 300s             | BRIDGE-MIB:778 dot1dTpAgingTime     |
| `frame_admission`   | `admitAll`       | Q-BRIDGE-MIB:1413 AcceptableFrame   |
| `ingress_filtering` | `false`          | Q-BRIDGE-MIB:1437 IngressFiltering  |
| `pvid`              | untagged VLAN ID | Port has exactly one untagged VID   |
| `mtu`               | unlimited (0)    | Interface without explicit MTU      |
| `power_milliwatts`  | 0 mW             | POWER-ETHERNET-MIB:420 PseBudget    |

## Drop reasons

Drop reasons recorded in traces and egress records:

| Reason             | Meaning                                                 |
| ------------------ | ------------------------------------------------------- |
| `port-down`        | Ingress or egress port is administratively or operationally down |
| `reserved-address` | Frame destination is in the IEEE reserved bridge range  |
| `admission`        | Tag format rejected by port admission filter            |
| `ingress-filter`   | Ingress port is not a member of the classified VLAN     |
| `undefined-vlan`   | Classified VLAN ID is missing from the VLAN table       |
| `no-pvid`          | Untagged frame arrived on port without a PVID           |
| `same-port`        | Destination MAC learned on ingress port (no reflection) |
| `mtu-exceeded`     | Frame payload length exceeds egress port MTU            |

## Denied PoE port status

When total port demands exceed a PSE group budget, lower-priority ports are
denied power. In exported telemetry, denied ports report status
`POE_STATUS_SEARCHING`.

Under IEEE 802.3 clause 33.2.4.4 and RFC 3621 (`pethPsePortDetectionStatus`),
searching is the state where a PSE port is enabled and actively probing for a
PD without delivering power. Because the port remains administratively enabled
and will receive power if capacity becomes available, searching represents
pending allocation rather than a hardware fault or administrative shutdown.

## Concurrency contract

A `vswitch.Switch` holds the forwarding database and is not safe for
concurrent use. A caller that needs parallel runs serializes its calls or
builds one switch per goroutine. `Config` holds maps and slices, so a copy
that will be shared comes from `Clone`.
