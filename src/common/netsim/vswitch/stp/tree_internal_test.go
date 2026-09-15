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

// TestCompareVectorsReproducesLandedOrders pins the two four-component orders
// the six-component vector must collapse to when two of its components hold
// equal (RSTP) or zero (both cases). With the regional root equal and the
// internal cost zero, the comparison must reproduce the landed RSTP order:
// root, then external cost, then designated bridge, then designated port.
// With the root equal and the external cost zero, it must reproduce IEEE
// 802.1Q clause 13.11's MSTI order: regional root, then internal cost, then
// designated bridge, then designated port.
func TestCompareVectorsReproducesLandedOrders(t *testing.T) {
	t.Parallel()

	lowBridge, highBridge := BridgeID{Priority: 10}, BridgeID{Priority: 20}
	lowRoot, highRoot := BridgeID{Priority: 100}, BridgeID{Priority: 200}
	sharedRegional := BridgeID{Priority: 1}

	tests := []struct {
		name string
		a, b priorityVector
		want int
	}{
		// RSTP order: regionalRootID equal, internalRootPathCost zero for both.
		{
			name: "rstp: lower root wins regardless of a higher cost, bridge, and port",
			a: priorityVector{
				rootID: lowRoot, externalRootPathCost: 100,
				regionalRootID: sharedRegional, bridgeID: highBridge, portID: 20,
			},
			b: priorityVector{
				rootID: highRoot, externalRootPathCost: 1,
				regionalRootID: sharedRegional, bridgeID: lowBridge, portID: 1,
			},
			want: -1,
		},
		{
			name: "rstp: equal root falls through to external cost",
			a: priorityVector{
				rootID: lowRoot, externalRootPathCost: 1,
				regionalRootID: sharedRegional, bridgeID: highBridge, portID: 20,
			},
			b: priorityVector{
				rootID: lowRoot, externalRootPathCost: 100,
				regionalRootID: sharedRegional, bridgeID: lowBridge, portID: 1,
			},
			want: -1,
		},
		{
			name: "rstp: equal root and cost fall through to designated bridge",
			a: priorityVector{
				rootID: lowRoot, externalRootPathCost: 5,
				regionalRootID: sharedRegional, bridgeID: lowBridge, portID: 20,
			},
			b: priorityVector{
				rootID: lowRoot, externalRootPathCost: 5,
				regionalRootID: sharedRegional, bridgeID: highBridge, portID: 1,
			},
			want: -1,
		},
		{
			name: "rstp: equal root, cost, and bridge fall through to designated port",
			a: priorityVector{
				rootID: lowRoot, externalRootPathCost: 5,
				regionalRootID: sharedRegional, bridgeID: lowBridge, portID: 1,
			},
			b: priorityVector{
				rootID: lowRoot, externalRootPathCost: 5,
				regionalRootID: sharedRegional, bridgeID: lowBridge, portID: 2,
			},
			want: -1,
		},
		{
			name: "rstp: every component equal compares equal",
			a: priorityVector{
				rootID: lowRoot, externalRootPathCost: 5,
				regionalRootID: sharedRegional, bridgeID: lowBridge, portID: 1,
			},
			b: priorityVector{
				rootID: lowRoot, externalRootPathCost: 5,
				regionalRootID: sharedRegional, bridgeID: lowBridge, portID: 1,
			},
			want: 0,
		},
		// Clause 13.11 MSTI order: rootID equal, externalRootPathCost zero for both.
		{
			name: "msti: lower regional root wins regardless of a higher cost, bridge, and port",
			a: priorityVector{
				rootID: sharedRegional, regionalRootID: lowRoot, internalRootPathCost: 100,
				bridgeID: highBridge, portID: 20,
			},
			b: priorityVector{
				rootID: sharedRegional, regionalRootID: highRoot, internalRootPathCost: 1,
				bridgeID: lowBridge, portID: 1,
			},
			want: -1,
		},
		{
			name: "msti: equal regional root falls through to internal cost",
			a: priorityVector{
				rootID: sharedRegional, regionalRootID: lowRoot, internalRootPathCost: 1,
				bridgeID: highBridge, portID: 20,
			},
			b: priorityVector{
				rootID: sharedRegional, regionalRootID: lowRoot, internalRootPathCost: 100,
				bridgeID: lowBridge, portID: 1,
			},
			want: -1,
		},
		{
			name: "msti: equal regional root and cost fall through to designated bridge",
			a: priorityVector{
				rootID: sharedRegional, regionalRootID: lowRoot, internalRootPathCost: 5,
				bridgeID: lowBridge, portID: 20,
			},
			b: priorityVector{
				rootID: sharedRegional, regionalRootID: lowRoot, internalRootPathCost: 5,
				bridgeID: highBridge, portID: 1,
			},
			want: -1,
		},
		{
			name: "msti: equal regional root, cost, and bridge fall through to designated port",
			a: priorityVector{
				rootID: sharedRegional, regionalRootID: lowRoot, internalRootPathCost: 5,
				bridgeID: lowBridge, portID: 1,
			},
			b: priorityVector{
				rootID: sharedRegional, regionalRootID: lowRoot, internalRootPathCost: 5,
				bridgeID: lowBridge, portID: 2,
			},
			want: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := compareVectors(tt.a, tt.b); got != tt.want {
				t.Errorf("compareVectors(a, b) = %d, want %d", got, tt.want)
			}
			if got := compareVectors(tt.b, tt.a); got != -tt.want {
				t.Errorf("compareVectors(b, a) = %d, want %d", got, -tt.want)
			}
		})
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
