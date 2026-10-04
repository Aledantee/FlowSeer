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
		Ports:    map[string]stp.Port{"p1": {}, "p2": {}},
		MST:      &region,
	}, mustPortTable(t, "p1", "p2"))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)

	peer := mstTestBPDU(t, region, 0x8001, true)
	l.Receive(now, "p1", peer)
	l.Advance(now.Add(15 * time.Second))
	l.Advance(now.Add(30 * time.Second))

	flagged := peer
	flagged.PortID = 0x8001
	flagged.SetProposal(false)
	flagged.SetAgreement(true)
	var flags bpdu.BPDU
	flags.SetRole(bpdu.RoleDesignated)
	flags.SetTopologyChange(true)
	flagged.MSTIs[0].Flags = flags.Flags
	flagged.MSTIs[1].Flags = 0
	effects := l.Receive(now.Add(35*time.Second), "p1", flagged)
	if len(effects.Emissions) == 0 {
		t.Fatal("MSTI-only change produced no emission")
	}
	foundMSTI1 := false
	for _, emission := range effects.Emissions {
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
	l := mustNewSTP(t, stp.Config{Ports: map[string]stp.Port{"p1": {}}}, mustPortTable(t, "p1"))
	l.LinkChange(now, "p1", true, false, 1_000_000_000)
	l.Advance(now.Add(15 * time.Second))
	effects := l.Advance(now.Add(30 * time.Second))
	if len(effects.Emissions) != 1 {
		t.Fatalf("detecting port emitted %d frames, want one", len(effects.Emissions))
	}
	decoded, _ := decodeTestEmission(t, effects.Emissions[0])
	if !decoded.TopologyChange() {
		t.Fatal("detecting port's BPDU lacks topology-change flag")
	}
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

func mstTestBPDU(t *testing.T, region stp.MST, portID uint16, proposal bool) bpdu.BPDU {
	t.Helper()
	root := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}
	peer := bpdu.BPDU{
		Version:        3,
		Type:           bpdu.TypeRapid,
		RootID:         root,
		BridgeID:       root,
		PortID:         portID,
		HelloTime:      20 * time.Second,
		MaxAge:         20 * time.Second,
		ForwardDelay:   15 * time.Second,
		ConfigID:       func() *bpdu.ConfigID { id := region.ConfigID(); return &id }(),
		RegionalRootID: root,
		RemainingHops:  20,
	}
	peer.SetRole(bpdu.RoleDesignated)
	peer.SetProposal(proposal)
	for _, id := range []bpdu.MSTID{1, 2} {
		peer.MSTIs = append(peer.MSTIs, bpdu.MSTIRecord{
			MSTID: id, RegionalRootID: root, RemainingHops: 20,
		})
		peer.MSTIs[len(peer.MSTIs)-1].Flags = peer.Flags
	}

	return peer
}

func pvstTestTrees(ids ...vlan.ID) *stp.PVST {
	trees := make(map[vlan.ID]stp.Tree, len(ids))
	for _, id := range ids {
		trees[id] = stp.Tree{}
	}

	return &stp.PVST{Trees: trees}
}
