package routing

import (
	"cmp"
	"net/netip"
	"slices"
	"strconv"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// VRFScope returns the construction metadata scope for one routing table.
func VRFScope(nodeID, vrf string) analysis.Scope {
	return analysis.ProtocolScope(nodeID, string(port.LayerRouting), vrf)
}

// PortLookupScope returns the exact scope for resolving a routed interface by port.
func PortLookupScope(nodeID, vrf, name string) analysis.Scope {
	return analysis.FieldScope(VRFScope(nodeID, vrf), "ports", name)
}

// VLANLookupScope returns the exact scope for resolving a routed interface by VLAN.
func VLANLookupScope(nodeID, vrf string, vid vlan.ID) analysis.Scope {
	return analysis.FieldScope(VRFScope(nodeID, vrf), "vlans", strconv.Itoa(int(vid)))
}

// OwnershipScope returns the exact scope for deciding whether an interface owns a frame.
func OwnershipScope(nodeID, vrf, iface string) analysis.Scope {
	return analysis.FieldScope(VRFScope(nodeID, vrf), "interfaces", iface, "ownership")
}

// RouteLookupScope returns the exact scope for a destination lookup in a VRF route table.
func RouteLookupScope(nodeID, vrf string, dst netip.Addr) analysis.Scope {
	return analysis.FieldScope(VRFScope(nodeID, vrf), "routes", dst.String())
}

// LocalAddressLookupScope returns the exact scope for a local-destination lookup in a VRF.
func LocalAddressLookupScope(nodeID, vrf string, dst netip.Addr) analysis.Scope {
	return analysis.FieldScope(VRFScope(nodeID, vrf), "local_addresses", dst.String())
}

// NeighborTableScope returns the scope for neighbor records on one routed interface.
func NeighborTableScope(nodeID, vrf, iface string) analysis.Scope {
	return analysis.FieldScope(VRFScope(nodeID, vrf), "interfaces", iface, "neighbors")
}

// NeighborLookupScope returns the exact scope for resolving a neighbor by interface and address.
func NeighborLookupScope(nodeID, vrf, iface string, addr netip.Addr) analysis.Scope {
	return analysis.FieldScope(VRFScope(nodeID, vrf), "interfaces", iface, "neighbors", addr.String())
}

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

// Result records the trace steps, egress interface, outcome reason, egress frame, and
// equal-cost candidate set produced by layer 3 routing or packet origination.
type Result struct {
	Steps           []trace.Step
	Reason          trace.Reason
	Interface       string
	Candidates      []Candidate
	Frame           ethernet.Frame
	consultedScopes []analysis.Scope
}

// Candidate is one route of the equal-cost set a lookup chose from: the routes whose prefix
// contains the destination and which tie the winner on prefix length, preference, and metric.
// A lookup reports them in canonical order, by next hop then egress interface, and forwards
// on the first. Interface is the egress the route resolved to, which for a route configured
// with a next hop alone is the interface whose prefix contains that next hop.
type Candidate struct {
	Prefix     netip.Prefix
	NextHop    netip.Addr
	Interface  string
	Preference uint8
	Metric     uint32
}

// ConsultedScopes returns the exact analysis scopes whose facts could change
// this routing result, in canonical order.
func (r Result) ConsultedScopes() []analysis.Scope {
	return slices.Clone(r.consultedScopes)
}

func (r *Result) consult(scope analysis.Scope) {
	r.consultedScopes = append(r.consultedScopes, scope)
	slices.SortFunc(r.consultedScopes, func(a, b analysis.Scope) int { return a.Compare(b) })
	r.consultedScopes = slices.CompactFunc(r.consultedScopes, func(a, b analysis.Scope) bool { return a.Compare(b) == 0 })
}

type routeKind string

const (
	routeConnected routeKind = "connected"
	routeStatic    routeKind = "static"
)

type routeEntry struct {
	Prefix     netip.Prefix
	NextHop    netip.Addr
	Interface  string
	Preference uint8
	Metric     uint32
	kind       routeKind
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
	nodeID   string
	byVLAN   map[vlan.ID]string
	byPort   map[string]string
	ifaceVRF map[string]string
	ifaces   map[string]Interface
	vrfs     map[string]*vrfState
}

// New normalizes and constructs a [Layer] from the provided configuration and
// port table. It returns an error if the normalized configuration is invalid
// against the ports.
//
// Per VRF the forwarding table contains connected routes derived from each interface prefix,
// at preference 0 and metric 0, and the configured static routes. A lookup takes the longest
// matching prefix, then the lowest preference, then the lowest metric; every route tying on
// all three is an equal-cost candidate, and the candidates are ordered by next hop then
// egress interface. The lookup forwards on the first of them.
func New(cfg Config, ports port.Table, nodeID string) (*Layer, error) {
	norm := cfg.Normalize()
	if err := norm.Validate(ports); err != nil {
		return nil, err
	}
	return newLayer(norm, nodeID), nil
}

func newLayer(cfg Config, nodeID string) *Layer {
	cloned := cfg.Clone()

	l := &Layer{
		nodeID:   nodeID,
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
				Prefix:     r.Prefix,
				NextHop:    r.NextHop,
				Interface:  egressIface,
				Preference: r.Preference,
				Metric:     r.Metric,
				kind:       routeStatic,
			})
		}

		slices.SortFunc(vs.table, func(a, b routeEntry) int {
			if a.Prefix.Bits() != b.Prefix.Bits() {
				return cmp.Compare(b.Prefix.Bits(), a.Prefix.Bits())
			}
			if c := a.Prefix.Addr().Compare(b.Prefix.Addr()); c != 0 {
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

		for _, n := range vrf.Neighbors {
			vs.neighbors[neighborKey{iface: n.Interface, addr: n.Addr}] = n
		}

		l.vrfs[vrfName] = vs
	}

	return l
}

// lookup returns the equal-cost candidates for dst in canonical order, or nil when no route
// covers it. The table is sorted so that the entries sharing the winner's prefix, preference,
// and metric follow it directly; an equal-length prefix that does not contain dst is a
// different prefix and so never joins the set.
func (vs *vrfState) lookup(dst netip.Addr) []routeEntry {
	for i := range vs.table {
		if !vs.table[i].Prefix.Contains(dst) {
			continue
		}
		best := vs.table[i]
		end := i + 1
		for end < len(vs.table) &&
			vs.table[end].Prefix == best.Prefix &&
			vs.table[end].Preference == best.Preference &&
			vs.table[end].Metric == best.Metric {
			end++
		}
		return vs.table[i:end]
	}
	return nil
}

func candidateSet(entries []routeEntry) []Candidate {
	set := make([]Candidate, len(entries))
	for i, e := range entries {
		set[i] = Candidate{
			Prefix:     e.Prefix,
			NextHop:    e.NextHop,
			Interface:  e.Interface,
			Preference: e.Preference,
			Metric:     e.Metric,
		}
	}
	return set
}

func (l *Layer) result(vrf string) Result {
	var result Result
	result.consult(analysis.ProtocolScope(l.nodeID, string(port.LayerRouting), vrf))
	return result
}

// ByVLAN returns the name of the routed interface associated with the given VLAN identifier.
func (l *Layer) ByVLAN(vid vlan.ID) (string, bool) {
	name, ok := l.byVLAN[vid]
	return name, ok
}

// VLANLookupScopes returns the exact VRF scopes consulted by [Layer.ByVLAN].
func (l *Layer) VLANLookupScopes(vid vlan.ID) []analysis.Scope {
	if iface, ok := l.byVLAN[vid]; ok {
		return []analysis.Scope{VLANLookupScope(l.nodeID, l.ifaceVRF[iface], vid)}
	}

	vrfs := make([]string, 0, len(l.vrfs))
	for vrf := range l.vrfs {
		vrfs = append(vrfs, vrf)
	}
	slices.Sort(vrfs)
	scopes := make([]analysis.Scope, len(vrfs))
	for i, vrf := range vrfs {
		scopes[i] = VLANLookupScope(l.nodeID, vrf, vid)
	}
	return scopes
}

// ByPort returns the name of the routed interface associated with the given port.
func (l *Layer) ByPort(port string) (string, bool) {
	name, ok := l.byPort[port]
	return name, ok
}

// PortLookupScopes returns the exact VRF scopes consulted by [Layer.ByPort].
func (l *Layer) PortLookupScopes(name string) []analysis.Scope {
	if iface, ok := l.byPort[name]; ok {
		return []analysis.Scope{PortLookupScope(l.nodeID, l.ifaceVRF[iface], name)}
	}

	vrfs := make([]string, 0, len(l.vrfs))
	for vrf := range l.vrfs {
		vrfs = append(vrfs, vrf)
	}
	slices.Sort(vrfs)
	scopes := make([]analysis.Scope, len(vrfs))
	for i, vrf := range vrfs {
		scopes[i] = PortLookupScope(l.nodeID, vrf, name)
	}
	return scopes
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

// InterfaceOwnershipScope returns the exact scope consulted by [Layer.Owns].
func (l *Layer) InterfaceOwnershipScope(iface string) analysis.Scope {
	vrf, ok := l.ifaceVRF[iface]
	if !ok {
		return analysis.FieldScope(analysis.NodeScope(l.nodeID), "routing", "interfaces", iface, "ownership")
	}
	return OwnershipScope(l.nodeID, vrf, iface)
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
				{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: trace.RuleID("classify"), Subject: trace.Subject{Kind: "interface", Key: iface}, Outputs: []trace.Fact{packetSnapshot(iface, f, ip.Header{}, false, ReasonNoRoute)}},
				{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: trace.RuleID("no-route"), Subject: trace.Subject{Kind: "interface", Key: iface}, Outputs: []trace.Fact{routeSnapshot("", netip.Addr{}, nil)}},
				{Layer: port.LayerRouting, Op: trace.OpDrop, RuleID: trace.RuleID("unknown-interface"), Subject: trace.Subject{Kind: "interface", Key: iface}, Outputs: []trace.Fact{packetSnapshot(iface, f, ip.Header{}, false, ReasonNoRoute)}},
			},
			Reason: ReasonNoRoute,
		}
	}

	vrf := l.vrfs[vrfName]
	hdr, payload, err := ip.Decode(f.Payload)
	// The egress frame keeps the EtherType, so a header whose family the
	// EtherType does not name would leave with a label no receiver can parse.
	if err == nil && (hdr.Version() == 4) != (f.EtherType == ethernet.EtherTypeIPv4) {
		err = errs.New().Attr("field", "version").Msg("IP version disagrees with the frame's EtherType")
	}
	classifyReason := trace.Reason("")
	if err != nil {
		classifyReason = ReasonBadHeader
	}
	var res Result
	res.Steps = append(res.Steps, trace.Step{
		Layer:   port.LayerRouting,
		Op:      trace.OpClassify,
		RuleID:  trace.RuleID("classify"),
		Subject: trace.Subject{Kind: "interface", Key: iface},
		Inputs:  []trace.Fact{packetSnapshot(iface, f, hdr, err == nil, classifyReason)},
		Outputs: []trace.Fact{RouteInterfaceFact(iface)},
	})

	if err != nil {
		res.Reason = ReasonBadHeader
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRouting,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(ReasonBadHeader),
			Subject: trace.Subject{Kind: "interface", Key: iface},
			Outputs: []trace.Fact{packetSnapshot(iface, f, hdr, false, ReasonBadHeader)},
		})
		return res
	}
	res.consult(LocalAddressLookupScope(l.nodeID, vrfName, hdr.Dst))

	if _, isLocal := vrf.localAddrs[hdr.Dst]; isLocal {
		res.Reason = ReasonNotRouted
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRouting,
			Op:      trace.OpLookup,
			RuleID:  trace.RuleID("local-delivery"),
			Subject: trace.Subject{Kind: "ip", Key: hdr.Dst.String()},
			Inputs:  []trace.Fact{packetSnapshot(iface, f, hdr, true, "")},
			Outputs: []trace.Fact{routeSnapshot(vrfName, hdr.Dst, nil)},
		})
		return res
	}

	if hdr.HopLimit <= 1 {
		res.Reason = ReasonTTLExpired
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRouting,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(ReasonTTLExpired),
			Subject: trace.Subject{Kind: "ip", Key: hdr.Dst.String()},
			Outputs: []trace.Fact{packetSnapshot(iface, f, hdr, false, ReasonTTLExpired)},
		})
		return res
	}

	res.consult(RouteLookupScope(l.nodeID, vrfName, hdr.Dst))
	candidates := vrf.lookup(hdr.Dst)
	var matchedRoute *routeEntry
	if len(candidates) > 0 {
		res.Candidates = candidateSet(candidates)
		matchedRoute = &candidates[0]
	}

	if matchedRoute == nil {
		res.Reason = ReasonNoRoute
		res.Steps = append(res.Steps,
			trace.Step{
				Layer:   port.LayerRouting,
				Op:      trace.OpLookup,
				RuleID:  trace.RuleID("no-route"),
				Subject: trace.Subject{Kind: "ip", Key: hdr.Dst.String()},
				Inputs:  []trace.Fact{packetSnapshot(iface, f, hdr, true, "")},
				Outputs: []trace.Fact{routeSnapshot(vrfName, hdr.Dst, nil)},
			},
			trace.Step{
				Layer:   port.LayerRouting,
				Op:      trace.OpDrop,
				RuleID:  trace.RuleID("no-route"),
				Subject: trace.Subject{Kind: "vrf", Key: vrfName},
				Outputs: []trace.Fact{packetSnapshot(iface, f, hdr, false, ReasonNoRoute)},
			},
		)
		return res
	}

	res.Steps = append(res.Steps, trace.Step{
		Layer:   port.LayerRouting,
		Op:      trace.OpLookup,
		RuleID:  trace.RuleID(matchedRoute.kind),
		Subject: trace.Subject{Kind: "prefix", Key: matchedRoute.Prefix.String()},
		Inputs:  []trace.Fact{packetSnapshot(iface, f, hdr, true, "")},
		Outputs: []trace.Fact{routeSnapshot(vrfName, hdr.Dst, matchedRoute)},
	})

	targetAddr := hdr.Dst
	if matchedRoute.NextHop.IsValid() {
		targetAddr = matchedRoute.NextHop
	}

	targetIface := matchedRoute.Interface
	res.Interface = targetIface
	res.consult(NeighborLookupScope(l.nodeID, vrfName, targetIface, targetAddr))
	neighbor, ok := vrf.neighbors[neighborKey{iface: targetIface, addr: targetAddr}]
	if !ok {
		res.Reason = ReasonNeighborMiss
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRouting,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(ReasonNeighborMiss),
			Subject: trace.Subject{Kind: "ip", Key: targetAddr.String()},
			Inputs:  []trace.Fact{routeSnapshot(vrfName, hdr.Dst, matchedRoute)},
			Outputs: []trace.Fact{neighborSnapshot(targetIface, targetAddr, Neighbor{}, false)},
		})
		return res
	}

	egressIfaceObj := l.ifaces[targetIface]
	egressMAC := egressIfaceObj.MAC

	newHdr := hdr
	newHdr.HopLimit = hdr.HopLimit - 1
	res.Steps = append(res.Steps, trace.Step{
		Layer:   port.LayerRouting,
		Op:      trace.OpRewrite,
		RuleID:  trace.RuleID("decrement-ttl"),
		Subject: trace.Subject{Kind: "interface", Key: targetIface},
		Inputs:  []trace.Fact{packetSnapshot(iface, f, hdr, true, ""), neighborSnapshot(targetIface, targetAddr, neighbor, true)},
		Outputs: []trace.Fact{packetSnapshot(targetIface, f, newHdr, true, "")},
	})

	newPayload, err := newHdr.Encode(payload)
	if err != nil {
		res.Reason = ReasonBadHeader
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRouting,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(ReasonBadHeader),
			Subject: trace.Subject{Kind: "interface", Key: targetIface},
			Inputs:  []trace.Fact{packetSnapshot(targetIface, f, newHdr, true, "")},
			Outputs: []trace.Fact{packetSnapshot(targetIface, f, newHdr, false, ReasonBadHeader)},
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
		res := l.result(vrf)
		res.Steps = []trace.Step{
			{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: trace.RuleID("no-route"), Subject: trace.Subject{Kind: "vrf", Key: vrf}, Outputs: []trace.Fact{routeSnapshot(vrf, dst, nil)}},
			{Layer: port.LayerRouting, Op: trace.OpDrop, RuleID: trace.RuleID("no-route"), Subject: trace.Subject{Kind: "vrf", Key: vrf}, Outputs: []trace.Fact{packetSnapshot("", ethernet.Frame{}, ip.Header{Dst: dst}, false, ReasonNoRoute)}},
		}
		res.Reason = ReasonNoRoute
		return res
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
		res := l.result(vrf)
		res.Steps = []trace.Step{
			{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: trace.RuleID("no-route"), Subject: trace.Subject{Kind: "vrf", Key: vrf}, Outputs: []trace.Fact{routeSnapshot(vrf, dst, nil)}},
			{Layer: port.LayerRouting, Op: trace.OpDrop, RuleID: trace.RuleID("no-route"), Subject: trace.Subject{Kind: "vrf", Key: vrf}, Outputs: []trace.Fact{packetSnapshot("", ethernet.Frame{}, ip.Header{Dst: dst}, false, ReasonNoRoute)}},
		}
		res.Reason = ReasonNoRoute
		return res
	}

	candidates := vrfState.lookup(dst)
	var matchedRoute *routeEntry
	if len(candidates) > 0 {
		matchedRoute = &candidates[0]
	}

	if matchedRoute == nil {
		res := l.result(vrf)
		res.Steps = []trace.Step{
			{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: trace.RuleID("no-route"), Subject: trace.Subject{Kind: "ip", Key: dst.String()}, Outputs: []trace.Fact{routeSnapshot(vrf, dst, nil)}},
			{Layer: port.LayerRouting, Op: trace.OpDrop, RuleID: trace.RuleID("no-route"), Subject: trace.Subject{Kind: "vrf", Key: vrf}, Outputs: []trace.Fact{packetSnapshot("", ethernet.Frame{}, ip.Header{Src: srcAddr, Dst: dst}, false, ReasonNoRoute)}},
		}
		res.Reason = ReasonNoRoute
		return res
	}

	var steps []trace.Step
	steps = append(steps, trace.Step{
		Layer:   port.LayerRouting,
		Op:      trace.OpLookup,
		RuleID:  trace.RuleID(matchedRoute.kind),
		Subject: trace.Subject{Kind: "prefix", Key: matchedRoute.Prefix.String()},
		Inputs:  []trace.Fact{packetSnapshot("", ethernet.Frame{}, ip.Header{Src: srcAddr, Dst: dst, HopLimit: 64}, true, "")},
		Outputs: []trace.Fact{routeSnapshot(vrf, dst, matchedRoute)},
	})

	targetAddr := dst
	if matchedRoute.NextHop.IsValid() {
		targetAddr = matchedRoute.NextHop
	}

	targetIface := matchedRoute.Interface
	neighbor, ok := vrfState.neighbors[neighborKey{iface: targetIface, addr: targetAddr}]
	if !ok {
		res := l.result(vrf)
		res.Steps = slices.Clone(steps)
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRouting,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(ReasonNeighborMiss),
			Subject: trace.Subject{Kind: "ip", Key: targetAddr.String()},
			Inputs:  []trace.Fact{routeSnapshot(vrf, dst, matchedRoute)},
			Outputs: []trace.Fact{neighborSnapshot(targetIface, targetAddr, Neighbor{}, false)},
		})
		res.Reason = ReasonNeighborMiss
		res.Interface = targetIface
		res.Candidates = candidateSet(candidates)
		return res
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
	steps[len(steps)-1].Outputs = append(steps[len(steps)-1].Outputs, neighborSnapshot(targetIface, targetAddr, neighbor, true))

	pktBytes, err := hdr.Encode(payload)
	if err != nil {
		res := l.result(vrf)
		res.Steps = slices.Clone(steps)
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRouting,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(ReasonBadHeader),
			Subject: trace.Subject{Kind: "interface", Key: targetIface},
			Inputs:  []trace.Fact{packetSnapshot(targetIface, ethernet.Frame{EtherType: etherType}, hdr, true, ""), neighborSnapshot(targetIface, targetAddr, neighbor, true)},
			Outputs: []trace.Fact{packetSnapshot(targetIface, ethernet.Frame{EtherType: etherType}, hdr, false, ReasonBadHeader)},
		})
		res.Reason = ReasonBadHeader
		res.Candidates = candidateSet(candidates)
		return res
	}

	egressIfaceObj := l.ifaces[targetIface]
	res := l.result(vrf)
	res.Steps = steps
	res.Interface = targetIface
	res.Candidates = candidateSet(candidates)
	res.Frame = ethernet.Frame{
		Src:       egressIfaceObj.MAC,
		Dst:       neighbor.MAC,
		EtherType: etherType,
		Payload:   pktBytes,
	}
	return res
}
