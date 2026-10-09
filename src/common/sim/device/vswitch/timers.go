package vswitch

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// emission describes an Ethernet frame to transmit out a port or member port.
type emission struct {
	Port   string
	Member string
	Frame  ethernet.Frame
	// Hold is the released frame's token. Protocol emissions carry zero.
	Hold routing.HoldToken
	// Ingress is the held frame's routed ingress interface, empty for origination.
	Ingress string

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
			s.emit(emission{Port: em.Port, Frame: em.Frame, Protocol: true})

			continue
		}
		egress, ok := s.bridge.OriginateFrame(em.Port, em.VID, em.Frame)
		if !ok {
			continue
		}
		s.emit(emission{Port: em.Port, Frame: egress, Protocol: true})
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
		s.emit(emission{Port: em.Port, Frame: egress, Protocol: true})
	}
}

func (s *Switch) applyLAGEffects(now time.Time, fx layer.Effects) {
	for _, em := range fx.Emissions {
		s.emit(emission{Port: em.Port, Frame: em.Frame, Protocol: true})
	}
	for _, lagName := range fx.Changed {
		// LAG effects name aggregators from the validated configuration and table.
		_ = s.updateLagState(now, lagName)
	}
}

// NextWake returns the earliest scheduled time at which the switch needs to be woken,
// and reports whether any timer is active in the shared layer table.
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
// It returns the emitted BPDUs in device.Output. Without spanning tree configuration,
// Mcheck returns an empty device.Output.
func (s *Switch) Mcheck(now time.Time, port string) device.Output {
	c, end := s.beginCall(now, true)
	defer end()

	if s.stp == nil {
		return c.output(forwardResult{})
	}
	resolvedPort := port
	if p, ok := s.ports.Resolve(port); ok {
		resolvedPort = p.Name
	}
	fx := s.stp.Mcheck(now, resolvedPort)
	s.applySTPEffects(fx)
	return c.output(forwardResult{})
}

// ClearLoopProtect manually lifts the loop-protection action applied to the
// named logical port. It returns an empty device.Output because clearing originates
// no frame. An absent layer or untracked port is a no-op.
func (s *Switch) ClearLoopProtect(now time.Time, port string) device.Output {
	c, end := s.beginCall(now, true)
	defer end()
	if s.loopprotect != nil {
		s.loopprotect.Clear(now, s.logicalIngress(port))
	}
	return c.output(forwardResult{})
}

func (s *Switch) emit(em emission) bool {
	if s.isMirrorOutputPort(em.Port) {
		return false
	}
	if p, ok := s.ports.Port(em.Port); ok && p.Kind == port.LAG && em.Member == "" {
		var vid vlan.ID
		for _, tag := range em.Frame.Tags {
			if tag.TPID == uint16(ethernet.EtherTypeDot1Q) {
				vid = tag.VID
				break
			}
		}
		member, ok := s.selectMember(s.call.now, em.Port, em.Frame, vid)
		if !ok {
			return false
		}
		em.Member = member
	}
	s.call.emissions = append(s.call.emissions, em)
	return true
}
