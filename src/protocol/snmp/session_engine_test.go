package snmp

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestSession_AsSnmpInterface pins the contract that *session satisfies
// the Session interface. Caught at compile time; the declaration is
// explicit so a contract break shows here, not at the first caller.
func TestSession_AsSnmpInterface(_ *testing.T) {
	var _ Session = (*session)(nil)
}

// mibEntry is one (OID, value) the mock MIB agent serves.
type mibEntry struct {
	oid OID
	vb  VarBind
}

// mibBehavior customizes the mock agent's responses for edge-case tests.
type mibBehavior struct {
	respVersion   Version // version to stamp on the response (0 → echo request)
	respCommunity string  // community to stamp (empty → echo request)
	tooBigOver    int     // GetBulk returns tooBig when maxRepetitions exceeds this (0 → never)
	nextOverride  func(req OID, prev OID, n int) (OID, VarBind, bool)
}

// startMIBAgent serves a sorted MIB over GetNext/Get/GetBulk/Set, enough
// to drive Walk/BulkWalk and the polling ops against deterministic data.
func startMIBAgent(t *testing.T, entries []mibEntry, b mibBehavior) *mockAgent {
	t.Helper()
	sorted := append([]mibEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].oid.Compare(sorted[j].oid) < 0 })

	findExact := func(oid OID) (VarBind, bool) {
		for _, e := range sorted {
			if e.oid.Equal(oid) {
				return e.vb, true
			}
		}
		return nil, false
	}
	findNext := func(oid OID) (mibEntry, bool) {
		for _, e := range sorted {
			if e.oid.Compare(oid) > 0 {
				return e, true
			}
		}
		return mibEntry{}, false
	}

	return startMockAgent(t, func(req *message, _ *net.UDPAddr, send func(*message)) {
		resp := &message{
			version:   req.version,
			community: req.community,
			pdu:       pdu{typ: pduGetResponse, requestID: req.pdu.requestID},
		}
		if b.respVersion != 0 {
			resp.version = b.respVersion
		}
		if b.respCommunity != "" {
			resp.community = b.respCommunity
		}

		switch req.pdu.typ {
		case pduGetRequest:
			for _, rvb := range req.pdu.varbinds {
				oid := rvb.GetHeader().OID
				if vb, ok := findExact(oid); ok {
					resp.pdu.varbinds = append(resp.pdu.varbinds, vb)
				} else {
					resp.pdu.varbinds = append(resp.pdu.varbinds,
						NoSuchInstanceVar{Header: Header{OID: oid, Kind: KindNoSuchInstance}})
				}
			}
		case pduGetNextRequest:
			for _, rvb := range req.pdu.varbinds {
				oid := rvb.GetHeader().OID
				if e, ok := findNext(oid); ok {
					resp.pdu.varbinds = append(resp.pdu.varbinds, e.vb)
				} else {
					resp.pdu.varbinds = append(resp.pdu.varbinds,
						EndOfMibViewVar{Header: Header{OID: oid, Kind: KindEndOfMibView}})
				}
			}
		case pduGetBulkRequest:
			if b.tooBigOver > 0 && req.pdu.maxRepetitions > b.tooBigOver {
				resp.pdu.errorStatus = TooBig
				send(resp)
				return
			}
			cur := req.pdu.varbinds[0].GetHeader().OID
			for i := 0; i < req.pdu.maxRepetitions; i++ {
				e, ok := findNext(cur)
				if !ok {
					resp.pdu.varbinds = append(resp.pdu.varbinds,
						EndOfMibViewVar{Header: Header{OID: cur, Kind: KindEndOfMibView}})
					break
				}
				resp.pdu.varbinds = append(resp.pdu.varbinds, e.vb)
				cur = e.oid
			}
		case pduSetRequest:
			resp.pdu.varbinds = req.pdu.varbinds
		}
		send(resp)
	})
}

func octet(oid OID, s string) VarBind {
	return OctetStringVar{Header: Header{OID: oid, Kind: KindOctetString}, Value: []byte(s)}
}

func dialNative(t *testing.T, agent *mockAgent, version Version) Session {
	t.Helper()
	sess, err := NewSession(context.Background(), agent.addr.String(), version,
		WithCommunity("public"),
		WithMinSecurity(MinSecurityNoAuth),
		WithTimeout(time.Second),
		WithRetries(1),
	)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestSession_Get(t *testing.T) {
	sysDescrOID := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	sysNameOID := MustOID(1, 3, 6, 1, 2, 1, 1, 5, 0)
	agent := startMIBAgent(t, []mibEntry{
		{sysDescrOID, octet(sysDescrOID, "Router X")},
		{sysNameOID, octet(sysNameOID, "core-1")},
	}, mibBehavior{})
	sess := dialNative(t, agent, V2c)

	t.Run("single", func(t *testing.T) {
		vbs, err := sess.Get(context.Background(), []OID{sysDescrOID})
		if err != nil {
			t.Fatal(err)
		}
		if len(vbs) != 1 {
			t.Fatalf("want 1 vb, got %d", len(vbs))
		}
		os, ok := vbs[0].(OctetStringVar)
		if !ok || string(os.Value) != "Router X" {
			t.Fatalf("vb = %#v", vbs[0])
		}
	})
	t.Run("multi", func(t *testing.T) {
		vbs, err := sess.Get(context.Background(), []OID{sysDescrOID, sysNameOID})
		if err != nil || len(vbs) != 2 {
			t.Fatalf("vbs=%v err=%v", vbs, err)
		}
	})
	t.Run("missing instance", func(t *testing.T) {
		missing := MustOID(1, 3, 6, 1, 2, 1, 1, 9, 0)
		vbs, err := sess.Get(context.Background(), []OID{missing})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := vbs[0].(NoSuchInstanceVar); !ok {
			t.Fatalf("want NoSuchInstanceVar, got %#v", vbs[0])
		}
	})
}

func TestSession_GetNext(t *testing.T) {
	a := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	b := MustOID(1, 3, 6, 1, 2, 1, 1, 2, 0)
	agent := startMIBAgent(t, []mibEntry{{a, octet(a, "A")}, {b, octet(b, "B")}}, mibBehavior{})
	sess := dialNative(t, agent, V2c)

	vbs, err := sess.GetNext(context.Background(), []OID{a})
	if err != nil {
		t.Fatal(err)
	}
	if !vbs[0].GetHeader().OID.Equal(b) {
		t.Fatalf("GetNext(%s) = %s, want %s", a, vbs[0].GetHeader().OID, b)
	}
}

// ifTable returns a small ifDescr column table rooted under ifTable.
func ifTable(n int) (root OID, entries []mibEntry) {
	root = MustOID(1, 3, 6, 1, 2, 1, 2, 2)
	ifDescr := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 2)
	for i := 1; i <= n; i++ {
		oid := ifDescr.Append(uint32(i))
		entries = append(entries, mibEntry{oid, octet(oid, "eth")})
	}
	// A scalar outside the subtree, to prove the walk stops at the boundary.
	outside := MustOID(1, 3, 6, 1, 2, 1, 3, 1, 0)
	entries = append(entries, mibEntry{outside, octet(outside, "outside")})
	return root, entries
}

func TestSession_Walk_Completion(t *testing.T) {
	root, entries := ifTable(5)
	agent := startMIBAgent(t, entries, mibBehavior{})
	sess := dialNative(t, agent, V2c)

	count := 0
	w := sess.Walk(context.Background(), root)
	for oid, vb := range w.Iter() {
		if !oid.HasPrefix(root) {
			t.Fatalf("walk yielded out-of-subtree OID %s", oid)
		}
		if vb == nil {
			t.Fatal("nil varbind")
		}
		count++
	}
	if err := w.Err(); err != nil {
		t.Fatalf("walk err: %v", err)
	}
	if count != 5 {
		t.Fatalf("walked %d rows, want 5", count)
	}
}

func TestSession_BulkWalk_Completion(t *testing.T) {
	root, entries := ifTable(7)
	agent := startMIBAgent(t, entries, mibBehavior{})
	sess := dialNative(t, agent, V2c)

	count := 0
	w := sess.BulkWalk(context.Background(), root)
	for range w.Iter() {
		count++
	}
	if err := w.Err(); err != nil {
		t.Fatalf("bulkwalk err: %v", err)
	}
	if count != 7 {
		t.Fatalf("bulkwalked %d rows, want 7", count)
	}
}

func TestSession_Set(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 5, 0)
	agent := startMIBAgent(t, []mibEntry{{oid, octet(oid, "old")}}, mibBehavior{})
	sess := dialNative(t, agent, V2c)

	in := []VarBind{octet(oid, "new-name")}
	out, err := sess.Set(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if os, ok := out[0].(OctetStringVar); !ok || string(os.Value) != "new-name" {
		t.Fatalf("Set echo = %#v", out[0])
	}
}

func TestSession_Set_RejectsException(t *testing.T) {
	agent := startMIBAgent(t, nil, mibBehavior{})
	sess := dialNative(t, agent, V2c)
	oid := MustOID(1, 3, 6, 1)
	_, err := sess.Set(context.Background(), []VarBind{
		EndOfMibViewVar{Header: Header{OID: oid, Kind: KindEndOfMibView}},
	})
	if err == nil {
		t.Fatal("Set with exception varbind should error")
	}
}

func TestSession_CloseIdempotentAndRejects(t *testing.T) {
	agent := startMIBAgent(t, nil, mibBehavior{})
	sess := dialNative(t, agent, V2c)
	if err := sess.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("second Close should be nil: %v", err)
	}
	_, err := sess.Get(context.Background(), []OID{MustOID(1, 3, 6, 1)})
	if !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("after-close Get err = %v, want Is ErrSessionClosed", err)
	}
}

func TestSession_GetBulk_V1Rejected(t *testing.T) {
	agent := startMIBAgent(t, nil, mibBehavior{})
	sess := dialNative(t, agent, V1)
	_, err := sess.GetBulk(context.Background(), 0, 10, []OID{MustOID(1, 3, 6, 1)})
	if !errors.Is(err, ErrBulkUnsupported) {
		t.Fatalf("v1 GetBulk err = %v, want Is ErrBulkUnsupported", err)
	}
}

func TestSession_BulkWalk_V1FailsClearly(t *testing.T) {
	root, entries := ifTable(3)
	agent := startMIBAgent(t, entries, mibBehavior{})
	sess := dialNative(t, agent, V1)
	w := sess.BulkWalk(context.Background(), root)
	for range w.Iter() {
		t.Fatal("v1 BulkWalk should not yield rows")
	}
	if !errors.Is(w.Err(), ErrBulkUnsupported) {
		t.Fatalf("v1 BulkWalk err = %v, want Is ErrBulkUnsupported", w.Err())
	}
}

func TestSession_GetBulk_TooBigBackoff(t *testing.T) {
	root, entries := ifTable(3)
	// Agent rejects maxRepetitions > 10 with tooBig; GetBulk(50) must
	// halve down (50→25→12→6) until accepted.
	agent := startMIBAgent(t, entries, mibBehavior{tooBigOver: 10})
	sess := dialNative(t, agent, V2c)

	vbs, err := sess.GetBulk(context.Background(), 0, 50, []OID{root})
	if err != nil {
		t.Fatalf("GetBulk with backoff: %v", err)
	}
	if len(vbs) == 0 {
		t.Fatal("GetBulk returned no varbinds after backoff")
	}
}

// dialNativeShort dials with a short timeout and no retries, for tests that
// expect a request to time out (e.g. when every reply is dropped).
func dialNativeShort(t *testing.T, agent *mockAgent, version Version) Session {
	t.Helper()
	sess, err := NewSession(context.Background(), agent.addr.String(), version,
		WithCommunity("public"),
		WithMinSecurity(MinSecurityNoAuth),
		WithTimeout(150*time.Millisecond),
		WithRetries(0),
	)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// TestSession_CommunityMismatch verifies a reply whose community differs
// from the session's is dropped by the reactor read-loop before delivery:
// a forged-community datagram can neither satisfy nor fail
// the in-flight request, so with no genuine reply the request times out.
func TestSession_CommunityMismatch(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	agent := startMIBAgent(t, []mibEntry{{oid, octet(oid, "x")}}, mibBehavior{respCommunity: "wrong"})
	sess := dialNativeShort(t, agent, V2c)
	_, err := sess.Get(context.Background(), []OID{oid})
	if !errors.Is(err, errTimeout) {
		t.Fatalf("err = %v, want Is errTimeout (mismatched community dropped)", err)
	}
}

// TestSession_VersionMismatch verifies the read-loop likewise drops a reply
// carrying a different SNMP version than the session's.
func TestSession_VersionMismatch(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	agent := startMIBAgent(t, []mibEntry{{oid, octet(oid, "x")}}, mibBehavior{respVersion: V1})
	sess := dialNativeShort(t, agent, V2c)
	_, err := sess.Get(context.Background(), []OID{oid})
	if !errors.Is(err, errTimeout) {
		t.Fatalf("err = %v, want Is errTimeout (mismatched version dropped)", err)
	}
}

func TestDial_V3RequiresUSM(t *testing.T) {
	// v3 with no USM config is a usage error: v3 is implemented, but with
	// no credentials the session resolves to noAuthNoPriv, which the
	// default authNoPriv minimum-security floor rejects before any wire IO.
	_, err := NewSession(context.Background(), "127.0.0.1:161", V3)
	if err == nil {
		t.Fatal("v3 without USM should error")
	}
	if !strings.Contains(err.Error(), "security level below") {
		t.Fatalf("v3-without-USM error should be the min-security rejection, got: %v", err)
	}
}

func TestDial_RequiresVersion(t *testing.T) {
	_, err := NewSession(context.Background(), "127.0.0.1:161", VersionUnset)
	if err == nil {
		t.Fatal("Dial with VersionUnset should error")
	}
}
