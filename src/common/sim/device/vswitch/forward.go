package vswitch

import (
	"fmt"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/ndp"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Forward processes an arrival on an ingress port at the given time, updating
// the forwarding database if a bridge relay is present, and returns the forwarding result
// with analysis metadata.
func (s *Switch) Forward(now time.Time, ingress string, f ethernet.Frame) ForwardResult {
	return s.wrapResult(s.forward(now, ingress, f, true))
}

// Peek processes an arrival on an ingress port at the given time without mutating
// the forwarding database and returns the forwarding result with analysis metadata.
func (s *Switch) Peek(now time.Time, ingress string, f ethernet.Frame) ForwardResult {
	return s.wrapResult(s.forward(now, ingress, f, false))
}

// ComposeForwardResult attaches switch-owned trust metadata to a forwarding result
// produced before the bridge pipeline. dependencyPorts names the port state that
// could have changed the result; a member port also depends on its logical LAG.
func (s *Switch) ComposeForwardResult(res bridge.Result, dependencyPorts ...string) ForwardResult {
	for _, name := range dependencyPorts {
		s.forwardingDependencies(name).consult(&res)
	}

	return s.wrapResult(res)
}

func (s *Switch) forward(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	s.lagRebalanceHits = nil
	s.mcastQueryUnobservedHits = nil
	s.pvstBoundaryHits = nil
	s.neighborUnresolvedHits = nil
	if mutate {
		s.copies = nil
	}
	if s.isMirrorOutputPort(ingress) {
		mirror := s.mirrorForOutput(ingress)
		res := bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  traffic.ReasonMirrorOutput,
				Steps: []trace.Step{{
					Layer:   traffic.LayerName,
					Op:      trace.OpDrop,
					RuleID:  traffic.RuleMirrorOutputDrop,
					Subject: trace.Subject{Kind: "port", Key: ingress},
					Inputs:  []trace.Fact{traffic.MirrorDecisionFact(mirror, ingress, frameOctets(f), traffic.ReasonMirrorOutput)},
				}},
			},
			Ingress: ingress,
		}
		s.forwardingDependencies(ingress).consult(&res)
		return res
	}

	if s.lag != nil && f.EtherType == ethernet.EtherTypeSlowProtocols && len(f.Payload) > 0 &&
		(f.Payload[0] == lacp.SubtypeLACP || f.Payload[0] == lacp.SubtypeMarker) {
		p, ok := s.ports.Port(ingress)
		if ok && p.LagParent != "" && p.Forwards() {
			res := s.interceptLACP(now, ingress, f, mutate)
			res.ConsultScopes(s.aggregatorScope(p.LagParent))
			res.ConsultScopes(analysis.FieldScope(
				protocolScope(s.nodeID, lag.LayerName), "ports", ingress,
			))
			s.forwardingDependencies(ingress).consult(&res)
			return s.finishForward(now, ingress, f, res, mutate)
		}
	}

	if s.stp != nil && f.Dst == stpGroupAddress {
		res := s.interceptBPDU(now, ingress, f, mutate)
		if res.Reason != port.ReasonPortDown {
			res.ConsultScopes(analysis.FieldScope(
				protocolScope(s.nodeID, stp.LayerName), "ports", res.Ingress,
			))
		}
		s.forwardingDependencies(ingress).consult(&res)
		return s.finishForward(now, ingress, f, res, mutate)
	}

	if s.stp != nil && f.Dst == sstpGroupAddress {
		res := s.interceptSSTP(now, ingress, f, mutate)
		if res.Reason != port.ReasonPortDown {
			res.ConsultScopes(analysis.FieldScope(
				protocolScope(s.nodeID, stp.LayerName), "ports", res.Ingress,
			))
		}
		s.forwardingDependencies(ingress).consult(&res)
		return s.finishForward(now, ingress, f, res, mutate)
	}

	if s.loopprotect != nil && f.Dst == loopprotect.GroupAddress() {
		if res, handled := s.interceptLoopProtect(now, ingress, f, mutate); handled {
			s.forwardingDependencies(ingress).consult(&res)
			return s.finishForward(now, ingress, f, res, mutate)
		}
	}

	// Reception is a side effect. Control frames keep their ordinary path.
	neighborFrame := f.EtherType == ethernet.EtherTypeARP ||
		(f.EtherType == ethernet.EtherTypeIPv6 && len(f.Payload) > ip.V6HeaderLen &&
			f.Payload[6] == protocolICMPv6 &&
			(f.Payload[ip.V6HeaderLen] == byte(ndp.NeighborSolicitation) || f.Payload[ip.V6HeaderLen] == byte(ndp.NeighborAdvertisement)))
	if mutate && s.routing != nil && neighborFrame {
		if iface, ok := s.observationInterface(now, ingress, f); ok {
			s.receiveAndRelease(now, iface, f)
		}
	}

	var routingScopes []analysis.Scope
	if s.routing == nil && metadataHasScopedContent(s.metadata, routing.VRFScope(s.nodeID, routing.DefaultVRF)) {
		resolved, ok := s.ports.Resolve(ingress)
		name := ingress
		if ok {
			name = resolved.Name
		}
		routingScopes = append(routingScopes, routing.PortLookupScope(s.nodeID, routing.DefaultVRF, name))
	}

	if s.routing != nil {
		receive := s.ports.Receive(ingress)
		resolved := receive.Resolved
		if resolved.Name != "" {
			vid, tagAcceptable := f.OuterVID()
			portVLANScopes := s.routing.PortVLANLookupScopes(resolved.Name, vid)
			routingScopes = append(routingScopes, portVLANScopes...)
			if ifaceName, portRouted, vlanMatched := s.routing.ByPortVLAN(resolved.Name, vid); portRouted {
				matched := vlanMatched && tagAcceptable

				if receive.Reason != "" {
					res := bridge.Result{
						Trace: trace.Trace{
							Outcome: trace.Dropped,
							Reason:  port.ReasonPortDown,
							Steps: []trace.Step{
								{
									Layer:   routing.LayerName,
									Op:      trace.OpDrop,
									RuleID:  routing.RuleStatusDown,
									Subject: trace.Subject{Kind: "port", Key: receive.Decisive},
									Inputs:  receive.ForwardingFacts(),
								},
							},
						},
						Ingress: resolved.Name,
						FID:     0,
					}
					s.forwardingDependencies(ingress).consult(&res)
					res.ConsultScopes(portVLANScopes...)
					return s.finishForward(now, ingress, f, res, mutate)
				}

				if !matched {
					// vlanMatched but !tagAcceptable is a service-tagged frame at a VID that
					// does name a configured interface: the recorded VLAN id alone would name
					// a routed pair and hide the real cause, so this case gets its own rule id
					// rather than sharing the plain VLAN-id miss's.
					ruleID := routing.RuleTagMiss
					if vlanMatched {
						ruleID = routing.RuleTagProtocolMiss
					}
					res := bridge.Result{
						Trace: trace.Trace{
							Outcome: trace.Dropped,
							Reason:  routing.ReasonNotBridged,
							Steps: []trace.Step{
								{
									Layer:   routing.LayerName,
									Op:      trace.OpDrop,
									RuleID:  ruleID,
									Subject: trace.Subject{Kind: "port", Key: resolved.Name},
									Inputs:  []trace.Fact{routing.PortFact(resolved.Name), routing.VLANFact(vid)},
								},
							},
						},
						Ingress: resolved.Name,
						FID:     0,
					}
					s.forwardingDependencies(ingress).consult(&res)
					res.ConsultScopes(portVLANScopes...)
					return s.finishForward(now, ingress, f, res, mutate)
				}

				ownershipScope := s.routing.InterfaceOwnershipScope(ifaceName)
				if !s.routing.Owns(ifaceName, f) {
					res := bridge.Result{
						Trace: trace.Trace{
							Outcome: trace.Dropped,
							Reason:  routing.ReasonNotBridged,
							Steps: []trace.Step{
								{
									Layer:   routing.LayerName,
									Op:      trace.OpDrop,
									RuleID:  routing.RuleNotBridged,
									Subject: trace.Subject{Kind: "port", Key: resolved.Name},
									Inputs:  []trace.Fact{port.ForwardingFact(resolved.Name, resolved, true, routing.ReasonNotBridged)},
								},
							},
						},
						Ingress: resolved.Name,
						FID:     0,
					}
					s.forwardingDependencies(ingress).consult(&res)
					res.ConsultScopes(append(portVLANScopes, ownershipScope)...)
					return s.finishForward(now, ingress, f, res, mutate)
				}

				var ingressFilterRes filter.Result
				if s.filter != nil {
					ingressFilterRes = s.filter.EvaluateIngress(ifaceName, f)
					if ingressFilterRes.Decision == filter.DecisionDrop || ingressFilterRes.Decision == filter.DecisionReject {
						res := bridge.Result{
							Trace: trace.Trace{
								Outcome: trace.Dropped,
								Reason:  ingressFilterRes.Reason,
								Steps:   ingressFilterRes.Steps,
							},
							Ingress: resolved.Name,
							FID:     0,
						}
						s.forwardingDependencies(ingress).consult(&res)
						res.ConsultScopes(append(portVLANScopes, ownershipScope)...)
						res.ConsultScopes(ingressFilterRes.ConsultedScopes()...)
						return s.finishForward(now, ingress, f, res, mutate)
					}
				}

				pcp, dei := f.Priority()
				routeRes := s.routing.Route(now, ifaceName, f, mutate)
				if mutate {
					s.emitRouting(now, routeRes.Emissions)
				}
				res := s.assembleRouteResult(now, resolved.Name, 0, pcp, dei, nil, ifaceName, ingressFilterRes, routeRes, mutate)
				s.forwardingDependencies(ingress).consult(&res)
				res.ConsultScopes(append(portVLANScopes, ownershipScope)...)
				return s.finishForward(now, ingress, f, res, mutate)
			}
		}
	}

	if s.bridge != nil {
		controlCandidate := multicastControlCandidate(f)
		in, res, ok := s.bridge.Ingress(now, ingress, f, mutate && !controlCandidate, mutate)
		if !ok {
			res.ConsultScopes(routingScopes...)
			if s.missingSTP && (f.Dst == stpGroupAddress || f.Dst == sstpGroupAddress) {
				res.ConsultScopes(analysis.FieldScope(
					protocolScope(s.nodeID, stp.LayerName), "ports", res.Ingress,
				))
			}
			return s.finishForward(now, ingress, f, res, mutate)
		}
		in.ConsultScopes(routingScopes...)

		if controlCandidate && s.mcast != nil {
			in.ConsultScopes(analysis.FieldScope(
				protocolScope(s.nodeID, mcast.LayerName),
				"vlans", fmt.Sprint(in.FID),
			))
			if s.mcast.Snooped(in.FID) {
				res := s.forwardMulticastControl(now, ingress, f, in, mutate)
				return s.finishForward(now, ingress, f, res, mutate)
			}
		}

		if s.routing != nil {
			vlanLookupScopes := s.routing.VLANLookupScopes(in.FID)
			in.ConsultScopes(vlanLookupScopes...)
			if iface, ok := s.routing.ByVLAN(in.FID); ok {
				in.ConsultScopes(s.routing.InterfaceOwnershipScope(iface))
				if s.routing.Owns(iface, f) {
					if controlCandidate {
						in = s.commitBridgeLearning(now, ingress, f, in, mutate)
					}
					var ingressFilterRes filter.Result
					if s.filter != nil {
						ingressFilterRes = s.filter.EvaluateIngress(iface, f)
						if ingressFilterRes.Decision == filter.DecisionDrop || ingressFilterRes.Decision == filter.DecisionReject {
							res := bridge.Result{
								Trace: trace.Trace{
									Outcome: trace.Dropped,
									Reason:  ingressFilterRes.Reason,
									Steps:   append(slices.Clone(in.Steps), ingressFilterRes.Steps...),
								},
								Ingress: in.Port,
								FID:     in.FID,
							}
							res.Consult(in.ConsultedPorts()...)
							res.ConsultScopes(in.ConsultedScopes()...)
							res.ConsultScopes(ingressFilterRes.ConsultedScopes()...)
							return s.finishForward(now, ingress, f, res, mutate)
						}
					}
					routeRes := s.routing.Route(now, iface, f, mutate)
					if mutate {
						s.emitRouting(now, routeRes.Emissions)
					}
					res := s.assembleRouteResult(now, in.Port, in.FID, in.PCP, in.DEI, in.Steps, iface, ingressFilterRes, routeRes, mutate)
					res.Consult(in.ConsultedPorts()...)
					res.ConsultScopes(in.ConsultedScopes()...)
					return s.finishForward(now, ingress, f, res, mutate)
				}
			}
		} else if metadataHasScopedContent(s.metadata, routing.VRFScope(s.nodeID, routing.DefaultVRF)) {
			in.ConsultScopes(routing.VLANLookupScope(s.nodeID, routing.DefaultVRF, in.FID))
		}

		if controlCandidate {
			in = s.commitBridgeLearning(now, ingress, f, in, mutate)
		}

		return s.finishForward(now, ingress, f, s.bridge.Egress(in, f), mutate)
	}

	res := s.forwardHub(now, ingress, f, mutate)
	res.ConsultScopes(routingScopes...)
	return s.finishForward(now, ingress, f, res, mutate)
}

func (s *Switch) commitBridgeLearning(now time.Time, ingress string, f ethernet.Frame, in bridge.Ingress, mutate bool) bridge.Ingress {
	if !mutate {
		return in
	}

	learned, _, ok := s.bridge.Ingress(now, ingress, f, true, true)
	if !ok {
		return in
	}
	in.Consult(learned.ConsultedPorts()...)
	in.ConsultScopes(learned.ConsultedScopes()...)
	if len(learned.Steps) > len(in.Steps) {
		in.Steps = append(in.Steps, learned.Steps[len(in.Steps):]...)
	}

	return in
}

func (s *Switch) finishForward(now time.Time, ingress string, received ethernet.Frame, res bridge.Result, mutate bool) bridge.Result {
	s.recordPVSTBoundaries(res)

	if s.traffic == nil {
		return res
	}

	reserved := false
	transmitted := false
	for i, egress := range res.Egress {
		if !s.isMirrorOutputPort(egress.Port) {
			if egress.Dropped == "" {
				transmitted = true
			}
			continue
		}

		reserved = true
		res.Egress[i] = bridge.Egress{
			Port:    egress.Port,
			Frame:   egress.Frame,
			PCP:     egress.PCP,
			Dropped: traffic.ReasonMirrorOutput,
		}
		res.Steps = append(res.Steps, trace.Step{
			Layer:   traffic.LayerName,
			Op:      trace.OpDrop,
			RuleID:  traffic.RuleMirrorEgressDrop,
			Subject: trace.Subject{Kind: "port", Key: egress.Port},
			Inputs:  []trace.Fact{traffic.MirrorDecisionFact(s.mirrorForOutput(egress.Port), egress.Port, frameOctets(egress.Frame), traffic.ReasonMirrorOutput)},
		})
	}
	if reserved && !transmitted {
		res.Outcome = trace.Dropped
		res.Reason = traffic.ReasonMirrorOutput
	}

	switchports := s.trafficSwitchports
	egress := make([]traffic.Egress, len(res.Egress))
	for i, e := range res.Egress {
		egress[i] = traffic.Egress{
			Port:    e.Port,
			Dropped: e.Dropped != "",
		}
	}
	resolvedIngress := res.Ingress
	if resolvedIngress == "" {
		resolvedIngress = ingress
	}
	copies := s.traffic.Copies(switchports, resolvedIngress, res.FID, received, egress)
	copies = s.readyMirrorCopies(now, &res, copies, mutate)
	if mutate {
		s.copies = copies
	}

	return res
}

func traceFacts(values ...trace.Fact) []trace.Fact {
	result := make([]trace.Fact, 0, len(values))
	for _, value := range values {
		if value != nil {
			result = append(result, value)
		}
	}

	return result
}

func (s *Switch) routingForwardingDependencies(name string) forwardingDependencies {
	iface, ok := s.routing.Interface(name)
	if !ok {
		return forwardingDependencies{}
	}
	if iface.Port != "" {
		return s.forwardingDependencies(iface.Port)
	}
	if _, ok := s.ports.Port(name); !ok {
		return forwardingDependencies{}
	}

	return s.forwardingDependencies(name)
}

type forwardingDependencies struct {
	ports  []port.Port
	scopes []analysis.Scope
}

func (dependencies forwardingDependencies) consult(res *bridge.Result) {
	res.Consult(dependencies.ports...)
	res.ConsultScopes(dependencies.scopes...)
}

func (s *Switch) forwardingDependencies(name string) forwardingDependencies {
	p, ok := s.ports.Port(name)
	if !ok {
		return forwardingDependencies{ports: []port.Port{(port.Port{Name: name}).Normalize()}}
	}
	path := []port.Port{p}
	var scopes []analysis.Scope
	if !p.Forwards() || p.LagParent == "" {
		return forwardingDependencies{ports: path}
	}
	parent, exists := s.ports.Port(p.LagParent)
	if !exists {
		return forwardingDependencies{ports: path}
	}
	path = append(path, parent)
	if parent.Forwards() {
		scopes = append(scopes, s.aggregatorScope(p.LagParent))
	}

	return forwardingDependencies{ports: path, scopes: scopes}
}

func (s *Switch) egressDependencies(name string) forwardingDependencies {
	dependencies := s.forwardingDependencies(name)
	if len(dependencies.ports) != 1 || dependencies.ports[0].Kind != port.LAG || !dependencies.ports[0].Forwards() {
		return dependencies
	}
	dependencies.ports = append(dependencies.ports, s.ports.Members(name)...)

	return dependencies
}
