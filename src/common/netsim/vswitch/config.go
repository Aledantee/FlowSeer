// Package vswitch composes port, physical, and bridge layers into a virtual switch.
package vswitch

import (
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

// Config specifies the configuration of a virtual switch, combining its port table,
// optional physical layer attributes, optional bridge relay and VLAN configuration,
// optional link aggregation configuration, optional spanning tree configuration,
// optional multicast snooping, optional layer 3 routing configuration, and optional
// traffic configuration.
// MAC is the device's base hardware address.
//
// Config is safe for concurrent read access.
type Config struct {
	MAC     netaddr.MAC
	Ports   port.Table
	Phy     *phy.Config
	Bridge  *bridge.Config
	LAG     *lag.Config
	STP     *stp.Config
	Mcast   *mcast.Config
	Routing *routing.Config
	Traffic *traffic.Config
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
	if c.Mcast != nil {
		caps = append(caps, port.LayerMcast)
	}
	if c.Routing != nil {
		caps = append(caps, port.LayerRouting)
	}
	if c.Traffic != nil {
		caps = append(caps, port.LayerTraffic)
	}
	if c.Phy != nil {
		if c.Phy.Ethernet != nil {
			caps = append(caps, port.LayerEthernet)
		}
		if c.Phy.PoE != nil {
			caps = append(caps, port.LayerPoe)
		}
	}
	hasLag := c.LAG != nil
	if !hasLag {
		for _, p := range c.Ports.Ports() {
			if p.Kind == port.Lag {
				hasLag = true
				break
			}
		}
	}
	if hasLag {
		caps = append(caps, port.LayerLag)
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
	if c.Traffic != nil {
		if err := c.Traffic.Validate(c.Ports); err != nil {
			return err
		}
	}
	if c.Bridge != nil {
		if err := c.Bridge.Validate(c.Ports); err != nil {
			return err
		}
	}
	if c.LAG != nil {
		if err := c.LAG.Validate(c.Ports); err != nil {
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
	if c.Mcast != nil {
		if err := c.Mcast.Validate(c.Ports); err != nil {
			return err
		}
		if c.Bridge == nil || c.Bridge.VLAN == nil {
			return errs.New().Msg("multicast snooping requires bridge VLAN configuration")
		}

		vids := make([]vlan.ID, 0, len(c.Mcast.VLANs))
		for vid := range c.Mcast.VLANs {
			vids = append(vids, vid)
		}
		slices.Sort(vids)
		for _, vid := range vids {
			if _, ok := c.Bridge.VLAN.Table[vid]; !ok {
				return errs.New().
					Attr("vlan", vid).
					Msgf("multicast snooping references VLAN %d absent from bridge VLAN table", vid)
			}
			for _, name := range c.Mcast.VLANs[vid].RouterPorts {
				p, _ := c.Ports.Port(name)
				switchport, ok := c.Bridge.VLAN.Switchports[name]
				member := ok && (slices.Contains(switchport.Tagged, vid) || slices.Contains(switchport.Untagged, vid) ||
					(switchport.Tunnel != nil && switchport.Tunnel.VID == vid))
				if p.LagParent != "" || !p.Forwards() || !member {
					return errs.New().
						Attr("vlan", vid).
						Attr("port", name).
						Msgf("multicast router port %q is not a logical forwarding member of VLAN %d", name, vid)
				}
			}
		}
	}
	if c.Routing != nil {
		if err := c.Routing.Validate(c.Ports); err != nil {
			return err
		}

		vrfNames := make([]string, 0, len(c.Routing.VRFs))
		for name := range c.Routing.VRFs {
			vrfNames = append(vrfNames, name)
		}
		slices.Sort(vrfNames)

		for _, vrfName := range vrfNames {
			vrf := c.Routing.VRFs[vrfName]
			ifaceNames := make([]string, 0, len(vrf.Interfaces))
			for name := range vrf.Interfaces {
				ifaceNames = append(ifaceNames, name)
			}
			slices.Sort(ifaceNames)

			for _, ifaceName := range ifaceNames {
				iface := vrf.Interfaces[ifaceName]
				if iface.VLAN != 0 {
					if c.Bridge == nil || c.Bridge.VLAN == nil {
						return errs.New().
							Attr("vrf", vrfName).
							Attr("interface", ifaceName).
							Attr("vlan", iface.VLAN).
							Msgf("routed VLAN interface %q requires bridge VLAN configuration", ifaceName)
					}
					if _, ok := c.Bridge.VLAN.Table[iface.VLAN]; !ok {
						return errs.New().
							Attr("vrf", vrfName).
							Attr("interface", ifaceName).
							Attr("vlan", iface.VLAN).
							Msgf("routed VLAN interface %q references VLAN %d absent from bridge VLAN table", ifaceName, iface.VLAN)
					}
				}

				if iface.Port != "" {
					// A relay without VLANs floods to every forwarding port and
					// has no table to leave a routed port out of.
					if c.Bridge != nil && c.Bridge.VLAN == nil {
						return errs.New().
							Attr("vrf", vrfName).
							Attr("interface", ifaceName).
							Attr("port", iface.Port).
							Msgf("routed port %q needs a relay with VLAN configuration or no relay", iface.Port)
					}
					if c.Bridge != nil && c.Bridge.VLAN != nil {
						if _, ok := c.Bridge.VLAN.Switchports[iface.Port]; ok {
							return errs.New().
								Attr("vrf", vrfName).
								Attr("interface", ifaceName).
								Attr("port", iface.Port).
								Msgf("routed port %q cannot be configured as a bridge switchport", iface.Port)
						}
					}
					if c.STP != nil && c.STP.Ports != nil {
						if _, ok := c.STP.Ports[iface.Port]; ok {
							return errs.New().
								Attr("vrf", vrfName).
								Attr("interface", ifaceName).
								Attr("port", iface.Port).
								Msgf("routed port %q cannot be configured as a spanning tree port", iface.Port)
						}
					}
				}
			}
		}

		if c.Bridge == nil {
			routedPorts := make(map[string]struct{})
			for _, vrf := range c.Routing.VRFs {
				for _, iface := range vrf.Interfaces {
					if iface.Port != "" {
						routedPorts[iface.Port] = struct{}{}
					}
				}
			}
			for _, p := range c.Ports.Ports() {
				if p.LagParent != "" {
					continue
				}
				if _, ok := routedPorts[p.Name]; !ok {
					return errs.New().
						Attr("port", p.Name).
						Msgf("router without bridge must route port %q", p.Name)
				}
			}
		}
	}

	return nil
}

// Clone returns an independent deep copy of the configuration.
func (c Config) Clone() Config {
	cp := Config{
		MAC:   c.MAC,
		Ports: c.Ports.Clone(),
	}
	if c.Phy != nil {
		cp.Phy = clonePhy(c.Phy)
	}
	if c.Bridge != nil {
		b := c.Bridge.Clone()
		cp.Bridge = &b
	}
	if c.LAG != nil {
		lagCfg := c.LAG.Clone()
		cp.LAG = &lagCfg
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
	if c.Mcast != nil {
		mcastCfg := c.Mcast.Clone()
		cp.Mcast = &mcastCfg
	}
	if c.Routing != nil {
		r := c.Routing.Clone()
		cp.Routing = &r
	}
	if c.Traffic != nil {
		trafficCfg := c.Traffic.Clone()
		cp.Traffic = &trafficCfg
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
