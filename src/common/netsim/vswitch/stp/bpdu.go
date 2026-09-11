package stp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

const (
	// ReasonUnsupportedBPDU indicates that a received frame could not be decoded
	// as an RST BPDU because of an unexpected LLC header, protocol identifier,
	// version, or BPDU type.
	ReasonUnsupportedBPDU trace.Reason = "unsupported-bpdu"
)

// Role represents the spanning tree role assigned to a port.
type Role string

const (
	// RoleRoot identifies the port providing the lowest-cost path to the root bridge.
	RoleRoot Role = "Root"

	// RoleDesignated identifies the port transmitting configuration BPDUs onto its attached LAN.
	RoleDesignated Role = "Designated"

	// RoleAlternate identifies an alternative path to the root bridge discarded to prevent loops.
	RoleAlternate Role = "Alternate"

	// RoleBackup identifies an alternative path to the attached LAN discarded to prevent loops.
	RoleBackup Role = "Backup"

	// RoleDisabled identifies a port that is administratively or operationally inactive.
	RoleDisabled Role = "Disabled"
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

// BridgeID identifies a spanning tree bridge by its administrative priority
// and unique hardware MAC address.
type BridgeID struct {
	Priority uint16
	Address  netaddr.MAC
}

// Less reports whether b is smaller than other, comparing priority first and
// hardware address lexicographically second.
func (b BridgeID) Less(other BridgeID) bool {
	if b.Priority != other.Priority {
		return b.Priority < other.Priority
	}

	return bytes.Compare(b.Address[:], other.Address[:]) < 0
}

// String returns the string representation of b in priority/address format.
func (b BridgeID) String() string {
	return fmt.Sprintf("%d/%s", b.Priority, b.Address)
}

const (
	flagTopologyChange    uint8 = 1 << 0
	flagProposal          uint8 = 1 << 1
	flagPortRoleMask      uint8 = 3 << 2
	flagPortRoleShift     uint8 = 2
	flagLearning          uint8 = 1 << 4
	flagForwarding        uint8 = 1 << 5
	flagAgreement         uint8 = 1 << 6
	flagTopologyChangeAck uint8 = 1 << 7
)

// BPDU represents an IEEE 802.1D-2004 Rapid Spanning Tree Bridge Protocol Data Unit.
type BPDU struct {
	Flags        uint8
	RootID       BridgeID
	RootPathCost uint32
	BridgeID     BridgeID
	PortID       uint16
	MessageAge   time.Duration
	MaxAge       time.Duration
	HelloTime    time.Duration
	ForwardDelay time.Duration
}

// Role returns the port role carried in the BPDU flags.
func (b BPDU) Role() Role {
	switch (b.Flags & flagPortRoleMask) >> flagPortRoleShift {
	case 1:
		return RoleAlternate
	case 2:
		return RoleRoot
	case 3:
		return RoleDesignated
	default:
		return RoleDisabled
	}
}

// SetRole sets the port role in the BPDU flags.
func (b *BPDU) SetRole(r Role) {
	b.Flags &^= flagPortRoleMask
	switch r {
	case RoleAlternate, RoleBackup:
		b.Flags |= 1 << flagPortRoleShift
	case RoleRoot:
		b.Flags |= 2 << flagPortRoleShift
	case RoleDesignated:
		b.Flags |= 3 << flagPortRoleShift
	case RoleDisabled:
	}
}

// Proposal reports whether the proposal flag bit is set.
func (b BPDU) Proposal() bool {
	return b.Flags&flagProposal != 0
}

// SetProposal sets or clears the proposal flag bit.
func (b *BPDU) SetProposal(v bool) {
	if v {
		b.Flags |= flagProposal
	} else {
		b.Flags &^= flagProposal
	}
}

// Agreement reports whether the agreement flag bit is set.
func (b BPDU) Agreement() bool {
	return b.Flags&flagAgreement != 0
}

// SetAgreement sets or clears the agreement flag bit.
func (b *BPDU) SetAgreement(v bool) {
	if v {
		b.Flags |= flagAgreement
	} else {
		b.Flags &^= flagAgreement
	}
}

// Learning reports whether the learning flag bit is set.
func (b BPDU) Learning() bool {
	return b.Flags&flagLearning != 0
}

// SetLearning sets or clears the learning flag bit.
func (b *BPDU) SetLearning(v bool) {
	if v {
		b.Flags |= flagLearning
	} else {
		b.Flags &^= flagLearning
	}
}

// Forwarding reports whether the forwarding flag bit is set.
func (b BPDU) Forwarding() bool {
	return b.Flags&flagForwarding != 0
}

// SetForwarding sets or clears the forwarding flag bit.
func (b *BPDU) SetForwarding(v bool) {
	if v {
		b.Flags |= flagForwarding
	} else {
		b.Flags &^= flagForwarding
	}
}

// TopologyChange reports whether the topology change flag bit is set.
func (b BPDU) TopologyChange() bool {
	return b.Flags&flagTopologyChange != 0
}

// SetTopologyChange sets or clears the topology change flag bit.
func (b *BPDU) SetTopologyChange(v bool) {
	if v {
		b.Flags |= flagTopologyChange
	} else {
		b.Flags &^= flagTopologyChange
	}
}

// TopologyChangeAck reports whether the topology change acknowledgement flag bit is set.
func (b BPDU) TopologyChangeAck() bool {
	return b.Flags&flagTopologyChangeAck != 0
}

// SetTopologyChangeAck sets or clears the topology change acknowledgement flag bit.
func (b *BPDU) SetTopologyChangeAck(v bool) {
	if v {
		b.Flags |= flagTopologyChangeAck
	} else {
		b.Flags &^= flagTopologyChangeAck
	}
}

var stpGroupAddress = netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00}

const (
	// llcBPDULength is the LLC header plus the RST BPDU body, the value the
	// 802.3 length field carries.
	llcBPDULength = 3 + 36
	// minDataLength pads the frame to the 802.3 minimum of 60 octets before
	// the check sequence, as a capture would show it.
	minDataLength = 46
)

// Encode serializes b into an untagged IEEE 802.3 LLC frame addressed to the
// standard bridge group address (01:80:c2:00:00:00), padded to 60 octets.
func Encode(b BPDU, src netaddr.MAC) ethernet.Frame {
	payload := make([]byte, minDataLength)
	payload[0] = 0x42
	payload[1] = 0x42
	payload[2] = 0x03
	binary.BigEndian.PutUint16(payload[3:5], 0x0000)
	payload[5] = 2
	payload[6] = 2
	payload[7] = b.Flags
	binary.BigEndian.PutUint16(payload[8:10], b.RootID.Priority)
	copy(payload[10:16], b.RootID.Address[:])
	binary.BigEndian.PutUint32(payload[16:20], b.RootPathCost)
	binary.BigEndian.PutUint16(payload[20:22], b.BridgeID.Priority)
	copy(payload[22:28], b.BridgeID.Address[:])
	binary.BigEndian.PutUint16(payload[28:30], b.PortID)
	binary.BigEndian.PutUint16(payload[30:32], encodeDuration(b.MessageAge))
	binary.BigEndian.PutUint16(payload[32:34], encodeDuration(b.MaxAge))
	binary.BigEndian.PutUint16(payload[34:36], encodeDuration(b.HelloTime))
	binary.BigEndian.PutUint16(payload[36:38], encodeDuration(b.ForwardDelay))
	payload[38] = 0

	return ethernet.Frame{
		Dst:       stpGroupAddress,
		Src:       src,
		EtherType: ethernet.EtherType(llcBPDULength),
		Payload:   payload,
	}
}

// Decode deserializes an RST BPDU from the payload of an Ethernet frame. It
// rejects frames with truncated payloads, unexpected LLC headers, protocol
// identifiers other than 0, versions below 2, or types other than 2 with
// [ReasonUnsupportedBPDU].
func Decode(f ethernet.Frame) (BPDU, error) {
	if len(f.Payload) < 39 {
		return BPDU{}, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("have", len(f.Payload)).
			Attr("min", 39).
			Msgf("BPDU payload length %d is too short", len(f.Payload))
	}

	dsap := f.Payload[0]
	if dsap != 0x42 {
		return BPDU{}, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("dsap", dsap).
			Msgf("unsupported BPDU LLC DSAP 0x%02x, want 0x42", dsap)
	}

	ssap := f.Payload[1]
	if ssap != 0x42 {
		return BPDU{}, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("ssap", ssap).
			Msgf("unsupported BPDU LLC SSAP 0x%02x, want 0x42", ssap)
	}

	control := f.Payload[2]
	if control != 0x03 {
		return BPDU{}, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("control", control).
			Msgf("unsupported BPDU LLC control 0x%02x, want 0x03", control)
	}

	protoID := binary.BigEndian.Uint16(f.Payload[3:5])
	if protoID != 0 {
		return BPDU{}, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("protocol_id", protoID).
			Msgf("unsupported BPDU protocol identifier 0x%04x, want 0x0000", protoID)
	}

	version := f.Payload[5]
	if version < 2 {
		return BPDU{}, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("version", version).
			Msgf("unsupported BPDU version %d, want at least 2", version)
	}

	bpduType := f.Payload[6]
	if bpduType != 2 {
		return BPDU{}, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("type", bpduType).
			Msgf("unsupported BPDU type %d, want 2", bpduType)
	}

	rootPriority := binary.BigEndian.Uint16(f.Payload[8:10])
	var rootAddr netaddr.MAC
	copy(rootAddr[:], f.Payload[10:16])

	bridgePriority := binary.BigEndian.Uint16(f.Payload[20:22])
	var bridgeAddr netaddr.MAC
	copy(bridgeAddr[:], f.Payload[22:28])

	// Received information ages on the sender's hello time, so a zero one
	// would be stale the instant it arrived and never elect anything.
	if binary.BigEndian.Uint16(f.Payload[34:36]) == 0 {
		return BPDU{}, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Msg("BPDU hello time is zero")
	}

	return BPDU{
		Flags:        f.Payload[7],
		RootID:       BridgeID{Priority: rootPriority, Address: rootAddr},
		RootPathCost: binary.BigEndian.Uint32(f.Payload[16:20]),
		BridgeID:     BridgeID{Priority: bridgePriority, Address: bridgeAddr},
		PortID:       binary.BigEndian.Uint16(f.Payload[28:30]),
		MessageAge:   decodeDuration(binary.BigEndian.Uint16(f.Payload[30:32])),
		MaxAge:       decodeDuration(binary.BigEndian.Uint16(f.Payload[32:34])),
		HelloTime:    decodeDuration(binary.BigEndian.Uint16(f.Payload[34:36])),
		ForwardDelay: decodeDuration(binary.BigEndian.Uint16(f.Payload[36:38])),
	}, nil
}

func encodeDuration(d time.Duration) uint16 {
	if d <= 0 {
		return 0
	}

	return uint16((d * 256) / time.Second)
}

func decodeDuration(units uint16) time.Duration {
	return time.Duration(units) * time.Second / 256
}
