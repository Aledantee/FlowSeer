// GLBP (Gateway Load Balancing Protocol) layer. Rides UDP on port 3222,
// multicast to 224.0.0.102. The wire layout follows Wireshark's
// packet-glbp.c at commit 1dbb8baf9c5bb2e9501b15cce98cea6a3c0f41a3,
// lines 137-218 and 289-365.
//
// The decoder types Hello TLVs and keeps other TLV values raw.

package layers

import (
	"encoding/binary"

	"github.com/gopacket/gopacket"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// GLBPState is a GLBP virtual-gateway state.
type GLBPState uint8

// GLBP virtual-gateway states from Wireshark's packet-glbp.c.
const (
	GLBPStateListen  GLBPState = 4
	GLBPStateSpeak   GLBPState = 8
	GLBPStateStandby GLBPState = 0x10
	GLBPStateActive  GLBPState = 0x20
)

const (
	glbpHeaderLen      = 12
	glbpTLVHeaderLen   = 2
	glbpHelloType      = 1
	glbpHelloFieldsLen = 22
)

// GLBPHello is the typed value of a GLBP Hello TLV.
type GLBPHello struct {
	Unknown10      uint8
	State          GLBPState
	Unknown11      uint8
	Priority       uint8
	Unknown12      uint16
	HelloTime      uint32
	HoldTime       uint32
	RedirectTime   uint16
	Timeout        uint16
	Unknown13      uint16
	AddressType    uint8
	AddressLength  uint8
	VirtualAddress []byte
}

// GLBPTLV is one Type-Length-Value triple in a GLBP frame.
type GLBPTLV struct {
	Type   uint8
	Length uint8
	Hello  *GLBPHello
	Value  []byte
}

// GLBP is a Gateway Load Balancing Protocol message.
type GLBP struct {
	BaseLayer
	Version  uint8
	Unknown1 uint8
	Group    uint16
	Unknown2 uint16
	OwnerMAC []byte
	TLVs     []GLBPTLV
}

// LayerType returns LayerTypeGLBP.
func (g *GLBP) LayerType() gopacket.LayerType { return LayerTypeGLBP }

// CanDecode returns the set of layer types this DecodingLayer can decode.
func (g *GLBP) CanDecode() gopacket.LayerClass { return LayerTypeGLBP }

// NextLayerType returns gopacket.LayerTypeZero; GLBP has no sub-layers.
func (g *GLBP) NextLayerType() gopacket.LayerType { return gopacket.LayerTypeZero }

// DecodeFromBytes decodes the GLBP payload after the UDP header.
func (g *GLBP) DecodeFromBytes(data []byte, df gopacket.DecodeFeedback) error {
	if len(data) < glbpHeaderLen {
		df.SetTruncated()
		return errs.Msgf("GLBP: truncated at offset 0, need >=%d bytes, got %d", glbpHeaderLen, len(data))
	}

	g.BaseLayer = BaseLayer{Contents: data, Payload: nil}
	g.Version = data[0]
	g.Unknown1 = data[1]
	g.Group = binary.BigEndian.Uint16(data[2:4])
	g.Unknown2 = binary.BigEndian.Uint16(data[4:6])
	g.OwnerMAC = append(g.OwnerMAC[:0], data[6:12]...)
	g.TLVs = g.TLVs[:0]

	for offset := glbpHeaderLen; offset < len(data); {
		if len(data)-offset < glbpTLVHeaderLen {
			df.SetTruncated()
			return errs.Msgf("GLBP: truncated TLV header at offset %d, need 2 bytes, got %d", offset, len(data)-offset)
		}

		tlvType := data[offset]
		tlvLen := int(data[offset+1])
		if tlvLen < glbpTLVHeaderLen {
			return errs.Msgf("GLBP: TLV at offset %d has length %d < 2 (header size)", offset, tlvLen)
		}
		if offset+tlvLen > len(data) {
			df.SetTruncated()
			return errs.Msgf("GLBP: truncated TLV value at offset %d, type 0x%02x, need %d bytes, got %d",
				offset, tlvType, tlvLen-glbpTLVHeaderLen, len(data)-offset-glbpTLVHeaderLen)
		}

		value := data[offset+glbpTLVHeaderLen : offset+tlvLen]
		tlv := GLBPTLV{Type: tlvType, Length: uint8(tlvLen)}
		if tlvType == glbpHelloType {
			hello, err := decodeGLBPHello(value, offset+glbpTLVHeaderLen, df)
			if err != nil {
				return err
			}
			tlv.Hello = hello
		} else {
			tlv.Value = append([]byte(nil), value...)
		}
		g.TLVs = append(g.TLVs, tlv)
		offset += tlvLen
	}

	return nil
}

func decodeGLBPHello(value []byte, offset int, df gopacket.DecodeFeedback) (*GLBPHello, error) {
	if len(value) < glbpHelloFieldsLen {
		df.SetTruncated()
		return nil, errs.Msgf("GLBP: truncated Hello TLV at offset %d, need >=%d bytes, got %d",
			offset, glbpHelloFieldsLen, len(value))
	}

	addressLength := int(value[21])
	if len(value) < glbpHelloFieldsLen+addressLength {
		df.SetTruncated()
		return nil, errs.Msgf("GLBP: truncated Hello address at offset %d, need %d bytes, got %d",
			offset+glbpHelloFieldsLen, addressLength, len(value)-glbpHelloFieldsLen)
	}

	return &GLBPHello{
		Unknown10:      value[0],
		State:          GLBPState(value[1]),
		Unknown11:      value[2],
		Priority:       value[3],
		Unknown12:      binary.BigEndian.Uint16(value[4:6]),
		HelloTime:      binary.BigEndian.Uint32(value[6:10]),
		HoldTime:       binary.BigEndian.Uint32(value[10:14]),
		RedirectTime:   binary.BigEndian.Uint16(value[14:16]),
		Timeout:        binary.BigEndian.Uint16(value[16:18]),
		Unknown13:      binary.BigEndian.Uint16(value[18:20]),
		AddressType:    value[20],
		AddressLength:  value[21],
		VirtualAddress: append([]byte(nil), value[22:22+addressLength]...),
	}, nil
}

// SerializeTo writes the GLBP layer from its header and TLVs.
func (g *GLBP) SerializeTo(b gopacket.SerializeBuffer, _ gopacket.SerializeOptions) error {
	values := make([][]byte, len(g.TLVs))
	bodyLen := glbpHeaderLen
	for i, tlv := range g.TLVs {
		value, err := encodeGLBPTLVValue(tlv)
		if err != nil {
			return err
		}
		if len(value)+glbpTLVHeaderLen > 0xff {
			return errs.Msgf("GLBP: TLV type 0x%02x is %d bytes, maximum is 255", tlv.Type, len(value)+glbpTLVHeaderLen)
		}
		values[i] = value
		bodyLen += glbpTLVHeaderLen + len(value)
	}

	buf, err := b.PrependBytes(bodyLen)
	if err != nil {
		return err
	}

	buf[0] = g.Version
	buf[1] = g.Unknown1
	binary.BigEndian.PutUint16(buf[2:4], g.Group)
	binary.BigEndian.PutUint16(buf[4:6], g.Unknown2)
	ownerMAC := g.OwnerMAC
	if len(ownerMAC) < 6 {
		ownerMAC = append(append([]byte(nil), ownerMAC...), make([]byte, 6-len(ownerMAC))...)
	}
	copy(buf[6:12], ownerMAC[:6])

	offset := glbpHeaderLen
	for i, tlv := range g.TLVs {
		value := values[i]
		buf[offset] = tlv.Type
		buf[offset+1] = uint8(glbpTLVHeaderLen + len(value))
		copy(buf[offset+glbpTLVHeaderLen:], value)
		offset += glbpTLVHeaderLen + len(value)
	}

	return nil
}

func encodeGLBPTLVValue(tlv GLBPTLV) ([]byte, error) {
	if tlv.Type != glbpHelloType || tlv.Hello == nil {
		return tlv.Value, nil
	}

	hello := tlv.Hello
	if int(hello.AddressLength) != len(hello.VirtualAddress) {
		return nil, errs.Msgf("GLBP: Hello address length is %d, got %d address bytes",
			hello.AddressLength, len(hello.VirtualAddress))
	}
	value := make([]byte, glbpHelloFieldsLen+len(hello.VirtualAddress))
	value[0] = hello.Unknown10
	value[1] = byte(hello.State)
	value[2] = hello.Unknown11
	value[3] = hello.Priority
	binary.BigEndian.PutUint16(value[4:6], hello.Unknown12)
	binary.BigEndian.PutUint32(value[6:10], hello.HelloTime)
	binary.BigEndian.PutUint32(value[10:14], hello.HoldTime)
	binary.BigEndian.PutUint16(value[14:16], hello.RedirectTime)
	binary.BigEndian.PutUint16(value[16:18], hello.Timeout)
	binary.BigEndian.PutUint16(value[18:20], hello.Unknown13)
	value[20] = hello.AddressType
	value[21] = hello.AddressLength
	copy(value[22:], hello.VirtualAddress)
	return value, nil
}

func decodeGLBP(data []byte, p gopacket.PacketBuilder) error {
	g := &GLBP{}
	if err := g.DecodeFromBytes(data, p); err != nil {
		return err
	}
	p.AddLayer(g)
	return nil
}
