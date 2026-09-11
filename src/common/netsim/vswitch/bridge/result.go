package bridge

import (
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

const (
	// ReasonReservedAddress indicates a frame dropped because its destination is in the IEEE reserved bridge group address range.
	ReasonReservedAddress trace.Reason = "reserved-address"

	// ReasonAdmission indicates a frame dropped because its tag format was rejected by port admission policy.
	ReasonAdmission trace.Reason = "admission"

	// ReasonIngressFilter indicates a frame dropped because the ingress port is not a member of the classified VLAN.
	ReasonIngressFilter trace.Reason = "ingress-filter"

	// ReasonUndefinedVLAN indicates a frame dropped because its classified VLAN is not defined in the VLAN table.
	ReasonUndefinedVLAN trace.Reason = "undefined-vlan"

	// ReasonNoPVID indicates an untagged or priority-tagged frame dropped because the ingress port has no configured PVID.
	ReasonNoPVID trace.Reason = "no-pvid"

	// ReasonSamePort indicates a frame dropped because its destination port resolves back to the ingress port.
	ReasonSamePort trace.Reason = "same-port"

	// ReasonNotMember indicates a known unicast whose destination port is not a member of the classified VLAN.
	ReasonNotMember trace.Reason = "not-member"

	// ReasonNoEgress indicates a flood with no forwarding member port other than the ingress port.
	ReasonNoEgress trace.Reason = "no-egress"

	// ReasonPortBlocked indicates a frame dropped because a port is blocked from learning or forwarding.
	ReasonPortBlocked trace.Reason = "port-blocked"

	// ReasonProtected indicates a frame dropped because transmission between protected ports is prohibited.
	ReasonProtected trace.Reason = "protected"

	// ReasonCustomerVLAN indicates a frame dropped on a tunnel port because its customer VLAN was not permitted.
	ReasonCustomerVLAN trace.Reason = "customer-vlan"

	// ReasonNoMember indicates a frame dropped because no member port was selected for LAG egress.
	ReasonNoMember trace.Reason = "no-member"
)

// Egress records the transmission or per-port drop of a frame on a specific egress port.
type Egress struct {
	Port    string
	Member  string
	Frame   ethernet.Frame
	Dropped trace.Reason
}

// Result embeds [trace.Trace] and includes structured bridge forwarding metadata.
type Result struct {
	trace.Trace
	Ingress string
	FID     vlan.ID
	Egress  []Egress
}
