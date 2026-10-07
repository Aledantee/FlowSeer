package vswitch

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
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
	Port   string
	Reason trace.Reason
}

// observationInterface finds the routed interface an ARP or Neighbor Discovery
// frame arrived on, for [routing.Layer.Receive] alone: it never mutates the bridge's
// forwarding database or admits the frame anywhere, so calling it ahead of
// the routed-port and bridge blocks below cannot pre-empt what either one
// decides. A routed port resolves through the same [routing.Layer.ByPortVLAN]
// lookup that block uses, so an advertisement binds the sub-interface its
// outer VID names and no other; a routed port whose lookup misses observes
// nothing, since it has no bridge membership to fall through to. Any other
// ingress classifies through a read-only bridge pass to find its VLAN, then
// resolves through [routing.Layer.ByVLAN] the way the bridge block does.
func (s *Switch) observationInterface(now time.Time, ingress string, f ethernet.Frame) (string, bool) {
	receive := s.ports.Receive(ingress)
	resolved := receive.Resolved
	if resolved.Name != "" && receive.Reason == "" {
		vid, tagAcceptable := f.OuterVID()
		if iface, portRouted, vlanMatched := s.routing.ByPortVLAN(resolved.Name, vid); portRouted {
			return iface, vlanMatched && tagAcceptable
		}
	}
	if s.bridge == nil {
		return "", false
	}
	in, _, ok := s.bridge.Ingress(now, ingress, f, false, false)
	if !ok {
		return "", false
	}
	return s.routing.ByVLAN(in.FID)
}

// receiveAndRelease processes frame and flushes whatever it freed or gave up on
// in the same call, rather than leaving a resolved entry's queue for a
// caller to notice. [routing.Layer.NextWake] reports a timer for an
// Incomplete, Delay, or Probe entry, so a fabric run that scheduled a wake for the old
// deadline cancels it the moment Receive resolves the entry. Settling on
// Wake, Age, or an observing Forward flushes that queue and any other entry
// whose resolution deadline has separately passed by now.
//
// A release reached through applyRoutingExits can itself commit a LAG
// selection or hit an unresolved neighbor, on a path the observing frame
// never traversed. It is a queued frame going out an egress interface of
// its own, not the frame forward is currently processing. Left alone that
// would charge the release's LAG rebalance or neighbor-unresolved issues to
// the observing frame's result, so receiveAndRelease saves
// s.lagRebalanceHits and s.neighborUnresolvedHits, then clears both to their
// zero value before the release runs, so the release always writes into a
// fresh map and slice rather than one this call is about to hand back:
// s.lagRebalanceHits is a map, and restoring a saved map reference alone,
// without first clearing the field, would not undo writes the release makes
// into that same map, since the saved variable names the same underlying
// map the release just mutated. Clearing first means the release can only
// ever write somewhere this call discards, whatever it does. The observing
// frame's own processing, which runs after this call returns, then records
// its own hits onto the state this call restores, exactly as if the release
// in between had never happened.
func (s *Switch) receiveAndRelease(now time.Time, iface string, frame ethernet.Frame) {
	savedLAGRebalanceHits := s.lagRebalanceHits
	savedNeighborUnresolvedHits := s.neighborUnresolvedHits

	s.lagRebalanceHits = nil
	s.neighborUnresolvedHits = nil

	s.emitRouting(now, s.routing.Receive(now, iface, frame).Emissions)
	s.emitRouting(now, s.routing.Advance(now).Emissions)
	s.applyRoutingExits(now, s.routing.DrainExits())

	s.lagRebalanceHits = savedLAGRebalanceHits
	s.neighborUnresolvedHits = savedNeighborUnresolvedHits
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
	now time.Time,
	ingressPort string,
	ingressFID vlan.ID,
	ingressPCP vlan.PCP,
	ingressDEI bool,
	ingressSteps []trace.Step,
	ingressIface string,
	ingressFilterRes filter.Result,
	routeRes routing.Result,
	mutate bool,
) bridge.Result {
	routeScopes := routeRes.ConsultedScopes()
	if len(ingressFilterRes.ConsultedScopes()) > 0 {
		routeScopes = append(routeScopes, ingressFilterRes.ConsultedScopes()...)
	}
	if outcome := routeRes.Outcome(); outcome != trace.Forwarded {
		if outcome == trace.Held {
			s.recordNeighborUnresolved(routeRes.VRF, routeRes.Interface, routeRes.NextHop)
		}
		steps := make([]trace.Step, 0, len(ingressSteps)+len(ingressFilterRes.Steps)+len(routeRes.Steps))
		steps = append(steps, ingressSteps...)
		if ingressFilterRes.Decision == filter.DecisionAccept {
			steps = append(steps, ingressFilterRes.Steps...)
		}
		steps = append(steps, routeRes.Steps...)

		res := bridge.Result{
			Trace: trace.Trace{
				Outcome: outcome,
				Reason:  routeRes.Reason,
				Steps:   steps,
			},
			Ingress: ingressPort,
			FID:     ingressFID,
		}
		if routeRes.Interface != "" {
			s.routingForwardingDependencies(routeRes.Interface).consult(&res)
		}
		res.ConsultScopes(routeScopes...)

		return res
	}

	// Route names only an interface of its own table, so the lookup cannot miss.
	egressIface, _ := s.routing.Interface(routeRes.Interface)

	if ingressFilterRes.Decision == filter.DecisionDeferred && s.filter != nil {
		resolved := s.filter.ResolveDeferred(ingressFilterRes, routeRes.Interface)
		routeScopes = append(routeScopes, resolved.ConsultedScopes()...)
		if resolved.Decision != filter.DecisionAccept {
			steps := make([]trace.Step, 0, len(ingressSteps)+len(routeRes.Steps)+len(resolved.Steps))
			steps = append(steps, ingressSteps...)
			steps = append(steps, routeRes.Steps...)
			steps = append(steps, resolved.Steps...)
			res := bridge.Result{
				Trace: trace.Trace{
					Outcome: trace.Dropped,
					Reason:  resolved.Reason,
					Steps:   steps,
				},
				Ingress: ingressPort,
				FID:     ingressFID,
			}
			if routeRes.Interface != "" {
				s.routingForwardingDependencies(routeRes.Interface).consult(&res)
			}
			res.ConsultScopes(routeScopes...)
			return res
		}
		ingressFilterRes = resolved
	}

	var egressFilterRes filter.Result
	if s.filter != nil {
		egressFilterRes = s.filter.EvaluateEgress(routeRes.Interface, ingressIface, routeRes.Frame)
		routeScopes = append(routeScopes, egressFilterRes.ConsultedScopes()...)
		if egressFilterRes.Decision != filter.DecisionAccept {
			steps := make([]trace.Step, 0, len(ingressSteps)+len(ingressFilterRes.Steps)+len(routeRes.Steps)+len(egressFilterRes.Steps))
			steps = append(steps, ingressSteps...)
			if ingressFilterRes.Decision == filter.DecisionAccept {
				steps = append(steps, ingressFilterRes.Steps...)
			}
			steps = append(steps, routeRes.Steps...)
			steps = append(steps, egressFilterRes.Steps...)
			res := bridge.Result{
				Trace: trace.Trace{
					Outcome: trace.Dropped,
					Reason:  egressFilterRes.Reason,
					Steps:   steps,
				},
				Ingress: ingressPort,
				FID:     ingressFID,
			}
			if routeRes.Interface != "" {
				s.routingForwardingDependencies(routeRes.Interface).consult(&res)
			}
			res.ConsultScopes(routeScopes...)
			return res
		}
	}

	if egressIface.VLAN != 0 && egressIface.Port == "" {
		stepsSoFar := make([]trace.Step, 0, len(ingressSteps)+len(ingressFilterRes.Steps)+len(routeRes.Steps)+len(egressFilterRes.Steps))
		stepsSoFar = append(stepsSoFar, ingressSteps...)
		if ingressFilterRes.Decision == filter.DecisionAccept {
			stepsSoFar = append(stepsSoFar, ingressFilterRes.Steps...)
		}
		stepsSoFar = append(stepsSoFar, routeRes.Steps...)
		if egressFilterRes.Decision == filter.DecisionAccept {
			stepsSoFar = append(stepsSoFar, egressFilterRes.Steps...)
		}

		bridgeIn := bridge.Ingress{
			Port:   "",
			FID:    egressIface.VLAN,
			PCP:    ingressPCP,
			DEI:    ingressDEI,
			Steps:  stepsSoFar,
			Now:    now,
			Commit: mutate,
		}
		bridgeIn.ConsultScopes(routeScopes...)
		res := s.bridge.Egress(bridgeIn, routeRes.Frame)
		res.FID = egressIface.VLAN
		res.Ingress = ingressPort
		return res
	}

	steps := make([]trace.Step, 0, len(ingressSteps)+len(ingressFilterRes.Steps)+len(routeRes.Steps)+len(egressFilterRes.Steps)+1)
	steps = append(steps, ingressSteps...)
	if ingressFilterRes.Decision == filter.DecisionAccept {
		steps = append(steps, ingressFilterRes.Steps...)
	}
	steps = append(steps, routeRes.Steps...)
	if egressFilterRes.Decision == filter.DecisionAccept {
		steps = append(steps, egressFilterRes.Steps...)
	}

	// A sub-interface's egress carries the outer tag its parent port classifies on; a plain
	// routed port (VLAN zero) leaves untagged. Applied before the transmit and LAG checks
	// below, because their refusal-path egress records embed this same frame.
	if tags, ok := subInterfaceTag(egressIface, ingressPCP, ingressDEI); ok {
		routeRes.Frame.Tags = append(tags, routeRes.Frame.Tags...)
	}

	txReason := s.ports.Transmit(egressIface.Port, len(routeRes.Frame.Payload))
	if txReason != "" {
		egressPort, _ := s.ports.Port(egressIface.Port)
		steps = append(steps, trace.Step{
			Layer:   routing.LayerName,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(routing.RuleStatusPrefix + string(txReason)),
			Subject: trace.Subject{Kind: "port", Key: egressIface.Port},
			Inputs:  []trace.Fact{port.ForwardingFact(egressIface.Port, egressPort, false, txReason)},
			Outputs: []trace.Fact{routing.EgressFact(routeRes.Interface, egressIface.Port, "", txReason)},
		})
		res := bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  txReason,
				Steps:   steps,
			},
			Ingress: ingressPort,
			FID:     0,
			Egress: []bridge.Egress{
				{
					Port:    egressIface.Port,
					Member:  "",
					Frame:   routeRes.Frame,
					PCP:     ingressPCP,
					Dropped: txReason,
				},
			},
		}
		s.egressDependencies(egressIface.Port).consult(&res)
		res.ConsultScopes(routeScopes...)
		return res
	}

	p, _ := s.ports.Port(egressIface.Port)
	var (
		member    string
		selection trace.Fact
	)
	resultScopes := routeScopes
	if p.Kind == port.LAG {
		resultScopes = append(resultScopes, s.aggregatorScope(egressIface.Port))
		sel, fact := s.selectMemberWithFact(now, egressIface.Port, routeRes.Frame, 0, mutate)
		selection = fact
		if !sel.OK {
			steps = append(steps, trace.Step{
				Layer:   routing.LayerName,
				Op:      trace.OpDrop,
				RuleID:  routing.RuleEgressNoMember,
				Subject: trace.Subject{Kind: "port", Key: egressIface.Port},
				Inputs:  []trace.Fact{selection},
				Outputs: []trace.Fact{routing.EgressFact(routeRes.Interface, egressIface.Port, "", bridge.ReasonNoMember)},
			})
			res := bridge.Result{
				Trace: trace.Trace{
					Outcome: trace.Dropped,
					Reason:  bridge.ReasonNoMember,
					Steps:   steps,
				},
				Ingress: ingressPort,
				FID:     0,
				Egress: []bridge.Egress{
					{
						Port:    egressIface.Port,
						Frame:   routeRes.Frame,
						PCP:     ingressPCP,
						Dropped: bridge.ReasonNoMember,
					},
				},
			}
			s.egressDependencies(egressIface.Port).consult(&res)
			res.ConsultScopes(resultScopes...)
			return res
		}
		member = sel.Member
	}

	steps = append(steps, trace.Step{
		Layer:   routing.LayerName,
		Op:      trace.OpTransmit,
		RuleID:  routing.RuleTransmit,
		Subject: trace.Subject{Kind: "port", Key: egressIface.Port},
		Inputs:  traceFacts(selection),
		Outputs: []trace.Fact{routing.EgressFact(routeRes.Interface, egressIface.Port, member, "")},
	})
	res := bridge.Result{
		Trace: trace.Trace{
			Outcome: trace.Forwarded,
			Steps:   steps,
		},
		Ingress: ingressPort,
		FID:     0,
		Egress: []bridge.Egress{
			{
				Port:   egressIface.Port,
				Member: member,
				Frame:  routeRes.Frame,
				PCP:    ingressPCP,
			},
		},
	}
	s.egressDependencies(egressIface.Port).consult(&res)
	res.ConsultScopes(resultScopes...)
	return res
}

func (s *Switch) emitRouting(now time.Time, emissions []layer.Emission) {
	savedLAG := s.lagRebalanceHits
	savedNeighbor := s.neighborUnresolvedHits
	savedMcast := s.mcastQueryUnobservedHits
	s.lagRebalanceHits = nil
	s.neighborUnresolvedHits = nil
	s.mcastQueryUnobservedHits = nil
	for _, em := range emissions {
		if em.Port == "" {
			res := s.bridge.Egress(bridge.Ingress{FID: em.VID, Now: now, Commit: true}, em.Frame)
			for _, eg := range res.Egress {
				if eg.Dropped == "" {
					s.emissions = append(s.emissions, Emission{Port: eg.Port, Frame: eg.Frame, Protocol: true})
				}
			}
			continue
		}
		frame := em.Frame
		if tags, ok := subInterfaceTag(routing.Interface{Port: em.Port, VLAN: em.VID}, 0, false); ok {
			frame.Tags = append(tags, frame.Tags...)
		}
		if s.ports.Transmit(em.Port, len(frame.Payload)) != "" {
			continue
		}
		p, _ := s.ports.Port(em.Port)
		if p.Kind == port.LAG && !s.selectOrPeekMember(now, em.Port, frame, 0, true).OK {
			continue
		}
		s.emissions = append(s.emissions, Emission{Port: em.Port, Frame: frame, Protocol: true})
	}
	s.lagRebalanceHits = savedLAG
	s.neighborUnresolvedHits = savedNeighbor
	s.mcastQueryUnobservedHits = savedMcast
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
	s.neighborFailures = append(s.neighborFailures, NeighborDrop{
		Step: trace.Step{
			Layer:   routing.LayerName,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(reason),
			Subject: trace.Subject{Kind: "interface", Key: hf.Interface},
			Outputs: []trace.Fact{routing.EgressFact(hf.Interface, hf.Port, "", reason)},
		},
		Port:   hf.Port,
		Reason: reason,
	})
}

// recordHeldEgressDrop records a released frame an egress stage refused, under the reason that
// stage gave. ruleID is separate from reason because a port-table refusal is namespaced as a
// port status while the counter it feeds is keyed by the bare reason.
func (s *Switch) recordHeldEgressDrop(hf routing.HeldFrame, egressPort, member string, ruleID trace.RuleID, reason trace.Reason) {
	subject := trace.Subject{Kind: "port", Key: egressPort}
	if egressPort == "" {
		// No port was chosen, so the interface is the most specific thing the drop is about.
		subject = trace.Subject{Kind: "interface", Key: hf.Interface}
	}
	s.neighborFailures = append(s.neighborFailures, NeighborDrop{
		Step: trace.Step{
			Layer:   routing.LayerName,
			Op:      trace.OpDrop,
			RuleID:  ruleID,
			Subject: subject,
			Outputs: []trace.Fact{routing.EgressFact(hf.Interface, egressPort, member, reason)},
		},
		Port:   egressPort,
		Reason: reason,
	})
}

// releaseHeldFrame resolves the port a released frame for routed interface
// hf.Interface leaves through, the same egress path [Switch.assembleRouteResult]
// builds for a route resolved live: a VLAN interface with no parent port goes out
// through [bridge.Layer.Egress] with the synthetic ingress a routed frame already
// uses, while a routed port's frame transmits directly, through its LAG member
// selection if it has one, and a sub-interface takes that same direct path with
// subInterfaceTag's C-TAG prepended. Every way out of here that is not an
// emission is a [NeighborDrop] rather than a silently discarded frame, because a released
// frame the bridge or port refuses is a different answer from one that was
// never held — and an SVI whose only member port is down produces no egress
// entry at all, so the bridge's own no-candidate reason is the only thing
// that can speak for it.
func (s *Switch) releaseHeldFrame(now time.Time, hf routing.HeldFrame) {
	egressIface, ok := s.routing.Interface(hf.Interface)
	if !ok {
		s.recordHeldEgressDrop(hf, "", "", trace.RuleID(reasonHeldInterfaceUnknown), reasonHeldInterfaceUnknown)

		return
	}

	if egressIface.VLAN != 0 && egressIface.Port == "" {
		res := s.bridge.Egress(bridge.Ingress{FID: egressIface.VLAN, PCP: hf.PCP, DEI: hf.DEI, Now: now, Commit: true}, hf.Frame)
		if len(res.Egress) == 0 {
			// bridge.replicate returns no egress entry at all when no port ever becomes a
			// flood candidate, naming the reason on the result instead, and a caller reading
			// only res.Egress would see nothing. Egress reaches replicate on a unicast miss, on
			// a flood VLAN, and on a group destination, whether or not a resolver decided it —
			// see TestReleaseOntoFloodVLANWithNoMemberRecordsTheBridgesReason for the flood-VLAN
			// case. The guard stays because the frame is already out of the hold queue by here,
			// so the day a fourth path reaches replicate with no candidates, the frame is gone
			// with no record at all unless this still catches it.
			reason := res.Reason
			if reason == "" {
				reason = bridge.ReasonNoEgress
			}
			s.recordHeldEgressDrop(hf, "", "", trace.RuleID(reason), reason)

			return
		}
		for _, eg := range res.Egress {
			if eg.Dropped != "" {
				s.recordHeldEgressDrop(hf, eg.Port, eg.Member, trace.RuleID(eg.Dropped), eg.Dropped)

				continue
			}
			s.emissions = append(s.emissions, Emission{Port: eg.Port, Frame: eg.Frame, PCP: eg.PCP})
		}

		return
	}

	// A sub-interface leaves by its parent port, tagged, exactly as the live path leaves it.
	// The tag goes on here rather than at the emission so this function and assembleRouteResult
	// read as one shape, with the same three stages in the same order.
	if tags, ok := subInterfaceTag(egressIface, hf.PCP, hf.DEI); ok {
		hf.Frame.Tags = append(tags, hf.Frame.Tags...)
	}

	txReason := s.ports.Transmit(egressIface.Port, len(hf.Frame.Payload))
	if txReason != "" {
		s.recordHeldEgressDrop(hf, egressIface.Port, "", trace.RuleID(routing.RuleStatusPrefix+string(txReason)), txReason)

		return
	}

	p, _ := s.ports.Port(egressIface.Port)
	if p.Kind == port.LAG {
		sel := s.selectOrPeekMember(now, egressIface.Port, hf.Frame, 0, true)
		if !sel.OK {
			s.recordHeldEgressDrop(hf, egressIface.Port, "", routing.RuleEgressNoMember, bridge.ReasonNoMember)

			return
		}
	}

	// A routed port has no bridge egress decision to read a priority off, so the frame keeps the
	// one it arrived with, which is what the live path transmits for the same frame.
	s.emissions = append(s.emissions, Emission{Port: egressIface.Port, Frame: hf.Frame, PCP: hf.PCP})
}
