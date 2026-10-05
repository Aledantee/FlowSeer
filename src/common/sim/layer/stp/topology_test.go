package stp_test

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

// tcBridge is a bridge that is not the root, with one port of each kind that
// topology change treats differently. "up" hears the root and is the Root
// port, "alt" hears a better bridge on a second path and is Alternate, "down"
// is a Designated port that stays Discarding until its peer agrees, and
// "edge" is an administrative edge port. The root and the second-path bridge
// are replayed on every step, so the stored information never ages out.
type tcBridge struct {
	t   *testing.T
	l   *stp.Layer
	now time.Time
}

func tcRootID() bpdu.BridgeID {
	return bpdu.BridgeID{Priority: 4096, Address: [6]byte{2, 0, 0, 0, 0, 1}}
}

// tcRootBPDU is the root's hello as the Root port hears it.
func tcRootBPDU() bpdu.BPDU {
	b := bpdu.BPDU{
		Version:      2,
		Type:         bpdu.TypeRapid,
		RootID:       tcRootID(),
		BridgeID:     tcRootID(),
		PortID:       0x8001,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	b.SetRole(bpdu.RoleDesignated)

	return b
}

// tcSecondPathBPDU is a hello from a bridge one hop from the root whose
// identifier beats the test bridge's, so its port is Alternate.
func tcSecondPathBPDU() bpdu.BPDU {
	b := tcRootBPDU()
	b.RootPathCost = 20_000
	b.BridgeID = bpdu.BridgeID{Priority: 8192, Address: [6]byte{2, 0, 0, 0, 0, 2}}
	b.PortID = 0x8002

	return b
}

// tcAgreement is the answer a downstream bridge gives the proposal on "down":
// a Root port message with the Agreement flag and a vector worse than the
// test bridge's own.
func tcAgreement() bpdu.BPDU {
	b := tcRootBPDU()
	b.RootPathCost = 40_000
	b.BridgeID = bpdu.BridgeID{Priority: 61440, Address: [6]byte{2, 0, 0, 0, 0, 3}}
	b.PortID = 0x8003
	b.SetRole(bpdu.RoleRoot)
	b.SetAgreement(true)

	return b
}

// newTCBridge brings the ports up and settles the Root port, so its own
// topology change timer, started when it opened, has run out by the returned
// bridge's now.
func newTCBridge(t *testing.T) *tcBridge {
	t.Helper()

	names := []string{"alt", "down", "edge", "up"}
	b := &tcBridge{
		t: t,
		l: mustNewSTP(t, stp.Config{
			Priority: 32768,
			Address:  mustMAC(t, "02:00:00:00:00:10"),
			Ports: map[string]stp.Port{
				"alt": {}, "down": {}, "edge": {AdminEdge: true}, "up": {},
			},
		}, mustPortTable(t, names...)),
		now: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
	}
	for _, name := range names {
		b.l.LinkChange(b.now, name, true, true, 1_000_000_000)
	}
	b.hear()
	for range 3 {
		b.step(2 * time.Second)
	}

	if got := b.l.PortInfo("up"); got.Role != bpdu.RoleRoot || got.State != stp.StateForwarding {
		t.Fatalf("up = %v %v, want Root Forwarding", got.Role, got.State)
	}
	if got := b.l.PortInfo("alt").Role; got != bpdu.RoleAlternate {
		t.Fatalf("alt role = %v, want Alternate", got)
	}
	if got := b.l.PortInfo("down"); got.Role != bpdu.RoleDesignated || got.State != stp.StateDiscarding {
		t.Fatalf("down = %v %v, want Designated Discarding", got.Role, got.State)
	}

	return b
}

// newForwardingTCBridge is newTCBridge after "down" has opened on its peer's
// agreement and every timer that opening started has run out.
func newForwardingTCBridge(t *testing.T) *tcBridge {
	t.Helper()

	b := newTCBridge(t)
	b.l.Receive(b.now, "down", tcAgreement())
	for range 2 {
		b.step(2 * time.Second)
	}
	if got := b.l.PortInfo("down").State; got != stp.StateForwarding {
		t.Fatalf("down state = %v, want Forwarding", got)
	}

	return b
}

// hear replays the root's and the second path's hellos.
func (b *tcBridge) hear() {
	b.l.Receive(b.now, "up", tcRootBPDU())
	b.l.Receive(b.now, "alt", tcSecondPathBPDU())
}

// step moves time on, replays the hellos, and advances the layer.
func (b *tcBridge) step(d time.Duration) layer.Effects {
	b.now = b.now.Add(d)
	b.hear()

	return b.l.Advance(b.now)
}

// emittedOn decodes the BPDUs fx sends on the named port.
func emittedOn(t *testing.T, fx layer.Effects, port string) []bpdu.BPDU {
	t.Helper()

	var out []bpdu.BPDU
	for _, e := range fx.Emissions {
		if e.Port != port {
			continue
		}
		b, err := bpdu.Decode(e.Frame)
		if err != nil {
			t.Fatalf("decode emission on %s: %v", port, err)
		}
		out = append(out, b)
	}

	return out
}

// flaggedPorts names the ports fx sends a BPDU with the topology change flag
// on, sorted.
func flaggedPorts(t *testing.T, fx layer.Effects) []string {
	t.Helper()

	var out []string
	for _, e := range fx.Emissions {
		b, err := bpdu.Decode(e.Frame)
		if err != nil {
			t.Fatalf("decode emission on %s: %v", e.Port, err)
		}
		if b.TopologyChange() && !slices.Contains(out, e.Port) {
			out = append(out, e.Port)
		}
	}
	slices.Sort(out)

	return out
}

// TestRootPortCarriesATopologyChangeTowardTheRoot pins that a Designated port
// reaching Forwarding makes the Root port send the flag at once and with each
// hello while its timer runs, and stop when HelloTime plus one second has
// passed (IEEE Std 802.1Q-2003 Figure 13-13 TRANSMIT_PERIODIC, 13.26.22).
func TestRootPortCarriesATopologyChangeTowardTheRoot(t *testing.T) {
	t.Parallel()

	b := newTCBridge(t)

	fx := b.l.Receive(b.now, "down", tcAgreement())
	if got := b.l.PortInfo("down").State; got != stp.StateForwarding {
		t.Fatalf("down state = %v, want Forwarding after its peer agreed", got)
	}
	if got := flaggedPorts(t, fx); !slices.Equal(got, []string{"up"}) {
		t.Errorf("flagged emissions in the call that opened down = %v, want the Root port alone", got)
	}

	if got := flaggedPorts(t, b.step(time.Second)); len(got) != 0 {
		t.Errorf("flagged emissions between hellos = %v, want none", got)
	}
	if got := flaggedPorts(t, b.step(time.Second)); !slices.Equal(got, []string{"down", "up"}) {
		t.Errorf("flagged emissions at the hello while the timer runs = %v, want down and up", got)
	}
	if got := flaggedPorts(t, b.step(2*time.Second)); len(got) != 0 {
		t.Errorf("flagged emissions once HelloTime plus one second has passed = %v, want none", got)
	}
}

// TestTopologyChangeIsNotSentBackOnTheArrivalPort pins that the port a flagged
// BPDU arrived on starts no timer of its own, so it sends none back, while
// the other active port passes the change on (13.26.20).
func TestTopologyChangeIsNotSentBackOnTheArrivalPort(t *testing.T) {
	t.Parallel()

	b := newForwardingTCBridge(t)

	flagged := tcAgreement()
	flagged.SetTopologyChange(true)
	fx := b.l.Receive(b.now, "down", flagged)
	if got := flaggedPorts(t, fx); !slices.Equal(got, []string{"up"}) {
		t.Errorf("flagged emissions in the receiving call = %v, want the Root port alone", got)
	}

	got := flaggedPorts(t, b.step(2*time.Second))
	if slices.Contains(got, "down") {
		t.Errorf("flagged hello ports = %v, want the arrival port \"down\" to send none back", got)
	}
	if !slices.Contains(got, "up") {
		t.Errorf("flagged hello ports = %v, want the Root port to carry the change", got)
	}
}

// TestLeavingTheActiveTopologyRaisesNoTopologyChange pins that a forwarding
// port that loses its role or goes down is flushed itself and raises nothing:
// no count, no flush elsewhere, no flag on the wire.
func TestLeavingTheActiveTopologyRaisesNoTopologyChange(t *testing.T) {
	t.Parallel()

	t.Run("link down", func(t *testing.T) {
		t.Parallel()

		b := newForwardingTCBridge(t)
		before, _ := b.l.TopologyChanges()

		fx := b.l.LinkChange(b.now, "down", false, true, 0)

		assertNothingRaised(t, b, fx, before)
	})

	t.Run("role lost", func(t *testing.T) {
		t.Parallel()

		b := newForwardingTCBridge(t)
		before, _ := b.l.TopologyChanges()

		better := tcSecondPathBPDU()
		better.BridgeID = bpdu.BridgeID{Priority: 8192, Address: [6]byte{2, 0, 0, 0, 0, 4}}
		fx := b.l.Receive(b.now, "down", better)
		if got := b.l.PortInfo("down").Role; got != bpdu.RoleAlternate {
			t.Fatalf("down role = %v, want Alternate after a better bridge answered", got)
		}

		assertNothingRaised(t, b, fx, before)
	})
}

func assertNothingRaised(t *testing.T, b *tcBridge, fx layer.Effects, before uint64) {
	t.Helper()

	if after, _ := b.l.TopologyChanges(); after != before {
		t.Errorf("topology changes = %d, want %d: a port leaving the topology detects none", after, before)
	}
	if got := flushPorts(fx.Flush); !slices.Equal(got, []string{"down"}) {
		t.Errorf("flushed ports = %v, want the leaving port alone", got)
	}
	if got := flaggedPorts(t, fx); len(got) != 0 {
		t.Errorf("flagged emissions = %v, want none", got)
	}
}

// TestInactivePortsIgnoreTopologyChange pins that a port outside the active
// topology acts on no topology change flag: an Alternate port and a Designated
// port still Discarding flush nothing and start no timer (Figure 13-19
// INACTIVE).
func TestInactivePortsIgnoreTopologyChange(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		port string
		msg  func() bpdu.BPDU
	}{
		{"alternate", "alt", tcSecondPathBPDU},
		{"designated discarding", "down", func() bpdu.BPDU {
			b := tcAgreement()
			b.SetAgreement(false)

			return b
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b := newTCBridge(t)
			flagged := tc.msg()
			flagged.SetTopologyChange(true)
			notification := bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification}

			for _, msg := range []bpdu.BPDU{flagged, notification} {
				fx := b.l.Receive(b.now, tc.port, msg)
				if len(fx.Flush) != 0 {
					t.Errorf("flush after %v on %s = %v, want none", msg.Type, tc.port, fx.Flush)
				}
			}
			if got := flaggedPorts(t, b.step(time.Second)); len(got) != 0 {
				t.Errorf("flagged emissions after the change on %s = %v, want none", tc.port, got)
			}
		})
	}
}

// TestPropagatedTopologyChangeLeavesAnEdgePortAlone pins that a change
// reaching a bridge flushes the active ports and not an edge port, which
// never enters the active topology (Figure 13-19, operEdge).
func TestPropagatedTopologyChangeLeavesAnEdgePortAlone(t *testing.T) {
	t.Parallel()

	b := newForwardingTCBridge(t)

	flagged := tcRootBPDU()
	flagged.SetTopologyChange(true)
	fx := b.l.Receive(b.now, "up", flagged)

	if got := flushPorts(fx.Flush); !slices.Equal(got, []string{"down"}) {
		t.Errorf("flushed ports = %v, want the active Designated port alone, not alt or edge", got)
	}
}

// TestTCNOnADesignatedPortIsAcknowledgedOnceAndRunsTheLegacyTimer pins that the
// next Configuration BPDU on a port that heard a TCN carries the
// acknowledgment once, and that the topology change flag stays set for Max
// Age plus Forward Delay, 35 seconds at the defaults (13.26.6, 13.26.21).
func TestTCNOnADesignatedPortIsAcknowledgedOnceAndRunsTheLegacyTimer(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "02:00:00:00:00:10"),
		Ports:    map[string]stp.Port{"down": {}, "other": {}},
	}, mustPortTable(t, "down", "other"))
	l.LinkChange(t0, "down", true, true, 1_000_000_000)
	l.LinkChange(t0, "other", true, true, 1_000_000_000)

	// Two forward delays open both Designated ports.
	l.Advance(t0.Add(15 * time.Second))
	l.Advance(t0.Add(30 * time.Second))
	if got := l.PortInfo("down").State; got != stp.StateForwarding {
		t.Fatalf("down state = %v, want Forwarding", got)
	}

	tcn := t0.Add(34 * time.Second)
	l.Receive(tcn, "down", bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification})

	type seen struct {
		after time.Duration
		b     bpdu.BPDU
	}
	var got []seen
	for at := tcn.Add(time.Second); at.Sub(tcn) <= 38*time.Second; at = at.Add(time.Second) {
		for _, b := range emittedOn(t, l.Advance(at), "down") {
			got = append(got, seen{at.Sub(tcn), b})
		}
	}
	if len(got) == 0 {
		t.Fatal("no BPDU left the port that heard the TCN")
	}

	for i, s := range got {
		if s.b.Type != bpdu.TypeConfiguration {
			t.Fatalf("emission %d type = %v, want Configuration: the peer speaks STP", i, s.b.Type)
		}
		if want := i == 0; s.b.TopologyChangeAck() != want {
			t.Errorf("emission %d at +%v: acknowledgment = %v, want %v: once, on the first", i, s.after, s.b.TopologyChangeAck(), want)
		}
		if want := s.after < 35*time.Second; s.b.TopologyChange() != want {
			t.Errorf("emission %d at +%v: topology change flag = %v, want %v: set for 35 seconds", i, s.after, s.b.TopologyChange(), want)
		}
	}
}

// TestLegacyRootPortSendsTCNUntilAcknowledged pins that a Root port facing an
// STP bridge sends a TCN BPDU when it opens and at each hello, and stops when
// a Configuration BPDU acknowledges it (Figure 13-13 TRANSMIT_TCN, 13.26.19).
func TestLegacyRootPortSendsTCNUntilAcknowledged(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "02:00:00:00:00:10"),
		Ports:    map[string]stp.Port{"up": {}, "down": {}},
	}, mustPortTable(t, "up", "down"))
	l.LinkChange(t0, "up", true, true, 1_000_000_000)
	l.LinkChange(t0, "down", true, true, 1_000_000_000)

	root := tcRootBPDU()
	root.Version = 0
	root.Type = bpdu.TypeConfiguration

	var tcns []time.Duration
	acknowledged := false
	for at := t0.Add(3 * time.Second); at.Sub(t0) <= 48*time.Second; at = at.Add(time.Second) {
		fx := l.Receive(at, "up", root)
		if at.Sub(t0) == 44*time.Second {
			ack := root
			ack.SetTopologyChangeAck(true)
			l.Receive(at, "up", ack)
			acknowledged = true
		}
		adv := l.Advance(at)
		for _, e := range append(fx.Emissions, adv.Emissions...) {
			if b, err := bpdu.Decode(e.Frame); err == nil && e.Port == "up" && b.Type == bpdu.TypeTopologyChangeNotification {
				tcns = append(tcns, at.Sub(t0))
			}
		}
	}
	if !acknowledged {
		t.Fatal("the acknowledgment was never delivered")
	}
	if got := l.PortInfo("up"); got.Role != bpdu.RoleRoot || got.State != stp.StateForwarding {
		t.Fatalf("up = %v %v, want Root Forwarding after two forward delays", got.Role, got.State)
	}
	if len(tcns) < 2 || tcns[1] != 31*time.Second {
		t.Fatalf("TCN BPDUs sent at %v, want the second at the next hello at 31s", tcns)
	}

	// The port left Discarding two forward delays after the link came up.
	if len(tcns) == 0 || tcns[0] != 30*time.Second {
		t.Fatalf("TCN BPDUs sent at %v, want the first when the Root port opens at 30s", tcns)
	}
	if len(tcns) < 3 {
		t.Errorf("TCN BPDUs sent at %v, want one at each hello until acknowledged", tcns)
	}
	if last := tcns[len(tcns)-1]; last > 44*time.Second {
		t.Errorf("last TCN BPDU at %v, want none once the acknowledgment arrived at 44s", last)
	}
}
