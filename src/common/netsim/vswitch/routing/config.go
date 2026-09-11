// Package routing implements layer 3 forwarding, routing tables, and neighbor resolution
// for the virtual switch and simulated endpoints.
package routing

import (
	"net/netip"
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// DefaultVRF is the standard VRF name used by single-table configurations and default loaders.
const DefaultVRF = "default"

// Interface defines the routed presence of a virtual switch interface, operating either
// as a VLAN interface (VLAN set, Port empty) or as a routed port (Port set, VLAN zero).
type Interface struct {
	VLAN     vlan.ID
	Port     string
	MAC      netaddr.MAC
	Prefixes []netip.Prefix
}

// Route defines a static forwarding entry mapping an IP prefix to a next-hop IP address,
// an egress interface, or both.
type Route struct {
	Prefix    netip.Prefix
	NextHop   netip.Addr
	Interface string
}

// Neighbor defines a static link-layer address binding mapping an IP address on an interface
// to a destination MAC address.
type Neighbor struct {
	Interface string
	Addr      netip.Addr
	MAC       netaddr.MAC
}

// VRF represents an isolated virtual routing and forwarding instance with its own interfaces,
// static routes, and neighbor table.
type VRF struct {
	Interfaces map[string]Interface
	Routes     []Route
	Neighbors  []Neighbor
}

// Config defines the complete routing capability configuration of a virtual switch,
// structured as a collection of VRFs keyed by name.
type Config struct {
	VRFs map[string]VRF
}

// Clone returns a deep copy of the routing configuration.
func (c Config) Clone() Config {
	cloned := Config{
		VRFs: make(map[string]VRF, len(c.VRFs)),
	}
	for vrfName, vrf := range c.VRFs {
		clonedVRF := VRF{
			Interfaces: make(map[string]Interface, len(vrf.Interfaces)),
			Routes:     slices.Clone(vrf.Routes),
			Neighbors:  slices.Clone(vrf.Neighbors),
		}
		for ifName, iface := range vrf.Interfaces {
			clonedVRF.Interfaces[ifName] = Interface{
				VLAN:     iface.VLAN,
				Port:     iface.Port,
				MAC:      iface.MAC,
				Prefixes: slices.Clone(iface.Prefixes),
			}
		}
		cloned.VRFs[vrfName] = clonedVRF
	}
	return cloned
}

// Validate verifies the configuration against the port table and internal routing invariants.
// It refuses empty VRF or interface names, VRFs with no interfaces, interfaces with both or
// neither VLAN and Port, duplicate VLAN or port assignments across VRFs, unknown ports,
// LAG members configured as routed ports, group MAC addresses, unmasked route prefixes,
// interface address prefixes of length 0, routes with neither next hop nor interface,
// routes or neighbors referencing interfaces outside their VRF, next hops unreachable by
// any interface prefix in the VRF when the route omits an interface, neighbor address families
// mismatching all interface prefixes, and duplicate neighbor entries within a VRF.
func (c Config) Validate(ports port.Table) error {
	vrfNames := make([]string, 0, len(c.VRFs))
	for name := range c.VRFs {
		vrfNames = append(vrfNames, name)
	}
	slices.Sort(vrfNames)

	claimedVLANs := make(map[vlan.ID]string)
	claimedPorts := make(map[string]string)

	for _, vrfName := range vrfNames {
		if vrfName == "" {
			return errs.New().Msg("VRF name cannot be empty")
		}

		vrf := c.VRFs[vrfName]
		if len(vrf.Interfaces) == 0 {
			return errs.New().
				Attr("vrf", vrfName).
				Msgf("VRF %q must have at least one interface", vrfName)
		}

		ifaceNames := make([]string, 0, len(vrf.Interfaces))
		for name := range vrf.Interfaces {
			ifaceNames = append(ifaceNames, name)
		}
		slices.Sort(ifaceNames)

		for _, ifaceName := range ifaceNames {
			if ifaceName == "" {
				return errs.New().
					Attr("vrf", vrfName).
					Msgf("interface name in VRF %q cannot be empty", vrfName)
			}

			iface := vrf.Interfaces[ifaceName]
			hasVLAN := iface.VLAN != 0
			hasPort := iface.Port != ""
			if (hasVLAN && hasPort) || (!hasVLAN && !hasPort) {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("interface", ifaceName).
					Msgf("interface %q must configure exactly one of VLAN or Port", ifaceName)
			}

			if hasVLAN {
				if prev, ok := claimedVLANs[iface.VLAN]; ok {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("interface", ifaceName).
						Attr("vlan", iface.VLAN).
						Attr("claimed_by", prev).
						Msgf("VLAN %d claimed by multiple interfaces across VRFs (%q and %q)", iface.VLAN, prev, ifaceName)
				}
				claimedVLANs[iface.VLAN] = ifaceName
			}

			if hasPort {
				if prev, ok := claimedPorts[iface.Port]; ok {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("interface", ifaceName).
						Attr("port", iface.Port).
						Attr("claimed_by", prev).
						Msgf("port %q claimed by multiple interfaces across VRFs (%q and %q)", iface.Port, prev, ifaceName)
				}
				claimedPorts[iface.Port] = ifaceName

				p, ok := ports.Port(iface.Port)
				if !ok {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("interface", ifaceName).
						Attr("port", iface.Port).
						Msgf("routed port %q absent from port table", iface.Port)
				}
				if p.LagParent != "" {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("interface", ifaceName).
						Attr("port", iface.Port).
						Attr("parent", p.LagParent).
						Msgf("routed port %q cannot be a LAG member", iface.Port)
				}
			}

			if iface.MAC.IsGroup() {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("interface", ifaceName).
					Attr("mac", iface.MAC).
					Msgf("interface %q MAC %s cannot be a group MAC", ifaceName, iface.MAC)
			}

			for _, p := range iface.Prefixes {
				if !p.IsValid() {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("interface", ifaceName).
						Msgf("interface %q contains invalid prefix", ifaceName)
				}
				if p.Bits() == 0 {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("interface", ifaceName).
						Attr("prefix", p).
						Msgf("interface %q address %s cannot have prefix length 0", ifaceName, p)
				}
			}
		}

		for _, r := range vrf.Routes {
			if !r.Prefix.IsValid() || r.Prefix != r.Prefix.Masked() {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("prefix", r.Prefix).
					Msgf("route prefix %s is not masked", r.Prefix)
			}

			if !r.NextHop.IsValid() && r.Interface == "" {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("prefix", r.Prefix).
					Msgf("route %s must name at least one of next hop or interface", r.Prefix)
			}

			if r.Interface != "" {
				if _, ok := vrf.Interfaces[r.Interface]; !ok {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("route", r.Prefix).
						Attr("interface", r.Interface).
						Msgf("route %s names interface %q outside VRF %q", r.Prefix, r.Interface, vrfName)
				}
			} else {
				found := false
				for _, iface := range vrf.Interfaces {
					for _, p := range iface.Prefixes {
						if p.Contains(r.NextHop) {
							found = true
							break
						}
					}
					if found {
						break
					}
				}
				if !found {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("route", r.Prefix).
						Attr("next_hop", r.NextHop).
						Msgf("route %s next hop %s not contained in any interface prefix in VRF %q", r.Prefix, r.NextHop, vrfName)
				}
			}
		}

		seenNeighbors := make(map[neighborKey]struct{}, len(vrf.Neighbors))
		for _, n := range vrf.Neighbors {
			iface, ok := vrf.Interfaces[n.Interface]
			if !ok {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("neighbor", n.Addr).
					Attr("interface", n.Interface).
					Msgf("neighbor %s names interface %q outside VRF %q", n.Addr, n.Interface, vrfName)
			}

			if n.MAC.IsGroup() {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("neighbor", n.Addr).
					Attr("mac", n.MAC).
					Msgf("neighbor %s MAC %s cannot be a group MAC", n.Addr, n.MAC)
			}

			matchingFamily := false
			for _, p := range iface.Prefixes {
				if (p.Addr().Is4() && n.Addr.Is4()) || (p.Addr().Is6() && n.Addr.Is6()) {
					matchingFamily = true
					break
				}
			}
			if !matchingFamily {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("neighbor", n.Addr).
					Attr("interface", n.Interface).
					Msgf("neighbor %s address family differs from every prefix on interface %q", n.Addr, n.Interface)
			}

			key := neighborKey{iface: n.Interface, addr: n.Addr}
			if _, exists := seenNeighbors[key]; exists {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("neighbor", n.Addr).
					Attr("interface", n.Interface).
					Msgf("duplicate neighbor %s on interface %q in VRF %q", n.Addr, n.Interface, vrfName)
			}
			seenNeighbors[key] = struct{}{}
		}
	}

	return nil
}
