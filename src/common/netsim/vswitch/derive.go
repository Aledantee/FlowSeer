package vswitch

import (
	"slices"

	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Derive builds a new [Switch] from the target configuration, seeding it with every
// dynamic forwarding database entry from the current switch that the new configuration
// still admits. It returns an error if the new configuration fails validation.
func Derive(cur *Switch, cfg Config) (*Switch, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	next := New(cfg)
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
			if !slices.Contains(sw.Tagged, entry.FID) && !slices.Contains(sw.Untagged, entry.FID) {
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
