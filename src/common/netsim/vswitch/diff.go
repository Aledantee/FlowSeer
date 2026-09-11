package vswitch

import (
	"slices"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

// Diff computes the difference between two switch configurations, concatenating
// device-level MAC differences, port table differences, capability presence changes,
// physical layer differences, bridge relay differences, link aggregation differences,
// spanning tree differences, routing differences, and traffic differences.
func Diff(a, b Config) []trace.Change {
	var changes []trace.Change

	if a.MAC != b.MAC {
		changes = append(changes, trace.Change{
			Layer:   port.LayerPort,
			Subject: trace.Subject{Kind: "device", Key: ""},
			Field:   "mac",
			From:    a.MAC,
			To:      b.MAC,
		})
	}

	changes = append(changes, port.Diff(a.Ports, b.Ports)...)
	changes = append(changes, diffCapabilities(a, b)...)

	var aPhy, bPhy phy.Config
	if a.Phy != nil {
		aPhy = *a.Phy
	}
	if b.Phy != nil {
		bPhy = *b.Phy
	}
	changes = append(changes, phy.Diff(aPhy, bPhy)...)

	if a.Bridge != nil || b.Bridge != nil {
		var aBridge, bBridge bridge.Config
		if a.Bridge != nil {
			aBridge = *a.Bridge
		}
		if b.Bridge != nil {
			bBridge = *b.Bridge
		}
		changes = append(changes, bridge.Diff(aBridge, bBridge)...)
	}

	if a.LAG != nil || b.LAG != nil {
		var aLAG, bLAG lag.Config
		if a.LAG != nil {
			aLAG = *a.LAG
		}
		if b.LAG != nil {
			bLAG = *b.LAG
		}
		changes = append(changes, lag.Diff(aLAG, bLAG)...)
	}

	if a.STP != nil || b.STP != nil {
		var aSTP, bSTP stp.Config
		if a.STP != nil {
			aSTP = *a.STP
		}
		if b.STP != nil {
			bSTP = *b.STP
		}
		changes = append(changes, stp.Diff(aSTP, bSTP)...)
	}

	if a.Routing != nil || b.Routing != nil {
		var aRouting, bRouting routing.Config
		if a.Routing != nil {
			aRouting = *a.Routing
		}
		if b.Routing != nil {
			bRouting = *b.Routing
		}
		changes = append(changes, routing.Diff(aRouting, bRouting)...)
	}

	if a.Traffic != nil || b.Traffic != nil {
		var aTraffic, bTraffic traffic.Config
		if a.Traffic != nil {
			aTraffic = *a.Traffic
		}
		if b.Traffic != nil {
			bTraffic = *b.Traffic
		}
		changes = append(changes, traffic.Diff(aTraffic, bTraffic)...)
	}

	return changes
}

func diffCapabilities(a, b Config) []trace.Change {
	aCaps := a.Capabilities()
	bCaps := b.Capabilities()

	seen := make(map[port.Layer]struct{}, len(aCaps)+len(bCaps))
	for _, l := range aCaps {
		seen[l] = struct{}{}
	}
	for _, l := range bCaps {
		seen[l] = struct{}{}
	}

	layers := make([]port.Layer, 0, len(seen))
	for l := range seen {
		layers = append(layers, l)
	}
	slices.Sort(layers)

	var changes []trace.Change
	for _, l := range layers {
		inA := slices.Contains(aCaps, l)
		inB := slices.Contains(bCaps, l)
		if inA && !inB {
			changes = append(changes, trace.Change{
				Layer: l,
				Subject: trace.Subject{
					Kind: "capability",
					Key:  string(l),
				},
				Field: "",
				From:  l,
				To:    nil,
			})
		} else if !inA && inB {
			changes = append(changes, trace.Change{
				Layer: l,
				Subject: trace.Subject{
					Kind: "capability",
					Key:  string(l),
				},
				Field: "",
				From:  nil,
				To:    l,
			})
		}
	}

	return changes
}
