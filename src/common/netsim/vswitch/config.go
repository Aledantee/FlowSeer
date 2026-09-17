// Package vswitch composes port, physical, and bridge layers into a virtual switch.
package vswitch

import (
	"fmt"
	"slices"
	"strconv"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
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
// optional netsim loop-protection configuration, optional multicast snooping,
// optional layer 3 routing configuration, and optional traffic configuration.
// MAC is the device's base hardware address.
//
// Config is safe for concurrent read access.
type Config struct {
	MAC         netaddr.MAC
	Ports       port.Table
	Phy         *phy.Config
	Bridge      *bridge.Config
	LAG         *lag.Config
	STP         *stp.Config
	LoopProtect *loopprotect.Config
	Mcast       *mcast.Config
	Routing     *routing.Config
	Traffic     *traffic.Config
}

// Canonical returns a deterministic encoding of the configuration semantics used
// by [Config.Equal]. Nil, empty, and defaulted representations that compare equal
// therefore have the same encoding.
func (c Config) Canonical() string {
	changes := Diff(Config{}, c)
	if len(changes) == 0 {
		return "[]"
	}
	return trace.RenderChanges(changes)
}

type configSnapshotFact string

func (f configSnapshotFact) TypeID() string    { return "vswitch.config" }
func (f configSnapshotFact) Canonical() string { return string(f) }

// ConfigFact returns an immutable semantic snapshot of a virtual switch configuration.
func ConfigFact(config Config) trace.Fact {
	return configSnapshotFact(config.Canonical())
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
	if c.LoopProtect != nil {
		caps = append(caps, port.LayerLoopProtect)
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
	if c.MAC.IsGroup() {
		return errs.New().
			Attr("field", "mac").
			Attr("mac", c.MAC).
			Msgf("switch MAC %s cannot be a group MAC", c.MAC)
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
	if err := c.validateTrafficVLANs(); err != nil {
		return err
	}
	if c.LAG != nil {
		if err := c.LAG.Validate(c.Ports); err != nil {
			return err
		}
	}
	if c.STP != nil {
		if c.Bridge == nil {
			return errs.New().Attr("field", "stp").Msg("spanning tree requires bridge configuration")
		}
		if err := c.STP.Validate(c.Ports); err != nil {
			return err
		}
		if err := c.validatePVSTCoversEveryVLAN(); err != nil {
			return err
		}
	}
	if c.LoopProtect != nil {
		if c.Bridge == nil {
			return errs.New().Attr("field", "loop_protect").Msg("loop protection requires bridge configuration")
		}
		if err := c.LoopProtect.Validate(c.Ports); err != nil {
			return err
		}

		portNames := make([]string, 0, len(c.LoopProtect.Ports))
		for name := range c.LoopProtect.Ports {
			portNames = append(portNames, name)
		}
		slices.Sort(portNames)

		for _, name := range portNames {
			var (
				sw    bridge.Switchport
				known bool
			)
			if c.Bridge.VLAN != nil {
				sw, known = c.Bridge.VLAN.Switchports[name]
			}

			if len(c.LoopProtect.Ports[name].VLANs) == 0 {
				if c.Bridge.VLAN == nil {
					continue
				}

				var (
					resolved vlan.ID
					hasVID   bool
				)
				switch {
				case sw.PVID != nil:
					resolved, hasVID = *sw.PVID, true
				case sw.Tunnel != nil:
					resolved, hasVID = sw.Tunnel.VID, true
				}
				if !hasVID {
					return errs.New().
						Attr("field", fmt.Sprintf("loop_protect.ports.%s.vlans", name)).
						Attr("port", name).
						Msgf("loop protection port %q has no VLANs and no PVID or tunnel to probe untagged", name)
				}
				// Validate against the same admission rule egress applies
				// (bridge.Switchport.CarriesVID), so a config that passes
				// here is guaranteed to reach the wire: a PVID that names a
				// VLAN absent from Tagged/Untagged is not, by itself,
				// enough for a frame to leave the port.
				if !sw.CarriesVID(resolved) {
					return errs.New().
						Attr("field", fmt.Sprintf("loop_protect.ports.%s.vlans", name)).
						Attr("port", name).
						Attr("vlan", resolved).
						Msgf("loop protection port %q's untagged VLAN %d is not carried by its switchport", name, resolved)
				}

				continue
			}

			for _, vid := range c.LoopProtect.Ports[name].VLANs {
				if !known || !sw.CarriesVID(vid) {
					return errs.New().
						Attr("field", fmt.Sprintf("loop_protect.ports.%s.vlans", name)).
						Attr("port", name).
						Attr("vlan", vid).
						Msgf("loop protection port %q references VLAN %d not admitted by its switchport", name, vid)
				}
			}
		}
	}
	if c.Mcast != nil {
		if err := c.Mcast.Validate(c.Ports); err != nil {
			return err
		}
		if c.Bridge == nil || c.Bridge.VLAN == nil {
			return errs.New().Attr("field", "mcast").Msg("multicast snooping requires bridge VLAN configuration")
		}

		vids := make([]vlan.ID, 0, len(c.Mcast.VLANs))
		for vid := range c.Mcast.VLANs {
			vids = append(vids, vid)
		}
		slices.Sort(vids)
		for _, vid := range vids {
			if _, ok := c.Bridge.VLAN.Table[vid]; !ok {
				return errs.New().
					Attr("field", fmt.Sprintf("mcast.vlans.%d", vid)).
					Attr("vlan", vid).
					Msgf("multicast snooping references VLAN %d absent from bridge VLAN table", vid)
			}
			for _, name := range c.Mcast.VLANs[vid].RouterPorts {
				switchport, ok := c.Bridge.VLAN.Switchports[name]
				member := ok && (slices.Contains(switchport.Tagged, vid) || slices.Contains(switchport.Untagged, vid) ||
					(switchport.Tunnel != nil && switchport.Tunnel.VID == vid))
				if !member {
					return errs.New().
						Attr("field", fmt.Sprintf("mcast.vlans.%d.router_ports.%s", vid, name)).
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
				// A sub-interface (VLAN and Port both set) classifies by the port's outer
				// tag rather than by bridge VLAN membership, so it needs no bridge VLAN
				// table entry and none of this block's checks apply to it.
				if iface.VLAN != 0 && iface.Port == "" {
					if c.Bridge == nil || c.Bridge.VLAN == nil {
						return errs.New().
							Attr("field", fmt.Sprintf("routing.vrfs.%s.interfaces.%s.vlan", vrfName, ifaceName)).
							Attr("vrf", vrfName).
							Attr("interface", ifaceName).
							Attr("vlan", iface.VLAN).
							Msgf("routed VLAN interface %q requires bridge VLAN configuration", ifaceName)
					}
					if _, ok := c.Bridge.VLAN.Table[iface.VLAN]; !ok {
						return errs.New().
							Attr("field", fmt.Sprintf("routing.vrfs.%s.interfaces.%s.vlan", vrfName, ifaceName)).
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
							Attr("field", fmt.Sprintf("routing.vrfs.%s.interfaces.%s.port", vrfName, ifaceName)).
							Attr("vrf", vrfName).
							Attr("interface", ifaceName).
							Attr("port", iface.Port).
							Msgf("routed port %q needs a relay with VLAN configuration or no relay", iface.Port)
					}
					if c.Bridge != nil && c.Bridge.VLAN != nil {
						if _, ok := c.Bridge.VLAN.Switchports[iface.Port]; ok {
							return errs.New().
								Attr("field", fmt.Sprintf("routing.vrfs.%s.interfaces.%s.port", vrfName, ifaceName)).
								Attr("vrf", vrfName).
								Attr("interface", ifaceName).
								Attr("port", iface.Port).
								Msgf("routed port %q cannot be configured as a bridge switchport", iface.Port)
						}
					}
					if c.STP != nil && c.STP.Ports != nil {
						if _, ok := c.STP.Ports[iface.Port]; ok {
							return errs.New().
								Attr("field", fmt.Sprintf("routing.vrfs.%s.interfaces.%s.port", vrfName, ifaceName)).
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
						Attr("field", fmt.Sprintf("ports.%s", p.Name)).
						Attr("port", p.Name).
						Msgf("router without bridge must route port %q", p.Name)
				}
			}
		}
	}

	return nil
}

// validatePVSTCoversEveryVLAN refuses a PVST switch carrying a VLAN that has
// no tree. This lives here rather than in stp.Config, which never sees the
// bridge's VLAN table: a VLAN with no tree has no defined forwarding state,
// which is exactly the false answer per-VLAN spanning tree exists to remove.
// The reverse, a tree for a VLAN the bridge does not carry, is not an error:
// it configures a tree nothing asks about.
func (c Config) validatePVSTCoversEveryVLAN() error {
	if c.STP.PVST == nil {
		return nil
	}
	if c.Bridge.VLAN == nil {
		return errs.New().
			Attr("field", "stp.pvst").
			Msg("PVST requires a VLAN-aware bridge: there is nothing per-VLAN about a bridge with no VLAN table")
	}

	trees := c.STP.PVST.Normalize(c.STP.Priority).Trees
	vids := make([]vlan.ID, 0, len(c.Bridge.VLAN.Table))
	for vid := range c.Bridge.VLAN.Table {
		vids = append(vids, vid)
	}
	slices.Sort(vids)

	for _, vid := range vids {
		if _, ok := trees[vid]; !ok {
			return errs.New().
				Attr("field", "stp.pvst.trees").
				Attr("vlan", vid).
				Msgf("vlan %d is carried by the bridge but has no per-VLAN spanning tree", vid)
		}
	}

	return nil
}

func (c Config) validateTrafficVLANs() error {
	if c.Traffic == nil {
		return nil
	}

	for mirrorIndex, mirror := range c.Traffic.Mirrors {
		prefix := "traffic.mirrors." + strconv.Itoa(mirrorIndex)
		if mirror.OutputVLAN != nil {
			field := prefix + ".output_vlan"
			if c.Bridge == nil || c.Bridge.VLAN == nil {
				return errs.New().
					Attr("field", field).
					Attr("mirror", mirror.Name).
					Attr("vlan", *mirror.OutputVLAN).
					Msgf("mirror %q output VLAN %d requires bridge VLAN configuration", mirror.Name, *mirror.OutputVLAN)
			}
			if _, ok := c.Bridge.VLAN.Table[*mirror.OutputVLAN]; !ok {
				return errs.New().
					Attr("field", field).
					Attr("mirror", mirror.Name).
					Attr("vlan", *mirror.OutputVLAN).
					Msgf("mirror %q output VLAN %d is absent from the bridge VLAN table", mirror.Name, *mirror.OutputVLAN)
			}
		}

		for selectorIndex, vid := range mirror.SelectVLANs {
			field := prefix + ".select_vlans." + strconv.Itoa(selectorIndex)
			if c.Bridge == nil || c.Bridge.VLAN == nil {
				return errs.New().
					Attr("field", field).
					Attr("mirror", mirror.Name).
					Attr("vlan", vid).
					Msgf("mirror %q selector VLAN %d requires bridge VLAN configuration", mirror.Name, vid)
			}
			if _, ok := c.Bridge.VLAN.Table[vid]; !ok {
				return errs.New().
					Attr("field", field).
					Attr("mirror", mirror.Name).
					Attr("vlan", vid).
					Msgf("mirror %q selector VLAN %d is absent from the bridge VLAN table", mirror.Name, vid)
			}
		}
	}

	return nil
}

// Normalize returns a normalized copy of the switch configuration with standard
// defaults applied across all configured subsystems, deterministic ordering for slices,
// and assigned hardware addresses when missing.
func (c Config) Normalize() Config {
	norm := c.Clone()
	norm.Ports = norm.Ports.Normalize()

	if norm.MAC == (netaddr.MAC{}) {
		explicit := make(map[netaddr.MAC]struct{})
		if norm.STP != nil && norm.STP.Address != (netaddr.MAC{}) {
			explicit[norm.STP.Address] = struct{}{}
		}
		if norm.Routing != nil {
			for _, vrf := range norm.Routing.VRFs {
				for _, iface := range vrf.Interfaces {
					if iface.MAC != (netaddr.MAC{}) {
						explicit[iface.MAC] = struct{}{}
					}
				}
			}
		}
		for n := uint32(1); ; n++ {
			cand := netaddr.Local(n)
			if _, ok := explicit[cand]; !ok {
				norm.MAC = cand
				break
			}
		}
	}

	if norm.Phy != nil {
		p := norm.Phy.Normalize()
		norm.Phy = &p
	}
	if norm.Bridge != nil {
		b := norm.Bridge.Normalize()
		norm.Bridge = &b
	}
	hasLAG := norm.LAG != nil
	if !hasLAG {
		for _, p := range norm.Ports.Ports() {
			if p.Kind == port.Lag {
				hasLAG = true
				break
			}
		}
	}
	if hasLAG {
		var cfg lag.Config
		if norm.LAG != nil {
			cfg = *norm.LAG
		}
		l := cfg.Normalize(norm.Ports, norm.MAC)
		norm.LAG = &l
	}
	if norm.STP != nil {
		if norm.STP.Address == (netaddr.MAC{}) {
			norm.STP.Address = norm.MAC
		}
		s := norm.STP.Normalize()
		norm.STP = &s
	}
	if norm.LoopProtect != nil {
		lp := norm.LoopProtect.Normalize()
		norm.LoopProtect = &lp
	}
	if norm.Mcast != nil {
		m := norm.Mcast.Normalize()
		norm.Mcast = &m
	}
	if norm.Routing != nil {
		for vrfName, vrf := range norm.Routing.VRFs {
			for ifaceName, iface := range vrf.Interfaces {
				if iface.MAC == (netaddr.MAC{}) {
					iface.MAC = norm.MAC
					vrf.Interfaces[ifaceName] = iface
				}
			}
			norm.Routing.VRFs[vrfName] = vrf
		}
		r := norm.Routing.Normalize()
		norm.Routing = &r
	}
	if norm.Traffic != nil {
		t := norm.Traffic.Normalize()
		norm.Traffic = &t
	}

	return norm
}

// Clone returns an independent deep copy of the configuration.
func (c Config) Clone() Config {
	cp := Config{
		MAC:   c.MAC,
		Ports: c.Ports.Clone(),
	}
	if c.Phy != nil {
		phyCfg := c.Phy.Clone()
		cp.Phy = &phyCfg
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
		stpCfg := c.STP.Clone()
		cp.STP = &stpCfg
	}
	if c.LoopProtect != nil {
		lpCfg := c.LoopProtect.Clone()
		cp.LoopProtect = &lpCfg
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

// Equal reports whether two switch configurations are semantically equal.
func (c Config) Equal(other Config) bool {
	return len(Diff(c, other)) == 0
}
