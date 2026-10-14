package vswitch

import (
	"fmt"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// Config returns an independent deep copy of the switch configuration.
func (s *Switch) Config() Config {
	return s.cfg.Clone()
}

// Retention reports what capability runtime state Derive retained from the
// current switch and what differed. On a forked switch, every layer is reported kept.
func (s *Switch) Retention() Retention {
	return s.retention
}

// Neighbors returns the current neighbor table entries across all VRFs.
func (s *Switch) Neighbors() []routing.NeighborEntry {
	if s.routing == nil {
		return nil
	}
	return s.routing.Neighbors()
}

// Ports returns a copy of the switch port table.
func (s *Switch) Ports() port.Table {
	return s.ports.Clone()
}

// Entries returns the active forwarding database entries in sorted order,
// or nil if the switch does not have a bridge relay.
func (s *Switch) Entries() []bridge.Entry {
	if s.bridge == nil {
		return nil
	}

	return s.bridge.Entries()
}

// Groups returns the multicast membership entries for vid as of now without aging stored state.
func (s *Switch) Groups(vid vlan.ID, now time.Time) []mcast.Entry {
	if s.mcast == nil {
		return nil
	}

	return s.mcast.Groups(vid, now)
}

// RouterPorts returns static and unexpired multicast router ports for vid as of now
// without aging stored state.
func (s *Switch) RouterPorts(vid vlan.ID, now time.Time) []mcast.RouterPort {
	if s.mcast == nil {
		return nil
	}

	return s.mcast.RouterPorts(vid, now)
}

// RelayCounters returns the forwarding database lifecycle counters from the switch's
// bridge relay, or a zero-value Counters struct if the switch has no bridge relay.
func (s *Switch) RelayCounters() bridge.Counters {
	if s.bridge == nil {
		return bridge.Counters{}
	}

	return s.bridge.Counters()
}

// Power returns the Power over Ethernet budget distribution and port allocations with analysis metadata.
func (s *Switch) Power() PowerResult {
	cp := phy.Allocation{}
	if s.power.Ports != nil {
		cp.Ports = make(map[string]phy.PortAllocation, len(s.power.Ports))
		for k, v := range s.power.Ports {
			cp.Ports[k] = v
		}
	}
	if s.power.Groups != nil {
		cp.Groups = make(map[string]phy.GroupAllocation, len(s.power.Groups))
		for k, v := range s.power.Groups {
			cp.Groups[k] = v
		}
	}

	var issues []analysis.Issue
	portNames := make([]string, 0, len(cp.Ports))
	for name := range cp.Ports {
		portNames = append(portNames, name)
	}
	slices.Sort(portNames)

	for _, portName := range portNames {
		pa := cp.Ports[portName]
		if pa.State == phy.PowerUnknown {
			scope := analysis.FieldScope(analysis.NodeScope(s.nodeID), "poe", portName)
			issues = append(issues, analysis.Issue{
				Code:    IssuePoEDemandUnknown,
				Status:  analysis.Incomplete,
				Scope:   scope,
				Message: fmt.Sprintf("port %q has unknown poe power demand", portName),
			})
		}
	}

	scope := analysis.FieldScope(analysis.NodeScope(s.nodeID), "poe")
	meta := analysis.NewMetadata(scope, issues, analysis.EvidenceCatalog{}, nil)

	return PowerResult{
		Allocation: cp,
		Metadata:   meta,
	}
}

// Roles returns the runtime spanning tree status for each configured port,
// or nil if the spanning tree layer is absent.
func (s *Switch) Roles() map[string]stp.PortInfo {
	if s.stp == nil || s.cfg.STP == nil {
		return nil
	}
	roles := make(map[string]stp.PortInfo, len(s.cfg.STP.Ports))
	for name := range s.cfg.STP.Ports {
		roles[name] = s.stp.PortInfo(name)
	}

	return roles
}

// TreeRoles returns the runtime spanning tree status for each configured port
// in each VLAN the configuration names, or nil if the spanning tree layer is
// absent. Those VLANs come from the VLAN table, the PVST trees, the MST
// instances, and the multicast snooping VLANs, with VLAN 1 added for PVST and
// MST to sample the CIST. Without any of them the result holds VLAN 1 alone.
func (s *Switch) TreeRoles() map[vlan.ID]map[string]stp.PortInfo {
	if s.stp == nil || s.cfg.STP == nil {
		return nil
	}
	vids := s.configuredVLANs()
	if len(vids) == 0 {
		return nil
	}
	roles := make(map[vlan.ID]map[string]stp.PortInfo, len(vids))
	for _, vid := range vids {
		portRoles := make(map[string]stp.PortInfo, len(s.cfg.STP.Ports))
		for name := range s.cfg.STP.Ports {
			portRoles[name] = s.stp.VLANPortInfo(vid, name)
		}
		roles[vid] = portRoles
	}

	return roles
}

func (s *Switch) configuredVLANs() []vlan.ID {
	seen := make(map[vlan.ID]struct{})
	if s.cfg.Bridge != nil && s.cfg.Bridge.VLAN != nil {
		for vid := range s.cfg.Bridge.VLAN.Table {
			seen[vid] = struct{}{}
		}
	}
	if s.cfg.STP != nil {
		if s.cfg.STP.PVST != nil {
			for vid := range s.cfg.STP.PVST.Trees {
				seen[vid] = struct{}{}
			}
			seen[1] = struct{}{}
		}
		if s.cfg.STP.MST != nil {
			for _, inst := range s.cfg.STP.MST.Instances {
				for _, vid := range inst.VLANs {
					seen[vid] = struct{}{}
				}
			}
			// Sample the CIST via VLAN 1 (which maps to MSTID 0 by default).
			// If VLAN 1 is assigned to an MSTI, this sample names that MSTI.
			// The VLAN-keyed return shape cannot express MSTID 0 directly.
			seen[1] = struct{}{}
		}
	}
	if s.cfg.Mcast != nil {
		for vid := range s.cfg.Mcast.VLANs {
			seen[vid] = struct{}{}
		}
	}
	if len(seen) == 0 && s.cfg.STP != nil {
		seen[1] = struct{}{}
	}
	vids := make([]vlan.ID, 0, len(seen))
	for vid := range seen {
		vids = append(vids, vid)
	}
	slices.Sort(vids)

	return vids
}

// Root returns the elected root bridge identifier, the path cost to reach it,
// and the interface name of the root port, or zero values if the spanning tree
// layer is absent.
func (s *Switch) Root() (bpdu.BridgeID, uint32, string) {
	if s.stp == nil {
		return bpdu.BridgeID{}, 0, ""
	}

	return s.stp.Root()
}

// TopologyChanges returns the total count of detected topology changes and the
// timestamp of the most recent change, or zero values if the spanning tree layer is absent.
func (s *Switch) TopologyChanges() (uint64, time.Time) {
	if s.stp == nil {
		return 0, time.Time{}
	}

	return s.stp.TopologyChanges()
}

// BridgeID returns the spanning tree bridge identifier with the priority in
// effect, or a zero value if the spanning tree layer is absent.
func (s *Switch) BridgeID() bpdu.BridgeID {
	if s.stp == nil {
		return bpdu.BridgeID{}
	}

	return s.stp.BridgeID()
}

// Times returns the spanning tree max age, hello time, and forward delay in
// force, or zero values if the spanning tree layer is absent.
func (s *Switch) Times() (maxAge, hello, forwardDelay time.Duration) {
	if s.stp == nil {
		return 0, 0, 0
	}

	return s.stp.Times()
}

// LagInfo returns the runtime aggregation status of the named LAG,
// or a zero-value Info if the aggregation layer is absent.
func (s *Switch) LagInfo(lagName string) lag.Info {
	if s.lag == nil {
		return lag.Info{}
	}

	return s.lag.Info(lagName)
}

// MemberInfo returns the runtime aggregation status of the named member port,
// or a zero-value MemberInfo if the aggregation layer is absent.
func (s *Switch) MemberInfo(member string) lag.MemberInfo {
	if s.lag == nil {
		return lag.MemberInfo{}
	}

	return s.lag.PortInfo(member)
}
