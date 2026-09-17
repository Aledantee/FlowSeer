package routing

import (
	"cmp"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
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

	// ReasonNeighborMiss indicates a frame dropped because the VRF does not resolve neighbors,
	// or because a prior resolution attempt for the next hop already failed.
	ReasonNeighborMiss trace.Reason = "neighbor-miss"

	// ReasonNeighborPending indicates a frame held because the next hop's neighbor entry is
	// newly or still unresolved under [NeighborObserved]; it is not a drop.
	ReasonNeighborPending trace.Reason = "neighbor-pending"

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
// on the one the packet's flow hash lands on. NextHop is the configured next hop; Interface
// is the egress the route resolved to, which for a route configured with a next hop alone is
// the interface the next hop is on-link on, whether directly or at the end of a chain of
// routes.
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

// routeEntry is one installed forwarding entry. NextHop is the configured next hop, which
// the facts and the candidate set report; resolvedNextHop is the on-link address the frame
// is actually built for, which for a recursive route is a different address entirely and for
// a route reaching its destination directly is not set at all. source indexes the configured
// route the entry came from, and is -1 for a connected route.
type routeEntry struct {
	Prefix          netip.Prefix
	NextHop         netip.Addr
	Interface       string
	Preference      uint8
	Metric          uint32
	resolvedNextHop netip.Addr
	source          int
	kind            routeKind
}

// WithdrawalReason names why a configured static route is absent from the forwarding table.
type WithdrawalReason string

const (
	// WithdrawnSelfRecursive indicates resolution reached the route being resolved.
	WithdrawnSelfRecursive WithdrawalReason = "self-recursive"

	// WithdrawnDepthExceeded indicates resolution walked more routes than [maxRecursionDepth].
	WithdrawnDepthExceeded WithdrawalReason = "depth-exceeded"

	// WithdrawnUnresolved indicates the next hop matched no route, or only a default route.
	WithdrawnUnresolved WithdrawalReason = "unresolved"

	// WithdrawnMaxPaths indicates the route resolved past the equal-cost cap of [maxCandidatePaths].
	WithdrawnMaxPaths WithdrawalReason = "max-paths"
)

// WithdrawnRoute records a configured static route the forwarding table does not hold,
// with the prefixes resolution walked before it gave up. Prefix, NextHop, and Interface
// are the configured values; Chain begins with Prefix and names each route resolution
// entered, ending at the one it could not leave.
type WithdrawnRoute struct {
	Prefix    netip.Prefix
	NextHop   netip.Addr
	Interface string
	Reason    WithdrawalReason
	Chain     []netip.Prefix
}

const (
	// maxRecursionDepth bounds how many static routes one next-hop resolution walks.
	// Vendors name a maximum forwarding recursion depth without publishing a value, and
	// BIRD allows a single level; eight is netsim's own number, high enough that an
	// ordinary two-step chain resolves and low enough to catch a configuration mistake.
	maxRecursionDepth = 8

	// maxCandidatePaths bounds how many equal-cost next hops one configured route installs.
	// FRR compiles a 64-way limit and netsim follows it rather than installing a set no
	// router would carry.
	maxCandidatePaths = 64
)

type neighborKey struct {
	iface string
	addr  netip.Addr
}

type vrfState struct {
	name       string
	table      []routeEntry
	withdrawn  []WithdrawnRoute
	localAddrs map[netip.Addr]struct{}
	neighbors  map[neighborKey]*neighborEntry
	interfaces map[string]Interface
	policy     NeighborPolicy
}

func (vs *vrfState) clone() *vrfState {
	cp := &vrfState{
		name:       vs.name,
		table:      slices.Clone(vs.table),
		localAddrs: make(map[netip.Addr]struct{}, len(vs.localAddrs)),
		neighbors:  make(map[neighborKey]*neighborEntry, len(vs.neighbors)),
		interfaces: make(map[string]Interface, len(vs.interfaces)),
		policy:     vs.policy,
	}
	for name, iface := range vs.interfaces {
		cp.interfaces[name] = Interface{VLAN: iface.VLAN, Port: iface.Port, MAC: iface.MAC, Prefixes: slices.Clone(iface.Prefixes)}
	}
	for i := range vs.withdrawn {
		cp.withdrawn = append(cp.withdrawn, WithdrawnRoute{
			Prefix:    vs.withdrawn[i].Prefix,
			NextHop:   vs.withdrawn[i].NextHop,
			Interface: vs.withdrawn[i].Interface,
			Reason:    vs.withdrawn[i].Reason,
			Chain:     slices.Clone(vs.withdrawn[i].Chain),
		})
	}
	for addr := range vs.localAddrs {
		cp.localAddrs[addr] = struct{}{}
	}
	for k, v := range vs.neighbors {
		cp.neighbors[k] = v.clone()
	}
	return cp
}

// Layer executes layer 3 routing decisions over plain configuration values
// and Ethernet frames, maintaining per-VRF forwarding and neighbor tables.
//
// A Layer is not safe for concurrent use: [Layer.Route] and [Layer.Originate] mutate the
// neighbor table and its hold queues when called with commit set, and [Layer.Observe],
// [Layer.Age], and [Layer.Wake] always do.
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
// egress interface, at most [maxCandidatePaths] of them. The packet takes the candidate
// whose RFC 2992 hash-threshold region holds its layer-3 flow hash, so one flow keeps one
// next hop and a path that goes moves as few of the others as the reduction allows.
//
// A static route whose next hop is not on-link resolves against its own VRF's table as the
// table is built, and installs carrying the on-link next hop and interface it reached. One
// that resolves to nothing is withdrawn rather than rejected, so forwarding answers from the
// routes that remain; [Layer.WithdrawnRoutes] reports why.
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
			neighbors:  make(map[neighborKey]*neighborEntry, len(vrf.Neighbors)),
			interfaces: vrf.Interfaces,
			policy:     vrf.NeighborPolicy,
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
					source:    -1,
					kind:      routeConnected,
				})
			}
		}

		vs.installRoutes(vrf.Routes)

		for _, n := range vrf.Neighbors {
			// A configured binding enters as Reachable with no expiry, so it never ages out
			// the way an observed one does.
			vs.neighbors[neighborKey{iface: n.Interface, addr: n.Addr}] = &neighborEntry{
				state:  NeighborReachable,
				mac:    n.MAC,
				origin: originConfigured,
			}
		}

		l.vrfs[vrfName] = vs
	}

	return l
}

// compareRouteEntries orders a forwarding table: longest prefix first, then by prefix
// address, preference, metric, configured next hop, egress interface, and finally the
// resolved next hop, which is the only field two inherited equal-cost members can differ in.
func compareRouteEntries(a, b routeEntry) int {
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
	if c := cmp.Compare(a.Interface, b.Interface); c != 0 {
		return c
	}
	return a.resolvedNextHop.Compare(b.resolvedNextHop)
}

// lookupIn returns the equal-cost candidates for dst in canonical order, or nil when no route
// in the sorted table covers it. The table is sorted so that the entries sharing the winner's
// prefix, preference, and metric follow it directly; an equal-length prefix that does not
// contain dst is a different prefix and so never joins the set.
func lookupIn(table []routeEntry, dst netip.Addr) []routeEntry {
	for i := range table {
		if !table[i].Prefix.Contains(dst) {
			continue
		}
		best := table[i]
		end := i + 1
		for end < len(table) &&
			table[end].Prefix == best.Prefix &&
			table[end].Preference == best.Preference &&
			table[end].Metric == best.Metric {
			end++
		}
		return table[i:end]
	}
	return nil
}

func (vs *vrfState) lookup(dst netip.Addr) []routeEntry {
	return lookupIn(vs.table, dst)
}

// resolvedHop is an on-link forwarding pair: the address a neighbor entry is looked up for,
// and the interface the frame leaves by.
type resolvedHop struct {
	nextHop netip.Addr
	iface   string
}

// installRoutes resolves the configured static routes against the VRF's own table and
// installs those reaching an on-link next hop, recording the rest as withdrawals. Resolution
// walks a table holding every configured route beside the connected ones, so a chain resolves
// whatever order its routes were configured in.
func (vs *vrfState) installRoutes(routes []Route) {
	resolution := slices.Clone(vs.table)
	for i, r := range routes {
		resolution = append(resolution, routeEntry{
			Prefix:     r.Prefix,
			NextHop:    r.NextHop,
			Interface:  r.Interface,
			Preference: r.Preference,
			Metric:     r.Metric,
			source:     i,
			kind:       routeStatic,
		})
	}
	slices.SortFunc(resolution, compareRouteEntries)

	for i, r := range routes {
		hops, chain, reason := resolveRoute(resolution, i, r)
		if reason != "" {
			vs.withdrawn = append(vs.withdrawn, WithdrawnRoute{
				Prefix:    r.Prefix,
				NextHop:   r.NextHop,
				Interface: r.Interface,
				Reason:    reason,
				Chain:     chain,
			})
			continue
		}
		for j, hop := range hops {
			if j >= maxCandidatePaths {
				vs.withdrawn = append(vs.withdrawn, WithdrawnRoute{
					Prefix:    r.Prefix,
					NextHop:   r.NextHop,
					Interface: hop.iface,
					Reason:    WithdrawnMaxPaths,
				})
				continue
			}
			vs.table = append(vs.table, routeEntry{
				Prefix:          r.Prefix,
				NextHop:         r.NextHop,
				Interface:       hop.iface,
				Preference:      r.Preference,
				Metric:          r.Metric,
				resolvedNextHop: hop.nextHop,
				source:          i,
				kind:            routeStatic,
			})
		}
	}

	slices.SortFunc(vs.table, compareRouteEntries)

	// One prefix carries at most maxCandidatePaths equal-cost routes, the first of them in
	// canonical order; the rest are withdrawn. The cap on the paths one recursive route
	// inherits, applied above, does not see the equal routes another configured route adds
	// to the same prefix.
	kept := make([]routeEntry, 0, len(vs.table))
	for start := 0; start < len(vs.table); {
		end := start + 1
		for end < len(vs.table) &&
			vs.table[end].Prefix == vs.table[start].Prefix &&
			vs.table[end].Preference == vs.table[start].Preference &&
			vs.table[end].Metric == vs.table[start].Metric {
			end++
		}
		for i := start; i < end; i++ {
			if i-start < maxCandidatePaths {
				kept = append(kept, vs.table[i])
				continue
			}
			vs.withdrawn = append(vs.withdrawn, WithdrawnRoute{
				Prefix:    vs.table[i].Prefix,
				NextHop:   vs.table[i].NextHop,
				Interface: vs.table[i].Interface,
				Reason:    WithdrawnMaxPaths,
			})
		}
		start = end
	}
	vs.table = kept

	slices.SortFunc(vs.withdrawn, func(a, b WithdrawnRoute) int {
		if c := comparePrefix(a.Prefix, b.Prefix); c != 0 {
			return c
		}
		if c := a.NextHop.Compare(b.NextHop); c != 0 {
			return c
		}
		return cmp.Compare(a.Interface, b.Interface)
	})
}

// resolveRoute returns the on-link hops the configured route at index reaches, or the reason
// it reaches none and the prefixes resolution walked.
func resolveRoute(table []routeEntry, index int, r Route) ([]resolvedHop, []netip.Prefix, WithdrawalReason) {
	if r.Interface != "" {
		// A route naming its egress is directly attached, so there is nothing to resolve.
		return []resolvedHop{{nextHop: r.NextHop, iface: r.Interface}}, nil, ""
	}
	return resolveNextHop(table, r.NextHop, map[int]bool{index: true}, []netip.Prefix{r.Prefix}, 1)
}

// resolveNextHop walks table for addr until it reaches interfaces addr is on-link on,
// returning one hop per equal-cost path. visited holds the configured routes already on the
// chain, so a walk returning to one of them is self-recursive rather than endless. A next hop
// matching only a default route does not resolve, which follows FRR's behavior of not
// resolving next hops via the default route.
func resolveNextHop(table []routeEntry, addr netip.Addr, visited map[int]bool, chain []netip.Prefix, depth int) ([]resolvedHop, []netip.Prefix, WithdrawalReason) {
	matches := lookupIn(table, addr)
	if len(matches) == 0 {
		return nil, chain, WithdrawnUnresolved
	}
	if matches[0].Prefix.Bits() == 0 {
		return nil, append(slices.Clone(chain), matches[0].Prefix), WithdrawnUnresolved
	}

	var (
		hops       []resolvedHop
		failChain  []netip.Prefix
		failReason WithdrawalReason
	)
	fail := func(reason WithdrawalReason, c []netip.Prefix) {
		if failReason == "" {
			failReason, failChain = reason, c
		}
	}

	for _, m := range matches {
		next := append(slices.Clone(chain), m.Prefix)
		switch {
		case !m.NextHop.IsValid():
			// A connected route, or a static route naming only an egress: addr is on-link there.
			hops = append(hops, resolvedHop{nextHop: addr, iface: m.Interface})
		case m.Interface != "":
			hops = append(hops, resolvedHop{nextHop: m.NextHop, iface: m.Interface})
		case visited[m.source]:
			fail(WithdrawnSelfRecursive, next)
		case depth+1 > maxRecursionDepth:
			fail(WithdrawnDepthExceeded, next)
		default:
			visited[m.source] = true
			deeper, c, reason := resolveNextHop(table, m.NextHop, visited, next, depth+1)
			delete(visited, m.source)
			if reason != "" {
				fail(reason, c)
				continue
			}
			hops = append(hops, deeper...)
		}
	}

	if len(hops) == 0 {
		fail(WithdrawnUnresolved, chain)
		return nil, failChain, failReason
	}
	return hops, nil, ""
}

// WithdrawnRoutes returns the configured static routes of the named VRF that the forwarding
// table does not hold, sorted by prefix then next hop. A withdrawn route is valid
// configuration whose next hop no chain of routes in its own VRF reaches; it is a different
// failure from [ReasonNeighborMiss], which is a next hop the table reaches and no neighbor
// entry resolves.
func (l *Layer) WithdrawnRoutes(vrf string) []WithdrawnRoute {
	vs, ok := l.vrfs[vrf]
	if !ok {
		return nil
	}
	out := slices.Clone(vs.withdrawn)
	for i := range out {
		out[i].Chain = slices.Clone(out[i].Chain)
	}
	return out
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
// destination, Route drops with [ReasonNoRoute] (RFC 1812 section 5.2.4.3). If the next
// hop's neighbor entry cannot resolve it, either because the VRF never resolves neighbors
// or because a prior resolution attempt already failed, Route drops with
// [ReasonNeighborMiss]; if the next hop is simply unresolved yet under [NeighborObserved],
// Route reports [ReasonNeighborPending] instead — see the package README's "Neighbor
// lifecycle" section.
//
// now is the instant the neighbor lifecycle reasons against, both for a newly created entry's
// resolution deadline and (through [Layer.Age]) an existing one's reachability deadline. commit
// gates every mutation Route can make to the neighbor table: with it clear, Route never creates
// an entry or queues a frame, so a preview cannot change what a later call observes.
//
// Route never mutates f; the returned Result carries a newly constructed frame and payload.
func (l *Layer) Route(now time.Time, iface string, f ethernet.Frame, commit bool) Result {
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
	var flowLabel uint32
	if hdr.V6 != nil {
		flowLabel = hdr.V6.FlowLabel
	}
	sel := selectRoute(vrf.lookup(hdr.Dst), hdr.Src, hdr.Dst, flowLabel)
	if sel == nil {
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

	res.Candidates = candidateSet(sel.candidates)
	matchedRoute := sel.route()
	res.Steps = append(res.Steps, trace.Step{
		Layer:   port.LayerRouting,
		Op:      trace.OpLookup,
		RuleID:  trace.RuleID(matchedRoute.kind),
		Subject: trace.Subject{Kind: "prefix", Key: matchedRoute.Prefix.String()},
		Inputs:  []trace.Fact{packetSnapshot(iface, f, hdr, true, "")},
		Outputs: []trace.Fact{routeSnapshot(vrfName, hdr.Dst, sel)},
	})

	targetAddr := hdr.Dst
	if matchedRoute.resolvedNextHop.IsValid() {
		targetAddr = matchedRoute.resolvedNextHop
	}

	targetIface := matchedRoute.Interface
	res.Interface = targetIface
	res.consult(NeighborLookupScope(l.nodeID, vrfName, targetIface, targetAddr))
	key := neighborKey{iface: targetIface, addr: targetAddr}
	lookup := vrf.resolveNeighbor(now, key, commit, func() heldEntry {
		// Queued in the egress form the direct path below builds too, so finishHeld need not
		// (and must not) decrement it again once resolution completes.
		heldHdr := hdr
		heldHdr.HopLimit--
		pcp, dei := f.Priority()
		return heldEntry{iface: targetIface, etherType: f.EtherType, header: heldHdr, payload: payload, pcp: pcp, dei: dei}
	})

	if lookup.state == NeighborIncomplete {
		res.Reason = ReasonNeighborPending
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRouting,
			Op:      trace.OpLookup,
			RuleID:  trace.RuleID(ReasonNeighborPending),
			Subject: trace.Subject{Kind: "ip", Key: targetAddr.String()},
			Inputs:  []trace.Fact{routeSnapshot(vrfName, hdr.Dst, sel)},
			Outputs: []trace.Fact{neighborSnapshot(targetIface, targetAddr, netaddr.MAC{}, lookup.state)},
		})
		return res
	}
	if !lookup.ok {
		res.Reason = ReasonNeighborMiss
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRouting,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(ReasonNeighborMiss),
			Subject: trace.Subject{Kind: "ip", Key: targetAddr.String()},
			Inputs:  []trace.Fact{routeSnapshot(vrfName, hdr.Dst, sel)},
			Outputs: []trace.Fact{neighborSnapshot(targetIface, targetAddr, netaddr.MAC{}, lookup.state)},
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
		Inputs:  []trace.Fact{packetSnapshot(iface, f, hdr, true, ""), neighborSnapshot(targetIface, targetAddr, lookup.mac, lookup.state)},
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
		Dst:       lookup.mac,
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
//
// now and commit govern the neighbor lookup exactly as they do for [Layer.Route]: now is the
// instant the neighbor lifecycle reasons against, and with commit clear Originate never creates
// an entry or queues a frame.
func (l *Layer) Originate(now time.Time, vrf string, dst netip.Addr, protocol uint8, payload []byte, commit bool) Result {
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

	// An originated datagram carries no flow label, so the address pair is the whole hash
	// input and nothing is read from the caller's payload.
	sel := selectRoute(vrfState.lookup(dst), srcAddr, dst, 0)
	if sel == nil {
		res := l.result(vrf)
		res.Steps = []trace.Step{
			{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: trace.RuleID("no-route"), Subject: trace.Subject{Kind: "ip", Key: dst.String()}, Outputs: []trace.Fact{routeSnapshot(vrf, dst, nil)}},
			{Layer: port.LayerRouting, Op: trace.OpDrop, RuleID: trace.RuleID("no-route"), Subject: trace.Subject{Kind: "vrf", Key: vrf}, Outputs: []trace.Fact{packetSnapshot("", ethernet.Frame{}, ip.Header{Src: srcAddr, Dst: dst}, false, ReasonNoRoute)}},
		}
		res.Reason = ReasonNoRoute
		return res
	}

	matchedRoute := sel.route()
	var steps []trace.Step
	steps = append(steps, trace.Step{
		Layer:   port.LayerRouting,
		Op:      trace.OpLookup,
		RuleID:  trace.RuleID(matchedRoute.kind),
		Subject: trace.Subject{Kind: "prefix", Key: matchedRoute.Prefix.String()},
		Inputs:  []trace.Fact{packetSnapshot("", ethernet.Frame{}, ip.Header{Src: srcAddr, Dst: dst, HopLimit: 64}, true, "")},
		Outputs: []trace.Fact{routeSnapshot(vrf, dst, sel)},
	})

	targetAddr := dst
	if matchedRoute.resolvedNextHop.IsValid() {
		targetAddr = matchedRoute.resolvedNextHop
	}

	targetIface := matchedRoute.Interface

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

	res := l.result(vrf)

	// Encode before resolving. A datagram Encode refuses is refused the same way whether or not
	// its next hop is known: queued on an unresolved next hop it would be re-encoded at release,
	// fail there, and leave the hold queue by a path nothing reports. The bytes are kept for the
	// direct path below, which would otherwise encode the same header and payload a second time,
	// but are deliberately not carried into heldEntry: finishHeld re-encodes from header and
	// payload, so a carried copy would be either a double encode or a second path through it.
	pktBytes, err := hdr.Encode(payload)
	if err != nil {
		res.Steps = slices.Clone(steps)
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRouting,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(ReasonBadHeader),
			Subject: trace.Subject{Kind: "interface", Key: targetIface},
			Inputs:  []trace.Fact{packetSnapshot(targetIface, ethernet.Frame{EtherType: etherType}, hdr, true, "")},
			Outputs: []trace.Fact{packetSnapshot(targetIface, ethernet.Frame{EtherType: etherType}, hdr, false, ReasonBadHeader)},
		})
		res.Reason = ReasonBadHeader
		res.Candidates = candidateSet(sel.candidates)
		return res
	}

	res.consult(NeighborLookupScope(l.nodeID, vrf, targetIface, targetAddr))
	key := neighborKey{iface: targetIface, addr: targetAddr}
	lookup := vrfState.resolveNeighbor(now, key, commit, func() heldEntry {
		// hdr's hop limit is already 64, the egress form Originate's direct path below encodes
		// unchanged, so nothing is decremented before queuing (contrast Route's closure, which
		// decrements here because its direct path does too). pcp and dei are left at their zero
		// value: Originate has no ingress frame to read a priority from, since the datagram is
		// generated by the device itself rather than received and forwarded.
		return heldEntry{iface: targetIface, etherType: etherType, header: hdr, payload: payload}
	})

	if lookup.state == NeighborIncomplete {
		res.Steps = slices.Clone(steps)
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRouting,
			Op:      trace.OpLookup,
			RuleID:  trace.RuleID(ReasonNeighborPending),
			Subject: trace.Subject{Kind: "ip", Key: targetAddr.String()},
			Inputs:  []trace.Fact{routeSnapshot(vrf, dst, sel)},
			Outputs: []trace.Fact{neighborSnapshot(targetIface, targetAddr, netaddr.MAC{}, lookup.state)},
		})
		res.Reason = ReasonNeighborPending
		res.Interface = targetIface
		res.Candidates = candidateSet(sel.candidates)
		return res
	}
	if !lookup.ok {
		res.Steps = slices.Clone(steps)
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRouting,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(ReasonNeighborMiss),
			Subject: trace.Subject{Kind: "ip", Key: targetAddr.String()},
			Inputs:  []trace.Fact{routeSnapshot(vrf, dst, sel)},
			Outputs: []trace.Fact{neighborSnapshot(targetIface, targetAddr, netaddr.MAC{}, lookup.state)},
		})
		res.Reason = ReasonNeighborMiss
		res.Interface = targetIface
		res.Candidates = candidateSet(sel.candidates)
		return res
	}

	steps[len(steps)-1].Outputs = append(steps[len(steps)-1].Outputs, neighborSnapshot(targetIface, targetAddr, lookup.mac, lookup.state))

	egressIfaceObj := l.ifaces[targetIface]
	res.Steps = steps
	res.Interface = targetIface
	res.Candidates = candidateSet(sel.candidates)
	res.Frame = ethernet.Frame{
		Src:       egressIfaceObj.MAC,
		Dst:       lookup.mac,
		EtherType: etherType,
		Payload:   pktBytes,
	}
	return res
}
