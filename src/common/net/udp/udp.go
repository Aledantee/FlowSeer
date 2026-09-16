// Package udp encodes and decodes User Datagram Protocol headers.
package udp

import (
	"encoding/binary"
	"errors"
	"math"
	"net/netip"

	"go.aledante.io/FlowSeer/src/common/errs"
)

const (
	headerLen = 8
	// protocolUDP is the IANA protocol number for UDP, carried in both the
	// IPv4 and the IPv6 pseudo-header.
	protocolUDP      = 17
	ipv4PseudoHdrLen = 12
	ipv6PseudoHdrLen = 40
)

// Header is a UDP header: source port, destination port, the length of the
// header plus payload in octets, and the one's-complement checksum.
type Header struct {
	SrcPort  uint16
	DstPort  uint16
	Length   uint16
	Checksum uint16
}

// ErrMalformed identifies invalid fields, lengths, and address families.
var ErrMalformed = errors.New("malformed UDP datagram")

// Decode parses a UDP header from b and returns the payload it bounds. It
// returns [ErrMalformed] if b is shorter than eight octets, or if the header's
// Length field is under eight or exceeds len(b). Decode does not verify the
// checksum; call [Verify] for that.
func Decode(b []byte) (Header, []byte, error) {
	if len(b) < headerLen {
		return Header{}, nil, errs.From(ErrMalformed).
			Attr("field", "length").
			Attr("length", len(b)).
			Attr("min", headerLen).
			Msg("UDP datagram is shorter than eight octets")
	}

	length := binary.BigEndian.Uint16(b[4:6])
	if length < headerLen {
		return Header{}, nil, errs.From(ErrMalformed).
			Attr("field", "length").
			Attr("length", length).
			Attr("min", headerLen).
			Msg("UDP length field is shorter than the header it must include")
	}
	if int(length) > len(b) {
		return Header{}, nil, errs.From(ErrMalformed).
			Attr("field", "length").
			Attr("length", length).
			Attr("have", len(b)).
			Msg("UDP length field exceeds the supplied buffer")
	}

	h := Header{
		SrcPort:  binary.BigEndian.Uint16(b[0:2]),
		DstPort:  binary.BigEndian.Uint16(b[2:4]),
		Length:   length,
		Checksum: binary.BigEndian.Uint16(b[6:8]),
	}
	return h, b[headerLen:length], nil
}

// Encode serializes h and payload into a UDP datagram addressed from src to
// dst. It ignores h.Length and h.Checksum and derives both fields itself:
// Length from headerLen+len(payload), and Checksum from the RFC 768 (IPv4) or
// RFC 8200 section 8.1 (IPv6) pseudo-header checksum. A checksum that
// computes to zero is sent as 0xffff, since a real zero means "no checksum"
// on IPv4 and is forbidden outright on IPv6. Encode returns [ErrMalformed] if
// src and dst are not the same address family, if either is not an IPv4,
// IPv6, or IPv4-mapped IPv6 address, or if the encoded datagram would exceed
// 65535 octets.
func Encode(h Header, payload []byte, src, dst netip.Addr) ([]byte, error) {
	v4, err := addressFamily(src, dst)
	if err != nil {
		return nil, err
	}

	length := headerLen + len(payload)
	if length > math.MaxUint16 {
		return nil, errs.From(ErrMalformed).
			Attr("field", "length").
			Attr("length", length).
			Attr("max", math.MaxUint16).
			Msg("UDP datagram exceeds the sixteen-bit length field")
	}

	wire := make([]byte, length)
	binary.BigEndian.PutUint16(wire[0:2], h.SrcPort)
	binary.BigEndian.PutUint16(wire[2:4], h.DstPort)
	binary.BigEndian.PutUint16(wire[4:6], uint16(length))
	copy(wire[headerLen:], payload)

	sum := checksum(src, dst, v4, wire)
	if sum == 0 {
		sum = math.MaxUint16
	}
	binary.BigEndian.PutUint16(wire[6:8], sum)
	return wire, nil
}

// Verify decodes b as a UDP datagram addressed from src to dst and recomputes
// its checksum. It reports whether the checksum matches; it returns false for
// a datagram [Decode] itself would refuse.
func Verify(b []byte, src, dst netip.Addr) bool {
	v4, err := addressFamily(src, dst)
	if err != nil {
		return false
	}
	_, payload, err := Decode(b)
	if err != nil {
		return false
	}
	wire := b[:headerLen+len(payload)]
	return checksum(src, dst, v4, wire) == 0
}

// addressFamily reports whether src and dst are both IPv4 addresses, treating
// an IPv4-mapped IPv6 address as IPv4 (true), or both pure IPv6 addresses
// (false). It returns [ErrMalformed] for a mixed pair.
func addressFamily(src, dst netip.Addr) (bool, error) {
	switch {
	case isIPv4(src) && isIPv4(dst):
		return true, nil
	case pureIPv6(src) && pureIPv6(dst):
		return false, nil
	default:
		return false, errs.From(ErrMalformed).
			Attr("field", "address").
			Attr("source", src).
			Attr("destination", dst).
			Msg("UDP checksum requires source and destination in the same address family")
	}
}

func isIPv4(a netip.Addr) bool {
	return a.Is4() || a.Is4In6()
}

func pureIPv6(a netip.Addr) bool {
	return a.Is6() && !a.Is4In6()
}

// checksum sums the pseudo-header for src and dst (IPv4 when v4 is true,
// otherwise IPv6) and the UDP datagram wire, folds the carries, and returns
// the one's complement.
func checksum(src, dst netip.Addr, v4 bool, wire []byte) uint16 {
	var sum uint64
	if v4 {
		sum = checksumSum(sum, ipv4PseudoHeader(src, dst, len(wire)))
	} else {
		sum = checksumSum(sum, ipv6PseudoHeader(src, dst, len(wire)))
	}
	sum = checksumSum(sum, wire)
	for sum > math.MaxUint16 {
		sum = sum&math.MaxUint16 + sum>>16
	}
	return ^uint16(sum)
}

func ipv4PseudoHeader(src, dst netip.Addr, udpLength int) []byte {
	pseudo := make([]byte, ipv4PseudoHdrLen)
	srcBytes := src.As4()
	dstBytes := dst.As4()
	copy(pseudo[0:4], srcBytes[:])
	copy(pseudo[4:8], dstBytes[:])
	pseudo[9] = protocolUDP
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(udpLength))
	return pseudo
}

func ipv6PseudoHeader(src, dst netip.Addr, udpLength int) []byte {
	pseudo := make([]byte, ipv6PseudoHdrLen)
	srcBytes := src.As16()
	dstBytes := dst.As16()
	copy(pseudo[0:16], srcBytes[:])
	copy(pseudo[16:32], dstBytes[:])
	binary.BigEndian.PutUint32(pseudo[32:36], uint32(udpLength))
	pseudo[39] = protocolUDP
	return pseudo
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
