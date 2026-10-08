package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

// mstiProposalRecord builds an MSTI 1 record from the bridge with the given
// priority byte that names root as the regional root.
func mstiProposalRecord(root bpdu.BridgeID, cost uint32, bridgePriority uint8, hops uint8, proposal bool) bpdu.MSTIRecord {
	rec := agreementRecord(root, bpdu.RoleDesignated, proposal, false)
	rec.InternalRootPathCost = cost
	rec.BridgePriority = bridgePriority
	rec.RemainingHops = hops

	return rec
}

// mstiProposalBPDU builds the internal BPDU a peer sends when its CIST
// vector is the one from, with the record it carries for MSTI 1.
func mstiProposalBPDU(
	t *testing.T,
	region stp.MST,
	root bpdu.BridgeID,
	from bpdu.BridgeID,
	internalCost uint32,
	rec bpdu.MSTIRecord,
) bpdu.BPDU {
	t.Helper()

	b := agreementBPDU(region.ConfigID(), root, 0, root, from, 0x8001, bpdu.RoleDesignated, false, rec)
	b.InternalRootPathCost = internalCost

	return b
}

// TestMSTIProposalInUnstoredRecordStartsNoHandshake pins that a proposal in an
// MSTI record the port does not store is not answered. The assertion is on the
// frames of the call: a port's state alone would also hold if the proposal
// were answered and found no Designated port to cut.
func TestMSTIProposalInUnstoredRecordStartsNoHandshake(t *testing.T) {
	t.Parallel()

	local := mustMAC(t, "00:11:22:33:44:02")
	rootID := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}
	storedID := bpdu.BridgeID{Priority: 8192, Address: mustMAC(t, "00:11:22:33:44:03")}
	otherID := bpdu.BridgeID{Priority: 61440, Address: mustMAC(t, "00:11:22:33:44:04")}
	regionalRoot := bpdu.BridgeID{Priority: 4097, Address: rootID.Address}

	tests := []struct {
		name string
		// from names the sender of the proposal, rec the record it carries.
		from bpdu.BridgeID
		rec  bpdu.MSTIRecord
	}{
		{
			name: "worse record from another bridge",
			from: otherID,
			rec:  mstiProposalRecord(regionalRoot, 20000, 0xF0, 20, true),
		},
		{
			name: "stored source repeats with one remaining hop",
			from: storedID,
			rec:  mstiProposalRecord(regionalRoot, 20000, 0x20, 1, true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
			l, region := agreementMSTBridge(t, local, map[string]stp.Port{"p1": {}, "p2": {}})
			l.LinkChange(now, "p1", true, true, 1_000_000_000)
			l.LinkChange(now, "p2", true, true, 1_000_000_000)

			l.Receive(now, "p1", mstiProposalBPDU(t, region, rootID, rootID, 0,
				mstiProposalRecord(regionalRoot, 0, 0x10, 20, false)))
			l.Receive(now, "p2", mstiProposalBPDU(t, region, rootID, storedID, 20000,
				mstiProposalRecord(regionalRoot, 20000, 0x20, 20, false)))

			if got := l.PortInfo("p2"); got.Role != bpdu.RoleAlternate {
				t.Fatalf("p2 CIST role = %v, want Alternate", got.Role)
			}
			if got := l.VLANPortInfo(10, "p2"); got.Role != bpdu.RoleAlternate {
				t.Fatalf("p2 MSTI 1 role = %v, want Alternate", got.Role)
			}
			if got := l.VLANPortInfo(10, "p1"); got.Role != bpdu.RoleRoot {
				t.Fatalf("p1 MSTI 1 role = %v, want Root", got.Role)
			}
			type roleState struct {
				role  bpdu.Role
				state stp.State
			}
			snapshot := func(name string) roleState {
				info := l.VLANPortInfo(10, name)

				return roleState{role: info.Role, state: info.State}
			}
			before := map[string]roleState{"p1": snapshot("p1"), "p2": snapshot("p2")}

			fx := l.Receive(now.Add(time.Second), "p2", mstiProposalBPDU(t, region, rootID, tt.from, 20000, tt.rec))

			for _, em := range fx.Emissions {
				if em.Port == "p2" {
					t.Errorf("frame on p2 after an unstored MSTI 1 proposal, want none: %+v", em)
				}
			}
			for name, want := range before {
				if got := snapshot(name); got != want {
					t.Errorf("MSTI 1 %s = %+v, want %+v", name, got, want)
				}
			}
		})
	}
}
