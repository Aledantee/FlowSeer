package stp_test

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

func mustPortTable(t *testing.T, names ...string) port.Table {
	t.Helper()
	b := port.NewBuilder()
	for _, n := range names {
		b.Add(port.Port{Name: n, Kind: port.Physical})
	}
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}
	return tbl
}

func mustMAC(t *testing.T, s string) netaddr.MAC {
	t.Helper()
	m, err := netaddr.Parse(s)
	if err != nil {
		t.Fatalf("parse MAC %q: %v", s, err)
	}
	return m
}

func TestTwoBridgesExchange(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	mac1 := mustMAC(t, "00:11:22:33:44:01")
	mac2 := mustMAC(t, "00:11:22:33:44:02")

	tbl1 := mustPortTable(t, "1/1/1")
	tbl2 := mustPortTable(t, "1/1/1")

	cfg1 := stp.Config{
		Priority: 4096,
		Address:  mac1,
		Ports: map[string]stp.Port{
			"1/1/1": {Priority: 128},
		},
	}
	cfg2 := stp.Config{
		Priority: 32768,
		Address:  mac2,
		Ports: map[string]stp.Port{
			"1/1/1": {Priority: 128},
		},
	}

	sw1 := stp.New(cfg1, tbl1)
	sw2 := stp.New(cfg2, tbl2)

	// Link up on both
	fx1 := sw1.LinkChange(now, "1/1/1", true, true, 1_000_000_000)
	sw2.LinkChange(now, "1/1/1", true, true, 1_000_000_000)

	// sw1 emitted a proposal on coming up
	if len(fx1.Emissions) != 1 {
		t.Fatalf("sw1 emissions count = %d, want 1", len(fx1.Emissions))
	}
	proposalBPDU, err := stp.Decode(fx1.Emissions[0].Frame)
	if err != nil {
		t.Fatalf("decode sw1 proposal: %v", err)
	}
	if !proposalBPDU.Proposal() {
		t.Fatal("proposal flag not set on sw1 BPDU")
	}

	// Feed sw1 proposal to sw2
	fx2 := sw2.Receive(now, "1/1/1", proposalBPDU)
	info2 := sw2.PortInfo("1/1/1")
	if info2.Role != stp.RoleRoot {
		t.Errorf("sw2 port role = %v, want %v", info2.Role, stp.RoleRoot)
	}
	if info2.State != stp.StateForwarding {
		t.Errorf("sw2 port state = %v, want %v", info2.State, stp.StateForwarding)
	}

	// sw2 answered with an agreement
	if len(fx2.Emissions) != 1 {
		t.Fatalf("sw2 emissions count = %d, want 1", len(fx2.Emissions))
	}
	agreementBPDU, err := stp.Decode(fx2.Emissions[0].Frame)
	if err != nil {
		t.Fatalf("decode sw2 agreement: %v", err)
	}
	if !agreementBPDU.Agreement() {
		t.Fatal("agreement flag not set on sw2 BPDU")
	}

	// Feed sw2 agreement to sw1
	sw1.Receive(now, "1/1/1", agreementBPDU)
	info1 := sw1.PortInfo("1/1/1")
	if info1.Role != stp.RoleDesignated {
		t.Errorf("sw1 port role = %v, want %v", info1.Role, stp.RoleDesignated)
	}
	if info1.State != stp.StateForwarding {
		t.Errorf("sw1 port state = %v, want %v", info1.State, stp.StateForwarding)
	}
}

func TestThreeBridgeRingConvergence(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	now := startTime

	mac1 := mustMAC(t, "00:11:22:33:44:01")
	mac2 := mustMAC(t, "00:11:22:33:44:02")
	mac3 := mustMAC(t, "00:11:22:33:44:03")

	tbl1 := mustPortTable(t, "p1", "p2")
	tbl2 := mustPortTable(t, "p1", "p2")
	tbl3 := mustPortTable(t, "p1", "p2")

	sw1 := stp.New(stp.Config{
		Priority: 4096,
		Address:  mac1,
		Ports: map[string]stp.Port{
			"p1": {Priority: 128},
			"p2": {Priority: 128},
		},
	}, tbl1)

	sw2 := stp.New(stp.Config{
		Priority: 8192,
		Address:  mac2,
		Ports: map[string]stp.Port{
			"p1": {Priority: 128},
			"p2": {Priority: 128},
		},
	}, tbl2)

	sw3 := stp.New(stp.Config{
		Priority: 12288,
		Address:  mac3,
		Ports: map[string]stp.Port{
			"p1": {Priority: 128},
			"p2": {Priority: 128},
		},
	}, tbl3)

	layers := []*stp.Layer{sw1, sw2, sw3}

	type cable struct {
		swA   int
		portA string
		swB   int
		portB string
	}

	cables := []cable{
		{swA: 0, portA: "p1", swB: 1, portB: "p1"}, // sw1 p1 <-> sw2 p1
		{swA: 0, portA: "p2", swB: 2, portB: "p1"}, // sw1 p2 <-> sw3 p1
		{swA: 1, portA: "p2", swB: 2, portB: "p2"}, // sw2 p2 <-> sw3 p2
	}

	findPeer := func(srcSw int, srcPort string) (int, string, bool) {
		for _, c := range cables {
			if c.swA == srcSw && c.portA == srcPort {
				return c.swB, c.portB, true
			}
			if c.swB == srcSw && c.portB == srcPort {
				return c.swA, c.portA, true
			}
		}
		return 0, "", false
	}

	type packet struct {
		targetSw   int
		targetPort string
		frame      ethernet.Frame
	}

	var queue []packet

	// Initialize links
	for i, l := range layers {
		for _, pName := range []string{"p1", "p2"} {
			fx := l.LinkChange(now, pName, true, true, 1_000_000_000)
			for _, em := range fx.Emissions {
				peerSw, peerPort, ok := findPeer(i, em.Port)
				if ok {
					queue = append(queue, packet{targetSw: peerSw, targetPort: peerPort, frame: em.Frame})
				}
			}
		}
	}

	// Scheduler loop
	type snapshotKey struct {
		sw    int
		port  string
		role  stp.Role
		state stp.State
	}

	takeSnapshot := func() []snapshotKey {
		var s []snapshotKey
		for i, l := range layers {
			for _, pName := range []string{"p1", "p2"} {
				info := l.PortInfo(pName)
				s = append(s, snapshotKey{sw: i, port: pName, role: info.Role, state: info.State})
			}
		}
		return s
	}

	var prevSnapshot []snapshotKey
	stableRounds := 0

	for round := 0; round < 200; round++ {
		currentSnapshot := takeSnapshot()
		if slices.Equal(currentSnapshot, prevSnapshot) {
			stableRounds++
			if stableRounds >= 2 && len(queue) == 0 {
				break
			}
		} else {
			stableRounds = 0
			prevSnapshot = currentSnapshot
		}

		if len(queue) > 0 {
			batch := queue
			queue = nil
			for _, pkt := range batch {
				bpdu, err := stp.Decode(pkt.frame)
				if err != nil {
					t.Fatalf("decode packet for sw%d %s: %v", pkt.targetSw+1, pkt.targetPort, err)
				}
				fx := layers[pkt.targetSw].Receive(now, pkt.targetPort, bpdu)
				for _, em := range fx.Emissions {
					peerSw, peerPort, ok := findPeer(pkt.targetSw, em.Port)
					if ok {
						queue = append(queue, packet{targetSw: peerSw, targetPort: peerPort, frame: em.Frame})
					}
				}
			}
			continue
		}

		// Advance to earliest NextWake
		var earliestWake time.Time
		hasWake := false
		for _, l := range layers {
			if w, ok := l.NextWake(); ok {
				if !hasWake || w.Before(earliestWake) {
					earliestWake = w
					hasWake = true
				}
			}
		}

		if !hasWake {
			break
		}

		now = earliestWake
		for i, l := range layers {
			fx := l.Wake(now)
			for _, em := range fx.Emissions {
				peerSw, peerPort, ok := findPeer(i, em.Port)
				if ok {
					queue = append(queue, packet{targetSw: peerSw, targetPort: peerPort, frame: em.Frame})
				}
			}
		}
	}

	// Assert roles and states
	rootID, rootCost, rootPort := sw1.Root()
	if rootID.Priority != 4096 || rootCost != 0 || rootPort != "" {
		t.Errorf("sw1 Root: got (%v, %d, %q), want (4096, 0, \"\")", rootID, rootCost, rootPort)
	}

	// sw2: p1 facing sw1 is Root, p2 facing sw3 is Designated
	sw2p1 := sw2.PortInfo("p1")
	if sw2p1.Role != stp.RoleRoot || sw2p1.State != stp.StateForwarding {
		t.Errorf("sw2 p1: got role %v, state %v; want Root Forwarding", sw2p1.Role, sw2p1.State)
	}
	sw2p2 := sw2.PortInfo("p2")
	if sw2p2.Role != stp.RoleDesignated || sw2p2.State != stp.StateForwarding {
		t.Errorf("sw2 p2: got role %v, state %v; want Designated Forwarding", sw2p2.Role, sw2p2.State)
	}

	// sw3: p1 facing sw1 is Root, p2 facing sw2 is Alternate Discarding
	sw3p1 := sw3.PortInfo("p1")
	if sw3p1.Role != stp.RoleRoot || sw3p1.State != stp.StateForwarding {
		t.Errorf("sw3 p1: got role %v, state %v; want Root Forwarding", sw3p1.Role, sw3p1.State)
	}
	sw3p2 := sw3.PortInfo("p2")
	if sw3p2.Role != stp.RoleAlternate || sw3p2.State != stp.StateDiscarding {
		t.Errorf("sw3 p2: got role %v, state %v; want Alternate Discarding", sw3p2.Role, sw3p2.State)
	}

	// Verify that ports facing sw1 reached Forwarding without forward delay passing
	if now.Sub(startTime) >= 15*time.Second {
		t.Errorf("ring converged at %v, but expected convergence without forward delay passing", now.Sub(startTime))
	}

	// Test link-down on sw3 Root port (p1): Alternate (p2) becomes Root with a flush
	fxDown := sw3.LinkChange(now, "p1", false, false, 0)
	sw3RootID, sw3RootCost, sw3RootPort := sw3.Root()
	if sw3RootPort != "p2" {
		t.Errorf("sw3 Root port after p1 down: got %q, want \"p2\"", sw3RootPort)
	}
	if sw3RootID.Priority != 4096 {
		t.Errorf("sw3 RootID priority: got %d, want 4096", sw3RootID.Priority)
	}
	if sw3RootCost != 40000 {
		t.Errorf("sw3 Root path cost: got %d, want 40000", sw3RootCost)
	}
	infoP2 := sw3.PortInfo("p2")
	if infoP2.Role != stp.RoleRoot || infoP2.State != stp.StateForwarding {
		t.Errorf("sw3 p2 after failover: got role %v, state %v; want Root Forwarding", infoP2.Role, infoP2.State)
	}
	if !slices.Contains(fxDown.Flush, "p2") {
		t.Errorf("sw3 LinkChange flushes: got %v, want flush containing \"p2\"", fxDown.Flush)
	}
}

func TestSharedPortForwardDelay(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	now := startTime

	mac := mustMAC(t, "00:11:22:33:44:01")
	tbl := mustPortTable(t, "1/1/1")
	l := stp.New(stp.Config{
		Priority: 32768,
		Address:  mac,
		Ports: map[string]stp.Port{
			"1/1/1": {Priority: 128},
		},
	}, tbl)

	// Port comes up on shared link (pointToPoint = false)
	l.LinkChange(now, "1/1/1", true, false, 1_000_000_000)

	info := l.PortInfo("1/1/1")
	if info.State != stp.StateDiscarding {
		t.Fatalf("initial state = %v, want Discarding", info.State)
	}

	// Advance time step-by-step through NextWake and Wake
	reachedLearningAt := time.Time{}
	reachedForwardingAt := time.Time{}

	for step := 0; step < 50; step++ {
		next, ok := l.NextWake()
		if !ok {
			break
		}
		now = next
		l.Wake(now)

		info = l.PortInfo("1/1/1")
		if info.State == stp.StateLearning && reachedLearningAt.IsZero() {
			reachedLearningAt = now
		}
		if info.State == stp.StateForwarding && reachedForwardingAt.IsZero() {
			reachedForwardingAt = now
			break
		}
	}

	if want := startTime.Add(15 * time.Second); reachedLearningAt != want {
		t.Errorf("reached Learning at %v, want %v", reachedLearningAt, want)
	}
	if want := startTime.Add(30 * time.Second); reachedForwardingAt != want {
		t.Errorf("reached Forwarding at %v, want %v (two forward delays)", reachedForwardingAt, want)
	}
}

func TestEdgePortForwardingAtOnce(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	mac := mustMAC(t, "00:11:22:33:44:01")
	tbl := mustPortTable(t, "1/1/1")
	l := stp.New(stp.Config{
		Priority: 32768,
		Address:  mac,
		Ports: map[string]stp.Port{
			"1/1/1": {Priority: 128, AdminEdge: true},
		},
	}, tbl)

	l.LinkChange(now, "1/1/1", true, true, 1_000_000_000)

	info := l.PortInfo("1/1/1")
	if info.State != stp.StateForwarding {
		t.Fatalf("edge port state = %v, want Forwarding", info.State)
	}
	if !info.Edge {
		t.Error("Edge = false, want true")
	}
	if info.ForwardTransitions != 1 {
		t.Errorf("ForwardTransitions = %d, want 1", info.ForwardTransitions)
	}
}

func TestInformationAging(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	now := startTime

	mac1 := mustMAC(t, "00:11:22:33:44:01")
	mac2 := mustMAC(t, "00:11:22:33:44:02")
	tbl := mustPortTable(t, "1/1/1")

	l := stp.New(stp.Config{
		Priority: 32768,
		Address:  mac2,
		Ports: map[string]stp.Port{
			"1/1/1": {Priority: 128},
		},
	}, tbl)

	l.LinkChange(now, "1/1/1", true, true, 1_000_000_000)

	// Receive superior BPDU from root 4096 with HelloTime 2s
	rootBPDU := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: mac1},
		RootPathCost: 0,
		BridgeID:     stp.BridgeID{Priority: 4096, Address: mac1},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	l.Receive(now, "1/1/1", rootBPDU)

	rootID, _, rootPort := l.Root()
	if rootID.Priority != 4096 || rootPort != "1/1/1" {
		t.Fatalf("Root = (%v, %q), want (4096, \"1/1/1\")", rootID, rootPort)
	}

	// Advance 5 seconds: not yet aged out (3 * 2s = 6s)
	now = now.Add(5 * time.Second)
	l.Wake(now)

	rootID, _, _ = l.Root()
	if rootID.Priority != 4096 {
		t.Fatalf("Root after 5s = %v, want 4096", rootID)
	}

	// Advance past 6 seconds
	now = startTime.Add(6 * time.Second)
	l.Wake(now)

	rootID, _, rootPort = l.Root()
	if rootID.Priority != 32768 || rootPort != "" {
		t.Errorf("Root after aging = (%v, %q), want (32768, \"\")", rootID, rootPort)
	}
	info := l.PortInfo("1/1/1")
	if info.Role != stp.RoleDesignated {
		t.Errorf("port role after aging = %v, want Designated", info.Role)
	}
}

func TestCloneIndependence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	mac := mustMAC(t, "00:11:22:33:44:01")
	tbl := mustPortTable(t, "1/1/1", "1/1/2")

	l1 := stp.New(stp.Config{
		Priority: 4096,
		Address:  mac,
		Ports: map[string]stp.Port{
			"1/1/1": {Priority: 128},
			"1/1/2": {Priority: 128},
		},
	}, tbl)

	l1.LinkChange(now, "1/1/1", true, true, 1_000_000_000)

	l2 := l1.Clone()

	// Verify l2 has same initial status
	info1 := l1.PortInfo("1/1/1")
	info2 := l2.PortInfo("1/1/1")
	if info1 != info2 {
		t.Fatalf("cloned PortInfo mismatch: l1=%+v, l2=%+v", info1, info2)
	}

	// Mutate l2: bring up 1/1/2 on l2 only
	l2.LinkChange(now, "1/1/2", true, true, 1_000_000_000)

	if l1.PortInfo("1/1/2").Role != stp.RoleDisabled {
		t.Errorf("l1 1/1/2 role = %v, want Disabled", l1.PortInfo("1/1/2").Role)
	}
	if l2.PortInfo("1/1/2").Role != stp.RoleDesignated {
		t.Errorf("l2 1/1/2 role = %v, want Designated", l2.PortInfo("1/1/2").Role)
	}
}

func TestUntrackedPortLearnsAndForwards(t *testing.T) {
	t.Parallel()

	mac := mustMAC(t, "00:11:22:33:44:01")
	tbl := mustPortTable(t, "1/1/1")

	l := stp.New(stp.Config{
		Priority: 32768,
		Address:  mac,
		Ports: map[string]stp.Port{
			"1/1/1": {Priority: 128},
		},
	}, tbl)

	// Port 1/1/99 is untracked by STP
	if !l.Learns("1/1/99") {
		t.Error("Learns(\"1/1/99\") = false, want true for untracked port")
	}
	if !l.Forwards("1/1/99") {
		t.Error("Forwards(\"1/1/99\") = false, want true for untracked port")
	}
}
