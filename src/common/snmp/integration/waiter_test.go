package integration

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/snmp"
)

// makeTrap builds a minimal trap with snmpTrapOID.0 set to oid. The
// shape is sufficient for waiter tests, which match on snmpTrapOID.0.
func makeTrap(oid snmp.OID) snmp.Trap {
	snmpTrapOID := snmp.MustOID(1, 3, 6, 1, 6, 3, 1, 1, 4, 1, 0)
	return snmp.Trap{
		Source:    net.IPv4(10, 0, 0, 1),
		Community: "public",
		Version:   snmp.V2c,
		VarBinds: []snmp.VarBind{
			snmp.ObjectIDVar{
				Header: snmp.Header{OID: snmpTrapOID, Kind: snmp.KindObjectID},
				Value:  oid,
			},
		},
		Received: time.Now(),
	}
}

// hasTrapOID returns a match function that checks the trap's
// snmpTrapOID.0 varbind for equality with want.
func hasTrapOID(want snmp.OID) func(snmp.Trap) bool {
	snmpTrapOID := snmp.MustOID(1, 3, 6, 1, 6, 3, 1, 1, 4, 1, 0)
	return func(trap snmp.Trap) bool {
		for _, vb := range trap.VarBinds {
			if !vb.GetHeader().OID.Equal(snmpTrapOID) {
				continue
			}
			oidVar, ok := vb.(snmp.ObjectIDVar)
			if !ok {
				return false
			}
			return oidVar.Value.Equal(want)
		}
		return false
	}
}

// TestWaitForTrap_Happy_MatchOnFirstTrap pushes one matching trap;
// WaitForTrap returns it within the timeout.
func TestWaitForTrap_Happy_MatchOnFirstTrap(t *testing.T) {
	ts := snmp.NewTrapStream(context.Background(), 4)
	defer func() { _ = ts.Close() }()

	target := snmp.MustOID(1, 3, 6, 1, 6, 3, 1, 1, 5, 1) // coldStart
	go ts.Push(makeTrap(target))

	got, err := WaitForTrap(context.Background(), ts, hasTrapOID(target), 2*time.Second)
	if err != nil {
		t.Fatalf("WaitForTrap: %v", err)
	}
	if !got.VarBinds[0].(snmp.ObjectIDVar).Value.Equal(target) {
		t.Errorf("matched trap snmpTrapOID = %s, want %s", got.VarBinds[0].(snmp.ObjectIDVar).Value, target)
	}
}

// TestWaitForTrap_Edge_SkipsNonMatching pushes two non-matching
// traps followed by the matching one; the waiter ignores the
// distractors and returns the third.
func TestWaitForTrap_Edge_SkipsNonMatching(t *testing.T) {
	ts := snmp.NewTrapStream(context.Background(), 4)
	defer func() { _ = ts.Close() }()

	linkDown := snmp.MustOID(1, 3, 6, 1, 6, 3, 1, 1, 5, 3)
	other1 := snmp.MustOID(1, 3, 6, 1, 6, 3, 1, 1, 5, 1) // coldStart
	other2 := snmp.MustOID(1, 3, 6, 1, 6, 3, 1, 1, 5, 2) // warmStart

	go func() {
		ts.Push(makeTrap(other1))
		ts.Push(makeTrap(other2))
		ts.Push(makeTrap(linkDown))
	}()

	got, err := WaitForTrap(context.Background(), ts, hasTrapOID(linkDown), 2*time.Second)
	if err != nil {
		t.Fatalf("WaitForTrap: %v", err)
	}
	if !got.VarBinds[0].(snmp.ObjectIDVar).Value.Equal(linkDown) {
		t.Errorf("matched trap snmpTrapOID = %s, want linkDown %s", got.VarBinds[0].(snmp.ObjectIDVar).Value, linkDown)
	}
}

// TestWaitForTrap_Edge_TimeoutWithNoMatch: nothing ever pushed; the
// waiter returns ErrTrapWaitTimeout after the deadline.
func TestWaitForTrap_Edge_TimeoutWithNoMatch(t *testing.T) {
	ts := snmp.NewTrapStream(context.Background(), 4)
	defer func() { _ = ts.Close() }()

	wantedOID := snmp.MustOID(1, 3, 6, 1, 6, 3, 1, 1, 5, 1)

	start := time.Now()
	_, err := WaitForTrap(context.Background(), ts, hasTrapOID(wantedOID), 50*time.Millisecond)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrTrapWaitTimeout) {
		t.Fatalf("err = %v, want ErrTrapWaitTimeout", err)
	}
	if elapsed < 45*time.Millisecond {
		t.Errorf("returned in %v, want >= ~50ms", elapsed)
	}
}

// TestWaitForTrap_Edge_StreamClosedBeforeMatch closes the stream
// before pushing any matching trap; the waiter returns
// ErrTrapStreamClosed once iteration drains.
func TestWaitForTrap_Edge_StreamClosedBeforeMatch(t *testing.T) {
	ts := snmp.NewTrapStream(context.Background(), 4)

	wantedOID := snmp.MustOID(1, 3, 6, 1, 6, 3, 1, 1, 5, 3)
	otherOID := snmp.MustOID(1, 3, 6, 1, 6, 3, 1, 1, 5, 1)

	go func() {
		ts.Push(makeTrap(otherOID))
		_ = ts.Close()
	}()

	_, err := WaitForTrap(context.Background(), ts, hasTrapOID(wantedOID), 2*time.Second)
	if !errors.Is(err, ErrTrapStreamClosed) {
		t.Fatalf("err = %v, want ErrTrapStreamClosed", err)
	}
}

// TestWaitForTrap_Edge_ContextCancellation cancels the context
// before any trap arrives; the waiter returns ctx.Err().
func TestWaitForTrap_Edge_ContextCancellation(t *testing.T) {
	ts := snmp.NewTrapStream(context.Background(), 4)
	defer func() { _ = ts.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	wantedOID := snmp.MustOID(1, 3, 6, 1, 6, 3, 1, 1, 5, 3)

	cancel()
	_, err := WaitForTrap(ctx, ts, hasTrapOID(wantedOID), 2*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestWaitForTrap_Regression_MultiCallSameStream pins the
// multi-call-safety contract documented in WaitForTrap's doc:
// successive calls against a single TrapStream must each be able to
// match a trap. The earlier Iter-based implementation killed the
// stream on the first match (Iter signals stop+drain when the
// consumer breaks out of the range loop), which made
// TestT2_Trap_LinkDownLinkUp's linkDown→linkUp pattern fail
// deterministically. The Scanner-style implementation does not
// signal stop; this test pins that invariant against a fake stream.
func TestWaitForTrap_Regression_MultiCallSameStream(t *testing.T) {
	ts := snmp.NewTrapStream(context.Background(), 4)
	defer func() { _ = ts.Close() }()

	linkDown := snmp.MustOID(1, 3, 6, 1, 6, 3, 1, 1, 5, 3)
	linkUp := snmp.MustOID(1, 3, 6, 1, 6, 3, 1, 1, 5, 4)

	go func() {
		ts.Push(makeTrap(linkDown))
		time.Sleep(10 * time.Millisecond)
		ts.Push(makeTrap(linkUp))
	}()

	got, err := WaitForTrap(context.Background(), ts, hasTrapOID(linkDown), 2*time.Second)
	if err != nil {
		t.Fatalf("first wait (linkDown): %v", err)
	}
	if !got.VarBinds[0].(snmp.ObjectIDVar).Value.Equal(linkDown) {
		t.Errorf("first match = %s, want linkDown %s", got.VarBinds[0].(snmp.ObjectIDVar).Value, linkDown)
	}

	got, err = WaitForTrap(context.Background(), ts, hasTrapOID(linkUp), 2*time.Second)
	if err != nil {
		t.Fatalf("second wait (linkUp): %v", err)
	}
	if !got.VarBinds[0].(snmp.ObjectIDVar).Value.Equal(linkUp) {
		t.Errorf("second match = %s, want linkUp %s", got.VarBinds[0].(snmp.ObjectIDVar).Value, linkUp)
	}
}

// TestWaitForTrap_Guard_NilStream asserts the fast-fail path for a
// nil stream input.
func TestWaitForTrap_Guard_NilStream(t *testing.T) {
	_, err := WaitForTrap(context.Background(), nil, func(snmp.Trap) bool { return true }, time.Second)
	if err == nil {
		t.Fatal("expected non-nil error for nil stream")
	}
}

// TestWaitForTrap_Guard_NilMatch asserts the fast-fail path for a
// nil match callback.
func TestWaitForTrap_Guard_NilMatch(t *testing.T) {
	ts := snmp.NewTrapStream(context.Background(), 4)
	defer func() { _ = ts.Close() }()

	_, err := WaitForTrap(context.Background(), ts, nil, time.Second)
	if err == nil {
		t.Fatal("expected non-nil error for nil match func")
	}
}
