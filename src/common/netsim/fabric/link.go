package fabric

import (
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Link represents the resolved operational link state of a cable connecting two endpoints. Its Cable
// carries the medium resolution used, which [Config.PhyAssumption] may have filled.
type Link struct {
	Cable
	A LinkEnd
	B LinkEnd
}

// LinkEnd represents the resolved operational status, reason, and negotiated speed of one endpoint
// attached to a cable. Oper is Up, Down, or Unknown; Reason is empty only for Up. Speed carries a
// nonzero SpeedBPS only when Oper is Up, and its DuplexA is this end's duplex. Ethernet is the facts
// resolution used for this end, including any [Config.PhyAssumption] filled.
type LinkEnd struct {
	Endpoint
	Oper     port.LinkState
	Reason   trace.Reason
	Speed    phy.Link
	Ethernet phy.Ethernet
}

func (e LinkEnd) clone() LinkEnd {
	e.Ethernet = e.Ethernet.Clone()

	return e
}
