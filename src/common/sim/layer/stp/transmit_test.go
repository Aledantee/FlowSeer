package stp_test

import (
	"bytes"
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

func countPortType(t *testing.T, effects layer.Effects, port string, typ bpdu.Type) int {
	t.Helper()
	count := 0
	for _, emission := range effects.Emissions {
		if emission.Port != port {
			continue
		}
		decoded, _ := decodeTestEmission(t, emission)
		if decoded.Type == typ {
			count++
		}
	}

	return count
}

func TestTransmitOnceFromFinalState(t *testing.T) {
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

	peer := bpdu.BPDU{
		Version:        3,
		Type:           bpdu.TypeRapid,
		RootID:         bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
		RegionalRootID: bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
		BridgeID:       bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
		PortID:         0x8001,
		HelloTime:      2 * time.Second,
		MaxAge:         20 * time.Second,
		ForwardDelay:   15 * time.Second,
		RemainingHops:  20,
		ConfigID:       new(region.ConfigID()),
		MSTIs: []bpdu.MSTIRecord{
			{MSTID: 1, RegionalRootID: bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}, BridgePriority: 0x20, RemainingHops: 20},
			{MSTID: 2, RegionalRootID: bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}, BridgePriority: 0x20, RemainingHops: 20},
		},
	}
	peer.SetRole(bpdu.RoleDesignated)
	peer.SetProposal(true)
	l.Receive(now.Add(100*time.Millisecond), "p1", peer)
	l.Receive(now.Add(200*time.Millisecond), "p2", mstiChangeBPDU(t, region))
	peer.RootID = bpdu.BridgeID{Priority: 2048, Address: mustMAC(t, "00:11:22:33:44:00")}
	peer.RegionalRootID = peer.RootID
	peer.BridgeID = bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}
	peer.MSTIs[0].RegionalRootID = bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}
	peer.MSTIs[0].BridgePriority = 0x20
	effects := l.Receive(now.Add(300*time.Millisecond), "p1", peer)
	if len(effects.Emissions) != 2 {
		t.Fatalf("final-state receive emitted %d frames, want one per port", len(effects.Emissions))
	}
	if got := l.PortInfo("p1"); got.Role != bpdu.RoleRoot {
		t.Fatalf("p1 CIST role = %v, want Root", got.Role)
	}
	if got := l.VLANPortInfo(10, "p1"); got.Role != bpdu.RoleAlternate {
		t.Fatalf("p1 MSTI 1 role = %v, want Alternate", got.Role)
	}
	if got := l.VLANPortInfo(10, "p2"); got.Role != bpdu.RoleRoot || got.State != stp.StateForwarding {
		t.Fatalf("p2 MSTI 1 state = %v/%v, want Root/Forwarding", got.Role, got.State)
	}
	var p2Frame bpdu.BPDU
	for _, emission := range effects.Emissions {
		if emission.Port == "p2" {
			p2Frame, _ = decodeTestEmission(t, emission)
		}
	}
	if p2Frame.Type != bpdu.TypeRapid || p2Frame.Role() != bpdu.RoleDesignated || !p2Frame.Proposal() {
		t.Fatalf("p2 final frame = type %v role %v proposal %t, want Rapid/Designated/true", p2Frame.Type, p2Frame.Role(), p2Frame.Proposal())
	}
	if len(p2Frame.MSTIs) != 2 {
		t.Fatalf("p2 final frame has %d MSTI records, want two", len(p2Frame.MSTIs))
	}
	msti1 := bpdu.BPDU{Flags: p2Frame.MSTIs[0].Flags}
	if msti1.Role() != bpdu.RoleRoot || !msti1.TopologyChange() {
		t.Fatalf("p2 final MSTI 1 record = role %v TC %t, want Root/true", msti1.Role(), msti1.TopologyChange())
	}
	if msti2 := (bpdu.BPDU{Flags: p2Frame.MSTIs[1].Flags}); msti2.TopologyChange() {
		t.Fatal("p2 final MSTI 2 record carried a topology-change flag")
	}
	if effects := l.Advance(now.Add(time.Second)); len(effects.Emissions) != 0 {
		t.Fatalf("final-state frame was repeated on the next advance: %+v", effects.Emissions)
	}
}

func TestHeldRequestIsBuiltAtRelease(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	t.Run("MSTI request", func(t *testing.T) {
		region := stp.MST{Name: "region-1", Revision: 1, Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: []vlan.ID{10}}}}
		l := mustNewSTP(t, stp.Config{
			TxHoldCount: 1,
			Address:     mustMAC(t, "00:11:22:33:44:02"),
			Ports:       map[string]stp.Port{"p1": {}, "p2": {}},
			MST:         &region,
		}, mustPortTable(t, "p1", "p2"))
		l.LinkChange(now, "p1", true, true, 1_000_000_000)
		l.LinkChange(now, "p2", true, true, 1_000_000_000)
		if effects := l.Receive(now.Add(100*time.Millisecond), "p1", mstiChangeBPDU(t, region)); len(effects.Emissions) != 0 {
			t.Fatalf("MSTI request escaped before hold release: %+v", effects.Emissions)
		}
		effects := l.Advance(now.Add(time.Second))
		var frame bpdu.BPDU
		for _, emission := range effects.Emissions {
			if emission.Port == "p1" {
				frame, _ = decodeTestEmission(t, emission)
			}
		}
		if frame.Type != bpdu.TypeRapid || frame.Role() != bpdu.RoleDesignated || frame.TopologyChange() {
			t.Fatalf("released CIST frame = type %v role %v TC %t, want Rapid/Designated/false", frame.Type, frame.Role(), frame.TopologyChange())
		}
		if len(frame.MSTIs) != 1 {
			t.Fatalf("released frame has %d MSTI records, want one", len(frame.MSTIs))
		}
		msti := bpdu.BPDU{Flags: frame.MSTIs[0].Flags}
		if msti.Role() != bpdu.RoleRoot || !msti.TopologyChange() {
			t.Fatalf("released MSTI record = role %v TC %t, want Root/true", msti.Role(), msti.TopologyChange())
		}
	})

	t.Run("legacy Root port", func(t *testing.T) {
		l := mustNewSTP(t, stp.Config{
			TxHoldCount: 1,
			Address:     mustMAC(t, "00:11:22:33:44:02"),
			Ports:       map[string]stp.Port{"p1": {}, "p2": {}, "p3": {}},
		}, mustPortTable(t, "p1", "p2", "p3"))
		for _, port := range []string{"p1", "p2", "p3"} {
			l.LinkChange(now, port, true, true, 1_000_000_000)
		}
		root := bpdu.BPDU{
			Version:      2,
			Type:         bpdu.TypeRapid,
			RootID:       bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
			BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
			PortID:       0x8001,
			HelloTime:    2 * time.Second,
			MaxAge:       20 * time.Second,
			ForwardDelay: 15 * time.Second,
		}
		root.SetRole(bpdu.RoleDesignated)
		root.SetProposal(true)
		l.Receive(now.Add(100*time.Millisecond), "p2", root)
		root.SetProposal(false)
		for second := 2; second <= 34; second += 2 {
			at := now.Add(time.Duration(second) * time.Second)
			l.Receive(at, "p2", root)
			l.Advance(at)
		}
		root.SetProposal(true)
		l.Receive(now.Add(34*time.Second), "p2", root)
		flagged := root
		flagged.RootID = bpdu.BridgeID{Priority: 32768, Address: mustMAC(t, "00:11:22:33:44:02")}
		flagged.RootPathCost = 100_000
		flagged.BridgeID = bpdu.BridgeID{Priority: 61440, Address: mustMAC(t, "00:11:22:33:44:03")}
		flagged.PortID = 0x8001
		flagged.SetRole(bpdu.RoleRoot)
		flagged.SetTopologyChange(true)
		if effects := l.Receive(now.Add(34*time.Second+500*time.Millisecond), "p3", flagged); countPortType(t, effects, "p2", bpdu.TypeTopologyChangeNotification) != 0 {
			t.Fatal("legacy migration emitted a TCN before hold release")
		}
		l.Receive(now.Add(34*time.Second+600*time.Millisecond), "p2", legacyConfigBPDU(t, 4096, "00:11:22:33:44:01"))
		if info := l.PortInfo("p2"); info.SendRSTP {
			t.Fatal("p2 remained in RSTP mode after the legacy Configuration BPDU")
		}
		effects := l.Advance(now.Add(35*time.Second + 600*time.Millisecond))
		if got := countPortType(t, effects, "p2", bpdu.TypeTopologyChangeNotification); got != 1 {
			t.Fatalf("released p2 TCN count = %d, want one", got)
		}
	})

	t.Run("CIST Alternate and MSTI Root", func(t *testing.T) {
		region := stp.MST{Name: "region-1", Revision: 1, Instances: map[bpdu.MSTID]stp.Instance{
			1: {VLANs: []vlan.ID{10}},
		}}
		m := mustNewSTP(t, stp.Config{
			TxHoldCount: 1,
			Address:     mustMAC(t, "00:11:22:33:44:02"),
			Ports:       map[string]stp.Port{"p1": {}, "p2": {}},
			MST:         &region,
		}, mustPortTable(t, "p1", "p2"))
		m.LinkChange(now, "p1", true, true, 1_000_000_000)
		m.LinkChange(now, "p2", true, true, 1_000_000_000)
		root := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}
		mstiRoot := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}
		cid := region.ConfigID()
		p2 := bpdu.BPDU{
			Version:        3,
			Type:           bpdu.TypeRapid,
			RootID:         root,
			RegionalRootID: root,
			BridgeID:       root,
			PortID:         0x8002,
			HelloTime:      2 * time.Second,
			MaxAge:         20 * time.Second,
			ForwardDelay:   15 * time.Second,
			RemainingHops:  20,
			ConfigID:       &cid,
			MSTIs:          []bpdu.MSTIRecord{{MSTID: 1, RegionalRootID: mstiRoot, BridgePriority: 0x80, RemainingHops: 20}},
		}
		p2.SetRole(bpdu.RoleDesignated)
		p2.SetProposal(true)
		p1 := p2
		p1.BridgeID = bpdu.BridgeID{Priority: 8192, Address: mustMAC(t, "00:11:22:33:44:00")}
		p1.PortID = 0x8001
		p1.MSTIs[0].BridgePriority = 0
		m.Receive(now.Add(100*time.Millisecond), "p2", p2)
		m.Receive(now.Add(200*time.Millisecond), "p1", p1)
		if got := m.PortInfo("p1"); got.Role != bpdu.RoleAlternate {
			t.Fatalf("p1 CIST role = %v, want Alternate", got.Role)
		}
		if got := m.VLANPortInfo(10, "p1"); got.Role != bpdu.RoleRoot || got.State != stp.StateForwarding {
			t.Fatalf("p1 MSTI state = %v/%v, want Root/Forwarding", got.Role, got.State)
		}
		effects := m.Advance(now.Add(time.Second))
		var frame bpdu.BPDU
		for _, emission := range effects.Emissions {
			if emission.Port != "p1" {
				continue
			}
			frame, _ = decodeTestEmission(t, emission)
		}
		if frame.Type != bpdu.TypeRapid || frame.Role() != bpdu.RoleAlternate || frame.TopologyChange() {
			t.Fatalf("released p1 CIST frame = type %v role %v TC %t, want Rapid/Alternate/false", frame.Type, frame.Role(), frame.TopologyChange())
		}
		if len(frame.MSTIs) != 1 || (bpdu.BPDU{Flags: frame.MSTIs[0].Flags}).Role() != bpdu.RoleRoot || !(bpdu.BPDU{Flags: frame.MSTIs[0].Flags}).TopologyChange() {
			t.Fatalf("released p1 MSTI record = %+v, want Root with topology change", frame.MSTIs)
		}
	})
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
	t.Run("RSTP", func(t *testing.T) {
		l := mustNewSTP(t, stp.Config{
			Address: mustMAC(t, "00:11:22:33:44:02"),
			Ports:   map[string]stp.Port{"p1": {}, "p2": {}, "p3": {}},
		}, mustPortTable(t, "p1", "p2", "p3"))
		for _, port := range []string{"p1", "p2", "p3"} {
			l.LinkChange(now, port, true, true, 1_000_000_000)
		}
		root := bpdu.BPDU{
			Version:      2,
			Type:         bpdu.TypeRapid,
			RootID:       bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
			BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
			PortID:       0x8001,
			HelloTime:    60 * time.Second,
			MaxAge:       120 * time.Second,
			ForwardDelay: 15 * time.Second,
		}
		root.SetRole(bpdu.RoleDesignated)
		root.SetProposal(true)
		l.Receive(now.Add(4*time.Second), "p1", root)
		l.Advance(now.Add(15 * time.Second))
		l.Advance(now.Add(30 * time.Second))
		l.Advance(now.Add(45 * time.Second))
		l.Advance(now.Add(50 * time.Second))
		flagged := root
		flagged.RootID = bpdu.BridgeID{Priority: 32768, Address: mustMAC(t, "00:11:22:33:44:02")}
		flagged.RootPathCost = 100_000
		flagged.BridgeID = bpdu.BridgeID{Priority: 61440, Address: mustMAC(t, "00:11:22:33:44:03")}
		flagged.PortID = 0x8001
		flagged.SetRole(bpdu.RoleRoot)
		flagged.SetTopologyChange(true)
		effects := l.Receive(now.Add(31*time.Second), "p3", flagged)
		var order []string
		for _, emission := range effects.Emissions {
			decoded, _ := decodeTestEmission(t, emission)
			if decoded.TopologyChange() {
				order = append(order, emission.Port)
			}
		}
		if !slices.Equal(order, []string{"p1", "p2"}) {
			t.Fatalf("RSTP flagged emission order = %v, want [p1 p2]", order)
		}
	})

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
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "00:11:22:33:44:02"),
		Ports:   map[string]stp.Port{"p1": {}, "p2": {}, "p3": {}},
	}, mustPortTable(t, "p1", "p2", "p3"))
	for _, port := range []string{"p1", "p2", "p3"} {
		l.LinkChange(now, port, true, false, 1_000_000_000)
	}
	root := bpdu.BPDU{
		Version:      2,
		Type:         bpdu.TypeRapid,
		RootID:       bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
		PortID:       0x8001,
		HelloTime:    60 * time.Second,
		MaxAge:       120 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	root.SetRole(bpdu.RoleDesignated)
	root.SetProposal(true)
	l.Receive(now.Add(4*time.Second), "p1", root)
	l.Advance(now.Add(15 * time.Second))
	l.Advance(now.Add(30 * time.Second))
	l.Advance(now.Add(45 * time.Second))
	l.Advance(now.Add(50 * time.Second))
	for _, port := range []string{"p1", "p2", "p3"} {
		if info := l.PortInfo(port); info.Role == bpdu.RoleDisabled || info.State != stp.StateForwarding {
			t.Fatalf("settled %s = %v/%v, want active Forwarding", port, info.Role, info.State)
		}
	}

	firstAt := now.Add(50*time.Second + 500*time.Millisecond)
	flagged := root
	flagged.SetProposal(false)
	flagged.SetRole(bpdu.RoleRoot)
	flagged.SetTopologyChange(true)
	effects := l.Receive(firstAt, "p3", flagged)
	flaggedPorts := make(map[string]int)
	for _, emission := range effects.Emissions {
		decoded, _ := decodeTestEmission(t, emission)
		if decoded.TopologyChange() {
			flaggedPorts[emission.Port]++
		}
	}
	for _, port := range []string{"p1", "p2"} {
		if flaggedPorts[port] != 1 {
			t.Fatalf("first flagged frame count on %s = %d, want one", port, flaggedPorts[port])
		}
	}
	if flaggedPorts["p3"] != 0 {
		t.Fatalf("origin port p3 sent %d flagged frames", flaggedPorts["p3"])
	}

	second := l.Receive(firstAt.Add(time.Second), "p3", flagged)
	if len(second.Emissions) != 0 {
		t.Fatalf("second flagged BPDU restarted transmission: %+v", second.Emissions)
	}
	hello := l.Advance(firstAt.Add(2 * time.Second))
	for _, emission := range hello.Emissions {
		decoded, _ := decodeTestEmission(t, emission)
		if !decoded.TopologyChange() {
			t.Fatalf("hello-time frame on %s was not flagged", emission.Port)
		}
	}
	if len(hello.Emissions) != 2 {
		t.Fatalf("hello-time flagged frame count = %d, want two", len(hello.Emissions))
	}

	last := l.Advance(firstAt.Add(3 * time.Second))
	for _, emission := range last.Emissions {
		decoded, _ := decodeTestEmission(t, emission)
		if decoded.TopologyChange() {
			t.Fatalf("frame on %s remained flagged after HelloTime plus one second", emission.Port)
		}
	}
	final := l.Advance(firstAt.Add(4 * time.Second))
	for _, emission := range final.Emissions {
		decoded, _ := decodeTestEmission(t, emission)
		if decoded.TopologyChange() {
			t.Fatalf("frame on %s remained flagged after the hello window", emission.Port)
		}
	}

	t.Run("PVST change stays on VLAN 10", func(t *testing.T) {
		l := mustNewSTP(t, stp.Config{
			Address: mustMAC(t, "00:11:22:33:44:02"),
			Ports:   map[string]stp.Port{"p1": {}, "p3": {}},
			PVST:    pvstTestTrees(1, 10),
		}, mustPortTable(t, "p1", "p3"))
		l.LinkChange(now, "p1", true, false, 1_000_000_000)
		l.LinkChange(now, "p3", true, false, 1_000_000_000)
		root := bpdu.BPDU{
			Version:      2,
			Type:         bpdu.TypeRapid,
			RootID:       bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
			BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
			PortID:       0x8001,
			HelloTime:    60 * time.Second,
			MaxAge:       120 * time.Second,
			ForwardDelay: 15 * time.Second,
		}
		root.SetRole(bpdu.RoleDesignated)
		root.SetProposal(true)
		if _, outcome := l.ReceiveSSTP(now.Add(4*time.Second), "p1", stp.SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}, root); outcome != stp.SSTPApplied {
			t.Fatalf("VLAN 10 root BPDU outcome = %v, want applied", outcome)
		}
		l.Advance(now.Add(15 * time.Second))
		l.Advance(now.Add(30 * time.Second))
		l.Advance(now.Add(45 * time.Second))
		l.Advance(now.Add(50 * time.Second))
		if got := l.VLANPortInfo(10, "p1").Role; got != bpdu.RoleRoot {
			t.Fatalf("VLAN 10 p1 role = %v, want Root", got)
		}

		flagged := root
		flagged.SetProposal(false)
		flagged.SetRole(bpdu.RoleRoot)
		flagged.SetTopologyChange(true)
		effects, outcome := l.ReceiveSSTP(now.Add(51*time.Second+500*time.Millisecond), "p3", stp.SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}, flagged)
		if outcome != stp.SSTPApplied {
			t.Fatalf("flagged VLAN 10 BPDU outcome = %v, want applied", outcome)
		}
		var vlan10Flagged, vlan1Flagged bool
		for _, emission := range effects.Emissions {
			decoded, vid := decodeTestEmission(t, emission)
			if decoded.TopologyChange() && vid == 10 {
				vlan10Flagged = true
			}
			if decoded.TopologyChange() && (vid == 0 || vid == 1) {
				vlan1Flagged = true
			}
		}
		if !vlan10Flagged || vlan1Flagged {
			t.Fatalf("PVST emissions = %+v, want a flagged VLAN 10 frame and no flagged VLAN 1 frame", effects.Emissions)
		}
	})
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
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "00:11:22:33:44:02"),
		Ports:   map[string]stp.Port{"p1": {}, "p2": {}, "p3": {}},
	}, mustPortTable(t, "p1", "p2", "p3"))
	for _, port := range []string{"p1", "p2", "p3"} {
		l.LinkChange(now, port, true, false, 1_000_000_000)
	}
	legacy := legacyConfigBPDU(t, 4096, "00:11:22:33:44:01")
	for second := 4; second <= 100; second += 2 {
		at := now.Add(time.Duration(second) * time.Second)
		l.Receive(at, "p1", legacy)
		l.Advance(at)
	}
	if info := l.PortInfo("p1"); info.Role != bpdu.RoleRoot || info.State != stp.StateForwarding || info.SendRSTP {
		t.Fatalf("settled legacy p1 = %v/%v RSTP=%t, want Root/Forwarding STP", info.Role, info.State, info.SendRSTP)
	}

	flagged := bpdu.BPDU{
		Version:      2,
		Type:         bpdu.TypeRapid,
		RootID:       bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
		BridgeID:     bpdu.BridgeID{Priority: 61440, Address: mustMAC(t, "00:11:22:33:44:03")},
		PortID:       0x8003,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	flagged.SetRole(bpdu.RoleRoot)
	flagged.SetTopologyChange(true)
	eventAt := now.Add(100*time.Second + 500*time.Millisecond)
	effects := l.Receive(eventAt, "p3", flagged)
	for _, emission := range effects.Emissions {
		if emission.Port != "p1" {
			continue
		}
		decoded, _ := decodeTestEmission(t, emission)
		if decoded.Type == bpdu.TypeTopologyChangeNotification {
			t.Fatal("legacy Root port emitted a TCN in the flagged receive call")
		}
	}

	tcnCount := 0
	for second := 102; second <= 136; second += 2 {
		at := now.Add(time.Duration(second) * time.Second)
		effects = l.Receive(at, "p1", legacy)
		for _, emission := range effects.Emissions {
			if emission.Port != "p1" {
				continue
			}
			decoded, _ := decodeTestEmission(t, emission)
			if decoded.Type != bpdu.TypeTopologyChangeNotification {
				continue
			}
			if second > 134 {
				t.Fatalf("TCN at %s is past the 35-second timer", at)
			}
			tcnCount++
		}
	}
	if tcnCount == 0 {
		t.Fatal("legacy Root port emitted no TCNs at its hellos")
	}
}

func TestVLANTreeReportsAChangeInSTPMode(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 4096,
		Address:  mustMAC(t, "00:11:22:33:44:01"),
		Ports:    map[string]stp.Port{"p1": {}, "p2": {}, "p3": {}},
		PVST:     pvstTestTrees(1, 10),
	}, mustPortTable(t, "p1", "p2", "p3"))
	for _, port := range []string{"p1", "p2", "p3"} {
		l.LinkChange(start, port, true, false, 1_000_000_000)
	}

	cistPeer := legacyConfigBPDU(t, 0, "00:aa:bb:cc:dd:00")
	cistPeer.HelloTime = 60 * time.Second
	cistPeer.MaxAge = 120 * time.Second
	l.Receive(start.Add(3500*time.Millisecond), "p1", cistPeer)

	peer := sstpConfigurationBPDU(bpdu.BridgeID{
		Priority: 0,
		Address:  mustMAC(t, "00:aa:bb:cc:dd:01"),
	})
	peer.HelloTime = 60 * time.Second
	peer.MaxAge = 120 * time.Second
	if _, outcome := l.ReceiveSSTP(start.Add(3500*time.Millisecond), "p1", stp.SSTPArrival{
		ArrivalVID: 10,
		TLVVID:     10,
		Admitted:   true,
	}, peer); outcome != stp.SSTPApplied {
		t.Fatalf("VLAN 10 root Configuration outcome = %q, want applied", outcome)
	}

	l.Advance(start.Add(15 * time.Second))
	l.Advance(start.Add(30 * time.Second))
	if info := l.VLANPortInfo(10, "p1"); info.Role != bpdu.RoleRoot || info.State != stp.StateForwarding || info.SendRSTP {
		t.Fatalf("VLAN 10 p1 = %v/%v RSTP=%t, want Root/Forwarding/STP", info.Role, info.State, info.SendRSTP)
	}
	cistAck := cistPeer
	cistAck.SetTopologyChangeAck(true)
	l.Receive(start.Add(30500*time.Millisecond), "p1", cistAck)

	flagged := peer
	flagged.Version = 2
	flagged.Type = bpdu.TypeRapid
	flagged.SetRole(bpdu.RoleRoot)
	flagged.SetTopologyChange(true)
	effects, outcome := l.ReceiveSSTP(start.Add(31*time.Second), "p3", stp.SSTPArrival{
		ArrivalVID: 10,
		TLVVID:     10,
		Admitted:   true,
	}, flagged)
	if outcome != stp.SSTPApplied {
		t.Fatalf("flagged VLAN 10 BPDU outcome = %q, want applied", outcome)
	}
	for _, emission := range effects.Emissions {
		if emission.Port == "p1" {
			t.Fatalf("p1 emitted while receiving the flagged BPDU: %+v", effects.Emissions)
		}
	}

	effects = l.Advance(start.Add(32 * time.Second))
	var tcnCount int
	for _, emission := range effects.Emissions {
		if emission.Port != "p1" {
			continue
		}
		if emission.Frame.Dst != bpdu.GroupAddressSSTP() {
			t.Fatalf("p1 emitted a VLAN 1 frame for the VLAN 10 topology change: %+v", emission)
		}
		if emission.Frame.Dst != bpdu.GroupAddressSSTP() || emission.VID != 10 {
			t.Fatalf("p1 VLAN 10 emission = %+v, want SSTP VID 10", emission)
		}
		decoded, vid := decodeTestEmission(t, emission)
		if decoded.Type != bpdu.TypeTopologyChangeNotification || vid != 0 {
			t.Fatalf("p1 VLAN 10 frame = type %v decoded VID %d, want SSTP TCN/0", decoded.Type, vid)
		}
		wantPayload := append([]byte{
			0xaa, 0xaa, 0x03, 0x00, 0x00, 0x0c, 0x01, 0x0b,
			0x00, 0x00, 0x00, 0x80,
		}, make([]byte, 34)...)
		if !bytes.Equal(emission.Frame.Payload, wantPayload) {
			t.Fatalf("p1 VLAN 10 TCN payload = % x, want % x", emission.Frame.Payload, wantPayload)
		}
		tcnCount++
	}
	if tcnCount != 1 {
		t.Fatalf("p1 VLAN 10 TCN count = %d, want one", tcnCount)
	}

	effects = l.Advance(start.Add(34 * time.Second))
	var repeatedTCN int
	for _, emission := range effects.Emissions {
		if emission.Port != "p1" {
			continue
		}
		if emission.Frame.Dst != bpdu.GroupAddressSSTP() || emission.VID != 10 {
			t.Fatalf("repeated p1 VLAN 10 emission = %+v, want SSTP VID 10", emission)
		}
		decoded, _ := decodeTestEmission(t, emission)
		if decoded.Type != bpdu.TypeTopologyChangeNotification {
			t.Fatalf("repeated p1 VLAN 10 frame type = %v, want TCN", decoded.Type)
		}
		repeatedTCN++
	}
	if repeatedTCN != 1 {
		t.Fatalf("repeated p1 VLAN 10 TCN count = %d, want one", repeatedTCN)
	}

	ack := peer
	ack.SetTopologyChangeAck(true)
	if effects, outcome := l.ReceiveSSTP(start.Add(34500*time.Millisecond), "p1", stp.SSTPArrival{
		ArrivalVID: 10,
		TLVVID:     10,
		Admitted:   true,
	}, ack); outcome != stp.SSTPApplied {
		t.Fatalf("VLAN 10 acknowledgment outcome = %q with effects %+v, want applied", outcome, effects)
	} else if len(effects.Emissions) != 0 {
		t.Fatalf("VLAN 10 acknowledgment emitted immediately: %+v", effects.Emissions)
	}

	effects = l.Advance(start.Add(36 * time.Second))
	for _, emission := range effects.Emissions {
		if emission.Port == "p1" {
			t.Fatalf("p1 emitted after VLAN 10 acknowledgment: %+v", effects.Emissions)
		}
	}
	for _, emission := range effects.Emissions {
		if emission.Port == "p1" && emission.VID == 1 {
			t.Fatal("p1 emitted a VLAN 1 frame for the VLAN 10 topology change")
		}
	}
}

func TestVLANTreeAcknowledgesInSTPMode(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 4096,
		Address:  mustMAC(t, "00:11:22:33:44:02"),
		Ports:    map[string]stp.Port{"p1": {}, "p2": {}},
		PVST:     pvstTestTrees(1, 10),
	}, mustPortTable(t, "p1", "p2"))
	for _, port := range []string{"p1", "p2"} {
		l.LinkChange(start, port, true, false, 1_000_000_000)
	}

	peer := sstpConfigurationBPDU(bpdu.BridgeID{
		Priority: 61440,
		Address:  mustMAC(t, "00:aa:bb:cc:dd:02"),
	})
	peer.HelloTime = 60 * time.Second
	peer.MaxAge = 120 * time.Second
	if _, outcome := l.ReceiveSSTP(start.Add(3500*time.Millisecond), "p2", stp.SSTPArrival{
		ArrivalVID: 10,
		TLVVID:     10,
		Admitted:   true,
	}, peer); outcome != stp.SSTPApplied {
		t.Fatalf("VLAN 10 Configuration outcome = %q, want applied", outcome)
	}
	l.Advance(start.Add(15 * time.Second))
	effects := l.Advance(start.Add(30 * time.Second))
	if info := l.VLANPortInfo(10, "p2"); info.Role != bpdu.RoleDesignated || info.State != stp.StateForwarding || info.SendRSTP {
		t.Fatalf("VLAN 10 p2 = %v/%v RSTP=%t, want Designated/Forwarding/STP", info.Role, info.State, info.SendRSTP)
	}

	var first bpdu.BPDU
	for _, emission := range effects.Emissions {
		if emission.Port != "p2" || emission.Frame.Dst != bpdu.GroupAddressSSTP() || emission.VID != 10 {
			continue
		}
		var vid vlan.ID
		first, vid = decodeTestEmission(t, emission)
		if first.Type != bpdu.TypeConfiguration || vid != 10 {
			t.Fatalf("first VLAN 10 frame = type %v vid %d, want Configuration/10", first.Type, vid)
		}
		break
	}
	if first.Type != bpdu.TypeConfiguration {
		t.Fatal("p2 emitted no VLAN 10 SSTP Configuration BPDU at its hello")
	}

	tcn := bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification}
	if effects, outcome := l.ReceiveSSTP(start.Add(31*time.Second), "p2", stp.SSTPArrival{
		ArrivalVID: 10,
		TLVVID:     0,
		Admitted:   true,
	}, tcn); outcome != stp.SSTPApplied {
		t.Fatalf("VLAN 10 TCN outcome = %q with effects %+v, want applied", outcome, effects)
	} else if len(effects.Emissions) != 0 {
		t.Fatalf("VLAN 10 TCN emitted immediately: %+v", effects.Emissions)
	}

	effects = l.Advance(start.Add(32 * time.Second))
	var ack bpdu.BPDU
	var ackVID vlan.ID
	for _, emission := range effects.Emissions {
		if emission.Port == "p2" && emission.Frame.Dst == bpdu.GroupAddressSSTP() && emission.VID == 10 {
			ack, ackVID = decodeTestEmission(t, emission)
			break
		}
	}
	if ack.Type != bpdu.TypeConfiguration || ackVID != 10 || !ack.TopologyChange() || !ack.TopologyChangeAck() {
		t.Fatalf("next VLAN 10 Configuration = type %v vid %d TC=%t TCAck=%t, want Configuration/10/true/true", ack.Type, ackVID, ack.TopologyChange(), ack.TopologyChangeAck())
	}

	effects = l.Advance(start.Add(34 * time.Second))
	for _, emission := range effects.Emissions {
		if emission.Port != "p2" || emission.Frame.Dst != bpdu.GroupAddressSSTP() || emission.VID != 10 {
			continue
		}
		following, vid := decodeTestEmission(t, emission)
		if following.Type != bpdu.TypeConfiguration || vid != 10 || following.TopologyChangeAck() {
			t.Fatalf("following VLAN 10 Configuration = type %v vid %d TC=%t TCAck=%t, want Configuration/10/any/false", following.Type, vid, following.TopologyChange(), following.TopologyChangeAck())
		}
		return
	}
	t.Fatal("p2 emitted no following VLAN 10 Configuration BPDU")
}

func TestTopologyChangeFlagIsPerTree(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	region := stp.MST{Name: "region-1", Revision: 1, Instances: map[bpdu.MSTID]stp.Instance{
		1: {VLANs: []vlan.ID{10}},
		2: {VLANs: []vlan.ID{20}},
	}}
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "00:11:22:33:44:02"),
		Ports:   map[string]stp.Port{"p1": {}, "p2": {}, "p3": {}},
		MST:     &region,
	}, mustPortTable(t, "p1", "p2", "p3"))
	for _, port := range []string{"p1", "p2", "p3"} {
		l.LinkChange(now, port, true, true, 1_000_000_000)
	}
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
	transmissions := 0
	msti1Flagged := false
	for _, emission := range effects.Emissions {
		if emission.Port == "p1" {
			t.Fatal("boundary port emitted an MSTI-only change")
		}
		payload := emission.Frame.Payload
		if len(payload) <= 121 {
			t.Fatalf("MST emission payload has length %d, want MSTI 2 flags at offset 121", len(payload))
		}
		if payload[7]&0x01 != 0 {
			t.Fatalf("CIST topology-change flag set in payload byte 7: %#02x", payload[7])
		}
		if payload[121]&0x01 != 0 {
			t.Fatalf("MSTI 2 topology-change flag set in payload byte 121: %#02x", payload[121])
		}
		if payload[105]&0x01 != 0 {
			msti1Flagged = true
		}
		transmissions++
	}
	if transmissions == 0 {
		t.Fatal("MSTI-only change produced no raw MST emission")
	}
	if !msti1Flagged {
		t.Fatal("no raw MST emission carried the MSTI 1 topology-change flag")
	}
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

	t.Run("RSTP designated port clears an inherited acknowledgment", func(t *testing.T) {
		l := mustNewSTP(t, stp.Config{
			TxHoldCount: 1,
			Address:     mustMAC(t, "00:11:22:33:44:02"),
			Ports:       map[string]stp.Port{"p1": {}, "p2": {}},
		}, mustPortTable(t, "p1", "p2"))
		l.LinkChange(now, "p1", true, true, 1_000_000_000)
		l.LinkChange(now, "p2", true, true, 1_000_000_000)
		root := bpdu.BPDU{
			Version:      2,
			Type:         bpdu.TypeRapid,
			RootID:       bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
			BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
			PortID:       0x8001,
			HelloTime:    2 * time.Second,
			MaxAge:       20 * time.Second,
			ForwardDelay: 15 * time.Second,
		}
		root.SetRole(bpdu.RoleDesignated)
		root.SetProposal(true)
		l.Receive(now.Add(4*time.Second), "p1", root)
		l.Advance(now.Add(15 * time.Second))
		l.Advance(now.Add(30 * time.Second))
		l.Mcheck(now.Add(31*time.Second), "p1")
		effects := l.Receive(now.Add(31*time.Second+100*time.Millisecond), "p1", bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification})
		if len(effects.Emissions) != 0 {
			t.Fatalf("TCN emitted before the held RSTP transmission was released: %+v", effects.Emissions)
		}
		effects = l.Advance(now.Add(32 * time.Second))
		if len(effects.Emissions) != 1 {
			t.Fatalf("released RSTP acknowledgment emitted %d frames, want one", len(effects.Emissions))
		}
		ack, _ := decodeTestEmission(t, effects.Emissions[0])
		if ack.Type != bpdu.TypeRapid || ack.TopologyChangeAck() {
			t.Fatalf("released RSTP BPDU = type %v TCAck %t, want Rapid/false", ack.Type, ack.TopologyChangeAck())
		}
	})

	t.Run("legacy migration clears the next acknowledgment", func(t *testing.T) {
		l := mustNewSTP(t, stp.Config{
			Address: mustMAC(t, "00:11:22:33:44:02"),
			Ports:   map[string]stp.Port{"p1": {}, "p2": {}},
		}, mustPortTable(t, "p1", "p2"))
		l.LinkChange(now, "p1", true, true, 1_000_000_000)
		l.LinkChange(now, "p2", true, true, 1_000_000_000)
		root := bpdu.BPDU{
			Version:      2,
			Type:         bpdu.TypeRapid,
			RootID:       bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
			BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")},
			PortID:       0x8001,
			HelloTime:    2 * time.Second,
			MaxAge:       20 * time.Second,
			ForwardDelay: 15 * time.Second,
		}
		root.SetRole(bpdu.RoleDesignated)
		root.SetProposal(true)
		l.Receive(now.Add(4*time.Second), "p1", root)
		l.Advance(now.Add(15 * time.Second))
		l.Advance(now.Add(30 * time.Second))
		l.Mcheck(now.Add(31*time.Second), "p2")
		l.Receive(now.Add(31*time.Second+100*time.Millisecond), "p2", bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification})
		legacy := legacyConfigBPDU(t, 32768, "00:11:22:33:44:02")
		legacy.BridgeID = bpdu.BridgeID{Priority: 32768, Address: mustMAC(t, "00:11:22:33:44:03")}
		l.Receive(now.Add(35*time.Second), "p2", legacy)
		if info := l.PortInfo("p2"); info.SendRSTP || info.Role != bpdu.RoleDesignated {
			t.Fatalf("p2 after legacy migration = role %v RSTP=%t, want Designated/STP", info.Role, info.SendRSTP)
		}
		for second := 36; second <= 40; second++ {
			effects := l.Advance(now.Add(time.Duration(second) * time.Second))
			for _, emission := range effects.Emissions {
				if emission.Port != "p2" {
					continue
				}
				frame, _ := decodeTestEmission(t, emission)
				if frame.Type != bpdu.TypeConfiguration {
					continue
				}
				if frame.TopologyChangeAck() {
					t.Fatal("next legacy Configuration BPDU carried TCAck")
				}
				return
			}
		}
		t.Fatal("legacy migration emitted no next Configuration BPDU")
	})
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
