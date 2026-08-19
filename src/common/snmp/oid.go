package snmp

import (
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// OID is an SNMP object identifier represented as a sequence of sub-identifiers.
//
// The zero value is a valid, empty OID. Methods that return a derived OID
// (Child, Append, Parent, Clone) never share backing storage with the
// receiver, so callers may freely mutate or further derive without aliasing.
type OID struct {
	subs []uint32
}

// maxOIDComponents is the implementation-conformance cap on the number of
// sub-identifiers in a single OID. RFC 3416 §3 requires SNMPv2 implementations
// to support OBJECT IDENTIFIER values with at least 128 sub-identifiers;
// FlowSeer enforces this as the hard upper bound for both [ParseOID] and
// [NewOID]. RFC 2578 §3.5 itself does not specify a count limit on OIDs
// (only that each sub-identifier fits in uint32).
const maxOIDComponents = 128

// NewOID constructs an OID from explicit sub-identifiers and validates
// the same SMIv2 root rules and 128-sub-id cap that [ParseOID] enforces:
//
//   - The first sub-identifier must be 0, 1, or 2 (the CCITT / ISO /
//     Joint-ISO-CCITT root).
//   - When the first sub-identifier is 0 or 1, the second must be in
//     the range [0, 39].
//   - The OID may contain at most 128 sub-identifiers.
//
// The zero-argument form (NewOID()) returns the empty OID and nil; the
// empty OID is a documented zero value used as a "no OID specified"
// sentinel by [OID.HasPrefix], [OID.Parent] and other helpers.
//
// Callers that know their sub-identifiers are statically valid (codegen
// output, test fixtures) should use [MustOID] for the non-fallible form.
//
// The returned OID owns its sub-identifier storage: mutating subs after
// the call does not affect the OID.
func NewOID(subs ...uint32) (OID, error) {
	if len(subs) == 0 {
		return OID{}, nil
	}
	if err := validateSubs(subs); err != nil {
		return OID{}, err
	}
	cp := make([]uint32, len(subs))
	copy(cp, subs)
	return OID{subs: cp}, nil
}

// MustOID is the panicking companion to [NewOID], intended for codegen
// output and test fixtures where the sub-identifiers are statically
// known to be valid. A validation failure panics with the [NewOID] error
// — matching the [regexp.MustCompile] and [netip.MustParseAddr] pattern.
//
// MustOID surfaces a generator-bug-produced invalid OID at process start
// (when the generated package's var-block initialiser runs) rather than
// as a wire-protocol error in a later Backend call.
func MustOID(subs ...uint32) OID {
	o, err := NewOID(subs...)
	if err != nil {
		panic(err)
	}
	return o
}

// newValidatedOID validates subs against the shared SMIv2 rules and adopts
// the slice as the OID's backing storage WITHOUT the defensive copy [NewOID]
// makes. The caller must hand over a slice it neither retains nor mutates
// afterwards. The wire decoder (decodeOID) is the sole caller: it builds a
// fresh sub-identifier slice per OID and discards its own reference
// immediately, so NewOID's copy would be pure waste. A spare-capacity slice
// is safe here because every derived-OID method (Child/Append/Parent/Clone)
// allocates and copies rather than appending into the receiver's capacity.
func newValidatedOID(subs []uint32) (OID, error) {
	if len(subs) == 0 {
		return OID{}, nil
	}
	if err := validateSubs(subs); err != nil {
		return OID{}, err
	}
	return OID{subs: subs}, nil
}

// validateSubs enforces the SMIv2 root rules and 128-sub-id cap shared
// between [ParseOID] and [NewOID]. Callers must not pass an empty slice;
// both construction paths short-circuit the empty case before calling.
func validateSubs(subs []uint32) error {
	if len(subs) > maxOIDComponents {
		return errs.New().Attr("count", len(subs)).Attr("max", maxOIDComponents).
			Msg("sub-identifier count exceeds SMIv2 maximum")
	}
	if subs[0] > 2 {
		return errs.New().Attr("got", subs[0]).
			Msg("first sub-identifier must be 0, 1, or 2 per SMIv2 (index 0)")
	}
	if len(subs) >= 2 && subs[0] <= 1 && subs[1] > 39 {
		return errs.New().Attr("first", subs[0]).Attr("got", subs[1]).
			Msg("second sub-identifier must be in [0, 39] when first is 0 or 1 (index 1)")
	}
	return nil
}

// ParseOID parses a dotted OID string such as "1.3.6.1.2.1" into an [OID].
//
// A single optional leading dot is accepted. Each component must be a
// non-negative decimal integer that fits in uint32. The empty string, a
// bare ".", trailing dots, and empty components ("1..3") are rejected.
//
// SMIv2 root constraints (see [NewOID]) are enforced via the same
// validation helper, so the two construction paths cannot drift apart.
func ParseOID(s string) (OID, error) {
	if s == "" {
		return OID{}, errs.Msg("empty OID string")
	}
	// Leading dot is optional: ".1.3" and "1.3" both denote
	// the same OID. Strip exactly one to keep the rest of the parser
	// uniform; a second leading dot would yield an empty first component
	// and trip the per-component check below.
	if s[0] == '.' {
		s = s[1:]
	}
	if s == "" {
		return OID{}, errs.Msg("OID string contained only a leading dot")
	}

	parts := strings.Split(s, ".")
	subs := make([]uint32, len(parts))
	for i, p := range parts {
		if p == "" {
			return OID{}, errs.New().Attr("oid", s).Msg("empty sub-identifier in OID")
		}
		// strconv.ParseUint with bitSize=32 rejects values exceeding
		// math.MaxUint32 and also rejects leading '+' or '-'.
		v, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return OID{}, errs.Wrapf(err, "invalid OID %q: sub-identifier %q", s, p)
		}
		subs[i] = uint32(v)
	}
	if err := validateSubs(subs); err != nil {
		return OID{}, errs.Wrapf(err, "invalid OID %q", s)
	}
	return OID{subs: subs}, nil
}

// WireBytes returns the BER OBJECT IDENTIFIER content octets encoding o
// (no tag or length header). The empty OID yields a nil slice. The
// returned slice is freshly allocated on every call.
func (o OID) WireBytes() []byte {
	return encodeOIDContent(o)
}

// WireKey returns [OID.WireBytes] as an immutable string, suitable as a
// map key. Because the canonical BER content encoding is bijective and
// order-preserving, WireKey equality is OID equality — and a lookup
// with a raw wire-OID byte slice (map[string(b)]) is allocation-free.
// Generated MIB packages key their dispatch and tier maps on WireKey
// so raw-walk consumers can look columns up without formatting.
func (o OID) WireKey() string {
	return string(encodeOIDContent(o))
}

// String returns the dotted form of the OID without a leading dot. The empty
// OID renders as the empty string.
func (o OID) String() string {
	if len(o.subs) == 0 {
		return ""
	}
	var b strings.Builder
	// Rough upper bound: ten digits per uint32 plus the joining dot.
	b.Grow(len(o.subs) * 4)
	for i, v := range o.subs {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(strconv.FormatUint(uint64(v), 10))
	}
	return b.String()
}

// Equal reports whether o and other have the same length and identical
// sub-identifiers at every position.
func (o OID) Equal(other OID) bool {
	if len(o.subs) != len(other.subs) {
		return false
	}
	for i, v := range o.subs {
		if v != other.subs[i] {
			return false
		}
	}
	return true
}

// Compare returns -1, 0, or +1 as o is lexicographically less than, equal
// to, or greater than other — the ordering SNMP walks advance along.
func (o OID) Compare(other OID) int {
	na, nb := len(o.subs), len(other.subs)
	n := na
	if nb < n {
		n = nb
	}
	for i := 0; i < n; i++ {
		av, bv := o.subs[i], other.subs[i]
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	switch {
	case na < nb:
		return -1
	case na > nb:
		return 1
	default:
		return 0
	}
}

// HasPrefix reports whether prefix is a prefix of o (every sub-identifier of
// prefix matches the corresponding sub-identifier of o). The empty OID is a
// prefix of every OID, and every OID is a prefix of itself.
func (o OID) HasPrefix(prefix OID) bool {
	if len(prefix.subs) > len(o.subs) {
		return false
	}
	for i, v := range prefix.subs {
		if o.subs[i] != v {
			return false
		}
	}
	return true
}

// Parent returns the OID with the last sub-identifier removed. The boolean
// is false when there is no parent (empty OID or single-element root) — using
// a boolean rather than a sentinel keeps callers from needing to compare
// against a magic value.
func (o OID) Parent() (OID, bool) {
	if len(o.subs) < 2 {
		return OID{}, false
	}
	parent := make([]uint32, len(o.subs)-1)
	copy(parent, o.subs[:len(o.subs)-1])
	return OID{subs: parent}, true
}

// Child returns a new OID with sub appended to o. The receiver is not
// modified.
func (o OID) Child(sub uint32) OID {
	out := make([]uint32, len(o.subs)+1)
	copy(out, o.subs)
	out[len(o.subs)] = sub
	return OID{subs: out}
}

// Append returns a new OID with the given sub-identifiers appended. The
// receiver is not modified.
func (o OID) Append(subs ...uint32) OID {
	out := make([]uint32, len(o.subs)+len(subs))
	copy(out, o.subs)
	copy(out[len(o.subs):], subs)
	return OID{subs: out}
}

// Len returns the number of sub-identifiers in the OID.
func (o OID) Len() int {
	return len(o.subs)
}

// At returns the sub-identifier at index i. It panics if i is out of range,
// matching the semantics of indexing a slice. See also [OID.Len] for the
// bounds check callers should perform before indexing into an OID whose
// length they have not otherwise verified.
func (o OID) At(i int) uint32 {
	return o.subs[i]
}

// Clone returns a deep copy of the OID. Use Clone when handing an OID to
// code that may mutate its backing storage or when storing an OID in a
// long-lived map/cache where aliasing would be a hazard.
func (o OID) Clone() OID {
	if len(o.subs) == 0 {
		return OID{}
	}
	cp := make([]uint32, len(o.subs))
	copy(cp, o.subs)
	return OID{subs: cp}
}
