package snmp

import (
	"bytes"
	"errors"
	"math"
	"net"
	"testing"
)

// Covers conformance matrix row: BER tag numeric values. A reorder of the
// tag constants would silently corrupt the wire encoding once these cross
// the network, so each is pinned to its RFC value.
func TestBERTags_NumericValues(t *testing.T) {
	cases := []struct {
		name string
		got  byte
		want byte
	}{
		{"INTEGER", tagInteger, 0x02},
		{"BIT STRING", tagBitString, 0x03},
		{"OCTET STRING", tagOctetString, 0x04},
		{"NULL", tagNull, 0x05},
		{"OID", tagOID, 0x06},
		{"SEQUENCE", tagSequence, 0x30},
		{"IpAddress", tagIPAddress, 0x40},
		{"Counter32", tagCounter32, 0x41},
		{"Gauge32", tagGauge32, 0x42},
		{"TimeTicks", tagTimeTicks, 0x43},
		{"Opaque", tagOpaque, 0x44},
		{"NsapAddress", tagNsapAddress, 0x45},
		{"Counter64", tagCounter64, 0x46},
		{"Unsigned32", tagUinteger32, 0x47},
		{"noSuchObject", tagNoSuchObject, 0x80},
		{"noSuchInstance", tagNoSuchInstance, 0x81},
		{"endOfMibView", tagEndOfMibView, 0x82},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("tag %s = 0x%02x, want 0x%02x", c.name, c.got, c.want)
		}
	}
}

// Covers conformance matrix row: BER length encoding. Short-form and
// long-form lengths must decode identically, and long-form encoding of a
// small value (the gosnmp #544 foot-gun) must be accepted on decode.
func TestParseLength(t *testing.T) {
	cases := []struct {
		name     string
		in       []byte
		wantLen  int
		wantUsed int
		wantErr  error
	}{
		{"short-form 0", []byte{0x00}, 0, 1, nil},
		{"short-form 4", []byte{0x04}, 4, 1, nil},
		{"short-form 127", []byte{0x7f}, 127, 1, nil},
		{"long-form 1 octet", []byte{0x81, 0x04}, 4, 2, nil},
		{"long-form 1 octet 200", []byte{0x81, 0xc8}, 200, 2, nil},
		{"long-form 2 octet", []byte{0x82, 0x01, 0x00}, 256, 3, nil},
		{"indefinite rejected", []byte{0x80}, 0, 0, errIndefiniteLength},
		{"empty truncated", []byte{}, 0, 0, errTruncated},
		{"long-form truncated", []byte{0x82, 0x01}, 0, 0, errTruncated},
		{"long-form too many octets", []byte{0x85, 1, 2, 3, 4, 5}, 0, 0, errLengthOverflow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotLen, gotUsed, err := parseLength(c.in)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want Is %v", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if gotLen != c.wantLen || gotUsed != c.wantUsed {
				t.Fatalf("got (len=%d used=%d), want (len=%d used=%d)", gotLen, gotUsed, c.wantLen, c.wantUsed)
			}
		})
	}
}

func TestAppendLength_RoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 127, 128, 255, 256, 65535, 65536, 16777215} {
		enc := appendLength(nil, n)
		got, used, err := parseLength(enc)
		if err != nil {
			t.Fatalf("n=%d: parseLength(%x): %v", n, enc, err)
		}
		if got != n || used != len(enc) {
			t.Fatalf("n=%d: round-trip got (len=%d used=%d), enc=%x", n, got, used, enc)
		}
	}
}

func TestParseTLV(t *testing.T) {
	t.Run("happy", func(t *testing.T) {
		// INTEGER 5, followed by trailing bytes that must not be consumed.
		buf := []byte{0x02, 0x01, 0x05, 0xde, 0xad}
		tag, content, consumed, err := parseTLV(buf)
		if err != nil {
			t.Fatal(err)
		}
		if tag != tagInteger || !bytes.Equal(content, []byte{0x05}) || consumed != 3 {
			t.Fatalf("tag=0x%02x content=%x consumed=%d", tag, content, consumed)
		}
	})
	t.Run("length exceeds buffer", func(t *testing.T) {
		// declares 10 octets of content, only 1 present (gosnmp #552 shape).
		_, _, _, err := parseTLV([]byte{0x04, 0x0a, 0x01})
		if !errors.Is(err, errLengthOverflow) {
			t.Fatalf("err = %v, want Is errLengthOverflow", err)
		}
	})
	t.Run("high-tag-number form rejected", func(t *testing.T) {
		_, _, _, err := parseTLV([]byte{0x1f, 0x81, 0x00, 0x00})
		if !errors.Is(err, errHighTagForm) {
			t.Fatalf("err = %v, want Is errHighTagForm", err)
		}
	})
	t.Run("empty truncated", func(t *testing.T) {
		_, _, _, err := parseTLV(nil)
		if !errors.Is(err, errTruncated) {
			t.Fatalf("err = %v, want Is errTruncated", err)
		}
	})
}

func TestParseSequence_DepthCeiling(t *testing.T) {
	// A SEQUENCE wrapping an empty body decodes at shallow depth.
	body := appendSequence(tagSequence, nil)
	if _, _, err := parseSequence(body, tagSequence, 0); err != nil {
		t.Fatalf("shallow depth: %v", err)
	}
	// At a depth past the ceiling the decoder refuses to descend.
	if _, _, err := parseSequence(body, tagSequence, maxDecodeDepth+1); !errors.Is(err, errMaxDepth) {
		t.Fatalf("err = %v, want Is errMaxDepth", err)
	}
	// Wrong tag is a typed error, not a silent accept.
	if _, _, err := parseSequence(body, tagOID, 0); !errors.Is(err, errUnexpectedTag) {
		t.Fatalf("err = %v, want Is errUnexpectedTag", err)
	}
}

func TestDecodeSignedInt(t *testing.T) {
	cases := []struct {
		in   []byte
		want int64
		err  error
	}{
		{[]byte{0x00}, 0, nil},
		{[]byte{0x05}, 5, nil},
		{[]byte{0x7f}, 127, nil},
		{[]byte{0x00, 0x80}, 128, nil},
		{[]byte{0xff}, -1, nil},
		{[]byte{0x80}, -128, nil},
		{[]byte{0xff, 0x7f}, -129, nil},
		{[]byte{}, 0, errTruncated},
		{[]byte{1, 2, 3, 4, 5, 6, 7, 8, 9}, 0, errIntOverflow},
	}
	for _, c := range cases {
		got, err := decodeSignedInt(c.in)
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("decodeSignedInt(%x) err = %v, want Is %v", c.in, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("decodeSignedInt(%x) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
}

func TestDecodeUnsigned_LeadingZero(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want uint64
		err  error
	}{
		{"zero", []byte{0x00}, 0, nil},
		{"small", []byte{0x2a}, 42, nil},
		{"4-byte max no pad", []byte{0xff, 0xff, 0xff, 0xff}, 0xffffffff, nil},
		{"5-byte leading zero", []byte{0x00, 0xff, 0xff, 0xff, 0xff}, 0xffffffff, nil},
		{"counter64 9-byte leading zero", []byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, math.MaxUint64, nil},
		{"too many significant", []byte{0x01, 2, 3, 4, 5, 6, 7, 8, 9}, 0, errIntOverflow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := decodeUnsigned(c.in)
			if c.err != nil {
				if !errors.Is(err, c.err) {
					t.Fatalf("err = %v, want Is %v", err, c.err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("decodeUnsigned(%x) = %d, %v; want %d", c.in, got, err, c.want)
			}
		})
	}
}

func TestUnsigned_RoundTrip(t *testing.T) {
	for _, v := range []uint64{0, 1, 127, 128, 255, 256, 0x7fffffff, 0x80000000, 0xffffffff, math.MaxUint64} {
		enc := encodeUnsigned(v)
		got, err := decodeUnsigned(enc)
		if err != nil || got != v {
			t.Fatalf("v=%d enc=%x decoded=%d err=%v", v, enc, got, err)
		}
	}
}

func TestSignedInt_RoundTrip(t *testing.T) {
	for _, v := range []int64{0, 1, -1, 127, 128, -128, -129, math.MaxInt32, math.MinInt32, math.MaxInt32 + 1} {
		enc := encodeSignedInt(v)
		got, err := decodeSignedInt(enc)
		if err != nil || got != v {
			t.Fatalf("v=%d enc=%x decoded=%d err=%v", v, enc, got, err)
		}
	}
}

func TestDecodeOID(t *testing.T) {
	t.Run("happy 1.3.6.1.2.1", func(t *testing.T) {
		// 0x2b = 43 = 1*40+3 -> arcs 1.3; then 6 1 2 1.
		content := []byte{0x2b, 0x06, 0x01, 0x02, 0x01}
		oid, err := decodeOID(content)
		if err != nil {
			t.Fatal(err)
		}
		want := MustOID(1, 3, 6, 1, 2, 1)
		if !oid.Equal(want) {
			t.Fatalf("decoded %s, want %s", oid, want)
		}
	})
	t.Run("multibyte sub-identifier", func(t *testing.T) {
		// 1.3.6.1.2.1.2.2.1.10.16777216 (a large ifIndex-style sub-id).
		want := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10, 16777216)
		enc := encodeOIDContent(want)
		oid, err := decodeOID(enc)
		if err != nil {
			t.Fatal(err)
		}
		if !oid.Equal(want) {
			t.Fatalf("round-trip decoded %s, want %s", oid, want)
		}
	})
	t.Run("zero-length OID is empty sentinel", func(t *testing.T) {
		oid, err := decodeOID(nil)
		if err != nil {
			t.Fatalf("zero-length OID should not error: %v", err)
		}
		if oid.Len() != 0 {
			t.Fatalf("got %s, want an empty OID", oid)
		}
	})
	t.Run("unterminated sub-identifier", func(t *testing.T) {
		// trailing 0x80 sets the continuation bit with no following octet.
		_, err := decodeOID([]byte{0x2b, 0x80})
		if !errors.Is(err, errTruncated) {
			t.Fatalf("err = %v, want Is errTruncated", err)
		}
	})
	t.Run("sub-identifier overflows uint32", func(t *testing.T) {
		// five continuation octets encode > 2^32.
		_, err := decodeOID([]byte{0x2b, 0x90, 0x80, 0x80, 0x80, 0x00})
		if !errors.Is(err, errOIDSubIDOverflow) {
			t.Fatalf("err = %v, want Is errOIDSubIDOverflow", err)
		}
	})
}

// TestDecodeOID_AdoptedSliceNoAlias guards the slice-adoption
// optimization: decodeOID
// now hands its freshly-built sub-id slice (which may carry spare capacity)
// straight to the OID instead of letting NewOID defensively copy it. This
// pins the invariant that deriving from a decoded OID never writes through
// that spare capacity into the original or a sibling derivation.
func TestDecodeOID_AdoptedSliceNoAlias(t *testing.T) {
	// Pre-sized capacity is len(content)+1; a short single-octet-arc OID
	// leaves spare cap (arcs < cap), exercising the aliasing risk.
	content := []byte{0x2b, 0x06, 0x01, 0x02, 0x01} // 1.3.6.1.2.1
	base, err := decodeOID(content)
	if err != nil {
		t.Fatal(err)
	}
	want := MustOID(1, 3, 6, 1, 2, 1)
	if !base.Equal(want) {
		t.Fatalf("decoded %s, want %s", base, want)
	}

	// Two independent derivations from the same decoded OID must not bleed
	// into each other through shared backing capacity.
	childA := base.Child(100)
	childB := base.Child(200)
	if !childA.Equal(MustOID(1, 3, 6, 1, 2, 1, 100)) {
		t.Fatalf("childA = %s, want …2.1.100", childA)
	}
	if !childB.Equal(MustOID(1, 3, 6, 1, 2, 1, 200)) {
		t.Fatalf("childB = %s aliased childA's append into spare capacity", childB)
	}
	if !base.Equal(want) {
		t.Fatalf("base mutated to %s after deriving children", base)
	}
}

// TestDecodeOID_TightCapNoRegrow confirms the len(b)+1 pre-size never regrows:
// for an all-single-octet OID (the arc-count worst case, arcs == len(b)+1) the
// adopted backing slice keeps its original capacity, proving the build loop
// did not reallocate. Guards the capacity bound against a future
// off-by-one.
func TestDecodeOID_TightCapNoRegrow(t *testing.T) {
	content := []byte{0x2b, 0x06, 0x01, 0x02, 0x01} // 1.3.6.1.2.1 — 6 arcs from 5 octets
	oid, err := decodeOID(content)
	if err != nil {
		t.Fatal(err)
	}
	if got := oid.Len(); got != len(content)+1 {
		t.Fatalf("arc count %d, want len(b)+1 = %d (worst-case bound)", got, len(content)+1)
	}
	// The adopted slice must still have exactly the pre-sized capacity — any
	// regrow during append would have produced a larger (doubled) capacity.
	if c := cap(oid.subs); c != len(content)+1 {
		t.Fatalf("backing cap %d, want %d — slice regrew during decode", c, len(content)+1)
	}
}

func TestOID_RoundTrip(t *testing.T) {
	for _, want := range []OID{
		MustOID(1, 3, 6, 1),
		MustOID(0, 0),
		MustOID(2, 100, 3),
		MustOID(1, 3, 6, 1, 4, 1, 9, 9, 999, 1, 2, 3),
	} {
		enc := encodeOIDContent(want)
		got, err := decodeOID(enc)
		if err != nil || !got.Equal(want) {
			t.Fatalf("oid %s enc=%x decoded=%s err=%v", want, enc, got, err)
		}
	}
}

func TestDecodeIPv4(t *testing.T) {
	got, err := decodeIPv4([]byte{10, 0, 0, 1})
	if err != nil || !got.Equal(net.IPv4(10, 0, 0, 1)) {
		t.Fatalf("decodeIPv4 = %v, %v", got, err)
	}
	if _, err := decodeIPv4([]byte{1, 2, 3}); !errors.Is(err, errUnexpectedTag) {
		t.Fatalf("3-byte IP err = %v, want Is errUnexpectedTag", err)
	}
}

func TestOpaqueReal(t *testing.T) {
	t.Run("float round-trip", func(t *testing.T) {
		enc := appendOpaqueFloat(nil, 3.5)
		_, content, _, err := parseTLV(enc)
		if err != nil {
			t.Fatal(err)
		}
		isDouble, ok, f, err := opaqueReal(content)
		if err != nil || !ok || isDouble || f != 3.5 {
			t.Fatalf("ok=%v isDouble=%v f=%v err=%v", ok, isDouble, f, err)
		}
	})
	t.Run("double round-trip", func(t *testing.T) {
		enc := appendOpaqueDouble(nil, 2.718281828)
		_, content, _, err := parseTLV(enc)
		if err != nil {
			t.Fatal(err)
		}
		isDouble, ok, f, err := opaqueReal(content)
		if err != nil || !ok || !isDouble || f != 2.718281828 {
			t.Fatalf("ok=%v isDouble=%v f=%v err=%v", ok, isDouble, f, err)
		}
	})
	t.Run("plain opaque is not a real", func(t *testing.T) {
		isDouble, ok, _, err := opaqueReal([]byte{0x01, 0x02, 0x03})
		if err != nil || ok || isDouble {
			t.Fatalf("plain opaque misclassified: ok=%v isDouble=%v err=%v", ok, isDouble, err)
		}
	})
}

// TestAppendTLVEncoders round-trips the complete-TLV encoders (tag +
// length + content) through parseTLV and the matching value decoder, so
// each encoder's framing is exercised end to end.
func TestAppendTLVEncoders(t *testing.T) {
	t.Run("appendInt", func(t *testing.T) {
		enc := appendInt(nil, -42)
		tag, content, _, err := parseTLV(enc)
		if err != nil || tag != tagInteger {
			t.Fatalf("tag=0x%02x err=%v", tag, err)
		}
		if v, err := decodeSignedInt(content); err != nil || v != -42 {
			t.Fatalf("decoded %d, %v", v, err)
		}
	})
	t.Run("appendUint", func(t *testing.T) {
		enc := appendUint(nil, tagCounter32, 0x80000000)
		tag, content, _, err := parseTLV(enc)
		if err != nil || tag != tagCounter32 {
			t.Fatalf("tag=0x%02x err=%v", tag, err)
		}
		if v, err := decodeUnsigned(content); err != nil || v != 0x80000000 {
			t.Fatalf("decoded %d, %v", v, err)
		}
	})
	t.Run("appendOID", func(t *testing.T) {
		want := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
		enc := encodeOID(want)
		tag, content, _, err := parseTLV(enc)
		if err != nil || tag != tagOID {
			t.Fatalf("tag=0x%02x err=%v", tag, err)
		}
		if got, err := decodeOID(content); err != nil || !got.Equal(want) {
			t.Fatalf("decoded %s, %v", got, err)
		}
	})
	t.Run("appendOctetString", func(t *testing.T) {
		enc := appendOctetString(nil, tagOctetString, []byte("hi"))
		tag, content, _, err := parseTLV(enc)
		if err != nil || tag != tagOctetString || string(content) != "hi" {
			t.Fatalf("tag=0x%02x content=%q err=%v", tag, content, err)
		}
	})
	t.Run("appendNull", func(t *testing.T) {
		enc := appendNull(nil)
		tag, content, _, err := parseTLV(enc)
		if err != nil || tag != tagNull || len(content) != 0 {
			t.Fatalf("tag=0x%02x content=%x err=%v", tag, content, err)
		}
	})
	t.Run("appendIPv4", func(t *testing.T) {
		enc, err := appendIPv4(net.IPv4(192, 168, 1, 1))
		if err != nil {
			t.Fatal(err)
		}
		tag, content, _, err := parseTLV(enc)
		if err != nil || tag != tagIPAddress {
			t.Fatalf("tag=0x%02x err=%v", tag, err)
		}
		if ip, err := decodeIPv4(content); err != nil || !ip.Equal(net.IPv4(192, 168, 1, 1)) {
			t.Fatalf("decoded %v, %v", ip, err)
		}
	})
	t.Run("appendIPv4 non-IPv4 rejected", func(t *testing.T) {
		if _, err := appendIPv4(net.ParseIP("::1")); !errors.Is(err, errUnexpectedTag) {
			t.Fatalf("err = %v, want Is errUnexpectedTag", err)
		}
	})
}

// TestGoldenWireVectors decodes hand-authored raw wire octets — not a
// re-encode of our own output — so a shared encoder/decoder co-bug cannot
// hide. These are the bytes a real agent puts on the wire.
func TestGoldenWireVectors(t *testing.T) {
	t.Run("Counter32 with long-form length", func(t *testing.T) {
		// Counter32 (0x41), long-form length 0x81 0x04, value 0x0F4240 = 1_000_000.
		buf := []byte{0x41, 0x81, 0x04, 0x00, 0x0f, 0x42, 0x40}
		tag, content, _, err := parseTLV(buf)
		if err != nil || tag != tagCounter32 {
			t.Fatalf("tag=0x%02x err=%v", tag, err)
		}
		got, err := decodeUnsigned(content)
		if err != nil || got != 1_000_000 {
			t.Fatalf("decoded %d, err %v", got, err)
		}
	})
	t.Run("sysDescr OCTET STRING", func(t *testing.T) {
		// OCTET STRING "Router" — bytes captured-shape.
		buf := []byte{0x04, 0x06, 'R', 'o', 'u', 't', 'e', 'r'}
		tag, content, _, err := parseTLV(buf)
		if err != nil || tag != tagOctetString || string(content) != "Router" {
			t.Fatalf("tag=0x%02x content=%q err=%v", tag, content, err)
		}
	})
}

// Covers conformance matrix row: enc-nonminimal-int (gosnmp #371). The
// encoder emits the minimal two's-complement form (-1 -> a single 0xFF
// octet), while the decoder tolerates the non-minimal 4-octet 0xFFFFFFFF
// form a sloppy agent may put on the wire — both resolve to -1.
func TestEncNonMinimalInt(t *testing.T) {
	// Encode side: -1 is one content octet, not four.
	enc := appendInt(nil, -1)
	wantEnc := []byte{tagInteger, 0x01, 0xff}
	if !bytes.Equal(enc, wantEnc) {
		t.Fatalf("appendInt(-1) = %x, want %x (minimal form)", enc, wantEnc)
	}
	// Decode side: the non-minimal 4-octet 0xFFFFFFFF still decodes to -1.
	got, err := decodeSignedInt([]byte{0xff, 0xff, 0xff, 0xff})
	if err != nil || got != -1 {
		t.Fatalf("decodeSignedInt(FFFFFFFF) = %d, %v; want -1", got, err)
	}
}

// Covers conformance matrix row: enc-id-range (gosnmp #272). A request-id
// at the 2^31-1 boundary must round-trip through encode/decode without
// overflowing to a negative value.
func TestEncIDRange(t *testing.T) {
	const maxID = int32(math.MaxInt32) // 2147483647
	m := &message{
		version:   V2c,
		community: "public",
		pdu:       pdu{typ: pduGetRequest, requestID: maxID},
	}
	wire, err := encodeMessage(m)
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	got, err := decodeMessage(wire)
	if err != nil {
		t.Fatalf("decodeMessage: %v", err)
	}
	if got.pdu.requestID != maxID {
		t.Fatalf("request-id round-trip = %d, want %d", got.pdu.requestID, maxID)
	}
	if got.pdu.requestID < 0 {
		t.Fatalf("request-id overflowed to negative: %d", got.pdu.requestID)
	}
}

// Covers conformance matrix row: enc-ipaddr-longform (gosnmp #544). An
// IpAddress whose 4-octet content is framed with a legal-but-redundant
// long-form BER length must still decode to the 4-byte address.
func TestEncIPAddrLongForm(t *testing.T) {
	// IpAddress (0x40), long-form length 0x81 0x04, then 10.0.0.1.
	buf := []byte{tagIPAddress, 0x81, 0x04, 10, 0, 0, 1}
	tag, content, _, err := parseTLV(buf)
	if err != nil || tag != tagIPAddress {
		t.Fatalf("parseTLV: tag=0x%02x err=%v", tag, err)
	}
	ip, err := decodeIPv4(content)
	if err != nil || !ip.Equal(net.IPv4(10, 0, 0, 1)) {
		t.Fatalf("decodeIPv4(long-form) = %v, %v; want 10.0.0.1", ip, err)
	}
}

// Covers conformance matrix row: enc-ipaddr-8byte (gosnmp #544). An
// IpAddress carrying 8 content octets is unrecoverable (no sane IPv4), so
// the decoder returns a typed error — not a panic and not a best-effort
// 4-byte truncation (per-row tolerance policy: typed error).
func TestEncIPAddr8Byte(t *testing.T) {
	eightByte := []byte{10, 0, 0, 1, 192, 168, 0, 1}
	if _, err := decodeIPv4(eightByte); !errors.Is(err, errUnexpectedTag) {
		t.Fatalf("decodeIPv4(8 bytes) err = %v, want Is errUnexpectedTag", err)
	}
	// Same via the full value-decode path, proving no panic end to end.
	oid := MustOID(1, 3, 6, 1)
	if _, err := decodeValue(oid, tagIPAddress, eightByte); !errors.Is(err, errUnexpectedTag) {
		t.Fatalf("decodeValue(IpAddress, 8 bytes) err = %v, want Is errUnexpectedTag", err)
	}
}

// Covers conformance matrix row: enc-opaque-unknown (gosnmp #374). An
// Opaque value wrapping an unknown sub-type marker (0x9F 0x7A, not the
// IEEE-754 float 0x78 / double 0x79 markers) is preserved as raw Opaque
// bytes rather than dropped to nil.
func TestEncOpaqueUnknown(t *testing.T) {
	content := []byte{opaqueTagPrefix, 0x7a, 0x04, 0x01, 0x02, 0x03, 0x04}
	oid := MustOID(1, 3, 6, 1)
	vb, err := decodeValue(oid, tagOpaque, content)
	if err != nil {
		t.Fatalf("decodeValue(Opaque, unknown sub-type): %v", err)
	}
	ov, ok := vb.(OpaqueVar)
	if !ok {
		t.Fatalf("decoded %T, want OpaqueVar", vb)
	}
	if ov.Value == nil {
		t.Fatal("OpaqueVar.Value is nil, want raw bytes preserved")
	}
	if !bytes.Equal(ov.Value, content) {
		t.Fatalf("OpaqueVar.Value = %x, want raw %x", ov.Value, content)
	}
}

// Covers conformance matrix row: enc-zerolen-int (gosnmp #241). A
// zero-length signed INTEGER (02 00) is unrecoverable on the signed path
// and must yield a typed errTruncated — never a zero-value best-effort.
// (The unsigned-counter leniency that decodes 02 00 as 0 is a separate
// path, pinned in enc_gaps_test.go.)
func TestEncZeroLenIntSigned(t *testing.T) {
	// Direct primitive: empty content is truncated.
	if _, err := decodeSignedInt([]byte{}); !errors.Is(err, errTruncated) {
		t.Fatalf("decodeSignedInt(zero-length) err = %v, want Is errTruncated", err)
	}
	// Full TLV 02 00 through the value-decode path.
	tag, content, _, err := parseTLV([]byte{tagInteger, 0x00})
	if err != nil || tag != tagInteger || len(content) != 0 {
		t.Fatalf("parseTLV(02 00): tag=0x%02x content=%x err=%v", tag, content, err)
	}
	oid := MustOID(1, 3, 6, 1)
	if _, err := decodeValue(oid, tagInteger, content); !errors.Is(err, errTruncated) {
		t.Fatalf("decodeValue(INTEGER, zero-length) err = %v, want Is errTruncated", err)
	}
}
