package loopprotect

import (
	"strconv"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

type forwardingDecisionFact string

func (f forwardingDecisionFact) TypeID() string    { return "loopprotect.forwarding_decision" }
func (f forwardingDecisionFact) Canonical() string { return string(f) }

// ForwardingFact returns an immutable snapshot of the loop-protection state
// used to gate bridge learning and forwarding, so the bridge can record why
// a gated port denied a request.
func (l *Layer) ForwardingFact(portName string, vid vlan.ID, learns, forwards bool) trace.Fact {
	info := l.PortInfo(portName)

	return forwardingDecisionFact("port=" + strconv.Quote(portName) +
		";vid=" + strconv.FormatUint(uint64(vid), 10) +
		";action=" + strconv.Quote(string(info.Action)) +
		";inter_vlan=" + strconv.FormatBool(info.InterVLAN) +
		";recurrences=" + strconv.FormatUint(info.Recurrences, 10) +
		";learns=" + strconv.FormatBool(learns) +
		";forwards=" + strconv.FormatBool(forwards))
}

type probeFact string

func (f probeFact) TypeID() string    { return "loopprotect.probe" }
func (f probeFact) Canonical() string { return string(f) }

// ProbeFact returns an immutable snapshot of a returned probe's payload.
func ProbeFact(probe Probe) trace.Fact {
	return probeFact("origin=" + probe.OriginMAC.String() +
		";sequence=" + strconv.FormatUint(uint64(probe.Sequence), 10) +
		";sent_vid=" + strconv.FormatUint(uint64(probe.VID), 10) +
		";port=" + strconv.Quote(probe.Port))
}

type returnFact string

func (f returnFact) TypeID() string    { return "loopprotect.probe_return" }
func (f returnFact) Canonical() string { return string(f) }

// ReturnFact returns an immutable snapshot of a probe's return,
// carrying both the VLAN it was sent on and the VLAN it was classified into
// so an inter-VLAN loop is visible in the trace.
func ReturnFact(probe Probe, returnedVID vlan.ID, before, after PortInfo) trace.Fact {
	return returnFact("port=" + strconv.Quote(probe.Port) +
		";sent_vid=" + strconv.FormatUint(uint64(probe.VID), 10) +
		";returned_vid=" + strconv.FormatUint(uint64(returnedVID), 10) +
		";before=" + portInfoSnapshot(before) +
		";after=" + portInfoSnapshot(after))
}

type transitionFact string

func (f transitionFact) TypeID() string    { return "loopprotect.port_transition" }
func (f transitionFact) Canonical() string { return string(f) }

// TransitionFact returns an immutable snapshot of a loop-protection
// port action transition.
func TransitionFact(portName string, before, after PortInfo) trace.Fact {
	return transitionFact("port=" + strconv.Quote(portName) +
		";before=" + portInfoSnapshot(before) +
		";after=" + portInfoSnapshot(after))
}

func portInfoSnapshot(info PortInfo) string {
	return "{action=" + strconv.Quote(string(info.Action)) +
		";inter_vlan=" + strconv.FormatBool(info.InterVLAN) +
		";recurrences=" + strconv.FormatUint(info.Recurrences, 10) + "}"
}
