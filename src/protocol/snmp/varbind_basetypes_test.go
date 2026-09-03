package snmp

import (
	"bytes"
	"math"
	"net"
	"testing"
)

// TestCounter32Var_BehaviouralPin pins Counter32Var's documented
// contract: carries a uint32 value, reports KindCounter32, never an
// exception. Wrap-around is the caller's concern (RFC 2578 §7.1.6
// "counters wrap modulo 2^32"); the type accepts any uint32 value.
//
// Covers conformance matrix row: RFC 2578 §7.1.6 / Counter32Var.
func TestCounter32Var_BehaviouralPin(t *testing.T) {
	h := Header{OID: MustOID(1, 3, 6), Kind: KindCounter32}
	for _, v := range []uint32{0, 1, math.MaxUint32 - 1, math.MaxUint32} {
		vb := Counter32Var{Header: h, Value: v}
		if vb.GetHeader().Kind != KindCounter32 {
			t.Errorf("Value=%d: Kind = %v, want KindCounter32", v, vb.GetHeader().Kind)
		}
		if vb.Value != v {
			t.Errorf("Value=%d: Value = %d, want %d", v, vb.Value, v)
		}
		if IsException(vb) {
			t.Errorf("Value=%d: IsException = true, want false", v)
		}
	}
}

// TestGauge32_Uinteger32_DistinctKinds pins that Gauge32Var and
// Uinteger32Var share the uint32 value type but report distinct Kinds.
// On the wire both use Asn1BER tag 0x42 (RFC 2578 §7.1.7); the library
// keeps them distinct so the Backend translation layer can choose
// whether to merge or preserve the SMI-level distinction.
//
// Covers conformance matrix row: RFC 2578 §7.1.7 / Gauge32Var, Uinteger32Var.
func TestGauge32_Uinteger32_DistinctKinds(t *testing.T) {
	h := func(k Kind) Header { return Header{OID: MustOID(1, 3, 6), Kind: k} }
	g := Gauge32Var{Header: h(KindGauge32), Value: math.MaxUint32}
	u := Uinteger32Var{Header: h(KindUinteger32), Value: math.MaxUint32}

	if g.GetHeader().Kind != KindGauge32 {
		t.Errorf("Gauge32Var.Kind = %v, want KindGauge32", g.GetHeader().Kind)
	}
	if u.GetHeader().Kind != KindUinteger32 {
		t.Errorf("Uinteger32Var.Kind = %v, want KindUinteger32", u.GetHeader().Kind)
	}
	if g.GetHeader().Kind == u.GetHeader().Kind {
		t.Error("KindGauge32 and KindUinteger32 must be distinct")
	}
	if g.Value != math.MaxUint32 || u.Value != math.MaxUint32 {
		t.Errorf("max-value round-trip: g=%d u=%d, want both = MaxUint32", g.Value, u.Value)
	}
}

// TestTimeTicksVar_HundredthsOfSecond pins TimeTicksVar's documented
// unit: "hundredths of a second" per RFC 2578 §7.1.8. Value=12345
// represents 123 seconds and 45 hundredths.
//
// Covers conformance matrix row: RFC 2578 §7.1.8 / TimeTicksVar.
func TestTimeTicksVar_HundredthsOfSecond(t *testing.T) {
	vb := TimeTicksVar{
		Header: Header{OID: MustOID(1, 3, 6), Kind: KindTimeTicks},
		Value:  12345,
	}
	if vb.GetHeader().Kind != KindTimeTicks {
		t.Errorf("Kind = %v, want KindTimeTicks", vb.GetHeader().Kind)
	}
	// Value/100 = seconds; Value%100 = hundredths.
	if got, want := vb.Value/100, uint32(123); got != want {
		t.Errorf("seconds = %d, want %d (Value/100)", got, want)
	}
	if got, want := vb.Value%100, uint32(45); got != want {
		t.Errorf("hundredths = %d, want %d (Value%%100)", got, want)
	}
}

// TestIPAddressVar_IPv4Forms pins RFC 2578 §7.1.10 IpAddress is a 4-byte
// IPv4 wire form. net.IP accepts both 4-byte and 16-byte representations;
// the docstring says the field type is broader than the wire type. This
// test pins both forms round-trip and resolve identically via To4()/To16().
//
// Covers conformance matrix row: RFC 2578 §7.1.10 / IPAddressVar.
func TestIPAddressVar_IPv4Forms(t *testing.T) {
	h := Header{OID: MustOID(1, 3, 6), Kind: KindIPAddress}

	// 16-byte form via net.IPv4 (this is what net.IPv4 returns).
	vb := IPAddressVar{Header: h, Value: net.IPv4(192, 0, 2, 1)}
	if vb.GetHeader().Kind != KindIPAddress {
		t.Errorf("Kind = %v, want KindIPAddress", vb.GetHeader().Kind)
	}
	v4 := vb.Value.To4()
	if v4 == nil {
		t.Fatal("To4() returned nil, want 4 bytes")
	}
	if want := []byte{192, 0, 2, 1}; !bytes.Equal(v4, want) {
		t.Errorf("To4() = %v, want %v", v4, want)
	}
	v16 := vb.Value.To16()
	if len(v16) != 16 {
		t.Fatalf("To16() length = %d, want 16", len(v16))
	}
	if !bytes.Equal(v16[12:], []byte{192, 0, 2, 1}) {
		t.Errorf("To16() last 4 bytes = %v, want [192 0 2 1]", v16[12:])
	}

	// 4-byte form passed directly — round-trips identically through To4().
	vb4 := IPAddressVar{Header: h, Value: net.IP{192, 0, 2, 1}}
	if got := vb4.Value.To4(); !bytes.Equal(got, []byte{192, 0, 2, 1}) {
		t.Errorf("4-byte-form To4() = %v, want [192 0 2 1]", got)
	}
}

// TestCounter64Var_BehaviouralPin pins Counter64Var's documented
// uint64 value type and KindCounter64 per RFC 2578 §7.1.11. Like
// Counter32, wrap-around is the caller's concern; the type accepts
// any uint64.
//
// Covers conformance matrix row: RFC 2578 §7.1.11 / Counter64Var.
func TestCounter64Var_BehaviouralPin(t *testing.T) {
	h := Header{OID: MustOID(1, 3, 6), Kind: KindCounter64}
	for _, v := range []uint64{0, 1, math.MaxUint64 - 1, math.MaxUint64} {
		vb := Counter64Var{Header: h, Value: v}
		if vb.GetHeader().Kind != KindCounter64 {
			t.Errorf("Value=%d: Kind = %v, want KindCounter64", v, vb.GetHeader().Kind)
		}
		if vb.Value != v {
			t.Errorf("Value=%d: Value = %d, want %d", v, vb.Value, v)
		}
		if IsException(vb) {
			t.Errorf("Value=%d: IsException = true, want false", v)
		}
	}
}

// TestOpaqueFloatVar_Passthrough pins RFC 2856 §3 OpaqueFloat carries
// an IEEE-754 float32 verbatim. 1.5 is exactly representable in IEEE-754,
// so the round-trip is bit-exact.
//
// Covers conformance matrix row: RFC 2856 §3 / OpaqueFloatVar.
func TestOpaqueFloatVar_Passthrough(t *testing.T) {
	vb := OpaqueFloatVar{
		Header: Header{OID: MustOID(1, 3, 6), Kind: KindOpaqueFloat},
		Value:  1.5,
	}
	if vb.GetHeader().Kind != KindOpaqueFloat {
		t.Errorf("Kind = %v, want KindOpaqueFloat", vb.GetHeader().Kind)
	}
	if vb.Value != 1.5 {
		t.Errorf("Value = %g, want 1.5", vb.Value)
	}
	if IsException(vb) {
		t.Error("IsException = true, want false")
	}
}

// TestOpaqueDoubleVar_Passthrough pins RFC 2856 §3 OpaqueDouble carries
// an IEEE-754 float64 verbatim. The float64 literal is its own canonical
// form, so the round-trip is bit-exact.
//
// Covers conformance matrix row: RFC 2856 §3 / OpaqueDoubleVar.
func TestOpaqueDoubleVar_Passthrough(t *testing.T) {
	const pi = 3.141592653589793
	vb := OpaqueDoubleVar{
		Header: Header{OID: MustOID(1, 3, 6), Kind: KindOpaqueDouble},
		Value:  pi,
	}
	if vb.GetHeader().Kind != KindOpaqueDouble {
		t.Errorf("Kind = %v, want KindOpaqueDouble", vb.GetHeader().Kind)
	}
	if vb.Value != pi {
		t.Errorf("Value = %g, want %g", vb.Value, pi)
	}
	if IsException(vb) {
		t.Error("IsException = true, want false")
	}
}

// Covers conformance matrix row: enc-opaque-zero (gosnmp #453). An Opaque
// float/double 0.0 must encode its full 4-/8-byte IEEE-754 payload — the
// all-zero bit pattern must not be minimized away to a shorter field, or a
// decoder expecting a fixed-width real would reject it.
func TestEncOpaqueZero(t *testing.T) {
	t.Run("float 0.0 -> full 4 bytes", func(t *testing.T) {
		enc := appendOpaqueFloat(nil, 0.0)
		_, content, _, err := parseTLV(enc)
		if err != nil {
			t.Fatal(err)
		}
		want := []byte{opaqueTagPrefix, opaqueFloatTag, 0x04, 0x00, 0x00, 0x00, 0x00}
		if !bytes.Equal(content, want) {
			t.Fatalf("Opaque float 0.0 content = %x, want %x (full 4-byte IEEE-754)", content, want)
		}
		isDouble, ok, f, err := opaqueReal(content)
		if err != nil || !ok || isDouble || f != 0.0 {
			t.Fatalf("opaqueReal = (isDouble=%v ok=%v f=%v err=%v), want single 0.0", isDouble, ok, f, err)
		}
	})
	t.Run("double 0.0 -> full 8 bytes", func(t *testing.T) {
		enc := appendOpaqueDouble(nil, 0.0)
		_, content, _, err := parseTLV(enc)
		if err != nil {
			t.Fatal(err)
		}
		want := []byte{opaqueTagPrefix, opaqueDoubleTag, 0x08, 0, 0, 0, 0, 0, 0, 0, 0}
		if !bytes.Equal(content, want) {
			t.Fatalf("Opaque double 0.0 content = %x, want %x (full 8-byte IEEE-754)", content, want)
		}
		isDouble, ok, f, err := opaqueReal(content)
		if err != nil || !ok || !isDouble || f != 0.0 {
			t.Fatalf("opaqueReal = (isDouble=%v ok=%v f=%v err=%v), want double 0.0", isDouble, ok, f, err)
		}
	})
}

// TestBaseTypeVars_ZeroOIDValid asserts that a zero-OID variant is
// structurally valid for every base-type variant — pins the
// value-embedded Header contract: an empty OID is the zero value,
// not an error.
func TestBaseTypeVars_ZeroOIDValid(t *testing.T) {
	cases := []VarBind{
		Counter32Var{Header: Header{Kind: KindCounter32}},
		Counter64Var{Header: Header{Kind: KindCounter64}},
		Gauge32Var{Header: Header{Kind: KindGauge32}},
		Uinteger32Var{Header: Header{Kind: KindUinteger32}},
		TimeTicksVar{Header: Header{Kind: KindTimeTicks}},
		IPAddressVar{Header: Header{Kind: KindIPAddress}},
		OpaqueFloatVar{Header: Header{Kind: KindOpaqueFloat}},
		OpaqueDoubleVar{Header: Header{Kind: KindOpaqueDouble}},
	}
	for _, vb := range cases {
		if got := vb.GetHeader().OID.Len(); got != 0 {
			t.Errorf("%T: zero-OID Len() = %d, want 0", vb, got)
		}
	}
}
