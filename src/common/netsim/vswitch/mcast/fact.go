package mcast

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/mld"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

type membershipDecisionFact string

func (f membershipDecisionFact) TypeID() string    { return "mcast.membership_decision" }
func (f membershipDecisionFact) Canonical() string { return string(f) }

type controlDecisionFact string

func (f controlDecisionFact) TypeID() string    { return "mcast.control_decision" }
func (f controlDecisionFact) Canonical() string { return string(f) }

type controlMessageFact string

func (f controlMessageFact) TypeID() string    { return "mcast.control_message" }
func (f controlMessageFact) Canonical() string { return string(f) }

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

// IGMPControlMessageFact returns an immutable semantic snapshot of a decoded
// IGMP message and its group-record transitions.
func IGMPControlMessageFact(source netip.Addr, message igmp.Message) trace.Fact {
	records := make([]string, len(message.Records))
	for i, record := range message.Records {
		records[i] = controlRecord(uint64(record.Type), record.Group, record.Sources)
	}

	return controlMessageFact(controlMessage(
		"igmp",
		uint64(message.Type),
		uint64(message.Version),
		source,
		message.Group,
		message.Sources,
		records,
	))
}

// MLDControlMessageFact returns an immutable semantic snapshot of a decoded
// MLD message and its address-record transitions.
func MLDControlMessageFact(source netip.Addr, message mld.Message) trace.Fact {
	records := make([]string, len(message.Records))
	for i, record := range message.Records {
		records[i] = controlRecord(uint64(record.Type), record.Group, record.Sources)
	}

	return controlMessageFact(controlMessage(
		"mld",
		uint64(message.Type),
		uint64(message.Version),
		source,
		message.Group,
		message.Sources,
		records,
	))
}

func controlMessage(protocol string, messageType, version uint64, source, group netip.Addr, sources []netip.Addr, records []string) string {
	return "protocol=" + strconv.Quote(protocol) +
		";message_type=" + strconv.FormatUint(messageType, 10) +
		";version=" + strconv.FormatUint(version, 10) +
		";source=" + strconv.Quote(source.String()) +
		";group=" + strconv.Quote(group.String()) +
		";sources=" + controlSources(sources) +
		";records=[" + strings.Join(records, ",") + "]"
}

func controlRecord(recordType uint64, group netip.Addr, sources []netip.Addr) string {
	return "{record_type=" + strconv.FormatUint(recordType, 10) +
		";group=" + strconv.Quote(group.String()) +
		";sources=" + controlSources(sources) + "}"
}

func controlSources(sources []netip.Addr) string {
	normalized := slices.Clone(sources)
	slices.SortFunc(normalized, netip.Addr.Compare)

	var b strings.Builder
	b.WriteByte('[')
	for i, source := range normalized {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Quote(source.String()))
	}
	b.WriteByte(']')

	return b.String()
}
