package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

// unhRootTransitionBPDU carries the MakeRootPortRST and MakeAlternatePortRST
// fields from UNH-IOL RSTP Conformance, Appendix B, pages 51 and 62.
func unhRootTransitionBPDU(alternate bool) bpdu.BPDU {
	sender := byte(0xc1)
	if alternate {
		sender = 0xc2
	}
	return bpdu.BPDU{
		Version:      2,
		Type:         bpdu.TypeRapid,
		Flags:        0x3c,
		RootID:       bpdu.BridgeID{Priority: 0x7000, Address: netaddr.MAC{0, 0xbf, 0xcb, 0xfc, 0xbf, 0xc0}},
		RootPathCost: 0x00030d40,
		BridgeID:     bpdu.BridgeID{Priority: 0xf000, Address: netaddr.MAC{0, 0xbf, 0xcb, 0xfc, 0xbf, sender}},
		PortID:       0x8001,
		MessageAge:   time.Second,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
}

func TestUNHRootPortReturnsToForwardingWhenBetterInformationAgesOut(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "00:11:22:33:44:09"),
		Ports:    map[string]stp.Port{"ts1": {}, "ts2": {Priority: 64, PriorityPresent: true}, "ts3": {}},
	}, mustPortTable(t, "ts1", "ts2", "ts3"))
	for _, name := range []string{"ts1", "ts2", "ts3"} {
		l.LinkChange(now, name, true, true, 1_000_000_000)
	}
	l.Advance(now.Add(15 * time.Second))
	if got := l.PortInfo("ts3").State; got != stp.StateLearning {
		t.Fatalf("ts3 after one forward delay = %v, want Learning", got)
	}
	l.Advance(now.Add(30 * time.Second))
	if got := l.PortInfo("ts3").State; got != stp.StateForwarding {
		t.Fatalf("ts3 after the ladder = %v, want Forwarding", got)
	}
	l.Receive(now.Add(30*time.Second), "ts1", unhRootTransitionBPDU(false))
	if p := l.PortInfo("ts1"); p.Role != bpdu.RoleRoot || p.State != stp.StateForwarding {
		t.Fatalf("ts1 = %v/%v, want Root/Forwarding", p.Role, p.State)
	}
	l.Receive(now.Add(32*time.Second), "ts1", unhRootTransitionBPDU(true))
	l.Receive(now.Add(32*time.Second), "ts2", unhRootTransitionBPDU(false))
	if p := l.PortInfo("ts1"); p.Role != bpdu.RoleAlternate || p.State != stp.StateDiscarding {
		t.Fatalf("ts1 = %v/%v, want Alternate/Discarding", p.Role, p.State)
	}
	if p := l.PortInfo("ts2"); p.Role != bpdu.RoleRoot || p.State != stp.StateForwarding {
		t.Fatalf("ts2 = %v/%v, want Root/Forwarding", p.Role, p.State)
	}
	l.Receive(now.Add(34*time.Second), "ts1", unhRootTransitionBPDU(true))
	l.Advance(now.Add(36 * time.Second))
	l.Receive(now.Add(36*time.Second), "ts1", unhRootTransitionBPDU(true))
	l.Advance(now.Add(38 * time.Second))
	if p := l.PortInfo("ts1"); p.Role != bpdu.RoleRoot || p.State != stp.StateForwarding {
		t.Errorf("ts1 after six seconds of TS2 silence = %v/%v, want Root/Forwarding", p.Role, p.State)
	}
	if p := l.PortInfo("ts3"); p.Role != bpdu.RoleDesignated || p.State != stp.StateForwarding {
		t.Errorf("ts3 = %v/%v, want Designated/Forwarding", p.Role, p.State)
	}
}
