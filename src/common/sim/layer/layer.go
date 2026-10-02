// Package layer defines shared types and contracts common to data-link and network
// layer models in the switch pipeline.
package layer

import (
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

// Emission describes an Ethernet frame to transmit out a virtual switch port.
// A zero VID retains layer-specific egress semantics: for spanning tree protocol
// (STP) frames, VID 0 indicates the frame leaves untagged without a VLAN membership
// check; for loop-protection frames, VID 0 requests transmission onto the port's
// native/untagged VLAN.
type Emission struct {
	Port  string
	VID   vlan.ID
	Frame ethernet.Frame
}

// FlushTarget names a port whose learned forwarding table entries must be
// flushed, and which FIDs on it are stale. An empty FIDs slice indicates that
// every FID on the port is stale and must be flushed.
type FlushTarget struct {
	Port string
	FIDs []vlan.ID
}

// Effects lists frames to emit, forwarding entries to flush, and LAGs whose
// enabled membership changed as a result of a layer state change or timer advance.
type Effects struct {
	Emissions []Emission
	Flush     []FlushTarget
	Changed   []string
}
