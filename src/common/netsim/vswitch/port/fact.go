package port

import (
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

type forwardingFact string

func (f forwardingFact) TypeID() string    { return "port.forwarding" }
func (f forwardingFact) Canonical() string { return string(f) }

// ForwardingFact returns an immutable snapshot of the port state consulted by
// a forwarding decision. A zero port records a missing named port.
func ForwardingFact(name string, p Port, eligible bool, reason trace.Reason) trace.Fact {
	var b strings.Builder
	b.WriteString("name=")
	b.WriteString(strconv.Quote(name))
	b.WriteString(";present=")
	b.WriteString(strconv.FormatBool(p.Name != ""))
	b.WriteString(";kind=")
	b.WriteString(strconv.Quote(string(p.Kind)))
	b.WriteString(";admin=")
	b.WriteString(strconv.Quote(string(p.AdminStatus)))
	b.WriteString(";oper=")
	b.WriteString(strconv.Quote(string(p.OperStatus)))
	b.WriteString(";mtu=")
	b.WriteString(strconv.Itoa(p.MTU))
	b.WriteString(";lag_parent=")
	b.WriteString(strconv.Quote(p.LagParent))
	b.WriteString(";eligible=")
	b.WriteString(strconv.FormatBool(eligible))
	b.WriteString(";reason=")
	b.WriteString(strconv.Quote(string(reason)))

	return forwardingFact(b.String())
}

type hubEgressFact string

func (f hubEgressFact) TypeID() string    { return "port.hub_egress" }
func (f hubEgressFact) Canonical() string { return string(f) }

// HubEgressFact returns an immutable snapshot of a hub replication decision.
func HubEgressFact(eligible, forwarding int, reason trace.Reason) trace.Fact {
	return hubEgressFact("eligible=" + strconv.Itoa(eligible) +
		";forwarding=" + strconv.Itoa(forwarding) +
		";reason=" + strconv.Quote(string(reason)))
}
