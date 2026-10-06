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

func receiveGapMST(t *testing.T) (*stp.Layer, stp.MST, time.Time) {
	t.Helper()

	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	l, region := agreementMSTBridge(t, mustMAC(t, "00:11:22:33:44:02"), map[string]stp.Port{"p1": {}, "p2": {}})
	l.LinkChange(now, "p2", true, true, 1_000_000_000)
	l.Advance(now.Add(16 * time.Second))
	l.Advance(now.Add(32 * time.Second))
	if got := l.VLANPortInfo(10, "p2"); got.State != stp.StateForwarding {
		t.Fatalf("MSTI p2 = %+v, want Forwarding", got)
	}
	return l, region, now
}

func receiveGapMSTBPDU(region stp.MST) bpdu.BPDU {
	peer := bpdu.BridgeID{Priority: 61440}
	b := agreementBPDU(region.ConfigID(), peer, 0, peer, peer, 0x8001, bpdu.RoleDesignated, false,
		agreementRecord(peer, bpdu.RoleDesignated, false, false))
	return b
}

func receiveGapMSTIFlag(b *bpdu.BPDU) {
	var flags bpdu.BPDU
	flags.SetRole(bpdu.RoleDesignated)
	flags.SetTopologyChange(true)
	b.MSTIs[0].Flags = flags.Flags
}

func receiveGapHasFID(flushes []layer.FlushTarget, port string, fid vlan.ID) bool {
	for _, target := range flushes {
		if target.Port == port && slices.Contains(target.FIDs, fid) {
			return true
		}
	}
	return false
}

func TestInactiveMSTIRecordDoesNotPropagateTopologyChange(t *testing.T) {
	l, region, now := receiveGapMST(t)
	l.LinkChange(now.Add(33*time.Second), "p1", true, true, 1_000_000_000)
	if got := l.VLANPortInfo(10, "p1"); got.State == stp.StateForwarding {
		t.Fatalf("MSTI p1 = %+v, want inactive", got)
	}
	b := receiveGapMSTBPDU(region)
	receiveGapMSTIFlag(&b)
	fx := l.Receive(now.Add(34*time.Second), "p1", b)
	if receiveGapHasFID(fx.Flush, "p2", 10) {
		t.Fatalf("inactive MSTI flag flushed VLAN 10 on p2: %+v", fx.Flush)
	}
}

func TestMSTIRecordHelloTimeHasOneSecondFloor(t *testing.T) {
	l, region, now := receiveGapMST(t)
	l.LinkChange(now.Add(33*time.Second), "p1", true, true, 1_000_000_000)
	root := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}
	b := agreementBPDU(region.ConfigID(), root, 0, root, root, 0x8001, bpdu.RoleDesignated, false,
		agreementRecord(root, bpdu.RoleDesignated, false, false))
	b.HelloTime = 0
	l.Receive(now.Add(34*time.Second), "p1", b)
	if got := l.VLANPortInfo(10, "p1"); got.Role != bpdu.RoleRoot {
		t.Fatalf("MSTI p1 after receive = %+v, want Root", got)
	}
	l.Advance(now.Add(36 * time.Second))
	if got := l.VLANPortInfo(10, "p1"); got.Role != bpdu.RoleRoot {
		t.Fatalf("MSTI p1 at two seconds = %+v, want stored Root until three seconds", got)
	}
	l.Advance(now.Add(37 * time.Second))
	if got := l.VLANPortInfo(10, "p1"); got.Role == bpdu.RoleRoot {
		t.Fatalf("MSTI p1 at three seconds = %+v, want expired Root", got)
	}
}
