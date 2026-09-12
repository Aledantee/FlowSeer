package traffic

import (
	"strconv"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

type mirrorDecisionFact string

func (f mirrorDecisionFact) TypeID() string    { return "traffic.mirror_decision" }
func (f mirrorDecisionFact) Canonical() string { return string(f) }

type policerDecisionFact string

func (f policerDecisionFact) TypeID() string    { return "traffic.policer_decision" }
func (f policerDecisionFact) Canonical() string { return string(f) }

// MirrorDecisionFact returns an immutable snapshot of a mirror output decision.
func MirrorDecisionFact(mirror, portName string, frameOctets int, reason trace.Reason) trace.Fact {
	return mirrorDecisionFact("mirror=" + strconv.Quote(mirror) +
		";port=" + strconv.Quote(portName) +
		";frame_octets=" + strconv.Itoa(frameOctets) +
		";reason=" + strconv.Quote(string(reason)))
}

// PolicerDecisionFact returns an immutable snapshot of an ingress policer decision.
func PolicerDecisionFact(rateBPS uint64, burstOctets, frameOctets int, admitted bool) trace.Fact {
	return policerDecisionFact("rate_bps=" + strconv.FormatUint(rateBPS, 10) +
		";burst_octets=" + strconv.Itoa(burstOctets) +
		";frame_octets=" + strconv.Itoa(frameOctets) +
		";admitted=" + strconv.FormatBool(admitted))
}
