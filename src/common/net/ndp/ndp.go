// Package ndp encodes and decodes IPv6 Neighbor Discovery Protocol Neighbor
// Solicitation and Neighbor Advertisement messages.
package ndp

import (
	"encoding/binary"
	"errors"
	"math"
	"net/netip"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

const (
	icmpv6Protocol            = 58
	neighborHopLimit          = 255
	optionSourceLinkLayerAddr = 1
	optionTargetLinkLayerAddr = 2
	optionLen                 = 1 // one 8-octet unit: 2-octet header plus a 6-octet MAC.
)

// Type identifies a Neighbor Discovery message format.
type Type uint8

const (
	// NeighborSolicitation asks whether a target address is reachable, or
	// announces a link-layer address change (RFC 4861 section 4.3).
	NeighborSolicitation Type = 135
	// NeighborAdvertisement answers a solicitation, or announces a link-layer
	// address change unsolicited (RFC 4861 section 4.4).
	NeighborAdvertisement Type = 136
)

// Message is a Neighbor Solicitation or Neighbor Advertisement. Router,
// Solicited, and Override apply only to [NeighborAdvertisement]; a
// [NeighborSolicitation] leaves them false. LinkLayerAddr carries the Source
// Link-Layer Address option on a solicitation and the Target Link-Layer
// Address option on an advertisement; HasLinkLayerAddr reports whether that
// option was present, since the zero MAC is itself a valid address.
type Message struct {
	Type             Type
	Target           netip.Addr
	Router           bool
	Solicited        bool
	Override         bool
	LinkLayerAddr    netaddr.MAC
	HasLinkLayerAddr bool
}

var (
	// ErrMalformed identifies invalid fields, lengths, and checksums.
	ErrMalformed = errors.New("malformed NDP message")
	// ErrUnsupported identifies well-formed wire formats the package does not support.
	ErrUnsupported = errors.New("unsupported NDP message")
)

// Encode serializes m as an ICMPv6 message and writes its checksum using
// hdr's IPv6 addresses. The result does not include an IPv6 header; a caller
// that sends it must set the IPv6 Hop Limit to 255 (RFC 4861 sections 4.3 and
// 4.4). Encode returns [ErrMalformed] for an invalid target, a solicitation
// from the unspecified address carrying a link-layer address option, and a
// solicitation with Router, Solicited, or Override set — the same rules
// Decode enforces, so Encode never builds a message Decode would refuse. It
// returns [ErrUnsupported] for unknown message types.
func Encode(hdr ip.Header, m Message) ([]byte, error) {
	if err := validateIPv6Header(hdr); err != nil {
		return nil, err
	}
	if !validTarget(m.Target) {
		return nil, errs.From(ErrMalformed).
			Attr("field", "target").
			Attr("target", m.Target).
			Msg("NDP target must be a unicast IPv6 address")
	}
	if m.Type == NeighborSolicitation && hdr.Src.IsUnspecified() && m.HasLinkLayerAddr {
		return nil, errs.From(ErrMalformed).
			Attr("field", "option").
			Msg("solicitation from the unspecified address must not carry a source link-layer address option")
	}
	if m.Type == NeighborSolicitation && (m.Router || m.Solicited || m.Override) {
		return nil, errs.From(ErrMalformed).
			Attr("field", "flags").
			Msg("solicitation must not set Router, Solicited, or Override, which apply only to an advertisement")
	}

	var wire []byte
	switch m.Type {
	case NeighborSolicitation:
		wire = encodeSolicitation(m)
	case NeighborAdvertisement:
		wire = encodeAdvertisement(m)
	default:
		return nil, errs.From(ErrUnsupported).
			Attr("type", m.Type).
			Msg("unsupported NDP message type")
	}

	binary.BigEndian.PutUint16(wire[2:4], checksum(hdr, wire))
	return wire, nil
}

func encodeSolicitation(m Message) []byte {
	wire := make([]byte, wireLength(m.HasLinkLayerAddr))
	wire[0] = byte(NeighborSolicitation)
	target := m.Target.As16()
	copy(wire[8:24], target[:])
	if m.HasLinkLayerAddr {
		putLinkLayerOption(wire[24:], optionSourceLinkLayerAddr, m.LinkLayerAddr)
	}
	return wire
}

func encodeAdvertisement(m Message) []byte {
	wire := make([]byte, wireLength(m.HasLinkLayerAddr))
	wire[0] = byte(NeighborAdvertisement)

	var flags byte
	if m.Router {
		flags |= 0x80
	}
	if m.Solicited {
		flags |= 0x40
	}
	if m.Override {
		flags |= 0x20
	}
	wire[4] = flags

	target := m.Target.As16()
	copy(wire[8:24], target[:])
	if m.HasLinkLayerAddr {
		putLinkLayerOption(wire[24:], optionTargetLinkLayerAddr, m.LinkLayerAddr)
	}
	return wire
}

func wireLength(hasOption bool) int {
	if hasOption {
		return 32
	}
	return 24
}

func putLinkLayerOption(dst []byte, optionType byte, mac netaddr.MAC) {
	dst[0] = optionType
	dst[1] = optionLen
	copy(dst[2:8], mac[:])
}

// Decode parses a Neighbor Solicitation or Neighbor Advertisement from
// payload and verifies its ICMPv6 checksum. It walks the option chain
// looking for the link-layer address option that matches the message type
// (Source for a solicitation, Target for an advertisement); an unrelated
// option earlier in the chain is skipped by its own declared length rather
// than mistaken for the option Decode wants. Decode returns [ErrMalformed]
// for a payload shorter than 24 octets, a hop limit other than 255, a code
// other than 0, a multicast target, an option whose declared length is zero
// or overruns the payload, a matching link-layer address option whose
// declared length is not one 8-octet unit, a Source Link-Layer Address
// option on a solicitation whose IPv6 source is the unspecified address, and
// a checksum that does not match. It returns [ErrUnsupported] for an unknown
// message type.
func Decode(hdr ip.Header, payload []byte) (Message, error) {
	if err := validateIPv6Header(hdr); err != nil {
		return Message{}, err
	}
	if len(payload) < 24 {
		return Message{}, errs.From(ErrMalformed).
			Attr("length", len(payload)).
			Attr("min", 24).
			Msg("NDP message is shorter than twenty-four octets")
	}
	if hdr.HopLimit != neighborHopLimit {
		return Message{}, errs.From(ErrMalformed).
			Attr("field", "hop_limit").
			Attr("hop_limit", hdr.HopLimit).
			Attr("want", neighborHopLimit).
			Msg("NDP message requires an IPv6 hop limit of 255")
	}
	if payload[1] != 0 {
		return Message{}, errs.From(ErrMalformed).
			Attr("field", "code").
			Attr("code", payload[1]).
			Msg("NDP message code must be zero")
	}
	if checksum(hdr, payload) != 0 {
		return Message{}, errs.From(ErrMalformed).
			Msg("NDP checksum does not match")
	}

	target := netip.AddrFrom16([16]byte(payload[8:24]))
	if !validTarget(target) {
		return Message{}, errs.From(ErrMalformed).
			Attr("field", "target").
			Attr("target", target).
			Msg("NDP target must be a unicast IPv6 address")
	}

	typ := Type(payload[0])
	switch typ {
	case NeighborSolicitation:
		return decodeSolicitation(hdr, payload, target)
	case NeighborAdvertisement:
		return decodeAdvertisement(payload, target)
	default:
		return Message{}, errs.From(ErrUnsupported).
			Attr("type", typ).
			Msg("unsupported NDP message type")
	}
}

func decodeSolicitation(hdr ip.Header, payload []byte, target netip.Addr) (Message, error) {
	m := Message{Type: NeighborSolicitation, Target: target}
	if len(payload) == 24 {
		return m, nil
	}

	mac, found, err := decodeLinkLayerOption(payload[24:], optionSourceLinkLayerAddr)
	if err != nil {
		return Message{}, err
	}
	if !found {
		return m, nil
	}
	if hdr.Src.IsUnspecified() {
		return Message{}, errs.From(ErrMalformed).
			Attr("field", "option").
			Msg("solicitation from the unspecified address must not carry a source link-layer address option")
	}
	m.LinkLayerAddr = mac
	m.HasLinkLayerAddr = true
	return m, nil
}

func decodeAdvertisement(payload []byte, target netip.Addr) (Message, error) {
	flags := payload[4]
	m := Message{
		Type:      NeighborAdvertisement,
		Target:    target,
		Router:    flags&0x80 != 0,
		Solicited: flags&0x40 != 0,
		Override:  flags&0x20 != 0,
	}
	if len(payload) == 24 {
		return m, nil
	}

	mac, found, err := decodeLinkLayerOption(payload[24:], optionTargetLinkLayerAddr)
	if err != nil {
		return Message{}, err
	}
	if !found {
		return m, nil
	}
	m.LinkLayerAddr = mac
	m.HasLinkLayerAddr = true
	return m, nil
}

// decodeLinkLayerOption walks the TLV option chain in b looking for an
// option of the given wantType. It returns the option's six-octet
// link-layer address and found true when one matches; it returns found
// false, with no error, when the chain runs out without a match. It refuses
// a declared option length of zero or one that overruns b, and refuses a
// matching option whose declared length is not 1 (the single 8-octet unit
// this package's 6-octet MAC option occupies).
func decodeLinkLayerOption(b []byte, wantType byte) (netaddr.MAC, bool, error) {
	for len(b) > 0 {
		if len(b) < 2 {
			return netaddr.MAC{}, false, errs.From(ErrMalformed).
				Attr("field", "option_length").
				Attr("remaining", len(b)).
				Msg("NDP link-layer address option exceeds the payload")
		}

		optionType := b[0]
		length := int(b[1])
		if length == 0 {
			return netaddr.MAC{}, false, errs.From(ErrMalformed).
				Attr("field", "option_length").
				Msg("NDP link-layer address option length must not be zero")
		}

		required := length * 8
		if len(b) < required {
			return netaddr.MAC{}, false, errs.From(ErrMalformed).
				Attr("field", "option_length").
				Attr("required", required).
				Attr("remaining", len(b)).
				Msg("NDP link-layer address option exceeds the payload")
		}

		if optionType == wantType {
			if length != optionLen {
				return netaddr.MAC{}, false, errs.From(ErrMalformed).
					Attr("field", "option_length").
					Attr("option_length", length).
					Attr("want", optionLen).
					Msg("NDP link-layer address option length must be one 8-octet unit")
			}
			return netaddr.MAC(b[2:8]), true, nil
		}

		b = b[required:]
	}

	return netaddr.MAC{}, false, nil
}

func validateIPv6Header(hdr ip.Header) error {
	if hdr.V6 == nil || hdr.V4 != nil || !pureIPv6(hdr.Src) || !pureIPv6(hdr.Dst) {
		return errs.From(ErrMalformed).
			Attr("field", "header").
			Attr("source", hdr.Src).
			Attr("destination", hdr.Dst).
			Msg("NDP checksum requires an IPv6 header and addresses")
	}
	return nil
}

func validTarget(addr netip.Addr) bool {
	return pureIPv6(addr) && !addr.IsMulticast()
}

func pureIPv6(address netip.Addr) bool {
	return address.Is6() && !address.Is4In6()
}

func checksum(hdr ip.Header, payload []byte) uint16 {
	var pseudo [40]byte
	source := hdr.Src.As16()
	destination := hdr.Dst.As16()
	copy(pseudo[0:16], source[:])
	copy(pseudo[16:32], destination[:])
	binary.BigEndian.PutUint32(pseudo[32:36], uint32(len(payload)))
	pseudo[39] = icmpv6Protocol

	sum := checksumSum(0, pseudo[:])
	sum = checksumSum(sum, payload)
	for sum > math.MaxUint16 {
		sum = sum&math.MaxUint16 + sum>>16
	}
	return ^uint16(sum)
}

func checksumSum(sum uint64, b []byte) uint64 {
	for len(b) >= 2 {
		sum += uint64(binary.BigEndian.Uint16(b[:2]))
		b = b[2:]
	}
	if len(b) == 1 {
		sum += uint64(b[0]) << 8
	}
	return sum
}
