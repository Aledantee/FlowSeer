package fabric

import (
	"cmp"
	"net/netip"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
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

	// ReasonNoCable records that a port is Down because no cable reaches it; Fabric.Unlinked carries it, since a
	// port without a cable has no Link.
	ReasonNoCable trace.Reason = "no-cable"

	// ReasonDeadDirection records that two-ended auto-negotiation cannot succeed because the cable is impaired in one direction.
	ReasonDeadDirection trace.Reason = "dead-direction"

	// ReasonCableLoss records that a transmitted frame was lost during cable propagation due to a configured fault.
	ReasonCableLoss trace.Reason = "cable-loss"

	// ReasonBadFrame records that an arriving frame was dropped because it was corrupted during transmission.
	ReasonBadFrame trace.Reason = "bad-frame"

	// ReasonReachExceeded records that a link cannot operate because the cable length exceeds the medium reach for every candidate speed.
	ReasonReachExceeded trace.Reason = "reach-exceeded"
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
	if e.Port == "" {
		return e.Node
	}
	return e.Node + ":" + e.Port
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
// A nil VLAN emits and accepts untagged frames, while a non-nil VLAN restricts the host to C-TAG frames
// with that VID. An optional IP stack enables packet origination through routing and neighbor lookup.
type Host struct {
	Address netaddr.MAC
	VLAN    *vlan.ID
	IP      *HostIP
}

// Clone returns an independent deep copy of the host configuration.
func (h Host) Clone() Host {
	cp := h
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
	}

	return routing.Config{
		VRFs: map[string]routing.VRF{
			routing.DefaultVRF: vrf,
		},
	}, tbl
}

// Cable models a physical link connecting two endpoints: a length and a medium that give the propagation time and
// bound the negotiated speed, an optional Delay that replaces the propagation term, an optional top speed limit,
// and declared faults.
type Cable struct {
	A            Endpoint
	B            Endpoint
	LengthMeters float64
	TopSpeedBPS  uint64
	Fault        Fault
	Medium       Medium
	Delay        *time.Duration
}

// Clone returns an independent deep copy of the cable configuration.
func (c Cable) Clone() Cable {
	cp := c
	cp.Fault = c.Fault.Clone()
	if c.Delay != nil {
		d := *c.Delay
		cp.Delay = &d
	}

	return cp
}

// Equal reports whether two cable configurations are identical.
func (c Cable) Equal(other Cable) bool {
	if c.A != other.A || c.B != other.B || c.LengthMeters != other.LengthMeters || c.TopSpeedBPS != other.TopSpeedBPS || c.Medium != other.Medium {
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
	return true
}

// Config declares the full static topology of a simulated network fabric.
type Config struct {
	Start    time.Time
	Switches map[string]vswitch.Config
	Hosts    map[string]Host
	Cables   []Cable
}

// Clone returns an independent deep copy of the fabric configuration.
func (c Config) Clone() Config {
	cp := Config{
		Start: c.Start,
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

// Equal reports whether two fabric configurations are identical.
func (c Config) Equal(other Config) bool {
	return equalNormalizedConfigs(c.Normalize(), other.Normalize())
}

func equalNormalizedConfigs(c, other Config) bool {
	if !c.Start.Equal(other.Start) {
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
// and deterministically ordered cables.
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
		cloned.Hosts[name] = h
	}

	for i := range cloned.Cables {
		cable := cloned.Cables[i]
		cable.Medium = normalizedMedium(cable.Medium)
		cable.Fault = normalizedFault(cable.Fault)
		cloned.Cables[i] = cable
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

func comparePrefix(a, b netip.Prefix) int {
	if order := a.Addr().Compare(b.Addr()); order != 0 {
		return order
	}

	return cmp.Compare(a.Bits(), b.Bits())
}

// Validate verifies structural and topological invariants of the configuration:
// switch configurations must pass their own validation, endpoints must reference existing
// nodes and ports, cable attachments cannot target LAGs or duplicate existing links,
// and hosts must attach to exactly one cable with an empty port name.
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
		if cable.LengthMeters < 0 {
			return errs.New().
				Attr("cable", i).
				Attr("length", cable.LengthMeters).
				Msg("cable length cannot be negative")
		}

		if cable.Delay != nil && *cable.Delay < 0 {
			return errs.New().
				Attr("cable", i).
				Attr("delay", *cable.Delay).
				Msg("cable delay cannot be negative")
		}

		switch cable.Medium {
		case "", TwistedPair, MultimodeFiber, SinglemodeFiber, Twinax:
		default:
			return errs.New().
				Attr("cable", i).
				Attr("medium", cable.Medium).
				Msg("unknown cable medium")
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

	return nil
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
		return nil
	case FaultLoseEveryNth, FaultCorruptEveryNth:
		if f.N == 0 {
			return errs.New().Attr("kind", f.Kind).Msg("fault parameter N must be greater than zero")
		}

		return nil
	case FaultLoseSequence:
		if len(f.Sequence) == 0 {
			return errs.New().Attr("kind", f.Kind).Msg("fault sequence cannot be empty")
		}

		return nil
	default:
		return errs.New().Attr("kind", f.Kind).Msgf("unsupported fault kind %q", f.Kind)
	}
}
