package stp

import (
	"strconv"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

type bpduDecisionFact string

func (f bpduDecisionFact) TypeID() string    { return "stp.bpdu_decision" }
func (f bpduDecisionFact) Canonical() string { return string(f) }

type forwardingDecisionFact string

func (f forwardingDecisionFact) TypeID() string    { return "stp.forwarding_decision" }
func (f forwardingDecisionFact) Canonical() string { return string(f) }

// BPDUDecisionFact returns an immutable snapshot of a BPDU and the port state
// before and after applying it.
func BPDUDecisionFact(b bpdu.BPDU, before, after PortInfo) trace.Fact {
	return bpduDecisionFact("bpdu=" + bpduSnapshot(b) +
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
// to gate bridge learning and forwarding. The state resolves against the tree
// carrying vid rather than the CIST, because Learns and Forwards already
// answered from that tree: rendering the CIST's role and state beside a
// forwards=false the gate took from an MSTI would put a fact and its own
// decision in the same step contradicting each other. A VLAN with no tree of
// its own renders the zero snapshot, since Learns and Forwards did not
// consult a tree either.
func (l *Layer) ForwardingFact(port string, vid vlan.ID, learns, forwards bool) trace.Fact {
	t, ok := l.treeFor(vid)

	var info PortInfo
	if ok {
		info = l.portInfo(t, port)
	}

	return forwardingDecisionFact("port=" + strconv.Quote(port) +
		";vid=" + strconv.FormatUint(uint64(vid), 10) +
		";state=" + portInfoSnapshot(info) +
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

func bpduSnapshot(b bpdu.BPDU) string {
	return "{version=" + strconv.FormatUint(uint64(b.Version), 10) +
		";type=" + strconv.FormatUint(uint64(b.Type), 10) +
		";flags=" + strconv.FormatUint(uint64(b.Flags), 10) +
		";root=" + strconv.Quote(b.RootID.String()) +
		";root_cost=" + strconv.FormatUint(uint64(b.RootPathCost), 10) +
		";bridge=" + strconv.Quote(b.BridgeID.String()) +
		";port_id=" + strconv.FormatUint(uint64(b.PortID), 10) +
		";message_age=" + strconv.FormatInt(int64(b.MessageAge), 10) +
		";max_age=" + strconv.FormatInt(int64(b.MaxAge), 10) +
		";hello=" + strconv.FormatInt(int64(b.HelloTime), 10) +
		";forward_delay=" + strconv.FormatInt(int64(b.ForwardDelay), 10) + "}"
}

func portInfoSnapshot(info PortInfo) string {
	return "{mstid=" + strconv.FormatUint(uint64(info.MSTID), 10) +
		";role=" + strconv.Quote(string(info.Role)) +
		";state=" + strconv.Quote(string(info.State)) +
		";block_reason=" + strconv.Quote(string(info.BlockReason)) +
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
