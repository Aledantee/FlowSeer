// Package port provides the port table for the virtual switch, representing
// ordered network interfaces with link states, MTU configuration, and LAG relationships.
package port

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// Layer identifies an architectural or protocol layer in trace steps and diff subjects.
type Layer = trace.Layer

const (
	// LayerPort identifies the base port table layer.
	LayerPort trace.Layer = "port"

	// LayerLag identifies the link aggregation layer.
	LayerLag trace.Layer = "lag"

	// LayerEthernet identifies the physical Ethernet speeds and auto-negotiation layer.
	LayerEthernet trace.Layer = "ethernet"

	// LayerPoe identifies the Power over Ethernet layer.
	LayerPoe trace.Layer = "poe"

	// LayerRelay identifies the bridge relay forwarding layer.
	LayerRelay trace.Layer = "relay"

	// LayerVlan identifies the 802.1Q VLAN awareness and filtering layer.
	LayerVlan trace.Layer = "vlan"

	// LayerStp identifies the Rapid Spanning Tree Protocol layer.
	LayerStp trace.Layer = "stp"

	// LayerLoopProtect identifies netsim's own loop-protection layer.
	LayerLoopProtect trace.Layer = "loopprotect"

	// LayerMcast identifies multicast snooping decisions.
	LayerMcast trace.Layer = "mcast"

	// LayerRouting identifies the layer 3 routing capability.
	LayerRouting trace.Layer = "routing"

	// LayerTraffic identifies mirroring, policing, and egress queue configuration.
	LayerTraffic trace.Layer = "traffic"
)

const (
	// ReasonPortDown indicates a frame dropped because an ingress or egress port is down.
	ReasonPortDown trace.Reason = "port-down"

	// ReasonMTUExceeded indicates a frame dropped on an egress port because its payload length exceeds the port MTU.
	ReasonMTUExceeded trace.Reason = "mtu-exceeded"
)

// Kind categorizes an interface by its underlying hardware or logical implementation.
type Kind string

const (
	// Physical represents a physical network interface.
	Physical Kind = "Physical"

	// Lag represents a link aggregation group interface.
	Lag Kind = "Lag"

	// Other represents any other interface kind, such as loopback or management.
	Other Kind = "Other"
)

// TypeID returns the stable identifier for Kind facts.
func (k Kind) TypeID() string {
	return "port.kind"
}

// Canonical returns the string value of the Kind.
func (k Kind) Canonical() string {
	return string(k)
}

// LinkState represents the administrative or operational link state of a port.
type LinkState string

const (
	// Unknown indicates the link state is unknown or unspecified.
	Unknown LinkState = "Unknown"

	// Up indicates the link is administratively or operationally active.
	Up LinkState = "Up"

	// Down indicates the link is administratively disabled or operationally inactive.
	Down LinkState = "Down"
)

// TypeID returns the stable identifier for LinkState facts.
func (s LinkState) TypeID() string {
	return "port.link_state"
}

// Canonical returns the string value of the LinkState.
func (s LinkState) Canonical() string {
	return string(s)
}

// Port represents a network port or interface with its state, limits, and LAG membership.
// IfIndex is the interface's ifIndex, or 0 when the source reported none; ifIndex values
// start at 1, so 0 is free to mean absent.
type Port struct {
	Name        string
	IfIndex     uint32
	Kind        Kind
	AdminStatus LinkState
	OperStatus  LinkState
	MTU         int
	LagParent   string
}

// TypeID returns the stable identifier for Port facts.
func (p Port) TypeID() string {
	return "port.port"
}

// Canonical returns a deterministic representation of the port for equality and ordering.
func (p Port) Canonical() string {
	return fmt.Sprintf("name=%q,kind=%q,ifindex=%d,admin=%q,oper=%q,mtu=%d,lag=%q",
		p.Name, string(p.Kind), p.IfIndex, string(p.AdminStatus), string(p.OperStatus), p.MTU, p.LagParent)
}

// Normalize returns a deterministic copy of the port with standard defaults applied.
// An unspecified Kind defaults to [Physical], and unspecified AdminStatus and OperStatus default to [Unknown].
func (p Port) Normalize() Port {
	cp := p
	if cp.Kind == "" {
		cp.Kind = Physical
	}
	if cp.AdminStatus == "" {
		cp.AdminStatus = Unknown
	}
	if cp.OperStatus == "" {
		cp.OperStatus = Unknown
	}

	return cp
}

// MTUFact is an immutable semantic fact representing a port's MTU setting.
type MTUFact int

// TypeID returns the stable identifier for MTUFact.
func (m MTUFact) TypeID() string { return "port.mtu" }

// Canonical returns the decimal string representation of the MTU.
func (m MTUFact) Canonical() string { return strconv.Itoa(int(m)) }

// String returns the string representation of the MTU.
func (m MTUFact) String() string { return strconv.Itoa(int(m)) }

// LagParentFact is an immutable semantic fact representing a port's LAG parent membership.
type LagParentFact string

// TypeID returns the stable identifier for LagParentFact.
func (f LagParentFact) TypeID() string { return "port.lag_parent" }

// Canonical returns the string representation of the LAG parent.
func (f LagParentFact) Canonical() string { return string(f) }

// String returns the string representation of the LAG parent.
func (f LagParentFact) String() string { return string(f) }

// IfIndexFact is an immutable semantic fact representing a port's ifIndex.
type IfIndexFact uint32

// TypeID returns the stable identifier for IfIndexFact.
func (f IfIndexFact) TypeID() string { return "port.ifindex" }

// Canonical returns the decimal string representation of the ifIndex.
func (f IfIndexFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// String returns the string representation of the ifIndex.
func (f IfIndexFact) String() string { return strconv.FormatUint(uint64(f), 10) }

// Forwards reports whether the port forwards frames. A port forwards only if its
// administrative and operational states are both Up.
func (p Port) Forwards() bool {
	return p.AdminStatus == Up && p.OperStatus == Up
}

// Table is a collection of ports keyed by port name and ordered by name.
//
// Lookups by name run in O(1) time. The zero value is an empty table ready for use.
// Table is safe for concurrent read access.
type Table struct {
	ports  []Port
	byName map[string]Port
}

// Port returns the port with the given name and reports whether it was found.
func (t Table) Port(name string) (Port, bool) {
	p, ok := t.byName[name]

	return p, ok
}

// Ports returns all ports in name order.
func (t Table) Ports() []Port {
	if len(t.ports) == 0 {
		return nil
	}
	cp := make([]Port, len(t.ports))
	copy(cp, t.ports)

	return cp
}

// Len returns the number of ports in the table.
func (t Table) Len() int {
	return len(t.ports)
}

// Members returns the member ports of the named LAG in name order.
// If lagName does not name a LAG in the table, or the LAG has no member ports,
// Members returns nil.
func (t Table) Members(lagName string) []Port {
	if lag, ok := t.byName[lagName]; !ok || lag.Kind != Lag {
		return nil
	}
	var members []Port
	for _, p := range t.ports {
		if p.LagParent == lagName {
			members = append(members, p)
		}
	}

	return members
}

// Resolve resolves a port name to its forwarding interface. If the port is a member
// of a LAG, Resolve returns the parent LAG port. If the port is an independent port,
// Resolve returns the port itself. It reports false if the port is not in the table
// or if its named LAG parent does not exist.
func (t Table) Resolve(name string) (Port, bool) {
	p, ok := t.Port(name)
	if !ok {
		return Port{}, false
	}
	if p.LagParent == "" {
		return p, true
	}
	parent, ok := t.Port(p.LagParent)
	if !ok {
		return Port{}, false
	}

	return parent, true
}

// ReceiveResult records the physical ingress, its resolved logical interface,
// and the port whose state refused the frame. Resolved equals Physical for an
// independent port. Decisive is empty when the frame is admitted.
type ReceiveResult struct {
	Ingress  string
	Physical Port
	Resolved Port
	Decisive string
	Reason   trace.Reason
}

// ConsultedPorts returns the present port-state snapshots used by the receive decision.
func (r ReceiveResult) ConsultedPorts() []Port {
	if r.Physical.Name == "" {
		return nil
	}
	if r.Decisive == r.Physical.Name {
		return []Port{r.Physical}
	}
	if r.Resolved.Name == "" || r.Resolved.Name == r.Physical.Name {
		return []Port{r.Physical}
	}

	return []Port{r.Physical, r.Resolved}
}

// ForwardingFacts returns one fact for every physical or logical port state
// used by the receive decision, including a missing ingress or LAG parent.
func (r ReceiveResult) ForwardingFacts() []trace.Fact {
	physicalReason := trace.Reason("")
	if r.Physical.Name == "" || !r.Physical.Forwards() {
		physicalReason = r.Reason
	}
	facts := []trace.Fact{ForwardingFact(r.Ingress, r.Physical, r.Physical.Forwards(), physicalReason)}
	if r.Decisive == r.Physical.Name {
		return facts
	}

	resolvedName := r.Resolved.Name
	if resolvedName == "" && r.Physical.LagParent != "" {
		resolvedName = r.Physical.LagParent
	}
	if resolvedName == "" || resolvedName == r.Ingress {
		return facts
	}
	resolvedReason := trace.Reason("")
	if r.Resolved.Name == "" || !r.Resolved.Forwards() {
		resolvedReason = r.Reason
	}

	return append(facts, ForwardingFact(resolvedName, r.Resolved, r.Resolved.Forwards(), resolvedReason))
}

// Receive evaluates whether a frame can arrive on the named physical port. A
// member resolves to its parent LAG, while the result retains both snapshots.
// It reports [ReasonPortDown] and the decisive port name when either state does
// not forward. An empty reason indicates the port can receive traffic.
func (t Table) Receive(name string) ReceiveResult {
	result := ReceiveResult{Ingress: name}
	p, ok := t.Port(name)
	if !ok {
		result.Decisive = name
		result.Reason = ReasonPortDown

		return result
	}
	result.Physical = p
	result.Resolved = p
	if p.LagParent == "" {
		if !p.Forwards() {
			result.Decisive = p.Name
			result.Reason = ReasonPortDown
		}

		return result
	}
	if !p.Forwards() {
		result.Decisive = p.Name
		result.Reason = ReasonPortDown

		return result
	}
	parent, ok := t.Port(p.LagParent)
	if !ok {
		result.Resolved = Port{}
		result.Decisive = p.LagParent
		result.Reason = ReasonPortDown

		return result
	}
	result.Resolved = parent
	if !parent.Forwards() {
		result.Decisive = parent.Name
		result.Reason = ReasonPortDown
	}

	return result
}

// Transmit evaluates whether a frame of payloadLen can egress through the named port.
// It returns [ReasonPortDown] if the port is unknown, does not forward, or is a LAG
// with no forwarding members. Member selection is handled by the link aggregation layer,
// so Transmit returns an empty member for both plain and LAG ports. It returns
// [ReasonMTUExceeded] if the port has an MTU configured (> 0) and payloadLen exceeds it.
func (t Table) Transmit(name string, payloadLen int) (string, trace.Reason) {
	p, ok := t.Port(name)
	if !ok || !p.Forwards() {
		return "", ReasonPortDown
	}

	if p.Kind == Lag {
		hasFwd := false
		for _, m := range t.Members(p.Name) {
			if m.Forwards() {
				hasFwd = true
				break
			}
		}
		if !hasFwd {
			return "", ReasonPortDown
		}
	}

	if p.MTU > 0 && payloadLen > p.MTU {
		return "", ReasonMTUExceeded
	}

	return "", ""
}

// Normalize returns an independent copy of the table with standard port defaults
// applied and ports sorted by name.
func (t Table) Normalize() Table {
	if len(t.ports) == 0 {
		return Table{}
	}
	norm := Table{
		ports:  make([]Port, len(t.ports)),
		byName: make(map[string]Port, len(t.ports)),
	}
	for i, p := range t.ports {
		np := p.Normalize()
		norm.ports[i] = np
		norm.byName[np.Name] = np
	}
	slices.SortFunc(norm.ports, func(a, b Port) int {
		return cmp.Compare(a.Name, b.Name)
	})

	return norm
}

// Validate verifies the invariants of the table: all port names must be non-empty
// and unique, enums must be in their declared domains, MTU must be non-negative,
// any configured LAG parent must refer to an existing port of kind Lag,
// and no LAG port may have a LAG parent.
func (t Table) Validate() error {
	seen := make(map[string]struct{}, len(t.ports))
	for _, p := range t.ports {
		if p.Name == "" {
			return errs.New().Attr("field", "name").Attr("name", "").Msg("port name cannot be empty")
		}
		if _, exists := seen[p.Name]; exists {
			return errs.New().Attr("field", "ports."+p.Name).Attr("name", p.Name).Msgf("duplicate port name %q", p.Name)
		}
		seen[p.Name] = struct{}{}

		switch p.Kind {
		case Physical, Lag, Other, "":
		default:
			return errs.New().
				Attr("field", "ports."+p.Name+".kind").
				Attr("name", p.Name).
				Attr("kind", p.Kind).
				Msgf("port %q has unknown kind %q", p.Name, p.Kind)
		}

		switch p.AdminStatus {
		case Up, Down, Unknown, "":
		default:
			return errs.New().
				Attr("field", "ports."+p.Name+".admin_status").
				Attr("name", p.Name).
				Attr("admin_status", p.AdminStatus).
				Msgf("port %q has invalid admin status %q", p.Name, p.AdminStatus)
		}

		switch p.OperStatus {
		case Up, Down, Unknown, "":
		default:
			return errs.New().
				Attr("field", "ports."+p.Name+".oper_status").
				Attr("name", p.Name).
				Attr("oper_status", p.OperStatus).
				Msgf("port %q has invalid oper status %q", p.Name, p.OperStatus)
		}

		if p.MTU < 0 {
			return errs.New().
				Attr("field", "ports."+p.Name+".mtu").
				Attr("name", p.Name).
				Attr("mtu", p.MTU).
				Msgf("port %q MTU cannot be negative", p.Name)
		}
	}

	for _, p := range t.ports {
		if p.Kind == Lag && p.LagParent != "" {
			return errs.New().
				Attr("field", "ports."+p.Name+".lag_parent").
				Attr("name", p.Name).
				Attr("parent", p.LagParent).
				Msgf("LAG port %q cannot have a LAG parent", p.Name)
		}
		if p.LagParent != "" {
			parent, exists := t.byName[p.LagParent]
			if !exists {
				return errs.New().
					Attr("field", "ports."+p.Name+".lag_parent").
					Attr("name", p.Name).
					Attr("parent", p.LagParent).
					Msgf("port %q refers to non-existent LAG parent %q", p.Name, p.LagParent)
			}
			if parent.Kind != Lag {
				return errs.New().
					Attr("field", "ports."+p.Name+".lag_parent").
					Attr("name", p.Name).
					Attr("parent", p.LagParent).
					Msgf("port %q parent %q is not a LAG", p.Name, p.LagParent)
			}
		}
	}

	return nil
}

// Clone returns an independent deep copy of the table.
func (t Table) Clone() Table {
	if len(t.ports) == 0 {
		return Table{}
	}
	cloned := Table{
		ports:  make([]Port, len(t.ports)),
		byName: make(map[string]Port, len(t.ports)),
	}
	copy(cloned.ports, t.ports)
	for _, p := range cloned.ports {
		cloned.byName[p.Name] = p
	}

	return cloned
}
