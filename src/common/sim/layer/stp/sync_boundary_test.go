package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

func boundarySyncFixture(t *testing.T) (*stp.Layer, bpdu.BPDU, bpdu.BPDU, time.Time) {
	t.Helper()

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
		t.Fatalf("p1 CIST = %+v, want Root Forwarding", got)
	}
	if got := l.PortInfo("p2"); got.Role != bpdu.RoleDesignated || got.State != stp.StateForwarding {
		t.Fatalf("p2 CIST = %+v, want Designated Forwarding", got)
	}
	if got := l.VLANPortInfo(10, "p2"); got.Role != bpdu.RoleDesignated || got.State != stp.StateForwarding {
		t.Fatalf("p2 MSTI = %+v, want Designated Forwarding", got)
	}

	return l, internal, boundary, now
}

func TestMSTISyncLeavesABoundaryPort(t *testing.T) {
	t.Parallel()

	l, internal, boundary, now := boundarySyncFixture(t)
	repeat := internal
	repeat.MSTIs = []bpdu.MSTIRecord{agreementRecord(
		internal.MSTIs[0].RegionalRootID, bpdu.RoleDesignated, true, false,
	)}
	fx := l.Receive(now.Add(37*time.Second), "p1", repeat)
	record, ok := boundarySyncRecord(t, fx, "p1")
	if !ok || !(bpdu.BPDU{Flags: record.Flags}).Agreement() {
		t.Errorf("MSTI 1 response with unagreed boundary = %+v, want Agreement set", record)
	}
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

	boundaryAgreement := boundary
	boundaryAgreement.SetRole(bpdu.RoleRoot)
	boundaryAgreement.SetAgreement(true)
	l.Receive(now.Add(38*time.Second), "p2", boundaryAgreement)
	agreed := l.Receive(now.Add(39*time.Second), "p1", repeat)
	record, ok = boundarySyncRecord(t, agreed, "p1")
	if !ok || !(bpdu.BPDU{Flags: record.Flags}).Agreement() {
		t.Errorf("MSTI 1 response with CIST-agreed boundary = %+v, want Agreement set", record)
	}

	cistProposal := repeat
	cistProposal.SetRole(bpdu.RoleDesignated)
	cistProposal.SetProposal(true)
	cistProposal.RootPathCost = 20_000
	l.Receive(now.Add(40*time.Second), "p1", cistProposal)
	if got := l.PortInfo("p2").State; got != stp.StateDiscarding {
		t.Errorf("p2 CIST state after a boundary CIST proposal = %v, want Discarding", got)
	}
	if got := l.VLANPortInfo(10, "p2").State; got != stp.StateDiscarding {
		t.Errorf("p2 MSTI state after a boundary CIST proposal = %v, want Discarding", got)
	}
}

func boundarySyncRecord(t *testing.T, fx layer.Effects, port string) (bpdu.MSTIRecord, bool) {
	t.Helper()

	for _, emission := range fx.Emissions {
		if emission.Port == port {
			return emittedMSTI(t, layer.Effects{Emissions: []layer.Emission{emission}})
		}
	}

	return bpdu.MSTIRecord{}, false
}

func betterBoundarySyncProposal(internal bpdu.BPDU) bpdu.BPDU {
	internal.MSTIs = []bpdu.MSTIRecord{agreementRecord(
		bpdu.BridgeID{Priority: 1, Address: internal.RootID.Address}, bpdu.RoleDesignated, true, false,
	)}
	return internal
}

func TestMSTISyncCutsBoundaryUntilItsLadderEnds(t *testing.T) {
	t.Parallel()

	l, internal, boundary, now := boundarySyncFixture(t)
	proposal := betterBoundarySyncProposal(internal)
	at := now.Add(37 * time.Second)
	fx := l.Receive(at, "p1", proposal)
	if got := l.VLANPortInfo(10, "p2").State; got != stp.StateDiscarding {
		t.Fatalf("MSTI boundary after sync = %v, want Discarding", got)
	}
	if got := l.PortInfo("p2").State; got != stp.StateForwarding {
		t.Fatalf("CIST boundary after MSTI sync = %v, want Forwarding", got)
	}
	record, ok := boundarySyncRecord(t, fx, "p1")
	if !ok || !(bpdu.BPDU{Flags: record.Flags}).Agreement() {
		t.Errorf("p1 MSTI response = %+v, want Agreement", record)
	}
	// Refresh both peers so information expiry cannot finish the cut.
	flags := bpdu.BPDU{Flags: proposal.MSTIs[0].Flags}
	flags.SetProposal(false)
	proposal.MSTIs[0].Flags = flags.Flags
	for step, want := range []stp.State{stp.StateLearning, stp.StateForwarding} {
		for second := 1; second < 15; second += 3 {
			refresh := at.Add(time.Duration(step*15+second) * time.Second)
			l.Receive(refresh, "p1", proposal)
			l.Receive(refresh, "p2", boundary)
		}
		l.Advance(at.Add(time.Duration((step+1)*15) * time.Second))
		if got := l.VLANPortInfo(10, "p2").State; got != want {
			t.Errorf("MSTI boundary ladder step %d = %v, want %v", step+1, got, want)
		}
		if got := l.PortInfo("p2").State; got != stp.StateForwarding {
			t.Errorf("CIST boundary ladder step %d = %v, want Forwarding", step+1, got)
		}
	}
}

func TestMSTIBoundaryAgreementEndsItsSyncCut(t *testing.T) {
	t.Parallel()

	l, internal, boundary, now := boundarySyncFixture(t)
	l.Receive(now.Add(37*time.Second), "p1", betterBoundarySyncProposal(internal))
	if got := l.VLANPortInfo(10, "p2").State; got != stp.StateDiscarding {
		t.Fatalf("MSTI boundary before agreement = %v, want Discarding", got)
	}
	boundary.SetRole(bpdu.RoleRoot)
	boundary.SetAgreement(true)
	l.Receive(now.Add(38*time.Second), "p2", boundary)
	if got := l.VLANPortInfo(10, "p2").State; got != stp.StateForwarding {
		t.Errorf("MSTI boundary after foreign agreement = %v, want Forwarding in Receive", got)
	}
}

func TestMSTISyncLeavesBoundaryRootUnchanged(t *testing.T) {
	t.Parallel()

	l, internal, boundary, now := boundarySyncFixture(t)
	boundary.RootID.Priority = 0
	boundary.RegionalRootID = boundary.RootID
	boundary.SetRole(bpdu.RoleDesignated)
	l.Receive(now.Add(37*time.Second), "p2", boundary)
	before := l.VLANPortInfo(10, "p2")
	if before.Role != bpdu.RoleRoot || before.State != stp.StateForwarding {
		t.Fatalf("boundary MSTI before sync = %+v, want Root Forwarding", before)
	}
	internal.RootID = boundary.RootID
	internal.RootPathCost = 40_000
	internal.SetRole(bpdu.RoleDesignated)
	l.Receive(now.Add(38*time.Second), "p1", betterBoundarySyncProposal(internal))
	if got := l.VLANPortInfo(10, "p1").Role; got != bpdu.RoleRoot {
		t.Fatalf("internal MSTI proposal port role = %v, want Root", got)
	}
	if got := l.PortInfo("p2").Role; got != bpdu.RoleRoot {
		t.Fatalf("boundary CIST role after sync = %v, want Root", got)
	}
	if got := l.VLANPortInfo(10, "p2"); got.Role != before.Role || got.State != before.State {
		t.Errorf("boundary MSTI after sync = %+v, want role %v state %v", got, before.Role, before.State)
	}
}

func TestCutMSTIBoundaryTakesTheCISTRootState(t *testing.T) {
	t.Parallel()
	l, internal, boundary, now := boundarySyncFixture(t)
	l.Receive(now.Add(37*time.Second), "p1", betterBoundarySyncProposal(internal))
	if got := l.VLANPortInfo(10, "p2").State; got != stp.StateDiscarding {
		t.Fatalf("MSTI p2 before selection = %v, want Discarding", got)
	}
	boundary.RootID.Priority = 0
	boundary.RegionalRootID = boundary.RootID
	l.Receive(now.Add(38*time.Second), "p2", boundary)
	if got := l.PortInfo("p2"); got.Role != bpdu.RoleRoot || got.State != stp.StateForwarding {
		t.Fatalf("CIST p2 = %+v, want Root Forwarding", got)
	}
	if got := l.VLANPortInfo(10, "p2"); got.Role != bpdu.RoleRoot || got.State != stp.StateForwarding {
		t.Errorf("MSTI p2 = %+v, want Root Forwarding with CIST", got)
	}
}

func TestMSTIBoundaryReentryReadsTheCISTState(t *testing.T) {
	t.Parallel()
	l, internal, boundary, now := boundarySyncFixture(t)
	l.Receive(now.Add(37*time.Second), "p1", betterBoundarySyncProposal(internal))
	if got := l.VLANPortInfo(10, "p2").State; got != stp.StateDiscarding {
		t.Fatalf("MSTI p2 before internal receive = %v, want Discarding", got)
	}
	inside := boundary
	inside.ConfigID = internal.ConfigID
	inside.RemainingHops = 20
	l.Receive(now.Add(38*time.Second), "p2", inside)
	if got := l.PortInfo("p2"); got.State != stp.StateForwarding {
		t.Fatalf("internal CIST p2 = %+v, want internal Forwarding", got)
	}
	l.Receive(now.Add(39*time.Second), "p2", boundary)
	if got := l.PortInfo("p2"); got.State != stp.StateForwarding {
		t.Fatalf("boundary CIST p2 = %+v, want boundary Forwarding", got)
	}
	if got := l.VLANPortInfo(10, "p2").State; got != stp.StateForwarding {
		t.Errorf("MSTI p2 after boundary reentry = %v, want Forwarding with CIST", got)
	}
}
