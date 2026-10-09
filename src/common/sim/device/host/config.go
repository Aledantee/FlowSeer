// Package host implements a single-port endpoint with optional IP addressing.
package host

import (
	"net/netip"
	"slices"
	"strconv"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// IP configures the layer 3 addressing, default gateway, and static link-layer neighbors
// for a simulated host.
type IP struct {
	Addresses []netip.Prefix
	Gateway   netip.Addr
	Neighbors map[netip.Addr]netaddr.MAC
}

// Config describes a single-port endpoint with no relay or forwarding database.
// A nil VLAN emits untagged frames and accepts untagged or VID 0 C-tags.
// A non-nil VLAN requires a C-tag with that VID. IP enables packet origination
// through static neighbors and narrows IP acceptance. Accept widens destination
// acceptance. Ethernet's zero value reports no physical facts.
type Config struct {
	Address  netaddr.MAC
	VLAN     *vlan.ID
	IP       *IP
	Ethernet phy.Ethernet
	Accept   Accept
}

// Accept widens which frames a host accepts beyond its own address, broadcast, and the groups its IP
// stack joins. Promiscuous accepts every destination MAC and skips the IP check, AllMulticast accepts every
// group MAC, and Multicast lists further group MACs; a unicast entry is invalid. The zero value accepts
// nothing more.
type Accept struct {
	Promiscuous  bool
	AllMulticast bool
	Multicast    []netaddr.MAC
}

// Clone returns an independent deep copy of the host configuration.
func (h Config) Clone() Config {
	cp := h
	cp.Ethernet.SupportedSpeedsBPS = slices.Clone(h.Ethernet.SupportedSpeedsBPS)
	cp.Accept.Multicast = slices.Clone(h.Accept.Multicast)
	// Addressing and port facts share backing storage to avoid an allocation
	// per optional pointer. Each clone owns its storage and mutable collections.
	if h.VLAN != nil || h.IP != nil || h.Ethernet.Setting != nil || h.Ethernet.Observed != nil {
		facts := new(struct {
			vlan     vlan.ID
			ip       IP
			setting  phy.Setting
			observed phy.Observed
		})
		if h.VLAN != nil {
			facts.vlan = *h.VLAN
			cp.VLAN = &facts.vlan
		}
		if h.Ethernet.Setting != nil {
			facts.setting = *h.Ethernet.Setting
			cp.Ethernet.Setting = &facts.setting
		}
		if h.Ethernet.Observed != nil {
			facts.observed = *h.Ethernet.Observed
			cp.Ethernet.Observed = &facts.observed
		}
		if h.IP != nil {
			facts.ip = IP{Addresses: slices.Clone(h.IP.Addresses), Gateway: h.IP.Gateway}
			if h.IP.Neighbors != nil {
				facts.ip.Neighbors = make(map[netip.Addr]netaddr.MAC, len(h.IP.Neighbors))
				for k, v := range h.IP.Neighbors {
					facts.ip.Neighbors[k] = v
				}
			}
			cp.IP = &facts.ip
		}
	}

	return cp
}

// Equal reports whether two host configurations are identical.
func (h Config) Equal(other Config) bool {
	if h.Address != other.Address {
		return false
	}
	if (h.VLAN == nil) != (other.VLAN == nil) {
		return false
	}
	if h.VLAN != nil && *h.VLAN != *other.VLAN {
		return false
	}
	if h.Ethernet.Canonical() != other.Ethernet.Canonical() {
		return false
	}
	if h.Accept.Promiscuous != other.Accept.Promiscuous || h.Accept.AllMulticast != other.Accept.AllMulticast ||
		!slices.Equal(h.Accept.Multicast, other.Accept.Multicast) {
		return false
	}
	if (h.IP == nil) != (other.IP == nil) {
		return false
	}
	if h.IP != nil {
		if !slices.Equal(h.IP.Addresses, other.IP.Addresses) || h.IP.Gateway != other.IP.Gateway {
			return false
		}
		if len(h.IP.Neighbors) != len(other.IP.Neighbors) {
			return false
		}
		for k, v := range h.IP.Neighbors {
			if other.IP.Neighbors[k] != v {
				return false
			}
		}
	}
	return true
}

// RoutingConfig translates a host's IP configuration into a virtual switch routing configuration
// and single-port table for layer 3 packet origination.
func RoutingConfig(name string, h Config) (routing.Config, port.Table) {
	b := port.NewBuilder()
	b.Add(port.Port{
		Name:        name,
		Kind:        port.Physical,
		AdminStatus: port.Up,
		OperStatus:  port.Up,
	})
	// One physical port with a name and no LAG parent cannot fail the
	// table's rules; Config.Validate refuses an empty host name before this.
	tbl, _ := b.Build()

	if h.IP == nil {
		return routing.Config{}, tbl
	}

	var routes []routing.Route
	if h.IP.Gateway.IsValid() {
		var pfx netip.Prefix
		if h.IP.Gateway.Is4() {
			pfx = netip.MustParsePrefix("0.0.0.0/0")
		} else if h.IP.Gateway.Is6() {
			pfx = netip.MustParsePrefix("::/0")
		}
		routes = append(routes, routing.Route{
			Prefix:  pfx,
			NextHop: h.IP.Gateway,
		})
	}

	var neighbors []routing.Neighbor
	if len(h.IP.Neighbors) > 0 {
		addrs := make([]netip.Addr, 0, len(h.IP.Neighbors))
		for addr := range h.IP.Neighbors {
			addrs = append(addrs, addr)
		}
		slices.SortFunc(addrs, func(a, b netip.Addr) int {
			return a.Compare(b)
		})
		for _, addr := range addrs {
			neighbors = append(neighbors, routing.Neighbor{
				Interface: name,
				Addr:      addr,
				MAC:       h.IP.Neighbors[addr],
			})
		}
	}

	vrf := routing.VRF{
		Interfaces: map[string]routing.Interface{
			name: {
				Port:     name,
				MAC:      h.Address,
				Prefixes: slices.Clone(h.IP.Addresses),
			},
		},
		Routes:    routes,
		Neighbors: neighbors,
		// A host answers local solicitations but only originates through
		// configured neighbors. It owns no resolution timers or hold queue.
		NeighborPolicy: routing.NeighborPolicy{Mode: routing.NeighborDisabled},
	}

	return routing.Config{
		ClearRouterFlag: true,
		VRFs: map[string]routing.VRF{
			routing.DefaultVRF: vrf,
		},
	}, tbl
}

// Validate refuses unicast entries in the multicast acceptance list.
func (a Accept) Validate(field string) error {
	for i, mac := range a.Multicast {
		if !mac.IsGroup() {
			return errs.New().
				Attr("field", field+".multicast."+strconv.Itoa(i)).
				Attr("mac", mac).
				Msgf("accepted multicast entry %s is not a group address", mac)
		}
	}

	return nil
}
