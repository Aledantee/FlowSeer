package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

func TestMcheckSchedulesAnExpiredEdgeDelayAtNow(t *testing.T) {
	start := time.Unix(1700000000, 0)
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "02:00:00:00:00:02"),
		Ports:   map[string]stp.Port{"p1": {AutoEdge: true}},
	}, mustPortTable(t, "p1"))
	service := func(until time.Time) {
		t.Helper()
		for range 100 {
			wake, ok := l.NextWake()
			if !ok || wake.After(until) {
				return
			}
			l.Advance(wake)
			if later, ok := l.NextWake(); ok && !later.After(wake) {
				t.Fatalf("Advance at %v left wake %v", wake, later)
			}
		}
		t.Fatal("wake service exceeded 100 calls")
	}
	l.LinkChange(start, "p1", true, true, 1_000_000_000)
	service(start.Add(time.Second))
	l.Receive(start.Add(time.Second), "p1", edgeTestBPDU(t, 61440, bpdu.RoleDesignated, false, false))
	service(start.Add(3500 * time.Millisecond))
	l.Receive(start.Add(3500*time.Millisecond), "p1", legacyConfigBPDU(t, 61440, "02:00:00:00:00:0a"))
	service(start.Add(8 * time.Second))
	if info := l.PortInfo("p1"); info.Edge || info.SendRSTP || info.State != stp.StateDiscarding {
		t.Fatalf("before Mcheck = %+v, want non-edge Discarding/STP", info)
	}
	now := start.Add(8 * time.Second)
	l.Mcheck(now, "p1")
	if wake, ok := l.NextWake(); !ok || !wake.Equal(now) {
		t.Fatalf("NextWake after Mcheck = (%v, %t), want (%v, true)", wake, ok, now)
	}
	l.Advance(now)
	if info := l.PortInfo("p1"); !info.Edge || info.State != stp.StateForwarding || info.Role != bpdu.RoleDesignated {
		t.Fatalf("after edge wake = %+v, want edge Designated/Forwarding", info)
	}
	if wake, ok := l.NextWake(); !ok || !wake.After(now) {
		t.Fatalf("NextWake after edge wake = (%v, %t), want later than %v", wake, ok, now)
	}
}

func TestSyncFromLearningRestartsTheEdgeDelay(t *testing.T) {
	start := time.Unix(1700000000, 0)
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "02:00:00:00:00:02"),
		Ports:   map[string]stp.Port{"p1": {}, "p2": {AutoEdge: true}},
	}, mustPortTable(t, "p1", "p2"))
	l.LinkChange(start, "p1", true, true, 1_000_000_000)
	l.LinkChange(start, "p2", true, true, 1_000_000_000)
	inferior := edgeTestBPDU(t, 61440, bpdu.RoleDesignated, false, false)
	inferior.HelloTime = 10 * time.Second
	inferior.MaxAge = 22 * time.Second
	for second := 1; second <= 13; second += 2 {
		l.Receive(start.Add(time.Duration(second)*time.Second), "p2", inferior)
	}
	l.Advance(start.Add(15 * time.Second))
	if info := l.PortInfo("p2"); info.Edge || info.State != stp.StateLearning {
		t.Fatalf("before sync = %+v, want non-edge Learning", info)
	}
	now := start.Add(20 * time.Second)
	l.Receive(now, "p1", edgeTestBPDU(t, 4096, bpdu.RoleDesignated, true, false))
	if info := l.PortInfo("p2"); info.Edge || info.State != stp.StateDiscarding {
		t.Fatalf("after sync = %+v, want non-edge Discarding", info)
	}
	if wake, ok := l.NextWake(); !ok || wake.Before(now) {
		t.Fatalf("NextWake after sync = (%v, %t), want no earlier than %v", wake, ok, now)
	}
}
