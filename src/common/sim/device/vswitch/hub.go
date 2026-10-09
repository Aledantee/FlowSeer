package vswitch

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func (s *Switch) hubIngress(ingress string) (bridge.Result, bool) {
	var res bridge.Result
	res.Outcome = trace.Dropped
	res.FID = 0
	s.forwardingDependencies(ingress).consult(&res)

	p, ok := s.ports.Port(ingress)
	if !ok {
		res.Reason = port.ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerName,
			Op:      trace.OpDrop,
			RuleID:  port.RuleStatusNotFound,
			Subject: trace.Subject{Kind: "port", Key: ingress},
			Outputs: []trace.Fact{port.ForwardingFact(ingress, port.Port{}, false, port.ReasonPortDown)},
		})

		return res, false
	}
	res.Ingress = p.Name
	if !p.Forwards() {
		res.Reason = port.ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerName,
			Op:      trace.OpDrop,
			RuleID:  port.RuleStatusDown,
			Subject: trace.Subject{Kind: "port", Key: p.Name},
			Inputs:  []trace.Fact{port.ForwardingFact(ingress, p, false, port.ReasonPortDown)},
		})

		return res, false
	}
	resolved, ok := s.ports.Resolve(ingress)
	if !ok {
		res.Reason = port.ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerName,
			Op:      trace.OpDrop,
			RuleID:  port.RuleLAGParentNotFound,
			Subject: trace.Subject{Kind: "port", Key: ingress},
			Inputs:  []trace.Fact{port.ForwardingFact(ingress, p, false, port.ReasonPortDown)},
		})

		return res, false
	}
	res.Ingress = resolved.Name

	if !resolved.Forwards() {
		res.Reason = port.ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerName,
			Op:      trace.OpDrop,
			RuleID:  port.RuleStatusDown,
			Subject: trace.Subject{Kind: "port", Key: resolved.Name},
			Inputs:  []trace.Fact{port.ForwardingFact(ingress, p, true, ""), port.ForwardingFact(resolved.Name, resolved, false, port.ReasonPortDown)},
		})

		return res, false
	}

	return res, true
}

func (s *Switch) forwardHub(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	res, admitted := s.hubIngress(ingress)
	if !admitted {
		return res
	}
	pcp, _ := f.Priority()

	var candidates []port.Port
	var eligible int
	var unavailable []trace.Fact
	for _, cand := range s.ports.Ports() {
		if cand.LagParent != "" || cand.Name == res.Ingress {
			continue
		}
		eligible++
		s.egressDependencies(cand.Name).consult(&res)
		if !cand.Forwards() {
			unavailable = append(unavailable, port.ForwardingFact(cand.Name, cand, false, port.ReasonPortDown))
			continue
		}
		candidates = append(candidates, cand)
	}

	if len(candidates) == 0 {
		res.Reason = bridge.ReasonNoEgress
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerName,
			Op:      trace.OpDrop,
			RuleID:  port.RuleHubNoEgress,
			Subject: trace.Subject{Kind: "port", Key: res.Ingress},
			Inputs:  unavailable,
			Outputs: []trace.Fact{port.HubEgressFact(eligible, 0, bridge.ReasonNoEgress)},
		})
		return res
	}

	var transmitted int
	for _, cand := range candidates {
		if cand.MTU > 0 && len(f.Payload) > cand.MTU {
			res.Egress = append(res.Egress, bridge.Egress{
				Port:    cand.Name,
				Frame:   f,
				PCP:     pcp,
				Dropped: port.ReasonMTUExceeded,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:   port.LayerName,
				Op:      trace.OpDrop,
				RuleID:  port.RuleStatusMTUExceeded,
				Subject: trace.Subject{Kind: "port", Key: cand.Name},
				Inputs:  []trace.Fact{port.ForwardingFact(cand.Name, cand, false, port.ReasonMTUExceeded)},
			})

			continue
		}

		var member string
		var selection trace.Fact
		if cand.Kind == port.LAG {
			res.ConsultScopes(s.aggregatorScope(cand.Name))
			sel, fact := s.selectMemberWithFact(now, cand.Name, f, 0, mutate)
			selection = fact
			if !sel.OK {
				res.Egress = append(res.Egress, bridge.Egress{
					Port:    cand.Name,
					Frame:   f,
					PCP:     pcp,
					Dropped: bridge.ReasonNoMember,
				})
				res.Steps = append(res.Steps, trace.Step{
					Layer:   port.LayerName,
					Op:      trace.OpDrop,
					RuleID:  port.RuleEgressNoMember,
					Subject: trace.Subject{Kind: "port", Key: cand.Name},
					Inputs:  []trace.Fact{selection},
				})

				continue
			}
			member = sel.Member
		}

		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerName,
			Op:      trace.OpReplicate,
			RuleID:  port.RuleHubReplicate,
			Subject: trace.Subject{Kind: "port", Key: cand.Name},
			Inputs:  traceFacts(selection),
			Outputs: []trace.Fact{port.ForwardingFact(cand.Name, cand, true, "")},
		})
		res.Egress = append(res.Egress, bridge.Egress{
			Port:   cand.Name,
			Member: member,
			Frame:  f,
			PCP:    pcp,
		})
		transmitted++
	}

	if transmitted > 0 {
		res.Outcome = trace.Flooded
	} else {
		res.Outcome = trace.Dropped
		if len(res.Egress) > 0 {
			res.Reason = res.Egress[0].Dropped
		} else {
			res.Reason = port.ReasonMTUExceeded
		}
	}

	return res
}
