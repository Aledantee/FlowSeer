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
	// as a BPDU because of an unexpected LLC header, protocol identifier,
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

// BPDUType identifies the format and purpose of a Spanning Tree Bridge Protocol Data Unit.
//
// IEEE 802.1D-2004 clause 9.3 defines three BPDU types: Configuration BPDUs (clause 9.3.1,
// 35 octets after the LLC header), Topology Change Notification BPDUs (clause 9.3.2,
// 4 octets after the LLC header), and Rapid Spanning Tree BPDUs (clause 9.3.3, 36 octets
// after the LLC header).
type BPDUType uint8

const (
	// BPDUTypeRapid identifies an IEEE 802.1D-2004 Rapid Spanning Tree BPDU
	// (clause 9.3.3, wire type 0x02).
	BPDUTypeRapid BPDUType = iota

	// BPDUTypeConfiguration identifies a legacy IEEE 802.1D Configuration BPDU
	// (clause 9.3.1, wire type 0x00).
	BPDUTypeConfiguration

	// BPDUTypeTopologyChangeNotification identifies a legacy IEEE 802.1D Topology Change
	// Notification BPDU (clause 9.3.2, wire type 0x80).
	BPDUTypeTopologyChangeNotification
)

const (
	bpduTypeWireConfig = 0x00
	bpduTypeWireRST    = 0x02
	bpduTypeWireTCN    = 0x80
)

// BPDU represents an IEEE 802.1D Spanning Tree Bridge Protocol Data Unit.
//
// The zero value represents an RST BPDU ([BPDUTypeRapid]).
type BPDU struct {
	Version      uint8
	Type         BPDUType
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
//
// For legacy Configuration BPDUs ([BPDUTypeConfiguration]), the role is always
// [RoleDesignated].
func (b BPDU) Role() Role {
	if b.Type == BPDUTypeConfiguration {
		return RoleDesignated
	}

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
	if b.Type == BPDUTypeConfiguration {
		return false
	}

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
	if b.Type == BPDUTypeConfiguration {
		return false
	}

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
	// llcBPDULength is the LLC header plus the RST BPDU body (IEEE 802.1D-2004
	// clause 9.3.3), the value the 802.3 length field carries.
	llcBPDULength = 3 + 36

	// llcConfigBPDULength is the LLC header plus the Configuration BPDU body
	// (IEEE 802.1D-2004 clause 9.3.1), the value the 802.3 length field carries.
	llcConfigBPDULength = 3 + 35

	// llcTCNBPDULength is the LLC header plus the Topology Change Notification
	// BPDU body (IEEE 802.1D-2004 clause 9.3.2), the value the 802.3 length
	// field carries.
	llcTCNBPDULength = 3 + 4

	// minDataLength pads the frame to the 802.3 minimum of 60 octets before
	// the check sequence, as a capture would show it.
	minDataLength = 46
)

// Encode serializes b into an untagged IEEE 802.3 LLC frame addressed to the
// standard bridge group address (01:80:c2:00:00:00), padded to 60 octets.
//
// Encode supports all three IEEE 802.1D-2004 clause 9.3 shapes:
//   - [BPDUTypeRapid] (or zero value) writes an RST BPDU (clause 9.3.3) with version 2
//     (or b.Version when at least 2), wire type 0x02, and LLC length 39.
//   - [BPDUTypeConfiguration] writes a Configuration BPDU (clause 9.3.1) with version 0
//     (or b.Version when 0 or 1), wire type 0x00, LLC length 38, and flags masked to
//     Topology Change (bit 0) and Topology Change Acknowledgment (bit 7).
//   - [BPDUTypeTopologyChangeNotification] writes a Topology Change Notification BPDU
//     (clause 9.3.2) with version 0, wire type 0x80, LLC length 7, and no body fields.
func Encode(b BPDU, src netaddr.MAC) ethernet.Frame {
	payload := make([]byte, minDataLength)
	payload[0] = 0x42
	payload[1] = 0x42
	payload[2] = 0x03
	binary.BigEndian.PutUint16(payload[3:5], 0x0000)

	switch b.Type {
	case BPDUTypeConfiguration:
		version := b.Version
		if version > 1 {
			version = 0
		}
		payload[5] = version
		payload[6] = bpduTypeWireConfig
		payload[7] = b.Flags & (flagTopologyChange | flagTopologyChangeAck)
		putBody(payload, b)

		return ethernet.Frame{
			Dst:       stpGroupAddress,
			Src:       src,
			EtherType: ethernet.EtherType(llcConfigBPDULength),
			Payload:   payload,
		}

	case BPDUTypeTopologyChangeNotification:
		payload[5] = 0
		payload[6] = bpduTypeWireTCN

		return ethernet.Frame{
			Dst:       stpGroupAddress,
			Src:       src,
			EtherType: ethernet.EtherType(llcTCNBPDULength),
			Payload:   payload,
		}

	default:
		version := b.Version
		if version < 2 {
			version = 2
		}
		payload[5] = version
		payload[6] = bpduTypeWireRST
		payload[7] = b.Flags
		putBody(payload, b)
		payload[38] = 0

		return ethernet.Frame{
			Dst:       stpGroupAddress,
			Src:       src,
			EtherType: ethernet.EtherType(llcBPDULength),
			Payload:   payload,
		}
	}
}

// Decode deserializes an IEEE 802.1D Spanning Tree BPDU from the payload of an
// Ethernet frame.
//
// Decode recognizes three shapes defined by IEEE 802.1D-2004 clause 9.3:
//   - Configuration BPDUs (clause 9.3.1): version 0 or 1, wire type 0x00, requiring at
//     least 38 payload octets. Decoded flags are masked to bits 0 and 7; [BPDU.Role]
//     reports [RoleDesignated].
//   - Topology Change Notification BPDUs (clause 9.3.2): version 0 or 1, wire type 0x80,
//     requiring at least 7 payload octets.
//   - Rapid Spanning Tree BPDUs (clause 9.3.3): version 2 or greater, wire type 0x02,
//     requiring at least 39 payload octets.
//
// Decode rejects frames with truncated payloads, unexpected LLC headers, protocol
// identifiers other than 0, unsupported version and type combinations, or zero hello
// time (on Configuration and RST shapes) with [ReasonUnsupportedBPDU].
func Decode(f ethernet.Frame) (BPDU, error) {
	if len(f.Payload) < 7 {
		return BPDU{}, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("have", len(f.Payload)).
			Attr("min", 7).
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
	bpduType := f.Payload[6]

	switch {
	case (version == 0 || version == 1) && bpduType == bpduTypeWireConfig:
		if len(f.Payload) < 38 {
			return BPDU{}, errs.New().
				Attr("reason", ReasonUnsupportedBPDU).
				Attr("have", len(f.Payload)).
				Attr("min", 38).
				Msgf("BPDU payload length %d is too short", len(f.Payload))
		}

		b, err := readBody(f.Payload)
		if err != nil {
			return BPDU{}, err
		}

		b.Version = version
		b.Type = BPDUTypeConfiguration
		b.Flags = f.Payload[7] & (flagTopologyChange | flagTopologyChangeAck)

		return b, nil

	case (version == 0 || version == 1) && bpduType == bpduTypeWireTCN:
		return BPDU{
			Version: version,
			Type:    BPDUTypeTopologyChangeNotification,
		}, nil

	case version >= 2 && bpduType == bpduTypeWireRST:
		if len(f.Payload) < 39 {
			return BPDU{}, errs.New().
				Attr("reason", ReasonUnsupportedBPDU).
				Attr("have", len(f.Payload)).
				Attr("min", 39).
				Msgf("BPDU payload length %d is too short", len(f.Payload))
		}

		b, err := readBody(f.Payload)
		if err != nil {
			return BPDU{}, err
		}

		b.Version = version
		b.Type = BPDUTypeRapid
		b.Flags = f.Payload[7]

		return b, nil

	default:
		if bpduType == bpduTypeWireRST {
			return BPDU{}, errs.New().
				Attr("reason", ReasonUnsupportedBPDU).
				Attr("version", version).
				Msgf("unsupported BPDU version %d, want at least 2", version)
		}
		if version >= 2 {
			return BPDU{}, errs.New().
				Attr("reason", ReasonUnsupportedBPDU).
				Attr("type", bpduType).
				Msgf("unsupported BPDU type %d, want 2", bpduType)
		}
		return BPDU{}, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("version", version).
			Attr("type", bpduType).
			Msgf("unsupported BPDU type 0x%02x for version %d", bpduType, version)
	}
}

// putBody writes the fields the Configuration and RST shapes share, from the
// root identifier at octet 8 through the forward delay at octet 37.
func putBody(payload []byte, b BPDU) {
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
}

// readBody reads the fields putBody writes. Received information ages on the
// sender's hello time, so a zero one would be stale the instant it arrived and
// never elect anything; it is refused.
func readBody(payload []byte) (BPDU, error) {
	if binary.BigEndian.Uint16(payload[34:36]) == 0 {
		return BPDU{}, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Msg("BPDU hello time is zero")
	}

	var rootAddr, bridgeAddr netaddr.MAC
	copy(rootAddr[:], payload[10:16])
	copy(bridgeAddr[:], payload[22:28])

	return BPDU{
		RootID:       BridgeID{Priority: binary.BigEndian.Uint16(payload[8:10]), Address: rootAddr},
		RootPathCost: binary.BigEndian.Uint32(payload[16:20]),
		BridgeID:     BridgeID{Priority: binary.BigEndian.Uint16(payload[20:22]), Address: bridgeAddr},
		PortID:       binary.BigEndian.Uint16(payload[28:30]),
		MessageAge:   decodeDuration(binary.BigEndian.Uint16(payload[30:32])),
		MaxAge:       decodeDuration(binary.BigEndian.Uint16(payload[32:34])),
		HelloTime:    decodeDuration(binary.BigEndian.Uint16(payload[34:36])),
		ForwardDelay: decodeDuration(binary.BigEndian.Uint16(payload[36:38])),
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
