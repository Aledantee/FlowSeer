// Package netmodel translates between FlowSeer network model messages and the virtual switch.
package netmodel

import (
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Skipped records an entity, facet, or row that was omitted during loading.
type Skipped struct {
	Port string
	What string
	Why  string
}

// Default records a field whose value was defaulted or coerced during loading.
type Default struct {
	Port  string
	Field string
	Value string
}

// Report details the capability set inferred or requested, how each layer was
// chosen, omitted entities or facets, ports without switchport configuration,
// and defaulted fields.
//
// Every slice is sorted so two reports compare with slices.Equal.
type Report struct {
	Capabilities      []port.Layer
	CapabilitySources map[port.Layer]string
	Skipped           []Skipped
	Defaults          []Default
	NoSwitchport      []string
}
