package vswitch

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Output holds the forwarding result and every frame, mirror copy, and held-frame
// drop produced by one switch call.
type Output struct {
	ForwardResult
	Emissions []Emission
	Copies    []traffic.Copy
	Drops     []NeighborDrop
}

// Advance settles all layer timers due at or before now.
func (s *Switch) Advance(now time.Time) Output {
	c, end := s.beginCall(now, true)
	c.pending = false
	defer end()
	s.advance(now)
	return c.output(ForwardResult{})
}

// Receive settles timers before policing and handling an arriving frame.
func (s *Switch) Receive(now time.Time, ingress string, frame ethernet.Frame) Output {
	return s.receive(now, ingress, frame, false)
}

// ReceiveMirrored handles a mirror arrival without policing or producing copies.
func (s *Switch) ReceiveMirrored(now time.Time, ingress string, frame ethernet.Frame) Output {
	return s.receive(now, ingress, frame, true)
}

func (s *Switch) receive(now time.Time, ingress string, frame ethernet.Frame, mirrored bool) Output {
	c, end := s.beginCall(now, true)
	c.pending = false
	defer end()
	s.advance(now)
	var res bridge.Result
	if !mirrored && !s.Police(now, ingress, frame.WireOctets()) {
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
		c.copies = nil
	}
	return c.output(s.wrapResult(c, res))
}

func (c *call) output(res ForwardResult) Output {
	return Output{ForwardResult: res, Emissions: c.emissions, Copies: c.copies, Drops: c.drops}
}
