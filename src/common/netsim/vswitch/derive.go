package vswitch

import (
	"slices"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

// Derive builds a new [Switch] from the target configuration, seeding it with every
// dynamic forwarding database entry from the current switch that the new configuration
// still admits. Reseeded dynamic entries count as learned and are bounded by the new
// configuration's MaxEntries, evicting from the oldest; static entries are not reseeded.
// A derived standalone switch keeps the roles of the current one when the spanning tree
// configuration is unchanged. It returns an error if the new configuration fails validation.
func Derive(cur *Switch, cfg Config) (*Switch, error) {
	if cur != nil && cfg.MAC == (netaddr.MAC{}) {
		cfg.MAC = cur.cfg.MAC
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	next := New(cfg)
	if cur != nil && cur.traffic != nil && next.traffic != nil &&
		len(traffic.Diff(*cur.cfg.Traffic, *next.cfg.Traffic)) == 0 {
		next.buckets = make(map[string]*traffic.Bucket, len(cur.buckets))
		for name, bucket := range cur.buckets {
			next.buckets[name] = bucket.Clone()
		}
	}

	// Both sides are compared as New filled them, so a bridge address the
	// switch assigned does not read as a change.
	if cur != nil && cur.stp != nil && next.cfg.STP != nil && len(stp.Diff(*cur.cfg.STP, *next.cfg.STP)) == 0 {
		next.stp = cur.stp.Clone()
		if next.bridge != nil {
			next.bridge.SetGate(next.stp)
		}
		if cur.portP2P != nil {
			next.portP2P = make(map[string]bool, len(cur.portP2P))
			for k, v := range cur.portP2P {
				next.portP2P[k] = v
			}
		}
		if cur.portSpeed != nil {
			next.portSpeed = make(map[string]uint64, len(cur.portSpeed))
			for k, v := range cur.portSpeed {
				next.portSpeed[k] = v
			}
		}
	}

	if cur != nil && cur.lag != nil && next.lag != nil {
		var aLAG, bLAG lag.Config
		if cur.cfg.LAG != nil {
			aLAG = *cur.cfg.LAG
		}
		if next.cfg.LAG != nil {
			bLAG = *next.cfg.LAG
		}
		if len(lag.Diff(aLAG, bLAG)) == 0 {
			next.lag = cur.lag.Clone()
			if next.bridge != nil {
				next.bridge.SetSelector(next.lag)
			}
		}
	}

	if cur == nil || cur.bridge == nil || next.bridge == nil {
		return next, nil
	}

	curVLAN := cur.cfg.Bridge != nil && cur.cfg.Bridge.VLAN != nil
	nextVLAN := cfg.Bridge != nil && cfg.Bridge.VLAN != nil

	var seeds []bridge.Seed
	for _, entry := range cur.Entries() {
		if entry.Static {
			continue
		}

		p, ok := cfg.Ports.Port(entry.Port)
		if !ok {
			continue
		}
		if p.Kind == port.Lag && len(cfg.Ports.Members(entry.Port)) == 0 {
			continue
		}

		if nextVLAN {
			sw, ok := cfg.Bridge.VLAN.Switchports[entry.Port]
			if !ok {
				continue
			}
			admitted := slices.Contains(sw.Tagged, entry.FID) || slices.Contains(sw.Untagged, entry.FID) ||
				(sw.PVID != nil && *sw.PVID == entry.FID) ||
				(sw.Tunnel != nil && sw.Tunnel.VID == entry.FID)
			if !admitted {
				continue
			}
			seeds = append(seeds, bridge.Seed{
				FID:       entry.FID,
				MAC:       entry.MAC,
				Port:      entry.Port,
				Static:    false,
				LearnedAt: entry.LearnedAt,
			})
		} else if !curVLAN {
			seeds = append(seeds, bridge.Seed{
				FID:       0,
				MAC:       entry.MAC,
				Port:      entry.Port,
				Static:    false,
				LearnedAt: entry.LearnedAt,
			})
		}
	}

	if len(seeds) > 0 {
		next.bridge.Learn(seeds)
	}

	return next, nil
}
