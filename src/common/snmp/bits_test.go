package snmp

import (
	"errors"
	"testing"
)

// bitsVarBind builds an OctetString VarBind carrying the supplied BITS
// octets. BITS travels as an OCTET STRING on the wire (RFC 2578 §7.1.4).
func bitsVarBind(octets ...byte) VarBind {
	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	return OctetStringVar{Header: Header{OID: oid, Kind: KindOctetString}, Value: octets}
}

// TestDecodeBitSet_TwoOctets pins the RFC 2578 §7.1.4 bit numbering:
// bit 0 is the most significant bit of the first octet, and the set
// carries positions the caller has no name for just as faithfully as
// the named ones.
func TestDecodeBitSet_TwoOctets(t *testing.T) {
	// 0x24 = 0010 0100 -> bits 2 and 5; 0x01 -> bit 15.
	got, err := DecodeBitSet(bitsVarBind(0x24, 0x01))
	if err != nil {
		t.Fatalf("DecodeBitSet: %v", err)
	}
	want := []BitPos{2, 5, 15}
	positions := got.Positions()
	if len(positions) != len(want) {
		t.Fatalf("Positions() = %v, want %v", positions, want)
	}
	for i, p := range want {
		if positions[i] != p {
			t.Fatalf("Positions() = %v, want %v", positions, want)
		}
	}
	for _, p := range want {
		if !got.Has(p) {
			t.Errorf("Has(%d) = false, want true", p)
		}
	}
	for _, p := range []BitPos{0, 1, 3, 4, 6, 7, 8, 14, 16, 1000} {
		if got.Has(p) {
			t.Errorf("Has(%d) = true, want false", p)
		}
	}
	if got.Count() != 3 {
		t.Errorf("Count() = %d, want 3", got.Count())
	}
	if got.Empty() {
		t.Error("Empty() = true on a three-bit set")
	}
}

// TestDecodeBitSet_Empty covers the "no capabilities" encoding: an
// empty (or all-zero) OCTET STRING is the empty set, not the set
// containing bit zero.
func TestDecodeBitSet_Empty(t *testing.T) {
	for name, vb := range map[string]VarBind{
		"zero-length": bitsVarBind(),
		"nil-value":   OctetStringVar{Header: Header{OID: MustOID(1, 3, 6, 1), Kind: KindOctetString}},
		"all-zero":    bitsVarBind(0x00, 0x00),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeBitSet(vb)
			if err != nil {
				t.Fatalf("DecodeBitSet: %v", err)
			}
			if got.Has(0) {
				t.Error("Has(0) = true on an empty BITS value")
			}
			if !got.Empty() {
				t.Errorf("Empty() = false, want true (positions %v)", got.Positions())
			}
			if got.Count() != 0 {
				t.Errorf("Count() = %d, want 0", got.Count())
			}
			if len(got.Positions()) != 0 {
				t.Errorf("Positions() = %v, want empty", got.Positions())
			}
		})
	}
}

// TestDecodeBitSet_ShortValueDeclines covers the malformed-input
// contract: a value the agent did not send as an OCTET STRING, and an
// exception marker, both decline with an error rather than panicking,
// matching every other decoder in this package.
func TestDecodeBitSet_ShortValueDeclines(t *testing.T) {
	oid := MustOID(1, 3, 6, 1)
	cases := map[string]VarBind{
		"integer-valued": Integer32Var{Header: Header{OID: oid, Kind: KindInteger32}, Value: 5},
		"no-such-object": NoSuchObjectVar{Header: Header{OID: oid, Kind: KindNoSuchObject}},
	}
	for name, vb := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeBitSet(vb)
			if err == nil {
				t.Fatalf("DecodeBitSet(%s) = %v, want error", name, got)
			}
			if !got.Empty() {
				t.Errorf("declined decode returned a non-empty set: %v", got.Positions())
			}
		})
	}

	// A truncated value — fewer octets than the MIB's named bits need —
	// is legal SNMP: the missing bits read as unset.
	short, err := DecodeBitSet(bitsVarBind(0x40))
	if err != nil {
		t.Fatalf("DecodeBitSet(short): %v", err)
	}
	if !short.Has(1) {
		t.Error("Has(1) = false on 0x40")
	}
	if short.Has(9) {
		t.Error("Has(9) = true past the end of the value")
	}
}

// TestDecodeBitSet_ErrorClass keeps the decoder's failures inside the
// package's sentinel classes so callers classify them the same way they
// classify every other column decode.
func TestDecodeBitSet_ErrorClass(t *testing.T) {
	oid := MustOID(1, 3, 6, 1)
	if _, err := DecodeBitSet(Integer32Var{Header: Header{OID: oid, Kind: KindInteger32}}); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("non-OctetString error = %v, want ErrTypeMismatch", err)
	}
	if _, err := DecodeBitSet(NoSuchObjectVar{Header: Header{OID: oid, Kind: KindNoSuchObject}}); !errors.Is(err, ErrException) {
		t.Errorf("exception error = %v, want ErrException", err)
	}
}

// TestBitSet_EqualIgnoresTrailingZeroOctets pins set semantics over
// wire semantics: agents pad BITS values to different widths, and two
// values with the same set bits are the same set.
func TestBitSet_EqualIgnoresTrailingZeroOctets(t *testing.T) {
	a, err := DecodeBitSet(bitsVarBind(0x24))
	if err != nil {
		t.Fatalf("DecodeBitSet: %v", err)
	}
	b, err := DecodeBitSet(bitsVarBind(0x24, 0x00, 0x00))
	if err != nil {
		t.Fatalf("DecodeBitSet: %v", err)
	}
	if !a.Equal(b) {
		t.Errorf("%v != %v, want equal", a, b)
	}
	c, err := DecodeBitSet(bitsVarBind(0x24, 0x01))
	if err != nil {
		t.Fatalf("DecodeBitSet: %v", err)
	}
	if a.Equal(c) {
		t.Errorf("%v == %v, want unequal", a, c)
	}
}

// TestNewBitSet round-trips the constructor against the decoder so
// tests and mappers can build expected values without hand-encoding
// octets.
func TestNewBitSet(t *testing.T) {
	got := NewBitSet(2, 5, 15)
	want, err := DecodeBitSet(bitsVarBind(0x24, 0x01))
	if err != nil {
		t.Fatalf("DecodeBitSet: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("NewBitSet(2,5,15) = %v, want %v", got, want)
	}
	if got.String() != "{2 5 15}" {
		t.Errorf("String() = %q, want %q", got.String(), "{2 5 15}")
	}
	if NewBitSet().String() != "{}" {
		t.Errorf("empty String() = %q, want %q", NewBitSet().String(), "{}")
	}
}

func TestDecodeBitSet_OversizedTruncates(t *testing.T) {
	// An agent answering a two-octet BITS object with kilobytes of set
	// bits would otherwise become one enum value per set position in
	// every message built from it. Declining instead of truncating would
	// cost the caller the whole varbind, and through it the whole row.
	huge := make([]byte, MaxBitSetOctets+8)
	for i := range huge {
		huge[i] = 0xFF
	}
	got, err := DecodeBitSet(bitsVarBind(huge...))
	if err != nil {
		t.Fatalf("DecodeBitSet(%d octets) error = %v, want the value truncated", len(huge), err)
	}
	if want := MaxBitSetOctets * 8; got.Count() != want {
		t.Errorf("Count() = %d, want %d", got.Count(), want)
	}
	if past := BitPos(MaxBitSetOctets * 8); got.Has(past) {
		t.Errorf("Has(%d) = true, want the positions past the bound dropped", past)
	}

	atLimit := make([]byte, MaxBitSetOctets)
	atLimit[MaxBitSetOctets-1] = 0x01
	got, err = DecodeBitSet(bitsVarBind(atLimit...))
	if err != nil {
		t.Fatalf("DecodeBitSet at the limit: %v", err)
	}
	if last := BitPos(MaxBitSetOctets*8 - 1); !got.Has(last) {
		t.Errorf("Has(%d) = false, want true", last)
	}
}
