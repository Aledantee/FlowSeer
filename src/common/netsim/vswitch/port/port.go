// Package port provides the port table for the virtual switch, representing
// ordered network interfaces with link states, MTU configuration, and LAG relationships.
package port

import (
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

// LinkState represents the administrative or operational link state of a port.
type LinkState string

const (
	// Up indicates the link is administratively or operationally active.
	Up LinkState = "Up"

	// Down indicates the link is administratively disabled or operationally inactive.
	Down LinkState = "Down"

	// Unreported indicates the link state was not reported.
	Unreported LinkState = "Unreported"
)

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

// Forwards reports whether the port forwards frames. A port forwards unless its
// administrative or operational state is Down.
func (p Port) Forwards() bool {
	return p.AdminStatus != Down && p.OperStatus != Down
}

// Table is an ordered collection of ports keyed by port name.
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

// Ports returns a slice of all ports in insertion order.
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

// Members returns the member ports of the named LAG in table insertion order.
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

// Receive evaluates whether a frame can arrive on the named port. If the port is
// a member of a LAG, Receive resolves it to its parent LAG port. It returns
// [ReasonPortDown] when the port is unknown, its LAG parent does not exist, or
// either the port or its resolved parent does not forward. An empty reason
// indicates the port can receive traffic.
func (t Table) Receive(name string) (Port, trace.Reason) {
	p, ok := t.Port(name)
	if !ok {
		return Port{}, ReasonPortDown
	}
	if p.LagParent == "" {
		if !p.Forwards() {
			return p, ReasonPortDown
		}

		return p, ""
	}
	parent, ok := t.Port(p.LagParent)
	if !ok {
		return Port{}, ReasonPortDown
	}
	if !p.Forwards() || !parent.Forwards() {
		return parent, ReasonPortDown
	}

	return parent, ""
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

// Validate verifies the invariants of the table: all port names must be non-empty
// and unique, any configured LAG parent must refer to an existing port of kind Lag,
// and no LAG port may have a LAG parent.
func (t Table) Validate() error {
	seen := make(map[string]struct{}, len(t.ports))
	for _, p := range t.ports {
		if p.Name == "" {
			return errs.New().Attr("name", "").Msg("port name cannot be empty")
		}
		if _, exists := seen[p.Name]; exists {
			return errs.New().Attr("name", p.Name).Msgf("duplicate port name %q", p.Name)
		}
		seen[p.Name] = struct{}{}
	}

	for _, p := range t.ports {
		if p.Kind == Lag && p.LagParent != "" {
			return errs.New().
				Attr("name", p.Name).
				Attr("parent", p.LagParent).
				Msgf("LAG port %q cannot have a LAG parent", p.Name)
		}
		if p.LagParent != "" {
			parent, exists := t.byName[p.LagParent]
			if !exists {
				return errs.New().
					Attr("name", p.Name).
					Attr("parent", p.LagParent).
					Msgf("port %q refers to non-existent LAG parent %q", p.Name, p.LagParent)
			}
			if parent.Kind != Lag {
				return errs.New().
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
