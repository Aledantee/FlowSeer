package snmp

import (
	"errors"
	"testing"
)

func TestNewColumn_AccessorsRoundTrip(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1.2.1.2.2.1.2")
	dec := func(vb VarBind) (string, error) {
		os, ok := vb.(OctetStringVar)
		if !ok {
			return "", ErrTypeMismatch
		}
		return string(os.Value), nil
	}
	c := NewColumn[string](oid, KindOctetString, dec)
	if !c.OID().Equal(oid) {
		t.Errorf("OID() = %q, want %q", c.OID(), oid)
	}
	if c.Kind() != KindOctetString {
		t.Errorf("Kind() = %v, want KindOctetString", c.Kind())
	}
}

func TestColumn_SatisfiesAnyColumn(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1")
	c := NewColumn[int32](oid, KindInteger32, func(vb VarBind) (int32, error) {
		iv, ok := vb.(Integer32Var)
		if !ok {
			return 0, ErrTypeMismatch
		}
		return iv.Value, nil
	})
	var ac AnyColumn = c
	if !ac.OID().Equal(oid) {
		t.Error("AnyColumn.OID() mismatch")
	}
	if ac.Kind() != KindInteger32 {
		t.Errorf("AnyColumn.Kind() = %v, want KindInteger32", ac.Kind())
	}
}

func TestColumn_Decode(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1.2.1.2.2.1.10")
	c := NewColumn[uint32](oid, KindCounter32, func(vb VarBind) (uint32, error) {
		cv, ok := vb.(Counter32Var)
		if !ok {
			return 0, ErrTypeMismatch
		}
		return cv.Value, nil
	})

	got, err := c.Decode(Counter32Var{Header: Header{OID: oid, Kind: KindCounter32}, Value: 42})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != 42 {
		t.Errorf("Decode = %d, want 42", got)
	}

	if _, err := c.Decode(NullVar{Header: Header{OID: oid, Kind: KindNull}}); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("Decode of wrong variant = %v, want ErrTypeMismatch", err)
	}
}

func TestColumn_DecodeNilDecoder(t *testing.T) {
	// A composite literal sets decode=nil; Decode should fail closed.
	var c Column[int]
	if _, err := c.Decode(NullVar{}); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("nil-decoder Decode = %v, want ErrTypeMismatch", err)
	}
}

func TestBindRow_NilWalkerYieldsZero(t *testing.T) {
	// BindRow against a nil Walker is a documented zero-yield case so
	// generated table walkers can compose against a missing source
	// without panicking.
	type row struct{}
	seq := BindRow[row](nil, func(_ OID, _ VarBind, _ *row) error { return nil })
	count := 0
	for range seq {
		count++
	}
	if count != 0 {
		t.Errorf("BindRow over nil Walker yielded %d rows, want 0", count)
	}
}
