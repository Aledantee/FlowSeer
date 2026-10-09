package vswitch

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// Derive constructs the target and carries eligible state through the layer table.
// Whole-layer retention compares normalized dependency keys. Bridge and multicast
// retention report whether any source record was lost after a clone of cur
// advances to now. Expired records do not count as lost. Traffic retains unchanged
// policer buckets independently of its whole-layer verdict. Filter is rebuilt.
// Dynamic bridge entries remain bounded by target capacity. Static seeds and
// construction trust come from the target. Port duplex and speed move together
// only where port and PHY dependencies match. device.Output reports held-frame drops
// from rebuilt routing. An invalid target returns an error.
func Derive(cur *Switch, target ConstructionSpec, now time.Time) (*Switch, device.Output, error) {
	next, err := NewWithSpec(target)
	if err != nil {
		return nil, device.Output{}, err
	}

	c, end := next.beginCall(now, true)
	defer end()

	var aged device.Output
	if cur == nil {
		cur = &Switch{}
	} else {
		cur = cur.Fork().(*Switch)
		aged = cur.Advance(now)
	}
	source, targetLayers := cur.layers(time.Time{}), next.layers(time.Time{})
	for i, entry := range targetLayers {
		oldKey, newKey := source[i].key(cur), entry.key(next)
		lost := 0
		if entry.carry != nil {
			lost, err = entry.carry(cur, next)
			if err != nil {
				return nil, device.Output{}, err
			}
		} else if oldKey != "" && oldKey == newKey && source[i].clone != nil && !entry.rebuild {
			source[i].clone(cur, next)
		}
		kept := oldKey == newKey
		if entry.perRecord {
			kept = lost == 0
		}
		report := entry.retention(next)
		*report = LayerRetention{Kept: kept}
		if !kept {
			report.Difference = diffDependency(oldKey, newKey)
			if report.Difference == "" {
				report.Difference = "config"
			}
		}
	}
	if next.bridge != nil {
		for _, entry := range next.layers(time.Time{}) {
			if entry.bind != nil {
				entry.bind(next)
			}
		}
	}
	if cur.portP2P != nil || cur.portSpeed != nil {

		next.portP2P = make(map[string]pointToPoint)
		next.portSpeed = make(map[string]uint64)
		for _, p := range next.ports.Ports() {
			old, ok := cur.ports.Port(p.Name)
			if !ok {
				continue
			}
			if old != p || cur.linkSpeed(old) != next.linkSpeed(p) {
				if _, heard := cur.portP2P[p.Name]; heard {
					next.portP2P[p.Name] = pointToPointUnknown
				}
				continue
			}
			if p2p, ok := cur.portP2P[p.Name]; ok {
				next.portP2P[p.Name] = p2p
			}
			if speed, ok := cur.portSpeed[p.Name]; ok {
				next.portSpeed[p.Name] = speed
			}
		}
	}
	next.recomputeProtocolLinkIssues()
	if !next.retention.Routing.Kept && cur.routing != nil {
		next.applyRoutingExits(now, cur.routing.Clone().FailHeld())
	}
	out := c.output(forwardResult{})
	out.Emissions = append(aged.Emissions, out.Emissions...)
	out.Copies = append(aged.Copies, out.Copies...)
	out.Drops = append(aged.Drops, out.Drops...)
	return next, out, nil
}

func carryMcast(cur, next *Switch) (int, error) {
	return next.mcast.Restore(cur.mcast, func(vid vlan.ID, name string) bool {
		p, ok := next.ports.Port(name)
		if !ok || p.LagParent != "" || !p.Forwards() {
			return false
		}
		sw, ok := next.cfg.Bridge.VLAN.Switchports[name]
		return ok && sw.CarriesVID(vid)
	}), nil
}

func carryBridge(cur, next *Switch) (int, error) {
	old := cur.Entries()
	targetSeeds := make(map[bridgeSeedKey]bool)
	for _, seed := range next.seeds {
		if seed.Lifetime == bridge.Static {
			targetSeeds[bridgeSeedKey{seed.FID, seed.MAC}] = true
		}
	}
	var seeds []bridge.Seed
	for _, entry := range old {
		if entry.Lifetime == bridge.Static || targetSeeds[bridgeSeedKey{entry.FID, entry.MAC}] || next.bridge == nil {
			continue
		}
		p, ok := next.ports.Port(entry.Port)
		if !ok || (p.Kind == port.LAG && len(next.ports.Members(entry.Port)) == 0) {
			continue
		}
		if next.cfg.Bridge.VLAN != nil {
			sw, ok := next.cfg.Bridge.VLAN.Switchports[entry.Port]
			if !ok || !bridge.SwitchportAdmitsFID(sw, entry.FID) {
				continue
			}
		} else if cur.cfg.Bridge.VLAN != nil {
			continue
		}
		seeds = append(seeds, bridge.Seed(entry))
	}
	if len(seeds) > 0 {
		if err := next.bridge.InstallSeeds(seeds); err != nil {
			return 0, err
		}
	}
	keys := make(map[bridgeSeedKey]bool)
	for _, entry := range next.Entries() {
		keys[bridgeSeedKey{entry.FID, entry.MAC}] = true
	}
	lost := 0
	for _, entry := range old {
		if entry.Lifetime != bridge.Static && !keys[bridgeSeedKey{entry.FID, entry.MAC}] {
			lost++
		}
	}
	return lost, nil
}

type bridgeSeedKey struct {
	fid vlan.ID
	mac netaddr.MAC
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
