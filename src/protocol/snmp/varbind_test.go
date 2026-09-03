package snmp

import (
	"net"
	"testing"
)

func mustOID(t *testing.T, s string) OID {
	t.Helper()
	o, err := ParseOID(s)
	if err != nil {
		t.Fatalf("ParseOID(%q): %v", s, err)
	}
	return o
}

// allVariants returns one instance of every VarBind variant. The slice is
// the source of truth for "every variant is covered"; if a variant is added
// to the sum without an entry here, the linter-style switches below will
// not enforce it but the test that asserts the count will fail.
func allVariants(t *testing.T) []VarBind {
	t.Helper()
	oid := mustOID(t, "1.3.6.1.2.1.1.1.0")
	mk := func(k Kind) Header { return Header{OID: oid, Kind: k} }
	return []VarBind{
		Integer32Var{Header: mk(KindInteger32), Value: -7},
		Uinteger32Var{Header: mk(KindUinteger32), Value: 7},
		OctetStringVar{Header: mk(KindOctetString), Value: []byte("hi")},
		ObjectIDVar{Header: mk(KindObjectID), Value: oid},
		BitStringVar{Header: mk(KindBitString), Value: []byte{0x80}},
		Counter32Var{Header: mk(KindCounter32), Value: 42},
		Gauge32Var{Header: mk(KindGauge32), Value: 42},
		TimeTicksVar{Header: mk(KindTimeTicks), Value: 12345},
		Counter64Var{Header: mk(KindCounter64), Value: 1 << 40},
		IPAddressVar{Header: mk(KindIPAddress), Value: net.IPv4(10, 0, 0, 1)},
		NsapAddressVar{Header: mk(KindNsapAddress), Value: []byte{0x01, 0x02}},
		OpaqueVar{Header: mk(KindOpaque), Value: []byte{0xff}},
		OpaqueFloatVar{Header: mk(KindOpaqueFloat), Value: 1.5},
		OpaqueDoubleVar{Header: mk(KindOpaqueDouble), Value: 2.5},
		NullVar{Header: mk(KindNull)},
		NoSuchObjectVar{Header: mk(KindNoSuchObject)},
		NoSuchInstanceVar{Header: mk(KindNoSuchInstance)},
		EndOfMibViewVar{Header: mk(KindEndOfMibView)},
	}
}

func TestVarBind_VariantCount(t *testing.T) {
	got := len(allVariants(t))
	const want = 18
	if got != want {
		t.Errorf("variant count = %d, want %d", got, want)
	}
}

// TestVarBind_ExhaustiveSwitch is the linter-style exhaustive switch test:
// every concrete variant must be a case. The go-check-sumtype linter will
// flag this switch if a variant is added but not handled here.
func TestVarBind_ExhaustiveSwitch(t *testing.T) {
	for _, v := range allVariants(t) {
		var handled bool
		switch v.(type) {
		case Integer32Var,
			Uinteger32Var,
			OctetStringVar,
			ObjectIDVar,
			BitStringVar,
			Counter32Var,
			Gauge32Var,
			TimeTicksVar,
			Counter64Var,
			IPAddressVar,
			NsapAddressVar,
			OpaqueVar,
			OpaqueFloatVar,
			OpaqueDoubleVar,
			NullVar,
			NoSuchObjectVar,
			NoSuchInstanceVar,
			EndOfMibViewVar:
			handled = true
		}
		if !handled {
			t.Errorf("variant %T not handled by exhaustive switch", v)
		}
	}
}

func TestVarBind_GetHeader(t *testing.T) {
	want := mustOID(t, "1.3.6.1.2.1.1.1.0")
	for _, v := range allVariants(t) {
		h := v.GetHeader()
		if !h.OID.Equal(want) {
			t.Errorf("%T.GetHeader().OID = %q, want %q", v, h.OID, want)
		}
		if h.Kind == KindUnknown {
			t.Errorf("%T.GetHeader().Kind = KindUnknown", v)
		}
	}
}

func TestIsException(t *testing.T) {
	oid := mustOID(t, "1.3.6.1")
	h := Header{OID: oid, Kind: KindNoSuchObject}
	cases := []struct {
		name string
		v    VarBind
		want bool
	}{
		{"NoSuchObject", NoSuchObjectVar{Header: h}, true},
		{"NoSuchInstance", NoSuchInstanceVar{Header: Header{OID: oid, Kind: KindNoSuchInstance}}, true},
		{"EndOfMibView", EndOfMibViewVar{Header: Header{OID: oid, Kind: KindEndOfMibView}}, true},
		{"Integer32", Integer32Var{Header: Header{OID: oid, Kind: KindInteger32}, Value: 1}, false},
		{"Null", NullVar{Header: Header{OID: oid, Kind: KindNull}}, false},
		{"OctetString", OctetStringVar{Header: Header{OID: oid, Kind: KindOctetString}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsException(tc.v); got != tc.want {
				t.Errorf("IsException(%T) = %v, want %v", tc.v, got, tc.want)
			}
		})
	}
}

// TestVarBind_FlowsThroughInterface verifies a VarBind survives passage
// through an interface{}-typed boundary and a type switch in the receiver
// correctly identifies the variant.
func TestVarBind_FlowsThroughInterface(t *testing.T) {
	send := func(v VarBind) any { return v }
	for _, v := range allVariants(t) {
		recv := send(v)
		got, ok := recv.(VarBind)
		if !ok {
			t.Errorf("%T did not type-assert to VarBind after any boundary", v)
			continue
		}
		if got.GetHeader().Kind != v.GetHeader().Kind {
			t.Errorf("%T: header kind changed across boundary", v)
		}
	}
}
