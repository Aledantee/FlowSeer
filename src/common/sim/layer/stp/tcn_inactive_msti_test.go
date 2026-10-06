package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

func TestInactiveMSTIOnActiveCISTPortIgnoresTCN(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 6, 16, 0, 0, 0, time.UTC)
	local := mustMAC(t, "00:11:22:33:44:02")
	peer := mustMAC(t, "00:11:22:33:44:01")
	l, region := agreementMSTBridge(t, local, map[string]stp.Port{"p1": {}, "p2": {}})
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)
	l.Advance(now.Add(16 * time.Second))
	l.Advance(now.Add(32 * time.Second))

	localRoot := localBridgeID(local)
	peerRoot := bpdu.BridgeID{Priority: 4097, Address: peer}
	b := agreementBPDU(
		region.ConfigID(), localRoot, 0, localRoot,
		bpdu.BridgeID{Priority: 61440, Address: peer}, 0x8001,
		bpdu.RoleDesignated, false,
		agreementRecord(peerRoot, bpdu.RoleDesignated, false, false),
	)
	l.Receive(now.Add(33*time.Second), "p2", b)
	b.PortID = 0x8002
	l.Receive(now.Add(33*time.Second), "p1", b)
	if info := l.PortInfo("p1"); info.Role != bpdu.RoleDesignated || info.State != stp.StateForwarding {
		t.Fatalf("p1 CIST = %v/%v, want Designated/Forwarding", info.Role, info.State)
	}
	if info := l.VLANPortInfo(10, "p1"); info.Role != bpdu.RoleAlternate || info.State != stp.StateDiscarding {
		t.Fatalf("p1 MSTI = %v/%v, want Alternate/Discarding", info.Role, info.State)
	}
	if info := l.VLANPortInfo(10, "p2"); info.Role != bpdu.RoleRoot || info.State != stp.StateForwarding {
		t.Fatalf("p2 MSTI = %v/%v, want Root/Forwarding", info.Role, info.State)
	}

	l.Advance(now.Add(37 * time.Second))
	b.PortID = 0x8001
	l.Receive(now.Add(37*time.Second), "p2", b)
	b.PortID = 0x8002
	l.Receive(now.Add(37*time.Second), "p1", b)
	fx := l.Receive(now.Add(38*time.Second), "p1", bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification})
	var p2Frames int
	for _, emission := range fx.Emissions {
		if emission.Port != "p2" {
			continue
		}
		p2Frames++
		decoded, _ := decodeTestEmission(t, emission)
		var msti1Records int
		for _, record := range decoded.MSTIs {
			if record.MSTID != 1 {
				continue
			}
			msti1Records++
			if (bpdu.BPDU{Flags: record.Flags}).TopologyChange() {
				t.Error("TCN on inactive MSTI port flagged MSTI 1 on p2")
			}
		}
		if msti1Records != 1 {
			t.Errorf("TCN on inactive MSTI port emitted %d MSTI 1 records on p2, want one", msti1Records)
		}
	}
	if p2Frames != 1 {
		t.Errorf("TCN on active CIST port emitted %d p2 frames, want one", p2Frames)
	}
}
