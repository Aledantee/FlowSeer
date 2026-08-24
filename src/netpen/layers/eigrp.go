// EIGRP (Enhanced Interior Gateway Routing Protocol, RFC 7868) layer. Rides
// directly on IP with protocol number 88, multicast to 224.0.0.10. The
// wire format is a 20-byte fixed header followed by variable-length TLVs
// (type 2 bytes, length 2 bytes, value).
//
// The decoder surfaces the opcode, flags, sequence/ack numbers, VRID, and
// autonomous system as typed fields a behavior can gate on. Missing TLV
// tails (header-only packets) decline gracefully: the header fields are
// decoded without error, and the TLV slice stays empty. Unknown TLV types
// pass through without failing the decode.

package layers

import (
	"encoding/binary"
	"fmt"

	"github.com/gopacket/gopacket"
)

// EIGRPOpcode is an EIGRP message opcode.
type EIGRPOpcode uint8

// EIGRP opcodes per RFC 7868.
const (
	EIGRPOpcodeUpdate   EIGRPOpcode = 1
	EIGRPOpcodeRequest  EIGRPOpcode = 2
	EIGRPOpcodeQuery    EIGRPOpcode = 3
	EIGRPOpcodeReply    EIGRPOpcode = 4
	EIGRPOpcodeHello    EIGRPOpcode = 5
	EIGRPOpcodeIPXSAP   EIGRPOpcode = 6
	EIGRPOpcodeSIAQuery EIGRPOpcode = 10
	EIGRPOpcodeSIAReply EIGRPOpcode = 11
)

// EIGRPTLV is one Type-Length-Value triple in an EIGRP frame.
type EIGRPTLV struct {
	Type   uint16
	Length uint16
	Value  []byte
}

// EIGRP is an Enhanced Interior Gateway Routing Protocol message.
type EIGRP struct {
	BaseLayer
	Version          uint8
	Opcode           EIGRPOpcode
	Checksum         uint16
	Flags            uint32
	SequenceNumber   uint32
	AckNumber        uint32
	VirtualRouterID  uint16
	AutonomousSystem uint16
	TLVs             []EIGRPTLV
}

// LayerType returns LayerTypeEIGRP.
func (e *EIGRP) LayerType() gopacket.LayerType { return LayerTypeEIGRP }

// CanDecode returns the set of layer types this DecodingLayer can decode.
func (e *EIGRP) CanDecode() gopacket.LayerClass { return LayerTypeEIGRP }

// NextLayerType returns gopacket.LayerTypeZero; EIGRP has no sub-layers.
func (e *EIGRP) NextLayerType() gopacket.LayerType { return gopacket.LayerTypeZero }

// EIGRP fixed-field offsets within the PDU (after IP header).
const (
	eigrpMinLen      = 20
	eigrpChecksumOff = 2
	eigrpFlagsOff    = 4
	eigrpSeqOff      = 8
	eigrpAckOff      = 12
	eigrpVRIDOff     = 16
	eigrpASOff       = 18
	eigrpTLVOff      = 20
)

// DecodeFromBytes decodes the EIGRP payload (the bytes after the IP
// header). A header-only packet (no TLV tail) decodes successfully: the
// header fields are populated and the TLV slice stays empty.
func (e *EIGRP) DecodeFromBytes(data []byte, df gopacket.DecodeFeedback) error {
	if len(data) < eigrpMinLen {
		df.SetTruncated()
		return fmt.Errorf("EIGRP: truncated at offset 0, need >=%d bytes, got %d", eigrpMinLen, len(data))
	}

	e.BaseLayer = BaseLayer{Contents: data, Payload: nil}
	e.Version = data[0]
	e.Opcode = EIGRPOpcode(data[1])
	e.Checksum = binary.BigEndian.Uint16(data[eigrpChecksumOff : eigrpChecksumOff+2])
	e.Flags = binary.BigEndian.Uint32(data[eigrpFlagsOff : eigrpFlagsOff+4])
	e.SequenceNumber = binary.BigEndian.Uint32(data[eigrpSeqOff : eigrpSeqOff+4])
	e.AckNumber = binary.BigEndian.Uint32(data[eigrpAckOff : eigrpAckOff+4])
	e.VirtualRouterID = binary.BigEndian.Uint16(data[eigrpVRIDOff : eigrpVRIDOff+2])
	e.AutonomousSystem = binary.BigEndian.Uint16(data[eigrpASOff : eigrpASOff+2])

	e.TLVs = e.TLVs[:0]
	offset := eigrpTLVOff
	for offset+4 <= len(data) {
		tlvType := binary.BigEndian.Uint16(data[offset : offset+2])
		tlvLen := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		if tlvLen < 4 {
			return fmt.Errorf("EIGRP: TLV at offset %d has length %d < 4 (header size)", offset, tlvLen)
		}
		if offset+tlvLen > len(data) {
			// Missing TLV tail: the header decoded fine. Stop parsing
			// TLVs without erroring — the header fields are valid.
			break
		}

		// Aliased to the packet buffer; lifetime is the same as
		// BaseLayer.Contents which also references data.
		value := data[offset+4 : offset+tlvLen]
		e.TLVs = append(e.TLVs, EIGRPTLV{
			Type:   tlvType,
			Length: uint16(tlvLen),
			Value:  value,
		})

		offset += tlvLen
	}

	return nil
}

// SerializeTo writes the EIGRP layer from the typed fields and TLVs. The
// checksum is not recomputed; the caller sets it if needed.
func (e *EIGRP) SerializeTo(b gopacket.SerializeBuffer, _ gopacket.SerializeOptions) error {
	bodyLen := eigrpMinLen
	for _, tlv := range e.TLVs {
		bodyLen += 4 + len(tlv.Value)
	}

	buf, err := b.PrependBytes(bodyLen)
	if err != nil {
		return err
	}

	buf[0] = e.Version
	buf[1] = byte(e.Opcode)
	binary.BigEndian.PutUint16(buf[eigrpChecksumOff:], e.Checksum)
	binary.BigEndian.PutUint32(buf[eigrpFlagsOff:], e.Flags)
	binary.BigEndian.PutUint32(buf[eigrpSeqOff:], e.SequenceNumber)
	binary.BigEndian.PutUint32(buf[eigrpAckOff:], e.AckNumber)
	binary.BigEndian.PutUint16(buf[eigrpVRIDOff:], e.VirtualRouterID)
	binary.BigEndian.PutUint16(buf[eigrpASOff:], e.AutonomousSystem)

	offset := eigrpTLVOff
	for _, tlv := range e.TLVs {
		binary.BigEndian.PutUint16(buf[offset:], tlv.Type)
		binary.BigEndian.PutUint16(buf[offset+2:], uint16(4+len(tlv.Value)))
		copy(buf[offset+4:], tlv.Value)
		offset += 4 + len(tlv.Value)
	}

	return nil
}

func decodeEIGRP(data []byte, p gopacket.PacketBuilder) error {
	e := &EIGRP{}
	if err := e.DecodeFromBytes(data, p); err != nil {
		return err
	}
	p.AddLayer(e)
	return nil
}
