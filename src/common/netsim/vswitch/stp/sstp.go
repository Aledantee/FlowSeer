package stp

import (
	"encoding/binary"
	"fmt"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

// GroupAddressSSTP is the destination address Cisco PVST+ uses for SSTP BPDUs
// (01:00:0c:cc:cc:cd), sent once per VLAN alongside (not instead of) the
// shared IEEE bridge group address [Encode] and [Decode] use.
var GroupAddressSSTP = netaddr.MAC{0x01, 0x00, 0x0c, 0xcc, 0xcc, 0xcd}

const (
	// sstpPayloadLength is the fixed SSTP payload length in octets: the
	// 3-octet LLC header, the 5-octet SNAP header (OUI and PID), the
	// 36-octet RST BPDU body, and the 6-octet originating-VLAN TLV.
	sstpPayloadLength = 50

	// sstpSNAPPID is the SNAP protocol identifier Cisco assigns to SSTP.
	sstpSNAPPID = 0x010B
)

// EncodeSSTP serializes b into an SSTP (Cisco Per-VLAN Spanning Tree Plus)
// BPDU: an IEEE 802.1D-2004 clause 9.3.3 RST BPDU carried in LLC/SNAP framing
// addressed to [GroupAddressSSTP], with vid appended as a trailing
// originating-VLAN TLV. The 50-octet payload is laid out as:
//   - octets 0-2: the LLC header AA AA 03
//   - octets 3-5: the SNAP OUI 00-00-0C
//   - octets 6-7: the SNAP PID 0x010B
//   - octets 8-43: the 36-octet RST BPDU body (protocol identifier, version,
//     type, flags, root id, root path cost, bridge id, port id, and the
//     four times)
//   - octets 44-49: the TLV (type 0x0000, length 0x0002, vid)
//
// EncodeSSTP forces wire type 0x02 and a version of at least 2, matching the
// RST shape [Encode] writes for the plain IEEE frame, and refuses a b whose
// ConfigID is non-nil: an MST BPDU has no SSTP form. The frame is not padded
// to the 802.3 minimum; 50 octets of payload plus the 14-octet Ethernet
// header already reaches 64.
func EncodeSSTP(b BPDU, vid vlan.ID, src netaddr.MAC) (ethernet.Frame, error) {
	if b.ConfigID != nil {
		return ethernet.Frame{}, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Msg("SSTP has no MST form: b.ConfigID must be nil")
	}

	payload := make([]byte, sstpPayloadLength)
	payload[0], payload[1], payload[2] = 0xAA, 0xAA, 0x03
	payload[3], payload[4], payload[5] = 0x00, 0x00, 0x0C
	binary.BigEndian.PutUint16(payload[6:8], sstpSNAPPID)

	binary.BigEndian.PutUint16(payload[8:10], 0x0000)
	version := b.Version
	if version < 2 {
		version = 2
	}
	payload[10] = version
	payload[11] = bpduTypeWireRST
	payload[12] = b.Flags

	// putBody writes the RST body fields (root id through forward delay)
	// starting at relative offset 8 of the slice it is given. In the plain
	// LLC payload [Encode] passes, that offset lands right after the LLC
	// header, protocol identifier, version, type, and flags octets it wrote
	// itself. Slicing this payload at 5 (past the LLC header and the
	// 5-octet SNAP header) puts protocol identifier through flags at the
	// same relative offset 3-7, so putBody's relative offset 8 lands on
	// absolute offset 13, exactly where the SSTP layout puts the root id.
	putBody(payload[5:], b)
	payload[43] = 0 // the version 1 length octet putBody leaves unwritten

	binary.BigEndian.PutUint16(payload[44:46], 0x0000)
	binary.BigEndian.PutUint16(payload[46:48], 0x0002)
	binary.BigEndian.PutUint16(payload[48:50], uint16(vid))

	return ethernet.Frame{
		Dst:       GroupAddressSSTP,
		Src:       src,
		EtherType: ethernet.EtherType(sstpPayloadLength),
		Payload:   payload,
	}, nil
}

// DecodeSSTP deserializes an SSTP BPDU from the payload of an Ethernet
// frame, the inverse of [EncodeSSTP]. It rejects, each with
// [ReasonUnsupportedBPDU]: a payload shorter than 50 octets; an LLC header
// other than AA AA 03; an SNAP OUI other than 00-00-0C; an SNAP PID other
// than 0x010B; a protocol identifier other than 0; a version below 2; a
// wire type other than 0x02; a TLV type other than 0; and a TLV length
// other than 2.
func DecodeSSTP(f ethernet.Frame) (BPDU, vlan.ID, error) {
	if len(f.Payload) < sstpPayloadLength {
		return BPDU{}, 0, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("have", len(f.Payload)).
			Attr("min", sstpPayloadLength).
			Msgf("SSTP BPDU payload length %d is too short", len(f.Payload))
	}

	if f.Payload[0] != 0xAA || f.Payload[1] != 0xAA || f.Payload[2] != 0x03 {
		return BPDU{}, 0, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("llc_header", fmt.Sprintf("% x", f.Payload[0:3])).
			Msgf("unsupported SSTP LLC header % x, want AA AA 03", f.Payload[0:3])
	}

	if f.Payload[3] != 0x00 || f.Payload[4] != 0x00 || f.Payload[5] != 0x0C {
		return BPDU{}, 0, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("oui", fmt.Sprintf("%02x-%02x-%02x", f.Payload[3], f.Payload[4], f.Payload[5])).
			Msgf("unsupported SSTP SNAP OUI % x, want 00-00-0C", f.Payload[3:6])
	}

	pid := binary.BigEndian.Uint16(f.Payload[6:8])
	if pid != sstpSNAPPID {
		return BPDU{}, 0, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("pid", pid).
			Msgf("unsupported SSTP SNAP PID 0x%04x, want 0x%04x", pid, uint16(sstpSNAPPID))
	}

	protoID := binary.BigEndian.Uint16(f.Payload[8:10])
	if protoID != 0 {
		return BPDU{}, 0, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("protocol_id", protoID).
			Msgf("unsupported SSTP protocol identifier 0x%04x, want 0x0000", protoID)
	}

	version := f.Payload[10]
	if version < 2 {
		return BPDU{}, 0, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("version", version).
			Msgf("unsupported SSTP BPDU version %d, want at least 2", version)
	}

	wireType := f.Payload[11]
	if wireType != bpduTypeWireRST {
		return BPDU{}, 0, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("type", wireType).
			Msgf("unsupported SSTP BPDU type %d, want 2", wireType)
	}

	tlvType := binary.BigEndian.Uint16(f.Payload[44:46])
	if tlvType != 0 {
		return BPDU{}, 0, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("tlv_type", tlvType).
			Msgf("unsupported SSTP TLV type 0x%04x, want 0x0000", tlvType)
	}

	tlvLength := binary.BigEndian.Uint16(f.Payload[46:48])
	if tlvLength != 2 {
		return BPDU{}, 0, errs.New().
			Attr("reason", ReasonUnsupportedBPDU).
			Attr("tlv_length", tlvLength).
			Msgf("unsupported SSTP TLV length %d, want 2", tlvLength)
	}

	b, err := readBody(f.Payload[5:])
	if err != nil {
		return BPDU{}, 0, err
	}
	b.Version = version
	b.Type = BPDUTypeRapid
	b.Flags = f.Payload[12]

	vid := vlan.ID(binary.BigEndian.Uint16(f.Payload[48:50]))

	return b, vid, nil
}
