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
type Port struct {
	Name        string
	IfIndex     *uint32
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

// Lookup returns the port with the given name and reports whether it was found.
func (t Table) Lookup(name string) (Port, bool) {
	return t.Port(name)
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
// If lagName is not found or has no member ports, Members returns nil.
func (t Table) Members(lagName string) []Port {
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
	for i, p := range t.ports {
		cp := p
		if p.IfIndex != nil {
			idx := *p.IfIndex
			cp.IfIndex = &idx
		}
		cloned.ports[i] = cp
		cloned.byName[cp.Name] = cp
	}

	return cloned
}
