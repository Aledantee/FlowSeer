package lag

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

// ReceiveMarker answers a Marker Information PDU on a known member regardless
// of its Mux state or LACP mode. Unknown members and refused frames have no
// effects. It leaves protocol state unchanged.
func (l *Layer) ReceiveMarker(_ time.Time, member string, f ethernet.Frame) layer.Effects {
	m, ok := l.members[member]
	if !ok {
		return layer.Effects{}
	}
	response, err := lacp.MarkerResponse(f, l.memberSource(m))
	if err != nil {
		return layer.Effects{}
	}
	return layer.Effects{Emissions: []layer.Emission{{Port: member, Frame: response}}}
}
