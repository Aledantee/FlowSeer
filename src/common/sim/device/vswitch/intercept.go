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

func (s *Switch) interceptBPDU(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	// The relay's first checks apply to a BPDU too: a dead or unknown port
	// received nothing, and a trace saying Consumed there would hide a BPDU
	// that died on a cut cable.
	receive := s.ports.Receive(ingress)
	if receive.Reason != "" {
		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  port.ReasonPortDown,
				Steps: []trace.Step{{
					Layer:   stp.LayerName,
					Op:      trace.OpDrop,
					RuleID:  stp.RuleStatusDown,
					Subject: trace.Subject{Kind: "port", Key: receive.Decisive},
					Inputs:  receive.ForwardingFacts(),
				}},
			},
			Ingress: receive.Resolved.Name,
		}
	}
	resolvedPort := receive.Resolved.Name
	before := s.stp.PortInfo(resolvedPort)

	bpdu, err := bpdu.Decode(f)
	if err != nil {
		if mutate {
			s.stp.BadBPDU(resolvedPort)
		}
		after := s.stp.PortInfo(resolvedPort)

		reason := stp.ReasonUnsupportedBPDU

		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  reason,
				Steps: []trace.Step{
					{
						Layer:   stp.LayerName,
						Op:      trace.OpDrop,
						RuleID:  trace.RuleID(stp.RuleBPDUPrefix + string(reason)),
						Subject: trace.Subject{Kind: "port", Key: resolvedPort},
						Inputs:  []trace.Fact{stp.BPDUDecodeFact(f, false, reason)},
						Outputs: []trace.Fact{stp.PortTransitionFact(resolvedPort, "bad-bpdu", before, after)},
					},
				},
			},
			Ingress: resolvedPort,
		}
	}

	if mutate {
		fx := s.stp.Receive(now, resolvedPort, bpdu)
		s.applySTPEffects(fx)
	}
	after := s.stp.PortInfo(resolvedPort)

	return bridge.Result{
		Trace: trace.Trace{
			Outcome: trace.Consumed,
			Steps: []trace.Step{
				{
					Layer:   stp.LayerName,
					Op:      trace.OpClassify,
					RuleID:  stp.RuleBPDUAdmit,
					Subject: trace.Subject{Kind: "port", Key: resolvedPort},
					Inputs:  []trace.Fact{stp.BPDUDecodeFact(f, true, "")},
					Outputs: []trace.Fact{stp.BPDUDecisionFact(bpdu, before, after)},
				},
			},
		},
		Ingress: resolvedPort,
	}
}

// interceptSSTP handles a frame addressed to the per-VLAN BPDU group. Unlike
// an IEEE-addressed BPDU, an SSTP BPDU means nothing without the VLAN it
// arrived on: the tree it belongs to is chosen by that VLAN, and the check
// that the peer agrees about the link compares it with the VLAN named by a
// Configuration or RST BPDU. A TCN names no VLAN.
//
// The VLAN is resolved from the frame's own tag, or the port's untagged VLAN
// when it carries none, rather than through the bridge's ingress pipeline the
// way a loop-protection probe is. A probe may be denied by a gated port; a
// BPDU may not. The ports a spanning tree holds discarding are exactly the
// ones whose blocking depends on continuing to hear their peer, so running a
// BPDU through the gate the tree itself set would drop the frames that keep
// the topology converged. The outer tag counts as a VLAN selection only when
// its TPID names dot1Q (or the codec's untagged zero value, treated the
// same); any other TPID — an 802.1ad/QinQ tag, say — is not a VLAN tag at
// all, and a VID of 0 under a dot1Q TPID is a priority tag, not a VLAN
// selection either. Both cases resolve the same as an untagged frame.
//
// The bridge's own ingress admission rule is not bypassed: it is computed
// here and handed to the layer as SSTPArrival.Admitted, so the spanning tree
// gate and the bridge's notion of which VLANs a port speaks are the same
// question asked once, rather than a second, stricter gate ahead of the
// layer. The layer decides what a refusal means — bpdu-guard still fires
// even when the frame's VLAN is not admitted, because the link half of a
// receive always runs before the tree half is judged.
func (s *Switch) interceptSSTP(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	receive := s.ports.Receive(ingress)
	if receive.Reason != "" {
		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  port.ReasonPortDown,
				Steps: []trace.Step{{
					Layer:   stp.LayerName,
					Op:      trace.OpDrop,
					RuleID:  stp.RuleStatusDown,
					Subject: trace.Subject{Kind: "port", Key: receive.Decisive},
					Inputs:  receive.ForwardingFacts(),
				}},
			},
			Ingress: receive.Resolved.Name,
		}
	}
	resolvedPort := receive.Resolved.Name
	before := s.stp.PortInfo(resolvedPort)

	bpdu, tlvVID, err := bpdu.DecodeSSTP(f)
	if err != nil {
		if mutate {
			s.stp.BadBPDU(resolvedPort)
		}
		after := s.stp.PortInfo(resolvedPort)

		reason := stp.ReasonUnsupportedBPDU

		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  reason,
				Steps: []trace.Step{{
					Layer:   stp.LayerName,
					Op:      trace.OpDrop,
					RuleID:  trace.RuleID(stp.RuleSSTPPrefix + string(reason)),
					Subject: trace.Subject{Kind: "port", Key: resolvedPort},
					Inputs:  []trace.Fact{stp.BPDUDecodeFact(f, false, reason)},
					Outputs: []trace.Fact{stp.PortTransitionFact(resolvedPort, "bad-bpdu", before, after)},
				}},
			},
			Ingress: resolvedPort,
		}
	}

	// arrivalVID and tagged both come from one test of the outer tag's TPID,
	// the same test bridge.Layer.Ingress makes on its non-tunnel arm (a
	// tunnel port classifies into the tunnel VID regardless, but
	// AdmitsVIDOnIngress refuses tunnel ports outright, so that arm never
	// reaches here): a tag whose TPID names neither dot1Q nor no-TPID (the
	// codec's own zero-value) is not a VLAN selection at all, so the frame is
	// untagged on the port's native VLAN however its VID field reads, and a
	// VID of 0 under a dot1Q TPID is a priority tag, also untagged. Deriving
	// the two independently — VID from any TPID, tagged from only a
	// dot1Q-shaped one — let a QinQ-tagged frame judge admission against a
	// VID the bridge itself would never classify it into.
	arrivalVID := s.untaggedVID(resolvedPort)
	tagged := false
	if len(f.Tags) > 0 {
		outer := f.Tags[0]
		if outer.TPID == 0 || outer.TPID == uint16(ethernet.EtherTypeDot1Q) {
			if outer.VID != 0 {
				tagged = true
				arrivalVID = outer.VID
			}
		}
	}

	admitted := true
	if s.cfg.Bridge.VLAN != nil {
		admitted = s.cfg.Bridge.VLAN.AdmitsVIDOnIngress(resolvedPort, arrivalVID, tagged)
	}
	tracked := s.stp.TracksVLAN(arrivalVID)

	before = s.stp.VLANPortInfo(arrivalVID, resolvedPort)

	var outcome stp.SSTPOutcome
	if mutate {
		var fx layer.Effects
		fx, outcome = s.stp.ReceiveSSTP(now, resolvedPort, stp.SSTPArrival{
			ArrivalVID: arrivalVID,
			TLVVID:     tlvVID,
			Admitted:   admitted,
		}, bpdu)
		s.applySTPEffects(fx)
	} else {
		// Peek cannot call ReceiveSSTP without mutating the link half of a
		// receive, so it renders the same step Forward reaches through
		// PortLinked, admitted, and tracked instead: PortLinked reproduces
		// ReceiveSSTP's own first check (the port is tracked and its link
		// record is up), and every remaining outcome but that one
		// follows from admitted and tracked alone once the frame has
		// decoded.
		switch {
		case !s.stp.PortLinked(resolvedPort):
			outcome = stp.SSTPPortDown
		case !admitted:
			outcome = stp.SSTPNotAdmitted
		case !tracked:
			outcome = stp.SSTPUntrackedVLAN
		default:
			outcome = stp.SSTPApplied
		}
	}
	after := s.stp.VLANPortInfo(arrivalVID, resolvedPort)

	inputs := []trace.Fact{
		stp.BPDUDecodeFact(f, true, ""),
		sstpVLANFact(bpdu.Type, tlvVID, arrivalVID),
	}
	outputs := []trace.Fact{stp.BPDUDecisionFact(bpdu, before, after)}
	subject := trace.Subject{Kind: "port", Key: resolvedPort}

	if outcome == stp.SSTPPortDown {
		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  port.ReasonPortDown,
				Steps: []trace.Step{{
					Layer:   stp.LayerName,
					Op:      trace.OpDrop,
					RuleID:  stp.RuleStatusDown,
					Subject: subject,
					Inputs:  inputs,
					Outputs: outputs,
				}},
			},
			Ingress: resolvedPort,
		}
	}

	// admitted and tracked are exactly what ReceiveSSTP itself judges the
	// tree half against, computed here the same way for Forward and Peek, so
	// deciding the drop step from them directly — rather than from outcome —
	// is what keeps the two paths identical by construction instead of two
	// derivations that have to agree. It also renders bpdu guard correctly on
	// an unadmitted VLAN: guard belongs to the link half, which always runs
	// before the tree half is judged, so ReceiveSSTP can return SSTPGuarded
	// for a frame that never reached admission at all.
	switch {
	case !admitted:
		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  stp.ReasonVLANNotAdmitted,
				Steps: []trace.Step{{
					Layer:   stp.LayerName,
					Op:      trace.OpDrop,
					RuleID:  stp.RuleSSTPVLANNotAdmitted,
					Subject: subject,
					Inputs:  inputs,
					Outputs: outputs,
				}},
			},
			Ingress: resolvedPort,
		}
	case !tracked:
		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  stp.ReasonVLANUntracked,
				Steps: []trace.Step{{
					Layer:   stp.LayerName,
					Op:      trace.OpDrop,
					RuleID:  stp.RuleSSTPVLANUntracked,
					Subject: subject,
					Inputs:  inputs,
					Outputs: outputs,
				}},
			},
			Ingress: resolvedPort,
		}
	}

	admitResult := bridge.Result{
		Trace: trace.Trace{
			Outcome: trace.Consumed,
			Steps: []trace.Step{{
				Layer:   stp.LayerName,
				Op:      trace.OpClassify,
				RuleID:  stp.RuleSSTPAdmit,
				Subject: subject,
				Inputs:  inputs,
				Outputs: outputs,
			}},
		},
		Ingress: resolvedPort,
		FID:     arrivalVID,
	}

	// The step is chosen above from admitted and tracked, not from the
	// outcome: both are true here; under Forward the layer has processed the
	// frame, under Peek it is untouched.
	// stp.sstp.admit is the step either way, whether the tree half applied
	// the vector, fired BPDU guard, marked a boundary, or refused a PVID
	// mismatch.
	//
	// ReceiveSSTP has a second source of SSTPUntrackedVLAN that tracked does
	// not model: the arrival VLAN's tree exists but holds no state for this
	// port. It cannot occur here, because every tree is built from the same
	// port name set, the invariant portInfo and recompute already rest on.
	return admitResult
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
