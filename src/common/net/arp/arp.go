// Package arp encodes and decodes Address Resolution Protocol messages
// (RFC 826) carried directly in an Ethernet frame.
package arp

import (
	"encoding/binary"
	"errors"
	"net/netip"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

// wireLength is the fixed size of an ARP message with an Ethernet hardware
// address and an IPv4 protocol address (RFC 826).
const wireLength = 28

// Operation identifies the ARP message kind.
type Operation uint16

const (
	// Request asks for the hardware address owning a protocol address.
	Request Operation = 1
	// Reply answers a Request with the owner's hardware address.
	Reply Operation = 2
)

// Message is an ARP message resolving an Ethernet hardware address against
// an IPv4 protocol address.
type Message struct {
	HardwareType uint16
	ProtocolType uint16
	Operation    Operation
	SenderMAC    netaddr.MAC
	SenderAddr   netip.Addr
	TargetMAC    netaddr.MAC
	TargetAddr   netip.Addr
}

var (
	// ErrMalformed identifies invalid fields and lengths.
	ErrMalformed = errors.New("malformed ARP message")
	// ErrUnsupported identifies well-formed wire formats the package does not support.
	ErrUnsupported = errors.New("unsupported ARP message")
)

// Encode serializes m into an Ethernet II frame carrying a 28-octet ARP
// message with EtherType 0x0806. dst becomes the frame's destination address
// verbatim; it is independent of m.TargetMAC, which is written only into the
// ARP payload's target hardware address field. The two coincide for a reply
// but not for a request, whose payload target hardware address is the zero
// MAC (unknown) while the frame itself goes to the broadcast address.
// Encode returns [ErrMalformed] wrapped with attributes when either address
// is not IPv4.
func Encode(m Message, dst netaddr.MAC) (ethernet.Frame, error) {
	if !m.SenderAddr.Is4() {
		return ethernet.Frame{}, errs.From(ErrMalformed).
			Attr("field", "sender_addr").
			Attr("addr", m.SenderAddr).
			Msg("ARP sender address is not IPv4")
	}
	if !m.TargetAddr.Is4() {
		return ethernet.Frame{}, errs.From(ErrMalformed).
			Attr("field", "target_addr").
			Attr("addr", m.TargetAddr).
			Msg("ARP target address is not IPv4")
	}

	payload := make([]byte, wireLength)
	binary.BigEndian.PutUint16(payload[0:2], m.HardwareType)
	binary.BigEndian.PutUint16(payload[2:4], m.ProtocolType)
	payload[4] = 6
	payload[5] = 4
	binary.BigEndian.PutUint16(payload[6:8], uint16(m.Operation))
	copy(payload[8:14], m.SenderMAC[:])
	senderAddr := m.SenderAddr.As4()
	copy(payload[14:18], senderAddr[:])
	copy(payload[18:24], m.TargetMAC[:])
	targetAddr := m.TargetAddr.As4()
	copy(payload[24:28], targetAddr[:])

	return ethernet.Frame{
		Dst:       dst,
		Src:       m.SenderMAC,
		EtherType: ethernet.EtherTypeARP,
		Payload:   payload,
	}, nil
}

// Decode parses an ARP message from an Ethernet frame. It refuses an
// EtherType other than [ethernet.EtherTypeARP], a payload shorter than 28
// octets, a hardware type other than 1, a hardware length other than 6, and
// a protocol length other than 4, wrapping [ErrMalformed] or [ErrUnsupported]
// with attributes. A protocol type other than 0x0800 is refused as
// [ErrUnsupported] because it names a protocol address family this package
// does not decode: the only family it constructs is IPv4, so there is no
// wire value for "protocol type is 0x0800 but the address is not IPv4".
func Decode(f ethernet.Frame) (Message, error) {
	if f.EtherType != ethernet.EtherTypeARP {
		return Message{}, errs.From(ErrUnsupported).
			Attr("ethertype", f.EtherType).
			Msg("unexpected EtherType for ARP message")
	}

	if len(f.Payload) < wireLength {
		return Message{}, errs.From(ErrMalformed).
			Attr("length", len(f.Payload)).
			Attr("min", wireLength).
			Msg("ARP payload is shorter than 28 octets")
	}

	hardwareType := binary.BigEndian.Uint16(f.Payload[0:2])
	if hardwareType != 1 {
		return Message{}, errs.From(ErrUnsupported).
			Attr("hardware_type", hardwareType).
			Msg("unsupported ARP hardware type")
	}

	protocolType := binary.BigEndian.Uint16(f.Payload[2:4])
	if protocolType != 0x0800 {
		return Message{}, errs.From(ErrUnsupported).
			Attr("protocol_type", protocolType).
			Msg("unsupported ARP protocol type")
	}

	hardwareLen := f.Payload[4]
	if hardwareLen != 6 {
		return Message{}, errs.From(ErrMalformed).
			Attr("hardware_length", hardwareLen).
			Msg("ARP hardware length is not 6 octets")
	}

	protocolLen := f.Payload[5]
	if protocolLen != 4 {
		return Message{}, errs.From(ErrMalformed).
			Attr("protocol_length", protocolLen).
			Msg("ARP protocol length is not 4 octets")
	}

	senderAddr := netip.AddrFrom4([4]byte(f.Payload[14:18]))
	targetAddr := netip.AddrFrom4([4]byte(f.Payload[24:28]))

	var senderMAC, targetMAC netaddr.MAC
	copy(senderMAC[:], f.Payload[8:14])
	copy(targetMAC[:], f.Payload[18:24])

	return Message{
		HardwareType: hardwareType,
		ProtocolType: protocolType,
		Operation:    Operation(binary.BigEndian.Uint16(f.Payload[6:8])),
		SenderMAC:    senderMAC,
		SenderAddr:   senderAddr,
		TargetMAC:    targetMAC,
		TargetAddr:   targetAddr,
	}, nil
}
