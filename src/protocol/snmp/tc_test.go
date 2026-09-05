package snmp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

func TestDecodeMacAddress(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1.2.1.2.2.1.6.1")
	vb := OctetStringVar{
		Header: Header{OID: oid, Kind: KindOctetString},
		Value:  []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
	}
	got, err := DecodeMacAddress(vb)
	if err != nil {
		t.Fatalf("DecodeMacAddress: %v", err)
	}
	want := net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	if !bytes.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// Wrong length.
	bad := OctetStringVar{Header: vb.Header, Value: []byte{0x00, 0x11, 0x22}}
	if _, err := DecodeMacAddress(bad); err == nil {
		t.Error("DecodeMacAddress: wrong-length input did not return an error")
	}

	// Wrong variant.
	if _, err := DecodeMacAddress(NullVar{Header: Header{OID: oid, Kind: KindNull}}); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("wrong-variant err = %v, want ErrTypeMismatch", err)
	}

	// Exception variant.
	if _, err := DecodeMacAddress(NoSuchInstanceVar{Header: Header{OID: oid, Kind: KindNoSuchInstance}}); !errors.Is(err, ErrException) {
		t.Errorf("exception variant err = %v, want ErrException", err)
	}
}

func TestDecodeDateAndTime_Short(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1.2.1.1.3.0")
	// 2026-05-12 14:30:45.5 UTC
	buf := make([]byte, 8)
	binary.BigEndian.PutUint16(buf[0:2], 2026)
	buf[2] = 5
	buf[3] = 12
	buf[4] = 14
	buf[5] = 30
	buf[6] = 45
	buf[7] = 5 // 5 deci-seconds = 500ms

	got, err := DecodeDateAndTime(OctetStringVar{
		Header: Header{OID: oid, Kind: KindOctetString},
		Value:  buf,
	})
	if err != nil {
		t.Fatalf("DecodeDateAndTime: %v", err)
	}
	want := time.Date(2026, time.May, 12, 14, 30, 45, int(500*time.Millisecond), time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestDecodeDateAndTime_LongWithOffset(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1.2.1.1.3.0")
	buf := make([]byte, 11)
	binary.BigEndian.PutUint16(buf[0:2], 2026)
	buf[2] = 5
	buf[3] = 12
	buf[4] = 9
	buf[5] = 0
	buf[6] = 0
	buf[7] = 0
	buf[8] = '+'
	buf[9] = 2 // +02:00
	buf[10] = 0

	got, err := DecodeDateAndTime(OctetStringVar{
		Header: Header{OID: oid, Kind: KindOctetString},
		Value:  buf,
	})
	if err != nil {
		t.Fatalf("DecodeDateAndTime: %v", err)
	}
	// Compare as instants: 2026-05-12 09:00 +02:00 == 2026-05-12 07:00 UTC.
	wantUTC := time.Date(2026, time.May, 12, 7, 0, 0, 0, time.UTC)
	if !got.Equal(wantUTC) {
		t.Errorf("got %s (instant %s), want %s", got, got.UTC(), wantUTC)
	}
	// Offset should reflect the wire form.
	_, off := got.Zone()
	if off != 2*3600 {
		t.Errorf("zone offset = %d, want %d", off, 2*3600)
	}
}

func TestDecodeDateAndTime_NegativeOffset(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1")
	buf := make([]byte, 11)
	binary.BigEndian.PutUint16(buf[0:2], 2026)
	buf[2], buf[3], buf[4], buf[5], buf[6], buf[7] = 1, 1, 0, 0, 0, 0
	buf[8] = '-'
	buf[9] = 5
	buf[10] = 30
	got, err := DecodeDateAndTime(OctetStringVar{Header: Header{OID: oid, Kind: KindOctetString}, Value: buf})
	if err != nil {
		t.Fatalf("DecodeDateAndTime: %v", err)
	}
	_, off := got.Zone()
	if off != -(5*3600 + 30*60) {
		t.Errorf("zone offset = %d, want %d", off, -(5*3600 + 30*60))
	}
}

func TestDecodeDateAndTime_BadLength(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1")
	for _, n := range []int{0, 7, 9, 10, 12} {
		_, err := DecodeDateAndTime(OctetStringVar{Header: Header{OID: oid, Kind: KindOctetString}, Value: make([]byte, n)})
		if err == nil {
			t.Errorf("len %d: expected error", n)
		}
	}
}

func TestDecodeDateAndTime_BadDirection(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1")
	buf := make([]byte, 11)
	binary.BigEndian.PutUint16(buf[0:2], 2026)
	buf[2], buf[3] = 1, 1
	buf[8] = '*' // invalid direction
	if _, err := DecodeDateAndTime(OctetStringVar{Header: Header{OID: oid, Kind: KindOctetString}, Value: buf}); err == nil {
		t.Error("expected error for bad direction byte")
	}
}

// TestDecodeDateAndTime_FieldRanges pins RFC 2579 §2 field-range checks
// that the docstring promises and the code now enforces. Each subtest's
// expected error message names the offending field so a regression on
// either side (range loosened, or wrong field named) surfaces directly.
//
// Covers conformance matrix rows:
//   - RFC 2579 §2 / DateAndTime hour ∈ [0,23]
//   - RFC 2579 §2 / DateAndTime minute ∈ [0,59]
//   - RFC 2579 §2 / DateAndTime second ∈ [0,60]  (60 = leap-second)
//   - RFC 2579 §2 / DateAndTime deci-second ∈ [0,9]
//   - RFC 2579 §2 / DateAndTime hours-from-UTC ∈ [0,13]
//   - RFC 2579 §2 / DateAndTime minutes-from-UTC ∈ [0,59]
func TestDecodeDateAndTime_FieldRanges(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1")

	// shortBuf builds a valid 8-octet form: 2026-05-12 14:30:45.5 UTC.
	// Tests mutate one byte to push a single field out of range.
	shortBuf := func() []byte {
		b := make([]byte, 8)
		binary.BigEndian.PutUint16(b[0:2], 2026)
		b[2] = 5  // month
		b[3] = 12 // day
		b[4] = 14 // hour
		b[5] = 30 // minute
		b[6] = 45 // second
		b[7] = 5  // deci-second
		return b
	}
	// longBuf builds a valid 11-octet form by appending direction +
	// offset bytes to the short form. Used for offHours / offMins.
	longBuf := func() []byte {
		b := append(shortBuf(), 0, 0, 0)
		b[8] = '+'
		b[9] = 2 // hours from UTC
		b[10] = 0
		return b
	}

	decode := func(t *testing.T, buf []byte) error {
		t.Helper()
		_, err := DecodeDateAndTime(OctetStringVar{
			Header: Header{OID: oid, Kind: KindOctetString},
			Value:  buf,
		})
		return err
	}

	type rejectCase struct {
		name    string
		buf     []byte
		wantMsg string // field-name substring expected in the static message
		attrKey string // attribute carrying the offending value
		attrVal int    // expected attribute value
	}
	rejects := []rejectCase{
		{"hour = 24", func() []byte { b := shortBuf(); b[4] = 24; return b }(), "hour", "hour", 24},
		{"hour = 99", func() []byte { b := shortBuf(); b[4] = 99; return b }(), "hour", "hour", 99},
		{"minute = 60", func() []byte { b := shortBuf(); b[5] = 60; return b }(), "minute", "minute", 60},
		{"second = 61", func() []byte { b := shortBuf(); b[6] = 61; return b }(), "second", "second", 61},
		{"deci-second = 10", func() []byte { b := shortBuf(); b[7] = 10; return b }(), "deci-second", "deci_second", 10},
		{"deci-second = 255", func() []byte { b := shortBuf(); b[7] = 255; return b }(), "deci-second", "deci_second", 255},
		{"hours-from-UTC = 14 (+)", func() []byte { b := longBuf(); b[8] = '+'; b[9] = 14; return b }(), "hours-from-UTC", "hours_from_utc", 14},
		// Sign = '-' to confirm the check fires on the magnitude, not signed value.
		{"hours-from-UTC = 14 (-)", func() []byte { b := longBuf(); b[8] = '-'; b[9] = 14; return b }(), "hours-from-UTC", "hours_from_utc", 14},
		{"minutes-from-UTC = 60", func() []byte { b := longBuf(); b[10] = 60; return b }(), "minutes-from-UTC", "minutes_from_utc", 60},
	}
	for _, c := range rejects {
		t.Run("reject "+c.name, func(t *testing.T) {
			err := decode(t, c.buf)
			if err == nil {
				t.Fatalf("decode(%v) = nil error, want error", c.buf)
			}
			msg := err.Error()
			if !strings.Contains(msg, c.wantMsg) {
				t.Errorf("error %q does not mention %q", msg, c.wantMsg)
			}
			// The offending value moved from the message into a
			// structured attribute.
			if got := errs.Attributes(err)[c.attrKey]; got != c.attrVal {
				t.Errorf("error %s attr = %v, want %d", c.attrKey, got, c.attrVal)
			}
		})
	}

	type acceptCase struct {
		name string
		buf  []byte
	}
	accepts := []acceptCase{
		{"hour = 23 (upper)", func() []byte { b := shortBuf(); b[4] = 23; return b }()},
		{"minute = 59 (upper)", func() []byte { b := shortBuf(); b[5] = 59; return b }()},
		// 60 seconds is the leap-second representation; time.Date normalizes
		// to the next minute but the decoder accepts it without error.
		{"second = 60 (leap-second)", func() []byte { b := shortBuf(); b[6] = 60; return b }()},
		{"deci-second = 9 (upper)", func() []byte { b := shortBuf(); b[7] = 9; return b }()},
		{"hours-from-UTC = 13 (upper)", func() []byte { b := longBuf(); b[9] = 13; return b }()},
		{"minutes-from-UTC = 59 (upper)", func() []byte { b := longBuf(); b[10] = 59; return b }()},
		// Lower-bound pin: every field at its lowest valid value.
		{"lower bound 8-octet", func() []byte {
			b := make([]byte, 8)
			binary.BigEndian.PutUint16(b[0:2], 1)
			b[2] = 1 // month
			b[3] = 1 // day
			// hour, minute, second, deci-second all 0
			return b
		}()},
		// Upper-bound pin: every range-checked field at its highest valid value.
		{"upper bound 11-octet", func() []byte {
			b := make([]byte, 11)
			binary.BigEndian.PutUint16(b[0:2], 9999)
			b[2] = 12
			b[3] = 31
			b[4] = 23
			b[5] = 59
			b[6] = 59 // 59 not 60 -- avoid leap-second normalization here
			b[7] = 9
			b[8] = '+'
			b[9] = 13
			b[10] = 59
			return b
		}()},
	}
	for _, c := range accepts {
		t.Run("accept "+c.name, func(t *testing.T) {
			if err := decode(t, c.buf); err != nil {
				t.Errorf("decode(%v) error: %v", c.buf, err)
			}
		})
	}
}

// Covers conformance matrix row: enc-dateandtime-lens (snmp_exporter
// #321). DateAndTime is valid only at 8 (UTC) or 11 (with offset) octets.
// A 0-octet (unknown/empty) value and a 7-octet (truncated) value — and
// any other off-length — are rejected with the length-class error BEFORE
// any field is indexed, so a short buffer cannot panic. This length class
// is handled distinctly from the field-range rejections pinned in
// TestDecodeDateAndTime_FieldRanges (which name the offending field).
func TestEncDateAndTimeLens(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1")
	for _, n := range []int{0, 7, 1, 9, 10, 12, 16} {
		t.Run(fmt.Sprintf("len-%d", n), func(t *testing.T) {
			_, err := DecodeDateAndTime(OctetStringVar{
				Header: Header{OID: oid, Kind: KindOctetString},
				Value:  make([]byte, n),
			})
			if err == nil {
				t.Fatalf("len %d: expected length error, got nil", n)
			}
			// The length-class error is distinct from the field-range
			// errors: it names neither a field nor an attribute value.
			if !strings.Contains(err.Error(), "8 or 11 octets") {
				t.Fatalf("len %d: error %q, want length-class error mentioning %q", n, err.Error(), "8 or 11 octets")
			}
		})
	}
}

func TestDecodeTruthValue(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1")
	h := Header{OID: oid, Kind: KindInteger32}

	for _, tc := range []struct {
		val     int32
		want    bool
		wantErr bool
	}{
		{1, true, false},
		{2, false, false},
		{0, false, true},
		{3, false, true},
		{-1, false, true},
	} {
		got, err := DecodeTruthValue(Integer32Var{Header: h, Value: tc.val})
		if tc.wantErr {
			if err == nil {
				t.Errorf("val=%d: expected error", tc.val)
			}
			continue
		}
		if err != nil {
			t.Errorf("val=%d: %v", tc.val, err)
			continue
		}
		if got != tc.want {
			t.Errorf("val=%d: got %v, want %v", tc.val, got, tc.want)
		}
	}

	// Type mismatch.
	if _, err := DecodeTruthValue(OctetStringVar{Header: Header{OID: oid, Kind: KindOctetString}}); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("wrong-variant err = %v, want ErrTypeMismatch", err)
	}
	// Exception.
	if _, err := DecodeTruthValue(EndOfMibViewVar{Header: Header{OID: oid, Kind: KindEndOfMibView}}); !errors.Is(err, ErrException) {
		t.Errorf("exception err = %v, want ErrException", err)
	}
}

func TestDecodeRowStatus(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1")
	h := Header{OID: oid, Kind: KindInteger32}

	cases := []struct {
		val  int32
		want RowStatus
	}{
		{1, RowStatusActive},
		{2, RowStatusNotInService},
		{3, RowStatusNotReady},
		{4, RowStatusCreateAndGo},
		{5, RowStatusCreateAndWait},
		{6, RowStatusDestroy},
	}
	for _, c := range cases {
		got, err := DecodeRowStatus(Integer32Var{Header: h, Value: c.val})
		if err != nil {
			t.Errorf("val=%d: %v", c.val, err)
			continue
		}
		if got != c.want {
			t.Errorf("val=%d: got %v, want %v", c.val, got, c.want)
		}
		if got.String() == "" {
			t.Errorf("val=%d: String() returned empty", c.val)
		}
	}

	// Out of range.
	if _, err := DecodeRowStatus(Integer32Var{Header: h, Value: 0}); err == nil {
		t.Error("expected error for RowStatus=0")
	}
	if _, err := DecodeRowStatus(Integer32Var{Header: h, Value: 7}); err == nil {
		t.Error("expected error for RowStatus=7")
	}

	// RowStatus.String() falls through to numeric for unknown values.
	if s := RowStatus(99).String(); s == "active" {
		t.Errorf("unknown RowStatus rendered as %q", s)
	}
}

func TestDecodeDisplayString(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1.2.1.1.1.0")
	got, err := DecodeDisplayString(OctetStringVar{
		Header: Header{OID: oid, Kind: KindOctetString},
		Value:  []byte("Cisco IOS Software, Version 15.2"),
	})
	if err != nil {
		t.Fatalf("DecodeDisplayString: %v", err)
	}
	if got != "Cisco IOS Software, Version 15.2" {
		t.Errorf("got %q", got)
	}

	// Covers conformance matrix row: RFC 2579 §2 / DisplayString accepts
	// UTF-8. RFC 2579 restricts DisplayString to NVT ASCII but real-world
	// agents emit UTF-8 in interface descriptions and contact strings;
	// the decoder accepts both and returns bytes verbatim per its docstring.
	const utf8Sample = "Café — interface ✓"
	got, err = DecodeDisplayString(OctetStringVar{
		Header: Header{OID: oid, Kind: KindOctetString},
		Value:  []byte(utf8Sample),
	})
	if err != nil {
		t.Fatalf("DecodeDisplayString(utf-8): %v", err)
	}
	if got != utf8Sample {
		t.Errorf("UTF-8 round-trip: got %q, want %q", got, utf8Sample)
	}

	if _, err := DecodeDisplayString(NoSuchObjectVar{Header: Header{OID: oid, Kind: KindNoSuchObject}}); !errors.Is(err, ErrException) {
		t.Errorf("exception err = %v, want ErrException", err)
	}
}

func TestDecodePhysAddress_DefensiveCopy(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1")
	src := []byte{1, 2, 3, 4}
	vb := OctetStringVar{Header: Header{OID: oid, Kind: KindOctetString}, Value: src}
	got, err := DecodePhysAddress(vb)
	if err != nil {
		t.Fatalf("DecodePhysAddress: %v", err)
	}
	got[0] = 0xff
	if src[0] != 1 {
		t.Errorf("DecodePhysAddress returned aliased slice (src mutated)")
	}

	// Covers conformance matrix row: RFC 2579 §2 / PhysAddress arbitrary
	// length. Nil-valued OctetStringVar returns a non-nil zero-length
	// slice so callers do not have to nil-check before iterating.
	got, err = DecodePhysAddress(OctetStringVar{Header: Header{OID: oid, Kind: KindOctetString}, Value: nil})
	if err != nil {
		t.Fatalf("DecodePhysAddress(nil): %v", err)
	}
	if got == nil {
		t.Error("DecodePhysAddress(nil) returned nil slice, want non-nil zero-length slice")
	}
	if len(got) != 0 {
		t.Errorf("DecodePhysAddress(nil) returned %d bytes, want 0", len(got))
	}

	// 20-octet round-trip onto a distinct backing slice. PhysAddress
	// per RFC 2579 has no fixed length cap; 20 octets exercises the
	// "arbitrary length" branch beyond the typical 6-octet MAC.
	long := make([]byte, 20)
	for i := range long {
		long[i] = byte(i)
	}
	got, err = DecodePhysAddress(OctetStringVar{Header: Header{OID: oid, Kind: KindOctetString}, Value: long})
	if err != nil {
		t.Fatalf("DecodePhysAddress(20-octet): %v", err)
	}
	if !bytes.Equal(got, long) {
		t.Errorf("20-octet round-trip mismatch: got %v, want %v", got, long)
	}
	if &got[0] == &long[0] {
		t.Error("DecodePhysAddress shared backing storage with input (no defensive copy)")
	}
}

func TestDecodeBITS_DefensiveCopy(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1")
	src := []byte{0x80, 0x40}
	vb := OctetStringVar{Header: Header{OID: oid, Kind: KindOctetString}, Value: src}
	got, err := DecodeBITS(vb)
	if err != nil {
		t.Fatalf("DecodeBITS: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Errorf("got %v, want %v", got, src)
	}
	got[0] = 0
	if src[0] != 0x80 {
		t.Errorf("DecodeBITS returned aliased slice")
	}

	// Covers conformance matrix row: RFC 2579 §2 / BITS encoding. Nil
	// input returns non-nil zero-length slice.
	got, err = DecodeBITS(OctetStringVar{Header: Header{OID: oid, Kind: KindOctetString}, Value: nil})
	if err != nil {
		t.Fatalf("DecodeBITS(nil): %v", err)
	}
	if got == nil {
		t.Error("DecodeBITS(nil) returned nil slice, want non-nil zero-length slice")
	}
	if len(got) != 0 {
		t.Errorf("DecodeBITS(nil) returned %d bytes, want 0", len(got))
	}

	// Leftmost-bit pin: bit 0 of the first octet (0x80 = 1000 0000)
	// round-trips verbatim — BITS is big-endian indexed from the
	// leftmost bit per RFC 2579, with the decoder leaving interpretation
	// to the caller.
	got, err = DecodeBITS(OctetStringVar{Header: Header{OID: oid, Kind: KindOctetString}, Value: []byte{0x80}})
	if err != nil {
		t.Fatalf("DecodeBITS({0x80}): %v", err)
	}
	if len(got) != 1 || got[0] != 0x80 {
		t.Errorf("DecodeBITS({0x80}) = %v, want [0x80] (leftmost-bit pin)", got)
	}
}

func TestTC_ExceptionMatrix(t *testing.T) {
	// Every TC decoder must surface ErrException for all three exception
	// variants. This pins the rule that exceptions are values on the
	// wire but become errors when a typed decoder is asked to extract a
	// Go value.
	oid, _ := ParseOID("1.3.6.1")
	exceptions := []VarBind{
		NoSuchObjectVar{Header: Header{OID: oid, Kind: KindNoSuchObject}},
		NoSuchInstanceVar{Header: Header{OID: oid, Kind: KindNoSuchInstance}},
		EndOfMibViewVar{Header: Header{OID: oid, Kind: KindEndOfMibView}},
	}
	decoders := []struct {
		name string
		fn   func(VarBind) error
	}{
		{"MacAddress", func(v VarBind) error { _, e := DecodeMacAddress(v); return e }},
		{"DateAndTime", func(v VarBind) error { _, e := DecodeDateAndTime(v); return e }},
		{"TruthValue", func(v VarBind) error { _, e := DecodeTruthValue(v); return e }},
		{"RowStatus", func(v VarBind) error { _, e := DecodeRowStatus(v); return e }},
		{"DisplayString", func(v VarBind) error { _, e := DecodeDisplayString(v); return e }},
		{"PhysAddress", func(v VarBind) error { _, e := DecodePhysAddress(v); return e }},
		{"BITS", func(v VarBind) error { _, e := DecodeBITS(v); return e }},
	}
	for _, d := range decoders {
		for _, v := range exceptions {
			err := d.fn(v)
			if !errors.Is(err, ErrException) {
				t.Errorf("%s(%T): err = %v, want ErrException", d.name, v, err)
			}
		}
	}
}
