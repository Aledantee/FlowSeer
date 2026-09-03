package snmp

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// Conformance pins for the raw fast path's off-spec boundary: the
// two ways a real device diverges from its MIB that the fused decode
// must route to the generic fallback, cataloged from user reports
// against other stacks (telegraf #14598, snmp_exporter #338,
// chemist/snmp #17). Both pins are differential: the raw path must
// produce byte-identical outcomes to the generic path over the same
// misbehaving agent.

// Covers conformance matrix row: raw-wrong-typed-column
//
// A Counter32-declared column served Gauge32-tagged must decline the
// fused arm and coerce through the generic decoder (values identical
// to BulkWalk + Column.Decode); an Integer32-declared column served
// OctetString-tagged must surface the same ErrTypeMismatch the generic
// path surfaces. A correctly-typed sibling instance pins the fused arm
// from the accepting side.
func TestRawWalk_WrongTypedColumn_FallsBackToGenericCoercion(t *testing.T) {
	root := MustOID(1, 3, 6, 1, 9999, 1)
	cntCol := NewColumn[uint32](root.Child(1), KindCounter32, DecodeUint32)
	intCol := NewColumn[int32](root.Child(2), KindInteger32, DecodeInt32)

	oid11 := root.Child(1).Child(1)
	oid12 := root.Child(1).Child(2)
	oid13 := root.Child(1).Child(3)
	oid21 := root.Child(2).Child(1)
	entries := []mibEntry{
		// Wrong tag: Gauge32 where the MIB declares Counter32 — the
		// coercion table accepts it, so the walk must keep working.
		{oid11, Gauge32Var{Header: Header{OID: oid11, Kind: KindGauge32}, Value: 100}},
		{oid12, Gauge32Var{Header: Header{OID: oid12, Kind: KindGauge32}, Value: 200}},
		// Correct tag: the fused arm must accept this one.
		{oid13, Counter32Var{Header: Header{OID: oid13, Kind: KindCounter32}, Value: 300}},
		// Wrong tag with no coercion: OctetString where Integer32 is
		// declared — both paths must fail with ErrTypeMismatch.
		{oid21, octet(oid21, "oops")},
	}

	// Generic arm: BulkWalk + Column.Decode, the pre-fast-path behavior.
	type outcome struct {
		oid OID
		val uint64
		err error
	}
	decodeAll := func(get func(yield func(OID, VarBind) bool)) []outcome {
		var out []outcome
		get(func(idx OID, vb VarBind) bool {
			var o outcome
			o.oid = idx
			switch {
			case idx.HasPrefix(cntCol.OID()):
				v, err := cntCol.Decode(vb)
				o.val, o.err = uint64(v), err
			case idx.HasPrefix(intCol.OID()):
				v, err := intCol.Decode(vb)
				o.val, o.err = uint64(v), err
			default:
				t.Fatalf("unexpected OID %s", idx)
			}
			out = append(out, o)
			return true
		})
		return out
	}

	agentGen := startMIBAgent(t, entries, mibBehavior{})
	sessGen := dialNative(t, agentGen, V2c)
	w := sessGen.BulkWalk(context.Background(), root)
	generic := decodeAll(func(yield func(OID, VarBind) bool) {
		for idx, vb := range w.Iter() {
			if IsException(vb) {
				continue
			}
			if !yield(idx, vb) {
				return
			}
		}
	})
	if err := w.Err(); err != nil {
		t.Fatalf("generic walk err: %v", err)
	}

	// Raw arm: BulkWalkRaw with the generated-walker decode shape —
	// fused primitive first, rv.Decode() + Column.Decode fallback.
	agentRaw := startMIBAgent(t, entries, mibBehavior{})
	sessRaw := dialNative(t, agentRaw, V2c)
	fusedAccepts, fusedDeclines := 0, 0
	rw := sessRaw.BulkWalkRaw(context.Background(), root)
	raw := decodeAll(func(yield func(OID, VarBind) bool) {
		for rv := range rw.Iter() {
			if rv.VB != nil {
				t.Fatalf("v2c raw walk delivered pre-decoded varbind for %x", rv.OID)
			}
			// Both walk engines yield the terminal EndOfMibView marker;
			// it is not a column value, so keep it out of the fused
			// accept/decline tally and the outcome comparison (the
			// generic arm filters it via the same guard below).
			if rv.exceptionTag() != 0 {
				continue
			}
			if v, ok := RawCounter32(rv); ok {
				fusedAccepts++
				oid, err := decodeOID(rv.OID)
				if err != nil {
					t.Fatal(err)
				}
				if !yield(oid, Counter32Var{Header: Header{OID: oid, Kind: KindCounter32}, Value: v}) {
					return
				}
				continue
			}
			fusedDeclines++
			vb, err := rv.Decode()
			if err != nil {
				t.Fatalf("fallback decode: %v", err)
			}
			if !yield(vb.GetHeader().OID, vb) {
				return
			}
		}
	})
	if err := rw.Err(); err != nil {
		t.Fatalf("raw walk err: %v", err)
	}

	if fusedAccepts != 1 {
		t.Fatalf("fused arm accepted %d varbinds, want exactly 1 (the correctly-typed Counter32)", fusedAccepts)
	}
	if fusedDeclines != 3 {
		t.Fatalf("fused arm declined %d varbinds, want 3 (two Gauge32-tagged + one OctetString-tagged)", fusedDeclines)
	}
	if len(generic) != len(raw) {
		t.Fatalf("outcome counts diverge: generic=%d raw=%d", len(generic), len(raw))
	}
	for i := range generic {
		g, r := generic[i], raw[i]
		if !g.oid.Equal(r.oid) || g.val != r.val || (g.err == nil) != (r.err == nil) {
			t.Fatalf("outcome %d diverges: generic=%+v raw=%+v", i, g, r)
		}
	}
	// The no-coercion case must be the same typed error on both arms.
	last := len(generic) - 1
	if !errors.Is(generic[last].err, ErrTypeMismatch) || !errors.Is(raw[last].err, ErrTypeMismatch) {
		t.Fatalf("OctetString-for-Integer32 errs = generic %v / raw %v, want Is ErrTypeMismatch", generic[last].err, raw[last].err)
	}
}

// startRawByteAgent is a UDP responder that replies to every request
// with a hand-assembled datagram from build(requestID) — the seam for
// wire shapes the production encoder cannot emit (here: a non-canonical
// OID arc).
func startRawByteAgent(t *testing.T, build func(reqID int32) []byte) string {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("raw agent listen: %v", err)
	}
	go func() {
		buf := make([]byte, maxUDPPayload)
		for {
			n, src, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			req, derr := decodeMessage(buf[:n])
			if derr != nil {
				continue
			}
			_, _ = conn.WriteToUDP(build(req.pdu.requestID), src)
		}
	}()
	t.Cleanup(func() { _ = conn.Close() })
	return conn.LocalAddr().String()
}

// Covers conformance matrix row: raw-noncanonical-oid-arc
//
// The response's first varbind name encodes its index sub-identifier
// as 0x80 0x01 — a redundant leading continuation octet, decodable but
// non-canonical. The mirror validation must refuse raw delivery so the
// read loop decodes eagerly: the walk yields the varbind pre-decoded
// (VB non-nil), with the same OID and value the generic path produces,
// and no error.
func TestRawWalk_NonCanonicalOIDArc_EagerFallback(t *testing.T) {
	root := MustOID(1, 3, 6, 1, 9999, 2)
	outside := MustOID(1, 3, 6, 1, 9998)

	// name = root + col arc 1 + index arc 1 padded to 0x80 0x01.
	paddedName := append(append([]byte{}, encodeOIDContent(root)...), 0x01, 0x80, 0x01)
	if err := validateRawNameOID(paddedName); !errors.Is(err, errNonCanonicalOID) {
		t.Fatalf("fixture sanity: padded name validates as %v, want errNonCanonicalOID", err)
	}

	build := func(reqID int32) []byte {
		var vb1 []byte
		vb1 = appendTLV(vb1, tagOID, paddedName)
		vb1 = appendUint(vb1, tagCounter32, 42)
		var vb2 []byte
		vb2 = appendTLV(vb2, tagOID, encodeOIDContent(outside))
		vb2 = appendNull(vb2)
		vbl := appendSequence(tagSequence, append(appendSequence(tagSequence, vb1), appendSequence(tagSequence, vb2)...))

		var p []byte
		p = appendInt(p, int64(reqID))
		p = appendInt(p, 0)
		p = appendInt(p, 0)
		p = append(p, vbl...)

		var body []byte
		body = appendInt(body, int64(wireVersionV2c))
		body = appendOctetString(body, tagOctetString, []byte("public"))
		body = append(body, appendSequence(byte(pduGetResponse), p)...)
		return appendSequence(tagSequence, body)
	}
	addr := startRawByteAgent(t, build)

	sess, err := NewSession(context.Background(), addr, V2c,
		WithCommunity("public"),
		WithMinSecurity(MinSecurityNoAuth),
		WithTimeout(time.Second),
		WithRetries(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	wantOID := root.Child(1).Child(1)

	// Raw arm: must degrade to pre-decoded delivery, same data.
	var got []RawVarBind
	rw := sess.BulkWalkRaw(context.Background(), root)
	for rv := range rw.Iter() {
		got = append(got, rv)
	}
	if err := rw.Err(); err != nil {
		t.Fatalf("raw walk err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("raw walk yielded %d varbinds, want 1", len(got))
	}
	if got[0].VB == nil {
		t.Fatal("non-canonical response was delivered raw; want eager-decoded fallback (VB non-nil)")
	}
	c, ok := got[0].VB.(Counter32Var)
	if !ok || c.Value != 42 || !c.OID.Equal(wantOID) {
		t.Fatalf("fallback varbind = %#v, want Counter32 42 at %s", got[0].VB, wantOID)
	}

	// Generic arm over the same agent: identical yield.
	w := sess.BulkWalk(context.Background(), root)
	n := 0
	for idx, vb := range w.Iter() {
		n++
		g, ok := vb.(Counter32Var)
		if !ok || g.Value != 42 || !idx.Equal(wantOID) {
			t.Fatalf("generic varbind = %#v at %s, want Counter32 42 at %s", vb, idx, wantOID)
		}
	}
	if err := w.Err(); err != nil {
		t.Fatalf("generic walk err: %v", err)
	}
	if n != 1 {
		t.Fatalf("generic walk yielded %d varbinds, want 1", n)
	}
}
