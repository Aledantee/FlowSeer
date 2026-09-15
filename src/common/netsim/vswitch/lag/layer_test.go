package lag_test

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// selectOK adapts lag.Layer's committing Select to the (member, ok) shape
// most of this file's tests check.
func selectOK(l *lag.Layer, now time.Time, lagName string, f ethernet.Frame, vid vlan.ID) (string, bool) {
	sel := l.Select(now, lagName, f, vid)
	return sel.Member, sel.OK
}

func lagTwoPortTable(t *testing.T) port.Table {
	t.Helper()
	tbl, err := port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1"}).
		Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	return tbl
}

func mustNewLAG(t *testing.T, cfg lag.Config, ports port.Table, systemID netaddr.MAC) *lag.Layer {
	t.Helper()
	l, err := lag.New(cfg, ports, systemID)
	if err != nil {
		t.Fatalf("lag.New: %v", err)
	}
	return l
}

func makeUDPFrame(t *testing.T, srcMAC, dstMAC netaddr.MAC, srcIP, dstIP string, srcPort, dstPort uint16, badChecksum bool) ethernet.Frame {
	t.Helper()
	udpPayload := make([]byte, 8)
	binary.BigEndian.PutUint16(udpPayload[0:2], srcPort)
	binary.BigEndian.PutUint16(udpPayload[2:4], dstPort)
	binary.BigEndian.PutUint16(udpPayload[4:6], 8)
	binary.BigEndian.PutUint16(udpPayload[6:8], 0)

	hdr := ip.Header{
		Src:      netip.MustParseAddr(srcIP),
		Dst:      netip.MustParseAddr(dstIP),
		Protocol: 17,
		HopLimit: 64,
		V4:       &ip.V4{},
	}
	ipPayload, err := hdr.Encode(udpPayload)
	if err != nil {
		t.Fatalf("encode IPv4 UDP frame: %v", err)
	}

	if badChecksum {
		ipPayload[10] ^= 0xff
	}

	return ethernet.Frame{
		Src:       srcMAC,
		Dst:       dstMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   ipPayload,
	}
}

func makeARPFrame(srcMAC, dstMAC netaddr.MAC) ethernet.Frame {
	arpPayload := make([]byte, 28)

	return ethernet.Frame{
		Src:       srcMAC,
		Dst:       dstMAC,
		EtherType: ethernet.EtherTypeARP,
		Payload:   arpPayload,
	}
}

// convergeLACP brings both members of lag1 up on two peered layers and
// exchanges LACPDUs until they attach and synchronize, as TestLACPConvergence
// does. It returns the time reached, for callers that continue from there.
func convergeLACP(t *testing.T, a, b *lag.Layer, t0 time.Time) time.Time {
	t.Helper()

	fxA1 := a.LinkChange(t0, "1/1/1", true)
	fxA2 := a.LinkChange(t0, "1/1/2", true)
	fxB1 := b.LinkChange(t0, "1/1/1", true)
	fxB2 := b.LinkChange(t0, "1/1/2", true)
	exchangeEmissions(t, t0, a, b, append(fxA1.Emissions, fxA2.Emissions...), append(fxB1.Emissions, fxB2.Emissions...))

	cur := t0
	for step := 0; step < 30; step++ {
		cur = cur.Add(100 * time.Millisecond)
		fxA := a.Wake(cur)
		fxB := b.Wake(cur)
		exchangeEmissions(t, cur, a, b, fxA.Emissions, fxB.Emissions)
	}

	return cur
}

func exchangeEmissions(t *testing.T, now time.Time, layerA, layerB *lag.Layer, emsA, emsB []lag.Emission) {
	t.Helper()
	queueA := slices.Clone(emsA)
	queueB := slices.Clone(emsB)

	for len(queueA) > 0 || len(queueB) > 0 {
		currA := queueA
		currB := queueB
		queueA = nil
		queueB = nil

		for _, em := range currA {
			pdu, err := lacp.Decode(em.Frame)
			if err != nil {
				t.Fatalf("decode emission from A: %v", err)
			}
			fx := layerB.Receive(now, em.Port, pdu)
			queueB = append(queueB, fx.Emissions...)
		}

		for _, em := range currB {
			pdu, err := lacp.Decode(em.Frame)
			if err != nil {
				t.Fatalf("decode emission from B: %v", err)
			}
			fx := layerA.Receive(now, em.Port, pdu)
			queueA = append(queueA, fx.Emissions...)
		}
	}
}

func TestBalanceSLB(t *testing.T) {
	t.Parallel()

	tbl := lagTwoPortTable(t)
	sysMAC := mustMAC(t, "02:00:00:00:00:01")
	cfg := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode: lag.BalanceSLB,
			},
		},
	}
	l := mustNewLAG(t, cfg, tbl, sysMAC)
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l.LinkChange(t0, "1/1/1", true)
	l.LinkChange(t0, "1/1/2", true)

	membersSeen := make(map[string]bool)
	dstMAC := mustMAC(t, "02:00:00:00:00:99")

	for i := 0xa1; i <= 0xa8; i++ {
		srcMAC := mustMAC(t, fmt.Sprintf("02:00:00:00:00:%02x", i))
		f := ethernet.Frame{
			Src:       srcMAC,
			Dst:       dstMAC,
			EtherType: ethernet.EtherTypeIPv4,
		}

		firstChoice, ok := selectOK(l, t0, "lag1", f, vlan.ID(10))
		if !ok {
			t.Fatalf("Select failed for src %v in VLAN 10", srcMAC)
		}
		membersSeen[firstChoice] = true

		for repeat := 0; repeat < 5; repeat++ {
			choice, ok := selectOK(l, t0, "lag1", f, vlan.ID(10))
			if !ok || choice != firstChoice {
				t.Fatalf("Select inconsistent for src %v: got (%q, %t), want (%q, true)", srcMAC, choice, ok, firstChoice)
			}
		}

		choiceV20, ok := selectOK(l, t0, "lag1", f, vlan.ID(20))
		if !ok {
			t.Fatalf("Select failed for src %v in VLAN 20", srcMAC)
		}
		membersSeen[choiceV20] = true
		choiceV20Repeat, ok := selectOK(l, t0, "lag1", f, vlan.ID(20))
		if !ok || choiceV20Repeat != choiceV20 {
			t.Fatalf("Select in VLAN 20 inconsistent for src %v: got %q, want %q", srcMAC, choiceV20Repeat, choiceV20)
		}
	}

	if len(membersSeen) < 2 {
		t.Fatalf("BalanceSLB did not balance across multiple members: only saw %v", membersSeen)
	}
}

// TestBalanceTCP is evidence that balance-tcp, once LACP has negotiated and
// attached both members, balances by the layer 2 through 4 hash tuple across
// distinct buckets.
func TestBalanceTCP(t *testing.T) {
	t.Parallel()

	tblA := lagTwoPortTable(t)
	tblB := lagTwoPortTable(t)
	macA := mustMAC(t, "02:00:00:00:00:0a")
	macB := mustMAC(t, "02:00:00:00:00:0b")
	cfg := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode: lag.BalanceTCP,
				LACP: lag.LACPConfig{Mode: lag.Active, Fast: true},
			},
		},
	}
	a := mustNewLAG(t, cfg, tblA, macA)
	b := mustNewLAG(t, cfg, tblB, macB)
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	cur := convergeLACP(t, a, b, t0)

	srcMAC := mustMAC(t, "02:00:00:00:00:01")
	dstMAC := mustMAC(t, "02:00:00:00:00:02")

	membersSeen := make(map[string]bool)
	for port := uint16(40000); port <= 40007; port++ {
		f := makeUDPFrame(t, srcMAC, dstMAC, "10.0.0.1", "10.0.0.2", port, 5000, false)
		firstChoice, ok := selectOK(a, cur, "lag1", f, 0)
		if !ok {
			t.Fatalf("Select failed for UDP port %d", port)
		}
		membersSeen[firstChoice] = true

		for repeat := 0; repeat < 5; repeat++ {
			choice, ok := selectOK(a, cur, "lag1", f, 0)
			if !ok || choice != firstChoice {
				t.Fatalf("Select inconsistent for UDP port %d: got (%q, %t), want (%q, true)", port, choice, ok, firstChoice)
			}
		}
	}

	if len(membersSeen) < 2 {
		t.Fatalf("BalanceTCP did not balance across multiple members: only saw %v", membersSeen)
	}

	// A bad IPv4 checksum and a non-IP frame both fall back to a layer-2-only
	// hash (see TestSelectionFactCapturesConsumedHashTuple's "rejected IP"
	// case for that directly); here it is enough that hashing such frames
	// still reaches a member.
	badFrame := makeUDPFrame(t, srcMAC, dstMAC, "10.0.0.1", "10.0.0.2", 40000, 5000, true)
	if _, ok := selectOK(a, cur, "lag1", badFrame, 0); !ok {
		t.Fatal("Select failed for bad checksum frame")
	}

	arpFrame := makeARPFrame(srcMAC, dstMAC)
	if _, ok := selectOK(a, cur, "lag1", arpFrame, 0); !ok {
		t.Fatal("Select failed for ARP frame")
	}
}

// TestBalanceTCPWithoutLACPSelectsNothing is evidence that balance-tcp
// requires negotiated LACP, so LACP off with two up members gives no
// selection.
func TestBalanceTCPWithoutLACPSelectsNothing(t *testing.T) {
	t.Parallel()

	tbl := lagTwoPortTable(t)
	sysMAC := mustMAC(t, "02:00:00:00:00:01")
	cfg := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {Mode: lag.BalanceTCP},
		},
	}
	l := mustNewLAG(t, cfg, tbl, sysMAC)
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l.LinkChange(t0, "1/1/1", true)
	l.LinkChange(t0, "1/1/2", true)

	if _, ok := selectOK(l, t0, "lag1", ethernet.Frame{}, 0); ok {
		t.Fatal("Select succeeded with LACP off and no Fallback, want false")
	}
}

func TestActiveBackup(t *testing.T) {
	t.Parallel()

	tbl := lagTwoPortTable(t)
	sysMAC := mustMAC(t, "02:00:00:00:00:01")
	cfg := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode: lag.ActiveBackup,
			},
		},
	}
	l := mustNewLAG(t, cfg, tbl, sysMAC)
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l.LinkChange(t0, "1/1/1", true)
	l.LinkChange(t0, "1/1/2", true)

	f := ethernet.Frame{}
	chosen, ok := selectOK(l, t0, "lag1", f, 0)
	if !ok || chosen != "1/1/1" {
		t.Fatalf("Select = (%q, %t), want (1/1/1, true)", chosen, ok)
	}

	t1 := t0.Add(time.Second)
	fx := l.LinkChange(t1, "1/1/1", false)
	if !slices.Contains(fx.Changed, "lag1") {
		t.Fatalf("Changed does not contain lag1: %v", fx.Changed)
	}

	chosen2, ok := selectOK(l, t1, "lag1", f, 0)
	if !ok || chosen2 != "1/1/2" {
		t.Fatalf("Select after 1/1/1 down = (%q, %t), want (1/1/2, true)", chosen2, ok)
	}

	l.LinkChange(t1.Add(time.Second), "1/1/2", false)
	_, ok = selectOK(l, t1.Add(time.Second), "lag1", f, 0)
	if ok {
		t.Fatal("Select succeeded when no member enabled, want false")
	}
}

func TestDelays(t *testing.T) {
	t.Parallel()

	tbl := lagTwoPortTable(t)
	sysMAC := mustMAC(t, "02:00:00:00:00:01")
	cfg := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode:      lag.ActiveBackup,
				UpDelay:   2 * time.Second,
				DownDelay: 1 * time.Second,
			},
		},
	}
	l := mustNewLAG(t, cfg, tbl, sysMAC)
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l.LinkChange(t0, "1/1/1", true)
	l.LinkChange(t0, "1/1/2", true)

	f := ethernet.Frame{}

	// Because UpDelay is 2s, neither is enabled yet at t0.
	w0, hasTimer := l.NextWake()
	if !hasTimer || w0 != t0.Add(2*time.Second) {
		t.Fatalf("NextWake = (%v, %v), want (%v, true)", w0, hasTimer, t0.Add(2*time.Second))
	}
	l.Wake(t0.Add(2 * time.Second))

	chosen, ok := selectOK(l, t0.Add(2*time.Second), "lag1", f, 0)
	if !ok || chosen != "1/1/1" {
		t.Fatalf("Select at t0+2s = (%q, %t), want (1/1/1, true)", chosen, ok)
	}

	t1 := t0.Add(10 * time.Second)
	l.LinkChange(t1, "1/1/1", false)

	// Still returns 1/1/1 before down delay passes.
	chosen, ok = selectOK(l, t1, "lag1", f, 0)
	if !ok || chosen != "1/1/1" {
		t.Fatalf("Select before down delay elapses = (%q, %t), want (1/1/1, true)", chosen, ok)
	}

	next, okTimer := l.NextWake()
	if !okTimer || next != t1.Add(1*time.Second) {
		t.Fatalf("NextWake = (%v, %v), want (%v, true)", next, okTimer, t1.Add(1*time.Second))
	}

	l.Wake(t1.Add(1 * time.Second))
	chosen, ok = selectOK(l, t1.Add(1*time.Second), "lag1", f, 0)
	if !ok || chosen != "1/1/2" {
		t.Fatalf("Select after Wake(t1+1s) = (%q, %t), want (1/1/2, true)", chosen, ok)
	}

	t2 := t1.Add(10 * time.Second)
	l.LinkChange(t2, "1/1/1", true)

	// 1/1/1 is not enabled until Wake(t2 + 2s).
	if l.PortInfo("1/1/1").Enabled {
		t.Fatal("1/1/1 is enabled before UpDelay expires")
	}

	l.Wake(t2.Add(2 * time.Second))
	if !l.PortInfo("1/1/1").Enabled {
		t.Fatal("1/1/1 is not enabled after Wake(t2+2s)")
	}
}

func TestLACPConvergence(t *testing.T) {
	t.Parallel()

	tblA := lagTwoPortTable(t)
	tblB := lagTwoPortTable(t)

	macA := mustMAC(t, "02:00:00:00:00:0a")
	macB := mustMAC(t, "02:00:00:00:00:0b")

	cfgA := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				LACP: lag.LACPConfig{
					Mode: lag.Active,
					Fast: true,
				},
			},
		},
	}
	cfgB := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				LACP: lag.LACPConfig{
					Mode: lag.Active,
					Fast: true,
				},
			},
		},
	}

	layerA := mustNewLAG(t, cfgA, tblA, macA)
	layerB := mustNewLAG(t, cfgB, tblB, macB)

	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	fxA1 := layerA.LinkChange(t0, "1/1/1", true)
	fxA2 := layerA.LinkChange(t0, "1/1/2", true)
	fxB1 := layerB.LinkChange(t0, "1/1/1", true)
	fxB2 := layerB.LinkChange(t0, "1/1/2", true)

	exchangeEmissions(t, t0, layerA, layerB, append(fxA1.Emissions, fxA2.Emissions...), append(fxB1.Emissions, fxB2.Emissions...))

	cur := t0
	for step := 0; step < 30; step++ {
		cur = cur.Add(100 * time.Millisecond)
		fxA := layerA.Wake(cur)
		fxB := layerB.Wake(cur)
		exchangeEmissions(t, cur, layerA, layerB, fxA.Emissions, fxB.Emissions)
	}

	infoA := layerA.Info("lag1")
	if !infoA.Up {
		t.Fatal("layerA lag1 Up is false, want true")
	}
	if len(infoA.Attached) != 2 || len(infoA.Enabled) != 2 {
		t.Fatalf("layerA attached %v, enabled %v, want both 2", infoA.Attached, infoA.Enabled)
	}

	p1 := layerA.PortInfo("1/1/1")
	if p1.Partner.SystemID != macB {
		t.Fatalf("layerA 1/1/1 partner system ID = %v, want %v", p1.Partner.SystemID, macB)
	}

	// Key mismatch test: B's member 1/1/2 configured with Key 2
	cfgBKey2 := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				LACP: lag.LACPConfig{
					Mode: lag.Active,
					Fast: true,
				},
				Members: map[string]lag.Member{
					"1/1/2": {Key: 2},
				},
			},
		},
	}
	layerA2 := mustNewLAG(t, cfgA, tblA, macA)
	layerB2 := mustNewLAG(t, cfgBKey2, tblB, macB)

	fxA1 = layerA2.LinkChange(t0, "1/1/1", true)
	fxA2 = layerA2.LinkChange(t0, "1/1/2", true)
	fxB1 = layerB2.LinkChange(t0, "1/1/1", true)
	fxB2 = layerB2.LinkChange(t0, "1/1/2", true)

	exchangeEmissions(t, t0, layerA2, layerB2, append(fxA1.Emissions, fxA2.Emissions...), append(fxB1.Emissions, fxB2.Emissions...))

	cur = t0
	for step := 0; step < 30; step++ {
		cur = cur.Add(100 * time.Millisecond)
		fxA := layerA2.Wake(cur)
		fxB := layerB2.Wake(cur)
		exchangeEmissions(t, cur, layerA2, layerB2, fxA.Emissions, fxB.Emissions)
	}

	p1Info := layerA2.PortInfo("1/1/1")
	p2Info := layerA2.PortInfo("1/1/2")
	if !p1Info.Enabled {
		t.Fatal("A 1/1/1 is not enabled")
	}
	if p2Info.Attached {
		t.Fatal("A 1/1/2 with mismatched key is attached, want detached")
	}
}

func TestFallback(t *testing.T) {
	t.Parallel()

	tblA := lagTwoPortTable(t)
	macA := mustMAC(t, "02:00:00:00:00:0a")

	t.Run("fallback true enables active backup after 6s", func(t *testing.T) {
		t.Parallel()
		cfg := lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {
					LACP: lag.LACPConfig{
						Mode:     lag.Active,
						Fast:     true,
						Fallback: true,
					},
				},
			},
		}
		l := mustNewLAG(t, cfg, tblA, macA)
		t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		l.LinkChange(t0, "1/1/1", true)
		l.LinkChange(t0, "1/1/2", true)

		// Before 6s, members are not enabled by fallback.
		l.Wake(t0.Add(3 * time.Second))
		if l.PortInfo("1/1/1").Status != lag.Expired {
			t.Fatalf("status at 3s = %v, want Expired", l.PortInfo("1/1/1").Status)
		}
		if _, ok := selectOK(l, t0.Add(3*time.Second), "lag1", ethernet.Frame{}, 0); ok {
			t.Fatal("Select succeeded at 3s before defaulting, want false")
		}

		l.Wake(t0.Add(6 * time.Second))
		if l.PortInfo("1/1/1").Status != lag.Defaulted {
			t.Fatalf("status at 6s = %v, want Defaulted", l.PortInfo("1/1/1").Status)
		}
		chosen, ok := selectOK(l, t0.Add(6*time.Second), "lag1", ethernet.Frame{}, 0)
		if !ok || chosen != "1/1/1" {
			t.Fatalf("Select at 6s with Fallback = (%q, %t), want (1/1/1, true)", chosen, ok)
		}
	})

	t.Run("fallback false enables nothing after 6s", func(t *testing.T) {
		t.Parallel()
		cfg := lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {
					LACP: lag.LACPConfig{
						Mode:     lag.Active,
						Fast:     true,
						Fallback: false,
					},
				},
			},
		}
		l := mustNewLAG(t, cfg, tblA, macA)
		t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		l.LinkChange(t0, "1/1/1", true)
		l.LinkChange(t0, "1/1/2", true)

		l.Wake(t0.Add(6 * time.Second))
		if _, ok := selectOK(l, t0.Add(6*time.Second), "lag1", ethernet.Frame{}, 0); ok {
			t.Fatal("Select succeeded with Fallback=false after 6s, want false")
		}
	})
}

func TestPassive(t *testing.T) {
	t.Parallel()

	tblA := lagTwoPortTable(t)
	tblB := lagTwoPortTable(t)
	macA := mustMAC(t, "02:00:00:00:00:0a")
	macB := mustMAC(t, "02:00:00:00:00:0b")

	t.Run("active and passive", func(t *testing.T) {
		t.Parallel()
		cfgA := lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {
					LACP: lag.LACPConfig{Mode: lag.Active, Fast: true},
				},
			},
		}
		cfgB := lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {
					LACP: lag.LACPConfig{Mode: lag.Passive, Fast: true},
				},
			},
		}
		lA := mustNewLAG(t, cfgA, tblA, macA)
		lB := mustNewLAG(t, cfgB, tblB, macB)

		t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		fxA1 := lA.LinkChange(t0, "1/1/1", true)
		fxB1 := lB.LinkChange(t0, "1/1/1", true)

		if len(fxB1.Emissions) != 0 {
			t.Fatalf("passive B emitted at link up before receiving A's PDU: %v", fxB1.Emissions)
		}
		if len(fxA1.Emissions) == 0 {
			t.Fatal("active A did not emit at link up")
		}

		pduA, err := lacp.Decode(fxA1.Emissions[0].Frame)
		if err != nil {
			t.Fatalf("decode A emission: %v", err)
		}
		fxBReceive := lB.Receive(t0, "1/1/1", pduA)
		if len(fxBReceive.Emissions) == 0 {
			t.Fatal("passive B did not emit after receiving A's PDU")
		}
	})

	t.Run("both passive never transmit", func(t *testing.T) {
		t.Parallel()
		cfg := lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {
					LACP: lag.LACPConfig{Mode: lag.Passive, Fast: true},
				},
			},
		}
		lA := mustNewLAG(t, cfg, tblA, macA)
		lB := mustNewLAG(t, cfg, tblB, macB)

		t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		fxA1 := lA.LinkChange(t0, "1/1/1", true)
		fxB1 := lB.LinkChange(t0, "1/1/1", true)
		if len(fxA1.Emissions) != 0 || len(fxB1.Emissions) != 0 {
			t.Fatal("passive emitted at link up")
		}

		cur := t0
		for step := 0; step < 50; step++ {
			cur = cur.Add(100 * time.Millisecond)
			fxA := lA.Wake(cur)
			fxB := lB.Wake(cur)
			if len(fxA.Emissions) != 0 || len(fxB.Emissions) != 0 {
				t.Fatalf("passive emitted within 5s at %v", cur)
			}
		}

		lA.Wake(t0.Add(6 * time.Second))
		lB.Wake(t0.Add(6 * time.Second))
		if lA.PortInfo("1/1/1").Status != lag.Defaulted || lB.PortInfo("1/1/1").Status != lag.Defaulted {
			t.Fatalf("status at 6s: A=%v, B=%v, want Defaulted", lA.PortInfo("1/1/1").Status, lB.PortInfo("1/1/1").Status)
		}
	})
}

func TestCounters(t *testing.T) {
	t.Parallel()

	tblA := lagTwoPortTable(t)
	tblB := lagTwoPortTable(t)
	macA := mustMAC(t, "02:00:00:00:00:0a")
	macB := mustMAC(t, "02:00:00:00:00:0b")

	cfg := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				LACP: lag.LACPConfig{Mode: lag.Active, Fast: true},
			},
		},
	}
	layerA := mustNewLAG(t, cfg, tblA, macA)
	layerB := mustNewLAG(t, cfg, tblB, macB)

	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	fxA1 := layerA.LinkChange(t0, "1/1/1", true)
	fxA2 := layerA.LinkChange(t0, "1/1/2", true)
	fxB1 := layerB.LinkChange(t0, "1/1/1", true)
	fxB2 := layerB.LinkChange(t0, "1/1/2", true)

	exchangeEmissions(t, t0, layerA, layerB, append(fxA1.Emissions, fxA2.Emissions...), append(fxB1.Emissions, fxB2.Emissions...))

	cur := t0
	for step := 0; step < 30; step++ {
		cur = cur.Add(100 * time.Millisecond)
		fxA := layerA.Wake(cur)
		fxB := layerB.Wake(cur)
		exchangeEmissions(t, cur, layerA, layerB, fxA.Emissions, fxB.Emissions)
	}

	info := layerA.PortInfo("1/1/1")
	if info.LACPDUsTx == 0 {
		t.Fatal("LACPDUsTx is 0 after convergence, want non-zero")
	}
	if info.LACPDUsRx == 0 {
		t.Fatal("LACPDUsRx is 0 after convergence, want non-zero")
	}

	layerA.BadLACPDU("1/1/1")
	info = layerA.PortInfo("1/1/1")
	if info.BadLACPDUs != 1 {
		t.Fatalf("BadLACPDUs = %d, want 1", info.BadLACPDUs)
	}
}

func TestCloneIndependence(t *testing.T) {
	t.Parallel()

	tbl := lagTwoPortTable(t)
	mac := mustMAC(t, "02:00:00:00:00:0a")
	cfg := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode: lag.ActiveBackup,
			},
		},
	}
	l1 := mustNewLAG(t, cfg, tbl, mac)
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l1.LinkChange(t0, "1/1/1", true)

	l2 := l1.Clone()
	l2.LinkChange(t0.Add(time.Second), "1/1/2", true)

	info1 := l1.Info("lag1")
	info2 := l2.Info("lag1")

	if len(info1.Enabled) != 1 || info1.Enabled[0] != "1/1/1" {
		t.Fatalf("original layer enabled = %v, want [1/1/1]", info1.Enabled)
	}
	if len(info2.Enabled) != 2 {
		t.Fatalf("cloned layer enabled = %v, want 2 members", info2.Enabled)
	}
}

// TestExpiredHoldsForThreePeriods is evidence that Expired lasts three more
// periods before Defaulted, so fallback engages at 6 s on fast timers.
func TestExpiredHoldsForThreePeriods(t *testing.T) {
	t.Parallel()

	l := mustNewLAG(t, lag.Config{LAGs: map[string]lag.LAG{"lag1": {LACP: lag.LACPConfig{Mode: lag.Active, Fast: true, Fallback: true}}}},
		lagTwoPortTable(t), mustMAC(t, "02:00:00:00:00:0a"))
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l.LinkChange(t0, "1/1/1", true)
	for _, s := range []int{3, 4, 5} {
		l.Wake(t0.Add(time.Duration(s) * time.Second))
		if st := l.PortInfo("1/1/1").Status; st != lag.Expired {
			t.Fatalf("status at %ds = %v, want Expired", s, st)
		}
	}
	l.Wake(t0.Add(6 * time.Second))
	if st := l.PortInfo("1/1/1").Status; st != lag.Defaulted {
		t.Fatalf("status at 6s = %v, want Defaulted", st)
	}
}

// TestMinLinksDisablesAll is evidence that a LAG below its minimum carries
// nothing.
func TestMinLinksDisablesAll(t *testing.T) {
	t.Parallel()

	l := mustNewLAG(t, lag.Config{LAGs: map[string]lag.LAG{"lag1": {MinLinks: 2}}}, lagTwoPortTable(t), mustMAC(t, "02:00:00:00:00:0a"))
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l.LinkChange(t0, "1/1/1", true)
	if _, ok := selectOK(l, t0, "lag1", ethernet.Frame{}, 0); ok {
		t.Fatal("Select succeeded with one of two minimum links")
	}
	l.LinkChange(t0, "1/1/2", true)
	if _, ok := selectOK(l, t0, "lag1", ethernet.Frame{}, 0); !ok {
		t.Fatal("Select failed with the minimum met")
	}
	l.LinkChange(t0.Add(time.Second), "1/1/2", false)
	if _, ok := selectOK(l, t0.Add(time.Second), "lag1", ethernet.Frame{}, 0); ok {
		t.Fatal("Select succeeded after a member left the minimum")
	}
}

// TestUpDelayDoesNotHoldTheProtocol is evidence that LACP runs on the carrier
// while the up delay only holds enablement, so a partner is heard at once.
func TestUpDelayDoesNotHoldTheProtocol(t *testing.T) {
	t.Parallel()

	cfg := func() lag.Config {
		return lag.Config{LAGs: map[string]lag.LAG{"lag1": {UpDelay: 2 * time.Second, LACP: lag.LACPConfig{Mode: lag.Active, Fast: true}}}}
	}
	a := mustNewLAG(t, cfg(), lagTwoPortTable(t), mustMAC(t, "02:00:00:00:00:0a"))
	b := mustNewLAG(t, cfg(), lagTwoPortTable(t), mustMAC(t, "02:00:00:00:00:0b"))
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	fa := a.LinkChange(t0, "1/1/1", true)
	fb := b.LinkChange(t0, "1/1/1", true)
	if len(fa.Emissions) == 0 {
		t.Fatal("no LACPDU on carrier up during the up delay")
	}
	exchangeEmissions(t, t0, a, b, fa.Emissions, fb.Emissions)
	if a.PortInfo("1/1/1").LACPDUsRx == 0 || a.PortInfo("1/1/1").Status != lag.Current {
		t.Fatalf("A did not hear B during the up delay: %+v", a.PortInfo("1/1/1"))
	}
	if _, ok := selectOK(a, t0, "lag1", ethernet.Frame{}, 0); ok {
		t.Fatal("A selected a member before the up delay passed")
	}
	// A second report of the same carrier does not restart the delay.
	a.LinkChange(t0.Add(time.Second), "1/1/1", true)
	var now time.Time
	for s := 1; s <= 2; s++ {
		now = t0.Add(time.Duration(s) * time.Second)
		fa, fb = a.Wake(now), b.Wake(now)
		exchangeEmissions(t, now, a, b, fa.Emissions, fb.Emissions)
	}
	if _, ok := selectOK(a, now, "lag1", ethernet.Frame{}, 0); !ok {
		t.Fatalf("A did not enable its member two seconds after carrier: %+v", a.PortInfo("1/1/1"))
	}
}

// TestNextWakeNeverBeforeTheLastEvent is evidence that an overdue timer is
// reported as due now rather than hidden.
func TestNextWakeNeverBeforeTheLastEvent(t *testing.T) {
	t.Parallel()

	l := mustNewLAG(t, lag.Config{LAGs: map[string]lag.LAG{"lag1": {LACP: lag.LACPConfig{Mode: lag.Active, Fast: true}}}},
		lagTwoPortTable(t), mustMAC(t, "02:00:00:00:00:0a"))
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l.LinkChange(t0, "1/1/1", true)
	// An event five seconds later without a wake in between.
	l.LinkChange(t0.Add(5*time.Second), "1/1/2", true)
	next, ok := l.NextWake()
	if !ok || !next.Equal(t0.Add(5*time.Second)) {
		t.Fatalf("NextWake = (%v, %v), want the overdue timer reported at the last event time", next, ok)
	}
}

func lagNamedPortTable(t *testing.T, memberNames ...string) port.Table {
	t.Helper()
	b := port.NewBuilder().Add(port.Port{Name: "lag1", Kind: port.Lag})
	for _, name := range memberNames {
		b = b.Add(port.Port{Name: name, Kind: port.Physical, LagParent: "lag1"})
	}
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	return tbl
}

// TestMemberFaultMovesOnlyItsOwnBuckets is evidence that a bucket keeps its
// member while that member stays enabled, so a fault on one member moves
// only the buckets that were on it.
func TestMemberFaultMovesOnlyItsOwnBuckets(t *testing.T) {
	t.Parallel()

	tbl := lagNamedPortTable(t, "1", "2", "3")
	mac := mustMAC(t, "02:00:00:00:00:01")
	cfg := lag.Config{LAGs: map[string]lag.LAG{"lag1": {Mode: lag.BalanceSLB}}}
	l := mustNewLAG(t, cfg, tbl, mac)
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l.LinkChange(t0, "1", true)
	l.LinkChange(t0, "2", true)
	l.LinkChange(t0, "3", true)

	dstMAC := mustMAC(t, "02:00:00:00:00:99")
	frames := make([]ethernet.Frame, 64)
	before := make([]string, 64)
	for i := range frames {
		frames[i] = ethernet.Frame{Src: netaddr.MAC{0x02, 0, 0, 0, 0, byte(i)}, Dst: dstMAC, EtherType: ethernet.EtherTypeIPv4}
		member, ok := selectOK(l, t0, "lag1", frames[i], 0)
		if !ok {
			t.Fatalf("Select failed for flow %d", i)
		}
		before[i] = member
	}

	onMember := map[string]bool{}
	for _, m := range before {
		onMember[m] = true
	}
	if !onMember["1"] || !onMember["2"] || !onMember["3"] {
		t.Fatalf("setup did not spread flows across all three members: %v", before)
	}

	t1 := t0.Add(time.Second)
	l.LinkChange(t1, "2", false)

	for i, f := range frames {
		member, ok := selectOK(l, t1, "lag1", f, 0)
		if !ok {
			t.Fatalf("Select failed after fault for flow %d", i)
		}
		switch before[i] {
		case "2":
			if member == "2" {
				t.Errorf("flow %d stayed on faulted member 2", i)
			}
		default:
			if member != before[i] {
				t.Errorf("flow %d moved from %q to %q, want unchanged (it was not on the faulted member)", i, before[i], member)
			}
		}
	}
}

// TestActiveBackupDoesNotFailBack is evidence that, without a configured
// Primary, active-backup keeps the last active member rather than failing
// back once it recovers; with a Primary, it returns to it.
func TestActiveBackupDoesNotFailBack(t *testing.T) {
	t.Parallel()

	t.Run("without Primary stays on the backup after recovery", func(t *testing.T) {
		t.Parallel()
		tbl := lagTwoPortTable(t)
		mac := mustMAC(t, "02:00:00:00:00:0a")
		l := mustNewLAG(t, lag.Config{LAGs: map[string]lag.LAG{"lag1": {Mode: lag.ActiveBackup}}}, tbl, mac)
		t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		l.LinkChange(t0, "1/1/1", true)
		l.LinkChange(t0, "1/1/2", true)

		f := ethernet.Frame{}
		if chosen, ok := selectOK(l, t0, "lag1", f, 0); !ok || chosen != "1/1/1" {
			t.Fatalf("initial Select = (%q, %t), want (1/1/1, true)", chosen, ok)
		}

		t1 := t0.Add(time.Second)
		l.LinkChange(t1, "1/1/1", false)
		if chosen, ok := selectOK(l, t1, "lag1", f, 0); !ok || chosen != "1/1/2" {
			t.Fatalf("Select after fault = (%q, %t), want (1/1/2, true)", chosen, ok)
		}

		t2 := t1.Add(time.Second)
		l.LinkChange(t2, "1/1/1", true)
		if chosen, ok := selectOK(l, t2, "lag1", f, 0); !ok || chosen != "1/1/2" {
			t.Fatalf("Select after recovery = (%q, %t), want (1/1/2, true): failed back without a configured Primary", chosen, ok)
		}
	})

	t.Run("with Primary returns to it", func(t *testing.T) {
		t.Parallel()
		tbl := lagTwoPortTable(t)
		mac := mustMAC(t, "02:00:00:00:00:0a")
		l := mustNewLAG(t, lag.Config{LAGs: map[string]lag.LAG{"lag1": {Mode: lag.ActiveBackup, Primary: "1/1/1"}}}, tbl, mac)
		t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		l.LinkChange(t0, "1/1/1", true)
		l.LinkChange(t0, "1/1/2", true)

		f := ethernet.Frame{}
		selectOK(l, t0, "lag1", f, 0)

		t1 := t0.Add(time.Second)
		l.LinkChange(t1, "1/1/1", false)
		selectOK(l, t1, "lag1", f, 0)

		t2 := t1.Add(time.Second)
		l.LinkChange(t2, "1/1/1", true)
		if chosen, ok := selectOK(l, t2, "lag1", f, 0); !ok || chosen != "1/1/1" {
			t.Fatalf("Select after recovery with Primary = (%q, %t), want (1/1/1, true)", chosen, ok)
		}
	})
}

// TestDeterministicAcrossSeparatelyConstructedLayers is evidence that
// replaying the same explicit sequence on two separately constructed layers
// yields the same bucket table, selections, and facts.
func TestDeterministicAcrossSeparatelyConstructedLayers(t *testing.T) {
	t.Parallel()

	cfg := lag.Config{LAGs: map[string]lag.LAG{"lag1": {Mode: lag.BalanceSLB}}}
	tbl := lagTwoPortTable(t)
	mac := mustMAC(t, "02:00:00:00:00:01")
	dstMAC := mustMAC(t, "02:00:00:00:00:99")

	run := func() ([]string, []string) {
		l := mustNewLAG(t, cfg, tbl, mac)
		t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		l.LinkChange(t0, "1/1/1", true)
		l.LinkChange(t0, "1/1/2", true)

		var selections, facts []string
		for i := 0; i < 16; i++ {
			f := ethernet.Frame{Src: netaddr.MAC{0x02, 0, 0, 0, 0, byte(i)}, Dst: dstMAC, EtherType: ethernet.EtherTypeIPv4}
			sel := l.Select(t0, "lag1", f, 0)
			selections = append(selections, sel.Member)
			facts = append(facts, l.SelectionFact("lag1", f, 0, sel).Canonical())
		}

		return selections, facts
	}

	selA, factsA := run()
	selB, factsB := run()

	if !slices.Equal(selA, selB) {
		t.Fatalf("selections differ across separately constructed layers: %v vs %v", selA, selB)
	}
	if !slices.Equal(factsA, factsB) {
		t.Fatalf("facts differ across separately constructed layers: %v vs %v", factsA, factsB)
	}
}

// TestFaultInOneLAGLeavesAnotherUnchanged is evidence that a fault in one LAG
// leaves a second LAG on the same layer's buckets, active member, and Info
// unchanged.
func TestFaultInOneLAGLeavesAnotherUnchanged(t *testing.T) {
	t.Parallel()

	tbl, err := port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1"}).
		Add(port.Port{Name: "lag2", Kind: port.Lag}).
		Add(port.Port{Name: "2/1/1", Kind: port.Physical, LagParent: "lag2"}).
		Add(port.Port{Name: "2/1/2", Kind: port.Physical, LagParent: "lag2"}).
		Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}
	mac := mustMAC(t, "02:00:00:00:00:01")
	cfg := lag.Config{LAGs: map[string]lag.LAG{
		"lag1": {Mode: lag.BalanceSLB},
		"lag2": {Mode: lag.BalanceSLB},
	}}
	l := mustNewLAG(t, cfg, tbl, mac)
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l.LinkChange(t0, "1/1/1", true)
	l.LinkChange(t0, "1/1/2", true)
	l.LinkChange(t0, "2/1/1", true)
	l.LinkChange(t0, "2/1/2", true)

	dstMAC := mustMAC(t, "02:00:00:00:00:99")
	frames := make([]ethernet.Frame, 8)
	lag2Before := make([]string, 8)
	for i := range frames {
		frames[i] = ethernet.Frame{Src: netaddr.MAC{0x02, 0, 0, 0, 0, byte(i)}, Dst: dstMAC, EtherType: ethernet.EtherTypeIPv4}
		member, ok := selectOK(l, t0, "lag2", frames[i], 0)
		if !ok {
			t.Fatalf("Select failed for lag2 flow %d", i)
		}
		lag2Before[i] = member
	}
	infoBefore := l.Info("lag2")

	l.LinkChange(t0.Add(time.Second), "1/1/1", false)

	infoAfter := l.Info("lag2")
	if !reflect.DeepEqual(infoBefore, infoAfter) {
		t.Fatalf("lag2 Info changed after lag1's fault: before %+v, after %+v", infoBefore, infoAfter)
	}
	for i, f := range frames {
		member, ok := selectOK(l, t0.Add(time.Second), "lag2", f, 0)
		if !ok || member != lag2Before[i] {
			t.Errorf("lag2 flow %d = (%q, %t), want %q unchanged after lag1's fault", i, member, ok, lag2Before[i])
		}
	}
}

// TestPeekCommitsNothing is evidence that two non-committing lookups
// followed by a committing one select the same member, with the same cause,
// as a lone committing call on a fresh layer.
func TestPeekCommitsNothing(t *testing.T) {
	t.Parallel()

	tbl := lagTwoPortTable(t)
	mac := mustMAC(t, "02:00:00:00:00:01")
	cfg := lag.Config{LAGs: map[string]lag.LAG{"lag1": {Mode: lag.BalanceSLB}}}
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	frame := ethernet.Frame{Src: mustMAC(t, "02:00:00:00:00:aa"), Dst: mustMAC(t, "02:00:00:00:00:bb"), EtherType: ethernet.EtherTypeIPv4}

	fresh := mustNewLAG(t, cfg, tbl, mac)
	fresh.LinkChange(t0, "1/1/1", true)
	fresh.LinkChange(t0, "1/1/2", true)
	want := fresh.Select(t0, "lag1", frame, 0)
	if want.Cause != lag.CauseFirstUse {
		t.Fatalf("baseline Select cause = %v, want first-use", want.Cause)
	}

	peeked := mustNewLAG(t, cfg, tbl, mac)
	peeked.LinkChange(t0, "1/1/1", true)
	peeked.LinkChange(t0, "1/1/2", true)
	peek1 := peeked.Peek(t0, "lag1", frame, 0)
	peek2 := peeked.Peek(t0, "lag1", frame, 0)
	if peek1.Member != peek2.Member || peek1.Cause != peek2.Cause {
		t.Fatalf("two Peeks disagreed: %+v vs %+v", peek1, peek2)
	}
	if peek1.Cause != lag.CauseFirstUse {
		t.Fatalf("Peek cause = %v, want first-use (Peek must see the same uncommitted bucket each time)", peek1.Cause)
	}

	got := peeked.Select(t0, "lag1", frame, 0)
	if got.Member != want.Member || got.Cause != want.Cause {
		t.Fatalf("Select after two Peeks = %+v, want %+v: Peek committed state", got, want)
	}
}

// TestRebalanceUnmodeledSignal is evidence that a balanced selection whose
// bucket is at least one rebalance interval old, with two or more enabled
// members, reports RebalanceUnmodeled; a fresh bucket or a disabled interval
// does not.
func TestRebalanceUnmodeledSignal(t *testing.T) {
	t.Parallel()

	tbl := lagTwoPortTable(t)
	mac := mustMAC(t, "02:00:00:00:00:01")
	interval := 10 * time.Second
	cfg := lag.Config{LAGs: map[string]lag.LAG{"lag1": {Mode: lag.BalanceSLB, RebalanceInterval: &interval}}}
	l := mustNewLAG(t, cfg, tbl, mac)
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l.LinkChange(t0, "1/1/1", true)
	l.LinkChange(t0, "1/1/2", true)

	frame := ethernet.Frame{Src: mustMAC(t, "02:00:00:00:00:aa"), Dst: mustMAC(t, "02:00:00:00:00:bb"), EtherType: ethernet.EtherTypeIPv4}

	sel0 := l.Select(t0, "lag1", frame, 0)
	if !sel0.OK || sel0.RebalanceUnmodeled {
		t.Fatalf("first assignment at t0 = %+v, want RebalanceUnmodeled false", sel0)
	}

	sel9 := l.Select(t0.Add(9*time.Second), "lag1", frame, 0)
	if sel9.RebalanceUnmodeled {
		t.Fatalf("selection at t0+9s = %+v, want RebalanceUnmodeled false", sel9)
	}

	sel10 := l.Select(t0.Add(10*time.Second), "lag1", frame, 0)
	if !sel10.RebalanceUnmodeled {
		t.Fatalf("selection at t0+10s = %+v, want RebalanceUnmodeled true", sel10)
	}

	zero := time.Duration(0)
	cfgDisabled := lag.Config{LAGs: map[string]lag.LAG{"lag1": {Mode: lag.BalanceSLB, RebalanceInterval: &zero}}}
	d := mustNewLAG(t, cfgDisabled, tbl, mac)
	d.LinkChange(t0, "1/1/1", true)
	d.LinkChange(t0, "1/1/2", true)
	d.Select(t0, "lag1", frame, 0)
	if selDisabled := d.Select(t0.Add(10*time.Second), "lag1", frame, 0); selDisabled.RebalanceUnmodeled {
		t.Fatalf("selection with RebalanceInterval=0 at t0+10s = %+v, want RebalanceUnmodeled false", selDisabled)
	}
}

// TestEnableOrderTies is evidence that members enabled together in one call
// join the back of the enabled list in name order
// (bond_enable_member: ovs_list_insert before the list head, applied once
// per member in the call's iteration order).
func TestEnableOrderTies(t *testing.T) {
	t.Parallel()

	tbl := lagNamedPortTable(t, "mc", "ma", "mb")
	mac := mustMAC(t, "02:00:00:00:00:01")
	cfg := lag.Config{LAGs: map[string]lag.LAG{"lag1": {MinLinks: 3}}}
	l := mustNewLAG(t, cfg, tbl, mac)
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

	l.LinkChange(t0, "mc", true)
	l.LinkChange(t0, "ma", true)
	if info := l.Info("lag1"); len(info.Enabled) != 0 {
		t.Fatalf("Enabled before the minimum is met = %v, want none", info.Enabled)
	}

	// All three cross MinLinks in this one call and so join together; name
	// order, not call order ("mc" then "ma" then "mb"), decides the tail.
	l.LinkChange(t0, "mb", true)
	info := l.Info("lag1")
	want := []string{"ma", "mb", "mc"}
	if !slices.Equal(info.Enabled, want) {
		t.Fatalf("Enabled once all three join at once = %v, want %v (name order)", info.Enabled, want)
	}
}

func findPending(pending []lag.Pending, member string) *lag.Pending {
	for i := range pending {
		if pending[i].Member == member {
			return &pending[i]
		}
	}

	return nil
}

// TestPendingForEachCause is evidence that Info.Pending reports a member for
// each of the three documented reasons it may still change state on its own.
func TestPendingForEachCause(t *testing.T) {
	t.Parallel()

	t.Run("link delay", func(t *testing.T) {
		t.Parallel()
		tbl := lagTwoPortTable(t)
		mac := mustMAC(t, "02:00:00:00:00:01")
		cfg := lag.Config{LAGs: map[string]lag.LAG{"lag1": {UpDelay: 2 * time.Second}}}
		l := mustNewLAG(t, cfg, tbl, mac)
		t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		l.LinkChange(t0, "1/1/1", true)

		found := findPending(l.Info("lag1").Pending, "1/1/1")
		if found == nil || found.Cause != lag.PendingLinkDelay || !found.At.Equal(t0.Add(2*time.Second)) {
			t.Fatalf("Pending = %+v, want link-delay at %v", found, t0.Add(2*time.Second))
		}
	})

	t.Run("partner expired", func(t *testing.T) {
		t.Parallel()
		tbl := lagTwoPortTable(t)
		mac := mustMAC(t, "02:00:00:00:00:0a")
		cfg := lag.Config{LAGs: map[string]lag.LAG{"lag1": {LACP: lag.LACPConfig{Mode: lag.Active, Fast: true}}}}
		l := mustNewLAG(t, cfg, tbl, mac)
		t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		l.LinkChange(t0, "1/1/1", true)
		l.Wake(t0.Add(3 * time.Second))

		found := findPending(l.Info("lag1").Pending, "1/1/1")
		if found == nil || found.Cause != lag.PendingPartnerExpired || !found.At.After(t0.Add(3*time.Second)) {
			t.Fatalf("Pending = %+v, want partner-expired after t0+3s", found)
		}
	})

	t.Run("attached without synchronization", func(t *testing.T) {
		t.Parallel()
		tbl := lagTwoPortTable(t)
		mac := mustMAC(t, "02:00:00:00:00:0a")
		cfg := lag.Config{LAGs: map[string]lag.LAG{"lag1": {LACP: lag.LACPConfig{Mode: lag.Active, Fast: true}}}}
		l := mustNewLAG(t, cfg, tbl, mac)
		t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		l.LinkChange(t0, "1/1/1", true)

		partner := lacp.Info{
			SystemPriority: 1,
			SystemID:       mustMAC(t, "02:00:00:00:00:0b"),
			Key:            1,
			PortPriority:   1,
			PortID:         1,
			State:          lacp.StateAggregation, // no StateSynchronization
		}
		l.Receive(t0, "1/1/1", lacp.PDU{Actor: partner})

		info := l.Info("lag1")
		found := findPending(info.Pending, "1/1/1")
		if found == nil || found.Cause != lag.PendingUnsynchronized {
			t.Fatalf("Pending = %+v, want unsynchronized", found)
		}
		if len(info.Attached) != 1 || info.Attached[0] != "1/1/1" || len(info.Enabled) != 0 {
			t.Fatalf("Attached = %v, Enabled = %v, want attached and not enabled", info.Attached, info.Enabled)
		}
	})
}

// TestCloneIsolatesBucketTable is evidence that Clone deep-copies the bucket
// table: mutating the clone's buckets leaves the original's unchanged.
func TestCloneIsolatesBucketTable(t *testing.T) {
	t.Parallel()

	tbl := lagTwoPortTable(t)
	mac := mustMAC(t, "02:00:00:00:00:01")
	cfg := lag.Config{LAGs: map[string]lag.LAG{"lag1": {Mode: lag.BalanceSLB}}}
	l1 := mustNewLAG(t, cfg, tbl, mac)
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l1.LinkChange(t0, "1/1/1", true)
	l1.LinkChange(t0, "1/1/2", true)

	frame := ethernet.Frame{Src: mustMAC(t, "02:00:00:00:00:aa"), Dst: mustMAC(t, "02:00:00:00:00:bb"), EtherType: ethernet.EtherTypeIPv4}
	before := l1.Select(t0, "lag1", frame, 0)

	l2 := l1.Clone()
	t1 := t0.Add(time.Second)
	// Disable the member the bucket is on, in the clone only, forcing its
	// bucket to reassign there.
	l2.LinkChange(t1, before.Member, false)
	l2.Select(t1, "lag1", frame, 0)

	after := l1.Select(t1, "lag1", frame, 0)
	if after.Member != before.Member || after.Cause != lag.CauseKept {
		t.Fatalf("original layer's bucket changed after mutating the clone: got %+v, want member %q kept", after, before.Member)
	}
}
