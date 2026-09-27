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

func TestDecodeColumn_FusedSuccessBypassesGenericDecode(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10)
	var genericCalled bool
	col := NewFusedTableColumn(
		oid,
		KindInteger32,
		func(_ VarBind) (int32, error) {
			genericCalled = true
			return 999, nil
		},
		func(_ RawVarBind) (int32, bool) {
			return 42, true
		},
		3,
	)
	var dst int32
	observed := make([]uint64, 1)
	rv := RawVarBind{Tag: tagInteger, Value: []byte{42}}
	if err := DecodeColumn(rv, col, &dst, observed); err != nil {
		t.Fatalf("DecodeColumn failed: %v", err)
	}
	if genericCalled {
		t.Error("generic decoder was called, want fused fast path to bypass it")
	}
	if dst != 42 {
		t.Errorf("dst = %d, want 42", dst)
	}
	if observed[0] != (1 << 3) {
		t.Errorf("observed = %x, want %x", observed[0], 1<<3)
	}
}

func TestDecodeColumn_DeclineWrongTagUsesGenericFallback(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10)
	var genericCalled bool
	col := NewFusedTableColumn(
		oid,
		KindInteger32,
		func(vb VarBind) (int32, error) {
			genericCalled = true
			uv, ok := vb.(Gauge32Var)
			if !ok {
				return 0, ErrTypeMismatch
			}
			return int32(uv.Value), nil
		},
		RawInteger32,
		2,
	)
	var dst int32
	observed := make([]uint64, 1)
	rv := RawVarBind{OID: oid.WireBytes(), Tag: tagGauge32, Value: []byte{77}}
	if err := DecodeColumn(rv, col, &dst, observed); err != nil {
		t.Fatalf("DecodeColumn: %v", err)
	}
	if !genericCalled {
		t.Error("generic decoder was not called on fused decline for wrong tag")
	}
	if dst != 77 {
		t.Errorf("dst = %d, want 77", dst)
	}
	if observed[0] != (1 << 2) {
		t.Errorf("observed = %x, want %x", observed[0], 1<<2)
	}
}

func TestDecodeColumn_DeclinePredecodedVBUsesGenericFallback(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10)
	var genericCalled bool
	col := NewFusedTableColumn(
		oid,
		KindInteger32,
		func(vb VarBind) (int32, error) {
			genericCalled = true
			iv, ok := vb.(Integer32Var)
			if !ok {
				return 0, ErrTypeMismatch
			}
			return iv.Value, nil
		},
		RawInteger32,
		1,
	)
	var dst int32
	observed := make([]uint64, 1)
	rv := RawVarBind{VB: Integer32Var{Header: Header{OID: oid, Kind: KindInteger32}, Value: 55}}
	if err := DecodeColumn(rv, col, &dst, observed); err != nil {
		t.Fatalf("DecodeColumn: %v", err)
	}
	if !genericCalled {
		t.Error("generic decoder was not called on pre-decoded VB")
	}
	if dst != 55 {
		t.Errorf("dst = %d, want 55", dst)
	}
	if observed[0] != (1 << 1) {
		t.Errorf("observed = %x, want %x", observed[0], 1<<1)
	}
}

func TestDecodeColumn_FailurePreservesDestinationAndPresence(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10)
	boom := errors.New("cannot decode")
	col := NewFusedTableColumn(
		oid,
		KindInteger32,
		func(_ VarBind) (int32, error) {
			return 0, boom
		},
		func(_ RawVarBind) (int32, bool) {
			return 0, false
		},
		4,
	)
	dst := int32(123)
	observed := []uint64{0}
	rv := RawVarBind{OID: oid.WireBytes(), Tag: tagInteger, Value: []byte{42}}
	err := DecodeColumn(rv, col, &dst, observed)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if dst != 123 {
		t.Errorf("dst modified on failure: got %d, want 123", dst)
	}
	if observed[0] != 0 {
		t.Errorf("observed modified on failure: got %x, want 0", observed[0])
	}
}

type testStatus int32

const (
	testStatusUp   testStatus = 1
	testStatusDown testStatus = 2
)

func TestDecodeColumn_NamedInteger32AdapterPreservesType(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 8)
	col := NewFusedTableColumn(
		oid,
		KindInteger32,
		func(vb VarBind) (testStatus, error) {
			iv, ok := vb.(Integer32Var)
			if !ok {
				return 0, ErrTypeMismatch
			}
			return testStatus(iv.Value), nil
		},
		RawInteger32As[testStatus],
		0,
	)
	var status testStatus
	observed := make([]uint64, 1)
	rv := RawVarBind{Tag: tagInteger, Value: []byte{1}}
	if err := DecodeColumn(rv, col, &status, observed); err != nil {
		t.Fatalf("DecodeColumn: %v", err)
	}
	if status != testStatusUp {
		t.Errorf("status = %v, want testStatusUp", status)
	}
	if observed[0] != 1 {
		t.Errorf("observed = %x, want 1", observed[0])
	}
}

func TestDecodeColumn_ZeroAllocationsOnSuccess(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10)
	col := NewFusedTableColumn(
		oid,
		KindInteger32,
		func(_ VarBind) (int32, error) { return 0, nil },
		RawInteger32,
		0,
	)
	rv := RawVarBind{Tag: tagInteger, Value: []byte{42}}
	var dst int32
	observed := make([]uint64, 1)
	allocs := testing.AllocsPerRun(1000, func() {
		_ = DecodeColumn(rv, col, &dst, observed)
	})
	if allocs != 0 {
		t.Errorf("DecodeColumn allocated %v times, want 0", allocs)
	}
}

func TestColumnObserved(t *testing.T) {
	oidA := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 1)
	oidB := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 2)
	oidForeign := MustOID(1, 3, 6, 1, 4, 1, 9, 9)

	boundColA := NewTableColumn[int32](oidA, KindInteger32, nil, 0)
	boundColB := NewTableColumn[string](oidB, KindOctetString, nil, 1)
	boundColOutOfRange := NewTableColumn[int32](MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 99), KindInteger32, nil, 128)
	boundColNeg := NewTableColumn[int32](MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 98), KindInteger32, nil, -1)

	cols := []AnyColumn{boundColA, boundColB, boundColOutOfRange, boundColNeg}

	observed := []uint64{1}

	if !ColumnObserved(observed, cols, boundColA) {
		t.Error("boundColA with reported zero was not observed")
	}

	unboundA := NewColumn[int32](oidA, KindInteger32, nil)
	if !ColumnObserved(observed, cols, unboundA) {
		t.Error("unboundA with same OID was not observed")
	}

	if ColumnObserved(observed, cols, boundColB) {
		t.Error("boundColB was observed, want false")
	}
	unboundB := NewColumn[string](oidB, KindOctetString, nil)
	if ColumnObserved(observed, cols, unboundB) {
		t.Error("unboundB was observed, want false")
	}

	foreignCol := NewColumn[int32](oidForeign, KindInteger32, nil)
	if ColumnObserved(observed, cols, foreignCol) {
		t.Error("foreignCol was observed, want false")
	}

	if ColumnObserved(observed, cols, boundColOutOfRange) {
		t.Error("boundColOutOfRange was observed, want false")
	}
	if ColumnObserved(observed, cols, boundColNeg) {
		t.Error("boundColNeg was observed, want false")
	}

	if ColumnObserved(observed, cols, nil) {
		t.Error("nil col was observed, want false")
	}
}
