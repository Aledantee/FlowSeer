package routing

import (
	"cmp"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// vlanFact wraps a vlan.ID as a trace.Fact.
type vlanFact vlan.ID

// TypeID returns the fact type identifier for vlanFact.
func (f vlanFact) TypeID() string { return "routing.vlan" }

// Canonical returns the decimal string of the VLAN ID.
func (f vlanFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// VLANFact returns an immutable fact wrapping a VLAN ID.
func VLANFact(vid vlan.ID) trace.Fact { return vlanFact(vid) }

// portFact wraps a port name as a trace.Fact.
type portFact string

// TypeID returns the fact type identifier for portFact.
func (f portFact) TypeID() string { return "routing.port" }

// Canonical returns the port name string.
func (f portFact) Canonical() string { return string(f) }

// PortFact returns an immutable fact wrapping a port name.
func PortFact(name string) trace.Fact { return portFact(name) }

// macFact wraps a netaddr.MAC as a trace.Fact.
type macFact netaddr.MAC

// TypeID returns the fact type identifier for macFact.
func (f macFact) TypeID() string { return "routing.mac" }

// Canonical returns the formatted MAC string.
func (f macFact) Canonical() string { return netaddr.MAC(f).String() }

type prefixesFact string

func (f prefixesFact) TypeID() string    { return "routing.prefixes" }
func (f prefixesFact) Canonical() string { return string(f) }

// PrefixesFact returns an immutable, injective snapshot of IP prefixes in their supplied order.
func PrefixesFact(prefixes []netip.Prefix) trace.Fact {
	var out strings.Builder
	for i, prefix := range prefixes {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(strconv.Quote(prefix.String()))
	}

	return prefixesFact(out.String())
}

// addrFact wraps a netip.Addr as a trace.Fact.
type addrFact netip.Addr

// TypeID returns the fact type identifier for addrFact.
func (f addrFact) TypeID() string { return "routing.addr" }

// Canonical returns the IP address string.
func (f addrFact) Canonical() string { return netip.Addr(f).String() }

// AddrFact returns an immutable fact wrapping a netip.Addr.
func AddrFact(addr netip.Addr) trace.Fact { return addrFact(addr) }

// routeInterfaceFact wraps a route interface name as a trace.Fact.
type routeInterfaceFact string

// TypeID returns the fact type identifier for routeInterfaceFact.
func (f routeInterfaceFact) TypeID() string { return "routing.route.interface" }

// Canonical returns the interface name string.
func (f routeInterfaceFact) Canonical() string { return string(f) }

// routePreferenceFact wraps a route preference as a trace.Fact.
type routePreferenceFact uint8

// TypeID returns the fact type identifier for routePreferenceFact.
func (f routePreferenceFact) TypeID() string { return "routing.route.preference" }

// Canonical returns the decimal preference string.
func (f routePreferenceFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// routeMetricFact wraps a route metric as a trace.Fact.
type routeMetricFact uint32

// TypeID returns the fact type identifier for routeMetricFact.
func (f routeMetricFact) TypeID() string { return "routing.route.metric" }

// Canonical returns the decimal metric string.
func (f routeMetricFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// neighborModeFact wraps a neighbor policy Mode as a trace.Fact.
type neighborModeFact Mode

// TypeID returns the fact type identifier for neighborModeFact.
func (f neighborModeFact) TypeID() string { return "routing.neighbor_policy.mode" }

// Canonical returns the mode string.
func (f neighborModeFact) Canonical() string { return string(f) }

// reachableTimeFact wraps a neighbor policy ReachableTime as a trace.Fact.
type reachableTimeFact time.Duration

// TypeID returns the fact type identifier for reachableTimeFact.
func (f reachableTimeFact) TypeID() string { return "routing.neighbor_policy.reachable_time" }

// Canonical returns the duration string.
func (f reachableTimeFact) Canonical() string { return time.Duration(f).String() }

// resolutionTimeoutFact wraps a neighbor policy ResolutionTimeout as a trace.Fact.
type resolutionTimeoutFact time.Duration

// TypeID returns the fact type identifier for resolutionTimeoutFact.
func (f resolutionTimeoutFact) TypeID() string { return "routing.neighbor_policy.resolution_timeout" }

// Canonical returns the duration string.
func (f resolutionTimeoutFact) Canonical() string { return time.Duration(f).String() }

// holdDepthFact wraps a neighbor policy HoldDepth as a trace.Fact.
type holdDepthFact int

// TypeID returns the fact type identifier for holdDepthFact.
func (f holdDepthFact) TypeID() string { return "routing.neighbor_policy.hold_depth" }

// Canonical returns the decimal depth string.
func (f holdDepthFact) Canonical() string { return strconv.Itoa(int(f)) }

type interfaceSnapshotFact string

func (f interfaceSnapshotFact) TypeID() string    { return "routing.interface" }
func (f interfaceSnapshotFact) Canonical() string { return string(f) }

type vrfSnapshotFact string

func (f vrfSnapshotFact) TypeID() string    { return "routing.vrf" }
func (f vrfSnapshotFact) Canonical() string { return string(f) }

func snapshotInterface(iface Interface) interfaceSnapshotFact {
	return interfaceSnapshotFact("vlan=" + strconv.Itoa(int(iface.VLAN)) +
		";port=" + strconv.Quote(iface.Port) +
		";mac=" + strconv.Quote(iface.MAC.String()) +
		";prefixes=[" + PrefixesFact(iface.Prefixes).Canonical() + "]")
}

func snapshotVRF(vrf VRF) vrfSnapshotFact {
	var b strings.Builder
	b.WriteString("interfaces={")
	interfaceNames := make([]string, 0, len(vrf.Interfaces))
	for name := range vrf.Interfaces {
		interfaceNames = append(interfaceNames, name)
	}
	slices.Sort(interfaceNames)
	for i, name := range interfaceNames {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Quote(name))
		b.WriteByte(':')
		b.WriteString(snapshotInterface(vrf.Interfaces[name]).Canonical())
	}
	b.WriteString("};routes=[")
	for i, route := range vrf.Routes {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("{prefix=")
		b.WriteString(strconv.Quote(route.Prefix.String()))
		b.WriteString(";preference=")
		b.WriteString(strconv.FormatUint(uint64(route.Preference), 10))
		b.WriteString(";metric=")
		b.WriteString(strconv.FormatUint(uint64(route.Metric), 10))
		b.WriteString(";next_hop=")
		b.WriteString(strconv.Quote(route.NextHop.String()))
		b.WriteString(";interface=")
		b.WriteString(strconv.Quote(route.Interface))
		b.WriteByte('}')
	}
	b.WriteString("];neighbors=[")
	for i, neighbor := range vrf.Neighbors {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("{interface=")
		b.WriteString(strconv.Quote(neighbor.Interface))
		b.WriteString(";addr=")
		b.WriteString(strconv.Quote(neighbor.Addr.String()))
		b.WriteString(";mac=")
		b.WriteString(strconv.Quote(neighbor.MAC.String()))
		b.WriteByte('}')
	}
	b.WriteString("];neighbor_policy={mode=")
	b.WriteString(strconv.Quote(string(vrf.NeighborPolicy.Mode)))
	b.WriteString(";reachable_time=")
	b.WriteString(vrf.NeighborPolicy.ReachableTime.String())
	b.WriteString(";resolution_timeout=")
	b.WriteString(vrf.NeighborPolicy.ResolutionTimeout.String())
	b.WriteString(";hold_depth=")
	b.WriteString(strconv.Itoa(vrf.NeighborPolicy.HoldDepth))
	b.WriteString("}")

	return vrfSnapshotFact(b.String())
}

// routeKey identifies a route within a VRF. Preference and metric stay out of it so that
// retuning either reads as one field change rather than a removal and an addition, while a
// second next hop on a prefix that already has one reads as the added route it is.
type routeKey struct {
	prefix  netip.Prefix
	nextHop netip.Addr
	iface   string
}

func keyOfRoute(r Route) routeKey {
	return routeKey{prefix: r.Prefix, nextHop: r.NextHop, iface: r.Interface}
}

// Diff computes the difference between two routing configurations, reporting
// added or removed VRFs, interface changes (vlan, port, mac, prefixes), route
// changes (preference, metric keyed by prefix, next hop, and interface), and
// neighbor changes (mac keyed by interface and address).
func Diff(a, b Config) []trace.Change {
	a = a.Normalize(layer.Env{})
	b = b.Normalize(layer.Env{})

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
				Layer: LayerName,
				Subject: trace.Subject{
					Kind: "vrf",
					Key:  vrfName,
				},
				Field: "",
				From:  snapshotVRF(aVRF),
				To:    nil,
			})
		case !inA && inB:
			changes = append(changes, trace.Change{
				Layer: LayerName,
				Subject: trace.Subject{
					Kind: "vrf",
					Key:  vrfName,
				},
				Field: "",
				From:  nil,
				To:    snapshotVRF(bVRF),
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
				key := trace.CompositeKey(vrfName, ifName)

				switch {
				case ifInA && !ifInB:
					changes = append(changes, trace.Change{
						Layer:   LayerName,
						Subject: trace.Subject{Kind: "interface", Key: key},
						Field:   "",
						From:    snapshotInterface(ifA),
						To:      nil,
					})
				case !ifInA && ifInB:
					changes = append(changes, trace.Change{
						Layer:   LayerName,
						Subject: trace.Subject{Kind: "interface", Key: key},
						Field:   "",
						From:    nil,
						To:      snapshotInterface(ifB),
					})
				case ifInA && ifInB:
					if ifA.VLAN != ifB.VLAN {
						changes = append(changes, trace.Change{
							Layer:   LayerName,
							Subject: trace.Subject{Kind: "interface", Key: key},
							Field:   "vlan",
							From:    vlanFact(ifA.VLAN),
							To:      vlanFact(ifB.VLAN),
						})
					}
					if ifA.Port != ifB.Port {
						changes = append(changes, trace.Change{
							Layer:   LayerName,
							Subject: trace.Subject{Kind: "interface", Key: key},
							Field:   "port",
							From:    portFact(ifA.Port),
							To:      portFact(ifB.Port),
						})
					}
					if ifA.MAC != ifB.MAC {
						changes = append(changes, trace.Change{
							Layer:   LayerName,
							Subject: trace.Subject{Kind: "interface", Key: key},
							Field:   "mac",
							From:    macFact(ifA.MAC),
							To:      macFact(ifB.MAC),
						})
					}
					if !slices.Equal(ifA.Prefixes, ifB.Prefixes) {
						changes = append(changes, trace.Change{
							Layer:   LayerName,
							Subject: trace.Subject{Kind: "interface", Key: key},
							Field:   "prefixes",
							From:    PrefixesFact(slices.Clone(ifA.Prefixes)),
							To:      PrefixesFact(slices.Clone(ifB.Prefixes)),
						})
					}
				}
			}

			aRoutes := make(map[routeKey]Route, len(aVRF.Routes))
			for _, r := range aVRF.Routes {
				aRoutes[keyOfRoute(r)] = r
			}
			bRoutes := make(map[routeKey]Route, len(bVRF.Routes))
			for _, r := range bVRF.Routes {
				bRoutes[keyOfRoute(r)] = r
			}
			routeSet := make(map[routeKey]struct{})
			for k := range aRoutes {
				routeSet[k] = struct{}{}
			}
			for k := range bRoutes {
				routeSet[k] = struct{}{}
			}
			sortedRoutes := make([]routeKey, 0, len(routeSet))
			for k := range routeSet {
				sortedRoutes = append(sortedRoutes, k)
			}
			slices.SortFunc(sortedRoutes, func(x, y routeKey) int {
				if c := comparePrefix(x.prefix, y.prefix); c != 0 {
					return c
				}
				if c := x.nextHop.Compare(y.nextHop); c != 0 {
					return c
				}
				return cmp.Compare(x.iface, y.iface)
			})

			for _, k := range sortedRoutes {
				rA, rInA := aRoutes[k]
				rB, rInB := bRoutes[k]
				key := trace.CompositeKey(vrfName, k.prefix.String(), k.nextHop.String(), k.iface)

				switch {
				case rInA && !rInB:
					changes = append(changes, trace.Change{
						Layer:   LayerName,
						Subject: trace.Subject{Kind: "route", Key: key},
						Field:   "",
						From:    rA,
						To:      nil,
					})
				case !rInA && rInB:
					changes = append(changes, trace.Change{
						Layer:   LayerName,
						Subject: trace.Subject{Kind: "route", Key: key},
						Field:   "",
						From:    nil,
						To:      rB,
					})
				case rInA && rInB:
					if rA.Preference != rB.Preference {
						changes = append(changes, trace.Change{
							Layer:   LayerName,
							Subject: trace.Subject{Kind: "route", Key: key},
							Field:   "preference",
							From:    routePreferenceFact(rA.Preference),
							To:      routePreferenceFact(rB.Preference),
						})
					}
					if rA.Metric != rB.Metric {
						changes = append(changes, trace.Change{
							Layer:   LayerName,
							Subject: trace.Subject{Kind: "route", Key: key},
							Field:   "metric",
							From:    routeMetricFact(rA.Metric),
							To:      routeMetricFact(rB.Metric),
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
				key := trace.CompositeKey(vrfName, k.iface, k.addr.String())

				switch {
				case nInA && !nInB:
					changes = append(changes, trace.Change{
						Layer:   LayerName,
						Subject: trace.Subject{Kind: "neighbor", Key: key},
						Field:   "",
						From:    nA,
						To:      nil,
					})
				case !nInA && nInB:
					changes = append(changes, trace.Change{
						Layer:   LayerName,
						Subject: trace.Subject{Kind: "neighbor", Key: key},
						Field:   "",
						From:    nil,
						To:      nB,
					})
				case nInA && nInB:
					if nA.MAC != nB.MAC {
						changes = append(changes, trace.Change{
							Layer:   LayerName,
							Subject: trace.Subject{Kind: "neighbor", Key: key},
							Field:   "mac",
							From:    macFact(nA.MAC),
							To:      macFact(nB.MAC),
						})
					}
				}
			}

			policySubject := trace.Subject{Kind: "neighbor-policy", Key: vrfName}
			if aVRF.NeighborPolicy.Mode != bVRF.NeighborPolicy.Mode {
				changes = append(changes, trace.Change{
					Layer:   LayerName,
					Subject: policySubject,
					Field:   "mode",
					From:    neighborModeFact(aVRF.NeighborPolicy.Mode),
					To:      neighborModeFact(bVRF.NeighborPolicy.Mode),
				})
			}
			if aVRF.NeighborPolicy.ReachableTime != bVRF.NeighborPolicy.ReachableTime {
				changes = append(changes, trace.Change{
					Layer:   LayerName,
					Subject: policySubject,
					Field:   "reachable_time",
					From:    reachableTimeFact(aVRF.NeighborPolicy.ReachableTime),
					To:      reachableTimeFact(bVRF.NeighborPolicy.ReachableTime),
				})
			}
			if aVRF.NeighborPolicy.ResolutionTimeout != bVRF.NeighborPolicy.ResolutionTimeout {
				changes = append(changes, trace.Change{
					Layer:   LayerName,
					Subject: policySubject,
					Field:   "resolution_timeout",
					From:    resolutionTimeoutFact(aVRF.NeighborPolicy.ResolutionTimeout),
					To:      resolutionTimeoutFact(bVRF.NeighborPolicy.ResolutionTimeout),
				})
			}
			if aVRF.NeighborPolicy.HoldDepth != bVRF.NeighborPolicy.HoldDepth {
				changes = append(changes, trace.Change{
					Layer:   LayerName,
					Subject: policySubject,
					Field:   "hold_depth",
					From:    holdDepthFact(aVRF.NeighborPolicy.HoldDepth),
					To:      holdDepthFact(bVRF.NeighborPolicy.HoldDepth),
				})
			}
		}
	}

	return changes
}
