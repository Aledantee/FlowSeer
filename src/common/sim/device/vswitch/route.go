package vswitch

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// reasonHeldInterfaceUnknown indicates a held frame the routing layer released onto an interface
// the switch's own routing configuration does not resolve. Nothing constructible reaches it:
// the exit names the interface the same layer resolved when it queued the frame, but a frame
// that leaves a hold queue and then reaches no wire is reported rather than dropped in silence.
const reasonHeldInterfaceUnknown trace.Reason = "held-interface-unknown"

// reasonHeldCauseUnknown indicates a held frame the routing layer released under a
// [routing.HeldCause] applyRoutingExits does not recognize. Nothing constructs it (the
// routing layer stamps one of the three defined causes at every release site), but a
// routing package that adds a fourth would otherwise reach this switch with no arm for it and
// vanish with no record, the way the interface case above does not.
const reasonHeldCauseUnknown trace.Reason = "held-cause-unknown"

// NeighborDrop is one frame taken out of a hold queue that could not be put on a wire,
// carrying the reason from whichever stage refused it: the routing layer, for a frame that timed
// out or was pushed out of a full queue, or the bridge or the port table, for a released frame
// the egress then refused. Wake, Age, and an observing Forward can each produce neighbor drop
// records when settling held frames. Reason is that stage's own answer rather than one the reader
// supplies, and Step is the trace step naming it. Port is the egress port the drop is counted against,
// and is empty when no single port owns it (a VLAN interface whose bridge found no candidate at all),
// in which case a consumer counts it against no port rather than inventing an endpoint.
type NeighborDrop struct {
	Step   trace.Step
	Steps  []trace.Step
	Port   string
	Reason trace.Reason
	// Hold and Ingress identify the frame removed from the routing hold queue.
	Hold    routing.HoldToken
	Ingress string
}

// receiveAndRelease processes frame and flushes whatever it freed or gave up on
// in the same call, rather than leaving a resolved entry's queue for a
// caller to notice. [routing.Layer.NextWake] reports a timer for an
// Incomplete, Delay, or Probe entry, so a fabric run that scheduled a wake for the old
// deadline cancels it the moment Receive resolves the entry. Settling on
// Wake, Age, or an observing Forward flushes that queue and any other entry
// whose resolution deadline has separately passed by now.
func (s *Switch) receiveAndRelease(now time.Time, iface string, frame ethernet.Frame) {
	s.emitRouting(now, s.routing.Receive(now, iface, frame).Emissions)
	s.settleRouting(now)
}

func (s *Switch) settleRouting(now time.Time) {
	s.emitRouting(now, s.routing.Advance(now).Emissions)
	s.applyRoutingExits(now, s.routing.DrainExits())
}

// subInterfaceTag returns the one C-TAG a routed sub-interface's egress carries: its own
// VLAN, at the priority the frame arrived with. Any other interface shape reports false —
// a plain routed port leaves the frame untagged, and a VLAN interface with no parent port
// hands off to the bridge, which tags the egress itself. The live path and the held-frame
// release both call it, so the two cannot drift about what a sub-interface puts on the wire.
func subInterfaceTag(iface routing.Interface, pcp vlan.PCP, dei bool) ([]vlan.Tag, bool) {
	if iface.VLAN == 0 || iface.Port == "" {
		return nil, false
	}

	return []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: iface.VLAN, PCP: pcp, DEI: dei}}, true
}

func (s *Switch) assembleRouteResult(
	c *call,
	ingressPort string,
	ingressFID vlan.ID,
	ingressPCP vlan.PCP,
	ingressDEI bool,
	ingressSteps []trace.Step,
	ingressIface string,
	ingressFilterRes filter.Result,
	routeRes routing.Result,
) bridge.Result {
	scopes := append(routeRes.ConsultedScopes(), ingressFilterRes.ConsultedScopes()...)
	outcome, reason := routeRes.Outcome(), routeRes.Reason
	var egressFilterRes filter.Result
	if outcome == trace.Held {
		c.hold = routeRes.Hold
		s.recordNeighborUnresolved(routeRes.VRF, routeRes.Interface, routeRes.NextHop)
	}
	if outcome == trace.Forwarded && s.filter != nil {
		if ingressFilterRes.Decision == filter.DecisionDeferred {
			resolved := s.filter.ResolveDeferred(ingressFilterRes, routeRes.Interface)
			scopes = append(scopes, resolved.ConsultedScopes()...)
			if resolved.Decision != filter.DecisionAccept {
				outcome, reason = trace.Dropped, resolved.Reason
				egressFilterRes = resolved
			} else {
				ingressFilterRes = resolved
			}
		}
		if outcome == trace.Forwarded {
			egressFilterRes = s.filter.EvaluateEgress(routeRes.Interface, ingressIface, routeRes.Frame)
			scopes = append(scopes, egressFilterRes.ConsultedScopes()...)
			if egressFilterRes.Decision != filter.DecisionAccept {
				outcome, reason = trace.Dropped, egressFilterRes.Reason
			}
		}
	}
	steps := routedSteps(ingressSteps, ingressFilterRes, routeRes.Steps, egressFilterRes)
	if outcome == trace.Forwarded {
		return s.putRoutedFrame(c, routeRes.Interface, ingressPort, ingressPCP, ingressDEI, routeRes.Frame, steps, scopes)
	}
	res := bridge.Result{
		Trace:   trace.Trace{Outcome: outcome, Reason: reason, Steps: steps},
		Ingress: ingressPort, FID: ingressFID,
	}
	if routeRes.Interface != "" {
		s.routingForwardingDependencies(routeRes.Interface).consult(&res)
	}
	res.ConsultScopes(scopes...)
	return res
}

func routedSteps(ingress []trace.Step, in filter.Result, route []trace.Step, out filter.Result) []trace.Step {
	steps := make([]trace.Step, 0, len(ingress)+len(in.Steps)+len(route)+len(out.Steps)+1)
	steps = append(steps, ingress...)
	if in.Decision == filter.DecisionAccept {
		steps = append(steps, in.Steps...)
	}
	steps = append(steps, route...)
	steps = append(steps, out.Steps...)
	return steps
}

// putRoutedFrame uses the relay for a VLAN interface and the port table for a
// routed port, sub-interface, or LAG. Live routes and held releases share it.
func (s *Switch) putRoutedFrame(
	c *call, ifaceName, ingress string, pcp vlan.PCP, dei bool,
	frame ethernet.Frame, steps []trace.Step, scopes []analysis.Scope,
) bridge.Result {
	// Both callers resolve the interface before attempting egress.
	iface, _ := s.routing.Interface(ifaceName)
	if iface.VLAN != 0 && iface.Port == "" {
		in := bridge.Ingress{FID: iface.VLAN, PCP: pcp, DEI: dei, Steps: steps, Now: c.now, Commit: c.commit}
		in.ConsultScopes(scopes...)
		res := s.bridge.Egress(in, frame)
		res.FID, res.Ingress = iface.VLAN, ingress
		return res
	}
	if tags, ok := subInterfaceTag(iface, pcp, dei); ok {
		frame.Tags = append(tags, frame.Tags...)
	}
	p, _ := s.ports.Port(iface.Port)
	reason := s.ports.Transmit(iface.Port, len(frame.Payload))
	op, rule := trace.OpTransmit, routing.RuleTransmit
	var inputs []trace.Fact
	member := ""
	if reason != "" {
		op, rule = trace.OpDrop, trace.RuleID(routing.RuleStatusPrefix+string(reason))
		inputs = []trace.Fact{port.ForwardingFact(iface.Port, p, false, reason)}
	} else if p.Kind == port.LAG {
		scopes = append(scopes, s.aggregatorScope(iface.Port))
		sel, fact := s.selectMemberWithFact(c.now, iface.Port, frame, 0, c.commit)
		inputs = traceFacts(fact)
		if !sel.OK {
			reason = bridge.ReasonNoMember
			op, rule = trace.OpDrop, routing.RuleEgressNoMember
		} else {
			member = sel.Member
		}
	}
	step := trace.Step{
		Layer: routing.LayerName, Op: op, RuleID: rule,
		Subject: trace.Subject{Kind: "port", Key: iface.Port}, Inputs: inputs,
		Outputs: []trace.Fact{routing.EgressFact(ifaceName, iface.Port, member, reason)},
	}
	outcome := trace.Forwarded
	if reason != "" {
		outcome = trace.Dropped
	}
	res := bridge.Result{
		Trace:   trace.Trace{Outcome: outcome, Reason: reason, Steps: append(steps, step)},
		Ingress: ingress,
		Egress:  []bridge.Egress{{Port: iface.Port, Member: member, Frame: frame, PCP: pcp, Dropped: reason}},
	}
	s.egressDependencies(iface.Port).consult(&res)
	res.ConsultScopes(scopes...)
	return res
}

func (s *Switch) emitRouting(now time.Time, emissions []layer.Emission) {
	for _, em := range emissions {
		func() {
			_, end := s.beginCall(now, true)
			defer end()

			if em.Port == "" {
				res := s.bridge.Egress(bridge.Ingress{FID: em.VID, Now: now, Commit: true}, em.Frame)
				for _, eg := range res.Egress {
					if eg.Dropped == "" {
						s.emit(Emission{Port: eg.Port, Member: eg.Member, Frame: eg.Frame, Protocol: true})
					}
				}
				return
			}
			frame := em.Frame
			if tags, ok := subInterfaceTag(routing.Interface{Port: em.Port, VLAN: em.VID}, 0, false); ok {
				frame.Tags = append(tags, frame.Tags...)
			}
			if s.ports.Transmit(em.Port, len(frame.Payload)) != "" {
				return
			}
			p, _ := s.ports.Port(em.Port)
			if p.Kind == port.LAG && !s.selectOrPeekMember(now, em.Port, frame, 0, true).OK {
				return
			}
			s.emit(Emission{Port: em.Port, Frame: frame, Protocol: true})
		}()
	}
}

// applyRoutingExits turns the routing layer's hold-queue exits into what
// an observing forward, [Switch.Wake], or [Switch.Age] caller can observe: a released frame becomes an Emission
// with Protocol false, since it is ordinary data the switch is finally able
// to send rather than a protocol frame of the switch's own, and a frame that
// left the queue any other way becomes a [NeighborDrop] under the reason its
// own cause names rather than vanishing silently.
//
// A timeout keeps [routing.ReasonNeighborMiss]: nothing ever answered for the
// next hop, which is what that reason says. An eviction gets its own, because
// the neighbor it was queued for may well have resolved, often on this very
// call.
func (s *Switch) applyRoutingExits(now time.Time, exits []routing.HeldFrame) {
	for _, hf := range exits {
		switch hf.Cause {
		case routing.HeldReleased:
			s.releaseHeldFrame(now, hf)
		case routing.HeldTimedOut:
			s.recordHeldExitDrop(hf, routing.ReasonNeighborMiss)
		case routing.HeldEvicted:
			s.recordHeldExitDrop(hf, routing.ReasonNeighborHoldOverflow)
		default:
			// Nothing in this package constructs a fourth HeldCause today, but HeldCause is a
			// bare string type, and reasonHeldInterfaceUnknown already chose a record over
			// silence for its own unreachable case; a cause this switch on hf.Cause does not
			// recognize gets the same treatment rather than vanishing with no emission and no
			// NeighborDrop.
			s.recordHeldExitDrop(hf, reasonHeldCauseUnknown)
		}
	}
}

// recordHeldExitDrop records a held frame the routing layer itself refused, so the subject is
// the interface it was held on rather than a port: no egress was attempted.
func (s *Switch) recordHeldExitDrop(hf routing.HeldFrame, reason trace.Reason) {
	s.call.drops = append(s.call.drops, NeighborDrop{
		Step: trace.Step{
			Layer:   routing.LayerName,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(reason),
			Subject: trace.Subject{Kind: "interface", Key: hf.Interface},
			Outputs: []trace.Fact{routing.EgressFact(hf.Interface, hf.Port, "", reason)},
		},
		Port: hf.Port, Reason: reason, Hold: hf.Token, Ingress: hf.Ingress,
	})
}

// recordHeldEgressDrop records a released frame an egress stage refused, under the reason that
// stage gave. ruleID is separate from reason because a port-table refusal is namespaced as a
// port status while the counter it feeds is keyed by the bare reason.
func (s *Switch) recordHeldEgressDrop(hf routing.HeldFrame, egressPort, member string, ruleID trace.RuleID, reason trace.Reason, steps []trace.Step) {
	subject := trace.Subject{Kind: "port", Key: egressPort}
	if egressPort == "" {
		// No port was chosen, so the interface is the most specific thing the drop is about.
		subject = trace.Subject{Kind: "interface", Key: hf.Interface}
	}
	s.call.drops = append(s.call.drops, NeighborDrop{
		Step: trace.Step{
			Layer:   routing.LayerName,
			Op:      trace.OpDrop,
			RuleID:  ruleID,
			Subject: subject,
			Outputs: []trace.Fact{routing.EgressFact(hf.Interface, egressPort, member, reason)},
		},
		Port: egressPort, Reason: reason, Steps: steps, Hold: hf.Token, Ingress: hf.Ingress,
	})
}

// releaseHeldFrame evaluates the current filter bindings before using the same
// egress as a live route. A refused frame retains its hold identity in the drop.
func (s *Switch) releaseHeldFrame(now time.Time, hf routing.HeldFrame) {
	c, end := s.beginCall(now, true)
	defer end()

	if _, ok := s.routing.Interface(hf.Interface); !ok {
		s.recordHeldEgressDrop(hf, "", "", trace.RuleID(reasonHeldInterfaceUnknown), reasonHeldInterfaceUnknown, nil)
		return
	}
	var in, out filter.Result
	if s.filter != nil {
		if hf.Ingress != "" {
			in = s.filter.EvaluateIngress(hf.Ingress, hf.Frame)
			if in.Decision == filter.DecisionDeferred {
				in = s.filter.ResolveDeferred(in, hf.Interface)
			}
			if in.Decision != filter.DecisionAccept {
				s.call.drops = append(s.call.drops, NeighborDrop{
					Step: in.Steps[len(in.Steps)-1], Steps: in.Steps,
					Reason: in.Reason, Hold: hf.Token, Ingress: hf.Ingress,
				})
				return
			}
		}
		out = s.filter.EvaluateEgress(hf.Interface, hf.Ingress, hf.Frame)
		if out.Decision != filter.DecisionAccept {
			steps := routedSteps(nil, in, nil, out)
			s.call.drops = append(s.call.drops, NeighborDrop{
				Step: out.Steps[len(out.Steps)-1], Steps: steps,
				Reason: out.Reason, Hold: hf.Token, Ingress: hf.Ingress,
			})
			return
		}
	}
	res := s.putRoutedFrame(c, hf.Interface, "", hf.PCP, hf.DEI, hf.Frame, routedSteps(nil, in, nil, out), nil)
	if len(res.Egress) == 0 {
		reason := res.Reason
		if reason == "" {
			reason = bridge.ReasonNoEgress
		}
		s.recordHeldEgressDrop(hf, "", "", trace.RuleID(reason), reason, res.Steps)
		return
	}
	iface, _ := s.routing.Interface(hf.Interface)
	for _, eg := range res.Egress {
		if eg.Dropped != "" {
			rule := trace.RuleID(eg.Dropped)
			if iface.Port != "" {
				rule = res.Steps[len(res.Steps)-1].RuleID
			}
			s.recordHeldEgressDrop(hf, eg.Port, eg.Member, rule, eg.Dropped, res.Steps)
			continue
		}
		if !s.emit(Emission{
			Port: eg.Port, Member: eg.Member, Frame: eg.Frame, PCP: eg.PCP,
			Hold: hf.Token, Ingress: hf.Ingress,
		}) {
			s.recordHeldEgressDrop(hf, eg.Port, eg.Member, traffic.RuleMirrorEgressDrop, traffic.ReasonMirrorOutput, res.Steps)
		}
	}
}
