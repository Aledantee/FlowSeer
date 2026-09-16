// Package routing implements layer 3 forwarding, routing tables, and neighbor resolution
// for the virtual switch and simulated endpoints.
package routing

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// DefaultVRF is the standard VRF name used by single-table configurations and default loaders.
const DefaultVRF = "default"

// Interface defines the routed presence of a virtual switch interface, operating as a
// VLAN interface (VLAN set, Port empty), an untagged routed port (Port set, VLAN zero),
// or a routed sub-interface (both set), which classifies by the outer VLAN tag of frames
// arriving on Port rather than by bridge VLAN membership.
type Interface struct {
	VLAN     vlan.ID
	Port     string
	MAC      netaddr.MAC
	Prefixes []netip.Prefix
}

// Route defines a static forwarding entry mapping an IP prefix to a next-hop IP address,
// an egress interface, or both. Preference is the administrative distance and Metric the
// tie-break within one preference; the lower value wins for both. Preference 0 belongs to
// connected routes, so a static route left at 0 normalizes to 1 and never reaches that tier.
type Route struct {
	Prefix     netip.Prefix
	NextHop    netip.Addr
	Interface  string
	Preference uint8
	Metric     uint32
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

// Normalize returns a normalized copy of the configuration, masking route prefixes, raising
// a static route's reserved preference 0 to 1, sorting routes by prefix, preference, metric,
// next hop, and interface, neighbors by (interface, addr), and interface prefixes.
func (c Config) Normalize() Config {
	cloned := c.Clone()
	for vrfName, vrf := range cloned.VRFs {
		for ifName, iface := range vrf.Interfaces {
			slices.SortFunc(iface.Prefixes, comparePrefix)
			vrf.Interfaces[ifName] = iface
		}
		for i := range vrf.Routes {
			if vrf.Routes[i].Prefix.IsValid() {
				vrf.Routes[i].Prefix = vrf.Routes[i].Prefix.Masked()
			}
			if vrf.Routes[i].Preference == 0 {
				vrf.Routes[i].Preference = 1
			}
		}
		slices.SortFunc(vrf.Routes, func(a, b Route) int {
			if c := comparePrefix(a.Prefix, b.Prefix); c != 0 {
				return c
			}
			if c := cmp.Compare(a.Preference, b.Preference); c != 0 {
				return c
			}
			if c := cmp.Compare(a.Metric, b.Metric); c != 0 {
				return c
			}
			if c := a.NextHop.Compare(b.NextHop); c != 0 {
				return c
			}
			return cmp.Compare(a.Interface, b.Interface)
		})
		slices.SortFunc(vrf.Neighbors, func(x, y Neighbor) int {
			if c := cmp.Compare(x.Interface, y.Interface); c != 0 {
				return c
			}
			return x.Addr.Compare(y.Addr)
		})
		cloned.VRFs[vrfName] = vrf
	}
	return cloned
}

// TypeID returns the fact type identifier for Route.
func (r Route) TypeID() string { return "routing.route" }

// Canonical returns the canonical string representation of the Route fact.
func (r Route) Canonical() string {
	return fmt.Sprintf("prefix=%q,preference=%d,metric=%d,next_hop=%q,interface=%q",
		r.Prefix.String(), r.Preference, r.Metric, r.NextHop.String(), r.Interface)
}

// TypeID returns the fact type identifier for Neighbor.
func (n Neighbor) TypeID() string { return "routing.neighbor" }

// Canonical returns the canonical string representation of the Neighbor fact.
func (n Neighbor) Canonical() string {
	return fmt.Sprintf("interface=%q,addr=%q,mac=%q", n.Interface, n.Addr.String(), n.MAC.String())
}

// portVLAN keys a routed port claim by the port name and the outer VLAN a sub-interface
// classifies on; a plain routed port claims VLAN 0.
type portVLAN struct {
	port string
	vlan vlan.ID
}

func comparePrefix(a, b netip.Prefix) int {
	if c := a.Addr().Compare(b.Addr()); c != 0 {
		return c
	}
	return cmp.Compare(a.Bits(), b.Bits())
}

// Validate verifies the configuration against the port table and internal routing invariants.
// It refuses empty VRF or interface names, VRFs with no interfaces, an interface configuring
// neither VLAN nor Port, a Port-bearing interface whose VLAN is set but outside 1 through 4094,
// duplicate VLAN interfaces across VRFs, duplicate (Port, VLAN) assignments across VRFs, unknown
// ports, LAG members configured as routed ports, group MAC addresses, unmasked route prefixes,
// routes with neither next hop nor interface, next hops whose address family differs from
// their route's prefix, unspecified or multicast next hops,
// routes or neighbors referencing interfaces outside their VRF, neighbor address families
// mismatching all interface prefixes, and duplicate neighbor entries within a VRF.
//
// A prefix may carry several routes, which is how an equal-cost set is configured. Two routes
// collide only when they agree on prefix, preference, metric, next hop, and interface alike.
//
// A next hop that is not on-link is valid configuration: [New] resolves it against the VRF's
// own table and withdraws the route from the forwarding table when it cannot, which is where
// the reason for an unusable next hop is reported. See [Layer.WithdrawnRoutes].
func (c Config) Validate(ports port.Table) error {
	vrfNames := make([]string, 0, len(c.VRFs))
	for name := range c.VRFs {
		vrfNames = append(vrfNames, name)
	}
	slices.Sort(vrfNames)

	claimedVLANs := make(map[vlan.ID]string)
	claimedPortVLANs := make(map[portVLAN]string)
	// The layer keys interfaces by name across every VRF, as a device does.
	claimedIfaces := make(map[string]string)

	for _, vrfName := range vrfNames {
		if vrfName == "" {
			return errs.New().
				Attr("field", "vrfs").
				Msg("VRF name cannot be empty")
		}

		vrf := c.VRFs[vrfName]
		if len(vrf.Interfaces) == 0 {
			return errs.New().
				Attr("vrf", vrfName).
				Attr("field", "vrfs."+vrfName+".interfaces").
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
					Attr("field", "vrfs."+vrfName+".interfaces").
					Msgf("interface name in VRF %q cannot be empty", vrfName)
			}

			if prev, ok := claimedIfaces[ifaceName]; ok {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("interface", ifaceName).
					Attr("claimed_by", prev).
					Attr("field", "vrfs."+vrfName+".interfaces."+ifaceName).
					Msgf("interface %q is named by VRF %q and VRF %q", ifaceName, prev, vrfName)
			}
			claimedIfaces[ifaceName] = vrfName

			iface := vrf.Interfaces[ifaceName]
			hasVLAN := iface.VLAN != 0
			hasPort := iface.Port != ""
			if !hasVLAN && !hasPort {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("interface", ifaceName).
					Attr("field", "vrfs."+vrfName+".interfaces."+ifaceName).
					Msgf("interface %q must configure at least one of VLAN or Port", ifaceName)
			}

			if hasPort && hasVLAN && !iface.VLAN.Valid() {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("interface", ifaceName).
					Attr("vlan", iface.VLAN).
					Attr("field", "vrfs."+vrfName+".interfaces."+ifaceName+".vlan").
					Msgf("sub-interface %q VLAN %d is outside the assignable range 1-4094", ifaceName, iface.VLAN)
			}

			if hasVLAN && !hasPort {
				if prev, ok := claimedVLANs[iface.VLAN]; ok {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("interface", ifaceName).
						Attr("vlan", iface.VLAN).
						Attr("claimed_by", prev).
						Attr("field", "vrfs."+vrfName+".interfaces."+ifaceName+".vlan").
						Msgf("VLAN %d claimed by multiple interfaces across VRFs (%q and %q)", iface.VLAN, prev, ifaceName)
				}
				claimedVLANs[iface.VLAN] = ifaceName
			}

			if hasPort {
				key := portVLAN{port: iface.Port, vlan: iface.VLAN}
				if prev, ok := claimedPortVLANs[key]; ok {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("interface", ifaceName).
						Attr("port", iface.Port).
						Attr("vlan", iface.VLAN).
						Attr("claimed_by", prev).
						Attr("field", "vrfs."+vrfName+".interfaces."+ifaceName+".port").
						Msgf("port %q VLAN %d claimed by multiple interfaces across VRFs (%q and %q)", iface.Port, iface.VLAN, prev, ifaceName)
				}
				claimedPortVLANs[key] = ifaceName

				p, ok := ports.Port(iface.Port)
				if !ok {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("interface", ifaceName).
						Attr("port", iface.Port).
						Attr("field", "vrfs."+vrfName+".interfaces."+ifaceName+".port").
						Msgf("routed port %q absent from port table", iface.Port)
				}
				if p.LagParent != "" {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("interface", ifaceName).
						Attr("port", iface.Port).
						Attr("parent", p.LagParent).
						Attr("field", "vrfs."+vrfName+".interfaces."+ifaceName+".port").
						Msgf("routed port %q cannot be a LAG member", iface.Port)
				}
			}

			if iface.MAC.IsGroup() {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("interface", ifaceName).
					Attr("mac", iface.MAC).
					Attr("field", "vrfs."+vrfName+".interfaces."+ifaceName+".mac").
					Msgf("interface %q MAC %s cannot be a group MAC", ifaceName, iface.MAC)
			}

			for _, p := range iface.Prefixes {
				if !p.IsValid() {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("interface", ifaceName).
						Attr("field", "vrfs."+vrfName+".interfaces."+ifaceName+".prefixes").
						Msgf("interface %q contains invalid prefix", ifaceName)
				}
				if p.Addr().Is4In6() {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("interface", ifaceName).
						Attr("prefix", p).
						Attr("field", "vrfs."+vrfName+".interfaces."+ifaceName+".prefixes").
						Msgf("interface %q address %s is IPv4-mapped; a decoded IPv4 address is 4 bytes and never matches it", ifaceName, p)
				}
			}
		}

		seenRoutes := make(map[Route]struct{}, len(vrf.Routes))
		for _, r := range vrf.Routes {
			if !r.Prefix.IsValid() || r.Prefix != r.Prefix.Masked() {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("prefix", r.Prefix).
					Attr("field", "vrfs."+vrfName+".routes."+r.Prefix.String()).
					Msgf("route prefix %s is not masked", r.Prefix)
			}
			if r.Prefix.Addr().Is4In6() || r.NextHop.Is4In6() {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("prefix", r.Prefix).
					Attr("next_hop", r.NextHop).
					Attr("field", "vrfs."+vrfName+".routes."+r.Prefix.String()).
					Msgf("route %s is IPv4-mapped; a decoded IPv4 address is 4 bytes and never matches it", r.Prefix)
			}
			if _, dup := seenRoutes[r]; dup {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("prefix", r.Prefix).
					Attr("preference", r.Preference).
					Attr("metric", r.Metric).
					Attr("next_hop", r.NextHop).
					Attr("interface", r.Interface).
					Attr("field", "vrfs."+vrfName+".routes."+r.Prefix.String()).
					Msgf("route %s appears twice in VRF %q with the same preference, metric, next hop, and interface", r.Prefix, vrfName)
			}
			seenRoutes[r] = struct{}{}

			if !r.NextHop.IsValid() && r.Interface == "" {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("prefix", r.Prefix).
					Attr("field", "vrfs."+vrfName+".routes."+r.Prefix.String()).
					Msgf("route %s must name at least one of next hop or interface", r.Prefix)
			}

			if r.NextHop.IsValid() {
				if r.NextHop.Is4() != r.Prefix.Addr().Is4() {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("prefix", r.Prefix).
						Attr("next_hop", r.NextHop).
						Attr("field", "vrfs."+vrfName+".routes."+r.Prefix.String()+".next_hop").
						Msgf("route %s next hop %s belongs to a different address family than the prefix", r.Prefix, r.NextHop)
				}
				if r.NextHop.IsUnspecified() || r.NextHop.IsMulticast() {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("prefix", r.Prefix).
						Attr("next_hop", r.NextHop).
						Attr("field", "vrfs."+vrfName+".routes."+r.Prefix.String()+".next_hop").
						Msgf("route %s next hop %s must be a unicast address", r.Prefix, r.NextHop)
				}
			}

			if r.Interface != "" {
				if _, ok := vrf.Interfaces[r.Interface]; !ok {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("route", r.Prefix).
						Attr("interface", r.Interface).
						Attr("field", "vrfs."+vrfName+".routes."+r.Prefix.String()+".interface").
						Msgf("route %s names interface %q outside VRF %q", r.Prefix, r.Interface, vrfName)
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
					Attr("field", "vrfs."+vrfName+".neighbors."+n.Interface+"/"+n.Addr.String()+".interface").
					Msgf("neighbor %s names interface %q outside VRF %q", n.Addr, n.Interface, vrfName)
			}

			if n.MAC == (netaddr.MAC{}) || n.MAC.IsGroup() {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("neighbor", n.Addr).
					Attr("mac", n.MAC).
					Attr("field", "vrfs."+vrfName+".neighbors."+n.Interface+"/"+n.Addr.String()+".mac").
					Msgf("neighbor %s MAC %s must be a non-zero individual MAC", n.Addr, n.MAC)
			}
			if n.Addr.Is4In6() {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("neighbor", n.Addr).
					Attr("interface", n.Interface).
					Attr("field", "vrfs."+vrfName+".neighbors."+n.Interface+"/"+n.Addr.String()+".addr").
					Msgf("neighbor %s is IPv4-mapped; a decoded IPv4 address is 4 bytes and never matches it", n.Addr)
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
					Attr("field", "vrfs."+vrfName+".neighbors."+n.Interface+"/"+n.Addr.String()+".addr").
					Msgf("neighbor %s address family differs from every prefix on interface %q", n.Addr, n.Interface)
			}

			key := neighborKey{iface: n.Interface, addr: n.Addr}
			if _, exists := seenNeighbors[key]; exists {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("neighbor", n.Addr).
					Attr("interface", n.Interface).
					Attr("field", "vrfs."+vrfName+".neighbors."+n.Interface+"/"+n.Addr.String()).
					Msgf("duplicate neighbor %s on interface %q in VRF %q", n.Addr, n.Interface, vrfName)
			}
			seenNeighbors[key] = struct{}{}
		}
	}

	return nil
}
