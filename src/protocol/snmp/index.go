package snmp

import (
	"math"
	"net/netip"
)

// IndexKind names how one INDEX part of a conceptual row is spelled in
// the instance OID suffix, per RFC 2578 §7.7.
type IndexKind uint8

// Index part kinds. A "length" arc counts the arcs that follow it; an
// IMPLIED part has no length arc and runs to the end of the suffix, so it
// can only be the last part.
const (
	// IndexInteger is one arc carrying a non-negative integer value.
	// Integer32 and Unsigned32 indexes both use it; the arc is delivered
	// as uint32 and the caller narrows it.
	IndexInteger IndexKind = iota
	// IndexFixedOctets is an OCTET STRING of exactly [IndexShape.Length]
	// octets, one arc each, with no length arc.
	IndexFixedOctets
	// IndexLengthPrefixedOctets is a length arc followed by that many
	// octet arcs.
	IndexLengthPrefixedOctets
	// IndexImpliedOctets is an IMPLIED OCTET STRING: every remaining arc
	// is one octet.
	IndexImpliedOctets
	// IndexFixedOID is an OBJECT IDENTIFIER of exactly
	// [IndexShape.Length] arcs with no length arc.
	IndexFixedOID
	// IndexLengthPrefixedOID is a length arc followed by that many OID
	// arcs.
	IndexLengthPrefixedOID
	// IndexImpliedOID is an IMPLIED OBJECT IDENTIFIER: every remaining
	// arc belongs to it.
	IndexImpliedOID
	// IndexIPv4 is an IpAddress: four arcs, one per octet.
	IndexIPv4
)

// IndexShape describes one INDEX part. Length is read only for
// [IndexFixedOctets] and [IndexFixedOID]; other kinds ignore it.
type IndexShape struct {
	Kind   IndexKind
	Length int
}

// IndexValue is one decoded INDEX part. Exactly the field matching Kind
// is set: Integer for [IndexInteger], Octets for the three octet kinds,
// OID for the three OID kinds, and Addr for [IndexIPv4]. Octets is nil
// for an empty string; OID and Addr are zero for an empty part.
type IndexValue struct {
	Kind    IndexKind
	Integer uint32
	Octets  []byte
	OID     OID
	Addr    netip.Addr
}

// DecodeIndex reads the row-index suffix of an instance OID (the arcs
// after the column OID, as a generated walker yields them) against the
// row's INDEX shapes in order.
//
// The returned slice always has one entry per shape. ok is true only when
// every part decoded and the suffix was consumed exactly. It is false,
// never an error, when the suffix runs short, a length arc exceeds the
// remaining arcs, an arc is out of range for its kind (an octet or IPv4
// arc above 255, or an unknown kind), or arcs remain after the last part.
// On a false result the parts before the failing one hold their decoded
// values and the failing part and every later part are zero. A malformed
// index is a fact about one row, so the caller keeps delivering the row
// with a zero key instead of ending the walk; an error here would cost
// every later row of the table.
func DecodeIndex(suffix OID, shapes []IndexShape) (parts []IndexValue, ok bool) {
	parts = make([]IndexValue, len(shapes))
	arcs := suffix.subs
	for i, shape := range shapes {
		var n int
		switch shape.Kind {
		case IndexInteger:
			n = 1
		case IndexIPv4:
			n = 4
		case IndexFixedOctets, IndexFixedOID:
			n = shape.Length
		case IndexLengthPrefixedOctets, IndexLengthPrefixedOID:
			if len(arcs) == 0 || uint64(arcs[0]) > uint64(len(arcs)-1) {
				return parts, false
			}
			n = int(arcs[0])
			arcs = arcs[1:]
		case IndexImpliedOctets, IndexImpliedOID:
			n = len(arcs)
		default:
			return parts, false
		}
		if n < 0 || n > len(arcs) {
			return parts, false
		}
		part, valid := decodeIndexPart(shape.Kind, arcs[:n])
		if !valid {
			return parts, false
		}
		parts[i] = part
		arcs = arcs[n:]
	}
	return parts, len(arcs) == 0
}

func decodeIndexPart(kind IndexKind, arcs []uint32) (IndexValue, bool) {
	v := IndexValue{Kind: kind}
	switch kind {
	case IndexInteger:
		v.Integer = arcs[0]
	case IndexIPv4:
		var b [4]byte
		for i, arc := range arcs {
			if arc > math.MaxUint8 {
				return IndexValue{}, false
			}
			b[i] = byte(arc)
		}
		v.Addr = netip.AddrFrom4(b)
	case IndexFixedOctets, IndexLengthPrefixedOctets, IndexImpliedOctets:
		if len(arcs) == 0 {
			break
		}
		v.Octets = make([]byte, len(arcs))
		for i, arc := range arcs {
			if arc > math.MaxUint8 {
				return IndexValue{}, false
			}
			v.Octets[i] = byte(arc)
		}
	case IndexFixedOID, IndexLengthPrefixedOID, IndexImpliedOID:
		// An index OID is a bare arc sequence, so the SMIv2 root rules
		// NewOID enforces do not apply to it.
		if len(arcs) > 0 {
			v.OID = OID{subs: append([]uint32(nil), arcs...)}
		}
	}
	return v, true
}
