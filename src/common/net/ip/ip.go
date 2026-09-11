// Package ip provides an IP header codec for IPv4 (RFC 791) and IPv6 (RFC 8200).
package ip

import (
	"encoding/binary"
	"net/netip"

	"go.aledante.io/FlowSeer/src/common/errs"
)

const (
	// V4HeaderLen is the minimum length of an IPv4 header in octets without options (RFC 791 section 3.1).
	V4HeaderLen = 20

	// V6HeaderLen is the fixed length of an IPv6 header in octets (RFC 8200 section 3).
	V6HeaderLen = 40
)

// V4 holds fields specific to an IPv4 header (RFC 791 section 3.1).
type V4 struct {
	ID             uint16
	Flags          uint8
	FragmentOffset uint16
	Options        []byte
}

// V6 holds fields specific to an IPv6 header (RFC 8200 section 3).
type V6 struct {
	FlowLabel uint32
}

// Header represents an IPv4 or IPv6 header. Fields common to both families
// share a single representation: HopLimit holds IPv4 TTL and IPv6 Hop Limit;
// Protocol holds IPv4 Protocol and IPv6 Next Header; TrafficClass holds IPv4
// Type of Service (TOS) and IPv6 Traffic Class. Exactly one of [V4] or [V6]
// must be set for a valid header.
type Header struct {
	Src          netip.Addr
	Dst          netip.Addr
	HopLimit     uint8
	Protocol     uint8
	TrafficClass uint8
	V4           *V4
	V6           *V6
}

// Version returns 4 or 6 based on which family part is set, or 0 when neither
// or both are set.
func (h Header) Version() int {
	if h.V4 != nil && h.V6 == nil {
		return 4
	}
	if h.V6 != nil && h.V4 == nil {
		return 6
	}
	return 0
}

// Decode decodes an IPv4 or IPv6 header from wire bytes, dispatching on the
// version nibble. It returns the decoded [Header] and the payload sub-slice
// bounded by the IPv4 total length (RFC 791 section 3.1) or the IPv6 payload
// length (RFC 8200 section 3).
//
// For IPv4, Decode reads the Internet Header Length (IHL), total length, and
// checksum before trusting any of them. Decode returns an error if the version
// is not 4 or 6, if IPv4 IHL is under 5 or exceeds the buffer, if IPv4 total
// length exceeds the buffer, if the IPv4 checksum is invalid (RFC 1071 section 2),
// or if the IPv6 payload length exceeds the buffer. The returned payload aliases b.
func Decode(b []byte) (Header, []byte, error) {
	if len(b) < 1 {
		return Header{}, nil, errs.New().
			Attr("field", "buffer").
			Attr("have", 0).
			Attr("min", 1).
			Msg("buffer too short for IP header")
	}

	version := b[0] >> 4
	switch version {
	case 4:
		return decodeV4(b)
	case 6:
		return decodeV6(b)
	default:
		return Header{}, nil, errs.New().
			Attr("field", "version").
			Attr("version", version).
			Msgf("unsupported IP version %d", version)
	}
}

func decodeV4(b []byte) (Header, []byte, error) {
	ihl := b[0] & 0x0f
	if ihl < 5 {
		return Header{}, nil, errs.New().
			Attr("field", "ihl").
			Attr("ihl", ihl).
			Msgf("IPv4 IHL %d is under minimum 5", ihl)
	}

	headerLen := int(ihl) * 4
	if len(b) < headerLen {
		return Header{}, nil, errs.New().
			Attr("field", "ihl").
			Attr("ihl", ihl).
			Attr("header_len", headerLen).
			Attr("buffer_len", len(b)).
			Msgf("IPv4 header length %d exceeds buffer length %d", headerLen, len(b))
	}

	totalLen := int(binary.BigEndian.Uint16(b[2:4]))
	if totalLen < headerLen {
		return Header{}, nil, errs.New().
			Attr("field", "total_length").
			Attr("total_length", totalLen).
			Attr("header_len", headerLen).
			Msgf("IPv4 total length %d is less than header length %d", totalLen, headerLen)
	}
	if totalLen > len(b) {
		return Header{}, nil, errs.New().
			Attr("field", "total_length").
			Attr("total_length", totalLen).
			Attr("buffer_len", len(b)).
			Msgf("IPv4 total length %d exceeds buffer length %d", totalLen, len(b))
	}

	transmittedChecksum := binary.BigEndian.Uint16(b[10:12])
	expectedChecksum := v4Checksum(b[:headerLen])
	if transmittedChecksum != expectedChecksum {
		return Header{}, nil, errs.New().
			Attr("field", "checksum").
			Attr("checksum", transmittedChecksum).
			Attr("expected", expectedChecksum).
			Msgf("IPv4 checksum 0x%04x does not match computed 0x%04x", transmittedChecksum, expectedChecksum)
	}

	var options []byte
	if headerLen > V4HeaderLen {
		options = append([]byte(nil), b[V4HeaderLen:headerLen]...)
	}

	fragWord := binary.BigEndian.Uint16(b[6:8])
	hdr := Header{
		Src:          netip.AddrFrom4([4]byte(b[12:16])),
		Dst:          netip.AddrFrom4([4]byte(b[16:20])),
		HopLimit:     b[8],
		Protocol:     b[9],
		TrafficClass: b[1],
		V4: &V4{
			ID:             binary.BigEndian.Uint16(b[4:6]),
			Flags:          uint8(fragWord >> 13),
			FragmentOffset: fragWord & 0x1fff,
			Options:        options,
		},
	}

	return hdr, b[headerLen:totalLen], nil
}

func decodeV6(b []byte) (Header, []byte, error) {
	if len(b) < V6HeaderLen {
		return Header{}, nil, errs.New().
			Attr("field", "buffer").
			Attr("have", len(b)).
			Attr("min", V6HeaderLen).
			Msgf("buffer length %d is too short for IPv6 header", len(b))
	}

	payloadLen := int(binary.BigEndian.Uint16(b[4:6]))
	if len(b) < V6HeaderLen+payloadLen {
		return Header{}, nil, errs.New().
			Attr("field", "payload_length").
			Attr("payload_length", payloadLen).
			Attr("header_len", V6HeaderLen).
			Attr("buffer_len", len(b)).
			Msgf("IPv6 payload length %d exceeds buffer length %d", payloadLen, len(b)-V6HeaderLen)
	}

	firstWord := binary.BigEndian.Uint32(b[0:4])
	hdr := Header{
		Src:          netip.AddrFrom16([16]byte(b[8:24])),
		Dst:          netip.AddrFrom16([16]byte(b[24:40])),
		HopLimit:     b[7],
		Protocol:     b[6],
		TrafficClass: uint8((firstWord >> 20) & 0xff),
		V6: &V6{
			FlowLabel: firstWord & 0x000fffff,
		},
	}

	return hdr, b[V6HeaderLen : V6HeaderLen+payloadLen], nil
}

// Encode serializes the header prepended to payload. It refuses a header with
// both or neither of [V4] and [V6] set, or whose addresses disagree with the
// selected family.
//
// For IPv4 (RFC 791 section 3.1), Encode calculates the Internet Header Length (IHL)
// from the options length (which must be a multiple of 4 and at most 40 octets),
// writes the total length including payload, and computes the header checksum
// over the header with the checksum field zeroed (RFC 1071 section 2).
//
// For IPv6 (RFC 8200 section 3), Encode writes the payload length. IPv6 headers
// carry no header checksum.
func (h Header) Encode(payload []byte) ([]byte, error) {
	if h.V4 == nil && h.V6 == nil {
		return nil, errs.New().
			Attr("field", "parts").
			Attr("parts", "neither").
			Msg("header must have exactly one family part set")
	}
	if h.V4 != nil && h.V6 != nil {
		return nil, errs.New().
			Attr("field", "parts").
			Attr("parts", "both").
			Msg("header cannot have both IPv4 and IPv6 parts set")
	}

	if h.V4 != nil {
		return h.encodeV4(payload)
	}
	return h.encodeV6(payload)
}

func (h Header) encodeV4(payload []byte) ([]byte, error) {
	if !h.Src.Is4() {
		return nil, errs.New().
			Attr("field", "address").
			Attr("address", h.Src).
			Attr("endpoint", "src").
			Msgf("IPv4 source address %s is not an IPv4 address", h.Src)
	}
	if !h.Dst.Is4() {
		return nil, errs.New().
			Attr("field", "address").
			Attr("address", h.Dst).
			Attr("endpoint", "dst").
			Msgf("IPv4 destination address %s is not an IPv4 address", h.Dst)
	}

	optLen := len(h.V4.Options)
	if optLen%4 != 0 || optLen > 40 {
		return nil, errs.New().
			Attr("field", "options").
			Attr("options_len", optLen).
			Msgf("IPv4 options length %d must be a multiple of 4 and at most 40 octets", optLen)
	}

	headerLen := V4HeaderLen + optLen
	totalLen := headerLen + len(payload)
	if totalLen > 65535 {
		return nil, errs.New().
			Attr("field", "total_length").
			Attr("total_length", totalLen).
			Msgf("IPv4 total length %d exceeds maximum 65535", totalLen)
	}

	out := make([]byte, totalLen)
	out[0] = (4 << 4) | uint8(headerLen/4)
	out[1] = h.TrafficClass
	binary.BigEndian.PutUint16(out[2:4], uint16(totalLen))
	binary.BigEndian.PutUint16(out[4:6], h.V4.ID)

	fragWord := (uint16(h.V4.Flags&0x07) << 13) | (h.V4.FragmentOffset & 0x1fff)
	binary.BigEndian.PutUint16(out[6:8], fragWord)

	out[8] = h.HopLimit
	out[9] = h.Protocol

	srcBytes := h.Src.As4()
	copy(out[12:16], srcBytes[:])
	dstBytes := h.Dst.As4()
	copy(out[16:20], dstBytes[:])

	if optLen > 0 {
		copy(out[V4HeaderLen:headerLen], h.V4.Options)
	}

	binary.BigEndian.PutUint16(out[10:12], v4Checksum(out[:headerLen]))
	copy(out[headerLen:], payload)

	return out, nil
}

func (h Header) encodeV6(payload []byte) ([]byte, error) {
	if !h.Src.Is6() || h.Src.Is4In6() {
		return nil, errs.New().
			Attr("field", "address").
			Attr("address", h.Src).
			Attr("endpoint", "src").
			Msgf("IPv6 source address %s is not an IPv6 address", h.Src)
	}
	if !h.Dst.Is6() || h.Dst.Is4In6() {
		return nil, errs.New().
			Attr("field", "address").
			Attr("address", h.Dst).
			Attr("endpoint", "dst").
			Msgf("IPv6 destination address %s is not an IPv6 address", h.Dst)
	}

	if len(payload) > 65535 {
		return nil, errs.New().
			Attr("field", "payload_length").
			Attr("payload_length", len(payload)).
			Msgf("IPv6 payload length %d exceeds maximum 65535", len(payload))
	}

	totalLen := V6HeaderLen + len(payload)
	out := make([]byte, totalLen)

	firstWord := (uint32(6) << 28) | (uint32(h.TrafficClass) << 20) | (h.V6.FlowLabel & 0x000fffff)
	binary.BigEndian.PutUint32(out[0:4], firstWord)
	binary.BigEndian.PutUint16(out[4:6], uint16(len(payload)))
	out[6] = h.Protocol
	out[7] = h.HopLimit

	srcBytes := h.Src.As16()
	copy(out[8:24], srcBytes[:])
	dstBytes := h.Dst.As16()
	copy(out[24:40], dstBytes[:])

	copy(out[V6HeaderLen:], payload)

	return out, nil
}

func v4Checksum(header []byte) uint16 {
	var sum uint32
	for i := 0; i < len(header); i += 2 {
		if i == 10 {
			continue
		}
		sum += uint32(binary.BigEndian.Uint16(header[i : i+2]))
	}
	for sum > 0xffff {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
