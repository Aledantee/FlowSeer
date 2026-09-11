package fabric_test

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func newLagTopology(t *testing.T, start time.Time, lagA, lagB *lag.Config) (*fabric.Fabric, netaddr.MAC, netaddr.MAC) {
	t.Helper()

	vid10 := vlan.ID(10)
	buildPorts := func() port.Table {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
		b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		tbl, err := b.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}
		return tbl
	}

	bridgeCfg := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{
					10: "VLAN10",
				},
				Switchports: map[string]bridge.Switchport{
					"1/1/3": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/4": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"lag1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
				},
			},
		}
	}

	macH1 := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
	macH2 := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x02}

	cfg := fabric.Config{
		Start: start,
		Switches: map[string]vswitch.Config{
			"A": {
				Ports:  buildPorts(),
				Bridge: bridgeCfg(),
				LAG:    lagA,
			},
			"B": {
				Ports:  buildPorts(),
				Bridge: bridgeCfg(),
				LAG:    lagB,
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "A", Port: "1/1/1"}, B: fabric.Endpoint{Node: "B", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "A", Port: "1/1/2"}, B: fabric.Endpoint{Node: "B", Port: "1/1/2"}},
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "A", Port: "1/1/3"}},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "B", Port: "1/1/3"}},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	return fab, macH1, macH2
}

func TestBalanceSLB(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	lagA := &lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode: lag.BalanceSLB,
			},
		},
	}
	fab, _, macH2 := newLagTopology(t, t0, lagA, nil)

	sources := []netaddr.MAC{
		{0x02, 0x00, 0x00, 0x00, 0x00, 0xa1},
		{0x02, 0x00, 0x00, 0x00, 0x00, 0xa2},
		{0x02, 0x00, 0x00, 0x00, 0x00, 0xa3},
		{0x02, 0x00, 0x00, 0x00, 0x00, 0xa4},
		{0x02, 0x00, 0x00, 0x00, 0x00, 0xa5},
		{0x02, 0x00, 0x00, 0x00, 0x00, 0xa6},
		{0x02, 0x00, 0x00, 0x00, 0x00, 0xa7},
		{0x02, 0x00, 0x00, 0x00, 0x00, 0xa8},
	}

	cablesUsed := make(map[string]bool)
	sourceCables := make(map[netaddr.MAC]string)

	for i, src := range sources {
		for rep := 0; rep < 2; rep++ {
			injTime := t0.Add(time.Duration(i*10+rep) * time.Millisecond)
			f := ethernet.Frame{
				Src:     src,
				Dst:     macH2,
				Payload: []byte("slb-test"),
			}
			fid, err := fab.Inject(fabric.Injection{
				At:     injTime,
				Origin: fabric.Endpoint{Node: "h1"},
				Frame:  f,
			})
			if err != nil {
				t.Fatalf("Inject src %v rep %d: %v", src, rep, err)
			}
			fab.Run(10)

			var j *fabric.Journey
			for k := range fab.Report() {
				if fab.Report()[k].FrameID == fid {
					j = &fab.Report()[k]
					break
				}
			}
			if j == nil {
				t.Fatalf("missing journey for fid %d", fid)
			}

			var crossed string
			for _, e := range j.Entries {
				if e.Kind == fabric.EntryCrossing && e.Cable != nil && e.Cable.A.Node == "A" {
					crossed = e.Cable.A.Port
					break
				}
			}
			if crossed == "" {
				t.Fatalf("frame from %v did not cross from A: %+v", src, j.Entries)
			}

			if prev, ok := sourceCables[src]; ok {
				if prev != crossed {
					t.Fatalf("source %v crossed %q on rep 0 but %q on rep %d", src, prev, crossed, rep)
				}
			} else {
				sourceCables[src] = crossed
				cablesUsed[crossed] = true
			}
		}
	}

	if len(cablesUsed) < 2 {
		t.Fatalf("expected at least one pair of sources to cross different cables, all used %v", cablesUsed)
	}
}

func TestBalanceTCP(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	lagA := &lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode: lag.BalanceTCP,
			},
		},
	}
	fab, macH1, macH2 := newLagTopology(t, t0, lagA, nil)

	ports := []uint16{40000, 40001, 40002, 40003, 40004, 40005, 40006, 40007}
	cablesUsed := make(map[string]bool)
	portCables := make(map[uint16]string)

	for i, srcPort := range ports {
		for rep := 0; rep < 2; rep++ {
			injTime := t0.Add(time.Duration(i*10+rep) * time.Millisecond)

			udpPayload := make([]byte, 8)
			binary.BigEndian.PutUint16(udpPayload[0:2], srcPort)
			binary.BigEndian.PutUint16(udpPayload[2:4], 5000)
			binary.BigEndian.PutUint16(udpPayload[4:6], 8)

			ipHdr := ip.Header{
				Src:      netip.MustParseAddr("192.168.1.1"),
				Dst:      netip.MustParseAddr("192.168.1.2"),
				HopLimit: 64,
				Protocol: 17,
				V4:       &ip.V4{},
			}
			ipPayload, err := ipHdr.Encode(udpPayload)
			if err != nil {
				t.Fatalf("encode IPv4: %v", err)
			}

			f := ethernet.Frame{
				Src:       macH1,
				Dst:       macH2,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   ipPayload,
			}

			fid, err := fab.Inject(fabric.Injection{
				At:     injTime,
				Origin: fabric.Endpoint{Node: "h1"},
				Frame:  f,
			})
			if err != nil {
				t.Fatalf("Inject port %d rep %d: %v", srcPort, rep, err)
			}
			fab.Run(10)

			var j *fabric.Journey
			for k := range fab.Report() {
				if fab.Report()[k].FrameID == fid {
					j = &fab.Report()[k]
					break
				}
			}
			if j == nil {
				t.Fatalf("missing journey for fid %d", fid)
			}

			var crossed string
			for _, e := range j.Entries {
				if e.Kind == fabric.EntryCrossing && e.Cable != nil && e.Cable.A.Node == "A" {
					crossed = e.Cable.A.Port
					break
				}
			}
			if crossed == "" {
				t.Fatalf("flow %d did not cross from A: %+v", srcPort, j.Entries)
			}

			if prev, ok := portCables[srcPort]; ok {
				if prev != crossed {
					t.Fatalf("flow %d crossed %q on rep 0 but %q on rep %d", srcPort, prev, crossed, rep)
				}
			} else {
				portCables[srcPort] = crossed
				cablesUsed[crossed] = true
			}
		}
	}

	if len(cablesUsed) < 2 {
		t.Fatalf("expected at least one pair of source ports to cross different cables, all used %v", cablesUsed)
	}
}

func TestActiveBackupFailover(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	fab, macH1, macH2 := newLagTopology(t, t0, nil, nil)

	// Inject frame 1 from h1 to h2 at t0.
	f1 := ethernet.Frame{
		Src:     macH1,
		Dst:     macH2,
		Payload: []byte("frame-1"),
	}
	fid1, err := fab.Inject(fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  f1,
	})
	if err != nil {
		t.Fatalf("Inject f1: %v", err)
	}

	fab.Run(10)

	j1 := fab.Report()[0]
	if j1.FrameID != fid1 {
		t.Fatalf("expected journey for frame 1, got frame %d", j1.FrameID)
	}
	var crossed1 string
	for _, entry := range j1.Entries {
		if entry.Kind == fabric.EntryCrossing && entry.Cable != nil && entry.Cable.A.Node == "A" {
			crossed1 = entry.Cable.A.Port
		}
	}
	if crossed1 != "1/1/1" {
		t.Fatalf("frame 1 crossed port %q, want 1/1/1", crossed1)
	}

	// Cut the first cable at t1.
	t1 := t0.Add(time.Second)
	err = fab.SetFault(
		fabric.Endpoint{Node: "A", Port: "1/1/1"},
		fabric.Endpoint{Node: "B", Port: "1/1/1"},
		fabric.Fault{Kind: fabric.FaultCut},
	)
	if err != nil {
		t.Fatalf("SetFault: %v", err)
	}

	// Next frame from h1 at t1 + 10ms should cross 1/1/2.
	f2 := ethernet.Frame{
		Src:     macH1,
		Dst:     macH2,
		Payload: []byte("frame-2"),
	}
	fid2, err := fab.Inject(fabric.Injection{
		At:     t1.Add(10 * time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  f2,
	})
	if err != nil {
		t.Fatalf("Inject f2: %v", err)
	}

	fab.Run(10)

	var j2 *fabric.Journey
	for i := range fab.Report() {
		if fab.Report()[i].FrameID == fid2 {
			j2 = &fab.Report()[i]
			break
		}
	}
	if j2 == nil {
		t.Fatalf("missing journey for frame 2")
	}

	var crossed2 string
	for _, entry := range j2.Entries {
		if entry.Kind == fabric.EntryCrossing && entry.Cable != nil && entry.Cable.A.Node == "A" {
			crossed2 = entry.Cable.A.Port
		}
	}
	if crossed2 != "1/1/2" {
		t.Fatalf("frame 2 crossed port %q, want 1/1/2", crossed2)
	}

	snap := fab.Snapshot()
	portsA := snap.Devices["A"].Ports
	var lag1Port *port.Port
	for i := range portsA {
		if portsA[i].Name == "lag1" {
			lag1Port = &portsA[i]
			break
		}
	}
	if lag1Port == nil {
		t.Fatal("lag1 port not found in snapshot for A")
	}
	if lag1Port.OperStatus != port.Up {
		t.Fatalf("lag1 OperStatus = %v, want Up", lag1Port.OperStatus)
	}
}

func TestDelays(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	lagA := &lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode:      lag.ActiveBackup,
				Primary:   "1/1/1",
				UpDelay:   2 * time.Second,
				DownDelay: 1 * time.Second,
			},
		},
	}
	fab, macH1, macH2 := newLagTopology(t, t0, lagA, nil)

	f := ethernet.Frame{
		Src:     macH1,
		Dst:     macH2,
		Payload: []byte("delays-test"),
	}

	// Wait for initial up-delay (2s) so 1/1/1 and 1/1/2 become enabled.
	fab.Run(10)
	t1 := fab.Snapshot().Clock // t0 + 2s

	// Cut first cable at t1.
	if err := fab.SetFault(
		fabric.Endpoint{Node: "A", Port: "1/1/1"},
		fabric.Endpoint{Node: "B", Port: "1/1/1"},
		fabric.Fault{Kind: fabric.FaultCut},
	); err != nil {
		t.Fatalf("SetFault cut: %v", err)
	}

	// Frame at t1 + 0.5s records EntryLoss with cable-loss on first cable.
	fidLoss, err := fab.Inject(fabric.Injection{
		At:     t1.Add(500 * time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  f,
	})
	if err != nil {
		t.Fatalf("Inject at t1+0.5s: %v", err)
	}
	fab.Run(10)

	var jLoss *fabric.Journey
	for i := range fab.Report() {
		if fab.Report()[i].FrameID == fidLoss {
			jLoss = &fab.Report()[i]
			break
		}
	}
	if jLoss == nil {
		t.Fatalf("missing journey for fidLoss")
	}
	var hadLoss bool
	for _, e := range jLoss.Entries {
		if e.Kind == fabric.EntryLoss && e.Reason == fabric.ReasonCableLoss && e.Cable != nil && e.Cable.A.Port == "1/1/1" {
			hadLoss = true
			break
		}
	}
	if !hadLoss {
		t.Fatalf("frame at t1+0.5s missing EntryLoss with cable-loss on 1/1/1: %+v", jLoss.Entries)
	}

	// Frame at t1 + 1.5s crosses the second cable.
	fidCross2, err := fab.Inject(fabric.Injection{
		At:     t1.Add(1500 * time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  f,
	})
	if err != nil {
		t.Fatalf("Inject at t1+1.5s: %v", err)
	}
	fab.Run(10)

	var jCross2 *fabric.Journey
	for i := range fab.Report() {
		if fab.Report()[i].FrameID == fidCross2 {
			jCross2 = &fab.Report()[i]
			break
		}
	}
	if jCross2 == nil {
		t.Fatalf("missing journey for fidCross2")
	}
	var crossed string
	for _, e := range jCross2.Entries {
		if e.Kind == fabric.EntryCrossing && e.Cable != nil && e.Cable.A.Node == "A" {
			crossed = e.Cable.A.Port
			break
		}
	}
	if crossed != "1/1/2" {
		t.Fatalf("frame at t1+1.5s crossed %q, want 1/1/2", crossed)
	}

	// Restore cable at t2.
	t2 := fab.Snapshot().Clock
	if err := fab.SetFault(
		fabric.Endpoint{Node: "A", Port: "1/1/1"},
		fabric.Endpoint{Node: "B", Port: "1/1/1"},
		fabric.Fault{Kind: fabric.FaultNone},
	); err != nil {
		t.Fatalf("SetFault restore: %v", err)
	}

	// Frame at t2 + 1s still crosses second cable.
	fidStill2, err := fab.Inject(fabric.Injection{
		At:     t2.Add(1 * time.Second),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  f,
	})
	if err != nil {
		t.Fatalf("Inject at t2+1s: %v", err)
	}
	fab.Run(10)

	var jStill2 *fabric.Journey
	for i := range fab.Report() {
		if fab.Report()[i].FrameID == fidStill2 {
			jStill2 = &fab.Report()[i]
			break
		}
	}
	if jStill2 == nil {
		t.Fatalf("missing journey for fidStill2")
	}
	crossed = ""
	for _, e := range jStill2.Entries {
		if e.Kind == fabric.EntryCrossing && e.Cable != nil && e.Cable.A.Node == "A" {
			crossed = e.Cable.A.Port
			break
		}
	}
	if crossed != "1/1/2" {
		t.Fatalf("frame at t2+1s crossed %q, want 1/1/2", crossed)
	}

	// Frame at t2 + 2.5s crosses first cable (primary).
	fidPrimary, err := fab.Inject(fabric.Injection{
		At:     t2.Add(2500 * time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  f,
	})
	if err != nil {
		t.Fatalf("Inject at t2+2.5s: %v", err)
	}
	fab.Run(10)

	var jPrimary *fabric.Journey
	for i := range fab.Report() {
		if fab.Report()[i].FrameID == fidPrimary {
			jPrimary = &fab.Report()[i]
			break
		}
	}
	if jPrimary == nil {
		t.Fatalf("missing journey for fidPrimary")
	}
	crossed = ""
	for _, e := range jPrimary.Entries {
		if e.Kind == fabric.EntryCrossing && e.Cable != nil && e.Cable.A.Node == "A" {
			crossed = e.Cable.A.Port
			break
		}
	}
	if crossed != "1/1/1" {
		t.Fatalf("frame at t2+2.5s crossed %q, want 1/1/1", crossed)
	}
}

func TestLACPConvergence(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	lagCfg := func() *lag.Config {
		return &lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {
					Mode: lag.ActiveBackup,
					LACP: lag.LACPConfig{
						Mode: lag.Active,
						Fast: true,
					},
				},
			},
		}
	}

	fab, _, _ := newLagTopology(t, t0, lagCfg(), lagCfg())

	// Run fabric to t0 + 3s.
	target := t0.Add(3 * time.Second)
	for {
		snap := fab.Snapshot()
		if !snap.Clock.Before(target) && len(snap.Queue) == 0 {
			break
		}
		if snap.Clock.After(target) {
			break
		}
		if _, ok := fab.Step(); !ok {
			break
		}
	}

	// Snapshot().Devices["A"].Ports shows lag1 Up.
	snap := fab.Snapshot()
	var lag1Up bool
	for _, p := range snap.Devices["A"].Ports {
		if p.Name == "lag1" && p.OperStatus == port.Up {
			lag1Up = true
			break
		}
	}
	if !lag1Up {
		t.Fatal("lag1 on switch A is not Up")
	}

	// fab.Switch("A").MemberInfo("1/1/1") reads Attached and Enabled with B's system ID as partner.
	swA := fab.Switch("A")
	swB := fab.Switch("B")
	infoA1 := swA.MemberInfo("1/1/1")
	if !infoA1.Attached {
		t.Errorf("A:1/1/1 Attached = false, want true")
	}
	if !infoA1.Enabled {
		t.Errorf("A:1/1/1 Enabled = false, want true")
	}
	if infoA1.Partner.SystemID != swB.Config().MAC {
		t.Errorf("A:1/1/1 Partner.SystemID = %v, want %v", infoA1.Partner.SystemID, swB.Config().MAC)
	}

	// LACPDU journeys are Protocol with crossings on the member cables.
	var protocolJourneys int
	for _, j := range fab.Report() {
		if !j.Protocol {
			continue
		}
		protocolJourneys++
		var hadCrossing bool
		for _, e := range j.Entries {
			if e.Kind == fabric.EntryCrossing && e.Cable != nil {
				portA := e.Cable.A.Port
				if portA == "1/1/1" || portA == "1/1/2" {
					hadCrossing = true
				}
			}
		}
		if !hadCrossing {
			t.Errorf("protocol journey %d missing crossing on member cable: %+v", j.FrameID, j.Entries)
		}
	}
	if protocolJourneys == 0 {
		t.Fatal("expected LACPDU protocol journeys, found 0")
	}

	// Key mismatch case: B's 1/1/2 with key 2 leaves A's 1/1/2 detached.
	lagBKey2 := &lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode: lag.ActiveBackup,
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
	fabMismatch, _, _ := newLagTopology(t, t0, lagCfg(), lagBKey2)
	for {
		snap := fabMismatch.Snapshot()
		if !snap.Clock.Before(target) && len(snap.Queue) == 0 {
			break
		}
		if snap.Clock.After(target) {
			break
		}
		if _, ok := fabMismatch.Step(); !ok {
			break
		}
	}

	swAMismatch := fabMismatch.Switch("A")
	info1 := swAMismatch.MemberInfo("1/1/1")
	if !info1.Attached || !info1.Enabled {
		t.Errorf("A:1/1/1 attached=%v enabled=%v, want true/true", info1.Attached, info1.Enabled)
	}
	info2 := swAMismatch.MemberInfo("1/1/2")
	if info2.Attached {
		t.Errorf("A:1/1/2 Attached = true, want false (detached due to partner key mismatch)")
	}
}

func TestFallback(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	f := ethernet.Frame{
		Src:     netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
		Dst:     netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x02},
		Payload: []byte("fallback-test"),
	}

	// Case with Fallback true: at t0+6s h1's frames cross A:1/1/1.
	lagAFallback := &lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode: lag.ActiveBackup,
				LACP: lag.LACPConfig{
					Mode:     lag.Active,
					Fast:     true,
					Fallback: true,
				},
			},
		},
	}
	fabWithFallback, _, _ := newLagTopology(t, t0, lagAFallback, nil)

	targetWith := t0.Add(6 * time.Second)
	for {
		snap := fabWithFallback.Snapshot()
		if !snap.Clock.Before(targetWith) && len(snap.Queue) == 0 {
			break
		}
		if snap.Clock.After(targetWith) {
			break
		}
		if _, ok := fabWithFallback.Step(); !ok {
			break
		}
	}

	fidWith, err := fabWithFallback.Inject(fabric.Injection{
		At:     t0.Add(6*time.Second + 10*time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  f,
	})
	if err != nil {
		t.Fatalf("Inject with fallback: %v", err)
	}
	fabWithFallback.Run(10)

	var jWith *fabric.Journey
	for i := range fabWithFallback.Report() {
		if fabWithFallback.Report()[i].FrameID == fidWith {
			jWith = &fabWithFallback.Report()[i]
			break
		}
	}
	if jWith == nil {
		t.Fatal("missing journey for fidWith")
	}
	var crossed string
	for _, e := range jWith.Entries {
		if e.Kind == fabric.EntryCrossing && e.Cable != nil && e.Cable.A.Node == "A" {
			crossed = e.Cable.A.Port
			break
		}
	}
	if crossed != "1/1/1" {
		t.Fatalf("frame with fallback crossed %q, want 1/1/1", crossed)
	}

	// Case without Fallback: at t0+7s a frame from h1 has a journey drop no-member.
	lagANoFallback := &lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode: lag.ActiveBackup,
				LACP: lag.LACPConfig{
					Mode:     lag.Active,
					Fast:     true,
					Fallback: false,
				},
			},
		},
	}
	fabNoFallback, _, _ := newLagTopology(t, t0, lagANoFallback, nil)

	targetWithout := t0.Add(7 * time.Second)
	for {
		snap := fabNoFallback.Snapshot()
		if !snap.Clock.Before(targetWithout) && len(snap.Queue) == 0 {
			break
		}
		if snap.Clock.After(targetWithout) {
			break
		}
		if _, ok := fabNoFallback.Step(); !ok {
			break
		}
	}

	fidWithout, err := fabNoFallback.Inject(fabric.Injection{
		At:     t0.Add(7*time.Second + 10*time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  f,
	})
	if err != nil {
		t.Fatalf("Inject without fallback: %v", err)
	}
	fabNoFallback.Run(10)

	var jWithout *fabric.Journey
	for i := range fabNoFallback.Report() {
		if fabNoFallback.Report()[i].FrameID == fidWithout {
			jWithout = &fabNoFallback.Report()[i]
			break
		}
	}
	if jWithout == nil {
		t.Fatal("missing journey for fidWithout")
	}
	var hadNoMemberDrop bool
	for _, e := range jWithout.Entries {
		if e.Kind == fabric.EntryDrop && e.Reason == bridge.ReasonNoMember {
			hadNoMemberDrop = true
			break
		}
	}
	if !hadNoMemberDrop {
		t.Fatalf("frame without fallback missing journey drop no-member: %+v", jWithout.Entries)
	}
}

func TestPassive(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	lagActive := &lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				LACP: lag.LACPConfig{Mode: lag.Active, Fast: true},
			},
		},
	}
	lagPassive := &lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				LACP: lag.LACPConfig{Mode: lag.Passive, Fast: true},
			},
		},
	}

	// Case 1: A Active, B Passive: B's first LACPDU journey is dated after A's first arrives at B.
	fab, _, _ := newLagTopology(t, t0, lagActive, lagPassive)

	// Step until A's first LACPDU arrives at B and B emits a reply.
	for i := 0; i < 20; i++ {
		fab.Step()
	}

	var firstArrivalAtB time.Time
	var firstBJourneyAt time.Time

	for _, j := range fab.Report() {
		if !j.Protocol {
			continue
		}
		// Check if journey originated at A and crossed to B.
		if j.Injection.Origin.Node == "A" {
			for _, e := range j.Entries {
				if e.Kind == fabric.EntryCrossing && e.Device == "B" {
					arrival := e.At.Add(e.Latency).Add(e.Serialization)
					if firstArrivalAtB.IsZero() || arrival.Before(firstArrivalAtB) {
						firstArrivalAtB = arrival
					}
				}
			}
		}
		if j.Injection.Origin.Node == "B" {
			if firstBJourneyAt.IsZero() || j.Injection.At.Before(firstBJourneyAt) {
				firstBJourneyAt = j.Injection.At
			}
		}
	}

	if firstArrivalAtB.IsZero() {
		t.Fatal("A's LACPDU never arrived at B")
	}
	if firstBJourneyAt.IsZero() {
		t.Fatal("B never emitted an LACPDU")
	}
	if firstBJourneyAt.Before(firstArrivalAtB) {
		t.Fatalf("B's first LACPDU is dated %v, which is before A's arrival at B %v", firstBJourneyAt, firstArrivalAtB)
	}

	// Case 2: both Passive: no LACPDU journeys by t0+5s.
	fabBothPassive, _, _ := newLagTopology(t, t0, lagPassive, lagPassive)
	target := t0.Add(5 * time.Second)
	for {
		snap := fabBothPassive.Snapshot()
		if !snap.Clock.Before(target) && len(snap.Queue) == 0 {
			break
		}
		if snap.Clock.After(target) {
			break
		}
		if _, ok := fabBothPassive.Step(); !ok {
			break
		}
	}

	for _, j := range fabBothPassive.Report() {
		if j.Protocol {
			t.Fatalf("found protocol journey when both switches are passive: %+v", j)
		}
	}
}
