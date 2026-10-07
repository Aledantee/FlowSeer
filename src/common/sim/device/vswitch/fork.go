package vswitch

import (
	"maps"
	"slices"

	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

// Fork returns an independent executable copy of the switch. Construction
// configuration, ports, phy resolution, node identity, and trust metadata are
// shared; capability layers, policer buckets, seeds, and forwarding issues are
// deep-copied. Fork leaves the packet filter unset on the fork. Bridge selector,
// resolver, and gate bindings are rebound to the fork's own cloned layers.
// The call in progress is unset on the fork.
func (s *Switch) Fork() *Switch {
	cp := &Switch{
		cfg:                s.cfg,
		ports:              s.ports,
		speeds:             s.speeds,
		power:              s.power,
		trafficSwitchports: s.trafficSwitchports,
		nodeID:             s.nodeID,
		metadata:           s.metadata,
		missingSTP:         s.missingSTP,
		operErr:            s.operErr,
	}
	if s.seeds != nil {
		cp.seeds = slices.Clone(s.seeds)
	}
	if s.copies != nil {
		cp.copies = slices.Clone(s.copies)
	}
	if s.emissions != nil {
		cp.emissions = slices.Clone(s.emissions)
	}
	if s.portP2P != nil {
		cp.portP2P = maps.Clone(s.portP2P)
	}
	if s.portSpeed != nil {
		cp.portSpeed = maps.Clone(s.portSpeed)
	}
	if s.protocolIssues != nil {
		cp.protocolIssues = maps.Clone(s.protocolIssues)
	}
	if s.neighborFailures != nil {
		cp.neighborFailures = slices.Clone(s.neighborFailures)
	}
	if s.traffic != nil {
		cp.traffic = s.traffic.Clone()
	}
	if s.stp != nil {
		cp.stp = s.stp.Clone()
	}
	if s.loopprotect != nil {
		cp.loopprotect = s.loopprotect.Clone()
	}
	if s.lag != nil {
		cp.lag = s.lag.Clone()
	}
	if s.mcast != nil {
		cp.mcast = s.mcast.Clone()
	}
	if s.routing != nil {
		cp.routing = s.routing.Clone()
	}
	if s.bridge != nil {
		cp.bridge = s.bridge.Clone()
		if cp.stp != nil {
			cp.bridge.SetGate(cp.stp, protocolScope(cp.nodeID, stp.LayerName))
		}
		if cp.loopprotect != nil {
			cp.bridge.SetGate(cp.loopprotect, protocolScope(cp.nodeID, loopprotect.LayerName))
		}
		if cp.lag != nil {
			cp.bridge.SetSelector(lagSelector{sw: cp}, protocolScope(cp.nodeID, lag.LayerName))
		}
		if cp.mcast != nil {
			cp.bridge.SetGroupResolver(mcastResolver{sw: cp}, protocolScope(cp.nodeID, mcast.LayerName), mcast.LayerName, mcast.RuleGroupMembers)
		}
	}
	cp.retention = allKeptRetention()
	return cp
}
