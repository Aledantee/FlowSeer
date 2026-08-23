// PAgP (Port Aggregation Protocol) layer. Cisco-proprietary, rides LLC/SNAP
// with OUI 0x00000C and PID 0x0104. The wire format is a fixed-layout PDU
// with local and partner device/port identification.
//
// The decoder surfaces the command, local device ID, and partner device ID as
// typed fields a behavior can gate on.

package layers

import (
	"encoding/binary"
	"fmt"

	"github.com/gopacket/gopacket"
)

// PAgPCommand is a PAgP command code.
type PAgPCommand uint8

// PAgP command codes per Cisco.
const (
	PAGPCmdHello    PAgPCommand = 0x01
	PAGPCmdRequest  PAgPCommand = 0x02
	PAGPCmdResponse PAgPCommand = 0x03
	PAGPCmdAdd      PAgPCommand = 0x04
	PAGPCmdRemove   PAgPCommand = 0x05
)

// PAgP is a Port Aggregation Protocol frame.
type PAgP struct {
	BaseLayer
	Version         uint8
	Command         PAgPCommand
	GroupCapability uint8
	GroupNumber     uint8
	LocalDeviceID   []byte // 6 bytes
	LocalPortID     uint32
	LocalIfIndex    uint32
	PartnerDeviceID []byte // 6 bytes
	PartnerPortID   uint32
	PartnerIfIndex  uint32
	PartnerGroupCap uint8
	LearnCount      uint8
	LearnTimes      []uint16
}

// LayerType returns LayerTypePAgP.
func (p *PAgP) LayerType() gopacket.LayerType { return LayerTypePAgP }

// CanDecode returns the set of layer types this DecodingLayer can decode.
func (p *PAgP) CanDecode() gopacket.LayerClass { return LayerTypePAgP }

// NextLayerType returns gopacket.LayerTypeZero; PAgP has no sub-layers.
func (p *PAgP) NextLayerType() gopacket.LayerType { return gopacket.LayerTypeZero }

// PAgP fixed-field offsets.
const (
	pagpMinLen = 38 // version(1)+cmd(1)+grpCap(1)+grpNum(1)+localDev(6)+localPort(4)+localIf(4)+localGrpCap(1)+rsvd(1)+partnerDev(6)+partnerPort(4)+partnerIf(4)+partnerGrpCap(1)+rsvd(1)+count(1)+rsvd(1)
)

// DecodeFromBytes decodes the PAgP payload (the bytes after the SNAP header).
func (p *PAgP) DecodeFromBytes(data []byte, df gopacket.DecodeFeedback) error {
	if len(data) < pagpMinLen {
		df.SetTruncated()
		return fmt.Errorf("PAgP: truncated at offset 0, need >=%d bytes, got %d", pagpMinLen, len(data))
	}

	p.BaseLayer = BaseLayer{Contents: data, Payload: nil}
	p.Version = data[0]
	p.Command = PAgPCommand(data[1])
	p.GroupCapability = data[2]
	p.GroupNumber = data[3]

	p.LocalDeviceID = append(p.LocalDeviceID[:0], data[4:10]...)
	p.LocalPortID = binary.BigEndian.Uint32(data[10:14])
	p.LocalIfIndex = binary.BigEndian.Uint32(data[14:18])
	// localGroupCapability(1) + reserved(1) at 18:20
	p.PartnerDeviceID = append(p.PartnerDeviceID[:0], data[20:26]...)
	p.PartnerPortID = binary.BigEndian.Uint32(data[26:30])
	p.PartnerIfIndex = binary.BigEndian.Uint32(data[30:34])
	p.PartnerGroupCap = data[34]
	// reserved at 35
	p.LearnCount = data[36]
	// reserved at 37

	p.LearnTimes = p.LearnTimes[:0]
	offset := 38
	for i := uint8(0); i < p.LearnCount; i++ {
		if offset+2 > len(data) {
			df.SetTruncated()
			return fmt.Errorf("PAgP: truncated learn time %d at offset %d, need 2 bytes, got %d", i, offset, len(data)-offset)
		}
		p.LearnTimes = append(p.LearnTimes, binary.BigEndian.Uint16(data[offset:offset+2]))
		offset += 2
	}

	return nil
}

// SerializeTo writes the PAgP layer from the typed fields.
func (p *PAgP) SerializeTo(b gopacket.SerializeBuffer, _ gopacket.SerializeOptions) error {
	bodyLen := pagpMinLen + 2*len(p.LearnTimes)
	buf, err := b.PrependBytes(bodyLen)
	if err != nil {
		return err
	}

	buf[0] = p.Version
	buf[1] = byte(p.Command)
	buf[2] = p.GroupCapability
	buf[3] = p.GroupNumber

	localDev := p.LocalDeviceID
	if len(localDev) < 6 {
		padded := make([]byte, 6)
		copy(padded, localDev)
		localDev = padded
	}
	copy(buf[4:10], localDev)
	binary.BigEndian.PutUint32(buf[10:14], p.LocalPortID)
	binary.BigEndian.PutUint32(buf[14:18], p.LocalIfIndex)
	buf[18] = p.GroupCapability
	buf[19] = 0

	partnerDev := p.PartnerDeviceID
	if len(partnerDev) < 6 {
		padded := make([]byte, 6)
		copy(padded, partnerDev)
		partnerDev = padded
	}
	copy(buf[20:26], partnerDev)
	binary.BigEndian.PutUint32(buf[26:30], p.PartnerPortID)
	binary.BigEndian.PutUint32(buf[30:34], p.PartnerIfIndex)
	buf[34] = p.PartnerGroupCap
	buf[35] = 0
	buf[36] = byte(len(p.LearnTimes))
	buf[37] = 0

	offset := 38
	for _, lt := range p.LearnTimes {
		binary.BigEndian.PutUint16(buf[offset:], lt)
		offset += 2
	}

	return nil
}

func decodePAgP(data []byte, p gopacket.PacketBuilder) error {
	pa := &PAgP{}
	if err := pa.DecodeFromBytes(data, p); err != nil {
		return err
	}
	p.AddLayer(pa)
	return nil
}
