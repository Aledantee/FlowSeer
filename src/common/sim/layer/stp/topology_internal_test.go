package stp

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

func TestPropagateTopologyChangeRetainsRunningTimer(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	l := newLayer(Config{Ports: map[string]Port{"p1": {}, "p2": {}}})
	l.links["p1"].up = true
	p := l.cist().ports["p1"]
	p.tcActive = true
	p.tcWhile = now.Add(10 * time.Second)

	l.propagateTopologyChange(l.cist(), "p2", now.Add(time.Second), nil)

	if !p.tcWhile.Equal(now.Add(10 * time.Second)) {
		t.Fatalf("running topology-change timer moved to %v, want %v", p.tcWhile, now.Add(10*time.Second))
	}
}

func TestPortThatBecomesAnEdgeLeavesTheActiveTopology(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for _, automatic := range []bool{false, true} {
		name := "state recompute"
		if automatic {
			name = "auto-edge expiry"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			l := newLayer(Config{Ports: map[string]Port{"p1": {AutoEdge: true}}}.Normalize(layer.Env{}))
			l.LinkChange(now, "p1", true, true, 1_000_000_000)
			tr := l.cist()
			p := tr.ports["p1"]
			p.tcActive = true
			p.tcWhile = now.Add(time.Minute)
			p.tcAck = true
			before := tr.topologyChangeCount

			var flushes []layer.FlushTarget
			if automatic {
				if !l.edgeDelayPending("p1") {
					t.Fatal("p1 has no pending auto-edge transition")
				}
				if !l.advanceAutoEdge(now.Add(migrateTime), &flushes) {
					t.Fatal("auto-edge delay did not expire")
				}
			} else {
				l.links["p1"].edge = true
				l.recomputeAll(now, &flushes)
			}

			if !l.links["p1"].edge || p.state != StateForwarding {
				t.Errorf("edge transition = edge %t state %v, want edge/Forwarding", l.links["p1"].edge, p.state)
			}
			if p.tcActive || !p.tcWhile.IsZero() || p.tcAck {
				t.Errorf("edge topology state = active %t timer %v ack %t, want inactive/zero/false", p.tcActive, p.tcWhile, p.tcAck)
			}
			if len(flushes) != 1 || flushes[0].Port != "p1" {
				t.Errorf("edge flushes = %+v, want p1 alone", flushes)
			}
			if tr.topologyChangeCount != before {
				t.Errorf("topology changes = %d, want %d", tr.topologyChangeCount, before)
			}
		})
	}
}

func TestTopologyChangeFlagDoesNotSetAcknowledgment(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for _, typ := range []bpdu.Type{bpdu.TypeConfiguration, bpdu.TypeRapid} {
		name := "Configuration"
		if typ == bpdu.TypeRapid {
			name = "Rapid"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			l := newLayer(Config{Ports: map[string]Port{"p1": {}, "p2": {}}}.Normalize(layer.Env{}))
			for _, port := range l.portNames {
				l.LinkChange(now, port, true, true, 1_000_000_000)
				p := l.cist().ports[port]
				p.state = StateForwarding
				p.agreed = true
				p.tcActive = true
			}
			p := l.cist().ports["p1"]
			l.links["p1"].sendRSTP = false
			l.links["p1"].mdelayWhile = now.Add(time.Minute)
			b := bpdu.BPDU{
				Version: 2, Type: typ,
				RootID: bpdu.BridgeID{Priority: 61440}, BridgeID: bpdu.BridgeID{Priority: 61440},
				PortID: 0x8001, HelloTime: 2 * time.Second, MaxAge: 20 * time.Second, ForwardDelay: 15 * time.Second,
			}
			b.SetRole(bpdu.RoleDesignated)
			b.SetTopologyChange(true)

			fx := l.Receive(now.Add(time.Second), "p1", b)

			if p.role != bpdu.RoleDesignated || !p.tcActive {
				t.Fatalf("TC receiver = %v active %t, want active Designated", p.role, p.tcActive)
			}
			if len(fx.Flush) != 1 || fx.Flush[0].Port != "p2" || !l.cist().ports["p2"].tcWhile.After(now.Add(time.Second)) {
				t.Errorf("TC propagation = flushes %+v timer %v, want p2 flushed with a running timer", fx.Flush, l.cist().ports["p2"].tcWhile)
			}
			replies := 0
			for _, emission := range fx.Emissions {
				if emission.Port != "p1" {
					continue
				}
				reply, err := bpdu.Decode(emission.Frame)
				if err != nil {
					t.Fatal(err)
				}
				replies++
				if reply.Type != bpdu.TypeConfiguration || reply.TopologyChangeAck() {
					t.Errorf("TC flag reply = %+v, want Configuration without acknowledgment", reply)
				}
			}
			if replies != 1 || p.tcAck {
				t.Errorf("TC flag replies = %d pending ack %t, want one/false", replies, p.tcAck)
			}
		})
	}
}
