package bridge

import (
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
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

	// ReasonNoEgress indicates replication with no forwarding logical port other than the ingress port.
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

// Egress records the transmission or per-port drop of a frame on a specific
// egress port. PCP is the classified ingress priority, including when Frame no
// longer carries a VLAN tag on an untagged egress.
type Egress struct {
	Port    string
	Member  string
	Frame   ethernet.Frame
	PCP     vlan.PCP
	Dropped trace.Reason
}

// Result embeds [trace.Trace] and includes structured bridge forwarding metadata.
type Result struct {
	trace.Trace
	Ingress        string
	FID            vlan.ID
	Egress         []Egress
	consultedPorts []port.Port
}

// ConsultedPorts returns independent snapshots of the port state that could
// change this forwarding result, in first-consulted order.
func (r Result) ConsultedPorts() []port.Port {
	return append([]port.Port(nil), r.consultedPorts...)
}

// Consult records port-state snapshots that could change this forwarding result.
func (r *Result) Consult(ports ...port.Port) {
	for _, candidate := range ports {
		if candidate.Name == "" {
			continue
		}
		seen := false
		for _, existing := range r.consultedPorts {
			if existing.Name == candidate.Name {
				seen = true
				break
			}
		}
		if !seen {
			r.consultedPorts = append(r.consultedPorts, candidate)
		}
	}
}
