package vswitch

import (
	"fmt"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
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

// Peek processes an arrival on an ingress port at the given time without mutating
// runtime state and returns the forwarding result, metadata, and mirror copies.
func (s *Switch) Peek(now time.Time, ingress string, f ethernet.Frame) Output {
	c, end := s.beginCall(now, false)
	defer end()

	return c.output(s.wrapResult(c, s.forward(c.now, ingress, f, c.commit)))
}

func (s *Switch) forward(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	if res, handled := s.forwardMirrorOutput(ingress, f); handled {
		return res
	}
	if res, handled := s.forwardIntercepts(now, ingress, f, mutate); handled {
		return res
	}
	var routingScopes []analysis.Scope
	if s.routing != nil {
		res, scopes, handled := s.forwardRoutedPort(now, ingress, f, mutate)
		if handled {
			return res
		}
		routingScopes = scopes
	} else if metadataHasScopedContent(s.metadata, routing.VRFScope(s.nodeID, routing.DefaultVRF)) {
		resolved, ok := s.ports.Resolve(ingress)
		name := ingress
		if ok {
			name = resolved.Name
		}
		routingScopes = append(routingScopes, routing.PortLookupScope(s.nodeID, routing.DefaultVRF, name))
	}
	if s.bridge != nil {
		return s.forwardBridge(now, ingress, f, mutate, routingScopes)
	}
	res := s.forwardHub(now, ingress, f, mutate)
	res.ConsultScopes(routingScopes...)
	return s.finishForward(now, ingress, f, res, mutate)
}

func (s *Switch) forwardMirrorOutput(ingress string, f ethernet.Frame) (bridge.Result, bool) {
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
		return res, true
	}

	return bridge.Result{}, false
}

func (s *Switch) forwardIntercepts(now time.Time, ingress string, f ethernet.Frame, mutate bool) (bridge.Result, bool) {
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
			return s.finishForward(now, ingress, f, res, mutate), true
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
		return s.finishForward(now, ingress, f, res, mutate), true
	}

	if s.stp != nil && f.Dst == sstpGroupAddress {
		res := s.interceptSSTP(now, ingress, f, mutate)
		if res.Reason != port.ReasonPortDown {
			res.ConsultScopes(analysis.FieldScope(
				protocolScope(s.nodeID, stp.LayerName), "ports", res.Ingress,
			))
		}
		s.forwardingDependencies(ingress).consult(&res)
		return s.finishForward(now, ingress, f, res, mutate), true
	}

	if s.loopprotect != nil && f.Dst == loopprotect.GroupAddress() {
		if res, handled := s.interceptLoopProtect(now, ingress, f, mutate); handled {
			s.forwardingDependencies(ingress).consult(&res)
			return s.finishForward(now, ingress, f, res, mutate), true
		}
	}

	return bridge.Result{}, false
}

func (s *Switch) forwardRoutedPort(now time.Time, ingress string, f ethernet.Frame, mutate bool) (bridge.Result, []analysis.Scope, bool) {
	var routingScopes []analysis.Scope
	receive := s.ports.Receive(ingress)
	resolved := receive.Resolved
	if resolved.Name != "" {
		vid, tagAcceptable := f.OuterVID()
		portVLANScopes := s.routing.PortVLANLookupScopes(resolved.Name, vid)
		routingScopes = append(routingScopes, portVLANScopes...)
		if ifaceName, portRouted, vlanMatched := s.routing.ByPortVLAN(resolved.Name, vid); portRouted {
			matched := vlanMatched && tagAcceptable

			if receive.Reason != "" {
				res := routedPortDrop(resolved.Name, port.ReasonPortDown, routing.RuleStatusDown, receive.Decisive, receive.ForwardingFacts())
				s.forwardingDependencies(ingress).consult(&res)
				res.ConsultScopes(portVLANScopes...)
				return s.finishForward(now, ingress, f, res, mutate), routingScopes, true
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
				res := routedPortDrop(resolved.Name, routing.ReasonNotBridged, ruleID, resolved.Name, []trace.Fact{routing.PortFact(resolved.Name), routing.VLANFact(vid)})
				s.forwardingDependencies(ingress).consult(&res)
				res.ConsultScopes(portVLANScopes...)
				return s.finishForward(now, ingress, f, res, mutate), routingScopes, true
			}

			ownershipScope := s.routing.InterfaceOwnershipScope(ifaceName)
			if s.routing.Receives(ifaceName, f) {
				if mutate {
					s.receiveAndRelease(now, ifaceName, f)
				}
				res := consumedNeighbor(ifaceName, bridge.Ingress{Port: resolved.Name})
				s.forwardingDependencies(ingress).consult(&res)
				res.ConsultScopes(append(portVLANScopes, ownershipScope)...)
				return s.finishForward(now, ingress, f, res, mutate), routingScopes, true
			}
			if !s.routing.Owns(ifaceName, f) {
				res := routedPortDrop(resolved.Name, routing.ReasonNotBridged, routing.RuleNotBridged, resolved.Name, []trace.Fact{port.ForwardingFact(resolved.Name, resolved, true, routing.ReasonNotBridged)})
				s.forwardingDependencies(ingress).consult(&res)
				res.ConsultScopes(append(portVLANScopes, ownershipScope)...)
				return s.finishForward(now, ingress, f, res, mutate), routingScopes, true
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
					return s.finishForward(now, ingress, f, res, mutate), routingScopes, true
				}
			}

			pcp, dei := f.Priority()
			routeRes := s.routing.Route(now, ifaceName, f, mutate)
			if mutate {
				s.emitRouting(now, routeRes.Emissions)
			}
			res := s.assembleRouteResult(s.call, resolved.Name, 0, pcp, dei, nil, ifaceName, ingressFilterRes, routeRes)
			s.forwardingDependencies(ingress).consult(&res)
			res.ConsultScopes(append(portVLANScopes, ownershipScope)...)
			return s.finishForward(now, ingress, f, res, mutate), routingScopes, true
		}
	}
	return bridge.Result{}, routingScopes, false
}

func routedPortDrop(ingress string, reason trace.Reason, rule trace.RuleID, subject string, inputs []trace.Fact) bridge.Result {
	return bridge.Result{Trace: trace.Trace{
		Outcome: trace.Dropped, Reason: reason,
		Steps: []trace.Step{{
			Layer: routing.LayerName, Op: trace.OpDrop, RuleID: rule,
			Subject: trace.Subject{Kind: "port", Key: subject}, Inputs: inputs,
		}},
	}, Ingress: ingress}
}

func (s *Switch) forwardBridge(now time.Time, ingress string, f ethernet.Frame, mutate bool, routingScopes []analysis.Scope) bridge.Result {
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
			res := s.forwardMulticastControl(now, f, in, mutate)
			return s.finishForward(now, ingress, f, res, mutate)
		}
	}

	if s.routing != nil {
		vlanLookupScopes := s.routing.VLANLookupScopes(in.FID)
		in.ConsultScopes(vlanLookupScopes...)
		if iface, ok := s.routing.ByVLAN(in.FID); ok {
			in.ConsultScopes(s.routing.InterfaceOwnershipScope(iface))
			if s.routing.Receives(iface, f) {
				if mutate {
					s.receiveAndRelease(now, iface, f)
				}
				configured, _ := s.routing.Interface(iface)
				if f.Dst == configured.MAC {
					return s.finishForward(now, ingress, f, consumedNeighbor(iface, in), mutate)
				}
			}
			if s.routing.Owns(iface, f) {
				if controlCandidate {
					in = s.commitBridgeLearning(f, in, mutate)
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
				res := s.assembleRouteResult(s.call, in.Port, in.FID, in.PCP, in.DEI, in.Steps, iface, ingressFilterRes, routeRes)
				res.Consult(in.ConsultedPorts()...)
				res.ConsultScopes(in.ConsultedScopes()...)
				return s.finishForward(now, ingress, f, res, mutate)
			}
		}
	} else if metadataHasScopedContent(s.metadata, routing.VRFScope(s.nodeID, routing.DefaultVRF)) {
		in.ConsultScopes(routing.VLANLookupScope(s.nodeID, routing.DefaultVRF, in.FID))
	}

	if controlCandidate {
		in = s.commitBridgeLearning(f, in, mutate)
	}

	return s.finishForward(now, ingress, f, s.bridge.Egress(in, f), mutate)
}

func (s *Switch) commitBridgeLearning(f ethernet.Frame, in bridge.Ingress, mutate bool) bridge.Ingress {
	if !mutate {
		return in
	}
	return s.bridge.Learn(in, f)
}

func consumedNeighbor(iface string, in bridge.Ingress) bridge.Result {
	res := bridge.Result{Trace: trace.Trace{
		Outcome: trace.Consumed,
		Reason:  routing.ReasonNotRouted,
		Steps: append(slices.Clone(in.Steps), trace.Step{
			Layer: routing.LayerName, Op: trace.OpClassify, RuleID: routing.RuleClassify,
			Subject: trace.Subject{Kind: "interface", Key: iface},
			Inputs:  []trace.Fact{routing.PortFact(in.Port), routing.VLANFact(in.FID)},
		}),
	}, Ingress: in.Port, FID: in.FID}
	res.Consult(in.ConsultedPorts()...)
	res.ConsultScopes(in.ConsultedScopes()...)
	return res
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
	resolvedIngress := s.logicalIngress(ingress)
	copies := s.traffic.Copies(switchports, resolvedIngress, res.FID, received, egress)
	copies = s.readyMirrorCopies(now, &res, copies, mutate)
	s.call.copies = append(s.call.copies, copies...)

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
