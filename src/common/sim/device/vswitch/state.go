package vswitch

import (
	"maps"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

// State samples independent layer-free state at now without advancing timers.
// Calls require serial access.
func (s *Switch) State(now time.Time) device.State {
	out := device.State{Ports: s.Ports().Ports(), RelayCounters: device.RelayCounters(s.RelayCounters())}
	for _, row := range s.Entries() {
		out.Entries = append(out.Entries, stateEntry(row))
	}
	for _, row := range s.Neighbors() {
		out.Neighbors = append(out.Neighbors, stateNeighbor(row))
	}
	if s.cfg.Mcast != nil {
		out.Groups = make(map[vlan.ID][]device.GroupEntry)
		out.RouterPorts = make(map[vlan.ID][]device.RouterPort)
		for _, vid := range slices.Sorted(maps.Keys(s.cfg.Mcast.VLANs)) {
			out.Groups[vid] = nil
			out.RouterPorts[vid] = nil
			for _, row := range s.Groups(vid, now) {
				out.Groups[vid] = append(out.Groups[vid], stateGroup(row))
			}
			for _, row := range s.RouterPorts(vid, now) {
				out.RouterPorts[vid] = append(out.RouterPorts[vid], stateRouterPort(row))
			}
		}
	}
	power := s.Power()
	out.Power = stateAllocation(power.Allocation)
	out.Power.Metadata = power.Metadata
	if roles := s.Roles(); roles != nil {
		out.Roles = make(map[string]device.PortInfo)
		out.TreeRoles = make(map[device.TreeRef]map[string]device.PortInfo)
		for _, name := range slices.Sorted(maps.Keys(roles)) {
			row := statePortInfo(roles[name])
			out.Roles[name] = row
			if out.TreeRoles[row.Tree] == nil {
				out.TreeRoles[row.Tree] = make(map[string]device.PortInfo)
			}
			out.TreeRoles[row.Tree][name] = row
		}
		trees := s.TreeRoles()
		for _, vid := range slices.Sorted(maps.Keys(trees)) {
			for _, name := range slices.Sorted(maps.Keys(trees[vid])) {
				row := statePortInfo(trees[vid][name])
				if row.Tree.Kind == "" {
					continue
				}
				if out.TreeRoles[row.Tree] == nil {
					out.TreeRoles[row.Tree] = make(map[string]device.PortInfo)
				}
				out.TreeRoles[row.Tree][name] = row
			}
		}
	}
	return out
}

func stateEntry(row bridge.Entry) device.Entry {
	return device.Entry{FID: row.FID, MAC: row.MAC, Port: row.Port, Origin: device.Origin(row.Origin), Lifetime: device.Lifetime(row.Lifetime), LearnedAt: row.LearnedAt}
}

func stateGroup(row mcast.Entry) device.GroupEntry {
	out := device.GroupEntry{Group: row.Group, Port: row.Port, Mode: device.FilterMode(row.Mode), GroupExpires: row.GroupExpires}
	for _, source := range row.Sources {
		out.Sources = append(out.Sources, device.SourceEntry(source))
	}
	return out
}

func stateRouterPort(row mcast.RouterPort) device.RouterPort {
	return device.RouterPort{Port: row.Port, Expires: row.Expires, Origin: device.Origin(row.Origin), Lifetime: device.Lifetime(row.Lifetime)}
}

func stateNeighbor(row routing.NeighborEntry) device.NeighborEntry {
	return device.NeighborEntry{VRF: row.VRF, Interface: row.Interface, Addr: row.Addr, MAC: row.MAC, State: device.NeighborState(row.State), HoldDepth: row.HoldDepth}
}

func statePortInfo(row stp.PortInfo) device.PortInfo {
	return device.PortInfo{
		Tree: device.TreeRef{Kind: device.TreeKind(row.Tree.Kind), ID: row.Tree.ID},
		Role: row.Role, State: device.PortState(row.State), BlockReason: device.BlockReason(row.BlockReason),
		Priority: row.Priority, PathCost: row.PathCost, DesignatedRoot: row.DesignatedRoot, Designated: row.Designated,
		DesignatedPort: row.DesignatedPort, DesignatedCost: row.DesignatedCost, PointToPoint: row.PointToPoint, Edge: row.Edge,
		ForwardTransitions: row.ForwardTransitions, TxBPDUs: row.TxBPDUs, RxBPDUs: row.RxBPDUs, BadBPDUs: row.BadBPDUs, SendRSTP: row.SendRSTP,
	}
}

func stateAllocation(row phy.Allocation) device.Allocation {
	out := device.Allocation{}
	if row.Ports != nil {
		out.Ports = make(map[string]device.PortAllocation)
	}
	if row.Groups != nil {
		out.Groups = make(map[string]device.GroupAllocation)
	}
	for name, p := range row.Ports {
		out.Ports[name] = device.PortAllocation{State: device.PowerState(p.State), MinNanowatts: p.MinNanowatts, MaxNanowatts: p.MaxNanowatts, Denial: p.Denial}
	}
	for name, g := range row.Groups {
		out.Groups[name] = device.GroupAllocation(g)
	}
	return out
}
