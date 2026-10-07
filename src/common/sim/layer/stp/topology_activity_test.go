package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

func TestSyncCutRetainsTopologyActivity(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "00:11:22:33:44:02"),
		Ports:   map[string]stp.Port{"p1": {}, "p2": {}},
	}, mustPortTable(t, "p1", "p2"))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)
	root := legacyConfigBPDU(t, 4096, "00:11:22:33:44:01")
	root.Version = 2
	root.Type = bpdu.TypeRapid
	root.HelloTime = 20 * time.Second
	root.SetRole(bpdu.RoleDesignated)
	root.SetProposal(true)
	l.Receive(now.Add(time.Second), "p1", root)
	agreement := root
	agreement.RootPathCost = 100_000
	agreement.BridgeID = bpdu.BridgeID{Priority: 61440, Address: mustMAC(t, "00:11:22:33:44:03")}
	agreement.SetRole(bpdu.RoleRoot)
	agreement.SetProposal(false)
	agreement.SetAgreement(true)
	l.Receive(now.Add(1500*time.Millisecond), "p2", agreement)
	l.Advance(now.Add(5 * time.Second))
	for port, role := range map[string]bpdu.Role{"p1": bpdu.RoleRoot, "p2": bpdu.RoleDesignated} {
		if info := l.PortInfo(port); info.Role != role || info.State != stp.StateForwarding {
			t.Fatalf("%s before sync = %v/%v, want %v/Forwarding", port, info.Role, info.State, role)
		}
	}
	before, _ := l.TopologyChanges()
	cut := l.Receive(now.Add(5500*time.Millisecond), "p1", root)
	if info := l.PortInfo("p2"); info.Role != bpdu.RoleDesignated || info.State != stp.StateDiscarding {
		t.Fatalf("p2 after sync = %v/%v, want Designated/Discarding", info.Role, info.State)
	}
	resume := l.Receive(now.Add(6*time.Second), "p2", agreement)
	if info := l.PortInfo("p2"); info.Role != bpdu.RoleDesignated || info.State != stp.StateForwarding {
		t.Fatalf("p2 after agreement = %v/%v, want Designated/Forwarding", info.Role, info.State)
	}
	if after, _ := l.TopologyChanges(); after != before {
		t.Errorf("sync cut raised topology changes from %d to %d", before, after)
	}
	if len(cut.Flush) != 0 || len(resume.Flush) != 0 {
		t.Errorf("sync cut flushes = %v, resume flushes = %v, want empty", cut.Flush, resume.Flush)
	}
	for _, emission := range append(cut.Emissions, resume.Emissions...) {
		if decoded, _ := decodeTestEmission(t, emission); decoded.TopologyChange() {
			t.Errorf("sync cut emitted a flagged frame on %s", emission.Port)
		}
	}
}

func TestMSTIProposalRequestsOnlyItsTree(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, legacy := range []bool{true, false} {
		t.Run(map[bool]string{true: "STP", false: "RSTP"}[legacy], func(t *testing.T) {
			local := mustMAC(t, "00:11:22:33:44:02")
			peer := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}
			l, region := agreementMSTBridge(t, local, map[string]stp.Port{"p1": {}})
			l.LinkChange(now, "p1", true, true, 1_000_000_000)
			b := agreementBPDU(region.ConfigID(), peer, 0, peer, peer, 0x8001, bpdu.RoleDesignated, false,
				agreementRecord(peer, bpdu.RoleDesignated, false, false))
			l.Receive(now.Add(time.Second), "p1", b)
			if legacy {
				l.Receive(now.Add(4*time.Second), "p1", legacyConfigBPDU(t, 4096, "00:11:22:33:44:01"))
			}
			l.Receive(now.Add(4500*time.Millisecond), "p1", b)
			if info := l.PortInfo("p1"); info.Role != bpdu.RoleRoot || info.SendRSTP == legacy {
				t.Fatalf("CIST before proposal = %+v, want Root with RSTP=%t", info, !legacy)
			}
			if info := l.VLANPortInfo(10, "p1"); info.Role != bpdu.RoleRoot {
				t.Fatalf("MSTI before proposal = %+v, want Root", info)
			}
			b.MSTIs[0] = agreementRecord(peer, bpdu.RoleDesignated, true, false)
			effects := l.Receive(now.Add(5*time.Second), "p1", b)
			want := 1
			if legacy {
				want = 0
			}
			if len(effects.Emissions) != want {
				t.Fatalf("MSTI-only proposal emitted %d frames, want %d", len(effects.Emissions), want)
			}
			if !legacy {
				frame, _ := decodeTestEmission(t, effects.Emissions[0])
				if effects.Emissions[0].Port != "p1" || frame.Type != bpdu.TypeRapid || len(frame.MSTIs) != 1 || !(bpdu.BPDU{Flags: frame.MSTIs[0].Flags}).Agreement() {
					t.Fatalf("MSTI proposal answer = %+v, want p1 Rapid with MSTI agreement", effects.Emissions)
				}
			}
		})
	}
}

func TestRoleLossClearsTopologyAcknowledgment(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{Address: mustMAC(t, "00:11:22:33:44:02"), Ports: map[string]stp.Port{"p1": {}, "p2": {}}}, mustPortTable(t, "p1", "p2"))
	l.LinkChange(now, "p1", true, false, 1_000_000_000)
	l.LinkChange(now, "p2", true, false, 1_000_000_000)
	root := legacyConfigBPDU(t, 4096, "00:11:22:33:44:01")
	root.HelloTime = 60 * time.Second
	root.MaxAge = 120 * time.Second
	l.Receive(now.Add(4*time.Second), "p1", root)
	downstream := root
	downstream.RootPathCost = 100_000
	downstream.BridgeID = bpdu.BridgeID{Priority: 61440, Address: mustMAC(t, "00:11:22:33:44:03")}
	l.Receive(now.Add(4*time.Second), "p2", downstream)
	l.Advance(now.Add(15 * time.Second))
	l.Advance(now.Add(30 * time.Second))
	if info := l.PortInfo("p2"); info.Role != bpdu.RoleDesignated || info.State != stp.StateForwarding || info.SendRSTP {
		t.Fatalf("p2 before TCN = %+v, want Designated/Forwarding/STP", info)
	}
	l.Receive(now.Add(31*time.Second), "p2", bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification})
	better := downstream
	better.RootPathCost = 0
	l.Receive(now.Add(31100*time.Millisecond), "p2", better)
	if info := l.PortInfo("p2"); info.Role != bpdu.RoleAlternate {
		t.Fatalf("p2 after better information = %+v, want Alternate", info)
	}
	effects := l.Receive(now.Add(31200*time.Millisecond), "p2", downstream)
	if info := l.PortInfo("p2"); info.Role != bpdu.RoleDesignated {
		t.Fatalf("p2 after worse information = %+v, want Designated", info)
	}
	if countPortType(t, effects, "p2", bpdu.TypeConfiguration) != 1 {
		t.Fatalf("return to Designated emitted no Configuration BPDU: %+v", effects.Emissions)
	}
	for _, emission := range effects.Emissions {
		if emission.Port == "p2" && emission.Frame.Payload[7]&0x80 != 0 {
			t.Fatal("first Configuration BPDU after role loss carried an old acknowledgment")
		}
	}
}

func TestRapidAcknowledgmentDoesNotStopTopologyTimer(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{Ports: map[string]stp.Port{"p1": {}}}, mustPortTable(t, "p1"))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	root := legacyConfigBPDU(t, 4096, "00:11:22:33:44:01")
	root.Version = 2
	root.Type = bpdu.TypeRapid
	root.SetRole(bpdu.RoleDesignated)
	root.SetProposal(true)
	l.Receive(now.Add(time.Second), "p1", root)
	root.SetProposal(false)
	root.SetTopologyChangeAck(true)
	l.Receive(now.Add(1500*time.Millisecond), "p1", root)
	effects := l.Advance(now.Add(3 * time.Second))
	if len(effects.Emissions) != 1 || countPortType(t, effects, "p1", bpdu.TypeRapid) != 1 {
		t.Fatalf("next hello emissions = %+v, want one Rapid BPDU on p1", effects.Emissions)
	}
	if effects.Emissions[0].Frame.Payload[7]&1 == 0 {
		t.Fatal("rapid BPDU bit 8 stopped the Root port's topology timer")
	}
}
