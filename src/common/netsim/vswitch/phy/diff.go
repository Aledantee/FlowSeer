package phy

import (
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Diff computes the difference between two physical-layer configurations,
// covering speed_bps and auto_negotiation_enabled per Ethernet port, enabled,
// power_limit_milliwatts, and priority per PSE port, and power_milliwatts per
// PSE group. A port or group present on only one side is a change with an
// empty Field and the absent side nil, matching [port.Diff]'s
// added-and-removed convention. An absent setting compares as its zero value.
// Changes are ordered by subject key so equal inputs diff identically.
func Diff(a, b Config) []trace.Change {
	changes := diffEthernet(a.Ethernet, b.Ethernet)

	return append(changes, diffPoE(a.PoE, b.PoE)...)
}

func diffEthernet(a, b map[string]Ethernet) []trace.Change {
	var changes []trace.Change

	for _, name := range sortedKeys(a) {
		ae := a[name]
		be, exists := b[name]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   port.LayerEthernet,
				Subject: trace.Subject{Kind: "port", Key: name},
				From:    ae,
			})

			continue
		}

		if from, to := settingSpeed(ae), settingSpeed(be); from != to {
			changes = append(changes, trace.Change{
				Layer:   port.LayerEthernet,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "speed_bps",
				From:    from,
				To:      to,
			})
		}
		if from, to := settingAutoNegotiation(ae), settingAutoNegotiation(be); from != to {
			changes = append(changes, trace.Change{
				Layer:   port.LayerEthernet,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "auto_negotiation_enabled",
				From:    from,
				To:      to,
			})
		}
	}

	for _, name := range sortedKeys(b) {
		if _, exists := a[name]; !exists {
			changes = append(changes, trace.Change{
				Layer:   port.LayerEthernet,
				Subject: trace.Subject{Kind: "port", Key: name},
				To:      b[name],
			})
		}
	}

	return changes
}

func diffPoE(a, b *PoE) []trace.Change {
	var aGroups, bGroups map[string]Group
	var aPorts, bPorts map[string]PsePort
	if a != nil {
		aGroups, aPorts = a.Groups, a.Ports
	}
	if b != nil {
		bGroups, bPorts = b.Groups, b.Ports
	}

	var changes []trace.Change

	for _, name := range sortedKeys(aGroups) {
		ag := aGroups[name]
		bg, exists := bGroups[name]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "pse_group", Key: name},
				From:    ag,
			})

			continue
		}
		if ag.PowerMilliwatts != bg.PowerMilliwatts {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "pse_group", Key: name},
				Field:   "power_milliwatts",
				From:    ag.PowerMilliwatts,
				To:      bg.PowerMilliwatts,
			})
		}
	}
	for _, name := range sortedKeys(bGroups) {
		if _, exists := aGroups[name]; !exists {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "pse_group", Key: name},
				To:      bGroups[name],
			})
		}
	}

	for _, name := range sortedKeys(aPorts) {
		ap := aPorts[name]
		bp, exists := bPorts[name]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "port", Key: name},
				From:    ap,
			})

			continue
		}

		if ap.Enabled != bp.Enabled {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "enabled",
				From:    ap.Enabled,
				To:      bp.Enabled,
			})
		}
		if from, to := limitValue(ap.Limit), limitValue(bp.Limit); from != to {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "power_limit_milliwatts",
				From:    from,
				To:      to,
			})
		}
		if ap.Priority != bp.Priority {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "priority",
				From:    ap.Priority,
				To:      bp.Priority,
			})
		}
	}
	for _, name := range sortedKeys(bPorts) {
		if _, exists := aPorts[name]; !exists {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "port", Key: name},
				To:      bPorts[name],
			})
		}
	}

	return changes
}

func settingSpeed(e Ethernet) uint64 {
	if e.Setting == nil {
		return 0
	}

	return e.Setting.SpeedBPS
}

func settingAutoNegotiation(e Ethernet) bool {
	return e.Setting != nil && e.Setting.AutoNegotiation
}

// limitValue collapses an absent limit to nil so a missing limit and a
// present one never compare equal.
func limitValue(limit *uint32) any {
	if limit == nil {
		return nil
	}

	return *limit
}
