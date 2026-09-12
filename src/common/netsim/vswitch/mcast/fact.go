package mcast

import (
	"net/netip"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

type membershipDecisionFact string

func (f membershipDecisionFact) TypeID() string    { return "mcast.membership_decision" }
func (f membershipDecisionFact) Canonical() string { return string(f) }

type controlDecisionFact string

func (f controlDecisionFact) TypeID() string    { return "mcast.control_decision" }
func (f controlDecisionFact) Canonical() string { return string(f) }

// MembershipDecisionFact returns an immutable snapshot of a multicast group lookup.
func MembershipDecisionFact(vid vlan.ID, group netip.Addr, ports []string, registered, decided bool) trace.Fact {
	var b strings.Builder
	b.WriteString("fid=")
	b.WriteString(strconv.FormatUint(uint64(vid), 10))
	b.WriteString(";group=")
	b.WriteString(strconv.Quote(group.String()))
	b.WriteString(";registered=")
	b.WriteString(strconv.FormatBool(registered))
	b.WriteString(";decided=")
	b.WriteString(strconv.FormatBool(decided))
	b.WriteString(";ports=[")
	for i, portName := range ports {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Quote(portName))
	}
	b.WriteByte(']')

	return membershipDecisionFact(b.String())
}

// ControlDecisionFact returns an immutable snapshot of multicast-control validation.
func ControlDecisionFact(protocol string, admitted bool, reason trace.Reason) trace.Fact {
	return controlDecisionFact("protocol=" + strconv.Quote(protocol) +
		";admitted=" + strconv.FormatBool(admitted) +
		";reason=" + strconv.Quote(string(reason)))
}
