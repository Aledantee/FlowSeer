// Package lacp provides an IEEE 802.1AX codec for Link Aggregation Control
// Protocol data units (LACPDUs) and Marker Protocol data units (Marker PDUs).
// Its LACPDU wire layout matches the Open vSwitch struct lacp_pdu (110 octets),
// carried directly in an Ethernet frame with EtherType 0x8809 (Slow Protocols)
// to destination 01:80:c2:00:00:02.
package lacp

import (
	"bytes"
	"encoding/binary"
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

// State represents the 8-bit LACP state bitfield from the LACPDU.
type State uint8

const (
	// SubtypeLACP identifies a Link Aggregation Control Protocol data unit.
	SubtypeLACP uint8 = 0x01

	// SubtypeMarker identifies a Marker Protocol data unit.
	SubtypeMarker uint8 = 0x02
)

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

// ErrUnsupported indicates that an Ethernet frame does not carry a supported
// LACPDU or Marker PDU.
var ErrUnsupported = errs.Msg("unsupported LACPDU or Marker PDU")

const (
	markerInformationTLVType   uint8 = 0x01
	markerResponseTLVType      uint8 = 0x02
	markerInformationTLVLength uint8 = 16
)

// MarkerResponse returns the Marker Response PDU that answers the Marker PDU
// in f: a copy of f with the TLV type changed to Marker Response Information,
// [GroupAddress] as destination, and src as source. Every other octet and the
// tag stack are as received, and the result shares no memory with f. It
// returns an error wrapping [ErrUnsupported] when f is not a Slow Protocols
// frame, its payload is shorter than 110 octets, its subtype is not
// [SubtypeMarker], or its first TLV is not Marker Information (type 0x01,
// length 16).
func MarkerResponse(f ethernet.Frame, src netaddr.MAC) (ethernet.Frame, error) {
	if f.EtherType != ethernet.EtherTypeSlowProtocols {
		return ethernet.Frame{}, errs.From(ErrUnsupported).
			Attr("ethertype", f.EtherType).
			Msg("unexpected EtherType for Marker PDU")
	}

	if len(f.Payload) < 110 {
		return ethernet.Frame{}, errs.From(ErrUnsupported).
			Attr("length", len(f.Payload)).
			Attr("min", 110).
			Msg("Marker PDU payload is shorter than 110 octets")
	}

	if f.Payload[0] != SubtypeMarker {
		return ethernet.Frame{}, errs.From(ErrUnsupported).
			Attr("subtype", f.Payload[0]).
			Msg("unsupported Marker PDU subtype")
	}

	if f.Payload[2] != markerInformationTLVType {
		return ethernet.Frame{}, errs.From(ErrUnsupported).
			Attr("tlv_type", f.Payload[2]).
			Msg("unsupported Marker TLV type")
	}

	if f.Payload[3] != markerInformationTLVLength {
		return ethernet.Frame{}, errs.From(ErrUnsupported).
			Attr("tlv_length", f.Payload[3]).
			Msg("unsupported Marker TLV length")
	}

	response := f
	response.Dst = GroupAddress
	response.Src = src
	response.Tags = slices.Clone(f.Tags)
	response.Payload = bytes.Clone(f.Payload)
	response.Payload[2] = markerResponseTLVType

	return response, nil
}

// Encode serializes p into an Ethernet II frame carrying a 110-octet LACPDU payload.
func Encode(p PDU, src netaddr.MAC) ethernet.Frame {
	payload := make([]byte, 110)

	payload[0] = SubtypeLACP
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

// Decode deserializes an LACPDU from an Ethernet frame. It rejects frames with
// an unexpected EtherType, a payload shorter than 110 octets, an unsupported
// subtype, or an actor, partner, or collector TLV length other than 20, 20, or
// 16 respectively. AX 6.4.12 forbids a Receive machine from validating the
// Version Number, TLV_type, and Reserved fields, so Decode reads the Version 1
// field positions after the length checks and ignores octets from offset 58
// onward. Rejections wrap [ErrUnsupported].
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
	if subtype != SubtypeLACP {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("subtype", subtype).
			Msg("unsupported LACPDU subtype")
	}

	actorLen := f.Payload[3]
	if actorLen != 20 {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("actor_length", actorLen).
			Msg("unsupported actor TLV length")
	}

	partnerLen := f.Payload[23]
	if partnerLen != 20 {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("partner_length", partnerLen).
			Msg("unsupported partner TLV length")
	}

	collectorLen := f.Payload[43]
	if collectorLen != 16 {
		return PDU{}, errs.From(ErrUnsupported).
			Attr("collector_length", collectorLen).
			Msg("unsupported collector TLV length")
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
