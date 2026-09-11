// Package vswitch composes port, physical, and bridge layers into a virtual switch.
package vswitch

import (
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

// Config specifies the configuration of a virtual switch, combining its port table,
// optional physical layer attributes, optional bridge relay and VLAN configuration,
// and optional spanning tree configuration.
//
// Config is safe for concurrent read access.
type Config struct {
	Ports  port.Table
	Phy    *phy.Config
	Bridge *bridge.Config
	STP    *stp.Config
}

// Capabilities returns the sorted architectural layers implied by the present configuration.
func (c Config) Capabilities() []port.Layer {
	var caps []port.Layer

	if c.Bridge != nil {
		caps = append(caps, port.LayerRelay)
		if c.Bridge.VLAN != nil {
			caps = append(caps, port.LayerVlan)
		}
	}
	if c.STP != nil {
		caps = append(caps, port.LayerStp)
	}
	if c.Phy != nil {
		if c.Phy.Ethernet != nil {
			caps = append(caps, port.LayerEthernet)
		}
		if c.Phy.PoE != nil {
			caps = append(caps, port.LayerPoe)
		}
	}
	for _, p := range c.Ports.Ports() {
		if p.Kind == port.Lag {
			caps = append(caps, port.LayerLag)
			break
		}
	}

	slices.Sort(caps)

	return caps
}

// Validate verifies the invariants of the configuration by validating the port table
// and each present subsystem against that table.
func (c Config) Validate() error {
	if err := c.Ports.Validate(); err != nil {
		return err
	}
	if c.Phy != nil {
		if err := c.Phy.Validate(c.Ports); err != nil {
			return err
		}
	}
	if c.Bridge != nil {
		if err := c.Bridge.Validate(c.Ports); err != nil {
			return err
		}
	}
	if c.STP != nil {
		if c.Bridge == nil {
			return errs.New().Msg("spanning tree requires bridge configuration")
		}
		if err := c.STP.Validate(c.Ports); err != nil {
			return err
		}
	}

	return nil
}

// Clone returns an independent deep copy of the configuration.
func (c Config) Clone() Config {
	cp := Config{
		Ports: c.Ports.Clone(),
	}
	if c.Phy != nil {
		cp.Phy = clonePhy(c.Phy)
	}
	if c.Bridge != nil {
		b := c.Bridge.Clone()
		cp.Bridge = &b
	}
	if c.STP != nil {
		stpCfg := *c.STP
		if c.STP.Ports != nil {
			stpCfg.Ports = make(map[string]stp.Port, len(c.STP.Ports))
			for k, v := range c.STP.Ports {
				stpCfg.Ports[k] = v
			}
		}
		cp.STP = &stpCfg
	}

	return cp
}

func clonePhy(p *phy.Config) *phy.Config {
	if p == nil {
		return nil
	}
	cp := &phy.Config{}
	if p.Ethernet != nil {
		cp.Ethernet = make(map[string]phy.Ethernet, len(p.Ethernet))
		for k, v := range p.Ethernet {
			eth := v
			if len(v.SupportedSpeedsBPS) > 0 {
				eth.SupportedSpeedsBPS = make([]uint64, len(v.SupportedSpeedsBPS))
				copy(eth.SupportedSpeedsBPS, v.SupportedSpeedsBPS)
			}
			if v.Setting != nil {
				s := *v.Setting
				eth.Setting = &s
			}
			if v.Observed != nil {
				o := *v.Observed
				eth.Observed = &o
			}
			cp.Ethernet[k] = eth
		}
	}
	if p.PoE != nil {
		poe := &phy.PoE{}
		if p.PoE.Groups != nil {
			poe.Groups = make(map[string]phy.Group, len(p.PoE.Groups))
			for k, v := range p.PoE.Groups {
				poe.Groups[k] = v
			}
		}
		if p.PoE.Ports != nil {
			poe.Ports = make(map[string]phy.PsePort, len(p.PoE.Ports))
			for k, v := range p.PoE.Ports {
				pp := v
				if v.Limit != nil {
					lim := *v.Limit
					pp.Limit = &lim
				}
				poe.Ports[k] = pp
			}
		}
		cp.PoE = poe
	}

	return cp
}
