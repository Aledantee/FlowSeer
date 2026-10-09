package vswitch

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Advance settles all layer timers due at or before now.
func (s *Switch) Advance(now time.Time) device.Output {
	c, end := s.beginCall(now, true)
	defer end()
	s.advance(now)
	return c.output(forwardResult{})
}

// Receive settles timers before policing and handling an arriving frame.
func (s *Switch) Receive(now time.Time, ingress string, frame ethernet.Frame) device.Output {
	return s.receive(now, ingress, frame, false)
}

// ReceiveMirrored handles a mirror arrival without policing or producing copies of its own
// frame. Copies of held frames the arrival releases are still reported.
func (s *Switch) ReceiveMirrored(now time.Time, ingress string, frame ethernet.Frame) device.Output {
	return s.receive(now, ingress, frame, true)
}

func (s *Switch) receive(now time.Time, ingress string, frame ethernet.Frame, mirrored bool) device.Output {
	c, end := s.beginCall(now, true)
	defer end()
	s.advance(now)
	c.mirrored = mirrored
	var res bridge.Result
	if !mirrored && !s.police(now, ingress, frame.WireOctets()) {
		policer := s.cfg.Traffic.Policers[ingress]
		res = bridge.Result{Trace: trace.Trace{
			Outcome: trace.Dropped, Reason: traffic.ReasonPoliced,
			Steps: []trace.Step{{
				Layer: traffic.LayerName, Op: trace.OpDrop,
				RuleID: traffic.RulePolicerRefuse, Subject: trace.Subject{Kind: "port", Key: ingress},
				Outputs: []trace.Fact{traffic.PolicerDecisionFact(policer.RateBPS, policer.BurstOctets, frame.WireOctets(), false)},
			}},
		}, Ingress: ingress}
		s.forwardingDependencies(ingress).consult(&res)
	} else {
		res = s.forward(now, ingress, frame, true)
	}
	if mirrored {
		// A frame that arrived as a mirror copy makes none of its own. Copies stamped
		// with a hold token belong to the frames its arrival released.
		c.copies = slices.DeleteFunc(c.copies, func(cp mirrorCopy) bool { return cp.Hold == 0 })
	}
	out := c.output(s.wrapResult(c, res))
	out.Policed = res.Reason == traffic.ReasonPoliced
	return out
}

func (c *call) output(res forwardResult) device.Output {
	out := device.Output{Result: device.Result{
		Trace: res.Trace, Ingress: res.Ingress, FID: res.FID, Metadata: res.Metadata,
		PortDependencies: res.ConsultedPorts(), ScopeDependencies: res.ConsultedScopes(),
	}, Hold: device.HoldToken(c.hold)}
	if res.Egress != nil {
		out.Egress = make([]device.Egress, len(res.Egress))
		for i, eg := range res.Egress {
			out.Egress[i] = device.Egress{Port: eg.Port, Member: eg.Member, Frame: eg.Frame, PCP: eg.PCP, Dropped: eg.Dropped}
		}
	}
	if c.emissions != nil {
		out.Emissions = make([]device.Emission, len(c.emissions))
		for i, em := range c.emissions {
			out.Emissions[i] = device.Emission{Port: em.Port, Member: em.Member, Frame: em.Frame, Hold: device.HoldToken(em.Hold), Ingress: em.Ingress, PCP: em.PCP, Protocol: em.Protocol}
		}
	}
	if c.copies != nil {
		out.Copies = make([]device.Copy, len(c.copies))
		for i, cp := range c.copies {
			out.Copies[i] = device.Copy{Mirror: cp.Mirror, Port: cp.Port, Member: cp.Member, VLAN: cp.VLAN, Frame: cp.Frame, Hold: device.HoldToken(cp.Hold)}
		}
	}
	if c.drops != nil {
		out.Drops = make([]device.Drop, len(c.drops))
		for i, drop := range c.drops {
			out.Drops[i] = device.Drop{Step: drop.Step, Steps: drop.Steps, Port: drop.Port, Reason: drop.Reason, Hold: device.HoldToken(drop.Hold), Ingress: drop.Ingress}
		}
	}
	return out
}
