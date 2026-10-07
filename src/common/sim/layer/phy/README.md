# Physical layer

Package `phy` models the physical layer of the virtual switch: per-port
Ethernet speed and duplex negotiation across cables, and Power over Ethernet
budget allocation across power-sourcing equipment ports.

The layer runs deterministically without background goroutines or wall clocks.
Speed resolution and PoE allocation are pure functions over port
configurations and cable constraints.

## Example

The example configures two auto-negotiating Ethernet endpoints across a
gigabit cable and allocates power for a PSE port from a group budget.

```go
package main

import (
	"fmt"

	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
)

func main() {
	ethA := phy.Ethernet{
		SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
		AutoNegotiationSupported: phy.CapabilitySupported,
		Setting:                  &phy.Setting{AutoNegotiation: true},
	}
	ethB := phy.Ethernet{
		SupportedSpeedsBPS:       []uint64{100_000_000, 1_000_000_000},
		AutoNegotiationSupported: phy.CapabilitySupported,
		Setting:                  &phy.Setting{AutoNegotiation: true},
	}

	link := phy.Negotiate(ethA, ethB, 1_000_000_000)
	fmt.Printf("Link: %s at %d b/s, source %s\n", link.State, link.SpeedBPS, link.Source)

	cfg := phy.Config{
		PoE: &phy.PoE{
			Groups: map[string]phy.Group{
				"1": {PowerNanowatts: 30_000_000_000},
			},
			Ports: map[string]phy.PsePort{
				"1/1/1": {
					Group:    "1",
					Enabled:  true,
					Priority: phy.PriorityCritical,
					MaxClass: 4,
					PD:       phy.PDAttached,
					PDClass:  phy.Class(3),
				},
			},
		},
	}
	alloc := cfg.Allocate()
	p := alloc.Ports["1/1/1"]
	fmt.Printf("Port 1/1/1: %s, power: %d nW\n", p.State, p.MaxNanowatts)
}
```

## Physical negotiation

`Negotiate(a, b, top)` resolves the operational link state, speed, duplex,
resolution source, and failure reason between two endpoints across a cable with
top speed `top` (0 is unlimited).

| End A mode | End B mode | Condition | Outcome | Speed and duplex | Source / Reason |
| --- | --- | --- | --- | --- | --- |
| Unreported | Any | `Setting` nil or forced speed 0 | `LinkUnknown` | 0, `DuplexA`, `DuplexB` | `capability-unknown` |
| Auto | Auto | Known common speeds exist at or below `top` | `LinkResolved` | Highest common speed, Full/Full | `SourceNegotiated` |
| Auto | Auto | No common speeds at or below `top` | `LinkFailed` | 0, unset | `speed-mismatch` |
| Auto | Auto | Either end lacks reported supported speeds | `LinkUnknown` | 0, unset | `capability-unknown` |
| Forced | Forced | Identical speeds at or below `top` | `LinkResolved` | Configured speed, configured duplexes | `SourceSetting`, `duplex-mismatch` if stated duplexes differ |
| Forced | Forced | Speeds differ or exceed `top` | `LinkFailed` | 0, unset | `speed-mismatch` |
| Forced (<= 100M) | Auto | Forced speed supported by auto end and at or below `top` | `LinkResolved` | Forced speed, configured duplex on forced end, Half on auto end | `SourceNegotiated`, `duplex-mismatch` if forced Full |
| Forced (<= 100M) | Auto | Forced speed unsupported by auto end or exceeds `top` | `LinkFailed` | 0, unset | `speed-mismatch` |
| Forced (<= 100M) | Auto | Auto end lacks reported supported speeds | `LinkUnknown` | 0, unset | `capability-unknown` |
| Forced (> 100M) | Auto | Forced speed above 100 Mb/s | `LinkUnsupported` | 0, unset | `forced-against-auto-unmodeled` |

When negotiation yields `LinkUnknown`, the observed rule resolves the link:

- If both ends report matching non-zero observed speeds at or below `top`, the
  link resolves to `LinkResolved` with `SourceObserved`. If stated observed
  duplexes differ, `ReasonDuplexMismatch` is set.
- If both ends report matching observed speeds that exceed `top`, negotiation
  fails with `LinkFailed` and `ReasonSpeedMismatch`.

## PoE budget allocation

`Config.Allocate()` distributes power budgets from PSE groups across their
member ports. Evaluation orders ports by priority (`Critical` first, then
`High`, then `Low`), breaking ties by port name. Each group maintains two
remainders: a minimum remainder (`remMin`) and a maximum remainder (`remMax`),
both initialized to the group budget.

| Port enabled | Device state | Condition | Port state | Power interval | Remainder updates |
| --- | --- | --- | --- | --- | --- |
| false | Absent | Any | `PowerNoDevice` | 0..0 nW | None |
| false | Attached | Any | `PowerDenied` | 0..0 nW | None, reason `disabled` |
| false | Unknown | Any | `PowerDenied` | 0..0 nW | None, reason `disabled` |
| true | Absent | Any | `PowerNoDevice` | 0..0 nW | None |
| true | Attached | Class > port `MaxClass` | `PowerDenied` | 0..0 nW | None, reason `class-unsupported` |
| true | Attached | Class power > port `Limit` | `PowerDenied` | 0..0 nW | None, reason `limit` |
| true | Attached | Class power <= `remMin` | `PowerDelivered` | P..P nW | `remMin -= P`, `remMax -= P` |
| true | Attached | Class power > `remMax` | `PowerDenied` | 0..0 nW | None, reason `budget` |
| true | Attached | `remMin` < class power <= `remMax` | `PowerUnknown` | 0..P nW | `remMin = subSat(remMin, P)` |
| true | Unknown class or unknown PD | Fitting power D exists | `PowerUnknown` | 0..min(D, `remMax`) nW | `remMin = subSat(remMin, D)` |

`D` is the largest class power fitting the port limit up to its maximum class.
`subSat` saturates subtraction at zero. `GroupAllocation` exposes both
`RemainderMinNanowatts` and `RemainderMaxNanowatts`.

## Sources

- UNH: UNH-IOL Clause 28 Auto-Negotiation Management System Test Suite,
  9 September 1999, Group 3 (parallel detection).
- RFC 3621: Power Ethernet MIB, objects `pethMainPseTable` (group power
  budgets) and `pethPsePortPowerPriority` (port priority vocabulary).

## Limits

- Parallel detection covers 10 and 100 Mb/s and resolves the detecting end at
  half duplex (UNH, Group 3). A faster forced end against an auto-negotiating
  one stays unsupported.
- The class power table (`classPowerLevels` in `poe.go`) is unverified against
  IEEE Std 802.3.
