package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

func TestAutoEdgeWaitsAfterReturningToDesignated(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "02:00:00:00:00:02"),
		Ports:    map[string]stp.Port{"p1": {AutoEdge: true}, "p2": {}},
	}, mustPortTable(t, "p1", "p2"))
	l.LinkChange(start, "p1", true, true, 1_000_000_000)
	l.LinkChange(start, "p2", true, true, 1_000_000_000)

	peer := legacyConfigBPDU(t, 4096, "02:00:00:00:00:01")
	peer.Type = bpdu.TypeRapid
	peer.SetRole(bpdu.RoleDesignated)
	l.Receive(start.Add(time.Second), "p2", peer)
	peer.PortID = 0x8002
	l.Receive(start.Add(time.Second), "p1", peer)
	if info := l.PortInfo("p1"); info.Role != bpdu.RoleAlternate {
		t.Fatalf("role after superior BPDU = %v, want Alternate", info.Role)
	}
	peer.PortID = 0x8001
	l.Receive(start.Add(6*time.Second), "p2", peer)

	returned := start.Add(8 * time.Second)
	l.Advance(returned)
	if info := l.PortInfo("p1"); info.Role != bpdu.RoleDesignated || info.State != stp.StateDiscarding || info.Edge {
		t.Fatalf("port at role return = %v/%v/edge %t, want Designated/Discarding/non-edge", info.Role, info.State, info.Edge)
	}
	l.Advance(returned.Add(2 * time.Second))
	if info := l.PortInfo("p1"); info.Edge {
		t.Fatal("port became edge before MigrateTime after returning to Designated")
	}
	l.Advance(returned.Add(3 * time.Second))
	if info := l.PortInfo("p1"); !info.Edge {
		t.Fatal("port did not become edge at MigrateTime after returning to Designated")
	}
}
