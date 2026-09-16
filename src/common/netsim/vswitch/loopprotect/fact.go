package loopprotect

import (
	"strconv"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
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
