package snmp

import (
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"testing"
)

// All variants used in decode_test.go share the same Header for
// readability — the helpers ignore it apart from the exception check.
func hdr(k Kind) Header { return Header{OID: MustOID(1, 3, 6, 1, 4, 1, 0, 1), Kind: k} }

// TestDecodeInt32_HappyPath covers the natural Integer32Var path plus
// each of the four 32-bit unsigned variants that the leniency policy
// widens (with no value > MaxInt32). All five should return the exact
// numeric value with no error.
func TestDecodeInt32_HappyPath(t *testing.T) {
	cases := []struct {
		name string
		vb   VarBind
		want int32
	}{
		{"Integer32-zero", Integer32Var{Header: hdr(KindInteger32), Value: 0}, 0},
		{"Integer32-positive", Integer32Var{Header: hdr(KindInteger32), Value: 78}, 78},
		{"Integer32-negative", Integer32Var{Header: hdr(KindInteger32), Value: -5}, -5},
		{"Uinteger32-coerced", Uinteger32Var{Header: hdr(KindUinteger32), Value: 78}, 78},
		{"Gauge32-coerced", Gauge32Var{Header: hdr(KindGauge32), Value: 78}, 78},
		{"Counter32-coerced", Counter32Var{Header: hdr(KindCounter32), Value: 78}, 78},
		{"TimeTicks-coerced", TimeTicksVar{Header: hdr(KindTimeTicks), Value: 78}, 78},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeInt32(tc.vb)
			if err != nil {
				t.Fatalf("DecodeInt32(%T): unexpected error: %v", tc.vb, err)
			}
			if got != tc.want {
				t.Fatalf("DecodeInt32(%T) = %d; want %d", tc.vb, got, tc.want)
			}
		})
	}
}

// TestDecodeInt32_Lossy covers the four 32-bit unsigned variants
// holding a value > MaxInt32 — the table's "value > MaxInt32" lossy
// condition. Each should return ErrLossyConversion carrying both the
// source variant type and the offending value in the message.
func TestDecodeInt32_Lossy(t *testing.T) {
	overflow := uint32(math.MaxInt32) + 1
	cases := []VarBind{
		Uinteger32Var{Header: hdr(KindUinteger32), Value: overflow},
		Gauge32Var{Header: hdr(KindGauge32), Value: overflow},
		Counter32Var{Header: hdr(KindCounter32), Value: overflow},
		TimeTicksVar{Header: hdr(KindTimeTicks), Value: overflow},
	}
	for _, vb := range cases {
		t.Run(typeNameOf(vb), func(t *testing.T) {
			got, err := DecodeInt32(vb)
			if got != 0 {
				t.Fatalf("DecodeInt32(%T) = %d; want zero on error", vb, got)
			}
			if !errors.Is(err, ErrLossyConversion) {
				t.Fatalf("DecodeInt32(%T) error = %v; want ErrLossyConversion", vb, err)
			}
			msg := err.Error()
			if !strings.Contains(msg, typeNameOf(vb)) {
				t.Errorf("error %q should mention source variant type", msg)
			}
			if !strings.Contains(msg, fmt.Sprint(overflow)) {
				t.Errorf("error %q should mention the offending value %d", msg, overflow)
			}
		})
	}
}

// TestDecodeUint32_HappyPath covers the natural unsigned 32-bit
// variants plus the Integer32Var case when value >= 0. None should
// produce an error.
func TestDecodeUint32_HappyPath(t *testing.T) {
	cases := []struct {
		name string
		vb   VarBind
		want uint32
	}{
		{"Uinteger32-natural", Uinteger32Var{Header: hdr(KindUinteger32), Value: 78}, 78},
		{"Gauge32-natural", Gauge32Var{Header: hdr(KindGauge32), Value: 78}, 78},
		{"Counter32-natural", Counter32Var{Header: hdr(KindCounter32), Value: 78}, 78},
		{"TimeTicks-natural", TimeTicksVar{Header: hdr(KindTimeTicks), Value: 78}, 78},
		{"Integer32-zero", Integer32Var{Header: hdr(KindInteger32), Value: 0}, 0},
		{"Integer32-positive", Integer32Var{Header: hdr(KindInteger32), Value: 78}, 78},
		{"Integer32-max-positive", Integer32Var{Header: hdr(KindInteger32), Value: math.MaxInt32}, math.MaxInt32},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeUint32(tc.vb)
			if err != nil {
				t.Fatalf("DecodeUint32(%T): unexpected error: %v", tc.vb, err)
			}
			if got != tc.want {
				t.Fatalf("DecodeUint32(%T) = %d; want %d", tc.vb, got, tc.want)
			}
		})
	}
}

// TestDecodeUint32_Lossy covers the Integer32Var-with-negative-value
// case — the only lossy condition in the table for the uint32 helper.
func TestDecodeUint32_Lossy(t *testing.T) {
	vb := Integer32Var{Header: hdr(KindInteger32), Value: -1}
	got, err := DecodeUint32(vb)
	if got != 0 {
		t.Fatalf("DecodeUint32(Integer32Var{-1}) = %d; want zero on error", got)
	}
	if !errors.Is(err, ErrLossyConversion) {
		t.Fatalf("DecodeUint32(Integer32Var{-1}) error = %v; want ErrLossyConversion", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "Integer32Var") {
		t.Errorf("error %q should mention source variant type", msg)
	}
	if !strings.Contains(msg, "-1") {
		t.Errorf("error %q should mention the offending value", msg)
	}
}

// TestDecodeUint64_HappyPath covers the natural Counter64Var path,
// widening from each 32-bit variant, and the Integer32Var case when
// value >= 0.
func TestDecodeUint64_HappyPath(t *testing.T) {
	const large = uint64(1) << 40 // beyond uint32 range
	cases := []struct {
		name string
		vb   VarBind
		want uint64
	}{
		{"Counter64-natural", Counter64Var{Header: hdr(KindCounter64), Value: large}, large},
		{"Counter64-zero", Counter64Var{Header: hdr(KindCounter64), Value: 0}, 0},
		{"Uinteger32-widen", Uinteger32Var{Header: hdr(KindUinteger32), Value: 78}, 78},
		{"Gauge32-widen", Gauge32Var{Header: hdr(KindGauge32), Value: 78}, 78},
		{"Counter32-widen", Counter32Var{Header: hdr(KindCounter32), Value: 78}, 78},
		{"TimeTicks-widen", TimeTicksVar{Header: hdr(KindTimeTicks), Value: 78}, 78},
		{"Integer32-zero", Integer32Var{Header: hdr(KindInteger32), Value: 0}, 0},
		{"Integer32-positive", Integer32Var{Header: hdr(KindInteger32), Value: 78}, 78},
		{"Uinteger32-max", Uinteger32Var{Header: hdr(KindUinteger32), Value: math.MaxUint32}, uint64(math.MaxUint32)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeUint64(tc.vb)
			if err != nil {
				t.Fatalf("DecodeUint64(%T): unexpected error: %v", tc.vb, err)
			}
			if got != tc.want {
				t.Fatalf("DecodeUint64(%T) = %d; want %d", tc.vb, got, tc.want)
			}
		})
	}
}

// TestDecodeUint64_Lossy covers the Integer32Var-negative case — the
// only lossy condition in the uint64 helper's accept set.
func TestDecodeUint64_Lossy(t *testing.T) {
	vb := Integer32Var{Header: hdr(KindInteger32), Value: -1}
	got, err := DecodeUint64(vb)
	if got != 0 {
		t.Fatalf("DecodeUint64(Integer32Var{-1}) = %d; want zero on error", got)
	}
	if !errors.Is(err, ErrLossyConversion) {
		t.Fatalf("DecodeUint64(Integer32Var{-1}) error = %v; want ErrLossyConversion", err)
	}
}

// TestDecodeBytes_HappyPath covers OctetStringVar (the natural carrier)
// and OpaqueVar (raw Opaque payload). Both should round-trip the byte
// content.
func TestDecodeBytes_HappyPath(t *testing.T) {
	cases := []struct {
		name string
		vb   VarBind
		want []byte
	}{
		{"OctetString-bytes", OctetStringVar{Header: hdr(KindOctetString), Value: []byte("hello")}, []byte("hello")},
		{"OctetString-empty", OctetStringVar{Header: hdr(KindOctetString), Value: nil}, []byte{}},
		{"Opaque-bytes", OpaqueVar{Header: hdr(KindOpaque), Value: []byte{0x01, 0x02, 0x03}}, []byte{0x01, 0x02, 0x03}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeBytes(tc.vb)
			if err != nil {
				t.Fatalf("DecodeBytes(%T): unexpected error: %v", tc.vb, err)
			}
			if string(got) != string(tc.want) {
				t.Fatalf("DecodeBytes(%T) = %q; want %q", tc.vb, got, tc.want)
			}
		})
	}
}

// TestDecodeBytes_DefensiveCopy pins the no-aliasing guarantee
// documented in DecodeBytes's godoc: the returned slice must not share
// storage with the source VarBind's Value field. gosnmp recycles
// packet buffers, so aliasing decode output to that storage is a
// silent-corruption shape we explicitly rule out.
//
// Both accepted variants (OctetStringVar, OpaqueVar) get the same
// guarantee — a single-variant test would let a refactor of the
// OpaqueVar branch silently start aliasing.
func TestDecodeBytes_DefensiveCopy(t *testing.T) {
	t.Run("OctetStringVar", func(t *testing.T) {
		source := []byte{1, 2, 3, 4}
		vb := OctetStringVar{Header: hdr(KindOctetString), Value: source}

		out, err := DecodeBytes(vb)
		if err != nil {
			t.Fatalf("DecodeBytes: unexpected error: %v", err)
		}

		// Mutate the returned slice; the source must remain untouched.
		out[0] = 0xff
		if source[0] != 1 {
			t.Fatalf("DecodeBytes returned a slice aliased to source storage: source[0] = %d after mutation", source[0])
		}
	})

	t.Run("OpaqueVar", func(t *testing.T) {
		source := []byte{1, 2, 3, 4}
		vb := OpaqueVar{Header: hdr(KindOpaque), Value: source}

		out, err := DecodeBytes(vb)
		if err != nil {
			t.Fatalf("DecodeBytes: unexpected error: %v", err)
		}

		out[0] = 0xff
		if source[0] != 1 {
			t.Fatalf("DecodeBytes returned an OpaqueVar slice aliased to source storage: source[0] = %d after mutation", source[0])
		}
	})
}

// TestDecodeOID_HappyPath covers the natural-match ObjectIDVar case.
func TestDecodeOID_HappyPath(t *testing.T) {
	want := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	vb := ObjectIDVar{Header: hdr(KindObjectID), Value: want}
	got, err := DecodeOID(vb)
	if err != nil {
		t.Fatalf("DecodeOID: unexpected error: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("DecodeOID = %s; want %s", got, want)
	}
}

// TestDecodeIP_HappyPath covers the natural-match IPAddressVar case.
func TestDecodeIP_HappyPath(t *testing.T) {
	want := net.IPv4(10, 1, 2, 3).To4()
	vb := IPAddressVar{Header: hdr(KindIPAddress), Value: want}
	got, err := DecodeIP(vb)
	if err != nil {
		t.Fatalf("DecodeIP: unexpected error: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("DecodeIP = %s; want %s", got, want)
	}
}

// TestDecode_TypeMismatch_OffTableVariants covers the closed-policy
// rejection rows. Each helper called on a variant outside its accept
// set must return ErrTypeMismatch carrying the source variant in the
// message. The closed-policy variants (BitString / NsapAddress /
// OpaqueFloat / OpaqueDouble / Null) are exercised explicitly — they
// are deliberately not accepted by any helper.
func TestDecode_TypeMismatch_OffTableVariants(t *testing.T) {
	closedPolicy := []VarBind{
		BitStringVar{Header: hdr(KindBitString), Value: []byte{0x80}},
		NsapAddressVar{Header: hdr(KindNsapAddress), Value: []byte{0x47, 0x00, 0x01}},
		OpaqueFloatVar{Header: hdr(KindOpaqueFloat), Value: 1.0},
		OpaqueDoubleVar{Header: hdr(KindOpaqueDouble), Value: 1.0},
		NullVar{Header: hdr(KindNull)},
	}

	t.Run("DecodeInt32-rejects-closed-policy", func(t *testing.T) {
		for _, vb := range closedPolicy {
			assertTypeMismatch(t, "DecodeInt32", vb, func() error {
				_, err := DecodeInt32(vb)
				return err
			})
		}
	})
	t.Run("DecodeUint32-rejects-closed-policy", func(t *testing.T) {
		for _, vb := range closedPolicy {
			assertTypeMismatch(t, "DecodeUint32", vb, func() error {
				_, err := DecodeUint32(vb)
				return err
			})
		}
	})
	t.Run("DecodeUint64-rejects-closed-policy", func(t *testing.T) {
		for _, vb := range closedPolicy {
			assertTypeMismatch(t, "DecodeUint64", vb, func() error {
				_, err := DecodeUint64(vb)
				return err
			})
		}
	})
	t.Run("DecodeBytes-rejects-closed-policy", func(t *testing.T) {
		for _, vb := range closedPolicy {
			assertTypeMismatch(t, "DecodeBytes", vb, func() error {
				_, err := DecodeBytes(vb)
				return err
			})
		}
	})
	t.Run("DecodeOID-rejects-closed-policy", func(t *testing.T) {
		for _, vb := range closedPolicy {
			assertTypeMismatch(t, "DecodeOID", vb, func() error {
				_, err := DecodeOID(vb)
				return err
			})
		}
	})
	t.Run("DecodeIP-rejects-closed-policy", func(t *testing.T) {
		for _, vb := range closedPolicy {
			assertTypeMismatch(t, "DecodeIP", vb, func() error {
				_, err := DecodeIP(vb)
				return err
			})
		}
	})

	// DecodeOID and DecodeIP do not coerce across structural types:
	// even an accepted-by-other-helpers variant like Uinteger32Var
	// must be rejected as TypeMismatch by these two.
	t.Run("DecodeOID-rejects-non-OID-variant", func(t *testing.T) {
		_, err := DecodeOID(Uinteger32Var{Header: hdr(KindUinteger32), Value: 1})
		if !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("DecodeOID(Uinteger32Var) error = %v; want ErrTypeMismatch", err)
		}
	})
	t.Run("DecodeIP-rejects-non-IP-variant", func(t *testing.T) {
		_, err := DecodeIP(OctetStringVar{Header: hdr(KindOctetString), Value: []byte("nope")})
		if !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("DecodeIP(OctetStringVar) error = %v; want ErrTypeMismatch", err)
		}
	})

	// DecodeBytes does not accept the numeric variants — even though
	// they have a Kind, they are not byte-bearing.
	t.Run("DecodeBytes-rejects-numeric-variant", func(t *testing.T) {
		_, err := DecodeBytes(Uinteger32Var{Header: hdr(KindUinteger32), Value: 1})
		if !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("DecodeBytes(Uinteger32Var) error = %v; want ErrTypeMismatch", err)
		}
	})

	// Counter64Var is deliberately outside DecodeUint32's accept set
	// (Counter64 carries a 64-bit value semantically; truncating to
	// 32 bits would silently discard information the agent intended
	// to deliver). The closed-policy contract surfaces this as a
	// TypeMismatch rather than a LossyConversion.
	t.Run("DecodeUint32-rejects-Counter64", func(t *testing.T) {
		_, err := DecodeUint32(Counter64Var{Header: hdr(KindCounter64), Value: uint64(1) << 33})
		if !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("DecodeUint32(Counter64Var) error = %v; want ErrTypeMismatch", err)
		}
	})
}

// TestDecode_Exception covers ErrException for all three exception
// variants on every helper. The helpers must surface exceptions
// explicitly — leniency policy is for value-bearing variants only.
func TestDecode_Exception(t *testing.T) {
	exceptions := []VarBind{
		NoSuchObjectVar{Header: hdr(KindNoSuchObject)},
		NoSuchInstanceVar{Header: hdr(KindNoSuchInstance)},
		EndOfMibViewVar{Header: hdr(KindEndOfMibView)},
	}
	helpers := []struct {
		name string
		call func(VarBind) error
	}{
		{"DecodeInt32", func(vb VarBind) error { _, err := DecodeInt32(vb); return err }},
		{"DecodeUint32", func(vb VarBind) error { _, err := DecodeUint32(vb); return err }},
		{"DecodeUint64", func(vb VarBind) error { _, err := DecodeUint64(vb); return err }},
		{"DecodeBytes", func(vb VarBind) error { _, err := DecodeBytes(vb); return err }},
		{"DecodeOID", func(vb VarBind) error { _, err := DecodeOID(vb); return err }},
		{"DecodeIP", func(vb VarBind) error { _, err := DecodeIP(vb); return err }},
	}
	for _, h := range helpers {
		for _, vb := range exceptions {
			t.Run(h.name+"/"+typeNameOf(vb), func(t *testing.T) {
				err := h.call(vb)
				if !errors.Is(err, ErrException) {
					t.Fatalf("%s(%T) error = %v; want ErrException", h.name, vb, err)
				}
			})
		}
	}
}

// Covers conformance matrix row: enc-unsigned-as-signed (Check Point sk115119;
// IBM IZ77427). RECORDED ACCEPTED-RISK: some agents carry a Gauge32/Unsigned32
// value with the INTEGER tag (0x02) and the MSB set. Decoded by the tag it is a
// negative Integer32Var, and coercing a negative Integer32Var to uint32 is
// rejected as ErrLossyConversion — the deliberate MikroTik leniency precedent
// (TestDecodeUint32_Lossy). A negative INTEGER-tagged value is indistinguishable
// from a genuine -1, so rejecting is safer than silently reinterpreting it as
// ~4 billion. This pin locks that reject behavior so a future change that
// silently reinterprets is caught.
func TestEncUnsignedAsSigned_RejectsNegativeIntTagged(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1)
	// 0xFFFFFFFF under the INTEGER tag decodes to a negative Integer32Var.
	vb, err := decodeValue(oid, tagInteger, []byte{0xff, 0xff, 0xff, 0xff})
	if err != nil {
		t.Fatalf("decodeValue(INTEGER, FF FF FF FF): %v", err)
	}
	iv, ok := vb.(Integer32Var)
	if !ok {
		t.Fatalf("decoded %T, want Integer32Var", vb)
	}
	if iv.Value >= 0 {
		t.Fatalf("decoded value = %d, want negative (MSB-set INTEGER)", iv.Value)
	}
	// Coercing the negative value to uint32 must be a typed lossy error, not a
	// silent two's-complement reinterpretation.
	if _, err := DecodeUint32(vb); !errors.Is(err, ErrLossyConversion) {
		t.Fatalf("DecodeUint32(negative Integer32Var) err = %v, want Is ErrLossyConversion", err)
	}
}

// assertTypeMismatch checks that fn() returns ErrTypeMismatch carrying
// the source variant's type name. Centralizes the assertion shape so
// the matrix tests above stay readable.
func assertTypeMismatch(t *testing.T, helper string, vb VarBind, fn func() error) {
	t.Helper()
	err := fn()
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("%s(%T) error = %v; want ErrTypeMismatch", helper, vb, err)
	}
	want := typeNameOf(vb)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("%s(%T) error %q should mention source variant %q", helper, vb, err, want)
	}
}

// typeNameOf returns the unqualified concrete type name of vb (e.g.
// "Integer32Var" for snmp.Integer32Var) — used to assert that error
// messages name the source variant.
func typeNameOf(vb VarBind) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", vb), "snmp.")
}
