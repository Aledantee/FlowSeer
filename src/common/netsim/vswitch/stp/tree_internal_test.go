package stp

import (
	"slices"
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
		got, ok := l.treeFor(vid)
		if !ok {
			t.Fatalf("treeFor(%d) = false, want the CIST", vid)
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

// TestLoopGuardHoldsAnInternalMSTIPortOutOfForwarding pins that loop guard,
// like BPDU guard, is a bridge-global property of the port: it must hold an
// MSTI's own role and state out of the active topology on an internal
// (non-boundary) port, not just the CIST's. The peer BPDU carries this
// bridge's own ConfigID and l.boundary is checked directly, so the port
// stays internal throughout: an external peer would route the MSTI's role
// through the boundary mirror instead and pass for the wrong reason.
func TestLoopGuardHoldsAnInternalMSTIPortOutOfForwarding(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	region := MST{
		Name: "region-1",
		Instances: map[MSTID]Instance{
			1: {VLANs: []vlan.ID{10}},
		},
	}
	l := newLayer(Config{
		Priority: 32768,
		Ports: map[string]Port{
			"1/1/1": {LoopGuard: true},
			"1/1/2": {},
		},
		MST: &region,
	}.Normalize())

	l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)
	l.LinkChange(t0, "1/1/2", true, true, 1_000_000_000)

	// Neither tree has heard from a peer yet, so both the CIST's and MSTI
	// 1's own copy of "1/1/1" are Designated and climb their own forward
	// delay ladders to Forwarding: two forward delays carries both there,
	// which is what makes the later assertion about MSTI 1 evidence rather
	// than a port that was never forwarding in the first place.
	l.Wake(t0.Add(16 * time.Second))
	l.Wake(t0.Add(32 * time.Second))
	if !l.Forwards("1/1/1", 10) {
		t.Fatalf("MSTI 1 does not forward VLAN 10 before the CIST hears a peer")
	}

	cid := l.mst.ConfigID()
	peer := BPDU{
		RootID:               BridgeID{Priority: 4096},
		BridgeID:             BridgeID{Priority: 4096},
		PortID:               0x8001,
		HelloTime:            2 * time.Second,
		MaxAge:               20 * time.Second,
		ForwardDelay:         15 * time.Second,
		ConfigID:             &cid,
		RegionalRootID:       BridgeID{Priority: 4096},
		InternalRootPathCost: 0,
		RemainingHops:        20,
	}
	peer.SetRole(RoleDesignated)

	l.Receive(t0.Add(33*time.Second), "1/1/1", peer)

	if l.boundary("1/1/1") {
		t.Fatal("port classified boundary from a BPDU carrying this bridge's own ConfigID, want internal")
	}
	if info := l.PortInfo("1/1/1"); info.Role != RoleRoot {
		t.Fatalf("CIST role = %v, want Root before the peer goes quiet", info.Role)
	}

	// Silence past three hello times expires the information and arms the
	// guard.
	l.Wake(t0.Add(33 * time.Second).Add(7 * time.Second))

	cistInfo := l.PortInfo("1/1/1")
	if cistInfo.Role != RoleAlternate || cistInfo.State != StateDiscarding {
		t.Fatalf("CIST guarded port = %v/%v, want Alternate/Discarding", cistInfo.Role, cistInfo.State)
	}
	if cistInfo.BlockReason != BlockReasonLoopInconsistent {
		t.Fatalf("CIST block reason = %q, want %q", cistInfo.BlockReason, BlockReasonLoopInconsistent)
	}

	if l.Forwards("1/1/1", 10) {
		t.Error("loop-inconsistent port forwards MSTI 1's VLAN 10, which is the loop the guard exists to prevent")
	}
	if info := l.InstancePortInfo(1, "1/1/1"); info.State == StateForwarding {
		t.Errorf("MSTI 1 port state = %v, want not Forwarding once loop guard trips on the CIST", info.State)
	}
}

// TestRawAndDesignatedVectorsShareShape pins that rawVector and
// designatedVector build the same six-component shape for the same port and
// tree state, since designatedOrBlocked compares them directly. Before the
// fix, a CIST internal port's rawVector left externalRootPathCost at zero
// while designatedVector filled it from the tree's own root path cost, so
// identical received and offered information compared as though the
// received one were superior.
func TestRawAndDesignatedVectorsShareShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		treeID   treeID
		external bool
	}{
		{name: "cist boundary port", treeID: cistID, external: true},
		{name: "cist internal port", treeID: cistID, external: false},
		{name: "msti port", treeID: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bridgeID := BridgeID{Priority: 100}
			otherBridge := BridgeID{Priority: 200}
			regionalRoot := BridgeID{Priority: 50}
			const portID = uint16(0x8001)

			tr := &tree{
				id:                   tc.treeID,
				bridgeID:             bridgeID,
				rootID:               otherBridge,
				rootPathCost:         5000,
				regionalRootID:       regionalRoot,
				internalRootPathCost: 3000,
			}

			p := &portState{
				portID:                  portID,
				external:                tc.external,
				rcvRootID:               tr.rootID,
				rcvRootPathCost:         tr.rootPathCost,
				rcvRegionalRootID:       tr.regionalRootID,
				rcvInternalRootPathCost: tr.internalRootPathCost,
				rcvBridgeID:             tr.bridgeID,
				rcvPortID:               portID,
			}

			raw := rawVector(tr, p)
			des := designatedVector(tr, p)

			// The two must compare equal directly: identical information
			// offered by the tree and received on the port is neither
			// superior nor inferior.
			if got := compareVectors(raw, des); got != 0 {
				t.Errorf("compareVectors(raw, designated) = %d, want 0 for identical information (raw=%+v, designated=%+v)",
					got, raw, des)
			}
		})
	}
}

// TestDesignatedOrBlockedElectsDesignatedOnCISTInternalPortFacingAWorsePeer
// pins the same bug through the code path that consumes the two vectors:
// before the fix, a CIST internal port's own nonzero root path cost never
// reached the comparison against a received vector, so a worse peer whose
// only advantage was a lower designated bridge identifier still won.
func TestDesignatedOrBlockedElectsDesignatedOnCISTInternalPortFacingAWorsePeer(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := newLayer(Config{
		Priority: 4096,
		Ports:    map[string]Port{"1/1/1": {}},
	}.Normalize())

	tr := l.cist()
	tr.rootID = BridgeID{Priority: 1024}
	tr.rootPathCost = 20000
	tr.regionalRootID = tr.bridgeID
	tr.internalRootPathCost = 0

	p := tr.ports["1/1/1"]
	p.up = true
	p.external = false
	p.rcvInfoValid = true
	p.rcvTime = t0
	p.rcvHelloTime = 2 * time.Second
	// A worse peer: the same root, regional root, and internal cost as this
	// tree's own designated vector, but a numerically higher (worse) bridge
	// identifier and port. Only a correctly compared external cost keeps the
	// comparison from stopping earlier with the peer looking superior.
	p.rcvRootID = tr.rootID
	p.rcvRootPathCost = tr.rootPathCost
	p.rcvRegionalRootID = tr.regionalRootID
	p.rcvInternalRootPathCost = tr.internalRootPathCost
	p.rcvBridgeID = BridgeID{Priority: 65535}
	p.rcvPortID = 0xFFFF

	if got := l.designatedOrBlocked(tr, p, t0); got != RoleDesignated {
		t.Errorf("designatedOrBlocked = %v, want Designated: this tree's own information beats a worse peer's", got)
	}
}

// TestExternalRootPortReportsItselfAsRegionalRoot pins that a bridge whose
// CIST root port is a boundary port is this region's own CIST regional root:
// before the fix, candidateVector copied the peer's own claimed root as this
// region's regional root instead, so this bridge advertised a bridge in the
// other region as its own region's regional root and never reached the
// isRegionalRoot branch that would have originated MaxHops.
func TestExternalRootPortReportsItselfAsRegionalRoot(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	region := MST{Name: "region-1"}
	l := newLayer(Config{
		Priority: 32768,
		Ports:    map[string]Port{"1/1/1": {}},
		MST:      &region,
	}.Normalize())
	l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)

	// A plain RSTP peer (no ConfigID at all) is unambiguously external.
	peer := BPDU{
		RootID:       BridgeID{Priority: 4096},
		BridgeID:     BridgeID{Priority: 4096},
		PortID:       0x8001,
		RootPathCost: 100,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	peer.SetRole(RoleDesignated)

	l.Receive(t0.Add(time.Second), "1/1/1", peer)

	cist := l.cist()
	if !cist.ports["1/1/1"].external {
		t.Fatal("port not classified external from a peer carrying no ConfigID")
	}
	if cist.rootPort != "1/1/1" {
		t.Fatalf("CIST root port = %q, want 1/1/1", cist.rootPort)
	}
	if cist.regionalRootID != cist.bridgeID {
		t.Errorf("CIST regional root = %v, want this bridge's own %v: it terminates the region for this external root port",
			cist.regionalRootID, cist.bridgeID)
	}

	want := effectiveMaxHops(l.mst.MaxHops)
	if got := l.instanceRemainingHops(cist); got != want {
		t.Errorf("instanceRemainingHops = %d, want MaxHops %d for this region's own regional root", got, want)
	}
}

// TestReceiveClearsInternalOnlyFieldsWhenAPortTurnsExternal pins the bug
// where a port's region-internal received information (regional root,
// internal cost, remaining hops) survived a later BPDU that reclassified the
// port external, so a boundary bridge re-originated a stale, decreasing hop
// count instead of MaxHops.
func TestReceiveClearsInternalOnlyFieldsWhenAPortTurnsExternal(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	region := MST{Name: "region-1"}
	l := newLayer(Config{
		Priority: 32768,
		Ports:    map[string]Port{"1/1/1": {}},
		MST:      &region,
	}.Normalize())

	cist := l.cist()
	p := cist.ports["1/1/1"]
	p.up = true
	p.pointToPoint = true
	p.external = false
	p.rcvInfoValid = true
	p.rcvRootID = BridgeID{Priority: 4096}
	p.rcvRootPathCost = 0
	p.rcvBridgeID = BridgeID{Priority: 4096}
	p.rcvPortID = 0x8001
	p.rcvHelloTime = 2 * time.Second
	p.rcvMaxAge = 20 * time.Second
	p.rcvTime = t0
	p.rcvRegionalRootID = BridgeID{Priority: 8192}
	p.rcvInternalRootPathCost = 100
	p.rcvRemainingHops = 5

	// A superior, foreign-region BPDU: a lower root than what is stored, and
	// no matching ConfigID, so this bridge stores it as external information.
	foreignRegion := MST{Name: "region-2"}
	foreignConfigID := foreignRegion.ConfigID()
	b := BPDU{
		RootID:       BridgeID{Priority: 1024},
		BridgeID:     BridgeID{Priority: 1024},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
		ConfigID:     &foreignConfigID,
	}
	b.SetRole(RoleDesignated)

	l.Receive(t0.Add(time.Second), "1/1/1", b)

	if !p.external {
		t.Fatal("port not classified external after a foreign-region BPDU")
	}
	if p.rcvRegionalRootID != (BridgeID{}) || p.rcvInternalRootPathCost != 0 || p.rcvRemainingHops != 0 {
		t.Errorf("internal-only fields survived the flip: regionalRoot=%v internalCost=%d remainingHops=%d, want all cleared",
			p.rcvRegionalRootID, p.rcvInternalRootPathCost, p.rcvRemainingHops)
	}

	// With the stale internal information cleared, a tree whose root port is
	// this one and whose regional root differs from its own bridge
	// (simulating a bridge that is not this instance's regional root)
	// originates MaxHops rather than a decreasing count derived from the
	// stale value.
	cist.rootPort = "1/1/1"
	cist.regionalRootID = BridgeID{Priority: 1}
	want := effectiveMaxHops(l.mst.MaxHops)
	if got := l.instanceRemainingHops(cist); got != want {
		t.Errorf("instanceRemainingHops = %d, want MaxHops %d once the stale hop count is cleared", got, want)
	}
}

// TestMSTITopologyChangeBitReachesAndFlushesThePeer pins that a per-instance
// topology change is carried on the wire and acted on. Bridge A raises a
// change on MSTI 1 alone; the BPDU it emits must carry the bit in MSTI 1's
// record and none in MSTI 2's; bridge B receiving it must flush MSTI 1's
// VLAN on its other ports and leave MSTI 2's VLAN alone.
func TestMSTITopologyChangeBitReachesAndFlushesThePeer(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	region := MST{
		Name: "region-1",
		Instances: map[MSTID]Instance{
			1: {VLANs: []vlan.ID{10}},
			2: {VLANs: []vlan.ID{20}},
		},
	}

	a := newLayer(Config{
		Priority: 4096,
		Ports:    map[string]Port{"p1": {}},
		MST:      &region,
	}.Normalize())
	a.LinkChange(t0, "p1", true, true, 1_000_000_000)

	b := newLayer(Config{
		Priority: 32768,
		Ports:    map[string]Port{"p1": {}, "p2": {}},
		MST:      &region,
	}.Normalize())
	b.LinkChange(t0, "p1", true, true, 1_000_000_000)
	b.LinkChange(t0, "p2", true, true, 1_000_000_000)

	// Converge and settle on A's hellos, refreshed every hello time so B's
	// stored information about A never ages out, until both instances are
	// well past their forward delay ladders.
	now := t0
	for i := 0; i < 20; i++ {
		now = now.Add(2 * time.Second)
		fx := a.Wake(now)
		for _, e := range fx.Emissions {
			dec, err := Decode(e.Frame)
			if err != nil {
				t.Fatalf("decode A's emission: %v", err)
			}
			b.Receive(now, "p1", dec)
		}
		b.Wake(now)
	}

	if info := b.PortInfo("p1"); info.Role != RoleRoot {
		t.Fatalf("B's p1 role = %v, want Root before the topology change under test", info.Role)
	}

	// Raise a topology change on MSTI 1 alone on A, as recompute would from
	// an instance-only role or state change on an internal port, without
	// touching the CIST's own timer.
	a.trees[treeID(1)].topologyChangeTimer = now.Add(10 * time.Second)

	now = now.Add(2 * time.Second)
	fx := a.Wake(now)
	if len(fx.Emissions) == 0 {
		t.Fatal("no hello emission at the scheduled hello time")
	}
	dec, err := Decode(fx.Emissions[0].Frame)
	if err != nil {
		t.Fatalf("decode A's emission: %v", err)
	}

	var rec1, rec2 MSTIRecord
	for _, r := range dec.MSTIs {
		switch r.MSTID {
		case 1:
			rec1 = r
		case 2:
			rec2 = r
		}
	}
	if !(BPDU{Flags: rec1.Flags}).TopologyChange() {
		t.Error("MSTI 1's record does not carry the topology change bit")
	}
	if (BPDU{Flags: rec2.Flags}).TopologyChange() {
		t.Error("MSTI 2's record carries the topology change bit, want none: only MSTI 1 changed")
	}

	fxB := b.Receive(now, "p1", dec)

	var target *FlushTarget
	for i := range fxB.Flush {
		if fxB.Flush[i].Port == "p2" {
			target = &fxB.Flush[i]
		}
	}
	if target == nil {
		t.Fatalf("Flush = %v, want a target for p2", fxB.Flush)
	}
	if len(target.FIDs) != 1 || target.FIDs[0] != vlan.ID(10) {
		t.Errorf("flush FIDs for p2 = %v, want [10]: only MSTI 1's VLAN, not MSTI 2's VLAN 20", target.FIDs)
	}
}

// TestBoundaryFlipClearsAStaleForwardDelayTimer pins that a port flipping
// from internal to boundary clears any live fwdDelayTimer an MSTI's own
// ladder was running, at the same recompute that mirrors its role and state
// from the CIST. Left set, the next wake would advance the instance port on
// a timer behind a state recompute already overwrote, raising a topology
// change with nothing behind it.
func TestBoundaryFlipClearsAStaleForwardDelayTimer(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	region := MST{
		Name: "region-1",
		Instances: map[MSTID]Instance{
			1: {VLANs: []vlan.ID{10}},
		},
	}
	l := newLayer(Config{
		Priority: 32768,
		Ports: map[string]Port{
			"1/1/1": {},
			"1/1/2": {},
		},
		MST: &region,
	}.Normalize())

	l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)
	l.LinkChange(t0, "1/1/2", true, true, 1_000_000_000)

	mstiPort := l.trees[treeID(1)].ports["1/1/1"]
	if mstiPort.fwdDelayTimer.IsZero() {
		t.Fatal("MSTI 1's port has no live forward delay timer before the flip; the test proves nothing")
	}

	// A superior foreign-region BPDU makes "1/1/1" a boundary port and, being
	// superior, this bridge's CIST root port.
	foreignRegion := MST{Name: "region-2"}
	foreignConfigID := foreignRegion.ConfigID()
	b := BPDU{
		RootID:       BridgeID{Priority: 4096},
		BridgeID:     BridgeID{Priority: 4096},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
		ConfigID:     &foreignConfigID,
	}
	b.SetRole(RoleDesignated)

	l.Receive(t0.Add(time.Second), "1/1/1", b)

	if !l.boundary("1/1/1") {
		t.Fatal("port not classified boundary after the foreign-region BPDU")
	}
	if !mstiPort.fwdDelayTimer.IsZero() {
		t.Fatalf("MSTI 1's forward delay timer = %v, want cleared once the port becomes boundary", mstiPort.fwdDelayTimer)
	}

	before := l.trees[treeID(1)].topologyChangeCount

	// Past the deadline the stale timer would have fired at, had it survived.
	l.Wake(t0.Add(20 * time.Second))

	if got := l.trees[treeID(1)].topologyChangeCount; got != before {
		t.Errorf("MSTI 1 topology change count = %d, want unchanged at %d: no timer should have fired", got, before)
	}
	if !mstiPort.fwdDelayTimer.IsZero() {
		t.Errorf("MSTI 1's forward delay timer = %v, want to stay cleared on a boundary port", mstiPort.fwdDelayTimer)
	}
}

// TestPVSTTreeMappingCoversEveryVLAN pins the mapping a PVST bridge builds:
// VLAN 1's tree holds the CIST slot, every other VLAN gets its own treeID,
// and every tree names exactly its own VLAN in treeVLANs. The CIST's entry is
// the one worth pinning: MSTP leaves it absent so a CIST flush widens to every
// FID, and PVST must not, since VLAN 1's tree carries VLAN 1 alone.
func TestPVSTTreeMappingCoversEveryVLAN(t *testing.T) {
	t.Parallel()

	l := newLayer(Config{
		Priority: 4096,
		Ports:    map[string]Port{"l1": {}, "l2": {}},
		PVST:     &PVST{Trees: map[vlan.ID]Tree{1: {}, 10: {}, 20: {}}},
	}.Normalize())

	wantOrder := []treeID{cistID, treeID(10), treeID(20)}
	if !slices.Equal(l.treeOrder, wantOrder) {
		t.Fatalf("treeOrder = %v, want %v", l.treeOrder, wantOrder)
	}

	for vid, want := range map[vlan.ID]treeID{1: cistID, 10: treeID(10), 20: treeID(20)} {
		if got := l.vidToTree[vid]; got != want {
			t.Errorf("vidToTree[%d] = %d, want %d", vid, got, want)
		}
		got, ok := l.treeFor(vid)
		if !ok {
			t.Errorf("treeFor(%d) = false, want tree{id:%d, vid:%d}", vid, want, vid)
			continue
		}
		if got.id != want || got.vid != vid {
			t.Errorf("treeFor(%d) = tree{id:%d, vid:%d}, want tree{id:%d, vid:%d}", vid, got.id, got.vid, want, vid)
		}
	}

	for id, want := range map[treeID][]vlan.ID{cistID: {1}, treeID(10): {10}, treeID(20): {20}} {
		if got := l.treeVLANs[id]; !slices.Equal(got, want) {
			t.Errorf("treeVLANs[%d] = %v, want %v", id, got, want)
		}
	}
}

// TestTransmitBudgetKeyingFollowsTheMode pins where the transmit budget
// lives. Outside PVST mode every tree shares one budget per port, which is
// what IEEE 802.1Q meters and what lets an MST bridge spend one slot for the
// CIST BPDU carrying every instance's record. Inside it each tree meters its
// own, because each VLAN puts a frame of its own on the wire.
func TestTransmitBudgetKeyingFollowsTheMode(t *testing.T) {
	t.Parallel()

	mstLayer := newLayer(Config{
		Ports: map[string]Port{"l1": {}, "l2": {}},
		MST: &MST{Name: "region-1", Revision: 1, Instances: map[MSTID]Instance{
			1: {VLANs: []vlan.ID{10}},
			2: {VLANs: []vlan.ID{20}},
		}},
	}.Normalize())

	if got := len(mstLayer.portTx); got != 2 {
		t.Errorf("MST bridge holds %d transmit budgets over 2 ports and 3 trees, want 2", got)
	}
	for _, id := range mstLayer.treeOrder {
		if got := mstLayer.tx(mstLayer.trees[id], "l1"); got != mstLayer.tx(mstLayer.cist(), "l1") {
			t.Errorf("tree %d on l1 resolved to its own budget, want the CIST's", id)
		}
	}

	pvstLayer := newLayer(Config{
		Ports: map[string]Port{"l1": {}, "l2": {}},
		PVST:  &PVST{Trees: map[vlan.ID]Tree{1: {}, 10: {}, 20: {}}},
	}.Normalize())

	if got := len(pvstLayer.portTx); got != 6 {
		t.Errorf("PVST bridge holds %d transmit budgets over 2 ports and 3 trees, want 6", got)
	}
	seen := make(map[*portTx]struct{}, 3)
	for _, id := range pvstLayer.treeOrder {
		tx := pvstLayer.tx(pvstLayer.trees[id], "l1")
		if tx == nil {
			t.Fatalf("tree %d has no transmit budget on l1", id)
		}
		if _, dup := seen[tx]; dup {
			t.Errorf("tree %d on l1 shares a budget with an earlier tree, want its own", id)
		}
		seen[tx] = struct{}{}
	}
}
