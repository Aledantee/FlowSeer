package snmp

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Watcher integration. These tests prove the native
// Session has no hidden backend coupling: a real Watcher drives it
// purely through the Session interface. On v2c a cold-start emits an Added
// event; on v1 the Watcher (which ticks via BulkWalk) surfaces a clear
// error rather than silently never ticking.

type ifWatchRow struct {
	IfIndex uint32
	IfDescr string
}

var (
	wIfTableRoot  = MustOID(1, 3, 6, 1, 2, 1, 2, 2)
	wIfDescrCol   = MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 2)
	wIfLastChCol  = MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 9)
	wIfDescrInst1 = wIfDescrCol.Append(1)
	wIfLastChIns1 = wIfLastChCol.Append(1)
)

func newIfWatcher(t *testing.T, sess Session) (*Watcher[ifWatchRow], error) {
	t.Helper()
	indicatorCol := NewColumn[uint32](wIfLastChCol, KindTimeTicks,
		func(vb VarBind) (uint32, error) {
			if tt, ok := vb.(TimeTicksVar); ok {
				return tt.Value, nil
			}
			return 0, errs.New().Attr("type", fmt.Sprintf("%T", vb)).Msg("want TimeTicks")
		})
	indicator, err := NewPerRowIndicator(indicatorCol, wIfTableRoot)
	if err != nil {
		t.Fatalf("NewPerRowIndicator: %v", err)
	}
	descrCol := NewColumn[string](wIfDescrCol, KindOctetString,
		func(vb VarBind) (string, error) {
			if os, ok := vb.(OctetStringVar); ok {
				return string(os.Value), nil
			}
			return "", errs.New().Attr("type", fmt.Sprintf("%T", vb)).Msg("want OctetString")
		})

	decode := func(idx OID, vbs []VarBind) (ifWatchRow, error) {
		r := ifWatchRow{}
		if idx.Len() >= 1 {
			r.IfIndex = idx.At(idx.Len() - 1)
		}
		for _, vb := range vbs {
			colOID, _ := vb.GetHeader().OID.Parent()
			if colOID.Equal(wIfDescrCol) {
				if os, ok := vb.(OctetStringVar); ok {
					r.IfDescr = string(os.Value)
				}
			}
		}
		return r, nil
	}
	equal := func(a, b ifWatchRow) bool { return a == b }
	merge := func(dst *ifWatchRow, vbs []VarBind) {
		for _, vb := range vbs {
			colOID, _ := vb.GetHeader().OID.Parent()
			if colOID.Equal(wIfDescrCol) {
				if os, ok := vb.(OctetStringVar); ok {
					dst.IfDescr = string(os.Value)
				}
			}
		}
	}

	return NewWatcher[ifWatchRow](
		context.Background(), sess, indicator,
		[]AnyColumn{descrCol}, decode, equal, merge,
		WithCadenceBounds(20*time.Millisecond, 20*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
	)
}

func timeTicks(oid OID, v uint32) VarBind {
	return TimeTicksVar{Header: Header{OID: oid, Kind: KindTimeTicks}, Value: v}
}

// TestWatcher_V2c_ColdStartEmits proves a Watcher over a v2c native
// session produces an Added event for the cold-start row.
func TestWatcher_V2c_ColdStartEmits(t *testing.T) {
	agent := startMIBAgent(t, []mibEntry{
		{wIfDescrInst1, octet(wIfDescrInst1, "eth1")},
		{wIfLastChIns1, timeTicks(wIfLastChIns1, 100)},
	}, mibBehavior{})
	sess := dialNative(t, agent, V2c)

	w, err := newIfWatcher(t, sess)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	type ev struct {
		kind ChangeKind
		row  ifWatchRow
	}
	events := make(chan ev, 4)
	go func() {
		for _, e := range w.Iter() {
			events <- ev{e.Kind, e.Row}
		}
	}()

	select {
	case e := <-events:
		if e.kind != ChangeKindAdded {
			t.Fatalf("first event kind = %v, want Added", e.kind)
		}
		if e.row.IfIndex != 1 || e.row.IfDescr != "eth1" {
			t.Fatalf("row = %+v, want {1 eth1}", e.row)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no cold-start event within timeout (watcher err: %v)", w.Err())
	}
}

// TestWatcher_V1_SurfacesError proves that a Watcher over a v1 native
// session surfaces a clear error (it ticks via BulkWalk, which native
// rejects on v1) instead of silently never ticking.
func TestWatcher_V1_SurfacesError(t *testing.T) {
	agent := startMIBAgent(t, []mibEntry{
		{wIfDescrInst1, octet(wIfDescrInst1, "eth1")},
		{wIfLastChIns1, timeTicks(wIfLastChIns1, 100)},
	}, mibBehavior{})
	sess := dialNative(t, agent, V1)

	w, err := newIfWatcher(t, sess)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Drain until the stream ends (it should fail, not run forever).
	done := make(chan struct{})
	go func() {
		for range w.Iter() {
		}
		close(done)
	}()
	select {
	case <-done:
		if w.Err() == nil {
			t.Fatal("v1 Watcher ended without an error; expected a clear BulkWalk-unsupported failure")
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("v1 Watcher neither emitted nor failed within timeout")
	}
}
