// Package routing implements layer 3 forwarding, routing tables, and neighbor resolution
// for the virtual switch and simulated endpoints.
package routing

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"time"

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
// to a destination MAC address. [New] loads it into the neighbor table as [NeighborReachable]
// with no expiry, so a static binding never ages out the way an observed one does.
type Neighbor struct {
	Interface string
	Addr      netip.Addr
	MAC       netaddr.MAC
}

// Mode selects whether a VRF's neighbor table resolves an address it was not told about, or
// refuses every such next hop outright.
type Mode string

const (
	// NeighborObserved is the zero value: an unresolved next hop enters the RFC 4861 section
	// 7.2.2 hold-and-resolve cycle described in the package README's "Neighbor lifecycle"
	// section.
	NeighborObserved Mode = ""

	// NeighborDisabled means the VRF never holds a frame for resolution; an absent or
	// previously failed neighbor is a terminal miss.
	NeighborDisabled Mode = "disabled"
)

// Default neighbor policy timers and depth, from RFC 4861 section 10: REACHABLE_TIME is
// 30,000 milliseconds, and the resolution timeout is MAX_MULTICAST_SOLICIT (3 transmissions)
// times RETRANS_TIMER (1,000 milliseconds). HoldDepth defaults above section 7.2.2's permitted
// minimum of one, because an analysis library is asked which of several frames arrived, and a
// depth of one answers that for the last one only.
const (
	defaultReachableTime     = 30 * time.Second
	defaultResolutionTimeout = 3 * time.Second
	defaultHoldDepth         = 3
)

// NeighborPolicy configures how a VRF's neighbor table resolves and holds. The zero value
// normalizes to the RFC 4861 section 10 defaults under [NeighborObserved].
type NeighborPolicy struct {
	Mode              Mode
	ReachableTime     time.Duration
	ResolutionTimeout time.Duration
	HoldDepth         int
}

func (p NeighborPolicy) normalize() NeighborPolicy {
	if p.ReachableTime == 0 {
		p.ReachableTime = defaultReachableTime
	}
	if p.ResolutionTimeout == 0 {
		p.ResolutionTimeout = defaultResolutionTimeout
	}
	if p.HoldDepth == 0 {
		p.HoldDepth = defaultHoldDepth
	}
	return p
}

// VRF represents an isolated virtual routing and forwarding instance with its own interfaces,
// static routes, and neighbor table.
type VRF struct {
	Interfaces     map[string]Interface
	Routes         []Route
	Neighbors      []Neighbor
	NeighborPolicy NeighborPolicy
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
			Interfaces:     make(map[string]Interface, len(vrf.Interfaces)),
			Routes:         slices.Clone(vrf.Routes),
			Neighbors:      slices.Clone(vrf.Neighbors),
			NeighborPolicy: vrf.NeighborPolicy,
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
		vrf.NeighborPolicy = vrf.NeighborPolicy.normalize()
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

func comparePrefix(a, b netip.Prefix) int {
	if c := a.Addr().Compare(b.Addr()); c != 0 {
		return c
	}
	return cmp.Compare(a.Bits(), b.Bits())
}

// Validate verifies the configuration against the port table and internal routing invariants.
// It refuses empty VRF or interface names, VRFs with no interfaces, interfaces with both or
// neither VLAN and Port, duplicate VLAN or port assignments across VRFs, unknown ports,
// LAG members configured as routed ports, group MAC addresses, unmasked route prefixes,
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
	claimedPorts := make(map[string]string)
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
			if (hasVLAN && hasPort) || (!hasVLAN && !hasPort) {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("interface", ifaceName).
					Attr("field", "vrfs."+vrfName+".interfaces."+ifaceName).
					Msgf("interface %q must configure exactly one of VLAN or Port", ifaceName)
			}

			if hasVLAN {
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
				if prev, ok := claimedPorts[iface.Port]; ok {
					return errs.New().
						Attr("vrf", vrfName).
						Attr("interface", ifaceName).
						Attr("port", iface.Port).
						Attr("claimed_by", prev).
						Attr("field", "vrfs."+vrfName+".interfaces."+ifaceName+".port").
						Msgf("port %q claimed by multiple interfaces across VRFs (%q and %q)", iface.Port, prev, ifaceName)
				}
				claimedPorts[iface.Port] = ifaceName

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

		policy := vrf.NeighborPolicy
		switch policy.Mode {
		case NeighborObserved, NeighborDisabled:
		default:
			return errs.New().
				Attr("vrf", vrfName).
				Attr("mode", string(policy.Mode)).
				Attr("field", "vrfs."+vrfName+".neighbor_policy.mode").
				Msgf("VRF %q neighbor policy mode %q is not one of the two named values", vrfName, policy.Mode)
		}
		// Zero on either field means "unset" here exactly as it does for a route's Preference
		// above: [NeighborPolicy.normalize] raises it to the RFC 4861 section 10 default, so
		// only a negative value — one normalization leaves alone — claims a hold path the
		// configuration cannot run.
		if policy.Mode == NeighborObserved {
			if policy.HoldDepth < 0 {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("hold_depth", policy.HoldDepth).
					Attr("field", "vrfs."+vrfName+".neighbor_policy.hold_depth").
					Msgf("VRF %q neighbor policy hold depth %d cannot be negative under NeighborObserved", vrfName, policy.HoldDepth)
			}
			if policy.ResolutionTimeout < 0 {
				return errs.New().
					Attr("vrf", vrfName).
					Attr("resolution_timeout", policy.ResolutionTimeout).
					Attr("field", "vrfs."+vrfName+".neighbor_policy.resolution_timeout").
					Msgf("VRF %q neighbor policy resolution timeout cannot be negative under NeighborObserved", vrfName)
			}
		}
	}

	return nil
}
