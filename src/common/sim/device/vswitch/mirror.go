package vswitch

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// AdmitEgress decides queue admission without changing occupancy. A nil switch
// has no queue table or stated MTU. Calls require serial access.
func (s *Switch) AdmitEgress(name string, pcp vlan.PCP, depth, octets uint64) device.QueueDecision {
	var queues *traffic.Layer
	var mtu int
	if s != nil {
		queues = s.traffic
		p, _ := s.ports.Port(name)
		mtu = p.MTU
	}
	decision := queues.AdmitEgress(name, pcp, depth, octets, mtu)
	return device.QueueDecision{Admitted: decision.Admitted, Reason: decision.Reason, Step: decision.Step, MaxRateBPS: decision.MaxRateBPS}
}

func buildTrafficSwitchports(cfg Config) map[string]traffic.Switchport {
	if cfg.Bridge == nil || cfg.Bridge.VLAN == nil {
		return nil
	}
	switchports := make(map[string]traffic.Switchport, len(cfg.Bridge.VLAN.Switchports))
	for name, sw := range cfg.Bridge.VLAN.Switchports {
		tsw := traffic.Switchport{
			Tagged:   slices.Clone(sw.Tagged),
			Untagged: slices.Clone(sw.Untagged),
		}
		if sw.Tunnel != nil {
			tsw.Tunnel = &traffic.Tunnel{VID: sw.Tunnel.VID}
		}
		switchports[name] = tsw
	}
	return switchports
}

func (s *Switch) police(now time.Time, name string, octets int) bool {
	if s.traffic == nil {
		return true
	}

	return s.traffic.Admit(now, name, octets)
}

// heldMirror is what the switch remembers about a frame the routing layer holds, keyed by its
// hold token, so the copies taken when the frame is released agree with the ones taken when it
// was held. Mirrors names the mirrors that copied the frame on arrival, and a mirror is copied
// to once per frame however many of its selectors match. Ingress and VLAN are the logical port
// and the VLAN the frame arrived on, which the routing layer's own record of the ingress
// interface cannot supply and which a release must select on as a live route would.
// Silent marks a frame that arrived as a mirror copy and therefore produces no copies at all.
type heldMirror struct {
	Ingress string
	VLAN    vlan.ID
	Mirrors []string
	Silent  bool
}

func (h heldMirror) clone() heldMirror {
	h.Mirrors = slices.Clone(h.Mirrors)
	return h
}

func cloneHeldMirrors(held map[routing.HoldToken]heldMirror) map[routing.HoldToken]heldMirror {
	if held == nil {
		return nil
	}
	cp := make(map[routing.HoldToken]heldMirror, len(held))
	for token, h := range held {
		cp[token] = h.clone()
	}
	return cp
}

// rememberHeldMirror records the arrival of the frame the current call holds. A call that
// holds nothing, or that does not commit, leaves no record.
func (s *Switch) rememberHeldMirror(ingress string, vid vlan.ID, copies []traffic.Copy) {
	c := s.call
	if !c.commit || c.hold == 0 {
		return
	}
	h := heldMirror{Ingress: ingress, VLAN: vid, Silent: c.mirrored}
	for _, cp := range copies {
		if !slices.Contains(h.Mirrors, cp.Mirror) {
			h.Mirrors = append(h.Mirrors, cp.Mirror)
		}
	}
	if s.heldMirrors == nil {
		s.heldMirrors = make(map[routing.HoldToken]heldMirror)
	}
	s.heldMirrors[c.hold] = h
}

// withoutMirrored drops the copies the frame's arrival already produced.
func (h heldMirror) withoutMirrored(copies []traffic.Copy) []traffic.Copy {
	if h.Silent {
		return nil
	}
	return slices.DeleteFunc(copies, func(cp traffic.Copy) bool {
		return slices.Contains(h.Mirrors, cp.Mirror)
	})
}

func (s *Switch) readyMirrorCopies(now time.Time, res *bridge.Result, copies []traffic.Copy, mutate bool) []traffic.Copy {
	ready := copies[:0]
	for _, copy := range copies {
		output, ok := s.ports.Port(copy.Port)
		if !ok {
			continue
		}
		s.egressDependencies(copy.Port).consult(res)

		reason := trace.Reason("")
		var selection trace.Fact
		if !output.Forwards() {
			reason = port.ReasonPortDown
		} else if output.Kind == port.LAG {
			res.ConsultScopes(s.aggregatorScope(copy.Port))
			sel, fact := s.selectMemberWithFact(now, copy.Port, copy.Frame, copy.VLAN, mutate)
			selection = fact
			if !sel.OK {
				reason = bridge.ReasonNoMember
			} else if memberPort, exists := s.ports.Port(sel.Member); !exists || !memberPort.Forwards() {
				reason = port.ReasonPortDown
			} else {
				copy.Member = sel.Member
			}
		}

		decision := traffic.MirrorDecisionFact(copy.Mirror, copy.Port, frameOctets(copy.Frame), reason)
		inputs := traceFacts(selection)
		if reason != "" {
			inputs = append(inputs,
				port.ForwardingFact(copy.Port, output, false, reason),
				decision,
			)
			res.Steps = append(res.Steps, trace.Step{
				Layer:   traffic.LayerName,
				Op:      trace.OpDrop,
				RuleID:  traffic.RuleMirrorCopyDrop,
				Subject: trace.Subject{Kind: "port", Key: copy.Port},
				Inputs:  inputs,
			})

			continue
		}

		res.Steps = append(res.Steps, trace.Step{
			Layer:   traffic.LayerName,
			Op:      trace.OpReplicate,
			RuleID:  traffic.RuleMirrorCopy,
			Subject: trace.Subject{Kind: "port", Key: copy.Port},
			Inputs:  inputs,
			Outputs: []trace.Fact{decision},
		})
		ready = append(ready, copy)
	}

	return ready
}

func (s *Switch) isMirrorOutputPort(name string) bool {
	if s.traffic == nil {
		return false
	}
	_, ok := s.traffic.MirrorForOutput(name)
	return ok
}

func (s *Switch) logicalIngress(name string) string {
	if p, ok := s.ports.Resolve(name); ok {
		return p.Name
	}
	return name
}

func (s *Switch) mirrorForOutput(name string) string {
	if s.traffic == nil {
		return ""
	}
	m, _ := s.traffic.MirrorForOutput(name)
	return m
}

func frameOctets(f ethernet.Frame) int {
	return 14 + len(f.Payload) + 4*len(f.Tags)
}
