package stp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
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

	// ReasonVLANNotAdmitted indicates that an SSTP BPDU decoded but the
	// bridge does not admit its arrival VLAN on the port it arrived on.
	ReasonVLANNotAdmitted trace.Reason = "vlan-not-admitted"

	// ReasonVLANUntracked indicates that an SSTP BPDU decoded and was
	// admitted, but this bridge runs PVST and has no tree for its arrival
	// VLAN.
	ReasonVLANUntracked trace.Reason = "vlan-untracked"
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

	// ConfigID, when non-nil, marks b as an IEEE 802.1Q MST BPDU (protocol
	// version 3) and carries the transmitting region's MST configuration
	// identifier. It stays nil for every other shape.
	ConfigID *ConfigID

	// RegionalRootID, InternalRootPathCost, and RemainingHops are the CIST
	// fields an MST BPDU carries in addition to the RST prefix above: the
	// root of the MST region (as opposed to RootID, the CIST root of the
	// whole bridged network), the path cost to that regional root, and the
	// hop count remaining before the BPDU is aged out within the region.
	RegionalRootID       BridgeID
	InternalRootPathCost uint32
	RemainingHops        uint8

	// MSTIs holds one record per MST Instance the transmitting bridge maps a
	// VLAN into.
	MSTIs []MSTIRecord
}

// MSTIRecord is one IEEE 802.1Q MST Instance record carried within an MST
// BPDU (protocol version 3).
type MSTIRecord struct {
	// MSTID identifies the instance. It has no octets of its own on the
	// wire: it rides in the low 12 bits of RegionalRootID.Priority (the
	// system ID extension), and [Encode] and [Decode] handle that encoding.
	// [Encode] replaces RegionalRootID.Priority's low 12 bits with MSTID on
	// the wire, and [Decode] re-derives both RegionalRootID.Priority and
	// MSTID from those same low 12 bits; the pair only round-trips through
	// RegionalRootID.Priority's top 4 bits, not its low 12.
	MSTID MSTID

	Flags                uint8
	RegionalRootID       BridgeID
	InternalRootPathCost uint32
	BridgePriority       uint8
	PortPriority         uint8
	RemainingHops        uint8
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

	// mstProtocolVersion is the IEEE 802.1Q protocol version identifier
	// (payload octet 5) that marks an MST BPDU.
	mstProtocolVersion = 3

	// mstBodyLength is the MST BPDU body length in octets, counted from the
	// protocol version identifier through the CIST remaining hops (payload
	// octets 3-104), before any MSTI records: the 30-octet CIST prefix (the
	// RST body [Encode] and [Decode] already share via putBody/readBody),
	// the version 1 and version 3 length fields, the 51-octet MST
	// configuration identifier, the CIST internal root path cost, the CIST
	// bridge identifier, and the CIST remaining hops.
	mstBodyLength = 102

	// mstiRecordLength is the octet length of one MSTI record.
	mstiRecordLength = 16

	// minMSTPayloadLength is the minimum LLC payload length, LLC header
	// included, that can hold an MST BPDU body with no MSTI records
	// (3 + mstBodyLength).
	minMSTPayloadLength = 105

	// maxMSTIRecords is the most MSTI records [Encode] can fit in an MST
	// BPDU: the version 3 length field carries 64 plus 16 octets per
	// record in a uint16, so the record count is capped at
	// (math.MaxUint16-64)/mstiRecordLength.
	maxMSTIRecords = (math.MaxUint16 - 64) / mstiRecordLength
)

// Encode serializes b into an untagged IEEE 802.3 LLC frame addressed to the
// standard bridge group address (01:80:c2:00:00:00). The Configuration, RST,
// and Topology Change Notification shapes are padded to the 802.3 minimum of
// 60 octets; the MST shape below is not.
//
// Encode supports all three IEEE 802.1D-2004 clause 9.3 shapes:
//   - [BPDUTypeRapid] (or zero value) writes an RST BPDU (clause 9.3.3) with version 2
//     (or b.Version when at least 2), wire type 0x02, and LLC length 39.
//   - [BPDUTypeConfiguration] writes a Configuration BPDU (clause 9.3.1) with version 0
//     (or b.Version when 0 or 1), wire type 0x00, LLC length 38, and flags masked to
//     Topology Change (bit 0) and Topology Change Acknowledgment (bit 7).
//   - [BPDUTypeTopologyChangeNotification] writes a Topology Change Notification BPDU
//     (clause 9.3.2) with version 0, wire type 0x80, LLC length 7, and no body fields.
//
// When b.ConfigID is non-nil, Encode instead writes an IEEE 802.1Q MST BPDU (protocol
// version 3), overriding b.Type and b.Version: the RST prefix above, followed by the
// MST body (configuration identifier, CIST internal root path cost, CIST bridge
// identifier, CIST remaining hops, and one 16-octet record per entry in b.MSTIs). An
// MST body is longer than the 802.3 minimum even with no MSTI records, so the payload
// is sized to the body rather than padded to minDataLength. b.MSTIs beyond
// maxMSTIRecords cannot fit the version 3 length field; Encode reports an error
// rather than write a length that would misread on decode.
func Encode(b BPDU, src netaddr.MAC) (ethernet.Frame, error) {
	if b.ConfigID != nil {
		return encodeMST(b, src)
	}

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
		}, nil

	case BPDUTypeTopologyChangeNotification:
		payload[5] = 0
		payload[6] = bpduTypeWireTCN

		return ethernet.Frame{
			Dst:       stpGroupAddress,
			Src:       src,
			EtherType: ethernet.EtherType(llcTCNBPDULength),
			Payload:   payload,
		}, nil

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
		}, nil
	}
}

// encodeMST writes b as an IEEE 802.1Q MST BPDU (protocol version 3). See
// [Encode] for the shape.
//
// The MST body positions the CIST bridge identifier and the CIST regional
// root identifier the other way round from the RST body putBody writes:
// putBody leaves b.BridgeID at [20:28], which the MST shape uses for the
// CIST regional root identifier, and putMSTBody's [96:104] for what the
// RST shape treats as the bridge identifier is where the MST shape carries
// the real CIST bridge identifier. encodeMST overwrites [20:28] with
// b.RegionalRootID after putBody runs, and putMSTBody writes b.BridgeID at
// [96:104], so the two fields land where the layout says they do.
func encodeMST(b BPDU, src netaddr.MAC) (ethernet.Frame, error) {
	if len(b.MSTIs) > maxMSTIRecords {
		return ethernet.Frame{}, errs.New().
			Attr("records", len(b.MSTIs)).
			Attr("max_records", maxMSTIRecords).
			Msgf("MST BPDU holds %d MSTI records, more than the %d the version 3 length field can carry", len(b.MSTIs), maxMSTIRecords)
	}

	contentLen := 3 + mstBodyLength + mstiRecordLength*len(b.MSTIs)

	payload := make([]byte, contentLen)
	payload[0] = 0x42
	payload[1] = 0x42
	payload[2] = 0x03
	binary.BigEndian.PutUint16(payload[3:5], 0x0000)
	payload[5] = mstProtocolVersion
	payload[6] = bpduTypeWireRST
	payload[7] = b.Flags
	putBody(payload, b)
	payload[38] = 0
	binary.BigEndian.PutUint16(payload[20:22], b.RegionalRootID.Priority)
	copy(payload[22:28], b.RegionalRootID.Address[:])
	putMSTBody(payload, b)

	return ethernet.Frame{
		Dst:       stpGroupAddress,
		Src:       src,
		EtherType: ethernet.EtherType(contentLen),
		Payload:   payload,
	}, nil
}

// putMSTBody writes the MST body that follows the RST prefix putBody
// writes: the version 3 length at [39:41], the 51-octet MST configuration
// identifier at [41:92], the CIST internal root path cost at [92:96], the
// CIST bridge identifier at [96:104] (encodeMST writes the CIST regional
// root identifier at [20:28] separately), the CIST remaining hops at
// [104], and one 16-octet record per entry in b.MSTIs starting at [105].
// payload must already be sized for len(b.MSTIs) records, and the caller
// has already checked len(b.MSTIs) against maxMSTIRecords.
func putMSTBody(payload []byte, b BPDU) {
	n := len(b.MSTIs)
	binary.BigEndian.PutUint16(payload[39:41], uint16(64+mstiRecordLength*n))

	payload[41] = b.ConfigID.Selector
	copy(payload[42:74], b.ConfigID.Name)
	binary.BigEndian.PutUint16(payload[74:76], b.ConfigID.Revision)
	copy(payload[76:92], b.ConfigID.Digest[:])

	binary.BigEndian.PutUint32(payload[92:96], b.InternalRootPathCost)
	binary.BigEndian.PutUint16(payload[96:98], b.BridgeID.Priority)
	copy(payload[98:104], b.BridgeID.Address[:])
	payload[104] = b.RemainingHops

	for i, rec := range b.MSTIs {
		off := minMSTPayloadLength + mstiRecordLength*i
		payload[off] = rec.Flags
		// The MSTID has no octets of its own: it rides in the low 12 bits of
		// the regional root priority (the system ID extension).
		priority := (rec.RegionalRootID.Priority & 0xF000) | (uint16(rec.MSTID) & 0x0FFF)
		binary.BigEndian.PutUint16(payload[off+1:off+3], priority)
		copy(payload[off+3:off+9], rec.RegionalRootID.Address[:])
		binary.BigEndian.PutUint32(payload[off+9:off+13], rec.InternalRootPathCost)
		payload[off+13] = rec.BridgePriority
		payload[off+14] = rec.PortPriority
		payload[off+15] = rec.RemainingHops
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
// A version 3 RST-shaped BPDU whose payload holds at least 105 octets is read as an
// IEEE 802.1Q MST BPDU: [BPDU.ConfigID], [BPDU.RegionalRootID],
// [BPDU.InternalRootPathCost], [BPDU.RemainingHops], and [BPDU.MSTIs] are filled from
// the MST body that follows the RST prefix. A version 3 payload too short to hold that
// body at all still decodes as its RST prefix, with ConfigID nil and no records,
// matching an RSTP peer; versions above 3 always decode this way. A payload long
// enough for the MST body but truncated inside the MSTI records is refused, not
// fallen back to the RST prefix.
//
// Decode rejects frames with truncated or over-long payloads, unexpected LLC headers,
// protocol identifiers other than 0, unsupported version and type combinations, zero
// hello time (on Configuration and RST shapes), or an MST version 3 length that
// disagrees with the payload or names a partial trailing record, with
// [ReasonUnsupportedBPDU].
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

		if version == mstProtocolVersion && len(f.Payload) >= minMSTPayloadLength {
			if err := readMSTBody(f.Payload, &b); err != nil {
				return BPDU{}, err
			}
		}

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

// readMSTBody reads the MST body [putMSTBody] writes and fills in b's MST
// fields, including taking b.RegionalRootID from the RST prefix's bridge
// identifier field and b.BridgeID from the MST body's [96:104] (see
// [encodeMST]). The caller has already checked that payload holds at least
// minMSTPayloadLength octets.
func readMSTBody(payload []byte, b *BPDU) error {
	v3Len := binary.BigEndian.Uint16(payload[39:41])
	if v3Len < 64 || (v3Len-64)%mstiRecordLength != 0 {
		return errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("version3_length", v3Len).
			Msgf("MST BPDU version 3 length %d is not 64 plus a multiple of %d", v3Len, mstiRecordLength)
	}

	n := int(v3Len-64) / mstiRecordLength
	want := minMSTPayloadLength + mstiRecordLength*n
	if len(payload) != want {
		return errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("have", len(payload)).
			Attr("want", want).
			Msgf("MST BPDU payload length %d disagrees with %d MSTI record(s)", len(payload), n)
	}

	var digest [16]byte
	copy(digest[:], payload[76:92])

	// putBody left the RST prefix's bridge identifier at [20:28]; the MST
	// shape uses that slot for the CIST regional root identifier instead.
	b.RegionalRootID = b.BridgeID

	var bridgeAddr netaddr.MAC
	copy(bridgeAddr[:], payload[98:104])

	b.ConfigID = &ConfigID{
		Selector: payload[41],
		Name:     string(bytes.TrimRight(payload[42:74], "\x00")),
		Revision: binary.BigEndian.Uint16(payload[74:76]),
		Digest:   digest,
	}
	b.InternalRootPathCost = binary.BigEndian.Uint32(payload[92:96])
	b.BridgeID = BridgeID{
		Priority: binary.BigEndian.Uint16(payload[96:98]),
		Address:  bridgeAddr,
	}
	b.RemainingHops = payload[104]

	if n == 0 {
		return nil
	}

	b.MSTIs = make([]MSTIRecord, n)
	for i := range b.MSTIs {
		off := minMSTPayloadLength + mstiRecordLength*i

		priority := binary.BigEndian.Uint16(payload[off+1 : off+3])
		var addr netaddr.MAC
		copy(addr[:], payload[off+3:off+9])

		b.MSTIs[i] = MSTIRecord{
			MSTID:                MSTID(priority & 0x0FFF),
			Flags:                payload[off],
			RegionalRootID:       BridgeID{Priority: priority, Address: addr},
			InternalRootPathCost: binary.BigEndian.Uint32(payload[off+9 : off+13]),
			BridgePriority:       payload[off+13],
			PortPriority:         payload[off+14],
			RemainingHops:        payload[off+15],
		}
	}

	return nil
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
