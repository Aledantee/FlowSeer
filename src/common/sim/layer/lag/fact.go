package lag

import (
	"encoding/hex"
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
func (l *Layer) SelectionFact(lagName string, f ethernet.Frame, vid vlan.ID, sel Selection) trace.Fact {
	lagState, ok := l.lags[lagName]
	if !ok {
		return selectionFact("lag=" + strconv.Quote(lagName) + ";present=false;selected=false")
	}

	var b strings.Builder
	b.WriteString("lag=")
	b.WriteString(strconv.Quote(lagName))
	b.WriteString(";present=true;mode=")
	b.WriteString(strconv.Quote(string(lagState.cfg.Mode)))
	b.WriteString(";hash_basis=")
	b.WriteString(strconv.FormatUint(uint64(lagState.cfg.HashBasis), 10))
	b.WriteByte(';')

	switch lagState.cfg.Mode {
	case BalanceSLB:
		b.WriteString("src=")
		b.WriteString(strconv.Quote(f.Src.String()))
		b.WriteString(";vid=")
		b.WriteString(strconv.FormatUint(uint64(vid), 10))
		b.WriteByte(';')
	case BalanceTCP:
		input := inspectTCPHashInput(f)
		ipSrc := ""
		ipDst := ""
		transport4 := ""
		if input.ipDecoded {
			ipSrc = input.ipSrc.String()
			ipDst = input.ipDst.String()
		}
		if input.hasTransport {
			transport4 = hex.EncodeToString(input.transport4[:])
		}

		b.WriteString("src=")
		b.WriteString(strconv.Quote(f.Src.String()))
		b.WriteString(";dst=")
		b.WriteString(strconv.Quote(f.Dst.String()))
		b.WriteString(";ether_type=")
		b.WriteString(strconv.FormatUint(uint64(f.EtherType), 10))
		b.WriteString(";ip_decoded=")
		b.WriteString(strconv.FormatBool(input.ipDecoded))
		b.WriteString(";ip_src=")
		b.WriteString(strconv.Quote(ipSrc))
		b.WriteString(";ip_dst=")
		b.WriteString(strconv.Quote(ipDst))
		b.WriteString(";ip_protocol=")
		b.WriteString(strconv.FormatUint(uint64(input.ipProtocol), 10))
		b.WriteString(";transport_4=")
		b.WriteString(strconv.Quote(transport4))
		b.WriteByte(';')
	default:
		b.WriteString("primary=")
		b.WriteString(strconv.Quote(lagState.cfg.Primary))
		b.WriteByte(';')
	}

	b.WriteString("enabled=")
	b.WriteString(quotedStrings(lagState.enabledOrder))
	b.WriteString(";member=")
	b.WriteString(strconv.Quote(sel.Member))
	b.WriteString(";selected=")
	b.WriteString(strconv.FormatBool(sel.OK))
	b.WriteString(";bucket=")
	b.WriteString(strconv.FormatUint(uint64(sel.Bucket), 10))
	b.WriteString(";prior=")
	b.WriteString(strconv.Quote(sel.Prior))
	b.WriteString(";cause=")
	b.WriteString(strconv.Quote(string(sel.Cause)))
	b.WriteString(";rebalance_unmodeled=")
	b.WriteString(strconv.FormatBool(sel.RebalanceUnmodeled))
	b.WriteByte(';')

	return selectionFact(b.String())
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
