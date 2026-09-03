package integration

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
	"go.aledante.io/FlowSeer/src/common/snmp"
)

type tracedSession struct {
	*fakeSession
	requests [][]snmp.OID
}

func (s *tracedSession) GetBulk(ctx context.Context, nr, reps uint8, oids []snmp.OID, opts ...snmp.CallOption) ([]snmp.VarBind, error) {
	s.requests = append(s.requests, append([]snmp.OID(nil), oids...))
	return s.fakeSession.GetBulk(ctx, nr, reps, oids, opts...)
}

func TestGeneratedWalkSelectionAndLifecycle(t *testing.T) {
	for _, stop := range []string{"break", "close", "empty", "duplicate", "unknown"} {
		t.Run(stop, func(t *testing.T) {
			sess := &tracedSession{fakeSession: fakeIfTableSession()}
			cols := []snmp.AnyColumn{ifmib.IfDescr, ifmib.IfOperStatus}
			if stop == "empty" {
				cols = nil
			}
			if stop == "duplicate" {
				cols = append(cols, ifmib.IfDescr)
			}
			if stop == "unknown" {
				cols = []snmp.AnyColumn{snmp.NewColumn[string](ifEntry.Child(999), snmp.KindOctetString, ifmib.IfDescr.Decode)}
			}
			w := ifmib.IfTable.WalkWithOptions(context.Background(), sess, snmp.TableWalkOptions{MaxRepetitions: 1}, cols...)
			if len(sess.requests) != 0 {
				t.Fatal("walk started eagerly")
			}
			if stop == "close" {
				w.Close()
				w.Close()
			}
			rows := 0
			for idx, row := range w.Iter() {
				rows++
				if !row.Index.Equal(idx) {
					t.Fatal("row index mismatch")
				}
				if stop == "break" {
					break
				}
			}
			if stop == "empty" || stop == "close" || stop == "unknown" {
				if rows != 0 || len(sess.requests) != 0 {
					t.Fatal("unexpected request or row")
				}
			} else {
				for _, req := range sess.requests {
					if len(req) > 2 {
						t.Fatal("duplicate stream")
					}
					for _, oid := range req {
						if !oid.HasPrefix(ifmib.IfDescr.OID()) && !oid.HasPrefix(ifmib.IfOperStatus.OID()) {
							t.Fatalf("unselected cursor %s", oid)
						}
					}
				}
				if stop == "break" && len(sess.requests) != 1 {
					t.Fatalf("early stop made %d requests", len(sess.requests))
				}
			}
			if stop == "unknown" {
				if !errors.Is(w.Err(), snmp.ErrForeignColumn) {
					t.Fatal(w.Err())
				}
			} else if w.Err() != nil {
				t.Fatal(w.Err())
			}
		})
	}
}

func TestGeneratedWalkDecodeErrorPrefix(t *testing.T) {
	sess := &fakeSession{}
	for i := uint32(1); i <= 4; i++ {
		sess.vbs = append(sess.vbs, ifRowFixtures(i, "eth", ifmib.IfOperStatusValueUp)...)
	}
	for i := range sess.vbs {
		if sess.vbs[i].oid.Equal(ifEntry.Append(8, 3)) {
			sess.vbs[i].vb = snmp.OctetStringVar{Header: snmp.Header{OID: sess.vbs[i].oid, Kind: snmp.KindOctetString}, Value: []byte("bad-status")}
		}
	}
	w := ifmib.IfTable.Walk(context.Background(), sess, ifmib.IfDescr, ifmib.IfOperStatus)
	var got []uint32
	for idx := range w.Iter() {
		got = append(got, idx.At(0))
	}
	if !reflect.DeepEqual(got, []uint32{1, 2}) || w.Err() == nil {
		t.Fatalf("indexes=%v err=%v", got, w.Err())
	}
}
