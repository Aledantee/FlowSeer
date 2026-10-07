package stp

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

func TestReceiveSSTPRunsTheLinkHalfForEveryOutcome(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		arrival SSTPArrival
		want    SSTPOutcome
	}{
		{"applied", SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}, SSTPApplied},
		{"guarded", SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}, SSTPGuarded},
		{"boundary", SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}, SSTPBoundary},
		{"not admitted", SSTPArrival{ArrivalVID: 10, TLVVID: 10}, SSTPNotAdmitted},
		{"untracked", SSTPArrival{ArrivalVID: 30, TLVVID: 30, Admitted: true}, SSTPUntrackedVLAN},
		{"PVID inconsistent", SSTPArrival{ArrivalVID: 10, TLVVID: 20, Admitted: true}, SSTPPVIDInconsistent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := Config{
				Ports: map[string]Port{"p1": {AutoEdge: true, BPDUGuard: tc.want == SSTPGuarded}},
				PVST:  &PVST{Trees: map[vlan.ID]Tree{1: {}, 10: {}}},
			}
			if tc.want == SSTPBoundary {
				cfg.PVST = nil
			}
			l := newLayer(cfg.Normalize(layer.Env{}))
			l.LinkChange(now, "p1", true, true, 1_000_000_000)
			l.Advance(now.Add(migrateTime))
			if !l.links["p1"].edge || l.PortInfo("p1").State != StateForwarding {
				t.Fatal("p1 did not become a forwarding auto-edge port")
			}
			b := bpdu.BPDU{
				Version: 2, Type: bpdu.TypeRapid,
				RootID: bpdu.BridgeID{Priority: 4096}, BridgeID: bpdu.BridgeID{Priority: 4096},
				PortID: 0x8001, HelloTime: 2 * time.Second, MaxAge: 20 * time.Second, ForwardDelay: 15 * time.Second,
			}
			b.SetRole(bpdu.RoleDesignated)
			at := now.Add(4 * time.Second)

			_, outcome := l.ReceiveSSTP(at, "p1", tc.arrival, b)

			if outcome != tc.want {
				t.Fatalf("SSTP outcome = %q, want %q", outcome, tc.want)
			}
			if l.links["p1"].rxBPDUs != 1 {
				t.Errorf("received BPDUs = %d, want one", l.links["p1"].rxBPDUs)
			}
			if tc.want == SSTPGuarded {
				info := l.PortInfo("p1")
				if info.Role != bpdu.RoleDisabled || info.BlockReason != BlockReasonBPDUGuard {
					t.Errorf("guarded port = %+v, want Disabled/BPDU guard", info)
				}
				return
			}
			if l.links["p1"].edge || !l.links["p1"].edgeDelayWhile.Equal(at.Add(migrateTime)) {
				t.Errorf("link after SSTP = edge %t delay %v, want non-edge with restarted delay", l.links["p1"].edge, l.links["p1"].edgeDelayWhile)
			}
			cistP := l.cist().ports["p1"]
			if cistP.role != bpdu.RoleDesignated || cistP.state != StateDiscarding || !cistP.fwdDelayTimer.Equal(at.Add(15*time.Second)) {
				t.Errorf("CIST after SSTP = %v/%v delay %v, want Designated/Discarding with forward delay %v", cistP.role, cistP.state, cistP.fwdDelayTimer, at.Add(15*time.Second))
			}
			if l.cist().rootID != l.cist().bridgeID || l.cist().rootPort != "" {
				t.Errorf("CIST root = %v via %q, want this bridge with no root port", l.cist().rootID, l.cist().rootPort)
			}
			if tc.want == SSTPBoundary {
				if !l.PVSTBoundary("p1") {
					t.Error("boundary receive left the PVST boundary mark clear")
				}
				return
			}
			p := l.trees[treeID(10)].ports["p1"]
			wantRole, wantState := bpdu.RoleDesignated, StateDiscarding
			wantReason := BlockReason("")
			switch tc.want {
			case SSTPApplied:
				wantRole, wantState = bpdu.RoleRoot, StateForwarding
			case SSTPPVIDInconsistent:
				wantRole, wantReason = bpdu.RoleAlternate, BlockReasonPVIDInconsistent
			}
			info := l.VLANPortInfo(10, "p1")
			if info.Role != wantRole || info.State != wantState || info.BlockReason != wantReason {
				t.Errorf("VLAN 10 after SSTP = %+v, want %v/%v/%q", info, wantRole, wantState, wantReason)
			}
			if tc.want == SSTPNotAdmitted || tc.want == SSTPUntrackedVLAN {
				if !p.fwdDelayTimer.Equal(at.Add(15 * time.Second)) {
					t.Errorf("VLAN 10 forward delay = %v, want %v", p.fwdDelayTimer, at.Add(15*time.Second))
				}
			}
		})
	}
}
