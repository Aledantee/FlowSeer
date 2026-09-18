package vswitch

import (
	"net/netip"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/mld"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

// Derive builds a new [Switch] from the target construction specification, seeding it with every
// dynamic forwarding database entry from the current switch that the new configuration
// still admits. Reseeded dynamic entries count as learned and are bounded by the new
// configuration's MaxEntries, evicting from the oldest. Static entries and construction
// trust come only from target.
// A derived standalone switch keeps spanning tree roles when their layer
// configuration is unchanged, and keeps eligible multicast memberships and
// learned router ports whenever multicast snooping remains enabled.
// It keeps LAG runtime state only while every member's administrative and operational
// state also matches the target.
// It returns an error if the target specification fails validation.
func Derive(cur *Switch, target ConstructionSpec) (*Switch, error) {
	next, err := NewWithSpec(target)
	if err != nil {
		return nil, err
	}

	// Traffic retention
	var curTrafficKey, nextTrafficKey string
	if cur != nil && cur.traffic != nil {
		curTrafficKey = traffic.RetentionKey(*cur.traffic)
	}
	if next.traffic != nil {
		nextTrafficKey = traffic.RetentionKey(*next.traffic)
	}
	if curTrafficKey == nextTrafficKey {
		next.retention.Traffic = LayerRetention{Kept: true}
	} else {
		next.retention.Traffic = LayerRetention{Kept: false, Difference: diffDependency(curTrafficKey, nextTrafficKey)}
	}
	if cur != nil && cur.traffic != nil && next.traffic != nil {
		for name, policer := range next.traffic.Policers {
			if current, ok := cur.traffic.Policers[name]; ok && current == policer {
				if b, ok := cur.buckets[name]; ok {
					next.buckets[name] = b.Clone()
				}
			}
		}
	}

	// STP retention: both sides are compared as New filled them, so a bridge address
	// the switch assigned does not read as a change.
	var curSTPKey, nextSTPKey string
	if cur != nil && cur.stp != nil && cur.cfg.STP != nil {
		curSTPKey = stp.RetentionKey(*cur.cfg.STP, cur.ports, resolvedSpeeds(cur))
	}
	if next.cfg.STP != nil {
		nextSTPKey = stp.RetentionKey(*next.cfg.STP, next.ports, resolvedSpeeds(next))
	}
	if curSTPKey == nextSTPKey {
		next.retention.STP = LayerRetention{Kept: true}
		if cur != nil && cur.stp != nil {
			next.stp = cur.stp.Clone()
			if next.bridge != nil {
				next.bridge.SetGate(next.stp, protocolScope(next.nodeID, port.LayerStp))
			}
		}
	} else {
		next.retention.STP = LayerRetention{Kept: false, Difference: diffDependency(curSTPKey, nextSTPKey)}
	}

	// Carry portP2P and portSpeed per port rather than wholesale,
	// only where target's port and resolved speed match.
	if cur != nil {
		for _, p := range next.ports.Ports() {
			curPort, ok := cur.ports.Port(p.Name)
			if !ok {
				continue
			}
			if curPort != p || cur.linkSpeed(curPort) != next.linkSpeed(p) {
				if cur.portP2P != nil {
					if _, heard := cur.portP2P[p.Name]; heard {
						if next.portP2P == nil {
							next.portP2P = make(map[string]PointToPoint)
						}
						next.portP2P[p.Name] = PointToPointUnknown
					}
				}
				continue
			}
			if cur.portP2P != nil {
				if p2p, ok := cur.portP2P[p.Name]; ok {
					if next.portP2P == nil {
						next.portP2P = make(map[string]PointToPoint)
					}
					next.portP2P[p.Name] = p2p
				}
			}
			if cur.portSpeed != nil {
				if sp, ok := cur.portSpeed[p.Name]; ok {
					if next.portSpeed == nil {
						next.portSpeed = make(map[string]uint64)
					}
					next.portSpeed[p.Name] = sp
				}
			}
		}
	}

	// LoopProtect retention
	var curLPKey, nextLPKey string
	if cur != nil && cur.loopprotect != nil && cur.cfg.LoopProtect != nil {
		curLPKey = loopprotect.RetentionKey(*cur.cfg.LoopProtect, cur.ports, cur.cfg.MAC)
	}
	if next.cfg.LoopProtect != nil {
		nextLPKey = loopprotect.RetentionKey(*next.cfg.LoopProtect, next.ports, next.cfg.MAC)
	}
	if curLPKey == nextLPKey {
		next.retention.LoopProtect = LayerRetention{Kept: true}
		if cur != nil && cur.loopprotect != nil {
			next.loopprotect = cur.loopprotect.Clone()
			if next.bridge != nil {
				next.bridge.SetGate(next.loopprotect, protocolScope(next.nodeID, port.LayerLoopProtect))
			}
		}
	} else {
		next.retention.LoopProtect = LayerRetention{Kept: false, Difference: diffDependency(curLPKey, nextLPKey)}
	}

	// LAG retention
	var curLAGKey, nextLAGKey string
	if cur != nil && cur.lag != nil && cur.cfg.LAG != nil {
		curLAGKey = lag.RetentionKey(*cur.cfg.LAG, cur.ports, cur.cfg.MAC)
	}
	if next.cfg.LAG != nil {
		nextLAGKey = lag.RetentionKey(*next.cfg.LAG, next.ports, next.cfg.MAC)
	}
	if curLAGKey == nextLAGKey {
		next.retention.LAG = LayerRetention{Kept: true}
		if cur != nil && cur.lag != nil {
			next.lag = cur.lag.Clone()
			if next.bridge != nil {
				next.bridge.SetSelector(lagSelector{sw: next}, protocolScope(next.nodeID, port.LayerLag))
			}
		}
	} else {
		next.retention.LAG = LayerRetention{Kept: false, Difference: diffDependency(curLAGKey, nextLAGKey)}
	}

	// Retained point-to-point reports change which spanning tree inputs are
	// unknown, so the issues New computed from an empty report set are stale.
	next.recomputeProtocolLinkIssues()

	// Mcast retention: multicast snooping is snoop-driven and unconditionally
	// replays retained dynamic state into the new switch, filtered per-entry
	// by port forwarding and VLAN membership. It reports kept when both switches
	// have multicast enabled.
	if (cur != nil && cur.mcast != nil) != (next.mcast != nil) {
		next.retention.Mcast = LayerRetention{Kept: false, Difference: "config"}
	} else {
		next.retention.Mcast = LayerRetention{Kept: true}
	}
	if cur != nil && cur.mcast != nil && next.mcast != nil {
		retained := cur.mcast.Clone()
		retained.Retain(next.ports, func(vid vlan.ID, name string) bool {
			if _, ok := next.cfg.Mcast.VLANs[vid]; !ok {
				return false
			}
			p, ok := next.ports.Port(name)
			if !ok || p.LagParent != "" || !p.Forwards() {
				return false
			}
			switchport, ok := next.cfg.Bridge.VLAN.Switchports[name]
			if !ok || !slices.Contains(switchport.Tagged, vid) && !slices.Contains(switchport.Untagged, vid) &&
				(switchport.Tunnel == nil || switchport.Tunnel.VID != vid) {
				return false
			}

			return next.stp == nil || next.stp.Forwards(name, vid)
		})
		restoreMulticastState(next, retained)
	}

	// Routing retention
	var curRoutingKey, nextRoutingKey string
	if cur != nil && cur.routing != nil && cur.cfg.Routing != nil {
		curRoutingKey = routing.RetentionKey(*cur.cfg.Routing, cur.ports)
	}
	if next.cfg.Routing != nil {
		nextRoutingKey = routing.RetentionKey(*next.cfg.Routing, next.ports)
	}
	if curRoutingKey == nextRoutingKey {
		next.retention.Routing = LayerRetention{Kept: true}
		if cur != nil && cur.routing != nil {
			next.routing = cur.routing.Clone()
		}
	} else {
		next.retention.Routing = LayerRetention{Kept: false, Difference: diffDependency(curRoutingKey, nextRoutingKey)}
		if cur != nil && cur.routing != nil {
			eff := cur.routing.Clone().FailHeld()
			next.applyRoutingEffects(time.Time{}, eff)
		}
	}

	if cur == nil || cur.bridge == nil || next.bridge == nil {
		return next, nil
	}

	curVLAN := cur.cfg.Bridge != nil && cur.cfg.Bridge.VLAN != nil
	nextVLAN := next.cfg.Bridge != nil && next.cfg.Bridge.VLAN != nil
	targetSeeds := make(map[bridgeSeedKey]struct{}, len(next.seeds))
	for _, seed := range next.seeds {
		if seed.Lifetime != bridge.Static {
			continue
		}
		targetSeeds[bridgeSeedKey{fid: seed.FID, mac: seed.MAC}] = struct{}{}
	}

	var seeds []bridge.Seed
	for _, entry := range cur.Entries() {
		if entry.Lifetime == bridge.Static {
			continue
		}
		if _, configured := targetSeeds[bridgeSeedKey{fid: entry.FID, mac: entry.MAC}]; configured {
			continue
		}

		p, ok := next.cfg.Ports.Port(entry.Port)
		if !ok {
			continue
		}
		if p.Kind == port.Lag && len(next.cfg.Ports.Members(entry.Port)) == 0 {
			continue
		}

		if nextVLAN {
			sw, ok := next.cfg.Bridge.VLAN.Switchports[entry.Port]
			if !ok {
				continue
			}
			admitted := slices.Contains(sw.Tagged, entry.FID) || slices.Contains(sw.Untagged, entry.FID) ||
				(sw.PVID != nil && *sw.PVID == entry.FID) ||
				(sw.Tunnel != nil && sw.Tunnel.VID == entry.FID)
			if !admitted {
				continue
			}
			seeds = append(seeds, bridge.Seed(entry))
		} else if !curVLAN {
			seeds = append(seeds, bridge.Seed{
				FID:       0,
				MAC:       entry.MAC,
				Port:      entry.Port,
				Origin:    entry.Origin,
				Lifetime:  entry.Lifetime,
				LearnedAt: entry.LearnedAt,
			})
		}
	}

	if len(seeds) > 0 {
		if err := next.bridge.Learn(seeds); err != nil {
			return nil, err
		}
	}

	return next, nil
}

type bridgeSeedKey struct {
	fid vlan.ID
	mac netaddr.MAC
}

// restoreMulticastState replays retained dynamic records into the new layer so
// new options and static router ports take effect without resetting expiries.
func restoreMulticastState(next *Switch, retained *mcast.Layer) {
	for vid, cfg := range next.cfg.Mcast.VLANs {
		membershipInterval := cfg.MembershipInterval
		if membershipInterval == 0 {
			membershipInterval = mcast.DefaultMembershipInterval
		}
		for _, entry := range retained.Groups(vid) {
			learnedAt := entry.GroupExpires.Add(-membershipInterval)
			if entry.Group.Is4() {
				next.mcast.Learn(learnedAt, vid, entry.Port, netip.Addr{}, igmp.Message{
					Type:  igmp.ReportV2,
					Group: entry.Group,
				})
			} else {
				next.mcast.LearnMLD(learnedAt, vid, entry.Port, netip.Addr{}, mld.Message{
					Type:  mld.ReportV1,
					Group: entry.Group,
				})
			}
		}

		for _, router := range retained.RouterPorts(vid) {
			if router.Lifetime == mcast.Static || slices.Contains(cfg.RouterPorts, router.Port) {
				continue
			}
			next.mcast.InstallObserved(vid, router.Port, router.Expires)
		}
	}
}

func resolvedSpeeds(s *Switch) map[string]uint64 {
	if s == nil {
		return nil
	}
	speeds := make(map[string]uint64, len(s.ports.Ports()))
	for _, p := range s.ports.Ports() {
		speeds[p.Name] = s.linkSpeed(p)
	}
	return speeds
}
