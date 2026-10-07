package vswitch

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
)

// Emission describes an Ethernet frame to transmit out a port or member port.
type Emission struct {
	Port  string
	Frame ethernet.Frame

	// PCP is the 802.1Q priority the frame queues at on egress. It is carried
	// rather than left to a reader to re-derive from Frame, because an
	// untagged egress frame carries no tag to derive from: a frame that
	// arrived tagged at priority 5 and left a routed or untagged access port
	// would re-derive as 0 and overtake nothing, where the live, non-held
	// path queues it at 5. A protocol frame leaves it at zero: a BPDU, an
	// LACPDU, and a loop-protect probe all go out either untagged or tagged at
	// priority 0 by [bridge.Layer.OriginateFrame], while a Marker response
	// preserves the request's tag stack and queues with PCP 0.
	PCP vlan.PCP

	// Protocol reports whether the frame is a BPDU, LACPDU, Marker response,
	// loop-protect probe, or other frame the switch generated for a protocol
	// of its own, as opposed to a held user frame released once its next hop
	// resolved.
	// [Fabric.injectEmission] reads it instead of assuming every emission
	// is a protocol frame.
	Protocol bool
}

// Age removes dynamic forwarding database entries older than the configured
// aging time relative to now, advances multicast router and group expiry, and
// advances the routing layer's neighbor table, applying any hold-queue exits.
func (s *Switch) Age(now time.Time) {
	_, end := s.beginCall(now, true)
	defer end()

	for _, entry := range s.layers(now) {
		if !entry.age {
			continue
		}
		fx := entry.advance(now)
		if entry.apply != nil {
			entry.apply(fx)
		}
	}
}

func (s *Switch) applySTPEffects(fx layer.Effects) {
	if len(fx.Flush) > 0 && s.bridge != nil {
		s.bridge.Flush(fx.Flush)
	}
	for _, em := range fx.Emissions {
		// A zero VID is a frame the layer built whole: an IEEE-addressed BPDU
		// rides the wire untagged and unchecked against VLAN membership, on a
		// trunk with no native VLAN as much as anywhere else. A non-zero VID
		// is a per-VLAN BPDU, which leaves exactly as any other frame the
		// switch originates on that VLAN would: tagged where the VLAN is
		// tagged, untagged where it is the port's untagged VLAN, and not at
		// all where the port does not carry it.
		if em.VID == 0 {
			s.call.emissions = append(s.call.emissions, Emission{Port: em.Port, Frame: em.Frame, Protocol: true})

			continue
		}
		egress, ok := s.bridge.OriginateFrame(em.Port, em.VID, em.Frame)
		if !ok {
			continue
		}
		s.call.emissions = append(s.call.emissions, Emission{Port: em.Port, Frame: egress, Protocol: true})
	}
}

// applyLoopProtectEffects puts the layer's probes on the wire and flushes the
// bridge's learned entries on the ports fx.Flush names, mirroring
// applySTPEffects's own flush. A probe leaves only where an ordinary frame
// would: the port has to be operationally forwarding, and a spanning tree
// running on the same switch has to forward the VLAN over it, so a port the
// tree already holds discarding cannot report a loop the tree has broken.
// What the loop-protection layer itself says about the port is deliberately
// not consulted, which is what lets a blocked port keep probing and a
// LoopCleared recovery see the loop persist.
func (s *Switch) applyLoopProtectEffects(fx layer.Effects) {
	if len(fx.Flush) > 0 && s.bridge != nil {
		s.bridge.Flush(fx.Flush)
	}
	for _, em := range fx.Emissions {
		p, ok := s.ports.Port(em.Port)
		if !ok || !p.Forwards() {
			continue
		}

		vid := em.VID
		if vid == 0 {
			vid = s.untaggedVID(em.Port)
		}
		if s.stp != nil && !s.stp.Forwards(em.Port, vid) {
			continue
		}

		// The payload must name the VLAN the probe actually rides. em.VID is
		// 0 for a port with no configured VLANs, encoded that way because
		// the layer does not know the port's PVID; now that vid is resolved,
		// patch the frame without decoding so the wire frame agrees with what
		// the switch classifies a returning copy into.
		frame := loopprotect.SetProbeVID(em.Frame, vid)

		egress, ok := s.bridge.OriginateFrame(em.Port, vid, frame)
		if !ok {
			continue
		}
		s.call.emissions = append(s.call.emissions, Emission{Port: em.Port, Frame: egress, Protocol: true})
	}
}

func (s *Switch) applyLAGEffects(now time.Time, fx layer.Effects) {
	for _, em := range fx.Emissions {
		s.call.emissions = append(s.call.emissions, Emission{Port: em.Port, Frame: em.Frame, Protocol: true})
	}
	for _, lagName := range fx.Changed {
		s.updateLagState(now, lagName)
	}
}

// Drain returns and clears pending frame emissions produced by the protocol layers.
func (s *Switch) Drain() []Emission {
	em := s.emissions
	s.emissions = nil

	return em
}

// DrainNeighborFailures returns and clears the records made for held frames
// that reached no wire, the same way [Switch.Drain] returns and clears
// released frames as emissions. Records are produced by [Switch.Wake],
// [Switch.Age], or an observing [Switch.Forward]. It is every exit from a
// hold queue that is not an emission, not timeouts alone: a frame the queue
// pushed out to make room, and a released frame the bridge or the port table
// then refused, are both here, each carrying the reason its own stage gave.
func (s *Switch) DrainNeighborFailures() []NeighborDrop {
	steps := s.neighborFailures
	s.neighborFailures = nil

	return steps
}

// Wake advances the protocol layers to now, firing due timers, flushing bridge entries,
// and triggering periodic transmissions.
// On a switch without spanning tree, link aggregation, loop protection, or routing, Wake is a no-op.
func (s *Switch) Wake(now time.Time) {
	_, end := s.beginCall(now, true)
	defer end()

	for _, entry := range s.layers(now) {
		if entry.timer == nil {
			continue
		}
		fx := entry.advance(now)
		if entry.apply != nil {
			entry.apply(fx)
		}
	}
}

// NextWake returns the earliest scheduled time at which the switch needs to be woken,
// and reports whether any timer is active across the switch's timed layers.
func (s *Switch) NextWake() (time.Time, bool) {
	var (
		earliest time.Time
		hasTimer bool
	)

	update := func(t time.Time, ok bool) {
		if !ok || t.IsZero() {
			return
		}
		if !hasTimer || t.Before(earliest) {
			earliest = t
			hasTimer = true
		}
	}

	for _, entry := range s.layers(time.Time{}) {
		if entry.timer != nil {
			update(entry.timer.NextWake())
		}
	}

	return earliest, hasTimer
}

// Mcheck triggers protocol migration checking on the named port, forcing it to
// transmit RSTP BPDUs and restarting the migration delay.
// On a switch without spanning tree configuration, Mcheck is a no-op.
func (s *Switch) Mcheck(now time.Time, port string) {
	_, end := s.beginCall(now, true)
	defer end()

	if s.stp == nil {
		return
	}
	resolvedPort := port
	if p, ok := s.ports.Resolve(port); ok {
		resolvedPort = p.Name
	}
	fx := s.stp.Mcheck(now, resolvedPort)
	s.applySTPEffects(fx)
}

// ClearLoopProtect manually lifts the loop-protection action applied to the
// named port. It reports whether the port was tracked by the loop-protection
// layer and had an action applied. On a switch without loop-protection
// configuration, ClearLoopProtect is a no-op that reports false.
func (s *Switch) ClearLoopProtect(now time.Time, port string) bool {
	if s.loopprotect == nil {
		return false
	}

	return s.loopprotect.Clear(now, port)
}
