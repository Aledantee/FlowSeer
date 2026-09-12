package stp

import (
	"strconv"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

type bpduDecisionFact string

func (f bpduDecisionFact) TypeID() string    { return "stp.bpdu_decision" }
func (f bpduDecisionFact) Canonical() string { return string(f) }

type forwardingDecisionFact string

func (f forwardingDecisionFact) TypeID() string    { return "stp.forwarding_decision" }
func (f forwardingDecisionFact) Canonical() string { return string(f) }

// BPDUDecisionFact returns an immutable snapshot of a BPDU and the port state
// before and after applying it.
func BPDUDecisionFact(bpdu BPDU, before, after PortInfo) trace.Fact {
	return bpduDecisionFact("bpdu=" + bpduSnapshot(bpdu) +
		";before=" + portInfoSnapshot(before) +
		";after=" + portInfoSnapshot(after))
}

// BPDUDecodeFact returns an immutable snapshot of a BPDU frame decode decision.
func BPDUDecodeFact(f ethernet.Frame, valid bool, reason trace.Reason) trace.Fact {
	return bpduDecisionFact("ether_type=" + strconv.FormatUint(uint64(f.EtherType), 10) +
		";payload_len=" + strconv.Itoa(len(f.Payload)) +
		";valid=" + strconv.FormatBool(valid) +
		";reason=" + strconv.Quote(string(reason)))
}

// ForwardingFact returns an immutable snapshot of the spanning-tree state used
// to gate bridge learning and forwarding.
func (l *Layer) ForwardingFact(port string, learns, forwards bool) trace.Fact {
	return forwardingDecisionFact("port=" + strconv.Quote(port) +
		";state=" + portInfoSnapshot(l.PortInfo(port)) +
		";learns=" + strconv.FormatBool(learns) +
		";forwards=" + strconv.FormatBool(forwards))
}

// PortTransitionFact returns an immutable snapshot of a spanning-tree port transition.
func PortTransitionFact(port, action string, before, after PortInfo) trace.Fact {
	return bpduDecisionFact("port=" + strconv.Quote(port) +
		";action=" + strconv.Quote(action) +
		";before=" + portInfoSnapshot(before) +
		";after=" + portInfoSnapshot(after))
}

func bpduSnapshot(bpdu BPDU) string {
	return "{version=" + strconv.FormatUint(uint64(bpdu.Version), 10) +
		";type=" + strconv.FormatUint(uint64(bpdu.Type), 10) +
		";flags=" + strconv.FormatUint(uint64(bpdu.Flags), 10) +
		";root=" + strconv.Quote(bpdu.RootID.String()) +
		";root_cost=" + strconv.FormatUint(uint64(bpdu.RootPathCost), 10) +
		";bridge=" + strconv.Quote(bpdu.BridgeID.String()) +
		";port_id=" + strconv.FormatUint(uint64(bpdu.PortID), 10) +
		";message_age=" + strconv.FormatInt(int64(bpdu.MessageAge), 10) +
		";max_age=" + strconv.FormatInt(int64(bpdu.MaxAge), 10) +
		";hello=" + strconv.FormatInt(int64(bpdu.HelloTime), 10) +
		";forward_delay=" + strconv.FormatInt(int64(bpdu.ForwardDelay), 10) + "}"
}

func portInfoSnapshot(info PortInfo) string {
	return "{role=" + strconv.Quote(string(info.Role)) +
		";state=" + strconv.Quote(string(info.State)) +
		";priority=" + strconv.FormatUint(uint64(info.Priority), 10) +
		";path_cost=" + strconv.FormatUint(uint64(info.PathCost), 10) +
		";designated_root=" + strconv.Quote(info.DesignatedRoot.String()) +
		";designated=" + strconv.Quote(info.Designated.String()) +
		";designated_port=" + strconv.FormatUint(uint64(info.DesignatedPort), 10) +
		";designated_cost=" + strconv.FormatUint(uint64(info.DesignatedCost), 10) +
		";point_to_point=" + strconv.FormatBool(info.PointToPoint) +
		";edge=" + strconv.FormatBool(info.Edge) +
		";forward_transitions=" + strconv.FormatUint(info.ForwardTransitions, 10) +
		";tx_bpdus=" + strconv.FormatUint(info.TxBPDUs, 10) +
		";rx_bpdus=" + strconv.FormatUint(info.RxBPDUs, 10) +
		";bad_bpdus=" + strconv.FormatUint(info.BadBPDUs, 10) +
		";send_rstp=" + strconv.FormatBool(info.SendRSTP) + "}"
}
