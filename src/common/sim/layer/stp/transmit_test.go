package stp_test

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

func decodeTestEmission(t *testing.T, emission layer.Emission) (bpdu.BPDU, vlan.ID) {
	t.Helper()
	if emission.Frame.Dst == bpdu.GroupAddressSSTP() {
		decoded, vid, err := bpdu.DecodeSSTP(emission.Frame)
		if err != nil {
			t.Fatalf("decode SSTP emission: %v", err)
		}

		return decoded, vid
	}
	decoded, err := bpdu.Decode(emission.Frame)
	if err != nil {
		t.Fatalf("decode IEEE emission: %v", err)
	}

	return decoded, 0
}

func TestTransmitOnceFromFinalState(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "00:11:22:33:44:02"),
		Ports:    map[string]stp.Port{"p1": {}, "p2": {}},
	}, mustPortTable(t, "p1", "p2"))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)

	peer := bpdu.BPDU{
		Version:      2,
		Type:         bpdu.TypeRapid,
		RootID:       bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	peer.SetRole(bpdu.RoleDesignated)
	peer.SetProposal(true)

	effects := l.Receive(now.Add(100*time.Millisecond), "p1", peer)
	if len(effects.Emissions) == 0 {
		t.Fatal("final-state receive emitted no frames")
	}
	seen := map[string]int{}
	for _, emission := range effects.Emissions {
		seen[emission.Port]++
		if emission.Port == "p1" {
			decoded, _ := decodeTestEmission(t, emission)
			if decoded.Role() != l.PortInfo("p1").Role {
				t.Fatalf("p1 frame role = %v, final role = %v", decoded.Role(), l.PortInfo("p1").Role)
			}
		}
	}
	for port, count := range seen {
		if count != 1 {
			t.Fatalf("port %s transmitted %d frames in one call, want one", port, count)
		}
	}
}

func TestHeldRequestIsBuiltAtRelease(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		TxHoldCount: 1,
		Address:     mustMAC(t, "00:11:22:33:44:02"),
		Ports:       map[string]stp.Port{"p1": {}},
	}, mustPortTable(t, "p1"))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)

	if effects := l.Mcheck(now.Add(100*time.Millisecond), "p1"); len(effects.Emissions) != 0 {
		t.Fatalf("held request emitted before the count fell: %+v", effects.Emissions)
	}
	effects := l.Advance(now.Add(time.Second))
	if len(effects.Emissions) != 1 {
		t.Fatalf("released request emitted %d frames, want one", len(effects.Emissions))
	}
	decoded, _ := decodeTestEmission(t, effects.Emissions[0])
	if decoded.Type != bpdu.TypeRapid {
		t.Fatalf("released request type = %v, want Rapid", decoded.Type)
	}
}

func TestMSTIFlagAloneSendsOnlyInsideTheRegion(t *testing.T) {
	runMSTIFlagAloneSendsOnlyInsideTheRegion(t)
}

func runMSTIFlagAloneSendsOnlyInsideTheRegion(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	region := stp.MST{Name: "region-1", Revision: 1, Instances: map[bpdu.MSTID]stp.Instance{
		1: {VLANs: []vlan.ID{10}},
		2: {VLANs: []vlan.ID{20}},
	}}
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "00:11:22:33:44:02"),
		Ports:    map[string]stp.Port{"p1": {}, "p2": {}, "p3": {}},
		MST:      &region,
	}, mustPortTable(t, "p1", "p2", "p3"))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)
	l.LinkChange(now, "p3", true, true, 1_000_000_000)
	boundary := bpdu.BPDU{
		Version:      2,
		Type:         bpdu.TypeRapid,
		RootID:       bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
		PortID:       0x8002,
		HelloTime:    60 * time.Second,
		MaxAge:       120 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	boundary.SetRole(bpdu.RoleDesignated)
	boundary.SetProposal(true)
	l.Receive(now, "p1", boundary)
	l.Advance(now.Add(15 * time.Second))
	l.Advance(now.Add(30 * time.Second))

	effects := l.Receive(now.Add(35*time.Second), "p2", mstiChangeBPDU(t, region))
	if len(effects.Emissions) == 0 {
		t.Fatal("MSTI-only change produced no emission")
	}
	foundMSTI1 := false
	for _, emission := range effects.Emissions {
		if emission.Port == "p1" {
			decoded, _ := decodeTestEmission(t, emission)
			t.Fatalf("boundary Root port emitted an MSTI-only request: type=%v tc=%t msti=%v frame=%+v", decoded.Type, decoded.TopologyChange(), decoded.MSTIs, emission)
		}
		decoded, _ := decodeTestEmission(t, emission)
		if decoded.TopologyChange() {
			t.Fatal("MSTI-only change set the CIST topology-change flag")
		}
		if len(decoded.MSTIs) != 2 {
			t.Fatalf("MSTI record count = %d, want two", len(decoded.MSTIs))
		}
		if !(bpdu.BPDU{Flags: decoded.MSTIs[0].Flags}).TopologyChange() {
			continue
		}
		foundMSTI1 = true
		if (bpdu.BPDU{Flags: decoded.MSTIs[1].Flags}).TopologyChange() {
			t.Fatal("MSTI 2 topology-change flag is set")
		}
	}
	if !foundMSTI1 {
		t.Fatal("no emitted MSTI 1 record carried the topology-change flag")
	}
}

func TestMSTIFlagOnAPortInSTPModeWaits(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	region := stp.MST{Name: "region-1", Revision: 1, Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: []vlan.ID{10}}}}
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "00:11:22:33:44:02"),
		Ports:   map[string]stp.Port{"p1": {}, "p2": {}},
		MST:     &region,
	}, mustPortTable(t, "p1", "p2"))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)
	legacy := legacyConfigBPDU(t, 32768, "00:11:22:33:44:02")
	legacy.BridgeID = bpdu.BridgeID{Priority: 32768, Address: mustMAC(t, "00:11:22:33:44:03")}
	l.Receive(now.Add(4*time.Second), "p2", legacy)
	if info := l.PortInfo("p2"); info.Role != bpdu.RoleDesignated || info.SendRSTP {
		t.Fatalf("p2 after legacy migration = role %v, SendRSTP %t, want Designated and false", info.Role, info.SendRSTP)
	}

	effects := l.Receive(now.Add(5*time.Second), "p1", mstiChangeBPDU(t, region))
	for _, emission := range effects.Emissions {
		if emission.Port == "p2" {
			t.Fatalf("STP port emitted an MSTI-only request before migration back: %+v", effects.Emissions)
		}
	}

	effects = l.Advance(now.Add(6 * time.Second))
	legacyHello := 0
	for _, emission := range effects.Emissions {
		if emission.Port != "p2" {
			continue
		}
		legacyHello++
		decoded, _ := decodeTestEmission(t, emission)
		if decoded.Type != bpdu.TypeConfiguration {
			t.Fatalf("p2 hello type = %v, want Configuration", decoded.Type)
		}
	}
	if legacyHello != 1 {
		t.Fatalf("p2 emitted %d legacy hellos, want one", legacyHello)
	}

	migration := mstiChangeBPDU(t, region)
	migration.MSTIs = nil
	migration.SetAgreement(true)
	effects = l.Receive(now.Add(7*time.Second), "p2", migration)
	for _, emission := range effects.Emissions {
		if emission.Port != "p2" {
			continue
		}
		decoded, _ := decodeTestEmission(t, emission)
		if decoded.Type != bpdu.TypeRapid {
			t.Fatalf("p2 emission after RSTP migration = %v, want Rapid", decoded.Type)
		}
		return
	}
	t.Fatal("p2 emitted no MST BPDU after RSTP migration")
}

func TestHelloRestartsWhenThePortTransmits(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{Ports: map[string]stp.Port{"p1": {}}}, mustPortTable(t, "p1"))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	if effects := l.Advance(now.Add(2 * time.Second)); len(effects.Emissions) != 1 {
		t.Fatalf("hello emitted %d frames, want one", len(effects.Emissions))
	}
	if effects := l.Advance(now.Add(3 * time.Second)); len(effects.Emissions) != 0 {
		t.Fatalf("hello restarted from its scheduled instant: %+v", effects.Emissions)
	}
	wake, ok := l.NextWake()
	if !ok || !wake.Equal(now.Add(4*time.Second)) {
		t.Fatalf("NextWake = (%v, %v), want (%v, true)", wake, ok, now.Add(4*time.Second))
	}
}

func TestOverdueHelloIsSettledBeforeTheEvent(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{Ports: map[string]stp.Port{"p1": {}, "p3": {}}}, mustPortTable(t, "p1", "p3"))
	l.LinkChange(now, "p1", true, false, 1_000_000_000)
	l.LinkChange(now, "p3", true, false, 1_000_000_000)
	legacy := legacyConfigBPDU(t, 4096, "00:11:22:33:44:01")
	legacy.HelloTime = 20 * time.Second
	l.Receive(now.Add(4*time.Second), "p1", legacy)
	l.Advance(now.Add(15 * time.Second))
	l.Advance(now.Add(30 * time.Second))

	flagged := bpdu.BPDU{
		Version:      2,
		Type:         bpdu.TypeRapid,
		RootID:       bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
		PortID:       0x8003,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	flagged.SetRole(bpdu.RoleDesignated)
	flagged.SetTopologyChange(true)

	effects := l.Receive(now.Add(33*time.Second), "p3", flagged)
	for _, emission := range effects.Emissions {
		if emission.Port == "p1" {
			t.Fatalf("overdue hello emitted on p1 while applying the event: %+v", emission)
		}
	}
	wake, ok := l.NextWake()
	if !ok || wake.After(now.Add(34*time.Second)) {
		t.Fatalf("NextWake after settling overdue hello = (%v, %v), want no later than %v", wake, ok, now.Add(34*time.Second))
	}

	effects = l.Advance(now.Add(34 * time.Second))
	for _, emission := range effects.Emissions {
		if emission.Port != "p1" {
			continue
		}
		decoded, _ := decodeTestEmission(t, emission)
		if decoded.Type != bpdu.TypeTopologyChangeNotification {
			t.Fatalf("p1 emission at the settled hello = %v, want TCN", decoded.Type)
		}
		return
	}
	t.Fatal("p1 emitted no TCN at the settled hello")
}

func TestPortComingUpTransmitsOnce(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		port stp.Port
		p2p  bool
	}{
		{name: "edge", port: stp.Port{AdminEdge: true}, p2p: true},
		{name: "shared", port: stp.Port{}, p2p: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			l := mustNewSTP(t, stp.Config{Ports: map[string]stp.Port{"p1": test.port}}, mustPortTable(t, "p1"))
			effects := l.LinkChange(now, "p1", true, test.p2p, 1_000_000_000)
			if len(effects.Emissions) != 1 {
				t.Fatalf("link-up emitted %d frames, want one", len(effects.Emissions))
			}
		})
	}

	t.Run("pvst initializes every tree", func(t *testing.T) {
		l := mustNewSTP(t, stp.Config{
			Address: mustMAC(t, "00:11:22:33:44:02"),
			Ports:   map[string]stp.Port{"p1": {}},
			PVST:    pvstTestTrees(1, 10, 20),
		}, mustPortTable(t, "p1"))
		effects := l.LinkChange(now, "p1", true, true, 1_000_000_000)
		counts := map[vlan.ID]int{}
		for _, emission := range effects.Emissions {
			_, vid := decodeTestEmission(t, emission)
			counts[vid]++
		}
		for vid, want := range map[vlan.ID]int{0: 1, 1: 1, 10: 1, 20: 1} {
			if counts[vid] != want {
				t.Errorf("PVST VLAN %d emissions = %d, want %d", vid, counts[vid], want)
			}
		}
		if len(counts) != 4 {
			t.Fatalf("PVST link-up emission VLANs = %v, want one IEEE and VLANs 1, 10, 20", counts)
		}
	})

	t.Run("mst initializes the shared record", func(t *testing.T) {
		region := stp.MST{Name: "region-1", Revision: 1, Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: []vlan.ID{10}}}}
		l := mustNewSTP(t, stp.Config{Ports: map[string]stp.Port{"p1": {}}, MST: &region}, mustPortTable(t, "p1"))
		effects := l.LinkChange(now, "p1", true, true, 1_000_000_000)
		if len(effects.Emissions) != 1 {
			t.Fatalf("MST link-up emitted %d frames, want one", len(effects.Emissions))
		}
		decoded, _ := decodeTestEmission(t, effects.Emissions[0])
		if decoded.ConfigID == nil {
			t.Fatal("MST link-up emitted no configuration identifier")
		}
	})

	t.Run("held request survives down and up", func(t *testing.T) {
		l := mustNewSTP(t, stp.Config{TxHoldCount: 1, Ports: map[string]stp.Port{"p1": {}}}, mustPortTable(t, "p1"))
		l.LinkChange(now, "p1", true, true, 1_000_000_000)
		if effects := l.Mcheck(now.Add(100*time.Millisecond), "p1"); len(effects.Emissions) != 0 {
			t.Fatalf("held request emitted before link down: %+v", effects.Emissions)
		}
		l.LinkChange(now.Add(200*time.Millisecond), "p1", false, true, 0)
		effects := l.LinkChange(now.Add(300*time.Millisecond), "p1", true, true, 1_000_000_000)
		if len(effects.Emissions) != 1 {
			t.Fatalf("link-up after held request emitted %d frames, want one", len(effects.Emissions))
		}
	})
}

func TestEmissionsLeaveByTreeThenPort(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "00:11:22:33:44:02"),
		Ports:   map[string]stp.Port{"p1": {}, "p2": {}},
		PVST:    pvstTestTrees(1, 10, 20),
	}, mustPortTable(t, "p1", "p2"))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)
	effects := l.Advance(now.Add(2 * time.Second))

	var order []string
	for _, emission := range effects.Emissions {
		if emission.Frame.Dst != bpdu.GroupAddressSSTP() {
			continue
		}
		_, vid := decodeTestEmission(t, emission)
		order = append(order, emission.Port+":"+strconv.FormatUint(uint64(vid), 10))
	}
	want := []string{"p1:1", "p2:1", "p1:10", "p2:10", "p1:20", "p2:20"}
	if !slices.Equal(order, want) {
		t.Fatalf("SSTP emission order = %v, want %v", order, want)
	}
}

func TestTopologyChangeLeavesAtOnceAndTwice(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{Ports: map[string]stp.Port{"p1": {}, "p2": {}}}, mustPortTable(t, "p1", "p2"))
	l.LinkChange(now, "p1", true, false, 1_000_000_000)
	l.LinkChange(now, "p2", true, false, 1_000_000_000)
	l.Advance(now.Add(15 * time.Second))
	effects := l.Advance(now.Add(30 * time.Second))
	if len(effects.Emissions) != 2 {
		t.Fatalf("first forwarding transition emitted %d frames, want two", len(effects.Emissions))
	}
	for _, emission := range effects.Emissions {
		decoded, _ := decodeTestEmission(t, emission)
		if !decoded.TopologyChange() {
			t.Fatalf("first transition frame on %s lacks topology-change flag", emission.Port)
		}
	}
	if effects := l.Advance(now.Add(31 * time.Second)); len(effects.Emissions) != 0 {
		t.Fatalf("second pass restarted topology-change transmission: %+v", effects.Emissions)
	}
}

func TestDetectionTransmitsOnTheDetectingPort(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	t.Run("rstp agreement", func(t *testing.T) {
		l := mustNewSTP(t, stp.Config{Ports: map[string]stp.Port{"p1": {}, "p2": {}}}, mustPortTable(t, "p1", "p2"))
		l.LinkChange(now, "p1", true, true, 1_000_000_000)
		l.LinkChange(now, "p2", true, true, 1_000_000_000)
		peer := bpdu.BPDU{
			Version:      2,
			Type:         bpdu.TypeRapid,
			RootID:       bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
			BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
			PortID:       0x8001,
			HelloTime:    2 * time.Second,
			MaxAge:       20 * time.Second,
			ForwardDelay: 15 * time.Second,
		}
		peer.SetRole(bpdu.RoleDesignated)
		peer.SetProposal(true)
		effects := l.Receive(now.Add(100*time.Millisecond), "p1", peer)
		for _, emission := range effects.Emissions {
			if emission.Port != "p1" {
				continue
			}
			decoded, _ := decodeTestEmission(t, emission)
			if decoded.TopologyChange() {
				return
			}
		}
		t.Fatal("RSTP detecting port emitted no flagged BPDU")
	})

	t.Run("legacy TCN", func(t *testing.T) {
		l := mustNewSTP(t, stp.Config{
			HelloTime:    3 * time.Second,
			MaxAge:       8 * time.Second,
			ForwardDelay: 5 * time.Second,
			Ports:        map[string]stp.Port{"p1": {}},
		}, mustPortTable(t, "p1"))
		l.LinkChange(now, "p1", true, false, 1_000_000_000)
		legacy := legacyConfigBPDU(t, 4096, "00:11:22:33:44:01")
		legacy.HelloTime = 20 * time.Second
		legacy.MaxAge = 8 * time.Second
		legacy.ForwardDelay = 5 * time.Second
		l.Receive(now.Add(3*time.Second), "p1", legacy)
		l.Advance(now.Add(5 * time.Second))
		l.Advance(now.Add(9 * time.Second))
		effects := l.Advance(now.Add(11 * time.Second))
		for _, emission := range effects.Emissions {
			decoded, _ := decodeTestEmission(t, emission)
			if decoded.Type == bpdu.TypeTopologyChangeNotification {
				return
			}
		}
		t.Fatal("legacy detecting port emitted no TCN")
	})
}

func TestLegacyPortReportsAChangeAtItsHello(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{Ports: map[string]stp.Port{"p1": {}}}, mustPortTable(t, "p1"))
	l.LinkChange(now, "p1", true, false, 1_000_000_000)
	l.Receive(now.Add(4*time.Second), "p1", legacyConfigBPDU(t, 4096, "00:11:22:33:44:01"))
	l.Advance(now.Add(15 * time.Second))
	effects := l.Advance(now.Add(30 * time.Second))
	if len(effects.Emissions) != 1 {
		t.Fatalf("legacy hello emitted %d frames, want one", len(effects.Emissions))
	}
	decoded, _ := decodeTestEmission(t, effects.Emissions[0])
	if decoded.Type != bpdu.TypeConfiguration || !decoded.TopologyChange() {
		t.Fatalf("legacy hello = type %v TC %v, want Configuration with TC", decoded.Type, decoded.TopologyChange())
	}
}

func TestTopologyChangeFlagIsPerTree(t *testing.T) {
	runMSTIFlagAloneSendsOnlyInsideTheRegion(t)
}

func TestAcknowledgmentLeavesInTheNextConfigurationBPDU(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{Ports: map[string]stp.Port{"p1": {}}}, mustPortTable(t, "p1"))
	l.LinkChange(now, "p1", true, false, 1_000_000_000)
	l.Receive(now.Add(4*time.Second), "p1", legacyConfigBPDU(t, 4096, "00:11:22:33:44:01"))
	l.Advance(now.Add(15 * time.Second))
	l.Advance(now.Add(30 * time.Second))

	if effects := l.Receive(now.Add(31*time.Second), "p1", bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification}); len(effects.Emissions) != 0 {
		t.Fatalf("TCN produced an immediate frame: %+v", effects.Emissions)
	}
	effects := l.Advance(now.Add(32 * time.Second))
	if len(effects.Emissions) != 1 {
		t.Fatalf("next hello emitted %d frames, want one", len(effects.Emissions))
	}
	ack, _ := decodeTestEmission(t, effects.Emissions[0])
	if !ack.TopologyChangeAck() || !ack.TopologyChange() {
		t.Fatalf("next Configuration BPDU = TCAck %v TC %v, want both", ack.TopologyChangeAck(), ack.TopologyChange())
	}

	effects = l.Advance(now.Add(34 * time.Second))
	if len(effects.Emissions) != 1 {
		t.Fatalf("following hello emitted %d frames, want one", len(effects.Emissions))
	}
	following, _ := decodeTestEmission(t, effects.Emissions[0])
	if following.TopologyChangeAck() || !following.TopologyChange() {
		t.Fatalf("following Configuration BPDU = TCAck %v TC %v, want false/true", following.TopologyChangeAck(), following.TopologyChange())
	}
}

func mstiChangeBPDU(t *testing.T, region stp.MST) bpdu.BPDU {
	t.Helper()
	local := bpdu.BridgeID{Priority: 32768, Address: mustMAC(t, "00:11:22:33:44:02")}
	peer := bpdu.BridgeID{Priority: 32768, Address: mustMAC(t, "00:11:22:33:44:03")}
	regionalRoot := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}
	cid := region.ConfigID()
	b := bpdu.BPDU{
		Version:        3,
		Type:           bpdu.TypeRapid,
		RootID:         local,
		RegionalRootID: local,
		BridgeID:       peer,
		PortID:         0x8002,
		HelloTime:      2 * time.Second,
		MaxAge:         20 * time.Second,
		ForwardDelay:   15 * time.Second,
		RemainingHops:  20,
		ConfigID:       &cid,
		MSTIs: []bpdu.MSTIRecord{
			{MSTID: 1, RegionalRootID: regionalRoot, BridgePriority: 0x10, PortPriority: 0x80, RemainingHops: 20},
			{MSTID: 2, RegionalRootID: local, BridgePriority: 0x80, PortPriority: 0x80, RemainingHops: 20},
		},
	}
	b.SetRole(bpdu.RoleDesignated)
	var flags bpdu.BPDU
	flags.SetRole(bpdu.RoleDesignated)
	flags.SetTopologyChange(true)
	b.MSTIs[0].Flags = flags.Flags

	return b
}

func pvstTestTrees(ids ...vlan.ID) *stp.PVST {
	trees := make(map[vlan.ID]stp.Tree, len(ids))
	for _, id := range ids {
		trees[id] = stp.Tree{}
	}

	return &stp.PVST{Trees: trees}
}
