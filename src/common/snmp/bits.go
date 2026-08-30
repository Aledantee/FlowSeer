package snmp

import (
	"strconv"
	"strings"
)

// BitPos is a zero-based bit position inside an SMIv2 BITS value.
// Generated MIB packages declare one named constant per bit the MIB
// gives a name to; positions with no name are ordinary BitPos values
// and travel through [BitSet] unchanged.
type BitPos uint32

// BitSet is the decoded form of an SMIv2 BITS value: the set of bit
// positions the agent reported as set.
//
// BITS is a set type, not a number. lldpRemSysCapEnabled on a device
// that both bridges and routes has two bits set at once, so a decoder
// that produced a single integer could not represent the answer at all.
// The wire form is an OCTET STRING whose bit 0 is the most significant
// bit of the first octet (RFC 2578 §7.1.4), and agents pad it to
// whatever width they like — so the set, not the octet string, is the
// meaningful value, and [BitSet.Equal] compares sets.
//
// The zero BitSet is the empty set and is safe to use. Values are
// immutable once constructed; copying one is a shallow copy that shares
// the (never-written) backing octets.
type BitSet struct {
	// octets is the wire form with trailing all-zero octets trimmed, so
	// two agents' different paddings of the same set compare equal by
	// bytes and hash to the same string key.
	octets []byte
}

// NewBitSet builds the set containing exactly the supplied positions.
// Duplicates are collapsed; order does not matter.
func NewBitSet(positions ...BitPos) BitSet {
	if len(positions) == 0 {
		return BitSet{}
	}
	var highest BitPos
	for _, p := range positions {
		if p > highest {
			highest = p
		}
	}
	octets := make([]byte, highest/8+1)
	for _, p := range positions {
		octets[p/8] |= 0x80 >> (p % 8)
	}
	return BitSet{octets: trimTrailingZeros(octets)}
}

// MaxBitSetOctets bounds how much of a BITS value [DecodeBitSet] keeps.
// No SMIv2 BITS definition in practice names more than a few dozen
// positions, so a larger answer is a broken or hostile agent rather than
// a wide enumeration. The bound matters because each set position becomes
// a value in the consuming message: an unbounded answer of set bits turns
// one varbind into hundreds of thousands of entries.
const MaxBitSetOctets = 32

// DecodeBitSet decodes an SMIv2 BITS value from its OCTET STRING wire
// form. Exception variants surface as a wrapped [ErrException] and
// non-OctetString variants as a wrapped [ErrTypeMismatch]; a value
// shorter than the MIB's named bits is legal SNMP, and its missing bits
// simply read unset.
//
// A value longer than [MaxBitSetOctets] is truncated to the bound rather
// than declined: no MIB here names a position past it, so nothing
// meaningful is lost, and an error would cost far more than the octets
// do. A column decode error ends the whole table walk, so declining one
// malformed capability bitmap on one row would void every row of the
// table — and with it every fact the caller was collecting.
func DecodeBitSet(vb VarBind) (BitSet, error) {
	raw, err := DecodeBITS(vb)
	if err != nil {
		return BitSet{}, err
	}

	if len(raw) > MaxBitSetOctets {
		raw = raw[:MaxBitSetOctets]
	}

	return BitSet{octets: trimTrailingZeros(raw)}, nil
}

// Has reports whether position p is set. Positions past the end of the
// decoded value read false, which is what a truncated agent answer
// means.
func (s BitSet) Has(p BitPos) bool {
	i := int(p / 8)
	if i >= len(s.octets) {
		return false
	}
	return s.octets[i]&(0x80>>(p%8)) != 0
}

// Positions returns the set bit positions in ascending order. The
// result is freshly allocated; callers may keep or mutate it.
func (s BitSet) Positions() []BitPos {
	out := make([]BitPos, 0, 8)
	for i, o := range s.octets {
		for b := range 8 {
			if o&(0x80>>b) != 0 {
				out = append(out, BitPos(i*8+b))
			}
		}
	}
	return out
}

// Count returns the number of set bits.
func (s BitSet) Count() int {
	n := 0
	for _, o := range s.octets {
		for b := range 8 {
			if o&(0x80>>b) != 0 {
				n++
			}
		}
	}
	return n
}

// Empty reports whether no bit is set.
func (s BitSet) Empty() bool { return len(s.octets) == 0 }

// Octets returns the value's wire octets with trailing zero octets
// trimmed, as a defensive copy.
func (s BitSet) Octets() []byte {
	out := make([]byte, len(s.octets))
	copy(out, s.octets)
	return out
}

// Equal reports whether s and other contain the same positions.
// Padding width is not part of the comparison.
func (s BitSet) Equal(other BitSet) bool {
	if len(s.octets) != len(other.octets) {
		return false
	}
	for i := range s.octets {
		if s.octets[i] != other.octets[i] {
			return false
		}
	}
	return true
}

// String renders the set as its ascending positions, e.g. "{2 5 15}".
func (s BitSet) String() string {
	var b strings.Builder
	b.WriteByte('{')
	for i, p := range s.Positions() {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strconv.FormatUint(uint64(p), 10))
	}
	b.WriteByte('}')
	return b.String()
}

// trimTrailingZeros drops the all-zero tail of a BITS wire value so
// that padding width never affects equality or emptiness.
func trimTrailingZeros(b []byte) []byte {
	end := len(b)
	for end > 0 && b[end-1] == 0 {
		end--
	}
	if end == 0 {
		return nil
	}
	return b[:end]
}
