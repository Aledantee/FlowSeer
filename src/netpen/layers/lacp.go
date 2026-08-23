// LACP (Link Aggregation Control Protocol, IEEE 802.1AX) layer. Rides directly
// on Ethernet with EtherType 0x8809 (slow protocols), subtype 0x01. The PDU
// is a fixed-layout sequence of typed TLVs: actor info, partner info,
// collector info, and a terminator.
//
// The decoder surfaces the actor and partner system/key/port/state and the
// collector max delay as typed fields a behavior can gate on.

package layers

import (
	"encoding/binary"
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
)

// LACPActorState is the actor state byte bitfield.
type LACPActorState uint8

// LACP actor state flags.
const (
	LACPStateActivity        LACPActorState = 0x01
	LACPStateTimeout         LACPActorState = 0x02
	LACPStateAggregation     LACPActorState = 0x04
	LACPStateSynchronization LACPActorState = 0x08
	LACPStateCollecting      LACPActorState = 0x10
	LACPStateDistributing    LACPActorState = 0x20
	LACPStateDefaulted       LACPActorState = 0x40
	LACPStateExpired         LACPActorState = 0x80
)

// LACPPortInfo is one actor or partner info block.
type LACPPortInfo struct {
	SystemPriority uint16
	System         net.HardwareAddr
	Key            uint16
	PortPriority   uint16
	Port           uint16
	State          LACPActorState
}

// LACP is a Link Aggregation Control Protocol frame.
type LACP struct {
	BaseLayer
	Subtype           uint8
	Version           uint8
	Actor             LACPPortInfo
	Partner           LACPPortInfo
	CollectorMaxDelay uint16
}

// LayerType returns LayerTypeLACP.
func (l *LACP) LayerType() gopacket.LayerType { return LayerTypeLACP }

// CanDecode returns the set of layer types this DecodingLayer can decode.
func (l *LACP) CanDecode() gopacket.LayerClass { return LayerTypeLACP }

// NextLayerType returns gopacket.LayerTypeZero; LACP has no sub-layers.
func (l *LACP) NextLayerType() gopacket.LayerType { return gopacket.LayerTypeZero }

// LACP fixed-field offsets within the PDU (after Ethernet header).
const (
	lacpMinLen       = 60 // subtype(1)+version(1)+actor(20)+partner(20)+collector(16)+terminator(2) = 60
	lacpActorOff     = 2
	lacpPartnerOff   = 22
	lacpCollectorOff = 42
	lacpTermOff      = 58
)

// DecodeFromBytes decodes the LACP payload (the bytes after the Ethernet
// header).
func (l *LACP) DecodeFromBytes(data []byte, df gopacket.DecodeFeedback) error {
	if len(data) < 4 {
		df.SetTruncated()
		return fmt.Errorf("LACP: truncated at offset 0, need >=4 bytes, got %d", len(data))
	}

	l.BaseLayer = BaseLayer{Contents: data, Payload: nil}
	l.Subtype = data[0]
	l.Version = data[1]

	if l.Subtype != lacpSubtype {
		return fmt.Errorf("LACP: unexpected subtype 0x%02x, want 0x%02x", l.Subtype, lacpSubtype)
	}

	if len(data) < lacpMinLen {
		df.SetTruncated()
		return fmt.Errorf("LACP: truncated PDU, need >=%d bytes, got %d", lacpMinLen, len(data))
	}

	l.Actor = decodeLACPPortInfo(data[lacpActorOff:])
	l.Partner = decodeLACPPortInfo(data[lacpPartnerOff:])

	if data[lacpCollectorOff] == 0x03 {
		l.CollectorMaxDelay = binary.BigEndian.Uint16(data[lacpCollectorOff+2 : lacpCollectorOff+4])
	}

	return nil
}

func decodeLACPPortInfo(data []byte) LACPPortInfo {
	// TLV: type(1) length(1) sys_pri(2) sys(6) key(2) port_pri(2) port(2) state(1) reserved(3)
	return LACPPortInfo{
		SystemPriority: binary.BigEndian.Uint16(data[2:4]),
		System:         net.HardwareAddr(append([]byte(nil), data[4:10]...)),
		Key:            binary.BigEndian.Uint16(data[10:12]),
		PortPriority:   binary.BigEndian.Uint16(data[12:14]),
		Port:           binary.BigEndian.Uint16(data[14:16]),
		State:          LACPActorState(data[16]),
	}
}

// SerializeTo writes the LACP layer from the typed fields. The PDU is padded
// to 110 bytes per IEEE 802.1AX.
func (l *LACP) SerializeTo(b gopacket.SerializeBuffer, _ gopacket.SerializeOptions) error {
	buf, err := b.PrependBytes(110)
	if err != nil {
		return err
	}

	buf[0] = l.Subtype
	buf[1] = l.Version

	encodeLACPPortInfo(buf[lacpActorOff:], 0x01, l.Actor)
	encodeLACPPortInfo(buf[lacpPartnerOff:], 0x02, l.Partner)

	// Collector TLV
	buf[lacpCollectorOff] = 0x03
	buf[lacpCollectorOff+1] = 16
	binary.BigEndian.PutUint16(buf[lacpCollectorOff+2:], l.CollectorMaxDelay)

	// Terminator
	buf[lacpTermOff] = 0x00
	buf[lacpTermOff+1] = 0x00

	// Remaining bytes are already zero from PrependBytes.

	return nil
}

func encodeLACPPortInfo(buf []byte, tlvType uint8, info LACPPortInfo) {
	buf[0] = tlvType
	buf[1] = 20
	binary.BigEndian.PutUint16(buf[2:4], info.SystemPriority)
	sys := info.System
	if len(sys) < 6 {
		padded := make([]byte, 6)
		copy(padded, sys)
		sys = padded
	}
	copy(buf[4:10], sys)
	binary.BigEndian.PutUint16(buf[10:12], info.Key)
	binary.BigEndian.PutUint16(buf[12:14], info.PortPriority)
	binary.BigEndian.PutUint16(buf[14:16], info.Port)
	buf[16] = byte(info.State)
	buf[17] = 0
	buf[18] = 0
	buf[19] = 0
}

func decodeLACP(data []byte, p gopacket.PacketBuilder) error {
	l := &LACP{}
	if err := l.DecodeFromBytes(data, p); err != nil {
		return err
	}
	p.AddLayer(l)
	return nil
}
