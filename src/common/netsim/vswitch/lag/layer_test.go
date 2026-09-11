package lag_test

import (
	"encoding/binary"
	"fmt"
	"net/netip"
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
	l := lag.New(cfg, tbl, sysMAC)
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

		firstChoice, ok := l.Select("lag1", f, vlan.ID(10))
		if !ok {
			t.Fatalf("Select failed for src %v in VLAN 10", srcMAC)
		}
		membersSeen[firstChoice] = true

		for repeat := 0; repeat < 5; repeat++ {
			choice, ok := l.Select("lag1", f, vlan.ID(10))
			if !ok || choice != firstChoice {
				t.Fatalf("Select inconsistent for src %v: got (%q, %t), want (%q, true)", srcMAC, choice, ok, firstChoice)
			}
		}

		choiceV20, ok := l.Select("lag1", f, vlan.ID(20))
		if !ok {
			t.Fatalf("Select failed for src %v in VLAN 20", srcMAC)
		}
		choiceV20Repeat, ok := l.Select("lag1", f, vlan.ID(20))
		if !ok || choiceV20Repeat != choiceV20 {
			t.Fatalf("Select in VLAN 20 inconsistent for src %v: got %q, want %q", srcMAC, choiceV20Repeat, choiceV20)
		}
	}

	if len(membersSeen) < 2 {
		t.Fatalf("BalanceSLB did not balance across multiple members: only saw %v", membersSeen)
	}
}

func TestBalanceTCP(t *testing.T) {
	t.Parallel()

	tbl := lagTwoPortTable(t)
	sysMAC := mustMAC(t, "02:00:00:00:00:01")
	cfg := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode: lag.BalanceTCP,
			},
		},
	}
	l := lag.New(cfg, tbl, sysMAC)
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l.LinkChange(t0, "1/1/1", true)
	l.LinkChange(t0, "1/1/2", true)

	srcMAC := mustMAC(t, "02:00:00:00:00:01")
	dstMAC := mustMAC(t, "02:00:00:00:00:02")

	membersSeen := make(map[string]bool)
	for port := uint16(40000); port <= 40007; port++ {
		f := makeUDPFrame(t, srcMAC, dstMAC, "10.0.0.1", "10.0.0.2", port, 5000, false)
		firstChoice, ok := l.Select("lag1", f, 0)
		if !ok {
			t.Fatalf("Select failed for UDP port %d", port)
		}
		membersSeen[firstChoice] = true

		for repeat := 0; repeat < 5; repeat++ {
			choice, ok := l.Select("lag1", f, 0)
			if !ok || choice != firstChoice {
				t.Fatalf("Select inconsistent for UDP port %d: got (%q, %t), want (%q, true)", port, choice, ok, firstChoice)
			}
		}
	}

	if len(membersSeen) < 2 {
		t.Fatalf("BalanceTCP did not balance across multiple members: only saw %v", membersSeen)
	}

	badFrame := makeUDPFrame(t, srcMAC, dstMAC, "10.0.0.1", "10.0.0.2", 40000, 5000, true)
	badChoice, ok := l.Select("lag1", badFrame, 0)
	if !ok {
		t.Fatal("Select failed for bad checksum frame")
	}

	arpFrame := makeARPFrame(srcMAC, dstMAC)
	arpChoice, ok := l.Select("lag1", arpFrame, 0)
	if !ok {
		t.Fatal("Select failed for ARP frame")
	}

	if badChoice != arpChoice {
		t.Errorf("bad IPv4 checksum frame selected %q, want same as ARP frame %q", badChoice, arpChoice)
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
	l := lag.New(cfg, tbl, sysMAC)
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l.LinkChange(t0, "1/1/1", true)
	l.LinkChange(t0, "1/1/2", true)

	f := ethernet.Frame{}
	chosen, ok := l.Select("lag1", f, 0)
	if !ok || chosen != "1/1/1" {
		t.Fatalf("Select = (%q, %t), want (1/1/1, true)", chosen, ok)
	}

	t1 := t0.Add(time.Second)
	fx := l.LinkChange(t1, "1/1/1", false)
	if !slices.Contains(fx.Changed, "lag1") {
		t.Fatalf("Changed does not contain lag1: %v", fx.Changed)
	}

	chosen2, ok := l.Select("lag1", f, 0)
	if !ok || chosen2 != "1/1/2" {
		t.Fatalf("Select after 1/1/1 down = (%q, %t), want (1/1/2, true)", chosen2, ok)
	}

	l.LinkChange(t1.Add(time.Second), "1/1/2", false)
	_, ok = l.Select("lag1", f, 0)
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
	l := lag.New(cfg, tbl, sysMAC)
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

	chosen, ok := l.Select("lag1", f, 0)
	if !ok || chosen != "1/1/1" {
		t.Fatalf("Select at t0+2s = (%q, %t), want (1/1/1, true)", chosen, ok)
	}

	t1 := t0.Add(10 * time.Second)
	l.LinkChange(t1, "1/1/1", false)

	// Still returns 1/1/1 before down delay passes.
	chosen, ok = l.Select("lag1", f, 0)
	if !ok || chosen != "1/1/1" {
		t.Fatalf("Select before down delay elapses = (%q, %t), want (1/1/1, true)", chosen, ok)
	}

	next, okTimer := l.NextWake()
	if !okTimer || next != t1.Add(1*time.Second) {
		t.Fatalf("NextWake = (%v, %v), want (%v, true)", next, okTimer, t1.Add(1*time.Second))
	}

	l.Wake(t1.Add(1 * time.Second))
	chosen, ok = l.Select("lag1", f, 0)
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

	layerA := lag.New(cfgA, tblA, macA)
	layerB := lag.New(cfgB, tblB, macB)

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
	layerA2 := lag.New(cfgA, tblA, macA)
	layerB2 := lag.New(cfgBKey2, tblB, macB)

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
		l := lag.New(cfg, tblA, macA)
		t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		l.LinkChange(t0, "1/1/1", true)
		l.LinkChange(t0, "1/1/2", true)

		// Before 6s, members are not enabled by fallback.
		l.Wake(t0.Add(3 * time.Second))
		if l.PortInfo("1/1/1").Status != lag.Expired {
			t.Fatalf("status at 3s = %v, want Expired", l.PortInfo("1/1/1").Status)
		}
		if _, ok := l.Select("lag1", ethernet.Frame{}, 0); ok {
			t.Fatal("Select succeeded at 3s before defaulting, want false")
		}

		l.Wake(t0.Add(6 * time.Second))
		if l.PortInfo("1/1/1").Status != lag.Defaulted {
			t.Fatalf("status at 6s = %v, want Defaulted", l.PortInfo("1/1/1").Status)
		}
		chosen, ok := l.Select("lag1", ethernet.Frame{}, 0)
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
		l := lag.New(cfg, tblA, macA)
		t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		l.LinkChange(t0, "1/1/1", true)
		l.LinkChange(t0, "1/1/2", true)

		l.Wake(t0.Add(6 * time.Second))
		if _, ok := l.Select("lag1", ethernet.Frame{}, 0); ok {
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
		lA := lag.New(cfgA, tblA, macA)
		lB := lag.New(cfgB, tblB, macB)

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
		lA := lag.New(cfg, tblA, macA)
		lB := lag.New(cfg, tblB, macB)

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
	layerA := lag.New(cfg, tblA, macA)
	layerB := lag.New(cfg, tblB, macB)

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
	l1 := lag.New(cfg, tbl, mac)
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
