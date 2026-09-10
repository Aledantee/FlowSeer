package fabric

import (
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Link represents the resolved operational link state of a cable connecting two endpoints.
type Link struct {
	Cable
	A LinkEnd
	B LinkEnd
}

// LinkEnd represents the resolved operational status, failure reason, and negotiated speed
// of one endpoint attached to a cable.
type LinkEnd struct {
	Endpoint
	Oper   port.LinkState
	Reason trace.Reason
	Speed  phy.Link
}
