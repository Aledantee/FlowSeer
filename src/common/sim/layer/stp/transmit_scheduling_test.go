package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

func TestTopologyTimerIsTheEarliestWake(t *testing.T) {
	start := time.Unix(1700000000, 0)
	l := mustNewSTP(t, stp.Config{Ports: map[string]stp.Port{"p1": {}}}, mustPortTable(t, "p1"))
	l.LinkChange(start, "p1", true, false, 1_000_000_000)
	l.Advance(start.Add(15 * time.Second))
	l.Advance(start.Add(30 * time.Second))
	l.Advance(start.Add(32 * time.Second))
	if wake, ok := l.NextWake(); !ok || !wake.Equal(start.Add(33*time.Second)) {
		t.Fatalf("NextWake = (%v, %t), want topology timer at 33s", wake, ok)
	}
}

func TestLearningUsesTheReceivedForwardDelay(t *testing.T) {
	start := time.Unix(1700000000, 0)
	l := mustNewSTP(t, stp.Config{Ports: map[string]stp.Port{"p1": {}}}, mustPortTable(t, "p1"))
	l.LinkChange(start, "p1", true, false, 1_000_000_000)
	peer := legacyConfigBPDU(t, 4096, "00:11:22:33:44:01")
	peer.MaxAge = 6 * time.Second
	peer.ForwardDelay = 4 * time.Second
	l.Receive(start, "p1", peer)
	l.Advance(start.Add(4 * time.Second))
	if info := l.PortInfo("p1"); info.State != stp.StateLearning {
		t.Fatalf("at 4s = %+v, want Learning", info)
	}
	l.Receive(start.Add(4*time.Second), "p1", peer)
	l.Advance(start.Add(7 * time.Second))
	if info := l.PortInfo("p1"); info.State != stp.StateLearning {
		t.Fatalf("at 7s = %+v, want Learning", info)
	}
	l.Advance(start.Add(8 * time.Second))
	if info := l.PortInfo("p1"); info.Role != bpdu.RoleRoot || info.State != stp.StateForwarding {
		t.Fatalf("at 8s = %+v, want Root/Forwarding", info)
	}
}

func TestPVSTVLANOnePairSpendsOneSlot(t *testing.T) {
	start := time.Unix(1700000000, 0)
	l := mustNewSTP(t, stp.Config{TxHoldCount: 2, Ports: map[string]stp.Port{"p1": {}}, PVST: pvstTestTrees(1)}, mustPortTable(t, "p1"))
	if effects := l.LinkChange(start, "p1", true, true, 1_000_000_000); len(effects.Emissions) != 2 {
		t.Fatalf("initial pair = %d frames, want two", len(effects.Emissions))
	}
	if effects := l.Mcheck(start.Add(100*time.Millisecond), "p1"); len(effects.Emissions) != 2 {
		t.Fatalf("second slot = %d frames, want VLAN 1 pair", len(effects.Emissions))
	}
	if effects := l.Mcheck(start.Add(200*time.Millisecond), "p1"); len(effects.Emissions) != 0 {
		t.Fatalf("at hold count = %d frames, want none", len(effects.Emissions))
	}
	if effects := l.Advance(start.Add(time.Second)); len(effects.Emissions) != 2 {
		t.Fatalf("released slot = %d frames, want VLAN 1 pair", len(effects.Emissions))
	}
}

func TestBPDUGuardStopsAHeldRequest(t *testing.T) {
	start := time.Unix(1700000000, 0)
	l := mustNewSTP(t, stp.Config{TxHoldCount: 1, Ports: map[string]stp.Port{"p1": {BPDUGuard: true}}}, mustPortTable(t, "p1"))
	l.LinkChange(start, "p1", true, true, 1_000_000_000)
	if effects := l.Mcheck(start.Add(100*time.Millisecond), "p1"); len(effects.Emissions) != 0 {
		t.Fatalf("held request emitted: %+v", effects.Emissions)
	}
	if effects := l.Receive(start.Add(200*time.Millisecond), "p1", legacyConfigBPDU(t, 4096, "00:11:22:33:44:01")); len(effects.Emissions) != 0 {
		t.Fatalf("guarded receive emitted: %+v", effects.Emissions)
	}
	if info := l.PortInfo("p1"); info.BlockReason != stp.BlockReasonBPDUGuard {
		t.Fatalf("guarded port = %+v", info)
	}
	if effects := l.Advance(start.Add(time.Second)); len(effects.Emissions) != 0 {
		t.Fatalf("guarded release emitted: %+v", effects.Emissions)
	}
}

func TestRootWithoutTopologyChangeHasNoHelloWake(t *testing.T) {
	start := time.Unix(1700000000, 0)
	l := mustNewSTP(t, stp.Config{Ports: map[string]stp.Port{"p1": {}}}, mustPortTable(t, "p1"))
	l.LinkChange(start, "p1", true, true, 1_000_000_000)
	peer := legacyConfigBPDU(t, 4096, "00:11:22:33:44:01")
	peer.Version = 2
	peer.Type = bpdu.TypeRapid
	l.Receive(start, "p1", peer)
	l.Advance(start.Add(2 * time.Second))
	l.Advance(start.Add(3 * time.Second))
	if info := l.PortInfo("p1"); info.Role != bpdu.RoleRoot || info.State != stp.StateForwarding {
		t.Fatalf("settled port = %+v, want Root/Forwarding", info)
	}
	if wake, ok := l.NextWake(); !ok || !wake.Equal(start.Add(6*time.Second)) {
		t.Fatalf("NextWake = (%v, %t), want received-information expiry at 6s", wake, ok)
	}
}

func TestMSTIHelloOnACISTAlternate(t *testing.T) {
	for _, mstiRole := range []bpdu.Role{bpdu.RoleDesignated, bpdu.RoleRoot} {
		t.Run(map[bpdu.Role]string{bpdu.RoleDesignated: "Designated", bpdu.RoleRoot: "Root"}[mstiRole], func(t *testing.T) {
			start := time.Unix(1700000000, 0)
			region := stp.MST{Name: "region", Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: []vlan.ID{10}}}}
			l := mustNewSTP(t, stp.Config{Address: mustMAC(t, "00:11:22:33:44:02"), Ports: map[string]stp.Port{"p1": {}, "p2": {}}, MST: &region}, mustPortTable(t, "p1", "p2"))
			l.LinkChange(start, "p1", true, true, 1_000_000_000)
			l.LinkChange(start, "p2", true, true, 1_000_000_000)
			root := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}
			peer := bpdu.BPDU{Version: 3, Type: bpdu.TypeRapid, RootID: root, RegionalRootID: root, BridgeID: root, PortID: 0x8001, HelloTime: 2 * time.Second, MaxAge: 20 * time.Second, ForwardDelay: 15 * time.Second, RemainingHops: 20, ConfigID: new(region.ConfigID())}
			peer.SetRole(bpdu.RoleDesignated)
			peer.SetProposal(true)
			l.Receive(start, "p2", peer)
			peer.BridgeID.Priority = 8192
			if mstiRole == bpdu.RoleRoot {
				var flags bpdu.BPDU
				flags.SetRole(bpdu.RoleDesignated)
				flags.SetProposal(true)
				peer.MSTIs = []bpdu.MSTIRecord{{MSTID: 1, RegionalRootID: root, BridgePriority: 0x10, PortPriority: 0x80, RemainingHops: 20, Flags: flags.Flags}}
			}
			l.Receive(start, "p1", peer)
			if info := l.PortInfo("p1"); info.Role != bpdu.RoleAlternate {
				t.Fatalf("CIST p1 = %+v, want Alternate", info)
			}
			if info := l.VLANPortInfo(10, "p1"); info.Role != mstiRole {
				t.Fatalf("MSTI p1 = %+v, want %v", info, mstiRole)
			}
			// A Root port's topology timer runs for three seconds. A Designated
			// port requests a frame at every hello without that timer.
			last := 2
			if mstiRole == bpdu.RoleDesignated {
				last = 4
			}
			for second := 2; second <= last; second += 2 {
				effects := l.Advance(start.Add(time.Duration(second) * time.Second))
				if got := countPortType(t, effects, "p1", bpdu.TypeRapid); got != 1 {
					t.Fatalf("hello at %ds emitted %d MST BPDUs on p1, want one", second, got)
				}
				for _, emission := range effects.Emissions {
					if emission.Port != "p1" {
						continue
					}
					frame, _ := decodeTestEmission(t, emission)
					if frame.ConfigID == nil || frame.Role() != bpdu.RoleAlternate || len(frame.MSTIs) != 1 {
						t.Fatalf("hello frame = %+v, want MST with Alternate CIST and one record", frame)
					}
					if got := (bpdu.BPDU{Flags: frame.MSTIs[0].Flags}).Role(); got != mstiRole {
						t.Fatalf("hello MSTI role = %v, want %v", got, mstiRole)
					}
				}
			}
		})
	}
}

func TestLegacyBlockedPortKeepsItsHeldRequest(t *testing.T) {
	for _, role := range []bpdu.Role{bpdu.RoleAlternate, bpdu.RoleBackup} {
		t.Run(map[bpdu.Role]string{bpdu.RoleAlternate: "Alternate", bpdu.RoleBackup: "Backup"}[role], func(t *testing.T) {
			start := time.Unix(1700000000, 0)
			l := mustNewSTP(t, stp.Config{TxHoldCount: 1, Address: mustMAC(t, "00:11:22:33:44:02"), Ports: map[string]stp.Port{"p1": {}, "p2": {}}}, mustPortTable(t, "p1", "p2"))
			l.LinkChange(start, "p1", true, true, 1_000_000_000)
			l.LinkChange(start, "p2", true, true, 1_000_000_000)
			l.Advance(start.Add(4 * time.Second))
			l.Advance(start.Add(6 * time.Second))
			peer := legacyConfigBPDU(t, 4096, "00:11:22:33:44:01")
			l.Receive(start.Add(6050*time.Millisecond), "p2", peer)
			peer.PortID++
			if role == bpdu.RoleBackup {
				peer.BridgeID = bpdu.BridgeID{Priority: 32768, Address: mustMAC(t, "00:11:22:33:44:02")}
				peer.RootPathCost = 10_000
			}
			l.Receive(start.Add(6100*time.Millisecond), "p1", peer)
			if info := l.PortInfo("p1"); info.Role != role || info.SendRSTP {
				t.Fatalf("held port = %+v, want %v/STP", info, role)
			}
			if effects := l.Advance(start.Add(7 * time.Second)); countPortType(t, effects, "p1", bpdu.TypeConfiguration) != 0 {
				t.Fatalf("%v emitted Configuration: %+v", role, effects.Emissions)
			}
			effects := l.Mcheck(start.Add(7200*time.Millisecond), "p1")
			if got := countPortType(t, effects, "p1", bpdu.TypeRapid); got != 1 {
				t.Fatalf("migration released %d frames, want one held request", got)
			}
		})
	}
}

func TestLegacyTopologyTimerUsesReceivedRootTimes(t *testing.T) {
	start := time.Unix(1700000000, 0)
	l := mustNewSTP(t, stp.Config{Address: mustMAC(t, "00:11:22:33:44:02"), Ports: map[string]stp.Port{"p1": {}, "p2": {}, "p3": {}}}, mustPortTable(t, "p1", "p2", "p3"))
	for _, name := range []string{"p1", "p2", "p3"} {
		l.LinkChange(start, name, true, false, 1_000_000_000)
	}
	peer := legacyConfigBPDU(t, 4096, "00:11:22:33:44:01")
	peer.MaxAge = 6 * time.Second
	peer.ForwardDelay = 4 * time.Second
	for second := 4; second <= 100; second += 2 {
		at := start.Add(time.Duration(second) * time.Second)
		l.Receive(at, "p1", peer)
		l.Advance(at)
	}
	if info := l.PortInfo("p1"); info.Role != bpdu.RoleRoot || info.State != stp.StateForwarding || info.SendRSTP {
		t.Fatalf("settled p1 = %+v, want Root/Forwarding/STP", info)
	}
	flagged := peer
	flagged.Version = 2
	flagged.Type = bpdu.TypeRapid
	flagged.BridgeID = bpdu.BridgeID{Priority: 61440, Address: mustMAC(t, "00:11:22:33:44:03")}
	flagged.SetRole(bpdu.RoleRoot)
	flagged.SetTopologyChange(true)
	l.Receive(start.Add(100500*time.Millisecond), "p3", flagged)
	for second := 102; second <= 112; second += 2 {
		effects := l.Receive(start.Add(time.Duration(second)*time.Second), "p1", peer)
		want := 1
		if second == 112 {
			want = 0
		}
		if got := countPortType(t, effects, "p1", bpdu.TypeTopologyChangeNotification); got != want {
			t.Fatalf("TCNs at %ds = %d, want %d for received 6s + 4s timer", second, got, want)
		}
	}
}
