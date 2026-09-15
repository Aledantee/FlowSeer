package stp

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

// TestTreeForAnswersForEveryVLAN pins the mapping this phase lands: one tree
// carries every VLAN, including the ones vlan.ID.Valid rejects, so no caller has
// to check a VID before asking the gate about it.
func TestTreeForAnswersForEveryVLAN(t *testing.T) {
	t.Parallel()

	l := newLayer(Config{
		Ports: map[string]Port{"1/1/1": {}, "1/1/2": {}},
	}.Normalize())

	cist := l.cist()
	if cist == nil {
		t.Fatal("cist() = nil, want the tree every bridge runs")
	}

	for vid := vlan.ID(0); vid <= 4095; vid++ {
		got := l.treeFor(vid)
		if got == nil {
			t.Fatalf("treeFor(%d) = nil, want the CIST", vid)
		}
		if got != cist {
			t.Fatalf("treeFor(%d) = tree %d, want the CIST", vid, got.id)
		}
	}

	if n := len(l.trees); n != 1 {
		t.Errorf("trees = %d, want exactly one while the bridge runs rapid spanning tree", n)
	}
}

// TestLoopGuardIgnoresAnEdgePort is a guard for later code, not evidence for
// this change: no sequence of Receive, Wake, and LinkChange can currently leave
// a port both operationally edge and holding received information in a
// non-designated role, because Receive resets edge from the administrative
// setting and the auto-edge promotion only fires on a Designated port. The
// state is built directly here so the exclusion is pinned before a later phase
// makes it reachable.
func TestLoopGuardIgnoresAnEdgePort(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := newLayer(Config{
		Ports: map[string]Port{"1/1/1": {LoopGuard: true}, "1/1/2": {}},
	}.Normalize())

	p := l.cist().ports["1/1/1"]
	p.up = true
	p.pointToPoint = true
	p.edge = true
	p.role = RoleRoot
	p.rcvInfoValid = true
	p.rcvHelloTime = 2 * time.Second
	p.rcvTime = t0

	// Three hello times of silence expire the information.
	l.Wake(t0.Add(7 * time.Second))

	if p.loopInconsistent {
		t.Error("loop guard held an edge port, which is where Cisco and Arista rule it out")
	}
	if got := l.PortInfo("1/1/1").BlockReason; got != "" {
		t.Errorf("block reason = %q, want none on an edge port", got)
	}

	// The same expiry on a non-edge port does arm the guard, so the assertion
	// above is about the edge clause and not about the trigger never firing.
	q := l.cist().ports["1/1/2"]
	q.up = true
	q.pointToPoint = true
	q.cfg.LoopGuard = true
	q.role = RoleRoot
	q.rcvInfoValid = true
	q.rcvHelloTime = 2 * time.Second
	q.rcvTime = t0.Add(7 * time.Second)

	l.Wake(t0.Add(14 * time.Second))

	if !q.loopInconsistent {
		t.Fatal("the control port did not arm the guard; the edge assertion proves nothing")
	}
}

// TestPortNamesStayBridgeGlobal guards the seam the tree keying could have
// broken: the identifier and the iteration order belong to the bridge, not to a
// tree, so a second tree cannot renumber a port or reorder a flush list.
func TestPortNamesStayBridgeGlobal(t *testing.T) {
	t.Parallel()

	l := newLayer(Config{
		Ports: map[string]Port{"1/1/3": {}, "1/1/1": {}, "1/1/2": {}},
	}.Normalize())

	want := []string{"1/1/1", "1/1/2", "1/1/3"}
	if len(l.portNames) != len(want) {
		t.Fatalf("portNames = %v, want %v", l.portNames, want)
	}
	for i, name := range want {
		if l.portNames[i] != name {
			t.Fatalf("portNames = %v, want %v", l.portNames, want)
		}
	}

	// The identifier's low byte is the index in that order, counted from one.
	for i, name := range want {
		p := l.cist().ports[name]
		if got, wantIdx := p.portID&0xff, uint16(i+1); got != wantIdx {
			t.Errorf("port %q identifier index = %d, want %d", name, got, wantIdx)
		}
	}
}
