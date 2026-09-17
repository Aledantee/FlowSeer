package fabric

import (
	"bytes"
	"cmp"
	"math"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

// Layer is the architectural trace layer identifier for the network fabric.
const Layer trace.Layer = "fabric"

const (
	// ReasonCut records that a link cannot operate because its physical cable is severed.
	ReasonCut trace.Reason = "cut"

	// ReasonPeerDown records that a link cannot operate because the peer switch port is administratively disabled.
	ReasonPeerDown trace.Reason = "peer-down"

	// ReasonAdminDown records that a link cannot operate because this end's own port is administratively
	// disabled; the other end reads peer-down, so a reader is pointed at the disabled device.
	ReasonAdminDown trace.Reason = "admin-down"

	// ReasonNoCable records that a port is Down because [Config.Uncabled] states no cable reaches it;
	// Fabric.Unlinked carries it, since a port without a cable has no Link.
	ReasonNoCable trace.Reason = "no-cable"

	// ReasonAdjacencyUnresolved records that a switch port is Unknown because no cable names it and
	// [Config.Uncabled] does not list it, so nothing says what, if anything, is attached.
	ReasonAdjacencyUnresolved trace.Reason = "adjacency-unresolved"

	// ReasonReachUnknown records that a link is Unknown because the medium's reach is unknown at the speed
	// negotiation would select or at a higher candidate speed.
	ReasonReachUnknown trace.Reason = "reach-unknown"

	// ReasonDeadDirection records that two-ended auto-negotiation cannot succeed because the cable is impaired in one direction.
	ReasonDeadDirection trace.Reason = "dead-direction"

	// ReasonCableLoss records that a transmitted frame was lost during cable propagation due to a configured fault.
	ReasonCableLoss trace.Reason = "cable-loss"

	// ReasonBadFrame records that an arriving frame was dropped because it was corrupted during transmission.
	ReasonBadFrame trace.Reason = "bad-frame"

	// ReasonReachExceeded records that a link cannot operate because the cable length exceeds the medium reach for every candidate speed.
	ReasonReachExceeded trace.Reason = "reach-exceeded"

	// ReasonHostVLANNotAccepted records a host refusing a frame whose tag form its VLAN does not accept.
	ReasonHostVLANNotAccepted trace.Reason = "host-vlan-not-accepted"

	// ReasonHostUnicastNotAddressed records a host refusing a unicast frame addressed to another MAC.
	ReasonHostUnicastNotAddressed trace.Reason = "host-unicast-not-addressed"

	// ReasonHostMulticastNotAccepted records a host refusing a frame to a group MAC it does not accept.
	ReasonHostMulticastNotAccepted trace.Reason = "host-multicast-not-accepted"

	// ReasonHostIPNotAddressed records a host refusing an IP packet addressed to none of its addresses,
	// broadcasts, or accepted groups.
	ReasonHostIPNotAddressed trace.Reason = "host-ip-not-addressed"

	// ReasonHostIPHeaderUndecodable records a host that cannot decide on a frame because its IP header
	// does not decode.
	ReasonHostIPHeaderUndecodable trace.Reason = "host-ip-header-undecodable"
)

// HostLayer is the trace layer of a host's acceptance decisions.
const HostLayer trace.Layer = "host"

const (
	// IssueOperStatusConflict marks a switch port whose configured operational status, Up or Down,
	// differs from the status its cable derives. The derived status is the one that executes.
	IssueOperStatusConflict analysis.IssueCode = "oper-status-conflict"

	// IssueObservedSpeedConflict marks a link end whose observed speed differs from the speed its link
	// resolved to by negotiation.
	IssueObservedSpeedConflict analysis.IssueCode = "observed-speed-conflict"

	// IssuePropagationUnknown marks an operational link whose medium is unspecified and which has no
	// Delay, so the propagation time of every frame crossing it is unknown and taken as zero.
	IssuePropagationUnknown analysis.IssueCode = "propagation-unknown"
)

// FaultKind identifies the nature of a cable impairment, distinguishing physical defects
// from frame-level transmission losses and corruptions.
type FaultKind string

const (
	// FaultNone indicates an unimpaired, fully operational cable.
	FaultNone FaultKind = "None"

	// FaultCut models complete physical severance, disabling transmission in both directions.
	FaultCut FaultKind = "Cut"

	// FaultDeadAToB models unidirectional failure blocking transmission from endpoint A to endpoint B.
	FaultDeadAToB FaultKind = "DeadAToB"

	// FaultDeadBToA models unidirectional failure blocking transmission from endpoint B to endpoint A.
	FaultDeadBToA FaultKind = "DeadBToA"

	// FaultLoseEveryNth models periodic packet loss dropping every Nth transmitted frame.
	FaultLoseEveryNth FaultKind = "LoseEveryNth"

	// FaultLoseSequence models deterministic packet loss at designated 1-based frame positions.
	FaultLoseSequence FaultKind = "LoseSequence"

	// FaultCorruptEveryNth models periodic frame corruption marking every Nth transmitted frame damaged.
	FaultCorruptEveryNth FaultKind = "CorruptEveryNth"
)

// Fault specifies physical defects or frame-level loss and corruption policies applied to a cable.
type Fault struct {
	Kind     FaultKind
	N        uint
	Sequence []uint
}

// Clone returns an independent deep copy of the fault configuration.
func (f Fault) Clone() Fault {
	cp := f
	if len(f.Sequence) > 0 {
		cp.Sequence = make([]uint, len(f.Sequence))
		copy(cp.Sequence, f.Sequence)
	}

	return cp
}

// Endpoint names a specific attachment point in the fabric, referencing a virtual switch port
// or a single-port host when Port is empty.
type Endpoint struct {
	Node string
	Port string
}

// TypeID returns the fact type identifier for Endpoint.
func (e Endpoint) TypeID() string {
	return "fabric.endpoint"
}

// Canonical returns the canonical representation of the endpoint.
func (e Endpoint) Canonical() string {
	return encodeEndpointPart(e.Node) + ":" + encodeEndpointPart(e.Port)
}

func encodeEndpointPart(value string) string {
	return strings.NewReplacer("%", "%25", ":", "%3A", "-", "%2D").Replace(value)
}

// HostIP configures the layer 3 addressing, default gateway, and static link-layer neighbors
// for a simulated host.
type HostIP struct {
	Addresses []netip.Prefix
	Gateway   netip.Addr
	Neighbors map[netip.Addr]netaddr.MAC
}

// Host is an endpoint with one address and no relay; modeling it as a one-port switch would give it a forwarding database it must never use.
//
// A nil VLAN emits untagged frames and accepts untagged and VID 0 priority-tagged ones, while a non-nil VLAN
// restricts the host to C-TAG frames with that VID. An optional IP stack enables packet origination through
// routing and neighbor lookup, and makes the host accept only IP packets addressed to it. Accept widens which
// destinations the host takes; the fabric delivers a frame only when the host accepts it.
// Ethernet holds the physical facts of the host's one port under the rules a switch port's facts follow;
// the zero value reports none, so the host's link is Unknown unless [Config.PhyAssumption] fills them.
type Host struct {
	Address  netaddr.MAC
	VLAN     *vlan.ID
	IP       *HostIP
	Ethernet phy.Ethernet
	Accept   HostAccept
}

// HostAccept widens which frames a host accepts beyond its own address, broadcast, and the groups its IP
// stack joins. Promiscuous accepts every destination MAC and skips the IP check, AllMulticast accepts every
// group MAC, and Multicast lists further group MACs; a unicast entry is invalid. The zero value accepts
// nothing more.
type HostAccept struct {
	Promiscuous  bool
	AllMulticast bool
	Multicast    []netaddr.MAC
}

// Clone returns an independent deep copy of the host configuration.
func (h Host) Clone() Host {
	cp := h
	cp.Ethernet = h.Ethernet.Clone()
	cp.Accept.Multicast = slices.Clone(h.Accept.Multicast)
	if h.VLAN != nil {
		v := *h.VLAN
		cp.VLAN = &v
	}
	if h.IP != nil {
		ip := HostIP{
			Addresses: slices.Clone(h.IP.Addresses),
			Gateway:   h.IP.Gateway,
		}
		if h.IP.Neighbors != nil {
			ip.Neighbors = make(map[netip.Addr]netaddr.MAC, len(h.IP.Neighbors))
			for k, v := range h.IP.Neighbors {
				ip.Neighbors[k] = v
			}
		}
		cp.IP = &ip
	}

	return cp
}

// Equal reports whether two host configurations are identical.
func (h Host) Equal(other Host) bool {
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

// HostRoutingConfig translates a host's IP configuration into a virtual switch routing configuration
// and single-port table for layer 3 packet origination.
func HostRoutingConfig(name string, h Host) (routing.Config, port.Table) {
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
		// A host stack resolves no neighbors: nothing wakes it, so an entry
		// it held would hold forever. The switch is where resolution is
		// modelled; a host is a packet source.
		NeighborPolicy: routing.NeighborPolicy{Mode: routing.NeighborDisabled},
	}

	return routing.Config{
		VRFs: map[string]routing.VRF{
			routing.DefaultVRF: vrf,
		},
	}, tbl
}

// Cable models a physical link connecting two endpoints: a length and a medium that give the propagation time and
// bound the negotiated speed, an optional Delay that replaces the propagation term, an optional top speed limit,
// and declared faults. A LengthMeters of 0 is a stated 0 m cable. Evidence references entries of
// [ConstructionSpec.Evidence] that support the cable; issues resting on the cable cite them. Evidence is not
// behavior, so [Diff] does not report it.
type Cable struct {
	A            Endpoint
	B            Endpoint
	LengthMeters float64
	TopSpeedBPS  uint64
	Fault        Fault
	Medium       Medium
	Delay        *time.Duration
	Evidence     []trace.EvidenceRef
}

// Clone returns an independent deep copy of the cable configuration.
func (c Cable) Clone() Cable {
	cp := c
	cp.Fault = c.Fault.Clone()
	cp.Evidence = slices.Clone(c.Evidence)
	if c.Delay != nil {
		d := *c.Delay
		cp.Delay = &d
	}

	return cp
}

// Equal reports whether two cable configurations are semantically equal.
func (c Cable) Equal(other Cable) bool {
	if c.A != other.A || c.B != other.B || !sameLengthMeters(c.LengthMeters, other.LengthMeters) || c.TopSpeedBPS != other.TopSpeedBPS || c.Medium != other.Medium {
		return false
	}
	if (c.Delay == nil) != (other.Delay == nil) {
		return false
	}
	if c.Delay != nil && *c.Delay != *other.Delay {
		return false
	}
	if c.Fault.Kind != other.Fault.Kind || c.Fault.N != other.Fault.N || !slices.Equal(c.Fault.Sequence, other.Fault.Sequence) {
		return false
	}
	return slices.Equal(c.Evidence, other.Evidence)
}

func sameLengthMeters(a, b float64) bool {
	return cmp.Compare(a, b) == 0
}

// Uncabled states that a switch port has no cable, so the port is Down with reason no-cable. A non-LAG
// switch port that no cable names and no Uncabled entry lists is unresolved instead. Evidence references
// entries of [ConstructionSpec.Evidence] and, like a cable's, stays out of [Diff].
type Uncabled struct {
	Endpoint Endpoint
	Evidence []trace.EvidenceRef
}

// Clone returns an independent deep copy of the entry.
func (u Uncabled) Clone() Uncabled {
	return Uncabled{Endpoint: u.Endpoint, Evidence: slices.Clone(u.Evidence)}
}

// Equal reports whether two entries name the same port with the same evidence references in the same order.
func (u Uncabled) Equal(other Uncabled) bool {
	return u.Endpoint == other.Endpoint && slices.Equal(u.Evidence, other.Evidence)
}

// PhyAssumption is the physical profile a fabric assumes for facts no source reported. On each link it
// fills an end's empty SupportedSpeedsBPS, unknown AutoNegotiationSupported, and nil Setting from Ethernet,
// and an unspecified cable Medium from Medium; a stated fact is never replaced, and an unspecified Medium or
// an unreported Ethernet field fills nothing. Ethernet follows a switch port's validation rules and must not
// carry Observed, since an observation cannot be assumed. The filled values show in [Fabric.Links], and
// [Fabric.Metadata] carries one assumption per link it filled, naming the facts.
type PhyAssumption struct {
	Medium   Medium
	Ethernet phy.Ethernet
}

// Clone returns an independent deep copy of the assumption, or nil for nil.
func (a *PhyAssumption) Clone() *PhyAssumption {
	if a == nil {
		return nil
	}

	return &PhyAssumption{Medium: a.Medium, Ethernet: a.Ethernet.Clone()}
}

// Equal reports whether two assumptions are both nil or hold the same medium and Ethernet facts.
func (a *PhyAssumption) Equal(other *PhyAssumption) bool {
	if a == nil || other == nil {
		return a == other
	}

	return a.Medium == other.Medium && a.Ethernet.Canonical() == other.Ethernet.Canonical()
}

// Config declares the full static topology of a simulated network fabric.
//
// Every non-LAG switch port is cabled, listed in Uncabled, or unresolved. A nil PhyAssumption leaves every
// unreported physical fact unknown.
type Config struct {
	Start         time.Time
	Switches      map[string]vswitch.Config
	Hosts         map[string]Host
	Cables        []Cable
	Uncabled      []Uncabled
	PhyAssumption *PhyAssumption
}

// Clone returns an independent deep copy of the fabric configuration.
func (c Config) Clone() Config {
	cp := Config{
		Start:         c.Start,
		Uncabled:      cloneUncabled(c.Uncabled),
		PhyAssumption: c.PhyAssumption.Clone(),
	}
	if c.Switches != nil {
		cp.Switches = make(map[string]vswitch.Config, len(c.Switches))
		for k, v := range c.Switches {
			cp.Switches[k] = v.Clone()
		}
	}
	if c.Hosts != nil {
		cp.Hosts = make(map[string]Host, len(c.Hosts))
		for k, v := range c.Hosts {
			cp.Hosts[k] = v.Clone()
		}
	}
	if c.Cables != nil {
		cp.Cables = make([]Cable, len(c.Cables))
		for i, cable := range c.Cables {
			cp.Cables[i] = cable.Clone()
		}
	}

	return cp
}

func cloneUncabled(entries []Uncabled) []Uncabled {
	if entries == nil {
		return nil
	}
	cp := make([]Uncabled, len(entries))
	for i, entry := range entries {
		cp[i] = entry.Clone()
	}

	return cp
}

// Equal reports whether two fabric configurations are identical.
func (c Config) Equal(other Config) bool {
	return equalNormalizedConfigs(c.Normalize(), other.Normalize())
}

func equalNormalizedConfigs(c, other Config) bool {
	if !c.Start.Equal(other.Start) {
		return false
	}
	if !slices.EqualFunc(c.Uncabled, other.Uncabled, Uncabled.Equal) || !c.PhyAssumption.Equal(other.PhyAssumption) {
		return false
	}
	if len(c.Switches) != len(other.Switches) || len(c.Hosts) != len(other.Hosts) || len(c.Cables) != len(other.Cables) {
		return false
	}
	for k, sw := range c.Switches {
		otherSW, ok := other.Switches[k]
		if !ok || !sw.Equal(otherSW) {
			return false
		}
	}
	for k, h := range c.Hosts {
		otherH, ok := other.Hosts[k]
		if !ok || !h.Equal(otherH) {
			return false
		}
	}
	for i := range c.Cables {
		if !c.Cables[i].Equal(other.Cables[i]) {
			return false
		}
	}
	return true
}

// Normalize returns a normalized copy of the fabric configuration with assigned
// hardware addresses across all switches and hosts, normalized switch configurations,
// Ethernet facts normalized as a switch port's are, sorted and deduplicated evidence
// references, and deterministically ordered cables and Uncabled entries. Duplicate
// Uncabled entries are kept, so Validate still refuses them.
func (c Config) Normalize() Config {
	cloned := c.Clone()

	usedMACs := make(map[netaddr.MAC]struct{})
	for _, swCfg := range cloned.Switches {
		if swCfg.MAC != (netaddr.MAC{}) {
			usedMACs[swCfg.MAC] = struct{}{}
		}
		if swCfg.STP != nil && swCfg.STP.Address != (netaddr.MAC{}) {
			usedMACs[swCfg.STP.Address] = struct{}{}
		}
		if swCfg.Routing != nil {
			for _, vrf := range swCfg.Routing.VRFs {
				for _, iface := range vrf.Interfaces {
					if iface.MAC != (netaddr.MAC{}) {
						usedMACs[iface.MAC] = struct{}{}
					}
				}
			}
		}
	}
	for _, h := range cloned.Hosts {
		if h.Address != (netaddr.MAC{}) {
			usedMACs[h.Address] = struct{}{}
		}
	}

	assignLocal := func() netaddr.MAC {
		for n := uint32(1); ; n++ {
			cand := netaddr.Local(n)
			if _, ok := usedMACs[cand]; !ok {
				usedMACs[cand] = struct{}{}
				return cand
			}
		}
	}

	swNames := make([]string, 0, len(cloned.Switches))
	for name := range cloned.Switches {
		swNames = append(swNames, name)
	}
	slices.Sort(swNames)
	for _, name := range swNames {
		swCfg := cloned.Switches[name]
		if swCfg.MAC == (netaddr.MAC{}) {
			swCfg.MAC = assignLocal()
		}
		cloned.Switches[name] = swCfg.Normalize()
	}

	hNames := make([]string, 0, len(cloned.Hosts))
	for name := range cloned.Hosts {
		hNames = append(hNames, name)
	}
	slices.Sort(hNames)
	for _, name := range hNames {
		h := cloned.Hosts[name]
		if h.Address == (netaddr.MAC{}) {
			h.Address = assignLocal()
		}
		if h.IP != nil && len(h.IP.Addresses) > 0 {
			slices.SortFunc(h.IP.Addresses, comparePrefix)
			h.IP.Addresses = slices.Compact(h.IP.Addresses)
		}
		h.Ethernet = normalizedEthernet(h.Ethernet)
		if len(h.Accept.Multicast) > 0 {
			slices.SortFunc(h.Accept.Multicast, compareMAC)
			h.Accept.Multicast = slices.Compact(h.Accept.Multicast)
		}
		cloned.Hosts[name] = h
	}

	for i := range cloned.Cables {
		cable := cloned.Cables[i]
		if cable.LengthMeters == 0 {
			cable.LengthMeters = 0
		}
		cable.Fault = normalizedFault(cable.Fault)
		cable.Evidence = canonicalEvidence(cable.Evidence)
		cloned.Cables[i] = canonicalCableOrientation(cable)
	}

	for i := range cloned.Uncabled {
		cloned.Uncabled[i].Evidence = canonicalEvidence(cloned.Uncabled[i].Evidence)
	}
	slices.SortStableFunc(cloned.Uncabled, func(a, b Uncabled) int {
		return compareEndpoint(a.Endpoint, b.Endpoint)
	})

	if cloned.PhyAssumption != nil {
		cloned.PhyAssumption.Ethernet = normalizedEthernet(cloned.PhyAssumption.Ethernet)
	}

	// Sort cables by endpoint names to ensure stable ordering.
	slices.SortFunc(cloned.Cables, func(i, j Cable) int {
		if r := cmp.Compare(i.A.Node, j.A.Node); r != 0 {
			return r
		}
		if r := cmp.Compare(i.A.Port, j.A.Port); r != 0 {
			return r
		}
		if r := cmp.Compare(i.B.Node, j.B.Node); r != 0 {
			return r
		}

		return cmp.Compare(i.B.Port, j.B.Port)
	})

	return cloned
}

// normalizedEthernet applies the normalization phy applies to a switch port's facts.
func normalizedEthernet(e phy.Ethernet) phy.Ethernet {
	return phy.Config{Ethernet: map[string]phy.Ethernet{"": e}}.Normalize().Ethernet[""]
}

func canonicalEvidence(refs []trace.EvidenceRef) []trace.EvidenceRef {
	if len(refs) == 0 {
		return nil
	}
	sorted := slices.Clone(refs)
	slices.Sort(sorted)

	return slices.Compact(sorted)
}

func canonicalCableOrientation(c Cable) Cable {
	if compareEndpoint(c.A, c.B) <= 0 {
		return c
	}

	c.A, c.B = c.B, c.A
	switch c.Fault.Kind {
	case FaultDeadAToB:
		c.Fault.Kind = FaultDeadBToA
	case FaultDeadBToA:
		c.Fault.Kind = FaultDeadAToB
	}

	return c
}

func compareEndpoint(a, b Endpoint) int {
	if order := cmp.Compare(a.Node, b.Node); order != 0 {
		return order
	}

	return cmp.Compare(a.Port, b.Port)
}

func compareMAC(a, b netaddr.MAC) int {
	return bytes.Compare(a[:], b[:])
}

func comparePrefix(a, b netip.Prefix) int {
	if order := a.Addr().Compare(b.Addr()); order != 0 {
		return order
	}

	return cmp.Compare(a.Bits(), b.Bits())
}

// Validate verifies structural and topological invariants of the configuration:
// switch configurations must pass their own validation, endpoints must reference existing
// nodes and ports, cable attachments cannot target LAGs or duplicate existing links,
// hosts must attach to exactly one cable with an empty port name, host and assumed
// Ethernet facts must pass a switch port's physical validation, a host's accepted multicast
// entries must be group addresses, and every Uncabled entry must name an existing non-LAG
// switch port that no cable and no other entry names. Errors about host facts, Uncabled
// entries, and the assumption carry a "field" path attribute such as "uncabled.1",
// "hosts.h1.ethernet.duplex", or "hosts.h1.accept.multicast.0".
func (c Config) Validate() error {
	swNames := make([]string, 0, len(c.Switches))
	for name := range c.Switches {
		swNames = append(swNames, name)
	}
	slices.Sort(swNames)

	hostNames := make([]string, 0, len(c.Hosts))
	for name := range c.Hosts {
		hostNames = append(hostNames, name)
	}
	slices.Sort(hostNames)

	claimedMACs := make(map[netaddr.MAC]string)
	checkMAC := func(node string, mac netaddr.MAC) error {
		if mac == (netaddr.MAC{}) {
			return nil
		}
		if prevNode, ok := claimedMACs[mac]; ok && prevNode != node {
			return errs.New().
				Attr("node", node).
				Attr("mac", mac).
				Attr("collides_with", prevNode).
				Msgf("MAC address %s on %q collides with %q", mac, node, prevNode)
		}
		claimedMACs[mac] = node

		return nil
	}

	for _, name := range swNames {
		if name == "" {
			return errs.New().Msg("switch name cannot be empty")
		}
		swCfg := c.Switches[name]
		// A protocol layer schedules its first hello from Start; a zero Start
		// puts every wake in year 1, ahead of any frame a caller injects.
		if swCfg.STP != nil && c.Start.IsZero() {
			return errs.New().Attr("switch", name).Msg("a fabric with a spanning tree switch needs a Start time")
		}
		if err := swCfg.Validate(); err != nil {
			return errs.Wrapf(err, "switch %q", name)
		}
		if err := checkMAC(name, swCfg.MAC); err != nil {
			return err
		}
		if swCfg.STP != nil {
			if err := checkMAC(name, swCfg.STP.Address); err != nil {
				return err
			}
		}
		if swCfg.Routing != nil {
			for _, vrf := range swCfg.Routing.VRFs {
				ifaceNames := make([]string, 0, len(vrf.Interfaces))
				for ifName := range vrf.Interfaces {
					ifaceNames = append(ifaceNames, ifName)
				}
				slices.Sort(ifaceNames)
				for _, ifName := range ifaceNames {
					if err := checkMAC(name, vrf.Interfaces[ifName].MAC); err != nil {
						return err
					}
				}
			}
		}
	}

	for _, name := range hostNames {
		if name == "" {
			return errs.New().Msg("host name cannot be empty")
		}
		if _, isSw := c.Switches[name]; isSw {
			return errs.New().Attr("node", name).Msgf("node %q cannot be both a switch and a host", name)
		}
		h := c.Hosts[name]
		if err := checkMAC(name, h.Address); err != nil {
			return err
		}
		if h.VLAN != nil && !h.VLAN.Valid() {
			return errs.New().
				Attr("host", name).
				Attr("vlan", *h.VLAN).
				Msgf("host %q has invalid VLAN ID %d", name, *h.VLAN)
		}
		if err := validateEthernet("hosts."+name+".ethernet", h.Ethernet); err != nil {
			return errs.Wrapf(err, "host %q", name)
		}
		if err := h.Accept.validate("hosts." + name + ".accept"); err != nil {
			return err
		}
		if h.IP != nil {
			if len(h.IP.Addresses) == 0 {
				return errs.New().
					Attr("host", name).
					Msgf("host %q with IP stack must configure at least one address", name)
			}
			rtCfg, tbl := HostRoutingConfig(name, h)
			if err := rtCfg.Validate(tbl); err != nil {
				return errs.Wrapf(err, "host %q", name)
			}
		}
	}

	hostCables := make(map[string]int, len(c.Hosts))
	portCables := make(map[Endpoint]int)

	for i, cable := range c.Cables {
		if math.IsNaN(cable.LengthMeters) || math.IsInf(cable.LengthMeters, 0) || cable.LengthMeters < 0 {
			return errs.New().
				Attr("cable", i).
				Attr("length", cable.LengthMeters).
				Msg("cable length must be finite and non-negative")
		}

		if cable.Delay != nil && *cable.Delay < 0 {
			return errs.New().
				Attr("cable", i).
				Attr("delay", *cable.Delay).
				Msg("cable delay cannot be negative")
		}

		if err := validateMedium("cables."+strconv.Itoa(i)+".medium", cable.Medium); err != nil {
			return errs.Wrapf(err, "cable %d", i)
		}

		if err := validateFault(cable.Fault); err != nil {
			return errs.Wrapf(err, "cable %d fault", i)
		}

		if err := c.validateEndpoint(cable.A, hostCables, portCables); err != nil {
			return errs.Wrapf(err, "cable %d endpoint A", i)
		}
		if err := c.validateEndpoint(cable.B, hostCables, portCables); err != nil {
			return errs.Wrapf(err, "cable %d endpoint B", i)
		}
	}

	for _, name := range hostNames {
		n := hostCables[name]
		if n == 0 {
			return errs.New().Attr("host", name).Msgf("host %q must be connected to a cable", name)
		}
		if n > 1 {
			return errs.New().Attr("host", name).Attr("count", n).Msgf("host %q is connected to %d cables", name, n)
		}
	}

	if err := c.validateUncabled(portCables); err != nil {
		return err
	}

	return c.PhyAssumption.validate()
}

func (c Config) validateUncabled(portCables map[Endpoint]int) error {
	listed := make(map[Endpoint]int, len(c.Uncabled))
	for i, entry := range c.Uncabled {
		field := "uncabled." + strconv.Itoa(i)
		ep := entry.Endpoint
		failure := errs.New().Attr("field", field).Attr("node", ep.Node).Attr("port", ep.Port)

		swCfg, isSwitch := c.Switches[ep.Node]
		if !isSwitch {
			return failure.Msgf("uncabled entry names %q, which is not a switch", ep.Node)
		}
		p, ok := swCfg.Ports.Port(ep.Port)
		if !ok {
			return failure.Msgf("uncabled entry names port %q, which switch %q does not have", ep.Port, ep.Node)
		}
		if p.Kind == port.Lag {
			return failure.Msgf("uncabled entry names LAG %q on switch %q, which takes no cable", ep.Port, ep.Node)
		}
		if portCables[ep] > 0 {
			return failure.Msgf("uncabled entry names port %q on switch %q, which a cable names", ep.Port, ep.Node)
		}
		if first, dup := listed[ep]; dup {
			return failure.Attr("duplicates", "uncabled."+strconv.Itoa(first)).
				Msgf("uncabled entry names port %q on switch %q twice", ep.Port, ep.Node)
		}
		listed[ep] = i
	}

	return nil
}

func (a HostAccept) validate(field string) error {
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

func (a *PhyAssumption) validate() error {
	if a == nil {
		return nil
	}
	if err := validateMedium("phy_assumption.medium", a.Medium); err != nil {
		return err
	}
	if a.Ethernet.Observed != nil {
		return errs.New().
			Attr("field", "phy_assumption.ethernet.observed").
			Msg("a physical assumption cannot carry an observation")
	}

	return validateEthernet("phy_assumption.ethernet", a.Ethernet)
}

func validateMedium(field string, m Medium) error {
	switch m {
	case MediumUnspecified, TwistedPair, MultimodeFiber, SinglemodeFiber, Twinax:
		return nil
	default:
		return errs.New().Attr("field", field).Attr("medium", m).Msg("unknown cable medium")
	}
}

// validateEthernet checks facts that belong to no switch under the rules phy
// applies to a switch port's, reporting phy's field path beneath field.
func validateEthernet(field string, e phy.Ethernet) error {
	const name = "end"
	// A table of one physical port always builds.
	tbl, _ := port.NewBuilder().
		Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	err := phy.Config{Ethernet: map[string]phy.Ethernet{name: e}}.Validate(tbl)
	if err == nil {
		return nil
	}
	inner, _ := errs.Attributes(err)["field"].(string)

	return errs.From(err).
		Attr("field", field+strings.TrimPrefix(inner, "ethernet."+name)).
		Msg("invalid ethernet facts")
}

func (c Config) validateEndpoint(ep Endpoint, hostCables map[string]int, portCables map[Endpoint]int) error {
	if ep.Node == "" {
		return errs.New().Msg("endpoint node name cannot be empty")
	}

	if _, isHost := c.Hosts[ep.Node]; isHost {
		if ep.Port != "" {
			return errs.New().
				Attr("node", ep.Node).
				Attr("port", ep.Port).
				Msgf("host endpoint %q must have an empty port, got %q", ep.Node, ep.Port)
		}
		hostCables[ep.Node]++

		return nil
	}

	swCfg, isSwitch := c.Switches[ep.Node]
	if !isSwitch {
		return errs.New().Attr("node", ep.Node).Msgf("endpoint node %q not found", ep.Node)
	}

	if ep.Port == "" {
		return errs.New().Attr("node", ep.Node).Msgf("switch endpoint %q requires a port name", ep.Node)
	}

	p, ok := swCfg.Ports.Port(ep.Port)
	if !ok {
		return errs.New().
			Attr("node", ep.Node).
			Attr("port", ep.Port).
			Msgf("port %q not found on switch %q", ep.Port, ep.Node)
	}

	if p.Kind == port.Lag {
		return errs.New().
			Attr("node", ep.Node).
			Attr("port", ep.Port).
			Msgf("port %q on switch %q is a LAG and cannot accept cables directly", ep.Port, ep.Node)
	}

	portCables[ep]++
	if portCables[ep] > 1 {
		return errs.New().
			Attr("node", ep.Node).
			Attr("port", ep.Port).
			Msgf("port %q on switch %q is connected to multiple cables", ep.Port, ep.Node)
	}

	return nil
}

func validateFault(f Fault) error {
	switch f.Kind {
	case "", FaultNone, FaultCut, FaultDeadAToB, FaultDeadBToA:
		if f.N != 0 || len(f.Sequence) != 0 {
			return errs.New().
				Attr("kind", f.Kind).
				Msg("fault parameters are set for a kind that does not use them")
		}
		return nil
	case FaultLoseEveryNth, FaultCorruptEveryNth:
		if f.N == 0 {
			return errs.New().Attr("kind", f.Kind).Msg("fault parameter N must be greater than zero")
		}
		if len(f.Sequence) != 0 {
			return errs.New().Attr("kind", f.Kind).Msg("fault sequence is set for an every-Nth kind")
		}

		return nil
	case FaultLoseSequence:
		if f.N != 0 {
			return errs.New().Attr("kind", f.Kind).Msg("fault parameter N is set for a sequence kind")
		}
		if len(f.Sequence) == 0 {
			return errs.New().Attr("kind", f.Kind).Msg("fault sequence cannot be empty")
		}
		for _, position := range f.Sequence {
			if position == 0 {
				return errs.New().Attr("kind", f.Kind).Msg("fault sequence positions must be greater than zero")
			}
		}

		return nil
	default:
		return errs.New().Attr("kind", f.Kind).Msgf("unsupported fault kind %q", f.Kind)
	}
}
