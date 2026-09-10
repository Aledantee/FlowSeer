package bridge

import (
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

const (
	// ReasonPortDown indicates a frame dropped because an ingress or egress port is down.
	ReasonPortDown trace.Reason = "port-down"

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

	// ReasonMTUExceeded indicates a frame dropped on an egress port because its payload length exceeds the port MTU.
	ReasonMTUExceeded trace.Reason = "mtu-exceeded"
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
