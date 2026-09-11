package mcast

import (
	"slices"
	"strconv"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

const layer trace.Layer = "mcast"

// Diff returns deterministic per-VLAN changes between a and b.
// Defaulted intervals and router-port order do not create changes.
func Diff(a, b Config) []trace.Change {
	var changes []trace.Change

	for _, vid := range sortedVLANIDs(a.VLANs) {
		aCfg := a.VLANs[vid]
		bCfg, ok := b.VLANs[vid]
		if !ok {
			changes = append(changes, vlanChange(vid, "", aCfg, nil))

			continue
		}

		if from, to := a.Floods(vid), b.Floods(vid); from != to {
			changes = append(changes, vlanChange(vid, "flood_unregistered", from, to))
		}
		if aCfg.FastLeave != bCfg.FastLeave {
			changes = append(changes, vlanChange(vid, "fast_leave", aCfg.FastLeave, bCfg.FastLeave))
		}

		aRouters := sortedRouterPorts(aCfg.RouterPorts)
		bRouters := sortedRouterPorts(bCfg.RouterPorts)
		if !slices.Equal(aRouters, bRouters) {
			changes = append(changes, vlanChange(vid, "router_ports", aRouters, bRouters))
		}
		if from, to := aCfg.membershipInterval(), bCfg.membershipInterval(); from != to {
			changes = append(changes, vlanChange(vid, "membership_interval", from, to))
		}
		if from, to := aCfg.routerPortInterval(), bCfg.routerPortInterval(); from != to {
			changes = append(changes, vlanChange(vid, "router_port_interval", from, to))
		}
	}

	for _, vid := range sortedVLANIDs(b.VLANs) {
		if _, ok := a.VLANs[vid]; !ok {
			changes = append(changes, vlanChange(vid, "", nil, b.VLANs[vid]))
		}
	}

	return changes
}

func vlanChange(vid vlan.ID, field string, from, to any) trace.Change {
	return trace.Change{
		Layer:   layer,
		Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(vid))},
		Field:   field,
		From:    from,
		To:      to,
	}
}

func sortedRouterPorts(ports []string) []string {
	ports = slices.Clone(ports)
	slices.Sort(ports)

	return ports
}
