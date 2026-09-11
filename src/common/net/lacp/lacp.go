// Package lacp provides an IEEE 802.1AX Link Aggregation Control Protocol Data
// Unit (LACPDU) codec. Its wire layout matches the Open vSwitch struct lacp_pdu
// (110 octets), carried directly in an Ethernet frame with EtherType 0x8809
// (Slow Protocols) to destination 01:80:c2:00:00:02.
package lacp

import (
	"encoding/binary"
	"errors"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

// State represents the 8-bit LACP state bitfield from the LACPDU.
type State uint8

const (
	// StateActive indicates the port is active LACP (lacpActivity).
	StateActive State = 0x01

	// StateShortTimeout indicates the port uses short timeouts (lacpTimeout).
	StateShortTimeout State = 0x02

	// StateAggregation indicates the port is aggregatable (aggregation).
	StateAggregation State = 0x04

	// StateSynchronization indicates the port is in sync with its partner (synchronization).
	StateSynchronization State = 0x08

	// StateCollecting indicates incoming frames are collected (collecting).
	StateCollecting State = 0x10

	// StateDistributing indicates outgoing frames are distributed (distributing).
	StateDistributing State = 0x20

	// StateDefaulted indicates partner information is defaulted (defaulted).
	StateDefaulted State = 0x40

	// StateExpired indicates the port receive timer has expired (expired).
	StateExpired State = 0x80
)

// Info represents the actor or partner parameters carried in an LACPDU.
type Info struct {
	SystemPriority uint16
	SystemID       netaddr.MAC
	Key            uint16
	PortPriority   uint16
	PortID         uint16
	State          State
}

// PDU represents a Link Aggregation Control Protocol Data Unit.
type PDU struct {
	Actor             Info
	Partner           Info
	CollectorMaxDelay uint16
}

// GroupAddress is the Slow Protocols multicast destination address (01:80:c2:00:00:02).
var GroupAddress = netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x02}

// ErrUnsupported indicates that an Ethernet frame does not carry a supported LACPDU.
var ErrUnsupported = errors.New("unsupported LACPDU")

// Encode serializes p into an Ethernet II frame carrying a 110-octet LACPDU payload.
func Encode(p PDU, src netaddr.MAC) ethernet.Frame {
	payload := make([]byte, 110)

	payload[0] = 1
	payload[1] = 1

	encodeInfo(payload[2:22], 1, p.Actor)
	encodeInfo(payload[22:42], 2, p.Partner)

	payload[42] = 3
	payload[43] = 16
	binary.BigEndian.PutUint16(payload[44:46], p.CollectorMaxDelay)

	payload[58] = 0
	payload[59] = 0

	return ethernet.Frame{
		Dst:       GroupAddress,
		Src:       src,
		EtherType: ethernet.EtherTypeSlowProtocols,
		Payload:   payload,
	}
}

func encodeInfo(dst []byte, tlvType uint8, info Info) {
	dst[0] = tlvType
	dst[1] = 20
	binary.BigEndian.PutUint16(dst[2:4], info.SystemPriority)
	copy(dst[4:10], info.SystemID[:])
	binary.BigEndian.PutUint16(dst[10:12], info.Key)
	binary.BigEndian.PutUint16(dst[12:14], info.PortPriority)
	binary.BigEndian.PutUint16(dst[14:16], info.PortID)
	dst[16] = byte(info.State)
}

// Decode deserializes an LACPDU from an Ethernet frame. It rejects frames with an
// unexpected EtherType, truncated payload, unsupported subtype, version, or TLV
// structure, returning an error wrapping [ErrUnsupported].
func Decode(f ethernet.Frame) (PDU, error) {
	if f.EtherType != ethernet.EtherTypeSlowProtocols {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("ethertype", f.EtherType).
			Msg("unexpected EtherType for LACPDU")
	}

	if len(f.Payload) < 110 {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("length", len(f.Payload)).
			Attr("min", 110).
			Msg("LACPDU payload is shorter than 110 octets")
	}

	subtype := f.Payload[0]
	if subtype != 1 {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("subtype", subtype).
			Msg("unsupported LACPDU subtype")
	}

	version := f.Payload[1]
	if version != 1 {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("version", version).
			Msg("unsupported LACPDU version")
	}

	actorType := f.Payload[2]
	if actorType != 1 {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("actor_type", actorType).
			Msg("unsupported actor TLV type")
	}

	actorLen := f.Payload[3]
	if actorLen != 20 {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("actor_length", actorLen).
			Msg("unsupported actor TLV length")
	}

	partnerType := f.Payload[22]
	if partnerType != 2 {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("partner_type", partnerType).
			Msg("unsupported partner TLV type")
	}

	partnerLen := f.Payload[23]
	if partnerLen != 20 {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("partner_length", partnerLen).
			Msg("unsupported partner TLV length")
	}

	collectorType := f.Payload[42]
	if collectorType != 3 {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("collector_type", collectorType).
			Msg("unsupported collector TLV type")
	}

	collectorLen := f.Payload[43]
	if collectorLen != 16 {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("collector_length", collectorLen).
			Msg("unsupported collector TLV length")
	}

	termType := f.Payload[58]
	if termType != 0 {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("terminator_type", termType).
			Msg("unsupported terminator TLV type")
	}

	termLen := f.Payload[59]
	if termLen != 0 {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("terminator_length", termLen).
			Msg("unsupported terminator TLV length")
	}

	return PDU{
		Actor:             decodeInfo(f.Payload[2:22]),
		Partner:           decodeInfo(f.Payload[22:42]),
		CollectorMaxDelay: binary.BigEndian.Uint16(f.Payload[44:46]),
	}, nil
}

func decodeInfo(src []byte) Info {
	var info Info
	info.SystemPriority = binary.BigEndian.Uint16(src[2:4])
	copy(info.SystemID[:], src[4:10])
	info.Key = binary.BigEndian.Uint16(src[10:12])
	info.PortPriority = binary.BigEndian.Uint16(src[12:14])
	info.PortID = binary.BigEndian.Uint16(src[14:16])
	info.State = State(src[16])

	return info
}
