package vswitch

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// Start initializes protocol layers from the switch's validated port table.
// Physical links default to full duplex with a relaying peer. A caller with
// observed link facts uses LinkChange instead.
func (s *Switch) Start(now time.Time) device.Output {
	c, end := s.beginCall(now, true)
	defer end()
	ports := s.ports.Ports()
	for _, members := range []bool{true, false} {
		for _, p := range ports {
			if (p.LagParent != "") != members {
				continue
			}
			// The validated table supplies both the name and operational state.
			_, _ = s.LinkChange(now, p.Name, device.Link{State: p.OperStatus, SpeedBPS: s.linkSpeed(p), Duplex: device.DuplexFull, PeerRelays: true})
		}
	}
	return c.output(forwardResult{})
}

type pointToPoint string

const (
	pointToPointUnknown pointToPoint = "Unknown"
	pointToPointTrue    pointToPoint = "True"
	pointToPointFalse   pointToPoint = "False"
)

func (s *Switch) pointToPoint(name string, link device.Link) pointToPoint {
	if s.cfg.STP != nil {
		switch s.cfg.STP.Ports[name].PointToPoint {
		case stp.PointToPointForceTrue:
			return pointToPointTrue
		case stp.PointToPointForceFalse:
			return pointToPointFalse
		}
	}
	if link.Duplex == "" || link.Duplex == device.DuplexUnknown {
		return pointToPointUnknown
	}
	if link.Duplex == device.DuplexFull && link.PeerRelays {
		return pointToPointTrue
	}
	return pointToPointFalse
}

// linkSpeed is the resolved speed of a port, or of a LAG's fastest member,
// since a LAG has no physical layer of its own.
func (s *Switch) linkSpeed(p port.Port) uint64 {
	if s.speeds == nil {
		return 0
	}
	if p.Kind != port.LAG {
		return s.speeds[p.Name].SpeedBPS
	}
	var best uint64
	for _, m := range s.ports.Members(p.Name) {
		best = max(best, s.speeds[m.Name].SpeedBPS)
	}

	return best
}

// untaggedVID is the VLAN a frame the switch originates on the named port
// carries when the probe did not name one: the port's PVID on a VLAN-aware
// bridge, falling back to the port's tunnel VID when it has no PVID, and
// VLAN 0 on a bridge with no VLAN configuration, which is the single
// forwarding domain the relay uses there.
func (s *Switch) untaggedVID(name string) vlan.ID {
	if s.cfg.Bridge == nil || s.cfg.Bridge.VLAN == nil {
		return 0
	}
	sw, ok := s.cfg.Bridge.VLAN.Switchports[name]
	if !ok {
		return 0
	}
	if sw.PVID != nil {
		return *sw.PVID
	}
	if sw.Tunnel != nil {
		return sw.Tunnel.VID
	}

	return 0
}

// sameUntaggedDomain reports whether the named port is an untagged member of
// both sent and classified, so a loop-protection probe crossing those two
// VLANs on that port says nothing about the VLANs being joined. IEEE
// 802.1Q makes untagged egress a per-VLAN port set (dot1qVlanStaticUntaggedPorts)
// while ingress classification is a per-port PVID scalar
// (dot1qPvid), so a port can legitimately untag on egress for several VLANs
// while classifying ingress into only one of them (vendors call this
// "asymmetric VLAN," used for a shared uplink to many tenant VLANs). An
// untagged frame carries no VLAN tag at all, so the VID the probe recorded
// and the VID it classified into are both local bookkeeping — this port's
// own choices, not evidence of anything on the wire joining the two VLANs.
// A bridge with no VLAN configuration has no switchports, so the question
// does not arise there.
func (s *Switch) sameUntaggedDomain(name string, sent, classified vlan.ID) bool {
	if s.cfg.Bridge == nil || s.cfg.Bridge.VLAN == nil {
		return false
	}
	sw, ok := s.cfg.Bridge.VLAN.Switchports[name]
	if !ok {
		return false
	}

	return slices.Contains(sw.Untagged, sent) && slices.Contains(sw.Untagged, classified)
}

func (s *Switch) updateLagState(now time.Time, lagName string) error {
	// The row follows the members' own rows, not the layer's delayed
	// link: the relay refuses a LAG whose members are all down, and the row
	// must say the same.
	lagOper := aggregateOperStatus(s.ports.Members(lagName))
	if err := s.setOperStatus(lagName, lagOper); err != nil {
		return err
	}

	var enabledMembers []string
	if s.lag != nil {
		info := s.lag.Info(lagName)
		enabledMembers = info.Enabled
	}
	lagUp := len(enabledMembers) > 0

	if s.stp != nil {
		var highestSpeed uint64
		lagP2P := len(enabledMembers) > 0
		for _, memName := range enabledMembers {
			memSpeed := s.portSpeed[memName]
			if memSpeed == 0 && s.speeds != nil {
				memSpeed = s.speeds[memName].SpeedBPS
			}
			highestSpeed = max(highestSpeed, memSpeed)

			if p2p, ok := s.portP2P[memName]; ok && p2p != pointToPointTrue {
				lagP2P = false
			}
		}
		duplex := device.DuplexHalf
		if lagP2P {
			duplex = device.DuplexFull
		}
		lagP2P = s.pointToPoint(lagName, device.Link{Duplex: duplex, PeerRelays: true}) == pointToPointTrue

		if s.portP2P != nil {
			s.portP2P[lagName] = pointToPointFalse
			if lagP2P {
				s.portP2P[lagName] = pointToPointTrue
			}
			s.portSpeed[lagName] = highestSpeed
		}
		fxSTP := s.stp.LinkChange(now, lagName, lagUp, lagP2P, highestSpeed)
		s.applySTPEffects(fxSTP)
	}
	if s.loopprotect != nil {
		fx := s.loopprotect.LinkChange(now, lagName, lagUp)
		s.applyLoopProtectEffects(fx)
	}
	return nil
}

func aggregateOperStatus(members []port.Port) port.LinkState {
	possible := false
	for _, member := range members {
		if member.AdminStatus == port.Up && member.OperStatus == port.Up {
			return port.Up
		}
		if member.AdminStatus != port.Down && member.OperStatus != port.Down {
			possible = true
		}
	}
	if possible {
		return port.Unknown
	}

	return port.Down
}

// LinkChange updates the named port and reports the output of its protocol layers.
// An absent port or a state outside [port.LinkState] returns an error without
// changing runtime state.
func (s *Switch) LinkChange(now time.Time, portName string, link device.Link) (device.Output, error) {
	if err := s.setOperStatus(portName, link.State); err != nil {
		return device.Output{}, err
	}
	c, end := s.beginCall(now, true)
	defer end()

	if s.portP2P == nil {
		s.portP2P = make(map[string]pointToPoint)
	}
	if s.portSpeed == nil {
		s.portSpeed = make(map[string]uint64)
	}
	pointToPoint := s.pointToPoint(portName, link)
	up := link.State == port.Up
	p2p := pointToPoint == pointToPointTrue
	speed := link.SpeedBPS

	p, ok := s.ports.Port(portName)
	if ok && p.LagParent != "" {
		s.portP2P[portName] = pointToPoint
		s.portSpeed[portName] = speed

		if s.lag != nil {
			fx := s.lag.LinkChange(now, portName, up)
			s.applyLAGEffects(now, fx)
		}
		if err := s.updateLagState(now, p.LagParent); err != nil {
			return device.Output{}, err
		}
		s.recomputeProtocolLinkIssues()
		return c.output(forwardResult{}), nil
	}

	if ok && p.Kind == port.LAG {
		if err := s.updateLagState(now, p.Name); err != nil {
			return device.Output{}, err
		}
		s.recomputeProtocolLinkIssues()
		return c.output(forwardResult{}), nil
	}

	resolvedPort := portName
	if p, ok := s.ports.Resolve(portName); ok {
		resolvedPort = p.Name
	}
	s.portP2P[resolvedPort] = pointToPoint
	s.portSpeed[resolvedPort] = speed

	if s.stp != nil {
		fx := s.stp.LinkChange(now, resolvedPort, up, p2p, speed)
		s.applySTPEffects(fx)
	}
	if s.loopprotect != nil {
		fx := s.loopprotect.LinkChange(now, resolvedPort, up)
		s.applyLoopProtectEffects(fx)
	}
	s.recomputeProtocolLinkIssues()
	return c.output(forwardResult{}), nil
}

func (s *Switch) setOperStatus(portName string, state port.LinkState) error {
	tbl, err := s.ports.WithOperStatus(portName, state)
	if err != nil {
		return err
	}

	if s.bridge != nil {
		if err := s.bridge.SetOperStatus(portName, state); err != nil {
			return err
		}
	}
	s.ports = tbl
	s.recomputeProtocolLinkIssues()

	return nil
}
