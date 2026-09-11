package routing

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

const (
	// ReasonNoRoute indicates a frame dropped because no route matches the destination address.
	ReasonNoRoute trace.Reason = "no-route"

	// ReasonTTLExpired indicates a frame dropped because its hop limit expired in transit.
	ReasonTTLExpired trace.Reason = "ttl-expired"

	// ReasonNeighborMiss indicates a frame dropped because next-hop neighbor resolution failed.
	ReasonNeighborMiss trace.Reason = "neighbor-miss"

	// ReasonNotRouted indicates a frame addressed to a local interface of the device was consumed rather than routed.
	ReasonNotRouted trace.Reason = "not-routed"

	// ReasonBadHeader indicates an IP header that could not be decoded.
	ReasonBadHeader trace.Reason = "bad-header"

	// ReasonNotBridged indicates a frame dropped on a routed port because it was not addressed to the port.
	ReasonNotBridged trace.Reason = "not-bridged"
)

// Result records the trace steps, egress interface, outcome reason, and egress frame
// produced by layer 3 routing or packet origination.
type Result struct {
	Steps     []trace.Step
	Reason    trace.Reason
	Interface string
	Frame     ethernet.Frame
}

type routeKind string

const (
	routeConnected routeKind = "connected"
	routeStatic    routeKind = "static"
)

type routeEntry struct {
	Prefix    netip.Prefix
	NextHop   netip.Addr
	Interface string
	kind      routeKind
}

type neighborKey struct {
	iface string
	addr  netip.Addr
}

type vrfState struct {
	name       string
	table      []routeEntry
	localAddrs map[netip.Addr]struct{}
	neighbors  map[neighborKey]Neighbor
	interfaces map[string]Interface
}

// Layer executes layer 3 routing decisions over plain configuration values
// and Ethernet frames, maintaining per-VRF forwarding and neighbor tables.
//
// A Layer is safe for concurrent use.
type Layer struct {
	byVLAN   map[vlan.ID]string
	byPort   map[string]string
	ifaceVRF map[string]string
	ifaces   map[string]Interface
	vrfs     map[string]*vrfState
}

// New constructs a [Layer] from the provided configuration.
//
// Per VRF the forwarding table contains connected routes derived from each interface
// prefix and configured static routes, sorted by prefix length descending then by prefix,
// with connected routes listed first at equal length. Lookup selects the first match; a
// static route on a prefix a connected route also covers wins nothing.
func New(cfg Config) *Layer {
	cloned := cfg.Clone()

	l := &Layer{
		byVLAN:   make(map[vlan.ID]string),
		byPort:   make(map[string]string),
		ifaceVRF: make(map[string]string),
		ifaces:   make(map[string]Interface),
		vrfs:     make(map[string]*vrfState, len(cloned.VRFs)),
	}

	for vrfName, vrf := range cloned.VRFs {
		vs := &vrfState{
			name:       vrfName,
			localAddrs: make(map[netip.Addr]struct{}),
			neighbors:  make(map[neighborKey]Neighbor, len(vrf.Neighbors)),
			interfaces: vrf.Interfaces,
		}

		ifaceNames := make([]string, 0, len(vrf.Interfaces))
		for name := range vrf.Interfaces {
			ifaceNames = append(ifaceNames, name)
		}
		slices.Sort(ifaceNames)

		for _, name := range ifaceNames {
			iface := vrf.Interfaces[name]
			l.ifaces[name] = iface
			l.ifaceVRF[name] = vrfName
			if iface.VLAN != 0 {
				l.byVLAN[iface.VLAN] = name
			}
			if iface.Port != "" {
				l.byPort[iface.Port] = name
			}

			for _, p := range iface.Prefixes {
				vs.localAddrs[p.Addr()] = struct{}{}
				vs.table = append(vs.table, routeEntry{
					Prefix:    p.Masked(),
					Interface: name,
					kind:      routeConnected,
				})
			}
		}

		for _, r := range vrf.Routes {
			egressIface := r.Interface
			if egressIface == "" {
				for _, name := range ifaceNames {
					iface := vrf.Interfaces[name]
					for _, p := range iface.Prefixes {
						if p.Contains(r.NextHop) {
							egressIface = name
							break
						}
					}
					if egressIface != "" {
						break
					}
				}
			}
			vs.table = append(vs.table, routeEntry{
				Prefix:    r.Prefix,
				NextHop:   r.NextHop,
				Interface: egressIface,
				kind:      routeStatic,
			})
		}

		slices.SortFunc(vs.table, func(a, b routeEntry) int {
			if a.Prefix.Bits() != b.Prefix.Bits() {
				return cmp.Compare(b.Prefix.Bits(), a.Prefix.Bits())
			}
			if c := a.Prefix.Addr().Compare(b.Prefix.Addr()); c != 0 {
				return c
			}
			if a.kind != b.kind {
				if a.kind == routeConnected {
					return -1
				}
				return 1
			}
			return cmp.Compare(a.Interface, b.Interface)
		})

		for _, n := range vrf.Neighbors {
			vs.neighbors[neighborKey{iface: n.Interface, addr: n.Addr}] = n
		}

		l.vrfs[vrfName] = vs
	}

	return l
}

// ByVLAN returns the name of the routed interface associated with the given VLAN identifier.
func (l *Layer) ByVLAN(vid vlan.ID) (string, bool) {
	name, ok := l.byVLAN[vid]
	return name, ok
}

// ByPort returns the name of the routed interface associated with the given port.
func (l *Layer) ByPort(port string) (string, bool) {
	name, ok := l.byPort[port]
	return name, ok
}

// Owns reports whether f is addressed to the named interface's MAC address and carries
// an IPv4 or IPv6 payload.
func (l *Layer) Owns(iface string, f ethernet.Frame) bool {
	ifObj, ok := l.ifaces[iface]
	if !ok {
		return false
	}
	if f.Dst != ifObj.MAC {
		return false
	}
	return f.EtherType == ethernet.EtherTypeIPv4 || f.EtherType == ethernet.EtherTypeIPv6
}

// Interface returns the configured interface by name.
func (l *Layer) Interface(name string) (Interface, bool) {
	iface, ok := l.ifaces[name]
	return iface, ok
}

// Route routes an incoming Ethernet frame arriving on iface through layer 3 processing,
// updating hop limit, IP checksum, and link-layer addressing.
//
// If the packet header fails to decode, Route drops the frame with [ReasonBadHeader]
// (RFC 1812 section 5.2.2). If the destination matches an interface address of the VRF,
// Route marks the frame [ReasonNotRouted]. If the hop limit is 1 or less, Route drops
// the frame with [ReasonTTLExpired] (RFC 1812 section 5.3.1). If no route matches the
// destination, Route drops with [ReasonNoRoute] (RFC 1812 section 5.2.4.3). If neighbor
// address resolution fails, Route drops with [ReasonNeighborMiss].
//
// Route never mutates f; the returned Result carries a newly constructed frame and payload.
func (l *Layer) Route(iface string, f ethernet.Frame) Result {
	vrfName, ok := l.ifaceVRF[iface]
	if !ok {
		return Result{
			Steps: []trace.Step{
				{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: fmt.Sprintf("interface %s", iface)},
				{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "no route"},
				{Layer: port.LayerRouting, Op: trace.OpDrop, Detail: fmt.Sprintf("unknown interface %s", iface)},
			},
			Reason: ReasonNoRoute,
		}
	}

	vrf := l.vrfs[vrfName]
	var res Result
	res.Steps = append(res.Steps, trace.Step{
		Layer:  port.LayerRouting,
		Op:     trace.OpClassify,
		Detail: fmt.Sprintf("vrf %s interface %s", vrfName, iface),
	})

	hdr, payload, err := ip.Decode(f.Payload)
	// The egress frame keeps the EtherType, so a header whose family the
	// EtherType does not name would leave with a label no receiver can parse.
	if err == nil && (hdr.Version() == 4) != (f.EtherType == ethernet.EtherTypeIPv4) {
		err = errs.New().Attr("field", "version").Msg("IP version disagrees with the frame's EtherType")
	}
	if err != nil {
		res.Reason = ReasonBadHeader
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRouting,
			Op:     trace.OpDrop,
			Detail: string(ReasonBadHeader),
		})
		return res
	}

	if _, isLocal := vrf.localAddrs[hdr.Dst]; isLocal {
		res.Reason = ReasonNotRouted
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRouting,
			Op:     trace.OpLookup,
			Detail: hdr.Dst.String(),
		})
		return res
	}

	if hdr.HopLimit <= 1 {
		res.Reason = ReasonTTLExpired
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRouting,
			Op:     trace.OpDrop,
			Detail: string(ReasonTTLExpired),
		})
		return res
	}

	var matchedRoute *routeEntry
	for i := range vrf.table {
		if vrf.table[i].Prefix.Contains(hdr.Dst) {
			matchedRoute = &vrf.table[i]
			break
		}
	}

	if matchedRoute == nil {
		res.Reason = ReasonNoRoute
		res.Steps = append(res.Steps,
			trace.Step{
				Layer:  port.LayerRouting,
				Op:     trace.OpLookup,
				Detail: "no route",
			},
			trace.Step{
				Layer:  port.LayerRouting,
				Op:     trace.OpDrop,
				Detail: fmt.Sprintf("vrf %s", vrfName),
			},
		)
		return res
	}

	lookupDetail := fmt.Sprintf("%s %s %s", matchedRoute.Prefix, matchedRoute.kind, matchedRoute.Interface)
	res.Steps = append(res.Steps, trace.Step{
		Layer:  port.LayerRouting,
		Op:     trace.OpLookup,
		Detail: lookupDetail,
	})

	targetAddr := hdr.Dst
	if matchedRoute.NextHop.IsValid() {
		targetAddr = matchedRoute.NextHop
	}

	targetIface := matchedRoute.Interface
	neighbor, ok := vrf.neighbors[neighborKey{iface: targetIface, addr: targetAddr}]
	if !ok {
		res.Reason = ReasonNeighborMiss
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRouting,
			Op:     trace.OpDrop,
			Detail: fmt.Sprintf("vrf %s interface %s address %s", vrfName, targetIface, targetAddr),
		})
		return res
	}

	egressIfaceObj := l.ifaces[targetIface]
	egressMAC := egressIfaceObj.MAC

	res.Steps = append(res.Steps, trace.Step{
		Layer:  port.LayerRouting,
		Op:     trace.OpRewrite,
		Detail: fmt.Sprintf("hop limit %d to %d, src %s, dst %s", hdr.HopLimit, hdr.HopLimit-1, egressMAC, neighbor.MAC),
	})

	newHdr := hdr
	newHdr.HopLimit = hdr.HopLimit - 1
	newPayload, err := newHdr.Encode(payload)
	if err != nil {
		res.Reason = ReasonBadHeader
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRouting,
			Op:     trace.OpDrop,
			Detail: string(ReasonBadHeader),
		})
		return res
	}

	res.Interface = targetIface
	res.Frame = ethernet.Frame{
		Src:       egressMAC,
		Dst:       neighbor.MAC,
		EtherType: f.EtherType,
		Payload:   newPayload,
	}
	return res
}

// Originate builds and encapsulates an IP datagram generated by the device itself
// for transmission towards dst within the named VRF.
//
// The source address is chosen among the VRF's interface addresses matching the destination
// family by longest matching prefix (RFC 6724 section 5 rule 8; RFC 1122 section 3.3.4.3),
// falling back to the first configured address of that family. The datagram transmits with
// hop limit 64 direct on connected prefixes and via gateway otherwise (RFC 1122 section 3.3.1.1).
func (l *Layer) Originate(vrf string, dst netip.Addr, protocol uint8, payload []byte) Result {
	vrfState, ok := l.vrfs[vrf]
	if !ok {
		return Result{
			Steps: []trace.Step{
				{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "no route"},
				{Layer: port.LayerRouting, Op: trace.OpDrop, Detail: fmt.Sprintf("vrf %s", vrf)},
			},
			Reason: ReasonNoRoute,
		}
	}

	ifaceNames := make([]string, 0, len(vrfState.interfaces))
	for name := range vrfState.interfaces {
		ifaceNames = append(ifaceNames, name)
	}
	slices.Sort(ifaceNames)

	var (
		srcAddr   netip.Addr
		bestBits  = -1
		firstAddr netip.Addr
	)

	for _, name := range ifaceNames {
		iface := vrfState.interfaces[name]
		for _, p := range iface.Prefixes {
			if (dst.Is4() && p.Addr().Is4()) || (dst.Is6() && p.Addr().Is6()) {
				if !firstAddr.IsValid() {
					firstAddr = p.Addr()
				}
				if p.Contains(dst) && p.Bits() > bestBits {
					bestBits = p.Bits()
					srcAddr = p.Addr()
				}
			}
		}
	}

	if !srcAddr.IsValid() {
		srcAddr = firstAddr
	}
	if !srcAddr.IsValid() {
		return Result{
			Steps: []trace.Step{
				{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "no route"},
				{Layer: port.LayerRouting, Op: trace.OpDrop, Detail: fmt.Sprintf("vrf %s", vrf)},
			},
			Reason: ReasonNoRoute,
		}
	}

	var matchedRoute *routeEntry
	for i := range vrfState.table {
		if vrfState.table[i].Prefix.Contains(dst) {
			matchedRoute = &vrfState.table[i]
			break
		}
	}

	if matchedRoute == nil {
		return Result{
			Steps: []trace.Step{
				{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "no route"},
				{Layer: port.LayerRouting, Op: trace.OpDrop, Detail: fmt.Sprintf("vrf %s", vrf)},
			},
			Reason: ReasonNoRoute,
		}
	}

	var steps []trace.Step
	lookupDetail := fmt.Sprintf("%s %s %s", matchedRoute.Prefix, matchedRoute.kind, matchedRoute.Interface)
	steps = append(steps, trace.Step{
		Layer:  port.LayerRouting,
		Op:     trace.OpLookup,
		Detail: lookupDetail,
	})

	targetAddr := dst
	if matchedRoute.NextHop.IsValid() {
		targetAddr = matchedRoute.NextHop
	}

	targetIface := matchedRoute.Interface
	neighbor, ok := vrfState.neighbors[neighborKey{iface: targetIface, addr: targetAddr}]
	if !ok {
		return Result{
			Steps: append(steps, trace.Step{
				Layer:  port.LayerRouting,
				Op:     trace.OpDrop,
				Detail: fmt.Sprintf("vrf %s interface %s address %s", vrf, targetIface, targetAddr),
			}),
			Reason: ReasonNeighborMiss,
		}
	}

	hdr := ip.Header{
		Src:      srcAddr,
		Dst:      dst,
		HopLimit: 64,
		Protocol: protocol,
	}
	var etherType ethernet.EtherType
	if dst.Is4() {
		hdr.V4 = &ip.V4{}
		etherType = ethernet.EtherTypeIPv4
	} else {
		hdr.V6 = &ip.V6{}
		etherType = ethernet.EtherTypeIPv6
	}

	pktBytes, err := hdr.Encode(payload)
	if err != nil {
		return Result{
			Steps: append(steps, trace.Step{
				Layer:  port.LayerRouting,
				Op:     trace.OpDrop,
				Detail: string(ReasonBadHeader),
			}),
			Reason: ReasonBadHeader,
		}
	}

	egressIfaceObj := l.ifaces[targetIface]
	return Result{
		Steps:     steps,
		Interface: targetIface,
		Frame: ethernet.Frame{
			Src:       egressIfaceObj.MAC,
			Dst:       neighbor.MAC,
			EtherType: etherType,
			Payload:   pktBytes,
		},
	}
}
