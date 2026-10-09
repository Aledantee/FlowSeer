package vswitch

import (
	"maps"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/device"
)

// Fork returns an independent executable copy of the switch. Construction
// configuration, ports, phy resolution, node identity, and trust metadata are
// shared. Capability layers, including packet filters, policer buckets, seeds,
// and forwarding issues are deep-copied. Bridge selector, resolver, and gate
// bindings are rebound to the fork's own cloned layers.
// The call in progress is unset on the fork.
func (s *Switch) Fork() device.Device {
	cp := &Switch{
		cfg:                s.cfg,
		ports:              s.ports,
		speeds:             s.speeds,
		power:              s.power,
		trafficSwitchports: s.trafficSwitchports,
		nodeID:             s.nodeID,
		metadata:           s.metadata,
		missingSTP:         s.missingSTP,
	}
	if s.seeds != nil {
		cp.seeds = slices.Clone(s.seeds)
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
	entries := s.layers(time.Time{})
	for _, entry := range entries {
		if entry.clone != nil {
			entry.clone(s, cp)
		}
	}
	if cp.bridge != nil {
		for _, entry := range entries {
			if entry.bind != nil {
				entry.bind(cp)
			}
		}
	}
	cp.retention = allKeptRetention()
	return cp
}
