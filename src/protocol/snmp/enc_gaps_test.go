package snmp

import (
	"net"
	"testing"
	"time"
)

// Encoding/type gap-closure pins (SNMP conformance hardening). These pin
// the tolerance-policy and code-change rows from Table 1. Each carries a
// provenance marker the corpus gate joins against.

// Covers conformance matrix row: enc-counter64-v1 (RFC 2576 §3; gosnmp no-check).
// An SNMPv1 message carrying a Counter64 varbind decodes deterministically
// — the value is preserved (Counter64Var, not type-confused) and the spec
// violation is recorded as a non-fatal decode warning, never a panic and never
// silently swallowed. A v2c message with the same varbind records no warning.
func TestEncCounter64InV1_DecodesWithWarning(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 31, 1, 1, 1, 6, 1) // ifHCInOctets.1
	const want = uint64(1)<<40 | 0x0123456789

	build := func(ver Version) *message {
		return &message{
			version:   ver,
			community: "public",
			pdu: pdu{
				typ:       pduGetResponse,
				requestID: 7,
				varbinds:  []VarBind{Counter64Var{Header: Header{OID: oid, Kind: KindCounter64}, Value: want}},
			},
		}
	}

	// v1: decodes, value preserved, exactly one Counter64-in-v1 warning.
	raw, err := encodeMessage(build(V1))
	if err != nil {
		t.Fatalf("encode v1: %v", err)
	}
	m, err := decodeMessage(raw)
	if err != nil {
		t.Fatalf("decode v1: %v", err)
	}
	if m.version != V1 {
		t.Fatalf("version = %s, want v1", m.version)
	}
	c64, ok := m.pdu.varbinds[0].(Counter64Var)
	if !ok {
		t.Fatalf("decoded %T, want Counter64Var (no type-confusion)", m.pdu.varbinds[0])
	}
	if c64.Value != want {
		t.Fatalf("decoded value = %d, want %d (data must be preserved)", c64.Value, want)
	}
	if len(m.warnings) != 1 {
		t.Fatalf("v1 warnings = %d, want 1 (Counter64-in-v1)", len(m.warnings))
	}

	// v2c: same varbind is legal — no warning.
	raw2, err := encodeMessage(build(V2c))
	if err != nil {
		t.Fatalf("encode v2c: %v", err)
	}
	m2, err := decodeMessage(raw2)
	if err != nil {
		t.Fatalf("decode v2c: %v", err)
	}
	if len(m2.warnings) != 0 {
		t.Fatalf("v2c warnings = %d, want 0", len(m2.warnings))
	}
}

// Covers conformance matrix row: enc-maxrep-signed (RFC 3416; gosnmp #293).
// GETBULK max-repetitions in 128..255 must encode as a positive (unsigned)
// integer, never sign-extend to a negative value on the wire.
func TestEncMaxRepSigned(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)
	for _, reps := range []int{128, 200, 255, 32767} {
		m := &message{
			version:   V2c,
			community: "public",
			pdu: pdu{
				typ:            pduGetBulkRequest,
				requestID:      1,
				nonRepeaters:   0,
				maxRepetitions: reps,
				varbinds:       []VarBind{NullVar{Header: Header{OID: oid, Kind: KindNull}}},
			},
		}
		raw, err := encodeMessage(m)
		if err != nil {
			t.Fatalf("encode reps=%d: %v", reps, err)
		}
		got, err := decodeMessage(raw)
		if err != nil {
			t.Fatalf("decode reps=%d: %v", reps, err)
		}
		if got.pdu.maxRepetitions != reps {
			t.Fatalf("maxRepetitions round-trip = %d, want %d (sign error?)", got.pdu.maxRepetitions, reps)
		}
		if got.pdu.maxRepetitions < 0 {
			t.Fatalf("maxRepetitions decoded negative (%d) for reps=%d", got.pdu.maxRepetitions, reps)
		}
	}
}

// Covers conformance matrix row: enc-timeticks-range (RFC 2578). A TimeTicks
// value carried in 5+ content octets (out of the 32-bit range) is masked to the
// low 32 bits — TimeTicks wraps mod 2^32 — rather than rejected. Scoped to
// TimeTicks; a genuinely malformed >8-octet value still errors.
func TestEncTimeTicksRange(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 3, 0) // sysUpTime.0
	// 5-octet content 0x01_00_00_00_2A = 2^32 + 42 -> masks to 42.
	vb, err := decodeValue(oid, tagTimeTicks, []byte{0x01, 0x00, 0x00, 0x00, 0x2a})
	if err != nil {
		t.Fatalf("decode 5-octet TimeTicks: %v", err)
	}
	tt, ok := vb.(TimeTicksVar)
	if !ok {
		t.Fatalf("decoded %T, want TimeTicksVar", vb)
	}
	if tt.Value != 42 {
		t.Fatalf("masked TimeTicks = %d, want 42 (low 32 bits of 2^32+42)", tt.Value)
	}
	// A genuinely malformed >8 significant octets still errors (no silent mask).
	if _, err := decodeValue(oid, tagTimeTicks, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}); err == nil {
		t.Fatal("9-octet TimeTicks decoded without error, want overflow error")
	}
}

// Covers conformance matrix row: enc-bits-padding (RFC 2579). A BITS value
// (BIT STRING form) with trailing zero padding or a short value is tolerated:
// the raw content is preserved, not rejected.
func TestEncBitsPadding(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 99, 1, 0)
	for _, content := range [][]byte{
		{0xA0, 0x00, 0x00}, // trailing zero padding
		{0x80},             // short single-octet value
		{},                 // empty
	} {
		vb, err := decodeValue(oid, tagBitString, content)
		if err != nil {
			t.Fatalf("decode BITS %x: %v", content, err)
		}
		bs, ok := vb.(BitStringVar)
		if !ok {
			t.Fatalf("decoded %T, want BitStringVar", vb)
		}
		if len(bs.Value) != len(content) {
			t.Fatalf("BITS value len = %d, want %d (raw bytes preserved)", len(bs.Value), len(content))
		}
	}
}

// Covers conformance matrix row: enc-zerolen-int (gosnmp #241) — unsigned
// leniency arm. A zero-length unsigned counter (e.g. Counter32 02-style empty
// content) decodes to 0 rather than erroring, mirroring the recorded leniency.
// (The signed arm, which errors, is pinned in ber_test.go.)
func TestEncZeroLenIntUnsigned(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10, 1)
	for _, tag := range []byte{tagCounter32, tagGauge32, tagTimeTicks} {
		vb, err := decodeValue(oid, tag, []byte{})
		if err != nil {
			t.Fatalf("decode zero-length tag 0x%02x: %v", tag, err)
		}
		got, err := DecodeUint32(vb)
		if err != nil {
			t.Fatalf("DecodeUint32 zero-length tag 0x%02x: %v", tag, err)
		}
		if got != 0 {
			t.Fatalf("zero-length tag 0x%02x decoded %d, want 0", tag, got)
		}
	}
}

// Covers conformance matrix row: enc-v1trap-spectrap (RFC 2576; gosnmp #182). A
// SNMPv1 enterpriseSpecific trap with specific-trap > 127 must translate to v2c
// without byte-truncating the specific value: snmpTrapOID = enterprise.0.specific.
func TestEncV1TrapSpecTrap(t *testing.T) {
	ts, addr := startTrapListener(t)
	enterprise := MustOID(1, 3, 6, 1, 4, 1, 9)
	const specific = 200 // > 127: must not truncate to a signed byte
	payload := []VarBind{
		OctetStringVar{Header: Header{OID: MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0), Kind: KindOctetString}, Value: []byte("d")},
	}
	sendDatagram(t, addr, buildV1Trap(t, "public", enterprise, net.IPv4(10, 0, 0, 1), 6, specific, 123, payload))

	tr, ok := nextTrap(ts, 2*time.Second)
	if !ok {
		t.Fatal("no v1 trap received")
	}
	trapOID, ok := tr.VarBinds[1].(ObjectIDVar)
	if !ok {
		t.Fatalf("vb1 = %#v, want snmpTrapOID.0", tr.VarBinds[1])
	}
	want := enterprise.Append(0, uint32(specific))
	if !trapOID.Value.Equal(want) {
		t.Fatalf("snmpTrapOID = %s, want %s (specific %d truncated?)", trapOID.Value, want, specific)
	}
}
