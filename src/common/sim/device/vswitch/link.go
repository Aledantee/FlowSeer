package vswitch

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// Start initializes the protocol layers with the current link state of every port in the table.
// On a switch without spanning tree, link aggregation, or loop protection, Start is a no-op.
func (s *Switch) Start(now time.Time) {
	if s.stp == nil && s.lag == nil && s.loopprotect == nil {
		return
	}
	if s.portP2P == nil {
		s.portP2P = make(map[string]PointToPoint)
	}
	if s.portSpeed == nil {
		s.portSpeed = make(map[string]uint64)
	}

	for _, p := range s.ports.Ports() {
		if p.LagParent != "" {
			speed := s.linkSpeed(p)
			s.portP2P[p.Name] = PointToPointTrue
			s.portSpeed[p.Name] = speed
			if s.lag != nil {
				fx := s.lag.LinkChange(now, p.Name, p.Forwards())
				s.applyLAGEffects(now, fx)
			}
		}
	}
	for _, p := range s.ports.Ports() {
		if p.Kind == port.LAG {
			s.updateLagState(now, p.Name)
		}
	}

	for _, p := range s.ports.Ports() {
		if p.LagParent != "" || p.Kind == port.LAG {
			continue
		}
		p2p := true
		if s.cfg.STP != nil {
			if pCfg, ok := s.cfg.STP.Ports[p.Name]; ok {
				if pCfg.PointToPoint == stp.PointToPointForceFalse {
					p2p = false
				}
			}
		}
		speed := s.linkSpeed(p)
		s.portP2P[p.Name] = PointToPointFalse
		if p2p {
			s.portP2P[p.Name] = PointToPointTrue
		}
		s.portSpeed[p.Name] = speed

		if s.stp != nil {
			fx := s.stp.LinkChange(now, p.Name, p.Forwards(), p2p, speed)
			s.applySTPEffects(fx)
		}
		if s.loopprotect != nil {
			fx := s.loopprotect.LinkChange(now, p.Name, p.Forwards())
			s.applyLoopProtectEffects(fx)
		}
	}
	s.recomputeProtocolLinkIssues()
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

func (s *Switch) updateLagState(now time.Time, lagName string) {
	// The row follows the members' own rows, not the layer's delayed
	// link: the relay refuses a LAG whose members are all down, and the row
	// must say the same.
	lagOper := aggregateOperStatus(s.ports.Members(lagName))
	s.setOperStatus(lagName, lagOper)

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

			if p2p, ok := s.portP2P[memName]; ok && p2p != PointToPointTrue {
				lagP2P = false
			}
		}
		if s.cfg.STP != nil && s.cfg.STP.Ports != nil {
			if pCfg, ok := s.cfg.STP.Ports[lagName]; ok && pCfg.PointToPoint == stp.PointToPointForceFalse {
				lagP2P = false
			}
		}
		if s.portP2P != nil {
			s.portP2P[lagName] = PointToPointFalse
			if lagP2P {
				s.portP2P[lagName] = PointToPointTrue
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

// PointToPoint represents the operational point-to-point status of a link.
type PointToPoint string

const (
	// PointToPointUnknown indicates point-to-point status cannot be determined
	// (for example, due to an unknown or unreported duplex).
	PointToPointUnknown PointToPoint = "Unknown"

	// PointToPointTrue indicates the port operates as a point-to-point link.
	PointToPointTrue PointToPoint = "True"

	// PointToPointFalse indicates the port operates as shared media.
	PointToPointFalse PointToPoint = "False"
)

// LinkChange notifies the protocol layers of a link transition on the named port.
//
// An invalid operational state or an absent port records a fault on operErr.
// LinkChange returns nothing and the fault has no exported reader.
func (s *Switch) LinkChange(now time.Time, portName string, state port.LinkState, pointToPoint PointToPoint, speed uint64) {
	if s.portP2P == nil {
		s.portP2P = make(map[string]PointToPoint)
	}
	if s.portSpeed == nil {
		s.portSpeed = make(map[string]uint64)
	}
	// An unset value is a report that says nothing about the link, so it is
	// unknown rather than shared media.
	if pointToPoint == "" {
		pointToPoint = PointToPointUnknown
	}

	up := state == port.Up
	p2p := pointToPoint == PointToPointTrue

	p, ok := s.ports.Port(portName)
	if ok && p.LagParent != "" {
		s.portP2P[portName] = pointToPoint
		s.portSpeed[portName] = speed

		s.setOperStatus(portName, state)

		if s.lag != nil {
			fx := s.lag.LinkChange(now, portName, up)
			s.applyLAGEffects(now, fx)
		}
		s.updateLagState(now, p.LagParent)
		s.recomputeProtocolLinkIssues()
		return
	}

	resolvedPort := portName
	if p, ok := s.ports.Resolve(portName); ok {
		resolvedPort = p.Name
	}
	s.portP2P[resolvedPort] = pointToPoint
	s.portSpeed[resolvedPort] = speed

	s.setOperStatus(resolvedPort, state)

	if s.stp != nil {
		fx := s.stp.LinkChange(now, resolvedPort, up, p2p, speed)
		s.applySTPEffects(fx)
	}
	if s.loopprotect != nil {
		fx := s.loopprotect.LinkChange(now, resolvedPort, up)
		s.applyLoopProtectEffects(fx)
	}
	s.recomputeProtocolLinkIssues()
}

// SetOperStatus updates the operational link state of the named port in the
// switch's port table and the relay's. It tells the spanning tree layer
// nothing: only the caller knows whether a member's change moves its LAG or
// what the link's speed and kind are, so the caller follows with LinkChange.
// SetOperStatus returns a structured validation error when state is outside
// the [port.LinkState] domain or the port is absent. On refusal, it leaves both
// tables unchanged.
func (s *Switch) SetOperStatus(portName string, state port.LinkState) error {
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

// setOperStatus applies an oper-status change on the forward path and records
// the first failure on the sticky operErr field instead of panicking. Its
// callers (updateLagState and LinkChange) run under the switch forwarding API
// (Forward, Peek), which returns no error. A rejected oper status is recorded
// on operErr and has no exported reader rather than taking the process down.
func (s *Switch) setOperStatus(portName string, state port.LinkState) {
	if err := s.SetOperStatus(portName, state); err != nil {
		s.recordOperFault(err)
	}
}
