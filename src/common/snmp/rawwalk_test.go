package snmp

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"testing"
)

// TestOID_WireByteOrder_Property pins the invariant the raw walk engine
// depends on: for canonically-encoded OIDs, lexicographic byte order of
// the BER content octets equals numeric arc order, and byte HasPrefix
// equals arc HasPrefix. Deterministic PRNG; arcs are drawn across all
// base-128 length classes (1..5 octets).
func TestOID_WireByteOrder_Property(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	randArc := func() uint32 {
		switch rng.Intn(5) {
		case 0:
			return uint32(rng.Intn(128)) // 1 octet
		case 1:
			return uint32(128 + rng.Intn(16384-128)) // 2 octets
		case 2:
			return uint32(16384 + rng.Intn(1<<21-16384)) // 3 octets
		case 3:
			return uint32(1<<21 + rng.Intn(1<<28-1<<21)) // 4 octets
		default:
			return uint32(1<<28) + uint32(rng.Int63n(1<<32-1<<28)) // 5 octets
		}
	}
	randOID := func() OID {
		n := 2 + rng.Intn(12)
		subs := make([]uint32, n)
		subs[0] = uint32(rng.Intn(3))
		if subs[0] <= 1 {
			subs[1] = uint32(rng.Intn(40))
		} else {
			subs[1] = randArc()
		}
		for i := 2; i < n; i++ {
			subs[i] = randArc()
		}
		return MustOID(subs...)
	}
	sign := func(v int) int {
		switch {
		case v < 0:
			return -1
		case v > 0:
			return 1
		}
		return 0
	}
	for i := 0; i < 20000; i++ {
		a, b := randOID(), randOID()
		if got, want := sign(cmpOIDWire(a.WireBytes(), b.WireBytes())), sign(a.Compare(b)); got != want {
			t.Fatalf("byte order diverges: %s vs %s (bytes %d, arcs %d)", a, b, got, want)
		}
		// Prefix equivalence: check b, a truncated-prefix of a, and a itself.
		p := a
		if a.Len() > 2 {
			p = MustOID(append([]uint32(nil), a.WireArcsForTest()[:2+rng.Intn(a.Len()-2)]...)...)
		}
		if got, want := bytes.HasPrefix(a.WireBytes(), p.WireBytes()), a.HasPrefix(p); got != want {
			t.Fatalf("prefix diverges: %s hasPrefix %s: bytes %v, arcs %v", a, p, got, want)
		}
	}
}

// WireArcsForTest exposes the arc slice for the property test's prefix
// construction (test-only helper).
func (o OID) WireArcsForTest() []uint32 { return o.subs }

// TestBulkWalkRaw_DifferentialWithBulkWalk pins the two walk engines to
// identical behavior: over the same agent, BulkWalkRaw must yield the
// same (OID, VarBind) sequence and the same terminal error as BulkWalk.
func TestBulkWalkRaw_DifferentialWithBulkWalk(t *testing.T) {
	scenarios := []struct {
		name     string
		rows     int
		behavior mibBehavior
	}{
		{"clean-walk", 7, mibBehavior{}},
		{"toobig-degrade", 5, mibBehavior{tooBigOver: 1}},
		{"eomv-mid-walk", 4, mibBehavior{
			nextOverride: func(_ OID, prev OID, n int) (OID, VarBind, bool) {
				if n >= 6 { // cut the walk short with an explicit marker
					return prev, EndOfMibViewVar{Header: Header{OID: prev, Kind: KindEndOfMibView}}, true
				}
				return OID{}, nil, false
			},
		}},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			root, entries := ifTable(sc.rows)

			agent1 := startMIBAgent(t, entries, sc.behavior)
			sess1 := dialNative(t, agent1, V2c)
			var genOIDs []OID
			var genVBs []VarBind
			w := sess1.BulkWalk(context.Background(), root)
			for idx, vb := range w.Iter() {
				genOIDs = append(genOIDs, idx)
				genVBs = append(genVBs, vb)
			}
			genErr := w.Err()

			agent2 := startMIBAgent(t, entries, sc.behavior)
			sess2 := dialNative(t, agent2, V2c)
			var rawOIDs []OID
			var rawVBs []VarBind
			rw := sess2.BulkWalkRaw(context.Background(), root)
			for rv := range rw.Iter() {
				if rv.VB != nil {
					t.Fatalf("v2c raw walk yielded pre-decoded varbind: %#v", rv.VB)
				}
				vb, err := rv.Decode()
				if err != nil {
					t.Fatalf("raw decode: %v", err)
				}
				rawOIDs = append(rawOIDs, vb.GetHeader().OID)
				rawVBs = append(rawVBs, vb)
			}
			rawErr := rw.Err()

			if (genErr == nil) != (rawErr == nil) {
				t.Fatalf("terminal errors diverge: generic=%v raw=%v", genErr, rawErr)
			}
			if len(genOIDs) != len(rawOIDs) {
				t.Fatalf("yield counts diverge: generic=%d raw=%d", len(genOIDs), len(rawOIDs))
			}
			for i := range genOIDs {
				if !genOIDs[i].Equal(rawOIDs[i]) {
					t.Fatalf("OID %d diverges: generic=%s raw=%s", i, genOIDs[i], rawOIDs[i])
				}
				if genVBs[i].GetHeader().Kind != rawVBs[i].GetHeader().Kind {
					t.Fatalf("kind %d diverges: generic=%v raw=%v", i, genVBs[i].GetHeader().Kind, rawVBs[i].GetHeader().Kind)
				}
			}
		})
	}
}

// TestBulkWalkRaw_V1Rejected mirrors BulkWalk's v1 contract.
func TestBulkWalkRaw_V1Rejected(t *testing.T) {
	root, entries := ifTable(3)
	agent := startMIBAgent(t, entries, mibBehavior{})
	sess := dialNative(t, agent, V1)
	rw := sess.BulkWalkRaw(context.Background(), root)
	for range rw.Iter() {
		t.Fatal("v1 raw walk yielded an item")
	}
	if err := rw.Err(); !errors.Is(err, ErrBulkUnsupported) {
		t.Fatalf("v1 BulkWalkRaw err = %v, want Is ErrBulkUnsupported", err)
	}
}

// TestValidateRawValue_MirrorsDecodeValue pins validateRawValue to
// decodeValue's accept/reject decision, per tag, over crafted and
// randomly generated content — so raw delivery can never accept a value
// the eager path would have dropped the datagram for.
func TestValidateRawValue_MirrorsDecodeValue(t *testing.T) {
	oid := MustOID(1, 3, 6, 1)
	tags := []byte{
		tagInteger, tagOctetString, tagNull, tagOID, tagBitString,
		tagIPAddress, tagCounter32, tagGauge32, tagTimeTicks, tagOpaque,
		tagNsapAddress, tagCounter64, tagUinteger32,
		tagNoSuchObject, tagNoSuchInstance, tagEndOfMibView,
		0x07, 0x1E, 0x48, // unknown tags
	}
	check := func(tag byte, content []byte) {
		t.Helper()
		_, dErr := decodeValue(oid, tag, content)
		vErr := validateRawValue(tag, content)
		if (dErr == nil) != (vErr == nil) {
			t.Fatalf("mirror diverges for tag 0x%02x content %x: decode=%v validate=%v", tag, content, dErr, vErr)
		}
	}
	// Crafted edges.
	crafted := [][]byte{
		nil,
		{},
		{0x00},
		{0x7f},
		{0x80},
		{0xff},
		{0x01, 0x02, 0x03, 0x04},
		{0x00, 0xff, 0xff, 0xff, 0xff},
		{0x01, 0x00, 0x00, 0x00, 0x00}, // > uint32 for 32-bit kinds
		{0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},       // 8 octets
		{0x00, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, // 9 octets w/ pad
		{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},                        // > 8 octets
		{10, 0, 0, 1},
		{1, 2, 3},                                  // IP shapes
		{0x9f, 0x78, 0x04, 0x40, 0x49, 0x0f, 0xdb}, // opaque float
		{0x9f, 0x78, 0x03, 0x40, 0x49, 0x0f},       // corrupt opaque float
		{0x2b, 0x06, 0x01},
		{0xff, 0xff, 0xff, 0xff, 0xff}, // OID-ish
	}
	for _, tag := range tags {
		for _, c := range crafted {
			check(tag, c)
		}
	}
	// Random sweep.
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 5000; i++ {
		tag := tags[rng.Intn(len(tags))]
		content := make([]byte, rng.Intn(12))
		rng.Read(content)
		check(tag, content)
	}
}

// TestValidateRawNameOID_NonCanonical pins the padded-arc refusal that
// forces the eager fallback for off-spec agents.
func TestValidateRawNameOID_NonCanonical(t *testing.T) {
	if err := validateRawNameOID([]byte{0x2b, 0x80, 0x01}); !errors.Is(err, errNonCanonicalOID) {
		t.Fatalf("padded arc err = %v, want errNonCanonicalOID", err)
	}
	if err := validateRawNameOID(MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 1, 128).WireBytes()); err != nil {
		t.Fatalf("canonical multi-octet arc rejected: %v", err)
	}
	if err := validateRawNameOID(nil); err != nil {
		t.Fatalf("zero-length sentinel rejected: %v", err)
	}
}

// TestRawPrimitives_FusedAndDecline covers the guarded fast-path
// contract: exact-tag values decode; off-spec tags, exceptions, and
// pre-decoded varbinds decline with ok=false (never an error).
func TestRawPrimitives_FusedAndDecline(t *testing.T) {
	mk := func(v VarBind) RawVarBind {
		enc, err := encodeVarBind(v)
		if err != nil {
			t.Fatal(err)
		}
		vbContent, _, err := parseSequence(enc, tagSequence, 1)
		if err != nil {
			t.Fatal(err)
		}
		_, nameContent, nameUsed, err := parseTLV(vbContent)
		if err != nil {
			t.Fatal(err)
		}
		valTag, valContent, _, err := parseTLV(vbContent[nameUsed:])
		if err != nil {
			t.Fatal(err)
		}
		return RawVarBind{OID: nameContent, Tag: valTag, Value: valContent}
	}
	oid := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10, 7)
	hdr := func(k Kind) Header { return Header{OID: oid, Kind: k} }

	if v, ok := RawCounter32(mk(Counter32Var{Header: hdr(KindCounter32), Value: 4294967295})); !ok || v != 4294967295 {
		t.Fatalf("RawCounter32 = %d, %v", v, ok)
	}
	if v, ok := RawInteger32(mk(Integer32Var{Header: hdr(KindInteger32), Value: -42})); !ok || v != -42 {
		t.Fatalf("RawInteger32 = %d, %v", v, ok)
	}
	if v, ok := RawGauge32(mk(Gauge32Var{Header: hdr(KindGauge32), Value: 7})); !ok || v != 7 {
		t.Fatalf("RawGauge32 = %d, %v", v, ok)
	}
	if v, ok := RawTimeTicks(mk(TimeTicksVar{Header: hdr(KindTimeTicks), Value: 12345})); !ok || v != 12345 {
		t.Fatalf("RawTimeTicks = %d, %v", v, ok)
	}
	if v, ok := RawCounter64(mk(Counter64Var{Header: hdr(KindCounter64), Value: 1 << 40})); !ok || v != 1<<40 {
		t.Fatalf("RawCounter64 = %d, %v", v, ok)
	}

	// Off-spec tag declines (Gauge32 where Counter32 expected, etc.).
	gauge := mk(Gauge32Var{Header: hdr(KindGauge32), Value: 1})
	if _, ok := RawCounter32(gauge); ok {
		t.Fatal("RawCounter32 accepted a Gauge32 tag")
	}
	if _, ok := RawInteger32(gauge); ok {
		t.Fatal("RawInteger32 accepted a Gauge32 tag")
	}
	// Exceptions decline everywhere.
	eomv := mk(EndOfMibViewVar{Header: hdr(KindEndOfMibView)})
	if _, ok := RawCounter32(eomv); ok {
		t.Fatal("RawCounter32 accepted endOfMibView")
	}
	if eomv.exceptionTag() != tagEndOfMibView {
		t.Fatalf("exceptionTag = 0x%02x", eomv.exceptionTag())
	}
	// Pre-decoded varbinds decline so callers use the generic arm.
	pre := RawVarBind{OID: oid.WireBytes(), VB: Counter32Var{Header: hdr(KindCounter32), Value: 9}}
	if _, ok := RawCounter32(pre); ok {
		t.Fatal("RawCounter32 accepted a pre-decoded varbind")
	}
	if vb, err := pre.Decode(); err != nil || vb.(Counter32Var).Value != 9 {
		t.Fatalf("pre-decoded Decode = %v, %v", vb, err)
	}
}

// TestDecodeIndexArcs covers the mid-OID suffix decode: no folding, no
// root validation, so an ifTable index of 7 round-trips as OID "7".
func TestDecodeIndexArcs(t *testing.T) {
	full := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10, 7, 300)
	entry := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)
	suffix := full.WireBytes()[len(entry.WireBytes()):]

	col, n, ok := RawFirstArc(suffix)
	if !ok || col != 10 {
		t.Fatalf("RawFirstArc = %d, %v", col, ok)
	}
	idx, err := DecodeIndexArcs(suffix[n:])
	if err != nil {
		t.Fatal(err)
	}
	if idx.Len() != 2 || idx.At(0) != 7 || idx.At(1) != 300 {
		t.Fatalf("index = %s", idx)
	}
	if empty, err := DecodeIndexArcs(nil); err != nil || empty.Len() != 0 {
		t.Fatalf("empty suffix = %s, %v", empty, err)
	}
}

// TestEncodeRequestFastEquivalence pins the single-buffer request
// encoder to the general encoder byte-for-byte across representative
// shapes, including long-form lengths and multi-varbind requests.
func TestEncodeRequestFastEquivalence(t *testing.T) {
	long := MustOID(1, 3, 6, 1, 4, 1, 2011, 5, 25, 31, 1, 1, 1, 1, 5, 67108864, 2999999999)
	var manyOIDs []OID
	for i := uint32(1); i <= 8; i++ {
		manyOIDs = append(manyOIDs, MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10, i))
	}
	msgs := []*message{
		{version: V2c, community: "public", pdu: pdu{typ: pduGetBulkRequest, requestID: 12345, maxRepetitions: 25, varbinds: nullVarbinds([]OID{MustOID(1, 3, 6, 1, 2, 1, 2, 2)})}},
		{version: V1, community: "private", pdu: pdu{typ: pduGetNextRequest, requestID: -1, varbinds: nullVarbinds([]OID{MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)})}},
		{version: V2c, community: "c", pdu: pdu{typ: pduGetRequest, requestID: 2147483647, varbinds: nullVarbinds([]OID{long, MustOID(1, 3, 6, 1, 2, 1, 1, 3, 0), MustOID(2, 999, 3)})}},
		{version: V2c, community: string(make([]byte, 150)), pdu: pdu{typ: pduGetRequest, requestID: 7, varbinds: nullVarbinds(manyOIDs)}},
	}
	for i, m := range msgs {
		fast, ok := encodeRequestFast(m)
		if !ok {
			t.Fatalf("msg %d: fast path declined", i)
		}
		pduBytes, err := encodePDU(&m.pdu)
		if err != nil {
			t.Fatal(err)
		}
		var body []byte
		body = appendInt(body, int64(wireVersionFor(m.version)))
		body = appendOctetString(body, tagOctetString, []byte(m.community))
		body = append(body, pduBytes...)
		slow := appendSequence(tagSequence, body)
		if !bytes.Equal(fast, slow) {
			t.Fatalf("msg %d: fast/slow mismatch\nfast %x\nslow %x", i, fast, slow)
		}
	}
	// Shapes the fast path must decline.
	if _, ok := encodeRequestFast(&message{version: V2c, community: "c", pdu: pdu{typ: pduSetRequest, varbinds: nullVarbinds([]OID{long})}}); ok {
		t.Fatal("fast path accepted a Set")
	}
	if _, ok := encodeRequestFast(&message{version: V2c, community: "c", pdu: pdu{typ: pduGetRequest, varbinds: []VarBind{Integer32Var{Header: Header{OID: long, Kind: KindInteger32}, Value: 1}}}}); ok {
		t.Fatal("fast path accepted a non-null varbind")
	}
}

// TestOID_WireKey covers the key API the generated dispatch/tier maps
// are built on.
func TestOID_WireKey(t *testing.T) {
	o := MustOID(1, 3, 6, 1, 2, 1, 31, 1, 1, 1, 6)
	if o.WireKey() != string(o.WireBytes()) {
		t.Fatal("WireKey != string(WireBytes)")
	}
	col := NewColumn[uint32](o, KindCounter32, nil)
	if col.Key() != o.WireKey() {
		t.Fatal("Column.Key != OID.WireKey")
	}
	var empty OID
	if empty.WireKey() != "" || empty.WireBytes() != nil {
		t.Fatal("empty OID wire forms not empty")
	}
}
