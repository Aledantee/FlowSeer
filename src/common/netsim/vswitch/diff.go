package vswitch

import (
	"slices"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Diff computes the difference between two switch configurations, concatenating
// port table differences, capability presence changes, physical layer differences,
// and bridge relay differences.
func Diff(a, b Config) []trace.Change {
	var changes []trace.Change

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

	var aBridge, bBridge bridge.Config
	if a.Bridge != nil {
		aBridge = *a.Bridge
	}
	if b.Bridge != nil {
		bBridge = *b.Bridge
	}
	changes = append(changes, bridge.Diff(aBridge, bBridge)...)

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
