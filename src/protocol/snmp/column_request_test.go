package snmp

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
)

// decodedSession hides the native capability, exercising third-party sessions.
type decodedSession struct{ Session }

func TestColumnRequestPaths(t *testing.T) {
	a, b := MustOID(1, 3, 6, 1, 4, 1, 999, 1), MustOID(1, 3, 6, 1, 4, 1, 999, 2)
	want := []VarBind{octet(a.Child(1), "a"), EndOfMibViewVar{Header: Header{OID: b, Kind: KindEndOfMibView}}, octet(a.Child(2), "b")}
	agent := startMockAgent(t, func(req *message, _ *net.UDPAddr, send func(*message)) {
		if req.pdu.typ != pduGetBulkRequest || req.pdu.maxRepetitions != 2 || len(req.pdu.varbinds) != 2 {
			t.Errorf("unexpected request: %+v", req.pdu)
		}
		send(&message{version: req.version, community: req.community, pdu: pdu{typ: pduGetResponse, requestID: req.pdu.requestID, varbinds: want}})
	})
	native := dialNative(t, agent, V2c)
	for _, sess := range []Session{native, decodedSession{native}} {
		r, err := newColumnRequester(sess, TableWalkOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := r.request(context.Background(), []OID{a, b}, 2, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("length = %d", len(got))
		}
		for i, rv := range got {
			vb, err := rv.Decode()
			if err != nil || !reflect.DeepEqual(vb, want[i]) {
				t.Fatalf("cell %d = %#v, %v", i, vb, err)
			}
			if (rv.VB == nil) != (sess == native) {
				t.Fatal("wrong raw/decoded path")
			}
		}
	}
}

func TestColumnRequestLimits(t *testing.T) {
	s := &session{maxOIDs: 3, maxWalkVars: 11, ignoreNonIncreasing: true}
	r, err := newColumnRequester(s, TableWalkOptions{MaxColumns: 10, CallOptions: []CallOption{WithCallMaxOIDs(2), WithCallMaxWalkVars(7), WithCallIgnoreNonIncreasing(false)}})
	if err != nil {
		t.Fatal(err)
	}
	if r.width != 2 || r.maxVars != 7 || r.ignoreNonIncreasing {
		t.Fatalf("limits = %+v", r)
	}
	for _, opts := range []TableWalkOptions{{MaxColumns: -1}, {MaxColumns: 129}, {MaxRepetitions: -1}, {MaxRepetitions: 256}} {
		if _, err := newColumnRequester(s, opts); err == nil {
			t.Fatalf("accepted %+v", opts)
		}
	}
	_, err = (&columnRequester{sess: &session{version: V1}, cfg: ApplyCallOptions()}).request(context.Background(), []OID{MustOID(1, 3, 6)}, 1, false)
	if !errors.Is(err, ErrBulkUnsupported) {
		t.Fatalf("v1 error: %v", err)
	}
}

func TestColumnRequestV3(t *testing.T) {
	agent := startV3Agent(t, baseCfg(), v3TestEngine, 3, 1000, nil)
	sess, err := dialV3(t, agent, baseCfg())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	r, err := newColumnRequester(sess, TableWalkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	items, err := r.request(context.Background(), []OID{MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 1)}, 2, false)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if items[0].VB == nil {
		t.Fatal("expected decoded USM response")
	}
	if _, err := items[0].Decode(); err != nil {
		t.Fatal(err)
	}
}

func TestColumnRequestInstrumentationAndLimit(t *testing.T) {
	root := MustOID(1, 3, 6, 1, 4, 1, 999, 1)
	agent := startMIBAgent(t, []mibEntry{{root.Child(1), octet(root.Child(1), "value")}}, mibBehavior{})
	sess, sr, reader := dialInstrumented(t, agent, WithMaxOIDs(1))
	r, err := newColumnRequester(sess, TableWalkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.width != 1 {
		t.Fatalf("width=%d", r.width)
	}
	if _, err := r.request(context.Background(), []OID{root}, 2, false); err != nil {
		t.Fatal(err)
	}
	if findSpan(sr.Ended(), "snmp.GetBulk") == nil {
		t.Fatal("missing request span")
	}
	if !metricNames(t, reader)["flowseer.snmp.requests"] {
		t.Fatal("missing requests metric")
	}
	if _, err := sess.Get(context.Background(), []OID{root.Child(1)}); err != nil {
		t.Fatal(err)
	}
}
