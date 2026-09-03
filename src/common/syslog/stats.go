package syslog

import "sync/atomic"

// Stats is a point-in-time snapshot with fixed-cardinality counters. UDP loss
// before ReadFrom (network or kernel drops) is not observable in UDPDropped.
type Stats struct {
	Received   uint64
	Queued     uint64
	Delivered  uint64
	Partial    uint64
	UDPDropped uint64
	// PressureClosed counts streams closed while waiting for frame admission or
	// handoff. Unread frames cannot be counted as individual message losses.
	PressureClosed     uint64
	Oversized          uint64
	FramingErrors      uint64
	ConnectionRejected uint64
	HandshakeErrors    uint64
	ShutdownDiscarded  uint64
	ActiveConnections  int64
	ActiveHandshakes   int64
	ReservedBytes      int
	ReservedFrames     int
}

type counters struct {
	received, queued, delivered, partial, udpDropped, pressureClosed, oversized, framingErrors, connectionRejected, handshakeErrors, shutdownDiscarded atomic.Uint64
	connections, handshakes                                                                                                                            atomic.Int64
}

// Stats reads counters without retaining source addresses or individual events.
func (r *Receiver) Stats() Stats {
	b, f := r.admission.snapshot()
	s := &r.stats
	return Stats{Received: s.received.Load(), Queued: s.queued.Load(), Delivered: s.delivered.Load(), Partial: s.partial.Load(), UDPDropped: s.udpDropped.Load(), PressureClosed: s.pressureClosed.Load(), Oversized: s.oversized.Load(), FramingErrors: s.framingErrors.Load(), ConnectionRejected: s.connectionRejected.Load(), HandshakeErrors: s.handshakeErrors.Load(), ShutdownDiscarded: s.shutdownDiscarded.Load(), ActiveConnections: s.connections.Load(), ActiveHandshakes: s.handshakes.Load(), ReservedBytes: b, ReservedFrames: f}
}
