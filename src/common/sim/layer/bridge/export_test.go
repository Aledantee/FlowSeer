package bridge

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
)

// Forward is a test helper that runs Ingress then Egress, learning dynamic addresses.
func (b *Layer) Forward(now time.Time, ingress string, f ethernet.Frame) Result {
	return b.forward(now, ingress, f, true)
}

// Peek is a test helper that runs Ingress then Egress without mutating the forwarding database.
func (b *Layer) Peek(now time.Time, ingress string, f ethernet.Frame) Result {
	return b.forward(now, ingress, f, false)
}
