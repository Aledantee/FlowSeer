package lag

import (
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

type selectionFact string

func (f selectionFact) TypeID() string    { return "lag.selection" }
func (f selectionFact) Canonical() string { return string(f) }

type lacpDecisionFact string

func (f lacpDecisionFact) TypeID() string    { return "lag.lacp_decision" }
func (f lacpDecisionFact) Canonical() string { return string(f) }

// SelectionFact returns an immutable snapshot of a LAG member-selection decision.
func (l *Layer) SelectionFact(lagName string, f ethernet.Frame, vid vlan.ID, member string, selected bool) trace.Fact {
	lagState, ok := l.lags[lagName]
	if !ok {
		return selectionFact("lag=" + strconv.Quote(lagName) + ";present=false;selected=false")
	}

	return selectionFact("lag=" + strconv.Quote(lagName) +
		";present=true;mode=" + strconv.Quote(string(lagState.cfg.Mode)) +
		";vid=" + strconv.FormatUint(uint64(vid), 10) +
		";src=" + strconv.Quote(f.Src.String()) +
		";dst=" + strconv.Quote(f.Dst.String()) +
		";enabled=" + quotedStrings(lagState.enabledMembers) +
		";member=" + strconv.Quote(member) +
		";selected=" + strconv.FormatBool(selected))
}

// LACPDecisionFact returns an immutable snapshot of a received LACPDU and the
// member state before and after applying it.
func LACPDecisionFact(pdu lacp.PDU, before, after MemberInfo) trace.Fact {
	return lacpDecisionFact("actor=" + lacpInfoSnapshot(pdu.Actor) +
		";partner=" + lacpInfoSnapshot(pdu.Partner) +
		";before=" + memberInfoSnapshot(before) +
		";after=" + memberInfoSnapshot(after))
}

// LACPDecodeFact returns an immutable snapshot of a LACP frame decode decision.
func LACPDecodeFact(f ethernet.Frame, valid bool, reason trace.Reason) trace.Fact {
	return lacpDecisionFact("ether_type=" + strconv.FormatUint(uint64(f.EtherType), 10) +
		";payload_len=" + strconv.Itoa(len(f.Payload)) +
		";valid=" + strconv.FormatBool(valid) +
		";reason=" + strconv.Quote(string(reason)))
}

// MemberTransitionFact returns an immutable snapshot of a LAG member state transition.
func MemberTransitionFact(member, action string, before, after MemberInfo) trace.Fact {
	return lacpDecisionFact("member=" + strconv.Quote(member) +
		";action=" + strconv.Quote(action) +
		";before=" + memberInfoSnapshot(before) +
		";after=" + memberInfoSnapshot(after))
}

func lacpInfoSnapshot(info lacp.Info) string {
	return "{system_priority=" + strconv.FormatUint(uint64(info.SystemPriority), 10) +
		";system_id=" + strconv.Quote(info.SystemID.String()) +
		";key=" + strconv.FormatUint(uint64(info.Key), 10) +
		";port_priority=" + strconv.FormatUint(uint64(info.PortPriority), 10) +
		";port_id=" + strconv.FormatUint(uint64(info.PortID), 10) +
		";state=" + strconv.FormatUint(uint64(info.State), 10) + "}"
}

func memberInfoSnapshot(info MemberInfo) string {
	return "{link_up=" + strconv.FormatBool(info.LinkUp) +
		";enabled=" + strconv.FormatBool(info.Enabled) +
		";attached=" + strconv.FormatBool(info.Attached) +
		";status=" + strconv.Quote(string(info.Status)) +
		";actor=" + lacpInfoSnapshot(info.Actor) +
		";partner=" + lacpInfoSnapshot(info.Partner) +
		";lacpdus_tx=" + strconv.FormatUint(info.LACPDUsTx, 10) +
		";lacpdus_rx=" + strconv.FormatUint(info.LACPDUsRx, 10) +
		";bad_lacpdus=" + strconv.FormatUint(info.BadLACPDUs, 10) + "}"
}

func quotedStrings(values []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Quote(value))
	}
	b.WriteByte(']')

	return b.String()
}
