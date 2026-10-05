package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

// TestAnAgreementOpensADesignatedPortOnlyWhenTheMessageQualifies drives one
// two-port bridge whose "up" port is Root towards bridge 01. Its other port
// "t" is Designated and proposing. The first row is an agreement that opens
// "t". Each other row differs from it in one property and must leave "t"
// without a forwarding transition: the link is shared, the port sends STP,
// the sender conveys a Designated role with a worse vector, or the sender
// conveys a Root role with a better one.
func TestAnAgreementOpensADesignatedPortOnlyWhenTheMessageQualifies(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	root := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:01")}
	local := bpdu.BridgeID{Priority: 32768, Address: mustMAC(t, "00:11:22:33:44:05")}

	tests := []struct {
		name   string
		shared bool
		legacy bool
		role   bpdu.Role
		better bool
		opens  bool
	}{
		{name: "root sender with a worse vector on a point-to-point RSTP link", role: bpdu.RoleRoot, opens: true},
		{name: "shared link", role: bpdu.RoleRoot, shared: true},
		{name: "port in STP mode", role: bpdu.RoleRoot, legacy: true},
		{name: "designated sender with a worse vector", role: bpdu.RoleDesignated},
		{name: "root sender with a better vector", role: bpdu.RoleRoot, better: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := mustNewSTP(t, stp.Config{
				Priority: local.Priority,
				Address:  local.Address,
				Ports:    map[string]stp.Port{"up": {}, "t": {}},
			}, mustPortTable(t, "up", "t"))

			l.LinkChange(t0, "up", true, true, 1_000_000_000)
			l.LinkChange(t0, "t", true, !tt.shared, 1_000_000_000)

			hello := bpdu.BPDU{
				Version:      2,
				Type:         bpdu.TypeRapid,
				RootID:       root,
				BridgeID:     root,
				PortID:       0x8001,
				HelloTime:    2 * time.Second,
				MaxAge:       20 * time.Second,
				ForwardDelay: 15 * time.Second,
			}
			hello.SetRole(bpdu.RoleDesignated)
			l.Receive(t0, "up", hello)
			if info := l.PortInfo("up"); info.Role != bpdu.RoleRoot {
				t.Fatalf("test setup: up role = %v, want Root", info.Role)
			}

			// Two links of 20000 each put the sender's cost at 40000 against
			// this bridge's 20000 on "t": worse. The better sender ties the
			// cost and wins on bridge identifier.
			sender := bpdu.BridgeID{Priority: 61440, Address: mustMAC(t, "00:aa:bb:cc:dd:01")}
			cost := uint32(40_000)
			if tt.better {
				sender = bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:11:22:33:44:02")}
				cost = 20_000
			}
			msg := hello
			msg.RootPathCost = cost
			msg.BridgeID = sender
			msg.PortID = 0x8002
			msg.SetRole(tt.role)

			at := t0.Add(4 * time.Second)
			if tt.legacy {
				// A Configuration BPDU after the migration delay moves the
				// port to STP, and the delay it restarts keeps the RST BPDU
				// below from moving it back.
				legacy := msg
				legacy.Version = 0
				legacy.Type = bpdu.TypeConfiguration
				legacy.Flags = 0
				l.Receive(at, "t", legacy)
				if l.PortInfo("t").SendRSTP {
					t.Fatal("test setup: t still sends RSTP after a Configuration BPDU")
				}
			}

			msg.SetAgreement(true)
			l.Receive(at, "t", msg)

			info := l.PortInfo("t")
			if tt.opens {
				if info.Role != bpdu.RoleDesignated || info.State != stp.StateForwarding {
					t.Errorf("t = %v/%v, want Designated/Forwarding", info.Role, info.State)
				}

				return
			}
			if info.State != stp.StateDiscarding || info.ForwardTransitions != 0 {
				t.Errorf("t state = %v with %d forward transitions, want Discarding with none",
					info.State, info.ForwardTransitions)
			}
			if !tt.better && info.Role != bpdu.RoleDesignated {
				t.Errorf("t role = %v, want Designated", info.Role)
			}
		})
	}
}

// regionPair builds two bridges of one MST region joined by a point-to-point
// link, with MSTI 1 carrying VLAN 10. The first is the root of every tree.
func regionPair(t *testing.T) (root, other *stp.Layer) {
	t.Helper()

	region := func() *stp.MST {
		return &stp.MST{
			Name:      "region-1",
			Revision:  1,
			Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: []vlan.ID{10}}},
		}
	}
	root = mustNewSTP(t, stp.Config{
		Priority: 4096,
		Address:  mustMAC(t, "00:11:22:33:44:01"),
		Ports:    map[string]stp.Port{"1/1/1": {}},
		MST:      region(),
	}, mustPortTable(t, "1/1/1"))
	other = mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "00:11:22:33:44:02"),
		Ports:    map[string]stp.Port{"1/1/1": {}},
		MST:      region(),
	}, mustPortTable(t, "1/1/1"))

	return root, other
}

// exchangeProposal brings both links up at now, delivers the root's proposal
// to the other bridge, and returns the other bridge's answer. No time passes.
func exchangeProposal(t *testing.T, now time.Time, root, other *stp.Layer) bpdu.BPDU {
	t.Helper()

	proposal := root.LinkChange(now, "1/1/1", true, true, 1_000_000_000)
	other.LinkChange(now, "1/1/1", true, true, 1_000_000_000)
	if len(proposal.Emissions) != 1 {
		t.Fatalf("root emissions at link up = %d, want the proposal", len(proposal.Emissions))
	}
	sent, err := bpdu.Decode(proposal.Emissions[0].Frame)
	if err != nil {
		t.Fatalf("decode proposal: %v", err)
	}

	fx := other.Receive(now, "1/1/1", sent)
	if len(fx.Emissions) != 1 {
		t.Fatalf("answer emissions = %d, want one", len(fx.Emissions))
	}
	answer, err := bpdu.Decode(fx.Emissions[0].Frame)
	if err != nil {
		t.Fatalf("decode answer: %v", err)
	}

	return answer
}

// TestAnMSTIPortOpensInTheExchangeThatOpensTheCIST is evidence that an MSTI
// Designated port runs proposal and agreement: two frames at one instant,
// with no timer firing, bring the instance's ports to Forwarding where the
// CIST's go.
func TestAnMSTIPortOpensInTheExchangeThatOpensTheCIST(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	root, other := regionPair(t)

	answer := exchangeProposal(t, now, root, other)
	root.Receive(now, "1/1/1", answer)

	for _, c := range []struct {
		name string
		l    *stp.Layer
		role bpdu.Role
	}{
		{"root", root, bpdu.RoleDesignated},
		{"other", other, bpdu.RoleRoot},
	} {
		for _, vid := range []vlan.ID{1, 10} {
			if got := c.l.VLANPortInfo(vid, "1/1/1"); got.Role != c.role || got.State != stp.StateForwarding {
				t.Errorf("%s VLAN %d = %v/%v, want %v/Forwarding", c.name, vid, got.Role, got.State, c.role)
			}
			if !c.l.Forwards("1/1/1", vid) {
				t.Errorf("%s does not forward VLAN %d", c.name, vid)
			}
		}
	}
}

// TestAnMSTIAgreementNeedsTheCISTMessageToNameTheHeldCISTVectors is evidence
// that an MSTI agreement is recorded only when the CIST message in the same
// BPDU names the CIST root, external cost, and regional root the port holds.
// The second row changes only the regional root, so the CIST still agrees and
// the instance stays Discarding.
func TestAnMSTIAgreementNeedsTheCISTMessageToNameTheHeldCISTVectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mutate   func(*bpdu.BPDU)
		wantMSTI stp.State
	}{
		{name: "the CIST message names the held regional root", mutate: func(*bpdu.BPDU) {}, wantMSTI: stp.StateForwarding},
		{
			name: "the CIST message names another regional root",
			mutate: func(b *bpdu.BPDU) {
				b.RegionalRootID = bpdu.BridgeID{Priority: 61440, Address: mustMAC(t, "00:aa:bb:cc:dd:01")}
			},
			wantMSTI: stp.StateDiscarding,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
			root, other := regionPair(t)

			answer := exchangeProposal(t, now, root, other)
			tt.mutate(&answer)
			root.Receive(now, "1/1/1", answer)

			if got := root.VLANPortInfo(1, "1/1/1"); got.State != stp.StateForwarding {
				t.Errorf("CIST state = %v, want Forwarding: only the MSTI agreement is withheld", got.State)
			}
			if got := root.VLANPortInfo(10, "1/1/1"); got.State != tt.wantMSTI {
				t.Errorf("MSTI 1 state = %v, want %v", got.State, tt.wantMSTI)
			}
		})
	}
}
