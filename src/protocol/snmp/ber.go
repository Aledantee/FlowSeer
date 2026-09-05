package snmp

import (
	"encoding/binary"
	"math"
	"net"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ber.go is the hand-rolled, SNMP-specific BER codec at the heart of the
// native backend. It is deliberately not a general-purpose ASN.1
// library: it covers exactly the tag/length rules and the SMIv2 type set
// SNMP v1/v2c uses on the wire, and it owns every bounds check so a
// malformed datagram yields a typed error rather than a panic.
//
// The decode primitives ([parseTLV], [parseLength], the decodeX helpers)
// alias the input buffer — they never copy — so the caller owns the
// lifetime of the bytes. The encode primitives append to a caller-owned
// destination slice in the [append] idiom.

// BER identifier (tag) octets used by SNMP. These are wire-protocol
// numeric codes; the pinning test in ber_test.go fixes each to its RFC
// value so a reorder cannot silently change the wire encoding.
//
// Universal-class tags (primitive unless noted).
const (
	tagInteger     byte = 0x02 // INTEGER
	tagOctetString byte = 0x04 // OCTET STRING
	tagNull        byte = 0x05 // NULL
	tagOID         byte = 0x06 // OBJECT IDENTIFIER
	tagBitString   byte = 0x03 // BIT STRING
	tagSequence    byte = 0x30 // SEQUENCE / SEQUENCE OF (constructed, 0x20|0x10)
)

// APPLICATION class (primitive), per RFC 2578 §7.1.1 / RFC 3416. The
// class bits are 0b01xxxxxx, so the APPLICATION-n tag is 0x40|n.
const (
	tagIPAddress   byte = 0x40 // [APPLICATION 0] IpAddress (4-octet)
	tagCounter32   byte = 0x41 // [APPLICATION 1] Counter32
	tagGauge32     byte = 0x42 // [APPLICATION 2] Gauge32 / Unsigned32
	tagTimeTicks   byte = 0x43 // [APPLICATION 3] TimeTicks
	tagOpaque      byte = 0x44 // [APPLICATION 4] Opaque
	tagNsapAddress byte = 0x45 // [APPLICATION 5] NsapAddress
	tagCounter64   byte = 0x46 // [APPLICATION 6] Counter64
	tagUinteger32  byte = 0x47 // [APPLICATION 7] Unsigned32 (gosnmp-compatible)
)

// SNMPv2 exception variants are context-specific primitive tags carried
// in place of a value in a response varbind (RFC 3416 §4.2.1).
const (
	tagNoSuchObject   byte = 0x80 // [0] noSuchObject
	tagNoSuchInstance byte = 0x81 // [1] noSuchInstance
	tagEndOfMibView   byte = 0x82 // [2] endOfMibView
)

// Opaque-wrapped IEEE-754 markers (RFC 2856 / Net-SNMP). The Opaque
// value's content begins with these two octets, a length octet, then the
// big-endian float payload.
const (
	opaqueFloatTag  byte = 0x78 // 0x9F 0x78 — single-precision (4 bytes)
	opaqueDoubleTag byte = 0x79 // 0x9F 0x79 — double-precision (8 bytes)
	opaqueTagPrefix byte = 0x9F // leading identifier octet of the wrapped real
)

// maxDecodeDepth caps SEQUENCE/constructed nesting the decoder will
// descend through. An SNMP message nests
// message > PDU > varbind-list > varbind > value, so 8 leaves ample
// headroom while stopping a crafted deeply-nested datagram from driving
// unbounded recursion — a DoS the per-TLV bounds checks alone do not
// catch.
const maxDecodeDepth = 8

// Codec sentinel errors. They are pointer-identity comparable via
// [errors.Is] even after [errs.Wrap]/[errs.Wrapf] adds context, so callers
// (and the fuzz target) can classify a malformed datagram without string
// matching.
var (
	// errTruncated is returned when a TLV's declared length runs past the
	// end of the available bytes, or a header is incomplete.
	errTruncated = errs.Msg("truncated TLV")
	// errIndefiniteLength rejects the BER indefinite-form length (0x80);
	// SNMP uses only definite-form lengths.
	errIndefiniteLength = errs.Msg("indefinite-form length not supported")
	// errLengthOverflow is returned when a length field is larger than the
	// remaining buffer or would overflow int (gosnmp #552).
	errLengthOverflow = errs.Msg("length exceeds remaining buffer")
	// errHighTagForm rejects multi-octet (high-tag-number) identifiers;
	// every SNMP tag fits in a single identifier octet.
	errHighTagForm = errs.Msg("high-tag-number identifier form not supported")
	// errMaxDepth is returned when constructed nesting exceeds
	// [maxDecodeDepth].
	errMaxDepth = errs.Msg("nesting depth exceeds maximum")
	// errIntOverflow is returned when an INTEGER/unsigned field carries
	// more significant octets than its Go target can hold.
	errIntOverflow = errs.Msg("integer value out of range")
	// errOIDSubIDOverflow is returned when an OID sub-identifier exceeds
	// the uint32 range SNMP permits.
	errOIDSubIDOverflow = errs.Msg("OID sub-identifier overflows uint32")
	// errUnexpectedTag is returned when a decoder is handed content under a
	// tag it does not handle.
	errUnexpectedTag = errs.Msg("unexpected tag")
)

// parseLength decodes a single BER length field from the front of buf.
// It handles short-form (one octet, < 0x80) and long-form (0x8N followed
// by N length octets) and rejects the indefinite form (0x80) outright.
// It returns the decoded length and the number of octets the
// length field itself consumed.
//
// At most four long-form length octets are accepted: a four-octet length
// already exceeds any UDP datagram, and the cap keeps the accumulation
// inside the int range on every supported platform.
func parseLength(buf []byte) (length int, consumed int, err error) {
	if len(buf) == 0 {
		return 0, 0, errs.Wrap(errTruncated, "ber: parse length")
	}
	b0 := buf[0]
	if b0 < 0x80 {
		return int(b0), 1, nil
	}
	if b0 == 0x80 {
		return 0, 0, errIndefiniteLength
	}
	n := int(b0 & 0x7f)
	if n > 4 {
		return 0, 0, errs.Wrapf(errLengthOverflow, "ber: long-form length with %d octets", n)
	}
	if len(buf) < 1+n {
		return 0, 0, errs.Wrap(errTruncated, "ber: parse long-form length")
	}
	val := 0
	for i := 0; i < n; i++ {
		val = (val << 8) | int(buf[1+i])
	}
	if val < 0 {
		return 0, 0, errs.Wrap(errLengthOverflow, "ber: long-form length")
	}
	return val, 1 + n, nil
}

// appendLength appends the BER definite-form encoding of n to dst using
// the minimal number of octets, and returns the extended slice. It is the
// exact inverse of [parseLength] for the values [parseLength] produces.
func appendLength(dst []byte, n int) []byte {
	if n < 0x80 {
		return append(dst, byte(n))
	}
	var tmp [8]byte
	i := len(tmp)
	for n > 0 {
		i--
		tmp[i] = byte(n)
		n >>= 8
	}
	cnt := len(tmp) - i
	dst = append(dst, byte(0x80|cnt))
	return append(dst, tmp[i:]...)
}

// parseTLV reads one BER TLV from the front of buf. It returns the
// identifier (tag) octet, the content sub-slice (aliasing buf, never
// copied), and the total number of octets consumed (identifier + length +
// content) so the caller can advance to the next TLV.
//
// SNMP uses only low-tag-number identifiers, so a high-tag-number form
// (low five bits all set) is rejected rather than mis-parsed. Every
// length and bounds check is performed before any slice is taken.
func parseTLV(buf []byte) (tag byte, content []byte, consumed int, err error) {
	if len(buf) < 1 {
		return 0, nil, 0, errs.Wrap(errTruncated, "ber: parse identifier")
	}
	id := buf[0]
	if id&0x1f == 0x1f {
		return 0, nil, 0, errs.Wrap(errHighTagForm, "ber: parse identifier")
	}
	length, lc, err := parseLength(buf[1:])
	if err != nil {
		return 0, nil, 0, err
	}
	start := 1 + lc
	end := start + length
	// end < start guards the integer-overflow case where a colossal
	// length wraps; end > len(buf) is the ordinary truncation case.
	if end < start || end > len(buf) {
		return 0, nil, 0, errs.Wrapf(errLengthOverflow,
			"ber: content of %d octets exceeds %d remaining", length, len(buf)-start)
	}
	return id, buf[start:end], end, nil
}

// parseSequence reads a constructed TLV expected to carry tag (a SEQUENCE
// or a context-specific constructed PDU tag) and returns its content. It
// is the depth-guarded entry point for descending into nested structure:
// callers pass depth+1 when recursing, and parseSequence rejects descent
// past [maxDecodeDepth]. This is where the recursion ceiling lives,
// so every structural decoder inherits it for free.
func parseSequence(buf []byte, wantTag byte, depth int) (content []byte, consumed int, err error) {
	if depth > maxDecodeDepth {
		return nil, 0, errs.Wrapf(errMaxDepth, "ber: depth %d", depth)
	}
	tag, content, consumed, err := parseTLV(buf)
	if err != nil {
		return nil, 0, err
	}
	if tag != wantTag {
		return nil, 0, errs.Wrapf(errUnexpectedTag, "ber: expected tag 0x%02x, got 0x%02x", wantTag, tag)
	}
	return content, consumed, nil
}

// decodeSignedInt decodes a two's-complement BER INTEGER (used for SNMP
// INTEGER/Integer32, request-id, error-status, error-index, version). It
// accepts up to eight octets and sign-extends, returning int64; callers
// that need a narrower range (Integer32) range-check the result.
func decodeSignedInt(b []byte) (int64, error) {
	if len(b) == 0 {
		return 0, errs.Wrap(errTruncated, "ber: decode integer")
	}
	if len(b) > 8 {
		return 0, errs.Wrapf(errIntOverflow, "ber: integer with %d octets", len(b))
	}
	var v int64
	if b[0]&0x80 != 0 {
		v = -1 // sign-extend negative values
	}
	for _, c := range b {
		v = (v << 8) | int64(c)
	}
	return v, nil
}

// decodeUnsigned decodes a BER-encoded non-negative integer for the SMIv2
// unsigned application types (Counter32/Gauge32/Unsigned32/TimeTicks/
// Counter64) as uint64. It tolerates the leading-zero padding agents emit
// to keep a high-bit-set value non-negative — including the 5-byte
// encoding of a 32-bit counter and the 9-byte encoding of a 64-bit one.
// The result is the raw counter; wrap/discontinuity handling is
// the caller's job.
func decodeUnsigned(b []byte) (uint64, error) {
	// Empty content decodes as zero; some agents emit a zero-length INTEGER
	// for a zero counter. Treat it leniently rather than erroring.
	i := 0
	for i < len(b)-1 && b[i] == 0x00 {
		i++
	}
	s := b[i:]
	if len(s) > 8 {
		return 0, errs.Wrapf(errIntOverflow, "ber: unsigned with %d significant octets", len(s))
	}
	var v uint64
	for _, c := range s {
		v = (v << 8) | uint64(c)
	}
	return v, nil
}

// decodeOID decodes a BER OBJECT IDENTIFIER content slice into an
// [OID]. A zero-length content yields the empty OID — the documented
// "no OID" sentinel — rather than a parse failure, so a varbind
// carrying a zero-length name does not abort decoding the rest of the
// list. The first sub-identifier byte(s) expand to the first two arcs per
// X.690 §8.19; every sub-identifier is bounds- and uint32-range-checked.
func decodeOID(b []byte) (OID, error) {
	if len(b) == 0 {
		return OID{}, nil
	}
	first, n, err := readBase128(b, 0)
	if err != nil {
		return OID{}, err
	}
	// The first octet expands to two arcs and every later sub-identifier is
	// at least one octet, so len(b)+1 is the exact upper bound on the arc
	// count. Pre-size to it so the slice never regrows during the loop.
	subs := make([]uint32, 0, len(b)+1)
	switch {
	case first < 40:
		subs = append(subs, 0, first)
	case first < 80:
		subs = append(subs, 1, first-40)
	default:
		subs = append(subs, 2, first-80)
	}
	for i := n; i < len(b); {
		v, adv, err := readBase128(b, i)
		if err != nil {
			return OID{}, err
		}
		subs = append(subs, v)
		i += adv
	}
	// subs is built fresh here and never escapes this function except as the
	// OID's storage, so hand it over directly — NewOID's defensive copy would
	// duplicate a slice nobody else can see. This removes the per-varbind OID
	// double-allocation (the densest decode-path alloc site).
	return newValidatedOID(subs)
}

// readBase128 decodes one base-128 (multi-octet, high-bit-continuation)
// sub-identifier from b starting at off, returning the value and the
// number of octets consumed. It rejects an unterminated run and a
// value that overflows uint32 (SNMP sub-identifiers are uint32).
func readBase128(b []byte, off int) (val uint32, consumed int, err error) {
	var acc uint64
	i := off
	for {
		if i >= len(b) {
			return 0, 0, errs.Wrap(errTruncated, "ber: decode OID sub-identifier")
		}
		c := b[i]
		acc = (acc << 7) | uint64(c&0x7f)
		i++
		if acc > math.MaxUint32 {
			return 0, 0, errOIDSubIDOverflow
		}
		if c&0x80 == 0 {
			break
		}
	}
	return uint32(acc), i - off, nil
}

// decodeIPv4 decodes an IpAddress content slice into a 4-byte [net.IP].
// The SMIv2 IpAddress type is IPv4-only and exactly four octets on the
// wire; a 16-octet form is tolerated (lenient) only for forward
// compatibility with mis-encoding agents.
func decodeIPv4(b []byte) (net.IP, error) {
	switch len(b) {
	case 4:
		out := make(net.IP, 4)
		copy(out, b)
		return out, nil
	case 16:
		out := make(net.IP, 16)
		copy(out, b)
		return out, nil
	default:
		return nil, errs.Wrapf(errUnexpectedTag, "ber: IpAddress with %d octets", len(b))
	}
}

// opaqueReal classifies an Opaque content slice that may carry an
// RFC 2856 / Net-SNMP wrapped IEEE-754 real. ok reports whether a
// well-formed 0x9F 0x78 / 0x9F 0x79 marker is present; when ok is true,
// isDouble distinguishes a double-precision (true) from a single-precision
// (false) value, and f is the decoded value. When ok is false the caller
// treats the slice as a plain Opaque. Collapsing the classification into a
// single discriminator makes the impossible "both float and double" state
// unrepresentable.
func opaqueReal(content []byte) (isDouble, ok bool, f float64, err error) {
	if len(content) < 3 || content[0] != opaqueTagPrefix {
		return false, false, 0, nil
	}
	marker := content[1]
	if marker != opaqueFloatTag && marker != opaqueDoubleTag {
		return false, false, 0, nil
	}
	length, lc, err := parseLength(content[2:])
	if err != nil {
		return false, false, 0, err
	}
	payload := content[2+lc:]
	if length > len(payload) {
		return false, false, 0, errs.Wrap(errTruncated, "ber: opaque real payload")
	}
	payload = payload[:length]
	switch marker {
	case opaqueFloatTag:
		if len(payload) != 4 {
			return false, false, 0, errs.Wrapf(errUnexpectedTag, "ber: opaque float with %d octets", len(payload))
		}
		bits := binary.BigEndian.Uint32(payload)
		return false, true, float64(math.Float32frombits(bits)), nil
	default: // opaqueDoubleTag
		if len(payload) != 8 {
			return false, false, 0, errs.Wrapf(errUnexpectedTag, "ber: opaque double with %d octets", len(payload))
		}
		bits := binary.BigEndian.Uint64(payload)
		return true, true, math.Float64frombits(bits), nil
	}
}

//
// Each appendX appends a complete TLV (identifier + length + content) to
// dst and returns the extended slice. Content is encoded minimally so the
// output round-trips through the decoders above; the differential oracle
// compares decoded values, not bytes, so canonical-minimal output is
// sufficient even though some agents emit non-minimal forms.

// appendTLV appends a complete TLV with the given tag and pre-built
// content. It is the shared primitive every other encoder builds on.
func appendTLV(dst []byte, tag byte, content []byte) []byte {
	dst = append(dst, tag)
	dst = appendLength(dst, len(content))
	return append(dst, content...)
}

// appendSequence returns a fresh constructed TLV (SEQUENCE or a
// constructed context tag, e.g. a PDU tag) wrapping the already-encoded
// body.
func appendSequence(tag byte, body []byte) []byte {
	return appendTLV(nil, tag, body)
}

// appendInt appends a signed INTEGER TLV with the minimal two's-complement
// encoding of v to dst and returns the extended slice. The minimal content
// octets are formed in a stack-local array and appended directly, so no
// per-call heap allocation is made for the content slice.
func appendInt(dst []byte, v int64) []byte {
	// Determine the minimal length: strip leading 0x00 (for non-negative)
	// or 0xFF (for negative) octets while preserving the sign bit.
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], uint64(v))
	i := 0
	if v >= 0 {
		for i < 7 && tmp[i] == 0x00 && tmp[i+1]&0x80 == 0 {
			i++
		}
	} else {
		for i < 7 && tmp[i] == 0xff && tmp[i+1]&0x80 != 0 {
			i++
		}
	}
	body := tmp[i:] // aliases tmp; consumed by the append below, never retained
	dst = append(dst, tagInteger)
	dst = appendLength(dst, len(body))
	return append(dst, body...)
}

// appendUint appends an unsigned application-integer TLV under tag with the
// minimal non-negative encoding of v (a leading 0x00 is prepended when the
// high bit would otherwise read as a sign bit). The content octets are
// formed in a stack-local array and appended directly, with no per-call
// heap allocation.
func appendUint(dst []byte, tag byte, v uint64) []byte {
	dst = append(dst, tag)
	if v == 0 {
		dst = appendLength(dst, 1)
		return append(dst, 0x00)
	}
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], v)
	i := 0
	for i < 7 && tmp[i] == 0x00 {
		i++
	}
	body := tmp[i:] // aliases tmp; consumed by the appends below
	if body[0]&0x80 != 0 {
		// Prepend a 0x00 so the high bit does not read as a sign bit.
		dst = appendLength(dst, len(body)+1)
		dst = append(dst, 0x00)
		return append(dst, body...)
	}
	dst = appendLength(dst, len(body))
	return append(dst, body...)
}

// encodeSignedInt returns the minimal two's-complement octets for v. It is
// retained for the round-trip tests; the production encode path uses
// [appendInt], which appends the same octets without an intermediate slice.
func encodeSignedInt(v int64) []byte {
	// appendInt emits [tag, length, content...]; the content of an INTEGER
	// is at most eight octets, so the single length octet is at index 1.
	return appendInt(nil, v)[2:]
}

// encodeUnsigned returns the minimal big-endian octets for v with a leading
// 0x00 when the top octet's high bit is set. Retained for the round-trip
// tests; the production path uses [appendUint].
func encodeUnsigned(v uint64) []byte {
	return appendUint(nil, tagCounter32, v)[2:]
}

// encodeOID returns an OBJECT IDENTIFIER TLV encoding o. The empty OID
// encodes as a zero-length OID TLV (the inverse of [decodeOID]'s
// zero-length sentinel handling).
func encodeOID(o OID) []byte {
	return appendTLV(nil, tagOID, encodeOIDContent(o))
}

// encodeOIDContent returns the BER content octets for o (without the tag
// or length). The first two arcs are folded into a single base-128 value
// per X.690 §8.19.
func encodeOIDContent(o OID) []byte {
	n := o.Len()
	if n == 0 {
		return nil
	}
	var out []byte
	if n == 1 {
		// Degenerate single-arc OID: encode arc*40 (no second arc to fold).
		out = appendBase128(out, uint64(o.At(0))*40)
		return out
	}
	out = appendBase128(out, uint64(o.At(0))*40+uint64(o.At(1)))
	for i := 2; i < n; i++ {
		out = appendBase128(out, uint64(o.At(i)))
	}
	return out
}

// appendBase128 appends v in base-128 (high-bit-continuation) form.
func appendBase128(dst []byte, v uint64) []byte {
	if v == 0 {
		return append(dst, 0x00)
	}
	var tmp [10]byte
	i := len(tmp)
	i--
	tmp[i] = byte(v & 0x7f) // last octet: high bit clear
	v >>= 7
	for v > 0 {
		i--
		tmp[i] = byte(v&0x7f) | 0x80
		v >>= 7
	}
	return append(dst, tmp[i:]...)
}

// appendOctetString appends an OCTET STRING (or any byte-valued tag, e.g.
// Opaque/NsapAddress/BitString) TLV carrying b.
func appendOctetString(dst []byte, tag byte, b []byte) []byte {
	return appendTLV(dst, tag, b)
}

// appendNull appends a NULL TLV (zero-length content).
func appendNull(dst []byte) []byte {
	return appendTLV(dst, tagNull, nil)
}

// appendIPv4 returns a fresh IpAddress TLV. ip is normalized to its
// 4-octet form; a non-IPv4 address is an encode error surfaced to the
// caller.
func appendIPv4(ip net.IP) ([]byte, error) {
	v4 := ip.To4()
	if v4 == nil {
		return nil, errs.Wrapf(errUnexpectedTag, "ber: encode IpAddress %q is not IPv4", ip.String())
	}
	return appendTLV(nil, tagIPAddress, v4), nil
}

// appendOpaqueFloat appends an Opaque TLV wrapping an RFC 2856 single-
// precision real.
func appendOpaqueFloat(dst []byte, f float32) []byte {
	var payload [4]byte
	binary.BigEndian.PutUint32(payload[:], math.Float32bits(f))
	content := []byte{opaqueTagPrefix, opaqueFloatTag, 0x04}
	content = append(content, payload[:]...)
	return appendTLV(dst, tagOpaque, content)
}

// appendOpaqueDouble appends an Opaque TLV wrapping an RFC 2856 double-
// precision real.
func appendOpaqueDouble(dst []byte, f float64) []byte {
	var payload [8]byte
	binary.BigEndian.PutUint64(payload[:], math.Float64bits(f))
	content := []byte{opaqueTagPrefix, opaqueDoubleTag, 0x08}
	content = append(content, payload[:]...)
	return appendTLV(dst, tagOpaque, content)
}
