package stp

import (
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

const (
	// ReasonUnsupportedBPDU indicates that a received frame could not be decoded
	// as a BPDU because of an unexpected LLC header, protocol identifier,
	// version, or BPDU type.
	ReasonUnsupportedBPDU trace.Reason = "unsupported-bpdu"

	// ReasonVLANNotAdmitted indicates that an SSTP BPDU decoded but the
	// bridge does not admit its arrival VLAN on the port it arrived on.
	ReasonVLANNotAdmitted trace.Reason = "vlan-not-admitted"

	// ReasonVLANUntracked indicates that an SSTP BPDU decoded and was
	// admitted, but this bridge runs PVST and has no tree for its arrival
	// VLAN.
	ReasonVLANUntracked trace.Reason = "vlan-untracked"
)

// State represents the spanning tree frame forwarding state of a port.
type State string

const (
	// StateDiscarding drops received frames and prevents frame transmission and address learning.
	StateDiscarding State = "Discarding"

	// StateLearning learns source MAC addresses into the filtering database without forwarding frames.
	StateLearning State = "Learning"

	// StateForwarding learns source MAC addresses and forwards traffic across the bridge.
	StateForwarding State = "Forwarding"
)
