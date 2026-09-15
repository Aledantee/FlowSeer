package stp_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
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

func mustNewSTP(t *testing.T, cfg stp.Config, ports port.Table) *stp.Layer {
	t.Helper()
	l, err := stp.New(cfg, ports)
	if err != nil {
		t.Fatalf("stp.New: %v", err)
	}
	return l
}

// flushPorts extracts the port names named by a flush list, in order, for
// tests that only care which ports were named and not their FIDs.
func flushPorts(targets []stp.FlushTarget) []string {
	names := make([]string, len(targets))
	for i, target := range targets {
		names[i] = target.Port
	}
	return names
}

// flushTarget returns the flush target for the named port and whether one
// exists.
func flushTarget(targets []stp.FlushTarget, port string) (stp.FlushTarget, bool) {
	for _, target := range targets {
		if target.Port == port {
			return target, true
		}
	}
	return stp.FlushTarget{}, false
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

	sw1 := mustNewSTP(t, cfg1, tbl1)
	sw2 := mustNewSTP(t, cfg2, tbl2)

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

func TestExplicitZeroPrioritiesParticipateInElections(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	localMAC := mustMAC(t, "00:11:22:33:44:02")
	rootMAC := mustMAC(t, "00:11:22:33:44:01")
	l := mustNewSTP(t, stp.Config{
		Priority:        0,
		PriorityPresent: true,
		Address:         localMAC,
		Ports: map[string]stp.Port{
			"preferred": {Priority: 0, PriorityPresent: true},
			"defaulted": {},
		},
	}, mustPortTable(t, "preferred", "defaulted"))

	if got := l.BridgeID().Priority; got != 0 {
		t.Fatalf("bridge election priority = %d, want explicit zero", got)
	}

	l.LinkChange(now, "preferred", true, true, 1_000_000_000)
	l.LinkChange(now, "defaulted", true, true, 1_000_000_000)
	bpdu := stp.BPDU{
		Version:      2,
		Type:         stp.BPDUTypeRapid,
		RootID:       stp.BridgeID{Priority: 0, Address: rootMAC},
		BridgeID:     stp.BridgeID{Priority: 0, Address: rootMAC},
		PortID:       0x8001,
		HelloTime:    stp.DefaultHelloTime,
		MaxAge:       stp.DefaultMaxAge,
		ForwardDelay: stp.DefaultForwardDelay,
	}
	bpdu.SetRole(stp.RoleDesignated)
	l.Receive(now, "defaulted", bpdu)
	l.Receive(now, "preferred", bpdu)

	if _, _, rootPort := l.Root(); rootPort != "preferred" {
		t.Errorf("root port = %q, want explicit-priority-zero port", rootPort)
	}
	if got := l.PortInfo("preferred").Priority; got != 0 {
		t.Errorf("preferred port election priority = %d, want explicit zero", got)
	}
	if got := l.PortInfo("defaulted").Priority; got != stp.DefaultPortPriority {
		t.Errorf("defaulted port election priority = %d, want %d", got, stp.DefaultPortPriority)
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

	sw1 := mustNewSTP(t, stp.Config{
		Priority: 4096,
		Address:  mac1,
		Ports: map[string]stp.Port{
			"p1": {Priority: 128},
			"p2": {Priority: 128},
		},
	}, tbl1)

	sw2 := mustNewSTP(t, stp.Config{
		Priority: 8192,
		Address:  mac2,
		Ports: map[string]stp.Port{
			"p1": {Priority: 128},
			"p2": {Priority: 128},
		},
	}, tbl2)

	sw3 := mustNewSTP(t, stp.Config{
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
	if !slices.Contains(flushPorts(fxDown.Flush), "p2") {
		t.Errorf("sw3 LinkChange flushes: got %v, want flush containing \"p2\"", fxDown.Flush)
	}
}

func TestSharedPortForwardDelay(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	now := startTime

	mac := mustMAC(t, "00:11:22:33:44:01")
	tbl := mustPortTable(t, "1/1/1")
	l := mustNewSTP(t, stp.Config{
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
	l := mustNewSTP(t, stp.Config{
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

	l := mustNewSTP(t, stp.Config{
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

	l1 := mustNewSTP(t, stp.Config{
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

	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mac,
		Ports: map[string]stp.Port{
			"1/1/1": {Priority: 128},
		},
	}, tbl)

	// Port 1/1/99 is untracked by STP
	if !l.Learns("1/1/99", 0) {
		t.Error("Learns(\"1/1/99\") = false, want true for untracked port")
	}
	if !l.Forwards("1/1/99", 0) {
		t.Error("Forwards(\"1/1/99\") = false, want true for untracked port")
	}
}

// TestDesignatedPointToPointForwardsWithoutAgreement is evidence for the
// forward-delay fallback: a designated port on a point-to-point link whose
// peer never answers a proposal, such as a host, still reaches Forwarding
// after two forward delays.
func TestDesignatedPointToPointForwardsWithoutAgreement(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "00:11:22:33:44:01"),
		Ports:    map[string]stp.Port{"1/1/1": {}},
	}, mustPortTable(t, "1/1/1"))

	l.LinkChange(now, "1/1/1", true, true, 1_000_000_000)
	if info := l.PortInfo("1/1/1"); info.Role != stp.RoleDesignated || info.State != stp.StateDiscarding {
		t.Fatalf("after link up: role %v state %v, want Designated Discarding", info.Role, info.State)
	}

	l.Wake(now.Add(stp.DefaultForwardDelay - time.Millisecond))
	if info := l.PortInfo("1/1/1"); info.State != stp.StateDiscarding {
		t.Fatalf("just before the forward delay: state %v, want Discarding", info.State)
	}
	l.Wake(now.Add(stp.DefaultForwardDelay))
	if info := l.PortInfo("1/1/1"); info.State != stp.StateLearning {
		t.Fatalf("after one forward delay: state %v, want Learning", info.State)
	}
	l.Wake(now.Add(2 * stp.DefaultForwardDelay))
	if info := l.PortInfo("1/1/1"); info.State != stp.StateForwarding {
		t.Fatalf("after two forward delays: state %v, want Forwarding", info.State)
	}
}

// TestLinkDownFlushesPortAndRepeatIsSilent is evidence that a link going
// down names the port for a flush with no FIDs, meaning every FID on it,
// since the entries stale a dead link leaves behind belong to whatever tree
// was using it. Reporting a link state the layer already holds produces no
// effect.
func TestLinkDownFlushesPortAndRepeatIsSilent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "00:11:22:33:44:01"),
		Ports:    map[string]stp.Port{"1/1/1": {}, "1/1/2": {}},
	}, mustPortTable(t, "1/1/1", "1/1/2"))

	up := l.LinkChange(now, "1/1/1", true, true, 1_000_000_000)
	if len(up.Emissions) != 1 {
		t.Fatalf("first link up emitted %d frames, want 1", len(up.Emissions))
	}
	again := l.LinkChange(now, "1/1/1", true, true, 1_000_000_000)
	if len(again.Emissions) != 0 || len(again.Flush) != 0 {
		t.Fatalf("repeated link up produced %+v, want nothing", again)
	}

	down := l.LinkChange(now.Add(time.Second), "1/1/1", false, true, 0)
	target, ok := flushTarget(down.Flush, "1/1/1")
	if !ok {
		t.Fatalf("link down flushes %v, want 1/1/1 among them", down.Flush)
	}
	if len(target.FIDs) != 0 {
		t.Errorf("link down flush target FIDs = %v, want none: a dead port flushes every FID", target.FIDs)
	}
	if info := l.PortInfo("1/1/1"); info.Role != stp.RoleDisabled {
		t.Errorf("after link down: role %v, want Disabled", info.Role)
	}
	downAgain := l.LinkChange(now.Add(2*time.Second), "1/1/1", false, true, 0)
	if len(downAgain.Flush) != 0 || len(downAgain.Emissions) != 0 {
		t.Fatalf("repeated link down produced %+v, want nothing", downAgain)
	}
}

// TestTimesInForceFollowTheRoot is evidence that Times reports the root's
// timer values on a non-root bridge and the bridge's own while it is root.
func TestTimesInForceFollowTheRoot(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "00:11:22:33:44:02"),
		Ports:    map[string]stp.Port{"1/1/1": {}},
	}, mustPortTable(t, "1/1/1"))
	l.LinkChange(now, "1/1/1", true, true, 1_000_000_000)

	if maxAge, hello, fwd := l.Times(); maxAge != stp.DefaultMaxAge || hello != stp.DefaultHelloTime || fwd != stp.DefaultForwardDelay {
		t.Fatalf("own times = %v %v %v, want defaults", maxAge, hello, fwd)
	}

	root := stp.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}
	b := stp.BPDU{
		RootID: root, BridgeID: root, PortID: 0x8001,
		MaxAge: 30 * time.Second, HelloTime: 3 * time.Second, ForwardDelay: 20 * time.Second,
	}
	b.SetRole(stp.RoleDesignated)
	l.Receive(now, "1/1/1", b)

	if l.BridgeID() != (stp.BridgeID{Priority: 32768, Address: mustMAC(t, "00:11:22:33:44:02")}) {
		t.Errorf("BridgeID = %+v", l.BridgeID())
	}
	if maxAge, hello, fwd := l.Times(); maxAge != 30*time.Second || hello != 3*time.Second || fwd != 20*time.Second {
		t.Errorf("times in force = %v %v %v, want the root's 30s 3s 20s", maxAge, hello, fwd)
	}
	if info := l.PortInfo("1/1/1"); info.DesignatedRoot != root || info.Priority != stp.DefaultPortPriority {
		t.Errorf("PortInfo = %+v, want designated root %+v and default priority", info, root)
	}
}

// TestCompatibilityOnLegacyBPDU is evidence that a port hearing an inferior
// legacy Configuration BPDU outside the migration delay migrates to 802.1D
// compatibility mode, emits Configuration BPDUs without proposal flags,
// climbs the forward-delay ladder, and counts received and bad BPDUs.
func TestCompatibilityOnLegacyBPDU(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "02:00:00:00:00:02"),
		Ports: map[string]stp.Port{
			"1/1/1": {},
			"1/1/2": {},
		},
	}, mustPortTable(t, "1/1/1", "1/1/2"))

	l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)
	l.LinkChange(t0, "1/1/2", true, true, 1_000_000_000)

	inferiorConfig := stp.BPDU{
		Version:      0,
		Type:         stp.BPDUTypeConfiguration,
		RootID:       stp.BridgeID{Priority: 61440, Address: mustMAC(t, "02:00:00:00:00:0c")},
		BridgeID:     stp.BridgeID{Priority: 61440, Address: mustMAC(t, "02:00:00:00:00:0c")},
		RootPathCost: 0,
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	inferiorConfig.SetRole(stp.RoleDesignated)

	rx := l.Receive(t0.Add(4*time.Second), "1/1/1", inferiorConfig)
	if len(rx.Emissions) != 1 {
		t.Fatalf("reply emissions count = %d, want 1", len(rx.Emissions))
	}
	reply, err := stp.Decode(rx.Emissions[0].Frame)
	if err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if reply.Version != 0 || reply.Type != stp.BPDUTypeConfiguration {
		t.Errorf("reply BPDU version = %d type = %v, want 0 and Configuration", reply.Version, reply.Type)
	}
	if reply.Proposal() {
		t.Error("reply BPDU unexpectedly has proposal set")
	}

	w6 := l.Wake(t0.Add(6 * time.Second))
	for _, em := range w6.Emissions {
		b, err := stp.Decode(em.Frame)
		if err != nil {
			t.Fatalf("decode wake emission on %s: %v", em.Port, err)
		}
		if em.Port == "1/1/1" && b.Proposal() {
			t.Errorf("emission on %s has proposal set after migration", em.Port)
		}
		switch em.Port {
		case "1/1/1":
			if b.Version != 0 || b.Type != stp.BPDUTypeConfiguration {
				t.Errorf("1/1/1 hello version = %d type = %v, want 0 and Configuration", b.Version, b.Type)
			}
		case "1/1/2":
			if b.Version != 2 || b.Type != stp.BPDUTypeRapid {
				t.Errorf("1/1/2 hello version = %d type = %v, want 2 and Rapid", b.Version, b.Type)
			}
		}
	}

	if info := l.PortInfo("1/1/1"); info.SendRSTP {
		t.Errorf("PortInfo(1/1/1).SendRSTP = true, want false")
	}

	l.Wake(t0.Add(15 * time.Second))

	w29 := l.Wake(t0.Add(29 * time.Second))
	for _, em := range w29.Emissions {
		if b, err := stp.Decode(em.Frame); err == nil && em.Port == "1/1/1" && b.Proposal() {
			t.Errorf("wake 29s emission on %s has proposal set", em.Port)
		}
	}
	if state := l.PortInfo("1/1/1").State; state == stp.StateForwarding {
		t.Errorf("1/1/1 state at 29s = %v, want not Forwarding", state)
	}

	w30 := l.Wake(t0.Add(30 * time.Second))
	for _, em := range w30.Emissions {
		if b, err := stp.Decode(em.Frame); err == nil && em.Port == "1/1/1" && b.Proposal() {
			t.Errorf("wake 30s emission on %s has proposal set", em.Port)
		}
	}
	if state := l.PortInfo("1/1/1").State; state != stp.StateForwarding {
		t.Errorf("1/1/1 state at 30s = %v, want Forwarding", state)
	}

	if rxCount := l.PortInfo("1/1/1").RxBPDUs; rxCount != 1 {
		t.Errorf("PortInfo(1/1/1).RxBPDUs = %d, want 1", rxCount)
	}
	l.BadBPDU("1/1/1")
	l.BadBPDU("1/1/1")
	if badCount := l.PortInfo("1/1/1").BadBPDUs; badCount != 2 {
		t.Errorf("PortInfo(1/1/1).BadBPDUs = %d, want 2", badCount)
	}
}

// TestProtocolMigrationReturnToRSTP is evidence that Mcheck forces an operating
// port back to RSTP, and that a received RST BPDU does so only after the migration
// delay has passed.
func TestProtocolMigrationReturnToRSTP(t *testing.T) {
	t.Parallel()

	inferiorConfig := stp.BPDU{
		Version:      0,
		Type:         stp.BPDUTypeConfiguration,
		RootID:       stp.BridgeID{Priority: 61440, Address: mustMAC(t, "02:00:00:00:00:0c")},
		BridgeID:     stp.BridgeID{Priority: 61440, Address: mustMAC(t, "02:00:00:00:00:0c")},
		RootPathCost: 0,
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	inferiorConfig.SetRole(stp.RoleDesignated)

	inferiorRapid := stp.BPDU{
		Version:      2,
		Type:         stp.BPDUTypeRapid,
		RootID:       stp.BridgeID{Priority: 61440, Address: mustMAC(t, "02:00:00:00:00:0c")},
		BridgeID:     stp.BridgeID{Priority: 61440, Address: mustMAC(t, "02:00:00:00:00:0c")},
		RootPathCost: 0,
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	inferiorRapid.SetRole(stp.RoleDesignated)

	t.Run("mcheck", func(t *testing.T) {
		t.Parallel()
		t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
		l := mustNewSTP(t, stp.Config{
			Priority: 32768,
			Address:  mustMAC(t, "02:00:00:00:00:02"),
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		}, mustPortTable(t, "1/1/1", "1/1/2"))

		l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)
		l.LinkChange(t0, "1/1/2", true, true, 1_000_000_000)
		l.Receive(t0.Add(4*time.Second), "1/1/1", inferiorConfig)

		l.Mcheck(t0.Add(10*time.Second), "1/1/1")
		if !l.PortInfo("1/1/1").SendRSTP {
			t.Error("after Mcheck: SendRSTP = false, want true")
		}

		w12 := l.Wake(t0.Add(12 * time.Second))
		found := false
		for _, em := range w12.Emissions {
			if em.Port == "1/1/1" {
				b, err := stp.Decode(em.Frame)
				if err != nil {
					t.Fatalf("decode 1/1/1 hello: %v", err)
				}
				if b.Type != stp.BPDUTypeRapid {
					t.Errorf("1/1/1 hello type = %v, want Rapid", b.Type)
				}
				found = true
			}
		}
		if !found {
			t.Error("no hello emitted on 1/1/1 at 12s")
		}
	})

	t.Run("received rst bpdu", func(t *testing.T) {
		t.Parallel()
		t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
		l := mustNewSTP(t, stp.Config{
			Priority: 32768,
			Address:  mustMAC(t, "02:00:00:00:00:02"),
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		}, mustPortTable(t, "1/1/1", "1/1/2"))

		l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)
		l.LinkChange(t0, "1/1/2", true, true, 1_000_000_000)
		l.Receive(t0.Add(4*time.Second), "1/1/1", inferiorConfig)

		l.Receive(t0.Add(5*time.Second), "1/1/1", inferiorRapid)
		if l.PortInfo("1/1/1").SendRSTP {
			t.Error("SendRSTP returned to true inside migration delay at 5s")
		}

		l.Receive(t0.Add(10*time.Second), "1/1/1", inferiorRapid)
		if !l.PortInfo("1/1/1").SendRSTP {
			t.Error("SendRSTP remained false after delay expired at 10s")
		}

		w12 := l.Wake(t0.Add(12 * time.Second))
		found := false
		for _, em := range w12.Emissions {
			if em.Port == "1/1/1" {
				b, err := stp.Decode(em.Frame)
				if err != nil {
					t.Fatalf("decode 1/1/1 hello: %v", err)
				}
				if b.Type != stp.BPDUTypeRapid {
					t.Errorf("1/1/1 hello type = %v, want Rapid", b.Type)
				}
				found = true
			}
		}
		if !found {
			t.Error("no hello emitted on 1/1/1 at 12s")
		}
	})
}

// TestSuperiorBPDUInsideMigrationDelay is evidence that a legacy BPDU received
// inside the migration delay does not trigger protocol migration, but updates
// the priority vector so the port elects the root role.
func TestSuperiorBPDUInsideMigrationDelay(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "02:00:00:00:00:02"),
		Ports: map[string]stp.Port{
			"1/1/1": {},
			"1/1/2": {},
		},
	}, mustPortTable(t, "1/1/1", "1/1/2"))

	l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)
	l.LinkChange(t0, "1/1/2", true, true, 1_000_000_000)

	superiorConfig := stp.BPDU{
		Version:      0,
		Type:         stp.BPDUTypeConfiguration,
		RootID:       stp.BridgeID{Priority: 4096, Address: mustMAC(t, "02:00:00:00:00:0a")},
		BridgeID:     stp.BridgeID{Priority: 4096, Address: mustMAC(t, "02:00:00:00:00:0a")},
		RootPathCost: 0,
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	superiorConfig.SetRole(stp.RoleDesignated)

	l.Receive(t0.Add(time.Second), "1/1/2", superiorConfig)
	info := l.PortInfo("1/1/2")
	if !info.SendRSTP {
		t.Errorf("1/1/2 SendRSTP = false, want true inside migration delay")
	}
	if info.Role != stp.RoleRoot {
		t.Errorf("1/1/2 role = %v, want Root", info.Role)
	}
}

// TestAutoEdgeDetection is evidence that a port with AutoEdge transitions to
// edge and forward on timer expiry without BPDUs, loses edge status and resets
// to discarding on hearing a BPDU, and regains edge status when quiet.
func TestAutoEdgeDetection(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "02:00:00:00:00:02"),
		Ports: map[string]stp.Port{
			"1/1/1": {AutoEdge: false},
			"1/1/2": {AutoEdge: true},
		},
	}, mustPortTable(t, "1/1/1", "1/1/2"))

	l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)
	l.LinkChange(t0, "1/1/2", true, true, 1_000_000_000)

	l.Wake(t0.Add(2 * time.Second))
	if info := l.PortInfo("1/1/2"); info.Edge || info.State == stp.StateForwarding {
		t.Fatalf("at 2s: 1/1/2 edge %v state %v, want not edge and discarding", info.Edge, info.State)
	}

	l.Wake(t0.Add(3 * time.Second))
	if info := l.PortInfo("1/1/2"); !info.Edge || info.State != stp.StateForwarding {
		t.Fatalf("at 3s: 1/1/2 edge %v state %v, want edge and forwarding", info.Edge, info.State)
	}
	if info := l.PortInfo("1/1/1"); info.Edge || info.State != stp.StateDiscarding {
		t.Fatalf("at 3s: 1/1/1 edge %v state %v, want non-edge and discarding", info.Edge, info.State)
	}

	inferiorRapid := stp.BPDU{
		Version:      2,
		Type:         stp.BPDUTypeRapid,
		RootID:       stp.BridgeID{Priority: 61440, Address: mustMAC(t, "02:00:00:00:00:0c")},
		BridgeID:     stp.BridgeID{Priority: 61440, Address: mustMAC(t, "02:00:00:00:00:0c")},
		RootPathCost: 0,
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	inferiorRapid.SetRole(stp.RoleDesignated)

	l.Receive(t0.Add(5*time.Second), "1/1/2", inferiorRapid)
	if info := l.PortInfo("1/1/2"); info.Edge || info.State != stp.StateDiscarding {
		t.Fatalf("at 5s after BPDU: 1/1/2 edge %v state %v, want non-edge and discarding", info.Edge, info.State)
	}

	l.Wake(t0.Add(8 * time.Second))
	if info := l.PortInfo("1/1/2"); !info.Edge || info.State != stp.StateForwarding {
		t.Fatalf("at 8s without BPDU: 1/1/2 edge %v state %v, want edge and forwarding", info.Edge, info.State)
	}
}

// TestTransmitHoldCountGating is evidence that emissions are held when the
// transmit hold count is reached, released at the next tick, and accounted for
// on PortInfo.TxBPDUs.
func TestTransmitHoldCountGating(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority:    32768,
		Address:     mustMAC(t, "02:00:00:00:00:02"),
		TxHoldCount: 2,
		Ports: map[string]stp.Port{
			"1/1/1": {},
			"1/1/2": {},
		},
	}, mustPortTable(t, "1/1/1", "1/1/2"))

	l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)
	l.LinkChange(t0, "1/1/2", true, true, 1_000_000_000)

	l.Wake(t0.Add(2 * time.Second))
	// The hello due at 4 s is the first of the two transmissions the bound
	// allows within the second that follows.
	l.Wake(t0.Add(4 * time.Second))

	inferiorConfig := stp.BPDU{
		Version:      0,
		Type:         stp.BPDUTypeConfiguration,
		RootID:       stp.BridgeID{Priority: 61440, Address: mustMAC(t, "02:00:00:00:00:0c")},
		BridgeID:     stp.BridgeID{Priority: 61440, Address: mustMAC(t, "02:00:00:00:00:0c")},
		RootPathCost: 0,
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	inferiorConfig.SetRole(stp.RoleDesignated)

	rx1 := l.Receive(t0.Add(4100*time.Millisecond), "1/1/1", inferiorConfig)
	rx2 := l.Receive(t0.Add(4200*time.Millisecond), "1/1/1", inferiorConfig)
	rx3 := l.Receive(t0.Add(4400*time.Millisecond), "1/1/1", inferiorConfig)

	totalReplies := len(rx1.Emissions) + len(rx2.Emissions) + len(rx3.Emissions)
	if totalReplies != 1 {
		t.Fatalf("total reply emissions in burst = %d, want 1: the hello at 4 s took the other slot", totalReplies)
	}

	next, hasTimer := l.NextWake()
	if !hasTimer || !next.Equal(t0.Add(5*time.Second)) {
		t.Fatalf("NextWake = (%v, %v), want (%v, true)", next, hasTimer, t0.Add(5*time.Second))
	}

	w5 := l.Wake(t0.Add(5 * time.Second))
	if len(w5.Emissions) != 1 || w5.Emissions[0].Port != "1/1/1" {
		t.Fatalf("Wake(5s) emissions = %+v, want 1 on 1/1/1", w5.Emissions)
	}

	// The link-up proposal, the hellos at 2 s and 4 s, the one reply that
	// passed the gate, and the one released at the tick.
	if txCount := l.PortInfo("1/1/1").TxBPDUs; txCount != 5 {
		t.Errorf("PortInfo(1/1/1).TxBPDUs = %d, want 5", txCount)
	}
}

func legacyConfigBPDU(t *testing.T, rootPriority uint16, addr string) stp.BPDU {
	t.Helper()
	b := stp.BPDU{
		Type:         stp.BPDUTypeConfiguration,
		RootID:       stp.BridgeID{Priority: rootPriority, Address: mustMAC(t, addr)},
		BridgeID:     stp.BridgeID{Priority: rootPriority, Address: mustMAC(t, addr)},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	b.SetRole(stp.RoleDesignated)

	return b
}

// TestMigratedRootPortClimbsTheLadder is evidence that a port in
// compatibility mode takes no rapid transition even as a synced root port.
func TestMigratedRootPortClimbsTheLadder(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "02:00:00:00:00:02"),
		Ports:    map[string]stp.Port{"1/1/1": {}},
	}, mustPortTable(t, "1/1/1"))
	l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)

	superior := legacyConfigBPDU(t, 4096, "02:00:00:00:00:0a")
	at := t0.Add(4 * time.Second)
	l.Receive(at, "1/1/1", superior)
	if info := l.PortInfo("1/1/1"); info.Role != stp.RoleRoot || info.SendRSTP || info.State == stp.StateForwarding {
		t.Fatalf("after a superior legacy BPDU: %+v, want a migrated root port not yet forwarding", info)
	}
	// Wake every second as the fabric would, and keep the information fresh
	// with a hello every two seconds; the ladder started at link-up, so it
	// fires at t0+15 s and t0+30 s whatever the role.
	for s := 5; s <= 30; s++ {
		now := t0.Add(time.Duration(s) * time.Second)
		if s%2 == 0 {
			l.Receive(now, "1/1/1", superior)
		}
		l.Wake(now)
		if info := l.PortInfo("1/1/1"); s < 30 && info.State == stp.StateForwarding {
			t.Fatalf("at t0+%ds the migrated root port is forwarding; want the ladder to hold it", s)
		}
	}
	if info := l.PortInfo("1/1/1"); info.State != stp.StateForwarding {
		t.Fatalf("at t0+30s the migrated root port is %v, want forwarding after two forward delays", info.State)
	}
}

// TestMigratedPortAgreesWithoutTheAgreementBit is evidence that a port in
// compatibility mode answers a proposal with a Configuration BPDU that
// carries no agreement flag.
func TestMigratedPortAgreesWithoutTheAgreementBit(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "02:00:00:00:00:02"),
		Ports:    map[string]stp.Port{"1/1/1": {}},
	}, mustPortTable(t, "1/1/1"))
	l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)
	l.Receive(t0.Add(4*time.Second), "1/1/1", legacyConfigBPDU(t, 61440, "02:00:00:00:00:0c"))
	if l.PortInfo("1/1/1").SendRSTP {
		t.Fatal("port did not migrate")
	}

	proposal := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: mustMAC(t, "02:00:00:00:00:0a")},
		BridgeID:     stp.BridgeID{Priority: 4096, Address: mustMAC(t, "02:00:00:00:00:0a")},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	proposal.SetRole(stp.RoleDesignated)
	proposal.SetProposal(true)
	// Inside the restarted delay the RST BPDU does not end compatibility.
	rx := l.Receive(t0.Add(5*time.Second), "1/1/1", proposal)
	if len(rx.Emissions) != 1 {
		t.Fatalf("emissions = %d, want the reply", len(rx.Emissions))
	}
	reply, err := stp.Decode(rx.Emissions[0].Frame)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Type != stp.BPDUTypeConfiguration || reply.Agreement() {
		t.Errorf("reply = type %v agreement %v, want a Configuration BPDU without agreement", reply.Type, reply.Agreement())
	}
}

// TestTopologyChangeFlushKeepsBridgeGlobalPortOrder guards the seam that keying
// the layer's state by tree could have broken. Effects.Flush reaches the caller
// in the order the emit loops walk the ports, so that order belongs to the
// bridge; a per-tree port map iterated on its own would reorder the list with no
// behavior change to point at.
func TestTopologyChangeFlushKeepsBridgeGlobalPortOrder(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	names := []string{"1/1/1", "1/1/2", "1/1/3", "1/1/4", "1/1/5"}
	ports := map[string]stp.Port{}
	for _, name := range names {
		ports[name] = stp.Port{}
	}
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "02:00:00:00:00:02"),
		Ports:    ports,
	}, mustPortTable(t, names...))
	for _, name := range names {
		l.LinkChange(t0, name, true, true, 1_000_000_000)
	}

	// A TCN on the first port flushes every other port, which is the widest
	// flush list one call produces.
	fx := l.Receive(t0.Add(4*time.Second), names[0], stp.BPDU{Type: stp.BPDUTypeTopologyChangeNotification})

	want := names[1:]
	if !slices.Equal(flushPorts(fx.Flush), want) {
		t.Errorf("Flush = %v, want %v in sorted bridge-global port order", fx.Flush, want)
	}
}

// TestTCNReceiveRaisesTopologyChange is evidence that a legacy Topology
// Change Notification flushes the other ports and migrates the port.
func TestTCNReceiveRaisesTopologyChange(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "02:00:00:00:00:02"),
		Ports:    map[string]stp.Port{"1/1/1": {}, "1/1/2": {}},
	}, mustPortTable(t, "1/1/1", "1/1/2"))
	l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)
	l.LinkChange(t0, "1/1/2", true, true, 1_000_000_000)

	fx := l.Receive(t0.Add(4*time.Second), "1/1/1", stp.BPDU{Type: stp.BPDUTypeTopologyChangeNotification})
	got := flushPorts(fx.Flush)
	if !slices.Contains(got, "1/1/2") || slices.Contains(got, "1/1/1") {
		t.Errorf("Flush = %v, want the other port and not the receiving one", fx.Flush)
	}
	if l.PortInfo("1/1/1").SendRSTP {
		t.Error("a TCN after the migration delay left the port in RSTP mode")
	}
	if l.PortInfo("1/1/1").RxBPDUs != 1 {
		t.Errorf("RxBPDUs = %d, want 1", l.PortInfo("1/1/1").RxBPDUs)
	}
}

// TestAutoEdgeTimerRestartsWhenAPortBecomesDesignatedAgain is evidence that
// NextWake never reports an edge delay that has already passed: a port that
// was blocked while the delay ran gets a fresh delay when it is designated
// again.
func TestAutoEdgeTimerRestartsWhenAPortBecomesDesignatedAgain(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "02:00:00:00:00:02"),
		Ports:    map[string]stp.Port{"1/1/1": {AutoEdge: true}},
	}, mustPortTable(t, "1/1/1"))
	l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)

	superior := legacyConfigBPDU(t, 4096, "02:00:00:00:00:0a")
	superior.Type = stp.BPDUTypeRapid
	l.Receive(t0.Add(1*time.Second), "1/1/1", superior)
	if l.PortInfo("1/1/1").Role != stp.RoleRoot {
		t.Fatal("port did not take the root role")
	}
	// Three hellos without a BPDU age the information out and the port is
	// designated again; its edge delay must count from then.
	now := t0.Add(8 * time.Second)
	l.Wake(now)
	if l.PortInfo("1/1/1").Role != stp.RoleDesignated {
		t.Fatalf("role after aging = %v, want designated", l.PortInfo("1/1/1").Role)
	}
	next, ok := l.NextWake()
	if !ok || next.Before(now) {
		t.Fatalf("NextWake = (%v, %v), want a time not before %v", next, ok, now)
	}
}

// TestInternalBPDUDiscardedAtOneRemainingHopStoredAtTwo is evidence that
// internal information ages by remaining hops rather than message age: a
// record naming one hop left is one hop too few to accept, and one naming two
// is stored and elects the root it carries.
func TestInternalBPDUDiscardedAtOneRemainingHopStoredAtTwo(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	localMAC := mustMAC(t, "00:11:22:33:44:02")
	peerMAC := mustMAC(t, "00:11:22:33:44:01")

	region := stp.MST{Name: "region-1", Revision: 1}
	cid := region.ConfigID()

	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  localMAC,
		Ports:    map[string]stp.Port{"1/1/1": {}},
		MST:      &region,
	}, mustPortTable(t, "1/1/1"))
	l.LinkChange(now, "1/1/1", true, true, 1_000_000_000)

	internalBPDU := func(remainingHops uint8) stp.BPDU {
		return stp.BPDU{
			RootID:               stp.BridgeID{Priority: 4096, Address: peerMAC},
			BridgeID:             stp.BridgeID{Priority: 4096, Address: peerMAC},
			PortID:               0x8001,
			HelloTime:            2 * time.Second,
			MaxAge:               20 * time.Second,
			ForwardDelay:         15 * time.Second,
			ConfigID:             &cid,
			RegionalRootID:       stp.BridgeID{Priority: 4096, Address: peerMAC},
			InternalRootPathCost: 0,
			RemainingHops:        remainingHops,
		}
	}

	l.Receive(now, "1/1/1", internalBPDU(1))
	if rootID, _, rootPort := l.Root(); rootPort != "" || rootID != l.BridgeID() {
		t.Fatalf("Root after RemainingHops=1 = (%v, %q), want this bridge's own with no root port", rootID, rootPort)
	}
	if info := l.PortInfo("1/1/1"); info.Role != stp.RoleDesignated {
		t.Errorf("role after RemainingHops=1 = %v, want Designated (discarded)", info.Role)
	}

	l.Receive(now, "1/1/1", internalBPDU(2))
	rootID, _, rootPort := l.Root()
	if rootPort != "1/1/1" || rootID.Priority != 4096 {
		t.Fatalf("Root after RemainingHops=2 = (%v, %q), want (4096/.., \"1/1/1\")", rootID, rootPort)
	}
	if info := l.PortInfo("1/1/1"); info.Role != stp.RoleRoot {
		t.Errorf("role after RemainingHops=2 = %v, want Root (stored)", info.Role)
	}
}

// TestForeignRegionRevisionMarksPortExternalAndMSTIFollowsCIST is evidence
// that an MST BPDU whose configuration identifier names a different region
// (here, a differing revision) is classified external rather than internal,
// and that the boundary port's MSTI role follows the CIST's role verbatim
// rather than computing one of its own.
func TestForeignRegionRevisionMarksPortExternalAndMSTIFollowsCIST(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	localMAC := mustMAC(t, "00:11:22:33:44:02")
	peerMAC := mustMAC(t, "00:11:22:33:44:01")

	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  localMAC,
		Ports:    map[string]stp.Port{"1/1/1": {}},
		MST: &stp.MST{
			Name:      "region-1",
			Revision:  1,
			Instances: map[stp.MSTID]stp.Instance{1: {VLANs: []vlan.ID{10}}},
		},
	}, mustPortTable(t, "1/1/1"))
	l.LinkChange(now, "1/1/1", true, true, 1_000_000_000)

	// The peer names the same region by name but a different revision, so it
	// belongs to a different region even though both sides run MSTP.
	foreignRegion := stp.MST{Name: "region-1", Revision: 2}
	foreignConfigID := foreignRegion.ConfigID()
	b := stp.BPDU{
		RootID:         stp.BridgeID{Priority: 4096, Address: peerMAC},
		BridgeID:       stp.BridgeID{Priority: 4096, Address: peerMAC},
		PortID:         0x8001,
		HelloTime:      2 * time.Second,
		MaxAge:         20 * time.Second,
		ForwardDelay:   15 * time.Second,
		ConfigID:       &foreignConfigID,
		RegionalRootID: stp.BridgeID{Priority: 4096, Address: peerMAC},
		RemainingHops:  20,
	}
	l.Receive(now, "1/1/1", b)

	cistInfo := l.PortInfo("1/1/1")
	if cistInfo.Role != stp.RoleRoot {
		t.Fatalf("CIST role = %v, want Root (a foreign-region BPDU is still evaluated on the CIST)", cistInfo.Role)
	}

	mstiInfo := l.InstancePortInfo(1, "1/1/1")
	if mstiInfo.Role != cistInfo.Role || mstiInfo.State != cistInfo.State {
		t.Errorf("MSTI 1 (role, state) = (%v, %v), want the CIST's boundary values (%v, %v)",
			mstiInfo.Role, mstiInfo.State, cistInfo.Role, cistInfo.State)
	}
}

// TestCISTTopologyChangeOnBoundaryPortFlushesEveryInstance is evidence for
// this unit's rule that a topology change raised from the CIST always flushes
// every FID rather than a derived list, which is what makes a CIST change on
// a boundary port reach both instances: the boundary port carries traffic no
// single MSTI claims, so nothing narrower than "every FID" would be correct
// there either.
func TestCISTTopologyChangeOnBoundaryPortFlushesEveryInstance(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	localMAC := mustMAC(t, "00:11:22:33:44:02")
	peerMAC := mustMAC(t, "00:11:22:33:44:01")

	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  localMAC,
		Ports:    map[string]stp.Port{"1/1/1": {}, "1/1/2": {}},
		MST: &stp.MST{
			Name:     "region-1",
			Revision: 1,
			Instances: map[stp.MSTID]stp.Instance{
				1: {VLANs: []vlan.ID{10}},
				2: {VLANs: []vlan.ID{20}},
			},
		},
	}, mustPortTable(t, "1/1/1", "1/1/2"))
	l.LinkChange(now, "1/1/1", true, true, 1_000_000_000)
	l.LinkChange(now, "1/1/2", true, true, 1_000_000_000)

	// A foreign-region BPDU marks 1/1/1 a boundary port and, being superior,
	// elects it Root; point-to-point and synced, it forwards in this same
	// call, raising the topology change under test.
	foreignRegion := stp.MST{Name: "region-1", Revision: 2}
	foreignConfigID := foreignRegion.ConfigID()
	b := stp.BPDU{
		RootID:         stp.BridgeID{Priority: 4096, Address: peerMAC},
		BridgeID:       stp.BridgeID{Priority: 4096, Address: peerMAC},
		PortID:         0x8001,
		HelloTime:      2 * time.Second,
		MaxAge:         20 * time.Second,
		ForwardDelay:   15 * time.Second,
		ConfigID:       &foreignConfigID,
		RegionalRootID: stp.BridgeID{Priority: 4096, Address: peerMAC},
		RemainingHops:  20,
	}
	fx := l.Receive(now, "1/1/1", b)

	if info := l.PortInfo("1/1/1"); info.Role != stp.RoleRoot || info.State != stp.StateForwarding {
		t.Fatalf("boundary port after the superior BPDU: role %v, state %v; want Root Forwarding", info.Role, info.State)
	}

	target, ok := flushTarget(fx.Flush, "1/1/2")
	if !ok {
		t.Fatalf("Flush = %v, want a target for \"1/1/2\"", fx.Flush)
	}
	if len(target.FIDs) != 0 {
		t.Errorf("boundary-port topology change flush target FIDs = %v, want none: it reaches every instance, VLAN 10 and VLAN 20 alike", target.FIDs)
	}
}

// cableLink names a link between two switches in a convergence test.
type cableLink struct {
	swA, swB     int
	portA, portB string
}

// convergeLayers drives LinkChange on every cabled port, then alternates
// draining the BPDU queue and advancing every layer's earliest NextWake until
// two consecutive rounds report the same snapshot with an empty queue. snapshot
// renders whatever a test needs to see stabilize; the scheduler does not look
// inside it.
func convergeLayers(t *testing.T, start time.Time, layers []*stp.Layer, cables []cableLink, snapshot func() string) time.Time {
	t.Helper()

	now := start

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
	linkUp := func(sw int, port string) {
		fx := layers[sw].LinkChange(now, port, true, true, 1_000_000_000)
		for _, em := range fx.Emissions {
			if peerSw, peerPort, ok := findPeer(sw, em.Port); ok {
				queue = append(queue, packet{targetSw: peerSw, targetPort: peerPort, frame: em.Frame})
			}
		}
	}
	for _, c := range cables {
		linkUp(c.swA, c.portA)
		linkUp(c.swB, c.portB)
	}

	var prevSnapshot string
	stableRounds := 0

	for round := 0; round < 400; round++ {
		current := snapshot()
		if current == prevSnapshot {
			stableRounds++
			if stableRounds >= 2 && len(queue) == 0 {
				break
			}
		} else {
			stableRounds = 0
			prevSnapshot = current
		}

		if len(queue) > 0 {
			batch := queue
			queue = nil
			for _, pkt := range batch {
				bpdu, err := stp.Decode(pkt.frame)
				if err != nil {
					t.Fatalf("decode packet for sw%d %s: %v", pkt.targetSw, pkt.targetPort, err)
				}
				fx := layers[pkt.targetSw].Receive(now, pkt.targetPort, bpdu)
				for _, em := range fx.Emissions {
					if peerSw, peerPort, ok := findPeer(pkt.targetSw, em.Port); ok {
						queue = append(queue, packet{targetSw: peerSw, targetPort: peerPort, frame: em.Frame})
					}
				}
			}

			continue
		}

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
				if peerSw, peerPort, ok := findPeer(i, em.Port); ok {
					queue = append(queue, packet{targetSw: peerSw, targetPort: peerPort, frame: em.Frame})
				}
			}
		}
	}

	return now
}

// TestMSTInstancesSelectIndependentRoots is evidence that each MST instance
// runs its own root election: with the non-root bridge's path cost inflated
// on L1 for MSTI 1 and on L2 for MSTI 2, MSTI 1 roots across L2 and MSTI 2
// roots across L1, the opposite of each other and of the CIST (which sees no
// per-instance cost override and roots across whichever link converges
// first).
func TestMSTInstancesSelectIndependentRoots(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	mac1 := mustMAC(t, "00:11:22:33:44:01")
	mac2 := mustMAC(t, "00:11:22:33:44:02")

	region := func(instances map[stp.MSTID]stp.Instance) *stp.MST {
		return &stp.MST{Name: "region-1", Revision: 1, Instances: instances}
	}

	sw1 := mustNewSTP(t, stp.Config{
		Priority: 4096,
		Address:  mac1,
		Ports:    map[string]stp.Port{"l1": {}, "l2": {}},
		MST: region(map[stp.MSTID]stp.Instance{
			1: {VLANs: []vlan.ID{10}},
			2: {VLANs: []vlan.ID{20}},
		}),
	}, mustPortTable(t, "l1", "l2"))

	sw2 := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mac2,
		Ports:    map[string]stp.Port{"l1": {}, "l2": {}},
		MST: region(map[stp.MSTID]stp.Instance{
			1: {VLANs: []vlan.ID{10}, Ports: map[string]stp.InstancePort{"l1": {PathCost: 200_000}}},
			2: {VLANs: []vlan.ID{20}, Ports: map[string]stp.InstancePort{"l2": {PathCost: 200_000}}},
		}),
	}, mustPortTable(t, "l1", "l2"))

	layers := []*stp.Layer{sw1, sw2}
	cables := []cableLink{
		{swA: 0, portA: "l1", swB: 1, portB: "l1"},
		{swA: 0, portA: "l2", swB: 1, portB: "l2"},
	}

	snapshot := func() string {
		var b strings.Builder
		for _, l := range layers {
			for _, port := range []string{"l1", "l2"} {
				fmt.Fprintf(&b, "%v/%v/%v/%v;",
					l.PortInfo(port).Role, l.InstancePortInfo(1, port).Role, l.InstancePortInfo(2, port).Role,
					l.PortInfo(port).State)
			}
		}
		return b.String()
	}

	convergeLayers(t, start, layers, cables, snapshot)

	msti1 := sw2.InstancePortInfo(1, "l2")
	if msti1.Role != stp.RoleRoot {
		t.Errorf("sw2 MSTI 1 on l2 = %v, want Root (l1 costs 200000 for MSTI 1)", msti1.Role)
	}
	if got := sw2.InstancePortInfo(1, "l1").Role; got != stp.RoleAlternate && got != stp.RoleDesignated {
		t.Errorf("sw2 MSTI 1 on l1 = %v, want Alternate or Designated, not Root", got)
	}

	msti2 := sw2.InstancePortInfo(2, "l1")
	if msti2.Role != stp.RoleRoot {
		t.Errorf("sw2 MSTI 2 on l1 = %v, want Root (l2 costs 200000 for MSTI 2)", msti2.Role)
	}
	if got := sw2.InstancePortInfo(2, "l2").Role; got != stp.RoleAlternate && got != stp.RoleDesignated {
		t.Errorf("sw2 MSTI 2 on l2 = %v, want Alternate or Designated, not Root", got)
	}
}

// TestMSTBridgeMigratedPortEmitsPlainConfigurationBPDU is evidence that a
// port an MST bridge has migrated to legacy STP emits a Configuration BPDU a
// legacy peer can read, not a version 3 MST BPDU: Encode picks the MST shape
// whenever ConfigID is set regardless of Version and Type, so attaching the
// region's configuration identifier on a migrated port would silently
// mislabel the frame it just downgraded.
func TestMSTBridgeMigratedPortEmitsPlainConfigurationBPDU(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "00:11:22:33:44:02"),
		Ports:    map[string]stp.Port{"1/1/1": {}},
		MST:      &stp.MST{Name: "region-1", Revision: 1},
	}, mustPortTable(t, "1/1/1"))

	l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)

	legacyPeer := stp.BPDU{
		Version:      0,
		Type:         stp.BPDUTypeConfiguration,
		RootID:       stp.BridgeID{Priority: 61440, Address: mustMAC(t, "00:11:22:33:44:0c")},
		BridgeID:     stp.BridgeID{Priority: 61440, Address: mustMAC(t, "00:11:22:33:44:0c")},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	legacyPeer.SetRole(stp.RoleDesignated)

	// Past the migration delay, receiving a legacy BPDU migrates the port.
	l.Receive(t0.Add(4*time.Second), "1/1/1", legacyPeer)
	if l.PortInfo("1/1/1").SendRSTP {
		t.Fatal("port did not migrate to legacy STP")
	}

	w := l.Wake(t0.Add(6 * time.Second))
	var emitted *stp.BPDU
	for _, em := range w.Emissions {
		if em.Port != "1/1/1" {
			continue
		}
		b, err := stp.Decode(em.Frame)
		if err != nil {
			t.Fatalf("decode emission: %v", err)
		}
		emitted = &b
	}
	if emitted == nil {
		t.Fatal("no hello emitted on the migrated port")
	}
	if emitted.Version != 0 || emitted.Type != stp.BPDUTypeConfiguration {
		t.Errorf("emitted version = %d type = %v, want 0 and Configuration", emitted.Version, emitted.Type)
	}
	if emitted.ConfigID != nil {
		t.Errorf("emitted ConfigID = %+v, want nil on a migrated port", emitted.ConfigID)
	}
}

// TestMSTIRecordFlagsCarryThePerInstancePortRole is evidence that an MSTI
// record's Flags octet reports the sending bridge's own role, learning, and
// forwarding bits for that instance's port, using the same bit layout the
// CIST's BPDU flags use (BPDU.Role, BPDU.Learning, BPDU.Forwarding decode
// both alike), rather than being left an unused zero.
func TestMSTIRecordFlagsCarryThePerInstancePortRole(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 4096,
		Address:  mustMAC(t, "00:11:22:33:44:01"),
		Ports:    map[string]stp.Port{"1/1/1": {}},
		MST: &stp.MST{
			Name:     "region-1",
			Revision: 1,
			Instances: map[stp.MSTID]stp.Instance{
				1: {VLANs: []vlan.ID{10}},
			},
		},
	}, mustPortTable(t, "1/1/1"))

	fx := l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)
	if len(fx.Emissions) != 1 {
		t.Fatalf("emissions count = %d, want 1", len(fx.Emissions))
	}

	b, err := stp.Decode(fx.Emissions[0].Frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(b.MSTIs) != 1 {
		t.Fatalf("MSTIs count = %d, want 1", len(b.MSTIs))
	}

	// The port has just come up on a bridge that is its own root for the
	// instance: Designated for the CIST and for MSTI 1 alike, but not yet
	// Learning or Forwarding since no agreement has run.
	rec := b.MSTIs[0]
	decoded := stp.BPDU{Flags: rec.Flags}
	if decoded.Role() != stp.RoleDesignated {
		t.Errorf("MSTI record role = %v, want Designated", decoded.Role())
	}
	if decoded.Learning() || decoded.Forwarding() {
		t.Errorf("MSTI record learning=%t forwarding=%t, want both false before any agreement", decoded.Learning(), decoded.Forwarding())
	}

	instanceInfo := l.InstancePortInfo(1, "1/1/1")
	if decoded.Role() != instanceInfo.Role {
		t.Errorf("MSTI record role = %v, want it to match InstancePortInfo's %v", decoded.Role(), instanceInfo.Role)
	}
}
