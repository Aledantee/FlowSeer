// Package phy provides the physical layer of the virtual switch: per-port
// Ethernet speed resolution and Power over Ethernet budget allocation.
package phy

import (
	"slices"
)

// Config is the physical-layer configuration of a virtual switch, keyed by
// port name. A nil Ethernet map is the Ethernet capability absent; a nil PoE
// is the power-sourcing capability absent.
type Config struct {
	Ethernet map[string]Ethernet
	PoE      *PoE
}

// Clone returns an independent deep copy of the configuration.
func (c Config) Clone() Config {
	cp := Config{}
	if c.Ethernet != nil {
		cp.Ethernet = make(map[string]Ethernet, len(c.Ethernet))
		for k, v := range c.Ethernet {
			cp.Ethernet[k] = v.Clone()
		}
	}
	if c.PoE != nil {
		cp.PoE = c.PoE.Clone()
	}

	return cp
}

// Normalize returns an independent copy of the configuration with standard defaults applied.
// In the Ethernet configuration, supported speeds are sorted and deduplicated, and fixed settings
// default Duplex to [Unknown] when unspecified. In the PoE configuration, unspecified port priority
// defaults to [PriorityLow].
func (c Config) Normalize() Config {
	cp := Config{}
	if c.Ethernet != nil {
		cp.Ethernet = make(map[string]Ethernet, len(c.Ethernet))
		for _, name := range sortedKeys(c.Ethernet) {
			e := c.Ethernet[name].Clone()
			if len(e.SupportedSpeedsBPS) > 0 {
				slices.Sort(e.SupportedSpeedsBPS)
				e.SupportedSpeedsBPS = slices.Compact(e.SupportedSpeedsBPS)
			}
			if e.Setting != nil && !e.Setting.AutoNegotiation {
				if e.Setting.Duplex == "" {
					e.Setting.Duplex = Unknown
				}
			}
			cp.Ethernet[name] = e
		}
	}
	if c.PoE != nil {
		cp.PoE = c.PoE.Clone()
		for _, name := range sortedKeys(cp.PoE.Ports) {
			p := cp.PoE.Ports[name]
			if p.Priority == "" {
				p.Priority = PriorityLow
			}
			cp.PoE.Ports[name] = p
		}
	}

	return cp
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	return keys
}
