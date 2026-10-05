package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

func agreementMSTBridge(t *testing.T, address netaddr.MAC, ports map[string]stp.Port) (*stp.Layer, stp.MST) {
	t.Helper()

	region := stp.MST{
		Name:     "region-1",
		Revision: 1,
		Instances: map[bpdu.MSTID]stp.Instance{
			1: {VLANs: []vlan.ID{10}},
		},
	}
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  address,
		Ports:    ports,
		MST:      &region,
	}, mustPortTable(t, "p1", "p2"))

	return l, region
}

func agreementBPDU(
	cid bpdu.ConfigID,
	rootID bpdu.BridgeID,
	rootPathCost uint32,
	regionalRootID bpdu.BridgeID,
	bridgeID bpdu.BridgeID,
	portID uint16,
	role bpdu.Role,
	agreement bool,
	records ...bpdu.MSTIRecord,
) bpdu.BPDU {
	b := bpdu.BPDU{
		Version:        3,
		Type:           bpdu.TypeRapid,
		RootID:         rootID,
		RootPathCost:   rootPathCost,
		RegionalRootID: regionalRootID,
		BridgeID:       bridgeID,
		PortID:         portID,
		HelloTime:      2 * time.Second,
		MaxAge:         20 * time.Second,
		ForwardDelay:   15 * time.Second,
		RemainingHops:  20,
		ConfigID:       &cid,
		MSTIs:          records,
	}
	b.SetRole(role)
	b.SetAgreement(agreement)

	return b
}

func agreementRecord(
	regionalRootID bpdu.BridgeID,
	role bpdu.Role,
	proposal bool,
	agreement bool,
) bpdu.MSTIRecord {
	var flags bpdu.BPDU
	flags.SetRole(role)
	flags.SetProposal(proposal)
	flags.SetAgreement(agreement)

	return bpdu.MSTIRecord{
		MSTID:                1,
		Flags:                flags.Flags,
		RegionalRootID:       regionalRootID,
		InternalRootPathCost: 0,
		BridgePriority:       0x80,
		PortPriority:         0x80,
		RemainingHops:        20,
	}
}

func advanceAgreementPorts(l *stp.Layer, start time.Time) {
	l.Advance(start.Add(16 * time.Second))
	l.Advance(start.Add(32 * time.Second))
}

func emittedMSTI(t *testing.T, effects layer.Effects, mstid bpdu.MSTID) (bpdu.MSTIRecord, bool) {
	t.Helper()

	for _, emission := range effects.Emissions {
		b, err := bpdu.Decode(emission.Frame)
		if err != nil {
			t.Fatalf("Decode emitted BPDU: %v", err)
		}
		for _, record := range b.MSTIs {
			if record.MSTID == mstid {
				return record, true
			}
		}
	}

	return bpdu.MSTIRecord{}, false
}

func TestMSTIProposalNeedsNoCISTMatch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	local := mustMAC(t, "00:11:22:33:44:02")
	peer := mustMAC(t, "00:11:22:33:44:01")
	peer2 := mustMAC(t, "00:aa:bb:cc:dd:ee")
	localMSTI := bpdu.BridgeID{Priority: 32769, Address: local}
	peerRoot := bpdu.BridgeID{Priority: 4096, Address: peer}
	peerMSTIRoot := bpdu.BridgeID{Priority: 4097, Address: peer}

	t.Run("CIST is stored from another root", func(t *testing.T) {
		t.Parallel()

		l, region := agreementMSTBridge(t, local, map[string]stp.Port{"p1": {}, "p2": {}})
		l.LinkChange(now, "p1", true, true, 1_000_000_000)
		l.LinkChange(now, "p2", true, true, 1_000_000_000)
		advanceAgreementPorts(l, now)

		b := agreementBPDU(
			region.ConfigID(), peerRoot, 0, peerRoot,
			bpdu.BridgeID{Priority: 61440, Address: peer2}, 0x8001,
			bpdu.RoleRoot, false,
			agreementRecord(peerMSTIRoot, bpdu.RoleDesignated, true, false),
		)
		fx := l.Receive(now.Add(33*time.Second), "p1", b)

		if info := l.PortInfo("p1"); info.Role != bpdu.RoleRoot || info.State != stp.StateForwarding {
			t.Errorf("p1 CIST = (role %v, state %v), want Root Forwarding", info.Role, info.State)
		}
		if got := l.VLANPortInfo(10, "p2").State; got != stp.StateDiscarding {
			t.Errorf("p2 MSTI state = %v, want Discarding after the proposal sync", got)
		}
		record, ok := emittedMSTI(t, fx, 1)
		if !ok || !(bpdu.BPDU{Flags: record.Flags}).Agreement() {
			t.Errorf("emitted MSTI 1 record = %+v, want an agreement", record)
		}
	})

	t.Run("CIST is held from a better root", func(t *testing.T) {
		t.Parallel()

		l, region := agreementMSTBridge(t, local, map[string]stp.Port{"p1": {}, "p2": {}})
		l.LinkChange(now, "p1", true, true, 1_000_000_000)
		l.LinkChange(now, "p2", true, true, 1_000_000_000)
		advanceAgreementPorts(l, now)

		heldRoot := bpdu.BridgeID{Priority: 8192, Address: peer}
		held := agreementBPDU(
			region.ConfigID(), peerRoot, 0, peerRoot,
			bpdu.BridgeID{Priority: 61440, Address: peer}, 0x8001,
			bpdu.RoleDesignated, false,
			agreementRecord(localMSTI, bpdu.RoleDesignated, false, false),
		)
		l.Receive(now.Add(33*time.Second), "p1", held)

		b := agreementBPDU(
			region.ConfigID(), heldRoot, 0, heldRoot,
			bpdu.BridgeID{Priority: 61440, Address: peer2}, 0x8001,
			bpdu.RoleDesignated, false,
			agreementRecord(peerMSTIRoot, bpdu.RoleDesignated, true, false),
		)
		fx := l.Receive(now.Add(34*time.Second), "p1", b)

		if info := l.PortInfo("p1"); info.Role != bpdu.RoleRoot || info.State != stp.StateForwarding {
			t.Errorf("p1 CIST = (role %v, state %v), want Root Forwarding", info.Role, info.State)
		}
		if got := l.VLANPortInfo(10, "p2").State; got != stp.StateDiscarding {
			t.Errorf("p2 MSTI state = %v, want Discarding after the proposal sync", got)
		}
		record, ok := emittedMSTI(t, fx, 1)
		if !ok || !(bpdu.BPDU{Flags: record.Flags}).Agreement() {
			t.Errorf("emitted MSTI 1 record = %+v, want an agreement", record)
		}
	})
}

func TestMSTIAgreementIsJudgedAfterTheCISTIsStored(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 5, 13, 0, 0, 0, time.UTC)
	local := mustMAC(t, "00:11:22:33:44:02")
	peer := mustMAC(t, "00:11:22:33:44:01")
	peer2 := mustMAC(t, "00:aa:bb:cc:dd:ee")
	localMSTIRoot := bpdu.BridgeID{Priority: 32769, Address: local}
	peerRoot := bpdu.BridgeID{Priority: 4096, Address: peer}

	t.Run("stored CIST agreement forwards a designated MSTI", func(t *testing.T) {
		t.Parallel()

		l, region := agreementMSTBridge(t, local, map[string]stp.Port{"p1": {}})
		l.LinkChange(now, "p1", true, true, 1_000_000_000)
		b := agreementBPDU(
			region.ConfigID(), peerRoot, 0, peerRoot,
			bpdu.BridgeID{Priority: 61440, Address: peer2}, 0x8001,
			bpdu.RoleRoot, false,
			agreementRecord(localMSTIRoot, bpdu.RoleRoot, false, true),
		)
		if got := l.Receive(now, "p1", b); len(got.Emissions) == 0 {
			t.Log("the received agreement did not require a response")
		}

		if info := l.VLANPortInfo(10, "p1"); info.State != stp.StateForwarding {
			t.Errorf("MSTI port = (role %v, state %v), want Designated Forwarding", info.Role, info.State)
		}
	})

	t.Run("a root mismatch is not an agreement", func(t *testing.T) {
		assertMSTIAgreementRefusedForCISTDifference(t, now, local, peer, peer2, func(base bpdu.BPDU) bpdu.BPDU {
			base.RootID = bpdu.BridgeID{Priority: 8192, Address: peer2}
			return base
		})
	})

	t.Run("an external cost mismatch is not an agreement", func(t *testing.T) {
		assertMSTIAgreementRefusedForCISTDifference(t, now, local, peer, peer2, func(base bpdu.BPDU) bpdu.BPDU {
			base.RootPathCost = 10
			return base
		})
	})

	t.Run("a regional root mismatch is not an agreement", func(t *testing.T) {
		assertMSTIAgreementRefusedForCISTDifference(t, now, local, peer, peer2, func(base bpdu.BPDU) bpdu.BPDU {
			base.RegionalRootID = bpdu.BridgeID{Priority: 8192, Address: peer2}
			return base
		})
	})

	t.Run("an inferior MSTI record can still agree", func(t *testing.T) {
		t.Parallel()

		l, region := agreementMSTBridge(t, local, map[string]stp.Port{"p1": {}})
		l.LinkChange(now, "p1", true, true, 1_000_000_000)
		base := agreementBPDU(
			region.ConfigID(), peerRoot, 0, peerRoot,
			bpdu.BridgeID{Priority: 61440, Address: peer2}, 0x8001,
			bpdu.RoleRoot, false,
			agreementRecord(localMSTIRoot, bpdu.RoleRoot, false, false),
		)
		l.Receive(now, "p1", base)

		base.SetAgreement(true)
		base.MSTIs[0].Flags = agreementRecord(localMSTIRoot, bpdu.RoleRoot, false, true).Flags
		l.Receive(now.Add(time.Second), "p1", base)

		if got := l.VLANPortInfo(10, "p1").State; got != stp.StateForwarding {
			t.Errorf("MSTI port state after an inferior-record agreement = %v, want Forwarding", got)
		}
	})
}

func assertMSTIAgreementRefusedForCISTDifference(
	t *testing.T,
	now time.Time,
	local, peer, peer2 netaddr.MAC,
	mutate func(bpdu.BPDU) bpdu.BPDU,
) {
	t.Helper()

	l, region := agreementMSTBridge(t, local, map[string]stp.Port{"p1": {}})
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	base := agreementBPDU(
		region.ConfigID(), bpdu.BridgeID{Priority: 4096, Address: peer}, 0,
		bpdu.BridgeID{Priority: 4096, Address: peer},
		bpdu.BridgeID{Priority: 61440, Address: peer}, 0x8001,
		bpdu.RoleDesignated, false,
	)
	l.Receive(now, "p1", base)

	changed := mutate(agreementBPDU(
		region.ConfigID(), base.RootID, base.RootPathCost, base.RegionalRootID,
		bpdu.BridgeID{Priority: 61440, Address: peer2}, 0x8001,
		bpdu.RoleRoot, false,
		agreementRecord(bpdu.BridgeID{Priority: 32769, Address: local}, bpdu.RoleRoot, false, true),
	))
	l.Receive(now.Add(time.Second), "p1", changed)

	if got := l.VLANPortInfo(10, "p1").State; got != stp.StateDiscarding {
		t.Errorf("MSTI port state after a mismatched CIST agreement = %v, want Discarding", got)
	}
}

func TestAgreementWaitsForRoleSelection(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	local := mustMAC(t, "00:11:22:33:44:02")
	peer := mustMAC(t, "00:11:22:33:44:01")
	peer2 := mustMAC(t, "00:aa:bb:cc:dd:ee")

	t.Run("a better designated sender becomes root before state changes", func(t *testing.T) {
		t.Parallel()

		l := mustNewSTP(t, stp.Config{
			Priority: 32768,
			Address:  local,
			Ports:    map[string]stp.Port{"p1": {}, "p2": {}},
			MST: &stp.MST{
				Name:      "region-1",
				Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: []vlan.ID{10}}},
			},
		}, mustPortTable(t, "p1", "p2"))
		l.LinkChange(now, "p1", true, true, 1_000_000_000)
		l.LinkChange(now, "p2", true, true, 1_000_000_000)
		advanceAgreementPorts(l, now)
		l.LinkChange(now.Add(33*time.Second), "p1", false, true, 1_000_000_000)
		l.LinkChange(now.Add(34*time.Second), "p1", true, true, 1_000_000_000)

		beforePort := l.PortInfo("p1")
		beforeChanges, _ := l.TopologyChanges()
		b := bpdu.BPDU{
			Version:      2,
			Type:         bpdu.TypeRapid,
			RootID:       bpdu.BridgeID{Priority: 4096, Address: peer},
			BridgeID:     bpdu.BridgeID{Priority: 61440, Address: peer2},
			PortID:       0x8001,
			HelloTime:    2 * time.Second,
			MaxAge:       20 * time.Second,
			ForwardDelay: 15 * time.Second,
		}
		b.SetRole(bpdu.RoleDesignated)
		b.SetAgreement(true)

		fx := l.Receive(now.Add(35*time.Second), "p1", b)
		afterPort := l.PortInfo("p1")
		if afterPort.Role != bpdu.RoleRoot || afterPort.State != stp.StateDiscarding {
			t.Errorf("p1 after agreement = (role %v, state %v), want Root Discarding", afterPort.Role, afterPort.State)
		}
		if afterPort.ForwardTransitions != beforePort.ForwardTransitions {
			t.Errorf("p1 forward transitions = %d, want unchanged at %d", afterPort.ForwardTransitions, beforePort.ForwardTransitions)
		}
		afterChanges, _ := l.TopologyChanges()
		if afterChanges != beforeChanges {
			t.Errorf("topology changes = %d, want unchanged at %d", afterChanges, beforeChanges)
		}
		if len(fx.Flush) != 0 {
			t.Errorf("Flush = %v, want empty", fx.Flush)
		}
		for _, emission := range fx.Emissions {
			decoded, err := bpdu.Decode(emission.Frame)
			if err != nil {
				t.Fatalf("Decode emission: %v", err)
			}
			if decoded.TopologyChange() {
				t.Errorf("emission on %s carries topology change", emission.Port)
			}
		}
		if got := l.VLANPortInfo(10, "p1").State; got != stp.StateDiscarding {
			t.Errorf("MSTI p1 state = %v, want Discarding", got)
		}
	})

	t.Run("a boundary agreement follows the CIST state", func(t *testing.T) {
		t.Parallel()

		l, _ := agreementMSTBridge(t, local, map[string]stp.Port{"p1": {}})
		l.LinkChange(now, "p1", true, true, 1_000_000_000)
		foreign := stp.MST{Name: "foreign", Revision: 1}
		foreignID := foreign.ConfigID()
		boundary := agreementBPDU(
			foreignID, localBridgeID(local), 0, localBridgeID(local),
			bpdu.BridgeID{Priority: 61440, Address: peer}, 0x8001,
			bpdu.RoleDesignated, false,
		)
		l.Receive(now, "p1", boundary)
		if got := l.PortInfo("p1").Role; got != bpdu.RoleDesignated {
			t.Fatalf("boundary CIST role = %v, want Designated", got)
		}

		b := agreementBPDU(
			foreignID, localBridgeID(local), 0, localBridgeID(local),
			bpdu.BridgeID{Priority: 61440, Address: peer2}, 0x8001,
			bpdu.RoleRoot, true,
		)
		l.Receive(now.Add(time.Second), "p1", b)
		if got := l.PortInfo("p1").State; got != stp.StateForwarding {
			t.Errorf("boundary CIST state = %v, want Forwarding", got)
		}
		if got := l.VLANPortInfo(10, "p1").State; got != stp.StateForwarding {
			t.Errorf("boundary MSTI state = %v, want Forwarding", got)
		}
	})
}

func localBridgeID(address netaddr.MAC) bpdu.BridgeID {
	return bpdu.BridgeID{Priority: 32768, Address: address}
}

func TestMSTISyncLeavesABoundaryPort(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC)
	local := mustMAC(t, "00:11:22:33:44:02")
	peer := mustMAC(t, "00:11:22:33:44:01")
	peer2 := mustMAC(t, "00:aa:bb:cc:dd:ee")
	l, region := agreementMSTBridge(t, local, map[string]stp.Port{"p1": {}, "p2": {}})
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)

	foreign := stp.MST{Name: "foreign", Revision: 1}
	foreignID := foreign.ConfigID()
	localRoot := localBridgeID(local)
	internal := agreementBPDU(
		region.ConfigID(), bpdu.BridgeID{Priority: 4096, Address: peer}, 0,
		bpdu.BridgeID{Priority: 4096, Address: peer},
		bpdu.BridgeID{Priority: 4096, Address: peer}, 0x8001,
		bpdu.RoleRoot, false,
		agreementRecord(bpdu.BridgeID{Priority: 4097, Address: peer}, bpdu.RoleRoot, false, false),
	)
	l.Receive(now, "p1", internal)

	boundary := agreementBPDU(
		foreignID, localRoot, 0, localRoot,
		bpdu.BridgeID{Priority: 61440, Address: peer2}, 0x8002,
		bpdu.RoleDesignated, false,
	)
	l.Receive(now, "p2", boundary)
	l.Receive(now.Add(15*time.Second), "p1", internal)
	l.Receive(now.Add(15*time.Second), "p2", boundary)
	l.Advance(now.Add(16 * time.Second))
	l.Receive(now.Add(31*time.Second), "p1", internal)
	l.Receive(now.Add(31*time.Second), "p2", boundary)
	advanceAgreementPorts(l, now)
	l.Receive(now.Add(34*time.Second), "p1", internal)
	l.Receive(now.Add(34*time.Second), "p2", boundary)
	l.Advance(now.Add(36 * time.Second))

	if got := l.PortInfo("p1"); got.Role != bpdu.RoleRoot || got.State != stp.StateForwarding {
		t.Fatalf("p1 CIST = (role %v, state %v), want Root Forwarding", got.Role, got.State)
	}
	if got := l.VLANPortInfo(10, "p2"); got.State != stp.StateForwarding {
		t.Fatalf("p2 MSTI before sync = %v, want Forwarding", got.State)
	}

	repeat := agreementBPDU(
		region.ConfigID(), bpdu.BridgeID{Priority: 4096, Address: peer}, 0,
		bpdu.BridgeID{Priority: 4096, Address: peer},
		bpdu.BridgeID{Priority: 4096, Address: peer}, 0x8001,
		bpdu.RoleRoot, false,
		agreementRecord(bpdu.BridgeID{Priority: 4097, Address: peer}, bpdu.RoleDesignated, true, false),
	)
	fx := l.Receive(now.Add(37*time.Second), "p1", repeat)
	if got := l.VLANPortInfo(10, "p2").State; got != stp.StateForwarding {
		t.Errorf("p2 MSTI after proposal sync = %v, want Forwarding", got)
	}
	if _, ok := flushTarget(fx.Flush, "p2"); ok {
		t.Errorf("Flush = %v, want no target for boundary p2", fx.Flush)
	}
	for _, emission := range fx.Emissions {
		decoded, err := bpdu.Decode(emission.Frame)
		if err != nil {
			t.Fatalf("Decode emission: %v", err)
		}
		for _, record := range decoded.MSTIs {
			if record.MSTID == 1 && (bpdu.BPDU{Flags: record.Flags}).TopologyChange() {
				t.Errorf("emitted MSTI 1 record carries topology change")
			}
		}
	}

	next := l.Advance(now.Add(38 * time.Second))
	if got := l.VLANPortInfo(10, "p2").State; got != stp.StateForwarding {
		t.Errorf("p2 MSTI after next Advance = %v, want Forwarding", got)
	}
	if _, ok := flushTarget(next.Flush, "p2"); ok {
		t.Errorf("next Advance Flush = %v, want no target for boundary p2", next.Flush)
	}

	cistProposal := repeat
	cistProposal.SetRole(bpdu.RoleDesignated)
	cistProposal.SetProposal(true)
	l.Receive(now.Add(39*time.Second), "p1", cistProposal)
	if got := l.PortInfo("p2").State; got != stp.StateDiscarding {
		t.Errorf("p2 CIST state after a boundary CIST proposal = %v, want Discarding", got)
	}
	if got := l.VLANPortInfo(10, "p2").State; got != stp.StateDiscarding {
		t.Errorf("p2 MSTI state after a boundary CIST proposal = %v, want Discarding", got)
	}
}
