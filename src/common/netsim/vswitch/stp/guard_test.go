package stp_test

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

// superiorBPDU builds an RST BPDU from a bridge better than any this file
// configures, so receiving it always makes the port's information superior.
func superiorBPDU(messageAge, maxAge time.Duration) stp.BPDU {
	b := stp.BPDU{
		Version:      2,
		Type:         stp.BPDUTypeRapid,
		RootID:       stp.BridgeID{Priority: 4096},
		RootPathCost: 10,
		BridgeID:     stp.BridgeID{Priority: 4096},
		PortID:       0x8001,
		MessageAge:   messageAge,
		MaxAge:       maxAge,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	b.SetRole(stp.RoleDesignated)

	return b
}

func guardLayer(t *testing.T, ports map[string]stp.Port) (*stp.Layer, time.Time) {
	t.Helper()

	names := make([]string, 0, len(ports))
	for name := range ports {
		names = append(names, name)
	}
	slices.Sort(names)

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "02:00:00:00:00:10"),
		Ports:    ports,
	}, mustPortTable(t, names...))
	for _, name := range names {
		l.LinkChange(t0, name, true, true, 1_000_000_000)
	}

	return l, t0
}

// TestMessageAgeAtMaxAgeIsDiscarded is evidence for R15a. The counterfactual
// belongs here rather than to a fabric run: makeBPDU always increments the age,
// so a fabric has no hook to hold it at a chosen value.
func TestMessageAgeAtMaxAgeIsDiscarded(t *testing.T) {
	t.Parallel()

	l, t0 := guardLayer(t, map[string]stp.Port{"1/1/1": {}, "1/1/2": {}})

	// One second below the bound is still accepted, and the root it names wins.
	l.Receive(t0.Add(time.Second), "1/1/1", superiorBPDU(19*time.Second, 20*time.Second))
	root, _, rootPort := l.Root()
	if root.Priority != 4096 || rootPort != "1/1/1" {
		t.Fatalf("root = %v via %q, want the received root 4096 via 1/1/1", root, rootPort)
	}

	// At the bound the information is discarded, so the port keeps what it has
	// and the stored information expires on its own timer rather than being
	// refreshed by a BPDU that has run out of hops.
	l.Receive(t0.Add(2*time.Second), "1/1/1", superiorBPDU(20*time.Second, 20*time.Second))
	root, _, rootPort = l.Root()
	if root.Priority != 4096 || rootPort != "1/1/1" {
		t.Errorf("root = %v via %q, want the stored information kept", root, rootPort)
	}

	// Three hello times after the last accepted BPDU, and not after the
	// discarded one, the information ages out and this bridge is root again.
	l.Wake(t0.Add(time.Second).Add(6 * time.Second))
	root, _, rootPort = l.Root()
	if root.Priority != 32768 || rootPort != "" {
		t.Errorf("root = %v via %q, want this bridge once the stale information aged out", root, rootPort)
	}
}

// TestMessageAgeBoundUsesTheReceivedMaxAge is evidence that the bound comes from
// the BPDU rather than from this bridge's configuration, which matters as soon
// as two bridges are configured with different timers.
func TestMessageAgeBoundUsesTheReceivedMaxAge(t *testing.T) {
	t.Parallel()

	l, t0 := guardLayer(t, map[string]stp.Port{"1/1/1": {}, "1/1/2": {}})

	// This bridge's own max age is the 20s default. A BPDU carrying a smaller
	// one is bounded by its own value, so an age this bridge would accept
	// against its configuration is discarded against the BPDU's.
	l.Receive(t0.Add(time.Second), "1/1/1", superiorBPDU(10*time.Second, 10*time.Second))
	if root, _, _ := l.Root(); root.Priority != 32768 {
		t.Errorf("root = %v, want this bridge: message age 10s reaches the BPDU's own 10s bound", root)
	}

	// The same age against a larger received max age is accepted, which is what
	// makes the rejection above about the BPDU's bound and not about the age.
	l.Receive(t0.Add(2*time.Second), "1/1/2", superiorBPDU(10*time.Second, 20*time.Second))
	if root, _, _ := l.Root(); root.Priority != 4096 {
		t.Errorf("root = %v, want the received root: message age 10s is inside a 20s bound", root)
	}
}

// TestGateAnswersAlikeForEveryVLAN is evidence for Rgate. One tree answers every
// VLAN in this phase, so the two VLANs agree; the test exists to pin that the
// parameter is carried and resolved rather than ignored, and it is the test that
// starts failing when phase 3d gives the VLANs different trees.
func TestGateAnswersAlikeForEveryVLAN(t *testing.T) {
	t.Parallel()

	l, t0 := guardLayer(t, map[string]stp.Port{"1/1/1": {}, "1/1/2": {}})

	// A blocked port and a forwarding one, so the assertion is not vacuous on a
	// bridge whose every port answers the same way.
	l.Receive(t0.Add(time.Second), "1/1/1", superiorBPDU(0, 20*time.Second))
	l.Receive(t0.Add(time.Second), "1/1/2", superiorBPDU(0, 20*time.Second))

	for _, name := range []string{"1/1/1", "1/1/2"} {
		if l.Forwards(name, 10) != l.Forwards(name, 20) {
			t.Errorf("port %q forwards VLAN 10 as %v and VLAN 20 as %v, want one tree to answer both",
				name, l.Forwards(name, 10), l.Forwards(name, 20))
		}
		if l.Learns(name, 10) != l.Learns(name, 20) {
			t.Errorf("port %q learns VLAN 10 as %v and VLAN 20 as %v, want one tree to answer both",
				name, l.Learns(name, 10), l.Learns(name, 20))
		}
	}

	// An untracked port answers alike too, and permissively.
	if !l.Forwards("1/1/99", 10) || !l.Learns("1/1/99", 4094) {
		t.Error("an untracked port did not answer permissively for every VLAN")
	}
}

// TestBPDUGuardDisablesPortUntilLinkBounce is evidence for R15b and R15e.
func TestBPDUGuardDisablesPortUntilLinkBounce(t *testing.T) {
	t.Parallel()

	l, t0 := guardLayer(t, map[string]stp.Port{
		"1/1/1": {AdminEdge: true, BPDUGuard: true},
		"1/1/2": {},
	})

	if info := l.PortInfo("1/1/1"); info.State != stp.StateForwarding {
		t.Fatalf("edge port state = %v, want Forwarding before any BPDU", info.State)
	}

	l.Receive(t0.Add(time.Second), "1/1/1", superiorBPDU(0, 20*time.Second))

	info := l.PortInfo("1/1/1")
	if info.Role != stp.RoleDisabled || info.State != stp.StateDiscarding {
		t.Errorf("guarded port = %v/%v, want Disabled/Discarding", info.Role, info.State)
	}
	if info.BlockReason != stp.BlockReasonBPDUGuard {
		t.Errorf("block reason = %q, want %q", info.BlockReason, stp.BlockReasonBPDUGuard)
	}
	if l.Forwards("1/1/1", 0) || l.Learns("1/1/1", 0) {
		t.Error("guarded port still forwards or learns")
	}
	// The unexpected bridge must not have become this bridge's root.
	if root, _, _ := l.Root(); root.Priority != 32768 {
		t.Errorf("root = %v, want this bridge: a guarded port contributes nothing", root)
	}

	// A wake with no further BPDU does not recover the port.
	l.Wake(t0.Add(30 * time.Second))
	if l.PortInfo("1/1/1").BlockReason != stp.BlockReasonBPDUGuard {
		t.Error("a wake recovered the guarded port; only a link bounce may")
	}

	// Neither does another BPDU.
	l.Receive(t0.Add(31*time.Second), "1/1/1", superiorBPDU(0, 20*time.Second))
	if l.PortInfo("1/1/1").BlockReason != stp.BlockReasonBPDUGuard {
		t.Error("a further BPDU recovered the guarded port")
	}

	// A link down and up does.
	l.LinkChange(t0.Add(40*time.Second), "1/1/1", false, true, 1_000_000_000)
	l.LinkChange(t0.Add(41*time.Second), "1/1/1", true, true, 1_000_000_000)
	info = l.PortInfo("1/1/1")
	if info.BlockReason != "" {
		t.Errorf("block reason after a link bounce = %q, want none", info.BlockReason)
	}
	if info.State != stp.StateForwarding {
		t.Errorf("edge port state after a link bounce = %v, want Forwarding", info.State)
	}
}

// TestRestrictedRoleKeepsPortOutOfRootSelection is evidence for R15b.
func TestRestrictedRoleKeepsPortOutOfRootSelection(t *testing.T) {
	t.Parallel()

	l, t0 := guardLayer(t, map[string]stp.Port{
		"1/1/1": {RestrictedRole: true},
		"1/1/2": {},
	})

	l.Receive(t0.Add(time.Second), "1/1/1", superiorBPDU(0, 20*time.Second))

	root, _, rootPort := l.Root()
	if root.Priority != 32768 || rootPort != "" {
		t.Errorf("root = %v via %q, want this bridge unchanged by a restricted port", root, rootPort)
	}
	if info := l.PortInfo("1/1/1"); info.Role != stp.RoleAlternate {
		t.Errorf("restricted port role = %v, want Alternate: superior information still blocks it", info.Role)
	}

	// The same BPDU on an unrestricted port does elect the sender.
	l.Receive(t0.Add(2*time.Second), "1/1/2", superiorBPDU(0, 20*time.Second))
	if root, _, rootPort := l.Root(); root.Priority != 4096 || rootPort != "1/1/2" {
		t.Errorf("root = %v via %q, want the received root via the unrestricted port", root, rootPort)
	}
}

// TestRestrictedTCNDoesNotPropagate is evidence for R15b.
func TestRestrictedTCNDoesNotPropagate(t *testing.T) {
	t.Parallel()

	restricted, t0 := guardLayer(t, map[string]stp.Port{
		"1/1/1": {RestrictedTCN: true},
		"1/1/2": {},
		"1/1/3": {},
	})
	fx := restricted.Receive(t0.Add(4*time.Second), "1/1/1", stp.BPDU{Type: stp.BPDUTypeTopologyChangeNotification})
	if len(fx.Flush) != 0 {
		t.Errorf("Flush = %v, want nothing: a restricted port does not propagate the change", fx.Flush)
	}

	// Without the guard the same notification flushes the other ports, which is
	// what makes the assertion above about the guard and not about the fabric.
	plain, t0 := guardLayer(t, map[string]stp.Port{
		"1/1/1": {},
		"1/1/2": {},
		"1/1/3": {},
	})
	fx = plain.Receive(t0.Add(4*time.Second), "1/1/1", stp.BPDU{Type: stp.BPDUTypeTopologyChangeNotification})
	if !slices.Contains(fx.Flush, "1/1/2") || !slices.Contains(fx.Flush, "1/1/3") {
		t.Errorf("Flush = %v, want the other two ports without the guard", fx.Flush)
	}
}

// TestLoopGuardHoldsPortDiscardingWhenBPDUsStop is evidence for R15b: the port
// whose designated peer went quiet stays out of the topology instead of
// claiming the segment and opening a loop.
func TestLoopGuardHoldsPortDiscardingWhenBPDUsStop(t *testing.T) {
	t.Parallel()

	l, t0 := guardLayer(t, map[string]stp.Port{
		"1/1/1": {LoopGuard: true},
		"1/1/2": {},
	})

	// A better bridge on 1/1/1 makes it the root port.
	l.Receive(t0.Add(time.Second), "1/1/1", superiorBPDU(0, 20*time.Second))
	if info := l.PortInfo("1/1/1"); info.Role != stp.RoleRoot {
		t.Fatalf("role = %v, want Root before the peer goes quiet", info.Role)
	}

	// Silence past three hello times expires the information.
	l.Wake(t0.Add(time.Second).Add(7 * time.Second))

	info := l.PortInfo("1/1/1")
	if info.Role != stp.RoleAlternate || info.State != stp.StateDiscarding {
		t.Errorf("guarded port = %v/%v, want Alternate/Discarding", info.Role, info.State)
	}
	if info.BlockReason != stp.BlockReasonLoopInconsistent {
		t.Errorf("block reason = %q, want %q", info.BlockReason, stp.BlockReasonLoopInconsistent)
	}
	if l.Forwards("1/1/1", 0) {
		t.Error("loop-inconsistent port forwards, which is the loop the guard exists to prevent")
	}

	// The next BPDU on the port restores normal role selection.
	l.Receive(t0.Add(10*time.Second), "1/1/1", superiorBPDU(0, 20*time.Second))
	info = l.PortInfo("1/1/1")
	if info.BlockReason != "" {
		t.Errorf("block reason after recovery = %q, want none", info.BlockReason)
	}
	if info.Role != stp.RoleRoot {
		t.Errorf("role after recovery = %v, want Root", info.Role)
	}
}

// TestWithoutLoopGuardTheQuietPortBecomesDesignated is the counterfactual for
// the test above: it is the false answer the guard exists to remove.
func TestWithoutLoopGuardTheQuietPortBecomesDesignated(t *testing.T) {
	t.Parallel()

	l, t0 := guardLayer(t, map[string]stp.Port{
		"1/1/1": {},
		"1/1/2": {},
	})

	l.Receive(t0.Add(time.Second), "1/1/1", superiorBPDU(0, 20*time.Second))
	l.Wake(t0.Add(time.Second).Add(7 * time.Second))

	info := l.PortInfo("1/1/1")
	if info.Role != stp.RoleDesignated {
		t.Errorf("unguarded port role = %v, want Designated", info.Role)
	}
	if info.BlockReason != "" {
		t.Errorf("unguarded port block reason = %q, want none", info.BlockReason)
	}
}

// TestLoopGuardIsInactiveWhereVendorsExcludeIt is evidence for R15d.
func TestLoopGuardIsInactiveWhereVendorsExcludeIt(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	// A shared link: the port is guarded, but the link is not point-to-point,
	// so a port that stops hearing BPDUs is not evidence of a broken direction.
	shared := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "02:00:00:00:00:10"),
		Ports: map[string]stp.Port{
			"1/1/1": {LoopGuard: true, PointToPoint: stp.PointToPointForceFalse},
			"1/1/2": {},
		},
	}, mustPortTable(t, "1/1/1", "1/1/2"))
	shared.LinkChange(t0, "1/1/1", true, false, 1_000_000_000)
	shared.LinkChange(t0, "1/1/2", true, true, 1_000_000_000)

	shared.Receive(t0.Add(time.Second), "1/1/1", superiorBPDU(0, 20*time.Second))
	shared.Wake(t0.Add(time.Second).Add(7 * time.Second))
	if got := shared.PortInfo("1/1/1").BlockReason; got != "" {
		t.Errorf("shared-link port block reason = %q, want none: loop guard does not watch it", got)
	}

	// An operationally edge port, reached through auto edge rather than through
	// AdminEdge, which validation refuses beside the guard.
	edge := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "02:00:00:00:00:11"),
		Ports: map[string]stp.Port{
			"1/1/1": {LoopGuard: true, AutoEdge: true},
			"1/1/2": {},
		},
	}, mustPortTable(t, "1/1/1", "1/1/2"))
	edge.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)
	edge.LinkChange(t0, "1/1/2", true, true, 1_000_000_000)

	// No BPDU ever arrives, so the edge delay makes the port an edge.
	edge.Wake(t0.Add(stp.MigrateTime + time.Second))
	if !edge.PortInfo("1/1/1").Edge {
		t.Fatal("port did not become an operational edge; the case under test was not reached")
	}
	edge.Wake(t0.Add(30 * time.Second))
	if got := edge.PortInfo("1/1/1").BlockReason; got != "" {
		t.Errorf("edge port block reason = %q, want none: loop guard does not watch an edge", got)
	}
}
