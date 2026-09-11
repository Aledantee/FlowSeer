# lag

Package `lag` implements link aggregation (IEEE 802.1AX) for the virtual switch.
It governs member selection across bond modes, link up and down delays, and
the LACP actor and partner state machines matching Open vSwitch behavior.

The layer runs deterministically in memory without background goroutines or wall
clocks. Time advances through explicit, time-stamped calls to `LinkChange`,
`Receive`, and `Wake`.

## Example

The example builds a two-member LAG in active-backup mode, brings both member
links up, selects an egress member, and fails over when the active member goes down.

```go
package main

import (
	"fmt"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func main() {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1"}).
		Build()
	if err != nil {
		panic(err)
	}

	sysMAC, err := netaddr.Parse("02:00:00:00:00:01")
	if err != nil {
		panic(err)
	}

	cfg := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode:    lag.ActiveBackup,
				Primary: "1/1/1",
			},
		},
	}

	layer := lag.New(cfg, ports, sysMAC)

	t0 := time.Unix(1700000000, 0)
	layer.LinkChange(t0, "1/1/1", true)
	layer.LinkChange(t0, "1/1/2", true)

	frame := ethernet.Frame{}
	member, ok := layer.Select("lag1", frame, 0)
	fmt.Printf("Selected member: %s (ok=%t)\n", member, ok)

	t1 := t0.Add(time.Second)
	fx := layer.LinkChange(t1, "1/1/1", false)
	fmt.Printf("Changed LAGs: %v\n", fx.Changed)

	member2, ok2 := layer.Select("lag1", frame, 0)
	fmt.Printf("Selected member after failover: %s (ok=%t)\n", member2, ok2)
}
```

## Bond modes and hashing

The layer exposes three bond modes via `Mode`:

- `ActiveBackup` (""): Transmits through `Primary` if that member is enabled.
  If the primary is disabled or unspecified, transmits through the enabled member
  with the alphabetically lowest name.
- `BalanceSLB` ("BalanceSLB"): Balances outbound frames using source MAC and
  VLAN identifier.
- `BalanceTCP` ("BalanceTCP"): Balances TCP and UDP traffic across member links
  using layer 2 through layer 4 header fields.

Both balancing modes compute a 32-bit hash value with FNV-1a (`hash/fnv`), extract
the low 8 bits (`bucket = hash & 0xff`), and select an enabled member by index:

```
member = enabled[bucket % len(enabled)]
```

Here `enabled` contains the currently enabled member names sorted in alphabetical
order.

### BalanceSLB hash input

`hashSLB` writes the following fields in sequence into the 32-bit FNV-1a hasher:

1. `HashBasis` (4 octets, big-endian uint32).
2. Source MAC address (6 octets).
3. 802.1Q VLAN identifier (2 octets, big-endian uint16).

### BalanceTCP hash input

`hashTCP` hashes layer 2, 3, and 4 fields. If the frame payload decodes as a valid
IPv4 or IPv6 header via `ip.Decode`, layer 3 and 4 fields are included. If IP
decoding fails (including invalid IPv4 header checksums or non-IP EtherTypes),
hashing stops after the layer 2 fields.

The byte sequence fed into FNV-1a is:

1. `HashBasis` (4 octets, big-endian uint32).
2. Source MAC address (6 octets).
3. Destination MAC address (6 octets).
4. EtherType (2 octets, big-endian uint16).
5. Source IP address (4 octets for IPv4, 16 octets for IPv6).
6. Destination IP address (4 octets for IPv4, 16 octets for IPv6).
7. IP protocol or next header (1 octet, uint8).
8. TCP/UDP ports (4 octets: 2 octets source port, 2 octets destination port),
   included only when protocol is 6 (TCP) or 17 (UDP) and the payload contains at
   least 4 octets.

## Link delays and timers

Carrier state changes pass to `LinkChange(now, member, up)`.

- If the configured delay (`UpDelay` when up is true, `DownDelay` when up is false)
  is zero, the transition takes effect immediately.
- If the configured delay is greater than zero, the transition is deferred.
  `LinkChange` arms a timer at `now + delay`. `NextWake` reports the timer, and
  the subsequent call to `Wake` applies the transition once the timer expires.

## LACP protocol machine

When `LACP.Mode` is `Active` or `Passive`, the layer executes the IEEE 802.1AX
exchange using Slow Protocols frames (EtherType 0x8809) sent to `01:80:c2:00:00:02`.

### Actor information

Each member maintains an actor `lacp.Info`:

- `SystemPriority`: LAG administrative priority (default 32768).
- `SystemID`: Switch system MAC address.
- `Key`: Operational aggregation key (default matches LAG port table index).
- `PortPriority`: Member administrative priority (default 32768).
- `PortID`: 1-based index of the member in the LAG's sorted member list.
- `State`: Bitmask containing:
  - `StateActive` (0x01): Set when LACP mode is Active.
  - `StateShortTimeout` (0x02): Set when `Fast` is true.
  - `StateAggregation` (0x04): Always set.
  - `StateSynchronization` (0x08): Set when attached to the aggregator.
  - `StateCollecting` (0x10): Set when enabled.
  - `StateDistributing` (0x20): Set when enabled.
  - `StateDefaulted` (0x40): Set while partner information is defaulted.
  - `StateExpired` (0x80): Set while the receive timer is in the expired state.

### Transmit and receive machine

Transmission rates use two standard periods:
- Fast: 1 second (`FastPeriod`).
- Slow: 30 seconds (`SlowPeriod`).

Members transmit periodically and immediately when their actor state changes.
In Passive mode, transmissions occur only after the partner advertises `StateActive`.

At link up, a member enters status `Current` and arms its receive timer for 3 times
the LAG's rate (3 seconds fast, 90 seconds slow). When `Receive` accepts an LACPDU,
it records the partner info, resets status to `Current`, and rearms the receive
timer to 3 times the partner's advertised timeout.

If the receive timer expires, status transitions to `Expired` and the timer rearms
for one period. If it expires a second time without receiving an LACPDU, status
transitions to `Defaulted`, restoring zeroed partner parameters.

### Aggregator attachment and selection

To determine attached members:
1. The lead member is chosen from members whose status is not `Defaulted`, selecting
   the partner with the lowest system priority and system ID.
2. A member attaches when its status is not `Defaulted` and its partner system ID
   and operational key match the lead member's partner.
3. An attached member is enabled when its partner advertises `StateSynchronization`
   and its carrier delay has expired.
4. If `MinLinks` is configured and the count of enabled members is below that
   minimum, all members in the LAG are disabled.

### Fallback

When `Fallback` is enabled and every member port in the LAG has defaulted, the layer
enables active-backup forwarding over whichever members have carrier up. This allows
traffic to pass to non-LACP endpoints before aggregation negotiation completes.

## Sources

The state machine, bond hashing, and configuration fields replicate the behavior
of Open vSwitch 3.3:

- `ofproto/bond.c`: Bond modes, hash basis, delays, and bucket distribution.
- `lib/lacp.c`: Actor and partner state transitions, attached selection, and transmission timers.
- `vswitch.xml`: Port and Interface database columns for LAG configuration.
- IEEE 802.3ad / IEEE 802.1AX MIB objects: `dot3adAggActorSystemPriority`
  (`spec/mib/ieee/IEEE8023-LAG-MIB:264`), `dot3adAggPortAttachedAggID`
  (`spec/mib/ieee/IEEE8023-LAG-MIB:1386`).
