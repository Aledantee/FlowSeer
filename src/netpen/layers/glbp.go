// GLBP (Gateway Load Balancing Protocol, RFC 7868) layer. Rides UDP on port
// 3222, multicast to 224.0.0.102. The wire format is a 23-byte fixed header
// followed by variable-length TLVs (type 2 bytes, length 2 bytes, value).
//
// The decoder surfaces the opcode, group, hello/hold times, priority,
// state, and virtual MAC as typed fields a behavior can gate on. Unknown
// TLV types pass through to the general path without failing the decode.

package layers

import (
	"encoding/binary"
	"fmt"

	"github.com/gopacket/gopacket"
)

// GLBPOpcode is a GLBP message opcode.
type GLBPOpcode uint8

// GLBP opcodes per RFC 7868.
const (
	GLBPOpcodeHello      GLBPOpcode = 1
	GLBPOpcodeRequest    GLBPOpcode = 2
	GLBPOpcodeRedirect   GLBPOpcode = 3
	GLBPOpcodeAssignment GLBPOpcode = 4
)

// GLBPState is a GLBP router state.
type GLBPState uint8

// GLBP states per RFC 7868.
const (
	GLBPStateInit    GLBPState = 0
	GLBPStateListen  GLBPState = 1
	GLBPStateSpeak   GLBPState = 2
	GLBPStateStandby GLBPState = 3
	GLBPStateActive  GLBPState = 4
)

// GLBPTLV is one Type-Length-Value triple in a GLBP frame.
type GLBPTLV struct {
	Type   uint16
	Length uint16
	Value  []byte
}

// GLBP is a Gateway Load Balancing Protocol message.
type GLBP struct {
	BaseLayer
	Version       uint8
	Reserved      uint8
	Opcode        GLBPOpcode
	Group         uint16
	HelloTime     uint16
	HoldTime      uint16
	VirtualMAC    []byte
	Priority      uint8
	State         GLBPState
	AddressFamily uint8
	TLVs          []GLBPTLV
}

// LayerType returns LayerTypeGLBP.
func (g *GLBP) LayerType() gopacket.LayerType { return LayerTypeGLBP }

// CanDecode returns the set of layer types this DecodingLayer can decode.
func (g *GLBP) CanDecode() gopacket.LayerClass { return LayerTypeGLBP }

// NextLayerType returns gopacket.LayerTypeZero; GLBP has no sub-layers.
func (g *GLBP) NextLayerType() gopacket.LayerType { return gopacket.LayerTypeZero }

// GLBP fixed-field offsets within the PDU (after UDP header).
const (
	glbpMinLen   = 23
	glbpHelloOff = 5
	glbpVMACOff  = 9
	glbpPrioOff  = 15
	glbpTLVOff   = 23
)

// DecodeFromBytes decodes the GLBP payload (the bytes after the UDP header).
func (g *GLBP) DecodeFromBytes(data []byte, df gopacket.DecodeFeedback) error {
	if len(data) < glbpMinLen {
		df.SetTruncated()
		return fmt.Errorf("GLBP: truncated at offset 0, need >=%d bytes, got %d", glbpMinLen, len(data))
	}

	g.BaseLayer = BaseLayer{Contents: data, Payload: nil}
	g.Version = data[0]
	g.Reserved = data[1]
	g.Opcode = GLBPOpcode(data[2])
	g.Group = binary.BigEndian.Uint16(data[3:5])
	g.HelloTime = binary.BigEndian.Uint16(data[glbpHelloOff : glbpHelloOff+2])
	g.HoldTime = binary.BigEndian.Uint16(data[glbpHelloOff+2 : glbpHelloOff+4])
	g.VirtualMAC = append(g.VirtualMAC[:0], data[glbpVMACOff:glbpVMACOff+6]...)
	g.Priority = data[glbpPrioOff]
	g.State = GLBPState(data[glbpPrioOff+1])
	g.AddressFamily = data[glbpPrioOff+2]

	g.TLVs = g.TLVs[:0]
	offset := glbpTLVOff
	for offset+4 <= len(data) {
		tlvType := binary.BigEndian.Uint16(data[offset : offset+2])
		tlvLen := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		if tlvLen < 4 {
			return fmt.Errorf("GLBP: TLV at offset %d has length %d < 4 (header size)", offset, tlvLen)
		}
		if offset+tlvLen > len(data) {
			df.SetTruncated()
			return fmt.Errorf("GLBP: truncated TLV value at offset %d, type 0x%04x, need %d bytes, got %d",
				offset, tlvType, tlvLen-4, len(data)-offset-4)
		}

		value := data[offset+4 : offset+tlvLen]
		g.TLVs = append(g.TLVs, GLBPTLV{
			Type:   tlvType,
			Length: uint16(tlvLen),
			Value:  append([]byte(nil), value...),
		})

		offset += tlvLen
	}

	return nil
}

// SerializeTo writes the GLBP layer from the typed fields and TLVs.
func (g *GLBP) SerializeTo(b gopacket.SerializeBuffer, _ gopacket.SerializeOptions) error {
	bodyLen := glbpTLVOff
	for _, tlv := range g.TLVs {
		bodyLen += 4 + len(tlv.Value)
	}

	buf, err := b.PrependBytes(bodyLen)
	if err != nil {
		return err
	}

	buf[0] = g.Version
	buf[1] = g.Reserved
	buf[2] = byte(g.Opcode)
	binary.BigEndian.PutUint16(buf[3:5], g.Group)
	binary.BigEndian.PutUint16(buf[glbpHelloOff:], g.HelloTime)
	binary.BigEndian.PutUint16(buf[glbpHelloOff+2:], g.HoldTime)

	vmac := g.VirtualMAC
	if len(vmac) < 6 {
		padded := make([]byte, 6)
		copy(padded, vmac)
		vmac = padded
	}
	copy(buf[glbpVMACOff:], vmac[:6])

	buf[glbpPrioOff] = g.Priority
	buf[glbpPrioOff+1] = byte(g.State)
	buf[glbpPrioOff+2] = g.AddressFamily
	buf[glbpPrioOff+3] = 0                             // unknown
	binary.BigEndian.PutUint16(buf[glbpPrioOff+4:], 0) // auth data
	binary.BigEndian.PutUint16(buf[glbpPrioOff+6:], 0) // reserved

	offset := glbpTLVOff
	for _, tlv := range g.TLVs {
		binary.BigEndian.PutUint16(buf[offset:], tlv.Type)
		binary.BigEndian.PutUint16(buf[offset+2:], uint16(4+len(tlv.Value)))
		copy(buf[offset+4:], tlv.Value)
		offset += 4 + len(tlv.Value)
	}

	return nil
}

func decodeGLBP(data []byte, p gopacket.PacketBuilder) error {
	g := &GLBP{}
	if err := g.DecodeFromBytes(data, p); err != nil {
		return err
	}
	p.AddLayer(g)
	return nil
}
