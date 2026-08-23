// DTP (Dynamic Trunking Protocol) layer. Cisco-proprietary, rides LLC/SNAP
// with OUI 0x00000C and PID 0x2004. The wire format is a 1-byte version
// followed by Type-Length-Value triples: type(2) length(2, includes header)
// value(length-4).
//
// The decoder surfaces typed TLV fields a behavior can gate on — trunk
// status (0x81 trunk-on, 0x03 desirable, 0x04 auto, 0x02 access), trunk type
// (0xA5 negotiated 802.1Q), and the neighbor MAC. Unknown TLV types pass
// through to the general path without failing the decode.

package layers

import (
	"encoding/binary"
	"fmt"

	"github.com/gopacket/gopacket"
)

// DTPTLVType identifies a DTP TLV.
type DTPTLVType uint16

// DTP TLV types per Wireshark packet-dtp.c.
const (
	DTPTLVTypeDomain      DTPTLVType = 0x0001
	DTPTLVTypeTrunkStatus DTPTLVType = 0x0002
	DTPTLVTypeTrunkType   DTPTLVType = 0x0003
	DTPTLVTypeNeighbor    DTPTLVType = 0x0004
)

// DTP trunk-status values.
const (
	DTPStatusAccess    uint8 = 0x02
	DTPStatusDesirable uint8 = 0x03
	DTPStatusAuto      uint8 = 0x04
	DTPStatusTrunk     uint8 = 0x81
)

// DTP trunk-type values.
const (
	DTPTypeNegotiated8021Q uint8 = 0xA5
	DTPTypeISL             uint8 = 0x0A
)

// DTPTLV is one Type-Length-Value triple in a DTP frame.
type DTPTLV struct {
	Type  DTPTLVType
	Value []byte
}

// DTP is a Dynamic Trunking Protocol frame.
type DTP struct {
	BaseLayer
	Version uint8
	TLVs    []DTPTLV

	// Typed fields extracted from TLVs for behavior gating.
	Domain      string
	TrunkStatus uint8
	TrunkType   uint8
	Neighbor    []byte
}

// LayerType returns LayerTypeDTP.
func (d *DTP) LayerType() gopacket.LayerType { return LayerTypeDTP }

// CanDecode returns the set of layer types this DecodingLayer can decode.
func (d *DTP) CanDecode() gopacket.LayerClass { return LayerTypeDTP }

// NextLayerType returns gopacket.LayerTypeZero; DTP has no sub-layers.
func (d *DTP) NextLayerType() gopacket.LayerType { return gopacket.LayerTypeZero }

// NeighborState reports the DTP negotiation state as a human-readable string
// derived from the trunk-status TLV.
func (d *DTP) NeighborState() string {
	switch d.TrunkStatus {
	case DTPStatusAccess:
		return "access"
	case DTPStatusDesirable:
		return "desirable"
	case DTPStatusAuto:
		return "auto"
	case DTPStatusTrunk:
		return "trunk"
	default:
		return fmt.Sprintf("unknown(0x%02x)", d.TrunkStatus)
	}
}

// DecodeFromBytes decodes the DTP payload (the bytes after the SNAP header).
func (d *DTP) DecodeFromBytes(data []byte, df gopacket.DecodeFeedback) error {
	if len(data) < 1 {
		df.SetTruncated()
		return fmt.Errorf("DTP: truncated at offset 0, need >=1 bytes, got %d", len(data))
	}

	d.BaseLayer = BaseLayer{Contents: data, Payload: nil}
	d.Version = data[0]
	d.TLVs = d.TLVs[:0]
	d.Domain = ""
	d.TrunkStatus = 0
	d.TrunkType = 0
	d.Neighbor = nil

	offset := 1
	for offset < len(data) {
		if offset+4 > len(data) {
			df.SetTruncated()
			return fmt.Errorf("DTP: truncated TLV header at offset %d, need 4 bytes, got %d", offset, len(data)-offset)
		}

		tlvType := DTPTLVType(binary.BigEndian.Uint16(data[offset : offset+2]))
		tlvLen := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		if tlvLen < 4 {
			return fmt.Errorf("DTP: TLV at offset %d has length %d < 4 (header size)", offset, tlvLen)
		}
		if offset+tlvLen > len(data) {
			df.SetTruncated()
			return fmt.Errorf("DTP: truncated TLV value at offset %d, type 0x%04x, need %d bytes, got %d", offset, tlvType, tlvLen-4, len(data)-offset-4)
		}

		value := data[offset+4 : offset+tlvLen]
		d.TLVs = append(d.TLVs, DTPTLV{Type: tlvType, Value: value})

		switch tlvType {
		case DTPTLVTypeDomain:
			d.Domain = string(value)
		case DTPTLVTypeTrunkStatus:
			if len(value) >= 1 {
				d.TrunkStatus = value[0]
			}
		case DTPTLVTypeTrunkType:
			if len(value) >= 1 {
				d.TrunkType = value[0]
			}
		case DTPTLVTypeNeighbor:
			d.Neighbor = append(d.Neighbor[:0], value...)
		}

		offset += tlvLen
	}

	return nil
}

// SerializeTo writes the DTP layer. The TLVs are serialized in the order they
// appear in the TLVs slice; typed fields (Domain, TrunkStatus, TrunkType,
// Neighbor) are not re-serialized from their extracted values — the caller
// populates TLVs directly for craft.
func (d *DTP) SerializeTo(b gopacket.SerializeBuffer, _ gopacket.SerializeOptions) error {
	bodyLen := 1
	for _, tlv := range d.TLVs {
		bodyLen += 4 + len(tlv.Value)
	}

	buf, err := b.PrependBytes(bodyLen)
	if err != nil {
		return err
	}

	buf[0] = d.Version
	offset := 1
	for _, tlv := range d.TLVs {
		binary.BigEndian.PutUint16(buf[offset:], uint16(tlv.Type))
		binary.BigEndian.PutUint16(buf[offset+2:], uint16(4+len(tlv.Value)))
		copy(buf[offset+4:], tlv.Value)
		offset += 4 + len(tlv.Value)
	}

	return nil
}

func decodeDTP(data []byte, p gopacket.PacketBuilder) error {
	d := &DTP{}
	if err := d.DecodeFromBytes(data, p); err != nil {
		return err
	}
	p.AddLayer(d)
	return nil
}
