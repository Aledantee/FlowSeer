package bpdu

import (
	"encoding/binary"
	"fmt"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

var sstpGroupAddress = netaddr.MAC{0x01, 0x00, 0x0c, 0xcc, 0xcc, 0xcd}

// GroupAddressSSTP returns the destination address Cisco PVST+ uses for SSTP
// BPDUs (01:00:0c:cc:cc:cd), sent once per VLAN alongside (not instead of) the
// shared IEEE bridge group address [Encode] and [Decode] use.
func GroupAddressSSTP() netaddr.MAC {
	return sstpGroupAddress
}

const (
	// sstpConfigurationPayloadLength is the fixed SSTP Configuration payload
	// length in octets: the LLC/SNAP header, the 36-octet RST-layout body, and
	// the 6-octet originating-VLAN TLV.
	sstpConfigurationPayloadLength = 50

	// sstpTCNLength is the length carried by the 802.3 field for an SSTP TCN:
	// the LLC/SNAP header plus its four-octet BPDU body.
	sstpTCNLength = 12

	// sstpSNAPPID is the SNAP protocol identifier Cisco assigns to SSTP.
	sstpSNAPPID = 0x010B
)

// EncodeSSTP serializes b into an SSTP (Cisco Per-VLAN Spanning Tree Plus)
// BPDU carried in LLC/SNAP framing addressed to [GroupAddressSSTP].
// Configuration and RST BPDUs use the 50-octet payload layout with vid appended
// as a trailing originating-VLAN TLV. A TCN uses the 12-octet LLC/SNAP and BPDU
// prefix and is padded to the 802.3 minimum. The Configuration and RST layout
// is:
//   - octets 0-2: the LLC header AA AA 03
//   - octets 3-5: the SNAP OUI 00-00-0C
//   - octets 6-7: the SNAP PID 0x010B
//   - octets 8-43: the 36-octet RST BPDU body (protocol identifier, version,
//     type, flags, root id, root path cost, bridge id, port id, and the
//     four times)
//   - octets 44-49: the TLV (type 0x0000, length 0x0002, vid)
//
// Configuration BPDUs use version 0, wire type 0x00, and only the topology
// change and acknowledgment flags. RST BPDUs use wire type 0x02 and a version
// of at least 2. TCN BPDUs use version 0 and wire type 0x80 without a TLV.
// EncodeSSTP refuses a b whose ConfigID is non-nil: an MST BPDU has no SSTP
// form.
func EncodeSSTP(b BPDU, vid vlan.ID, src netaddr.MAC) (ethernet.Frame, error) {
	if b.ConfigID != nil {
		return ethernet.Frame{}, errs.From(ErrUnsupported).
			Msg("SSTP has no MST form: b.ConfigID must be nil")
	}

	if b.Type == TypeTopologyChangeNotification {
		payload := make([]byte, minDataLength)
		payload[0], payload[1], payload[2] = 0xAA, 0xAA, 0x03
		payload[3], payload[4], payload[5] = 0x00, 0x00, 0x0C
		binary.BigEndian.PutUint16(payload[6:8], sstpSNAPPID)
		binary.BigEndian.PutUint16(payload[8:10], 0x0000)
		payload[10] = 0
		payload[11] = bpduTypeWireTCN

		return ethernet.Frame{
			Dst:       sstpGroupAddress,
			Src:       src,
			EtherType: ethernet.EtherType(sstpTCNLength),
			Payload:   payload,
		}, nil
	}

	payload := make([]byte, sstpConfigurationPayloadLength)
	payload[0], payload[1], payload[2] = 0xAA, 0xAA, 0x03
	payload[3], payload[4], payload[5] = 0x00, 0x00, 0x0C
	binary.BigEndian.PutUint16(payload[6:8], sstpSNAPPID)

	binary.BigEndian.PutUint16(payload[8:10], 0x0000)

	if b.Type == TypeConfiguration {
		payload[10] = 0
		payload[11] = bpduTypeWireConfig
		payload[12] = b.Flags & (flagTopologyChange | flagTopologyChangeAck)
	} else {
		version := b.Version
		if version < 2 {
			version = 2
		}
		payload[10] = version
		payload[11] = bpduTypeWireRST
		payload[12] = b.Flags
	}

	// putBody writes the RST body fields (root id through forward delay)
	// starting at relative offset 8 of the slice it is given. Slicing this
	// payload at 5 places putBody's relative offset 8 at absolute offset 13
	// (following the 8-octet LLC/SNAP header and the 5-octet protocol identifier,
	// version, type, and flags prefix), where the SSTP layout puts the root id.
	putBody(payload[5:], b)
	payload[43] = 0

	binary.BigEndian.PutUint16(payload[44:46], 0x0000)
	binary.BigEndian.PutUint16(payload[46:48], 0x0002)
	binary.BigEndian.PutUint16(payload[48:50], uint16(vid))

	return ethernet.Frame{
		Dst:       sstpGroupAddress,
		Src:       src,
		EtherType: ethernet.EtherType(sstpConfigurationPayloadLength),
		Payload:   payload,
	}, nil
}

// DecodeSSTP deserializes an SSTP BPDU from the payload of an Ethernet
// frame, the inverse of [EncodeSSTP]. It accepts Configuration and RST shapes
// with their 50-octet layout, and TCN shapes with at least 12 octets. It
// rejects, wrapping [ErrUnsupported], malformed LLC/SNAP headers, a protocol
// identifier other than 0, a version below 2 on the RST shape, unsupported
// wire types, and malformed Configuration or RST TLVs.
func DecodeSSTP(f ethernet.Frame) (BPDU, vlan.ID, error) {
	if len(f.Payload) < sstpTCNLength {
		return BPDU{}, 0, errs.From(ErrUnsupported).
			Attr("have", len(f.Payload)).
			Attr("min", sstpTCNLength).
			Msgf("SSTP BPDU payload length %d is too short", len(f.Payload))
	}

	if f.Payload[0] != 0xAA || f.Payload[1] != 0xAA || f.Payload[2] != 0x03 {
		return BPDU{}, 0, errs.From(ErrUnsupported).
			Attr("llc_header", fmt.Sprintf("% x", f.Payload[0:3])).
			Msgf("unsupported SSTP LLC header % x, want AA AA 03", f.Payload[0:3])
	}

	if f.Payload[3] != 0x00 || f.Payload[4] != 0x00 || f.Payload[5] != 0x0C {
		return BPDU{}, 0, errs.From(ErrUnsupported).
			Attr("oui", fmt.Sprintf("%02x-%02x-%02x", f.Payload[3], f.Payload[4], f.Payload[5])).
			Msgf("unsupported SSTP SNAP OUI % x, want 00-00-0C", f.Payload[3:6])
	}

	pid := binary.BigEndian.Uint16(f.Payload[6:8])
	if pid != sstpSNAPPID {
		return BPDU{}, 0, errs.From(ErrUnsupported).
			Attr("pid", pid).
			Msgf("unsupported SSTP SNAP PID 0x%04x, want 0x%04x", pid, uint16(sstpSNAPPID))
	}

	protoID := binary.BigEndian.Uint16(f.Payload[8:10])
	if protoID != 0 {
		return BPDU{}, 0, errs.From(ErrUnsupported).
			Attr("protocol_id", protoID).
			Msgf("unsupported SSTP protocol identifier 0x%04x, want 0x0000", protoID)
	}

	version := f.Payload[10]
	wireType := f.Payload[11]
	if wireType == bpduTypeWireTCN {
		return BPDU{
			Version: version,
			Type:    TypeTopologyChangeNotification,
		}, 0, nil
	}

	if wireType != bpduTypeWireConfig && wireType != bpduTypeWireRST {
		return BPDU{}, 0, errs.From(ErrUnsupported).
			Attr("type", wireType).
			Msgf("unsupported SSTP BPDU type %d", wireType)
	}

	if len(f.Payload) < sstpConfigurationPayloadLength {
		return BPDU{}, 0, errs.From(ErrUnsupported).
			Attr("have", len(f.Payload)).
			Attr("min", sstpConfigurationPayloadLength).
			Msgf("SSTP BPDU payload length %d is too short", len(f.Payload))
	}

	if wireType == bpduTypeWireRST && version < 2 {
		return BPDU{}, 0, errs.From(ErrUnsupported).
			Attr("version", version).
			Msgf("unsupported SSTP BPDU version %d, want at least 2", version)
	}

	tlvType := binary.BigEndian.Uint16(f.Payload[44:46])
	if tlvType != 0 {
		return BPDU{}, 0, errs.From(ErrUnsupported).
			Attr("tlv_type", tlvType).
			Msgf("unsupported SSTP TLV type 0x%04x, want 0x0000", tlvType)
	}

	tlvLength := binary.BigEndian.Uint16(f.Payload[46:48])
	if tlvLength != 2 {
		return BPDU{}, 0, errs.From(ErrUnsupported).
			Attr("tlv_length", tlvLength).
			Msgf("unsupported SSTP TLV length %d, want 2", tlvLength)
	}

	b := readBody(f.Payload[5:])
	b.Version = version
	b.Type = TypeRapid
	b.Flags = f.Payload[12]
	if wireType == bpduTypeWireConfig {
		b.Type = TypeConfiguration
		b.Flags &= flagTopologyChange | flagTopologyChangeAck
	}

	vid := vlan.ID(binary.BigEndian.Uint16(f.Payload[48:50]))

	return b, vid, nil
}
