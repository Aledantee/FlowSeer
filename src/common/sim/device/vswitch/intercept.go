package vswitch

import (
	"fmt"
	"net/netip"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

var (
	stpGroupAddress = netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00}

	// sstpGroupAddress is where a PVST bridge sends its per-VLAN BPDUs. It is
	// returned by bpdu.GroupAddressSSTP rather than re-declared the way
	// stpGroupAddress is, since the codec that builds those frames is what
	// owns the address.
	sstpGroupAddress = bpdu.GroupAddressSSTP()

	allNodesAddress = netip.MustParseAddr("ff02::1")
)

func (s *Switch) interceptLACP(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	before := s.lag.PortInfo(ingress)
	var pdu lacp.PDU
	var marker layer.Effects
	var unsupported bool
	if f.Payload[0] == lacp.SubtypeMarker {
		marker = s.lag.ReceiveMarker(now, ingress, f)
		unsupported = len(marker.Emissions) == 0
	} else {
		var err error
		pdu, err = lacp.Decode(f)
		unsupported = err != nil
	}
	if unsupported {
		if mutate {
			s.lag.BadLACPDU(ingress)
		}
		after := s.lag.PortInfo(ingress)
		dropInput := lag.SlowProtocolsDecodeFact(f, false, lag.ReasonUnsupportedLACPDU)

		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  lag.ReasonUnsupportedLACPDU,
				Steps: []trace.Step{
					{
						Layer:   lag.LayerName,
						Op:      trace.OpDrop,
						RuleID:  lag.RuleLACPDUUnsupported,
						Subject: trace.Subject{Kind: "port", Key: ingress},
						Inputs:  []trace.Fact{dropInput},
						Outputs: []trace.Fact{lag.MemberTransitionFact(ingress, "bad-lacpdu", before, after)},
					},
				},
			},
			Ingress: ingress,
		}
	}

	rule := lag.RuleLACPDUAdmit
	var inputs []trace.Fact
	var outputs []trace.Fact
	if len(marker.Emissions) > 0 {
		rule = lag.RuleMarkerRespond
		if mutate {
			s.applyLAGEffects(now, marker)
		}
		inputs = []trace.Fact{lag.SlowProtocolsDecodeFact(f, true, "")}
		outputs = []trace.Fact{lag.MarkerResponseFact(f, marker.Emissions[0].Frame)}
	} else {
		if mutate {
			fx := s.lag.Receive(now, ingress, pdu)
			s.applyLAGEffects(now, fx)
		}
		inputs = []trace.Fact{lag.SlowProtocolsDecodeFact(f, true, "")}
		outputs = []trace.Fact{lag.LACPDecisionFact(pdu, before, s.lag.PortInfo(ingress))}
	}

	return bridge.Result{
		Trace: trace.Trace{
			Outcome: trace.Consumed,
			Steps: []trace.Step{
				{
					Layer:   lag.LayerName,
					Op:      trace.OpClassify,
					RuleID:  rule,
					Subject: trace.Subject{Kind: "port", Key: ingress},
					Inputs:  inputs,
					Outputs: outputs,
				},
			},
		},
		Ingress: ingress,
	}
}

// interceptLoopProtect handles a frame addressed to the loop-protection probe
// group. It reports handled=false for a probe this switch did not originate,
// or for a frame that does not decode as a probe at all: both fall through to
// the ordinary relay path unclassified, where an unrecognized destination
// floods as unregistered multicast. A probe this switch did originate is
// classified through the bridge's ordinary VLAN and gate pipeline first, so a
// gated ingress port still denies it exactly as it would deny any other
// frame; only once that pipeline admits it does this apply the port's
// configured loop-protection action.
func (s *Switch) interceptLoopProtect(now time.Time, ingress string, f ethernet.Frame, mutate bool) (bridge.Result, bool) {
	probe, err := loopprotect.Decode(f)
	if err != nil || probe.OriginMAC != s.cfg.MAC {
		return bridge.Result{}, false
	}

	in, res, ok := s.bridge.Ingress(now, ingress, f, false, false)
	if !ok {
		return res, true
	}

	before := s.loopprotect.PortInfo(probe.Port)
	if mutate {
		ret := loopprotect.Return{
			VID:                in.FID,
			SameUntaggedDomain: s.sameUntaggedDomain(probe.Port, probe.VID, in.FID),
		}
		fx := s.loopprotect.Receive(now, ret, probe)
		s.applyLoopProtectEffects(fx)
	}
	after := s.loopprotect.PortInfo(probe.Port)

	steps := append([]trace.Step(nil), in.Steps...)
	steps = append(steps, trace.Step{
		Layer:   loopprotect.LayerName,
		Op:      trace.OpClassify,
		RuleID:  loopprotect.RuleProbeReturn,
		Subject: trace.Subject{Kind: "port", Key: probe.Port},
		Inputs:  []trace.Fact{loopprotect.ProbeFact(probe)},
		Outputs: []trace.Fact{loopprotect.ReturnFact(probe, in.FID, before, after)},
	})
	if before.Action != after.Action {
		steps = append(steps, trace.Step{
			Layer:   loopprotect.LayerName,
			Op:      trace.OpFilter,
			RuleID:  loopprotect.RulePortBlock,
			Subject: trace.Subject{Kind: "port", Key: probe.Port},
			Outputs: []trace.Fact{loopprotect.TransitionFact(probe.Port, before, after)},
		})
	}

	result := bridge.Result{
		Trace: trace.Trace{
			Outcome: trace.Consumed,
			Steps:   steps,
		},
		Ingress: in.Port,
		FID:     in.FID,
	}
	result.Consult(in.ConsultedPorts()...)
	result.ConsultScopes(in.ConsultedScopes()...)
	result.ConsultScopes(analysis.FieldScope(
		protocolScope(s.nodeID, loopprotect.LayerName), "ports", probe.Port,
	))

	return result, true
}

func (s *Switch) bpduPort(ingress string) (string, bridge.Result, bool) {
	receive := s.ports.Receive(ingress)
	name := receive.Resolved.Name
	if receive.Reason == "" && s.stp.PortLinked(name) {
		return name, bridge.Result{}, true
	}
	subject := receive.Decisive
	if subject == "" {
		subject = name
	}
	return name, bridge.Result{
		Trace: trace.Trace{
			Outcome: trace.Dropped, Reason: port.ReasonPortDown,
			Steps: []trace.Step{{
				Layer: stp.LayerName, Op: trace.OpDrop, RuleID: stp.RuleStatusDown,
				Subject: trace.Subject{Kind: "port", Key: subject}, Inputs: receive.ForwardingFacts(),
			}},
		},
		Ingress: name,
	}, false
}

func (s *Switch) bpduTrees(name string) []stp.PortInfo {
	infos := []stp.PortInfo{s.stp.PortInfo(name)}
	seen := map[stp.TreeRef]bool{infos[0].Tree: true}
	for _, vid := range s.configuredVLANs() {
		info := s.stp.VLANPortInfo(vid, name)
		if info.Tree.Kind != "" && !seen[info.Tree] {
			seen[info.Tree] = true
			infos = append(infos, info)
		}
	}
	return infos
}

func (s *Switch) bpduResult(name string, vid vlan.ID, rule trace.RuleID, reason trace.Reason,
	inputs, outputs []trace.Fact, before []stp.PortInfo,
) bridge.Result {
	after := s.bpduTrees(name)
	for _, old := range before {
		for _, info := range after {
			if old.Tree == info.Tree && (old.Role != info.Role || old.State != info.State) {
				outputs = append(outputs, stp.PortTransitionFact(name, "bpdu", old, info))
			}
		}
	}
	outcome, op := trace.Consumed, trace.OpClassify
	if reason != "" {
		outcome, op = trace.Dropped, trace.OpDrop
	}
	return bridge.Result{
		Trace: trace.Trace{
			Outcome: outcome, Reason: reason,
			Steps: []trace.Step{{
				Layer: stp.LayerName, Op: op, RuleID: rule,
				Subject: trace.Subject{Kind: "port", Key: name}, Inputs: inputs, Outputs: outputs,
			}},
		},
		Ingress: name, FID: vid,
	}
}

func (s *Switch) badBPDU(name string, f ethernet.Frame, prefix string, mutate bool) bridge.Result {
	before := s.stp.PortInfo(name)
	if mutate {
		s.stp.BadBPDU(name)
	}
	reason := stp.ReasonUnsupportedBPDU
	return s.bpduResult(name, 0, trace.RuleID(prefix+string(reason)), reason,
		[]trace.Fact{stp.BPDUDecodeFact(f, false, reason)},
		[]trace.Fact{stp.PortTransitionFact(name, "bad-bpdu", before, s.stp.PortInfo(name))}, nil)
}

func (s *Switch) interceptBPDU(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	name, res, ok := s.bpduPort(ingress)
	if !ok {
		return res
	}
	b, err := bpdu.Decode(f)
	if err != nil {
		return s.badBPDU(name, f, stp.RuleBPDUPrefix, mutate)
	}
	before := s.bpduTrees(name)
	if mutate {
		s.applySTPEffects(s.stp.Receive(now, name, b))
	}
	return s.bpduResult(name, 0, stp.RuleBPDUAdmit, "",
		[]trace.Fact{stp.BPDUDecodeFact(f, true, "")},
		[]trace.Fact{stp.BPDUDecisionFact(b, before[0], s.stp.PortInfo(name))}, before)
}

// SSTP bypasses the gate that its own tree sets so a discarding port still
// hears its peer. VLAN admission is passed to the layer, whose link guards
// run even when the VLAN is refused.
func (s *Switch) interceptSSTP(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	name, res, ok := s.bpduPort(ingress)
	if !ok {
		return res
	}
	b, tlvVID, err := bpdu.DecodeSSTP(f)
	if err != nil {
		return s.badBPDU(name, f, stp.RuleSSTPPrefix, mutate)
	}
	arrivalVID, tagged := s.sstpArrival(name, f)
	admitted := s.cfg.Bridge.VLAN == nil || s.cfg.Bridge.VLAN.AdmitsVIDOnIngress(name, arrivalVID, tagged)
	tracked := s.stp.TracksVLAN(arrivalVID)
	trees := s.bpduTrees(name)
	before := s.stp.VLANPortInfo(arrivalVID, name)
	if mutate {
		fx, _ := s.stp.ReceiveSSTP(now, name, stp.SSTPArrival{
			ArrivalVID: arrivalVID, TLVVID: tlvVID, Admitted: admitted,
		}, b)
		s.applySTPEffects(fx)
	}
	rule, reason := stp.RuleSSTPAdmit, trace.Reason("")
	switch {
	case !admitted:
		rule, reason = stp.RuleSSTPVLANNotAdmitted, stp.ReasonVLANNotAdmitted
	case !tracked:
		rule, reason = stp.RuleSSTPVLANUntracked, stp.ReasonVLANUntracked
	}
	res = s.bpduResult(name, arrivalVID, rule, reason,
		[]trace.Fact{stp.BPDUDecodeFact(f, true, ""), sstpVLANFact(b.Type, tlvVID, arrivalVID)},
		[]trace.Fact{stp.BPDUDecisionFact(b, before, s.stp.VLANPortInfo(arrivalVID, name))}, trees)
	if reason != "" {
		res.FID = 0
	}
	return res
}

func (s *Switch) sstpArrival(name string, f ethernet.Frame) (vlan.ID, bool) {
	if len(f.Tags) > 0 {
		outer := f.Tags[0]
		if (outer.TPID == 0 || outer.TPID == uint16(ethernet.EtherTypeDot1Q)) && outer.VID != 0 {
			return outer.VID, true
		}
	}
	return s.untaggedVID(name), false
}

// sstpVLANFact records the VLAN relationship an SSTP BPDU is judged against.
// Configuration and RST BPDUs carry a TLV that is compared with the arrival
// VLAN. A TCN carries no VLAN or TLV, so its fact names only the arrival VLAN.
func sstpVLANFact(shape bpdu.Type, tlvVID, arrivalVID vlan.ID) trace.Fact {
	canonical := fmt.Sprintf("tlv=%d,arrival=%d,consistent=%t", tlvVID, arrivalVID, tlvVID == arrivalVID)
	if shape == bpdu.TypeTopologyChangeNotification {
		canonical = fmt.Sprintf("tlv=none,arrival=%d", arrivalVID)
	}

	return runtimeFact{
		typeID:    "stp.sstp.vlans",
		canonical: canonical,
	}
}
