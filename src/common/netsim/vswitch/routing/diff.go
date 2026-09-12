package routing

import (
	"cmp"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// VLANFact wraps a vlan.ID as a trace.Fact.
type VLANFact vlan.ID

// TypeID returns the fact type identifier for VLANFact.
func (f VLANFact) TypeID() string { return "routing.vlan" }

// Canonical returns the decimal string of the VLAN ID.
func (f VLANFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// VID returns the underlying vlan.ID.
func (f VLANFact) VID() vlan.ID { return vlan.ID(f) }

// PortFact wraps a port name as a trace.Fact.
type PortFact string

// TypeID returns the fact type identifier for PortFact.
func (f PortFact) TypeID() string { return "routing.port" }

// Canonical returns the port name string.
func (f PortFact) Canonical() string { return string(f) }

// MACFact wraps a netaddr.MAC as a trace.Fact.
type MACFact netaddr.MAC

// TypeID returns the fact type identifier for MACFact.
func (f MACFact) TypeID() string { return "routing.mac" }

// Canonical returns the formatted MAC string.
func (f MACFact) Canonical() string { return netaddr.MAC(f).String() }

// PrefixesFact wraps a slice of IP prefixes as a trace.Fact.
type PrefixesFact []netip.Prefix

// TypeID returns the fact type identifier for PrefixesFact.
func (f PrefixesFact) TypeID() string { return "routing.prefixes" }

// Canonical returns the comma-separated prefix strings.
func (f PrefixesFact) Canonical() string {
	if len(f) == 0 {
		return ""
	}
	strs := make([]string, len(f))
	for i, p := range f {
		strs[i] = p.String()
	}
	return strings.Join(strs, ",")
}

// AddrFact wraps a netip.Addr as a trace.Fact.
type AddrFact netip.Addr

// TypeID returns the fact type identifier for AddrFact.
func (f AddrFact) TypeID() string { return "routing.addr" }

// Canonical returns the IP address string.
func (f AddrFact) Canonical() string { return netip.Addr(f).String() }

// RouteInterfaceFact wraps a route interface name as a trace.Fact.
type RouteInterfaceFact string

// TypeID returns the fact type identifier for RouteInterfaceFact.
func (f RouteInterfaceFact) TypeID() string { return "routing.interface" }

// Canonical returns the interface name string.
func (f RouteInterfaceFact) Canonical() string { return string(f) }

// Diff computes the difference between two routing configurations, reporting
// added or removed VRFs, interface changes (vlan, port, mac, prefixes), route
// changes (next_hop, interface keyed by prefix), and neighbor changes (mac
// keyed by interface and address).
func Diff(a, b Config) []trace.Change {
	a = a.Normalize()
	b = b.Normalize()

	var changes []trace.Change

	vrfNames := make(map[string]struct{})
	for name := range a.VRFs {
		vrfNames[name] = struct{}{}
	}
	for name := range b.VRFs {
		vrfNames[name] = struct{}{}
	}
	sortedVRFs := make([]string, 0, len(vrfNames))
	for name := range vrfNames {
		sortedVRFs = append(sortedVRFs, name)
	}
	slices.Sort(sortedVRFs)

	for _, vrfName := range sortedVRFs {
		aVRF, inA := a.VRFs[vrfName]
		bVRF, inB := b.VRFs[vrfName]

		switch {
		case inA && !inB:
			changes = append(changes, trace.Change{
				Layer: port.LayerRouting,
				Subject: trace.Subject{
					Kind: "vrf",
					Key:  vrfName,
				},
				Field: "",
				From:  aVRF,
				To:    nil,
			})
		case !inA && inB:
			changes = append(changes, trace.Change{
				Layer: port.LayerRouting,
				Subject: trace.Subject{
					Kind: "vrf",
					Key:  vrfName,
				},
				Field: "",
				From:  nil,
				To:    bVRF,
			})
		case inA && inB:
			ifaceNames := make(map[string]struct{})
			for name := range aVRF.Interfaces {
				ifaceNames[name] = struct{}{}
			}
			for name := range bVRF.Interfaces {
				ifaceNames[name] = struct{}{}
			}
			sortedIfaces := make([]string, 0, len(ifaceNames))
			for name := range ifaceNames {
				sortedIfaces = append(sortedIfaces, name)
			}
			slices.Sort(sortedIfaces)

			for _, ifName := range sortedIfaces {
				ifA, ifInA := aVRF.Interfaces[ifName]
				ifB, ifInB := bVRF.Interfaces[ifName]
				key := vrfName + "/" + ifName

				switch {
				case ifInA && !ifInB:
					changes = append(changes, trace.Change{
						Layer:   port.LayerRouting,
						Subject: trace.Subject{Kind: "interface", Key: key},
						Field:   "",
						From:    ifA,
						To:      nil,
					})
				case !ifInA && ifInB:
					changes = append(changes, trace.Change{
						Layer:   port.LayerRouting,
						Subject: trace.Subject{Kind: "interface", Key: key},
						Field:   "",
						From:    nil,
						To:      ifB,
					})
				case ifInA && ifInB:
					if ifA.VLAN != ifB.VLAN {
						changes = append(changes, trace.Change{
							Layer:   port.LayerRouting,
							Subject: trace.Subject{Kind: "interface", Key: key},
							Field:   "vlan",
							From:    VLANFact(ifA.VLAN),
							To:      VLANFact(ifB.VLAN),
						})
					}
					if ifA.Port != ifB.Port {
						changes = append(changes, trace.Change{
							Layer:   port.LayerRouting,
							Subject: trace.Subject{Kind: "interface", Key: key},
							Field:   "port",
							From:    PortFact(ifA.Port),
							To:      PortFact(ifB.Port),
						})
					}
					if ifA.MAC != ifB.MAC {
						changes = append(changes, trace.Change{
							Layer:   port.LayerRouting,
							Subject: trace.Subject{Kind: "interface", Key: key},
							Field:   "mac",
							From:    MACFact(ifA.MAC),
							To:      MACFact(ifB.MAC),
						})
					}
					if !slices.Equal(ifA.Prefixes, ifB.Prefixes) {
						changes = append(changes, trace.Change{
							Layer:   port.LayerRouting,
							Subject: trace.Subject{Kind: "interface", Key: key},
							Field:   "prefixes",
							From:    PrefixesFact(slices.Clone(ifA.Prefixes)),
							To:      PrefixesFact(slices.Clone(ifB.Prefixes)),
						})
					}
				}
			}

			aRoutes := make(map[netip.Prefix]Route, len(aVRF.Routes))
			for _, r := range aVRF.Routes {
				aRoutes[r.Prefix] = r
			}
			bRoutes := make(map[netip.Prefix]Route, len(bVRF.Routes))
			for _, r := range bVRF.Routes {
				bRoutes[r.Prefix] = r
			}
			prefixSet := make(map[netip.Prefix]struct{})
			for p := range aRoutes {
				prefixSet[p] = struct{}{}
			}
			for p := range bRoutes {
				prefixSet[p] = struct{}{}
			}
			sortedPrefixes := make([]netip.Prefix, 0, len(prefixSet))
			for p := range prefixSet {
				sortedPrefixes = append(sortedPrefixes, p)
			}
			slices.SortFunc(sortedPrefixes, comparePrefix)

			for _, p := range sortedPrefixes {
				rA, rInA := aRoutes[p]
				rB, rInB := bRoutes[p]
				key := vrfName + "/" + p.String()

				switch {
				case rInA && !rInB:
					changes = append(changes, trace.Change{
						Layer:   port.LayerRouting,
						Subject: trace.Subject{Kind: "route", Key: key},
						Field:   "",
						From:    rA,
						To:      nil,
					})
				case !rInA && rInB:
					changes = append(changes, trace.Change{
						Layer:   port.LayerRouting,
						Subject: trace.Subject{Kind: "route", Key: key},
						Field:   "",
						From:    nil,
						To:      rB,
					})
				case rInA && rInB:
					if rA.NextHop != rB.NextHop {
						changes = append(changes, trace.Change{
							Layer:   port.LayerRouting,
							Subject: trace.Subject{Kind: "route", Key: key},
							Field:   "next_hop",
							From:    AddrFact(rA.NextHop),
							To:      AddrFact(rB.NextHop),
						})
					}
					if rA.Interface != rB.Interface {
						changes = append(changes, trace.Change{
							Layer:   port.LayerRouting,
							Subject: trace.Subject{Kind: "route", Key: key},
							Field:   "interface",
							From:    RouteInterfaceFact(rA.Interface),
							To:      RouteInterfaceFact(rB.Interface),
						})
					}
				}
			}

			aNeighbors := make(map[neighborKey]Neighbor, len(aVRF.Neighbors))
			for _, n := range aVRF.Neighbors {
				aNeighbors[neighborKey{iface: n.Interface, addr: n.Addr}] = n
			}
			bNeighbors := make(map[neighborKey]Neighbor, len(bVRF.Neighbors))
			for _, n := range bVRF.Neighbors {
				bNeighbors[neighborKey{iface: n.Interface, addr: n.Addr}] = n
			}
			neighborSet := make(map[neighborKey]struct{})
			for k := range aNeighbors {
				neighborSet[k] = struct{}{}
			}
			for k := range bNeighbors {
				neighborSet[k] = struct{}{}
			}
			sortedNeighbors := make([]neighborKey, 0, len(neighborSet))
			for k := range neighborSet {
				sortedNeighbors = append(sortedNeighbors, k)
			}
			slices.SortFunc(sortedNeighbors, func(x, y neighborKey) int {
				if c := cmp.Compare(x.iface, y.iface); c != 0 {
					return c
				}
				return x.addr.Compare(y.addr)
			})

			for _, k := range sortedNeighbors {
				nA, nInA := aNeighbors[k]
				nB, nInB := bNeighbors[k]
				key := vrfName + "/" + k.iface + "/" + k.addr.String()

				switch {
				case nInA && !nInB:
					changes = append(changes, trace.Change{
						Layer:   port.LayerRouting,
						Subject: trace.Subject{Kind: "neighbor", Key: key},
						Field:   "",
						From:    nA,
						To:      nil,
					})
				case !nInA && nInB:
					changes = append(changes, trace.Change{
						Layer:   port.LayerRouting,
						Subject: trace.Subject{Kind: "neighbor", Key: key},
						Field:   "",
						From:    nil,
						To:      nB,
					})
				case nInA && nInB:
					if nA.MAC != nB.MAC {
						changes = append(changes, trace.Change{
							Layer:   port.LayerRouting,
							Subject: trace.Subject{Kind: "neighbor", Key: key},
							Field:   "mac",
							From:    MACFact(nA.MAC),
							To:      MACFact(nB.MAC),
						})
					}
				}
			}
		}
	}

	return changes
}
