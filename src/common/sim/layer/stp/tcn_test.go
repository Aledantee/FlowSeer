package stp_test

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

func TestIEETCNUnderPVSTIsVLAN1s(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "00:11:22:33:44:02"),
		Ports:   map[string]stp.Port{"p1": {}, "p2": {}},
		PVST:    pvstTestTrees(1, 10),
	}, mustPortTable(t, "p1", "p2"))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)
	l.Advance(now.Add(15 * time.Second))
	l.Advance(now.Add(30 * time.Second))

	for _, vid := range []vlan.ID{1, 10} {
		for _, port := range []string{"p1", "p2"} {
			info := l.VLANPortInfo(vid, port)
			if info.State != stp.StateForwarding {
				t.Fatalf("VLAN %d port %s = %v, want Forwarding", vid, port, info.State)
			}
		}
	}

	tcnAt := now.Add(36 * time.Second)
	effects := l.Receive(tcnAt, "p1", bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification})
	target, ok := flushTarget(effects.Flush, "p2")
	if !ok {
		t.Fatalf("TCN flushes = %+v, want a target for p2", effects.Flush)
	}
	if !slices.Equal(target.FIDs, []vlan.ID{1}) {
		t.Errorf("p2 TCN flush FIDs = %v, want [1]", target.FIDs)
	}
	assertPVSTVLAN10Unflagged(t, effects.Emissions)

	nextHello := l.Advance(tcnAt.Add(2 * time.Second))
	assertPVSTVLAN10Unflagged(t, nextHello.Emissions)

	region := stp.MST{
		Name:     "region-1",
		Revision: 1,
		Instances: map[bpdu.MSTID]stp.Instance{
			1: {VLANs: []vlan.ID{10}},
		},
	}
	mst := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "00:11:22:33:44:02"),
		Ports:   map[string]stp.Port{"p1": {}, "p2": {}},
		MST:     &region,
	}, mustPortTable(t, "p1", "p2"))
	mst.LinkChange(now, "p1", true, true, 1_000_000_000)
	mst.LinkChange(now, "p2", true, true, 1_000_000_000)
	mst.Advance(now.Add(15 * time.Second))
	mst.Advance(now.Add(30 * time.Second))

	mstEffects := mst.Receive(tcnAt, "p1", bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification})
	var msti1Flagged bool
	for _, emission := range mstEffects.Emissions {
		if emission.Port != "p2" {
			continue
		}
		decoded, err := bpdu.Decode(emission.Frame)
		if err != nil {
			t.Fatalf("decode MST emission: %v", err)
		}
		for _, record := range decoded.MSTIs {
			if record.MSTID == 1 {
				msti1Flagged = (bpdu.BPDU{Flags: record.Flags}).TopologyChange()
			}
		}
	}
	if !msti1Flagged {
		t.Fatal("MSTI 1 record on p2 lacks a topology-change flag")
	}
}

func TestEveryTCNPropagates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "00:11:22:33:44:02"),
		Ports: map[string]stp.Port{
			"p1": {},
			"p2": {},
			"p3": {},
			"p4": {},
		},
	}, mustPortTable(t, "p1", "p2", "p3", "p4"))
	for _, port := range []string{"p1", "p2", "p3", "p4"} {
		l.LinkChange(now, port, true, true, 1_000_000_000)
	}

	root := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}
	for _, test := range []struct {
		port   string
		portID uint16
	}{
		{port: "p2", portID: 0x8002},
		{port: "p4", portID: 0x8004},
	} {
		peer := bpdu.BPDU{
			Version:      2,
			Type:         bpdu.TypeRapid,
			RootID:       root,
			BridgeID:     root,
			PortID:       test.portID,
			HelloTime:    60 * time.Second,
			MaxAge:       120 * time.Second,
			ForwardDelay: 15 * time.Second,
		}
		peer.SetRole(bpdu.RoleDesignated)
		peer.SetProposal(true)
		l.Receive(now.Add(4*time.Second), test.port, peer)
	}
	l.Receive(now.Add(4*time.Second), "p1", legacyConfigBPDU(t, 61440, "00:11:22:33:44:03"))
	l.Advance(now.Add(15 * time.Second))
	l.Advance(now.Add(30 * time.Second))

	for _, test := range []struct {
		port string
		role bpdu.Role
	}{
		{port: "p1", role: bpdu.RoleDesignated},
		{port: "p2", role: bpdu.RoleRoot},
		{port: "p3", role: bpdu.RoleDesignated},
		{port: "p4", role: bpdu.RoleAlternate},
	} {
		info := l.PortInfo(test.port)
		if info.Role != test.role || (test.role != bpdu.RoleAlternate && info.State != stp.StateForwarding) {
			t.Fatalf("%s = %v/%v, want %v/%v", test.port, info.Role, info.State, test.role, stateForRole(test.role))
		}
	}
	if l.PortInfo("p1").SendRSTP {
		t.Fatal("p1 is still in RSTP mode, want STP mode for the TCN receiver")
	}

	l.Advance(now.Add(164 * time.Second))
	l.Advance(now.Add(166 * time.Second))
	firstAt := now.Add(167 * time.Second)
	beforeChanges, _ := l.TopologyChanges()
	first := l.Receive(firstAt, "p1", bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification})
	assertFlaggedPorts(t, first.Emissions, "p2", "p3")
	if got := flushPorts(first.Flush); !slices.Equal(got, []string{"p2", "p3"}) {
		t.Fatalf("first TCN flush ports = %v, want [p2 p3]", got)
	}
	middleChanges, _ := l.TopologyChanges()
	if middleChanges != beforeChanges+1 {
		t.Fatalf("topology changes after first TCN = %d, want %d", middleChanges, beforeChanges+1)
	}
	firstHello := l.Advance(firstAt.Add(2 * time.Second))
	var acknowledged bool
	for _, emission := range firstHello.Emissions {
		if emission.Port != "p1" {
			continue
		}
		decoded, err := bpdu.Decode(emission.Frame)
		if err != nil {
			t.Fatalf("decode p1 hello: %v", err)
		}
		acknowledged = decoded.Type == bpdu.TypeConfiguration && decoded.TopologyChangeAck()
	}
	if !acknowledged {
		t.Fatal("Designated TCN receiver did not acknowledge on its next Configuration BPDU")
	}

	second := l.Receive(firstAt.Add(10*time.Second), "p1", bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification})
	assertFlaggedPorts(t, second.Emissions, "p2", "p3")
	if got := flushPorts(second.Flush); !slices.Equal(got, []string{"p2", "p3"}) {
		t.Fatalf("second TCN flush ports = %v, want [p2 p3]", got)
	}
	afterChanges, _ := l.TopologyChanges()
	if afterChanges != middleChanges {
		t.Errorf("topology changes after second TCN = %d, want unchanged at %d", afterChanges, middleChanges)
	}

	alternate := l.Receive(firstAt.Add(11*time.Second), "p4", bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification})
	if len(alternate.Flush) != 0 {
		t.Errorf("Alternate TCN flushes = %+v, want none", alternate.Flush)
	}
	assertUnflaggedPort(t, alternate.Emissions, "p4")
	assertUnflaggedPort(t, l.Advance(firstAt.Add(13*time.Second)).Emissions, "p4")
}

func assertPVSTVLAN10Unflagged(t *testing.T, emissions []layer.Emission) {
	t.Helper()

	for _, emission := range emissions {
		if emission.Frame.Dst != bpdu.GroupAddressSSTP() {
			continue
		}
		decoded, vid := decodeTestEmission(t, emission)
		if vid == 10 && decoded.TopologyChange() {
			t.Errorf("VLAN 10 emission on %s carries a topology-change flag", emission.Port)
		}
	}
}

func assertFlaggedPorts(t *testing.T, emissions []layer.Emission, want ...string) {
	t.Helper()

	seen := make(map[string]int, len(want))
	for _, emission := range emissions {
		decoded, _ := decodeTestEmission(t, emission)
		if decoded.TopologyChange() {
			seen[emission.Port]++
		}
	}
	for _, port := range want {
		if seen[port] != 1 {
			t.Errorf("%s emitted %d topology-change frames, want one", port, seen[port])
		}
	}
}

func assertUnflaggedPort(t *testing.T, emissions []layer.Emission, port string) {
	t.Helper()

	for _, emission := range emissions {
		if emission.Port != port {
			continue
		}
		decoded, _ := decodeTestEmission(t, emission)
		if decoded.TopologyChange() {
			t.Errorf("%s emitted a topology-change frame", port)
		}
	}
}

func stateForRole(role bpdu.Role) stp.State {
	if role == bpdu.RoleAlternate {
		return stp.StateDiscarding
	}

	return stp.StateForwarding
}
