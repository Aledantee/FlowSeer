package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

func TestRootRoleProposalDoesNotSyncCIST(t *testing.T) {
	l, now := guardLayer(t, map[string]stp.Port{"1/1/1": {}, "1/1/2": {}})
	b := superiorBPDU(0, 120*time.Second)
	b.HelloTime = 60 * time.Second
	l.Receive(now.Add(time.Second), "1/1/1", b)
	l.Advance(now.Add(16 * time.Second))
	l.Advance(now.Add(32 * time.Second))
	if got := l.PortInfo("1/1/2"); got.State != stp.StateForwarding {
		t.Fatalf("p2 before proposal = %+v, want Forwarding", got)
	}
	b.SetRole(bpdu.RoleRoot)
	b.SetProposal(true)
	l.Receive(now.Add(33*time.Second), "1/1/1", b)
	if got := l.PortInfo("1/1/2"); got.State != stp.StateForwarding {
		t.Fatalf("p2 after Root-role proposal = %+v, want Forwarding", got)
	}
}

func TestRootRoleMSTIProposalDoesNotSyncInstance(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	l, region := agreementMSTBridge(t, mustMAC(t, "00:11:22:33:44:02"), map[string]stp.Port{"p1": {}, "p2": {}})
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)
	root := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}
	b := agreementBPDU(region.ConfigID(), root, 0, root, root, 0x8001, bpdu.RoleDesignated, false,
		agreementRecord(root, bpdu.RoleDesignated, false, false))
	b.HelloTime = 60 * time.Second
	l.Receive(now.Add(time.Second), "p1", b)
	l.Advance(now.Add(16 * time.Second))
	l.Advance(now.Add(32 * time.Second))
	if got := l.VLANPortInfo(10, "p2"); got.State != stp.StateForwarding {
		t.Fatalf("MSTI p2 before proposal = %+v, want Forwarding", got)
	}
	b.MSTIs[0] = agreementRecord(root, bpdu.RoleRoot, true, false)
	l.Receive(now.Add(33*time.Second), "p1", b)
	if got := l.VLANPortInfo(10, "p2"); got.State != stp.StateForwarding {
		t.Fatalf("MSTI p2 after Root-role proposal = %+v, want Forwarding", got)
	}
}
