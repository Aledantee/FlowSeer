package vswitch

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/sim/device"
)

// Originate returns a frame as an arrival on a known port. Packet injection is
// refused because a switch origin names a received frame. Calls require serial access.
func (s *Switch) Originate(_ time.Time, port string, frame ethernet.Frame, packet *device.Packet) (device.Origination, error) {
	if packet != nil {
		return device.Origination{}, errs.New().Attr("switch", s.nodeID).Msgf("cannot inject packet at switch origin %q", s.nodeID)
	}
	if _, ok := s.ports.Port(port); !ok {
		return device.Origination{}, errs.New().Attr("node", s.nodeID).Attr("port", port).Msgf("port %q not found on switch %q", port, s.nodeID)
	}
	frame.Tags = slices.Clone(frame.Tags)
	frame.Payload = slices.Clone(frame.Payload)
	return device.Origination{Arrival: &device.Arrival{Port: port, Frame: frame}}, nil
}
