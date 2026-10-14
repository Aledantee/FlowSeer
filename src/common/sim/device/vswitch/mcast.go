package vswitch

import (
	"errors"
	"net/netip"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/mld"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

const (
	protocolHopByHop  = 0
	protocolIGMP      = 2
	protocolICMPv6    = 58
	optionRouterAlert = 5
)

func multicastControlCandidate(f ethernet.Frame) bool {
	switch f.EtherType {
	case ethernet.EtherTypeIPv4:
		if len(f.Payload) < ip.V4HeaderLen {
			return false
		}
		headerLength := int(f.Payload[0]&0x0f) * 4
		return headerLength >= ip.V4HeaderLen && len(f.Payload) >= headerLength && f.Payload[9] == protocolIGMP
	case ethernet.EtherTypeIPv6:
		if len(f.Payload) < ip.V6HeaderLen {
			return false
		}
		return f.Payload[6] == protocolHopByHop && len(f.Payload) > ip.V6HeaderLen && f.Payload[ip.V6HeaderLen] == protocolICMPv6
	default:
		return false
	}
}

func (s *Switch) forwardMulticastControl(
	now time.Time,
	f ethernet.Frame,
	in bridge.Ingress,
	mutate bool,
) bridge.Result {
	hdr, payload, err := ip.Decode(f.Payload)
	if err != nil {
		return badMulticastControl(in)
	}

	if hdr.V4 != nil {
		if hdr.HopLimit != 1 || !hdr.Dst.IsMulticast() {
			return badMulticastControl(in)
		}
		message, err := igmp.Decode(payload)
		if err != nil {
			if errors.Is(err, igmp.ErrUnsupported) {
				in = s.commitBridgeLearning(f, in, mutate)
				in.Steps = append(in.Steps, multicastControlStep(mcast.RuleControlUnsupported, "igmp", false, "unsupported", nil))
				return s.bridge.EgressTo(in, f, s.logicalPorts(), bridge.ReasonNoEgress)
			}

			return badMulticastControl(in)
		}

		in = s.commitBridgeLearning(f, in, mutate)
		in.Steps = append(in.Steps, multicastControlStep(
			mcast.RuleControlAdmit, "igmp", true, "", mcast.IGMPControlMessageFact(hdr.Src, message),
		))
		if mutate {
			s.mcast.Learn(now, in.FID, in.Port, hdr.Src, message)
		}
		if message.Type == igmp.Query {
			return s.bridge.EgressTo(in, f, s.logicalPorts(), bridge.ReasonNoEgress)
		}

		return s.bridge.EgressTo(in, f, routerPortNames(s.mcast.RouterPorts(in.FID, now)), mcast.ReasonNoRouterPort)
	}

	if hdr.HopLimit != 1 || !hasRouterAlert(payload) {
		return badMulticastControl(in)
	}
	message, err := mld.Decode(hdr, payload)
	if err != nil {
		if errors.Is(err, mld.ErrUnsupported) {
			if !hdr.Src.IsLinkLocalUnicast() {
				return badMulticastControl(in)
			}
			in = s.commitBridgeLearning(f, in, mutate)
			in.Steps = append(in.Steps, multicastControlStep(mcast.RuleControlUnsupported, "mld", false, "unsupported", nil))
			return s.bridge.EgressTo(in, f, s.logicalPorts(), bridge.ReasonNoEgress)
		}

		return badMulticastControl(in)
	}
	validSource := hdr.Src.IsLinkLocalUnicast() || hdr.Src.IsUnspecified() && (message.Type == mld.ReportV1 || message.Type == mld.ReportV2)
	if !validSource {
		return badMulticastControl(in)
	}

	in = s.commitBridgeLearning(f, in, mutate)
	in.Steps = append(in.Steps, multicastControlStep(
		mcast.RuleControlAdmit, "mld", true, "", mcast.MLDControlMessageFact(hdr.Src, message),
	))
	if mutate {
		s.mcast.LearnMLD(now, in.FID, in.Port, hdr.Src, message)
	}
	if message.Type == mld.Query {
		return s.bridge.EgressTo(in, f, s.logicalPorts(), bridge.ReasonNoEgress)
	}

	return s.bridge.EgressTo(in, f, routerPortNames(s.mcast.RouterPorts(in.FID, now)), mcast.ReasonNoRouterPort)
}

func multicastControlStep(ruleID trace.RuleID, proto string, admitted bool, reason trace.Reason, message trace.Fact) trace.Step {
	return trace.Step{
		Layer:   mcast.LayerName,
		Op:      trace.OpClassify,
		RuleID:  ruleID,
		Subject: trace.Subject{Kind: "protocol", Key: proto},
		Inputs:  traceFacts(message),
		Outputs: []trace.Fact{mcast.ControlDecisionFact(proto, admitted, reason)},
	}
}

func badMulticastControl(in bridge.Ingress) bridge.Result {
	steps := slices.Clone(in.Steps)
	steps = append(steps, trace.Step{
		Layer:   mcast.LayerName,
		Op:      trace.OpDrop,
		RuleID:  mcast.RuleControlBad,
		Subject: trace.Subject{Kind: "port", Key: in.Port},
		Outputs: []trace.Fact{mcast.ControlDecisionFact("", false, mcast.ReasonBadControl)},
	})

	res := bridge.Result{
		Trace:   trace.Trace{Outcome: trace.Dropped, Reason: mcast.ReasonBadControl, Steps: steps},
		Ingress: in.Port,
		FID:     in.FID,
	}
	res.Consult(in.ConsultedPorts()...)
	res.ConsultScopes(in.ConsultedScopes()...)

	return res
}

func hasRouterAlert(payload []byte) bool {
	if len(payload) < 2 || payload[0] != protocolICMPv6 {
		return false
	}
	headerLength := (int(payload[1]) + 1) * 8
	if len(payload) < headerLength {
		return false
	}

	found := false
	for i := 2; i < headerLength; {
		if payload[i] == 0 {
			i++
			continue
		}
		if i+1 >= headerLength {
			return false
		}
		optionLength := int(payload[i+1])
		if i+2+optionLength > headerLength {
			return false
		}
		if payload[i] == optionRouterAlert {
			if optionLength != 2 || payload[i+2] != 0 || payload[i+3] != 0 || found {
				return false
			}
			found = true
		}
		i += 2 + optionLength
	}

	return found
}

func (s *Switch) logicalPorts() []string {
	ports := make([]string, 0, s.ports.Len())
	for _, p := range s.ports.Ports() {
		if p.LagParent == "" {
			ports = append(ports, p.Name)
		}
	}

	return ports
}

func routerPortNames(routers []mcast.RouterPort) []string {
	ports := make([]string, len(routers))
	for i, router := range routers {
		ports[i] = router.Port
	}

	return ports
}

// resolveMcast selects multicast members and router ports for an eligible IP group
// frame as of now, filtering admitted member ports by the frame's IP source.
func (s *Switch) resolveMcast(now time.Time, vid vlan.ID, f ethernet.Frame) ([]string, bool) {
	if s.mcast == nil || s.cfg.Mcast == nil {
		return nil, false
	}
	if _, ok := s.cfg.Mcast.VLANs[vid]; !ok {
		return nil, false
	}
	if f.EtherType != ethernet.EtherTypeIPv4 && f.EtherType != ethernet.EtherTypeIPv6 {
		return nil, false
	}

	hdr, _, err := ip.Decode(f.Payload)
	if err != nil || !hdr.Dst.IsMulticast() {
		return nil, false
	}
	if f.EtherType == ethernet.EtherTypeIPv4 && hdr.V4 == nil ||
		f.EtherType == ethernet.EtherTypeIPv6 && hdr.V6 == nil {
		return nil, false
	}
	if hdr.Dst.Is4() {
		addr := hdr.Dst.As4()
		if addr[0] == 224 && addr[1] == 0 && addr[2] == 0 {
			return nil, false
		}
	} else {
		addr := hdr.Dst.As16()
		scope := addr[1] & 0x0f
		if hdr.Dst == allNodesAddress || scope == 0 || scope == 1 {
			return nil, false
		}
	}

	ports, registered, pending := s.mcast.Resolve(vid, hdr.Dst, hdr.Src, now)
	if registered {
		if pending {
			s.recordMcastQueryUnobserved(vid, hdr.Dst, now)
		}
		return ports, true
	}
	if s.cfg.Mcast.Floods(vid) {
		return nil, false
	}

	return ports, true
}

// mcastMembershipFact records the multicast membership lookup used by bridge
// replication, naming the frame's IP source: ports is already that source's
// admitted egress set, so the fact makes explicit which source produced it.
func (s *Switch) mcastMembershipFact(now time.Time, vid vlan.ID, f ethernet.Frame, ports []string, decided bool) trace.Fact {
	if s.mcast == nil {
		return mcast.MembershipFact(vid, netip.Addr{}, netip.Addr{}, ports, false, decided)
	}
	hdr, _, err := ip.Decode(f.Payload)
	if err != nil {
		return mcast.MembershipFact(vid, netip.Addr{}, netip.Addr{}, ports, false, decided)
	}
	_, registered, _ := s.mcast.Resolve(vid, hdr.Dst, hdr.Src, now)

	return mcast.MembershipFact(vid, hdr.Dst, hdr.Src, ports, registered, decided)
}

// mcastResolver adapts a switch's multicast layer to [bridge.GroupResolver] and
// bridge's semanticGroupResolver.
type mcastResolver struct {
	sw *Switch
}

func (r mcastResolver) Resolve(now time.Time, vid vlan.ID, f ethernet.Frame) ([]string, bool) {
	return r.sw.resolveMcast(now, vid, f)
}

func (r mcastResolver) MembershipFact(now time.Time, vid vlan.ID, f ethernet.Frame, ports []string, decided bool) trace.Fact {
	return r.sw.mcastMembershipFact(now, vid, f, ports, decided)
}
