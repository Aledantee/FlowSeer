package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

func receivePVSTLayer(t *testing.T, first stp.Port) (*stp.Layer, time.Time) {
	t.Helper()

	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 4096,
		Address:  mustMAC(t, "00:11:22:33:44:02"),
		Ports:    map[string]stp.Port{"l1": first, "l2": {}},
		PVST:     pvstTrees(nil, 1, 10),
	}, mustPortTable(t, "l1", "l2"))
	l.LinkChange(now, "l2", true, false, 1_000_000_000)
	l.Advance(now.Add(16 * time.Second))
	l.Advance(now.Add(32 * time.Second))
	if got := l.VLANPortInfo(10, "l2"); got.State != stp.StateForwarding {
		t.Fatalf("VLAN 10 l2 = %+v, want Forwarding", got)
	}
	return l, now
}

func receiveSSTPTCN(l *stp.Layer, at time.Time) (stp.SSTPOutcome, bool) {
	fx, outcome := l.ReceiveSSTP(at, "l1", stp.SSTPArrival{ArrivalVID: 10, Admitted: true},
		bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification})
	return outcome, hasReceiveFlushFID(fx.Flush, "l2", vlan.ID(10))
}

func TestInactiveSSTPTCNDoesNotPropagate(t *testing.T) {
	l, now := receivePVSTLayer(t, stp.Port{})
	l.LinkChange(now.Add(33*time.Second), "l1", true, false, 1_000_000_000)
	if got := l.VLANPortInfo(10, "l1"); got.State == stp.StateForwarding {
		t.Fatalf("VLAN 10 l1 = %+v, want inactive", got)
	}
	outcome, flushed := receiveSSTPTCN(l, now.Add(34*time.Second))
	if outcome != stp.SSTPApplied || flushed {
		t.Fatalf("inactive SSTP TCN outcome=%q flushed VLAN 10=%t, want applied/false", outcome, flushed)
	}
}

func TestRestrictedSSTPTCNDoesNotPropagate(t *testing.T) {
	l, now := receivePVSTLayer(t, stp.Port{RestrictedTCN: true})
	l.LinkChange(now.Add(33*time.Second), "l1", true, false, 1_000_000_000)
	l.Advance(now.Add(49 * time.Second))
	l.Advance(now.Add(65 * time.Second))
	if got := l.VLANPortInfo(10, "l1"); got.State != stp.StateForwarding {
		t.Fatalf("VLAN 10 l1 = %+v, want active", got)
	}
	outcome, flushed := receiveSSTPTCN(l, now.Add(66*time.Second))
	if outcome != stp.SSTPApplied || flushed {
		t.Fatalf("restricted SSTP TCN outcome=%q flushed VLAN 10=%t, want applied/false", outcome, flushed)
	}
}

func TestSSTPTCNClearsOnlyArrivalTreeLoopGuard(t *testing.T) {
	l, now := receivePVSTLayer(t, stp.Port{LoopGuard: true})
	l.LinkChange(now.Add(33*time.Second), "l1", true, true, 1_000_000_000)
	root := bpdu.BridgeID{Priority: 1, Address: mustMAC(t, "00:11:22:33:44:01")}
	b := sstpConfigurationBPDU(root)
	for _, vid := range []vlan.ID{1, 10} {
		_, outcome := l.ReceiveSSTP(now.Add(34*time.Second), "l1", stp.SSTPArrival{ArrivalVID: vid, TLVVID: vid, Admitted: true}, b)
		if outcome != stp.SSTPApplied {
			t.Fatalf("VLAN %d receive = %q, want applied", vid, outcome)
		}
	}
	l.Advance(now.Add(41 * time.Second))
	for _, vid := range []vlan.ID{1, 10} {
		if got := l.VLANPortInfo(vid, "l1").BlockReason; got != stp.BlockReasonLoopInconsistent {
			t.Fatalf("VLAN %d block reason = %q, want loop guard", vid, got)
		}
	}
	outcome, _ := receiveSSTPTCN(l, now.Add(42*time.Second))
	if outcome != stp.SSTPApplied {
		t.Fatalf("SSTP TCN outcome = %q, want applied", outcome)
	}
	if got := l.VLANPortInfo(10, "l1").BlockReason; got == stp.BlockReasonLoopInconsistent {
		t.Fatalf("VLAN 10 block reason after TCN = %q, want cleared", got)
	}
	if got := l.VLANPortInfo(1, "l1").BlockReason; got != stp.BlockReasonLoopInconsistent {
		t.Fatalf("VLAN 1 block reason after VLAN 10 TCN = %q, want loop guard", got)
	}
}

func TestIEEEReceiveClearsOnlyCISTLoopGuard(t *testing.T) {
	for _, shape := range []string{"aged", "TCN"} {
		t.Run(shape, func(t *testing.T) {
			l, now := receivePVSTLayer(t, stp.Port{LoopGuard: true})
			l.LinkChange(now.Add(33*time.Second), "l1", true, true, 1_000_000_000)
			root := bpdu.BridgeID{Priority: 1, Address: mustMAC(t, "00:11:22:33:44:01")}
			b := sstpConfigurationBPDU(root)
			for _, vid := range []vlan.ID{1, 10} {
				l.ReceiveSSTP(now.Add(34*time.Second), "l1", stp.SSTPArrival{ArrivalVID: vid, TLVVID: vid, Admitted: true}, b)
			}
			l.Advance(now.Add(41 * time.Second))
			for _, vid := range []vlan.ID{1, 10} {
				if got := l.VLANPortInfo(vid, "l1").BlockReason; got != stp.BlockReasonLoopInconsistent {
					t.Fatalf("VLAN %d before IEEE receive = %q, want loop guard", vid, got)
				}
			}
			if shape == "TCN" {
				l.Receive(now.Add(42*time.Second), "l1", bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification})
			} else {
				b.MessageAge = b.MaxAge
				l.Receive(now.Add(42*time.Second), "l1", b)
			}
			if got := l.VLANPortInfo(1, "l1").BlockReason; got == stp.BlockReasonLoopInconsistent {
				t.Fatalf("CIST after %s = %q, want loop guard cleared", shape, got)
			}
			if got := l.VLANPortInfo(10, "l1").BlockReason; got != stp.BlockReasonLoopInconsistent {
				t.Fatalf("VLAN 10 after IEEE %s = %q, want loop guard held", shape, got)
			}
		})
	}
}

func TestRefusedSSTPFrameRecomputesAfterAutoEdgeLoss(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "00:11:22:33:44:02"),
		Ports:   map[string]stp.Port{"l1": {AutoEdge: true}},
		PVST:    pvstTrees(nil, 1, 10),
	}, mustPortTable(t, "l1"))
	l.LinkChange(now, "l1", true, true, 1_000_000_000)
	l.Advance(now.Add(3 * time.Second))
	if got := l.PortInfo("l1"); !got.Edge || got.State != stp.StateForwarding {
		t.Fatalf("before refusal = %+v, want edge Forwarding", got)
	}
	_, outcome := l.ReceiveSSTP(now.Add(4*time.Second), "l1", stp.SSTPArrival{ArrivalVID: 10, Admitted: false},
		sstpConfigurationBPDU(bpdu.BridgeID{Priority: 61440}))
	if outcome != stp.SSTPNotAdmitted {
		t.Fatalf("outcome = %q, want not admitted", outcome)
	}
	if got := l.PortInfo("l1"); got.Edge || got.State != stp.StateDiscarding {
		t.Fatalf("after refusal = %+v, want non-edge Discarding", got)
	}
	l.Advance(now.Add(19 * time.Second))
	l.Advance(now.Add(34 * time.Second))
	if got := l.PortInfo("l1"); got.State != stp.StateForwarding {
		t.Fatalf("after forward-delay ladder = %+v, want Forwarding", got)
	}
}
