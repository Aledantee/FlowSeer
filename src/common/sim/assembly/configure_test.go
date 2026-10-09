package assembly_test

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func newTwoSwitchFabric(t *testing.T) (*fabric.Fabric, map[string]netaddr.MAC) {
	return newTwoSwitchFabricWith(t, nil)
}

func newTwoSwitchFabricWith(t *testing.T, mutate func(*assembly.Config)) (*fabric.Fabric, map[string]netaddr.MAC) {
	t.Helper()

	newPorts := func() port.Table {
		b := port.NewBuilder()
		b.Add(port.Port{
			Name:        "1/1/1",
			Kind:        port.Physical,
			AdminStatus: port.Up,
			OperStatus:  port.Up,
		})
		b.Add(port.Port{
			Name:        "1/1/24",
			Kind:        port.Physical,
			AdminStatus: port.Up,
			OperStatus:  port.Up,
		})
		tbl, err := b.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}
		return tbl
	}

	vid10 := vlan.ID(10)
	makeBridge := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "prod"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/24": {Tagged: []vlan.ID{10}},
				},
			},
		}
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}
	macs := map[string]netaddr.MAC{
		"h1": macH1,
		"h2": macH2,
	}

	cfg := assembly.Config{
		Start: time.Unix(1700000000, 0),
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: newPorts(), Bridge: makeBridge()},
			"sw2": {Ports: newPorts(), Bridge: makeBridge()},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				Cable: cablemodel.Cable{LengthMeters: 300, Medium: cablemodel.MultimodeFiber},
				A:     fabric.Endpoint{Node: "sw1", Port: "1/1/24"},
				B:     fabric.Endpoint{Node: "sw2", Port: "1/1/24"},
			},
			{
				A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				B: fabric.Endpoint{Node: "h2"},
			},
		},
		PhyAssumption: &cablemodel.PhyAssumption{
			Medium: cablemodel.TwistedPair,
			Ethernet: phy.Ethernet{
				SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
		},
	}

	if mutate != nil {
		mutate(&cfg)
	}

	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
	}

	return fab, macs
}

func TestConfigureKeepsInFlightFrame(t *testing.T) {
	fab, macs := newTwoSwitchFabric(t)
	t0 := assembly.Spec(fab).Config().Start

	fid, err := fab.Inject(fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       macs["h2"],
			Src:       macs["h1"],
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("hello"),
		},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}

	// Step until the frame is on the trunk cable (sw1 has hopped and arrival at sw2 is queued).
	for {
		entry, ok := fab.Step()
		if !ok {
			t.Fatal("step ended before frame reached trunk cable")
		}
		if entry.Kind == fabric.EntryHop && entry.Device == "sw1" {
			break
		}
	}

	// Configure sw2 moving trunk and access ports to VLAN 20 with ingress filtering.
	vid20 := vlan.ID(20)
	sw2Cfg := fab.Device("sw2").(*vswitch.Switch).Config()
	sw2Cfg.Bridge = &bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{20: "vlan20"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1":  {PVID: &vid20, Untagged: []vlan.ID{20}},
				"1/1/24": {Tagged: []vlan.ID{20}, IngressFiltering: true},
			},
		},
	}

	if err := assembly.Configure(fab, "sw2", sw2Cfg); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	fab.Run(20)

	var frameJourney fabric.Journey
	var found bool
	for _, j := range fab.Report() {
		if j.FrameID == fid {
			frameJourney = j
			found = true
			break
		}
	}
	if !found {
		t.Fatal("journey from t0 missing in report")
	}
	if !frameJourney.Injection.At.Equal(t0) {
		t.Errorf("journey At = %v, want %v", frameJourney.Injection.At, t0)
	}
	if len(frameJourney.Entries) == 0 {
		t.Fatal("journey has no entries")
	}
	last := frameJourney.Entries[len(frameJourney.Entries)-1]
	if last.Kind != fabric.EntryDrop || last.Reason != bridge.ReasonIngressFilter {
		t.Fatalf("last entry = %+v, want EntryDrop with ingress-filter reason", last)
	}
}

func TestConfigureLearnedStateAdmittedKept(t *testing.T) {
	fab, macs := newTwoSwitchFabric(t)
	t0 := assembly.Spec(fab).Config().Start

	_, err := fab.Inject(fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       macs["h2"],
			Src:       macs["h1"],
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("learn-me"),
		},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(20)

	snap := fab.Snapshot()
	entries1 := snap.Devices["sw1"].Entries
	var found bool
	for _, e := range snap.Devices["sw1"].Entries {
		if e.MAC == macs["h1"] && e.Port == "1/1/1" && e.FID == 10 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("sw1 did not learn h1 on 1/1/1: %+v", entries1)
	}

	// Change only VLAN name on sw1.
	sw1Cfg := fab.Device("sw1").(*vswitch.Switch).Config()
	sw1Cfg.Bridge.VLAN.Table[10] = "production"
	if err := assembly.Configure(fab, "sw1", sw1Cfg); err != nil {
		t.Fatalf("Configure same VLAN: %v", err)
	}

	snapAfterSame := fab.Snapshot()
	found = false
	for _, e := range snapAfterSame.Devices["sw1"].Entries {
		if e.MAC == macs["h1"] && e.Port == "1/1/1" && e.FID == 10 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("learned entry lost after VLAN rename: %+v", snapAfterSame.Devices["sw1"].Entries)
	}

	// Move 1/1/1 to VLAN 20: entry should be removed.
	swBefore := fab.Device("sw1").(*vswitch.Switch)
	vid20 := vlan.ID(20)
	sw1CfgV20 := fab.Device("sw1").(*vswitch.Switch).Config()
	sw1CfgV20.Bridge.VLAN.Table = map[vlan.ID]string{20: "vlan20"}
	sw1CfgV20.Bridge.VLAN.Switchports["1/1/1"] = bridge.Switchport{PVID: &vid20, Untagged: []vlan.ID{20}}
	sw1CfgV20.Bridge.VLAN.Switchports["1/1/24"] = bridge.Switchport{Tagged: []vlan.ID{20}}

	if err := assembly.Configure(fab, "sw1", sw1CfgV20); err != nil {
		t.Fatalf("Configure VLAN 20: %v", err)
	}

	snapAfterMove := fab.Snapshot()
	for _, e := range snapAfterMove.Devices["sw1"].Entries {
		if e.MAC == macs["h1"] && e.Port == "1/1/1" {
			t.Errorf("entry still present after moving port to VLAN 20: %+v", e)
		}
	}

	// Retention matches vswitch.Derive on the same pair.
	targetSpec := fab.Device("sw1").(*vswitch.Switch).Spec()
	derivedDirect, _, err := vswitch.Derive(swBefore, targetSpec, fab.Snapshot().Clock)
	if err != nil {
		t.Fatalf("vswitch.Derive: %v", err)
	}
	if assembly.Retention(fab)["sw1"] != derivedDirect.Retention() {
		t.Errorf("Retention[sw1] = %+v, want %+v", assembly.Retention(fab)["sw1"], derivedDirect.Retention())
	}
}

func TestConfigureLinkStateFollowsSwitchConfig(t *testing.T) {
	fab, macs := newTwoSwitchFabric(t)
	t0 := assembly.Spec(fab).Config().Start

	// Set sw1 1/1/24 to AdminStatus: Down.
	sw1Cfg := fab.Device("sw1").(*vswitch.Switch).Config()
	b := port.NewBuilder()
	for _, p := range sw1Cfg.Ports.Ports() {
		if p.Name == "1/1/24" {
			p.AdminStatus = port.Down
		}
		b.Add(p)
	}
	newPorts, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}
	sw1Cfg.Ports = newPorts

	if err := assembly.Configure(fab, "sw1", sw1Cfg); err != nil {
		t.Fatalf("Configure admin down: %v", err)
	}

	// Trunk link should be Down.
	var trunkLink *fabric.Link
	for _, l := range fab.Links() {
		if (l.Cable.A.Node == "sw1" && l.Cable.B.Node == "sw2") || (l.Cable.A.Node == "sw2" && l.Cable.B.Node == "sw1") {
			cp := l
			trunkLink = &cp
			break
		}
	}
	if trunkLink == nil {
		t.Fatal("trunk link not found")
	}
	if trunkLink.A.Oper != port.Down || trunkLink.B.Oper != port.Down {
		t.Errorf("trunk link oper = (%v, %v), want (Down, Down)", trunkLink.A.Oper, trunkLink.B.Oper)
	}

	// sw2 1/1/24 should be oper Down in its port table.
	sw2Port, ok := fab.Device("sw2").(*vswitch.Switch).Ports().Port("1/1/24")
	if !ok || sw2Port.OperStatus != port.Down {
		t.Errorf("sw2 1/1/24 oper = %v, want Down", sw2Port.OperStatus)
	}

	// Injected frame from h2 is not delivered to h1.
	fid, err := fab.Inject(fabric.Injection{
		At:     t0.Add(time.Second),
		Origin: fabric.Endpoint{Node: "h2"},
		Frame: ethernet.Frame{
			Dst:       macs["h1"],
			Src:       macs["h2"],
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("cant-cross"),
		},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(20)

	for _, j := range fab.Report() {
		if j.FrameID == fid {
			if len(j.Deliveries) > 0 {
				t.Errorf("frame was delivered across admin down link: %+v", j.Deliveries)
			}
		}
	}

	// Configure Up again restores the link.
	sw1CfgUp := fab.Device("sw1").(*vswitch.Switch).Config()
	bUp := port.NewBuilder()
	for _, p := range sw1CfgUp.Ports.Ports() {
		if p.Name == "1/1/24" {
			p.AdminStatus = port.Up
		}
		bUp.Add(p)
	}
	portsUp, err := bUp.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}
	sw1CfgUp.Ports = portsUp

	if err := assembly.Configure(fab, "sw1", sw1CfgUp); err != nil {
		t.Fatalf("Configure admin up: %v", err)
	}

	for _, l := range fab.Links() {
		if (l.Cable.A.Node == "sw1" && l.Cable.B.Node == "sw2") || (l.Cable.A.Node == "sw2" && l.Cable.B.Node == "sw1") {
			if l.A.Oper != port.Up || l.B.Oper != port.Up {
				t.Errorf("restored trunk link oper = (%v, %v), want (Up, Up)", l.A.Oper, l.B.Oper)
			}
		}
	}
}

func TestConfigureRefusalLeavesFabricUnchanged(t *testing.T) {
	fab, _ := newTwoSwitchFabric(t)

	spec0 := assembly.Spec(fab)
	links0 := fab.Links()
	fp0 := fab.Fingerprint()

	// Missing port 1/1/24 which cable names.
	sw1Cfg := fab.Device("sw1").(*vswitch.Switch).Config()
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	portsMissing, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}
	sw1Cfg.Ports = portsMissing

	err = assembly.Configure(fab, "sw1", sw1Cfg)
	if err == nil || !strings.Contains(err.Error(), "1/1/24") {
		t.Fatalf("Configure missing port err = %v, want error naming 1/1/24", err)
	}

	if !assembly.Spec(fab).Equal(spec0) {
		t.Error("Spec changed after refused Configure")
	}
	if !reflect.DeepEqual(fab.Links(), links0) {
		t.Error("Links changed after refused Configure")
	}
	if fab.Fingerprint() != fp0 {
		t.Errorf("Fingerprint changed: got %s, want %s", fab.Fingerprint(), fp0)
	}

	// Refused node: "h1" (a host)
	err = assembly.Configure(fab, "h1", fab.Device("sw1").(*vswitch.Switch).Config())
	if err == nil || !strings.Contains(err.Error(), "h1") {
		t.Fatalf("Configure host err = %v, want error naming h1", err)
	}
	if !assembly.Spec(fab).Equal(spec0) || fab.Fingerprint() != fp0 {
		t.Error("fabric modified after refused node h1")
	}

	// Refused node: "nope"
	err = assembly.Configure(fab, "nope", fab.Device("sw1").(*vswitch.Switch).Config())
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("Configure unknown node err = %v, want error naming nope", err)
	}
	if !assembly.Spec(fab).Equal(spec0) || fab.Fingerprint() != fp0 {
		t.Error("fabric modified after refused node nope")
	}
}

func operStatusConflictCount(fab *fabric.Fabric) int {
	count := 0
	for _, issue := range fab.Metadata().Issues() {
		if issue.Code == fabric.IssueOperStatusConflict {
			count++
		}
	}
	return count
}

func TestConfigureNoOpKeepsConfiguredOperStatus(t *testing.T) {
	// sw1's trunk port is configured Down over a healthy cable, so the
	// configured table disagrees with the state the cable derives.
	fab, _ := newTwoSwitchFabricWith(t, func(cfg *assembly.Config) {
		sw := cfg.Switches["sw1"]
		b := port.NewBuilder()
		for _, p := range sw.Ports.Ports() {
			if p.Name == "1/1/24" {
				p.OperStatus = port.Down
			}
			b.Add(p)
		}
		tbl, err := b.Build()
		if err != nil {
			t.Fatalf("build sw1 ports: %v", err)
		}
		sw.Ports = tbl
		cfg.Switches["sw1"] = sw
	})

	spec0 := assembly.Spec(fab)
	if got := operStatusConflictCount(fab); got != 1 {
		t.Fatalf("oper-status conflicts before Configure = %d, want 1", got)
	}

	if err := assembly.Configure(fab, "sw1", assembly.Spec(fab).Config().Switches["sw1"]); err != nil {
		t.Fatalf("Configure no-op: %v", err)
	}

	if got := operStatusConflictCount(fab); got != 1 {
		t.Errorf("oper-status conflicts after no-op Configure = %d, want 1", got)
	}
	if !assembly.Spec(fab).Equal(spec0) {
		t.Error("Spec changed after no-op Configure")
	}
}

func TestConfigureHeldFramesFailAtFabricClock(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	swMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}

	swCfg := vswitch.Config{
		MAC:   swMAC,
		Ports: ports,
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"1/1/1": {
							Port:     "1/1/1",
							MAC:      swMAC,
							Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")},
						},
						"1/1/2": {
							Port:     "1/1/2",
							MAC:      swMAC,
							Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.60.1/24")},
						},
					},
				},
			},
		},
	}

	cfg := assembly.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": swCfg,
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {
				Address: macH1,
				IP: &hostmodel.IP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.50.7/24")},
					Gateway:   netip.MustParseAddr("10.0.50.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{
						netip.MustParseAddr("10.0.50.1"): swMAC,
					},
				},
			},
		},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
		},
		PhyAssumption: &cablemodel.PhyAssumption{
			Medium: cablemodel.TwistedPair,
			Ethernet: phy.Ethernet{
				SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
		},
	}

	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
	}

	heldID, err := fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h1"},
		Packet: &fabric.Packet{
			To:       netip.MustParseAddr("10.0.60.77"),
			Protocol: 17,
			Payload:  []byte("ping"),
		},
	})
	if err != nil {
		t.Fatalf("Inject packet: %v", err)
	}

	// Step until the frame is held on sw1.
	for {
		entry, ok := fab.Step()
		if !ok {
			t.Fatal("step ended before frame was held")
		}
		if entry.Kind == fabric.EntryHop && entry.Device == "sw1" && entry.Result != nil && entry.Result.Outcome == trace.Held {
			break
		}
	}

	// Switch held the frame. Now configure sw1 adding a static route at time t.
	tConfigure := fab.Snapshot().Clock
	newCfg := fab.Device("sw1").(*vswitch.Switch).Config()
	vrf := newCfg.Routing.VRFs[routing.DefaultVRF]
	vrf.Routes = append(vrf.Routes, routing.Route{
		Prefix:    netip.MustParsePrefix("10.99.0.0/16"),
		Interface: "1/1/1",
	})
	newCfg.Routing.VRFs[routing.DefaultVRF] = vrf

	if err := assembly.Configure(fab, "sw1", newCfg); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	res := fab.Run(50)
	if res.Stop != fabric.StopQueueDrained && res.Stop != fabric.StopConverged {
		t.Fatalf("res.Stop = %v, want StopQueueDrained or StopConverged", res.Stop)
	}

	var dropEntry *fabric.Entry
	for _, j := range fab.Report() {
		if j.FrameID != heldID {
			continue
		}
		for _, e := range j.Entries {
			if e.Kind == fabric.EntryDrop && e.Reason == routing.ReasonNeighborMiss {
				ee := e
				dropEntry = &ee
			}
		}
	}
	if dropEntry == nil {
		t.Fatal("neighbor failure drop entry not found in report")
	}
	if !dropEntry.At.Equal(tConfigure) {
		t.Errorf("dropEntry.At = %v, want %v (fabric clock at Configure)", dropEntry.At, tConfigure)
	}
}

func TestConfigureProtocolTimersRestart(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	newPorts := func() port.Table {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		tbl, err := b.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}
		return tbl
	}

	vid10 := vlan.ID(10)
	newBridgeCfg := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "VLAN10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/2": {Tagged: []vlan.ID{10}},
					"1/1/3": {Tagged: []vlan.ID{10}},
				},
			},
		}
	}

	macs := map[string]netaddr.MAC{
		"sw1": {0, 0, 0, 0, 1, 1},
		"sw2": {0, 0, 0, 0, 1, 2},
		"sw3": {0, 0, 0, 0, 1, 3},
	}

	cfg := assembly.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  newPorts(),
				Bridge: newBridgeCfg(),
				STP: &stp.Config{
					Priority: 8192,
					Address:  macs["sw1"],
					Ports:    map[string]stp.Port{"1/1/2": {}, "1/1/3": {}},
				},
			},
			"sw2": {
				Ports:  newPorts(),
				Bridge: newBridgeCfg(),
				STP: &stp.Config{
					Priority: 16384,
					Address:  macs["sw2"],
					Ports:    map[string]stp.Port{"1/1/2": {}, "1/1/3": {}},
				},
			},
			"sw3": {
				Ports:  newPorts(),
				Bridge: newBridgeCfg(),
				STP: &stp.Config{
					Priority: 32768,
					Address:  macs["sw3"],
					Ports:    map[string]stp.Port{"1/1/2": {}, "1/1/3": {}},
				},
			},
		},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 1.0}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}},
			{Cable: cablemodel.Cable{LengthMeters: 1.0}, A: fabric.Endpoint{Node: "sw2", Port: "1/1/3"}, B: fabric.Endpoint{Node: "sw3", Port: "1/1/2"}},
			{Cable: cablemodel.Cable{LengthMeters: 1.0}, A: fabric.Endpoint{Node: "sw3", Port: "1/1/3"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}},
		},
		PhyAssumption: &cablemodel.PhyAssumption{
			Medium: cablemodel.TwistedPair,
			Ethernet: phy.Ethernet{
				SupportedSpeedsBPS:       []uint64{1_000_000_000},
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
		},
	}

	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
	}

	fab.Run(200)

	// sw1 is root.
	snap0 := fab.Snapshot()
	for pName, info := range snap0.Devices["sw1"].Roles {
		if info.Role != bpdu.RoleDesignated {
			t.Errorf("initial root sw1 port %s role = %v, want Designated", pName, info.Role)
		}
	}

	// A no-op Configure on sw3 keeps every port role in place and adds no
	// topology change: a fork that reconfigures to its own config and one that
	// does not stay identical after both run on.
	baseline := fab.Fork()
	reconfigured := baseline.Fork()
	if err := assembly.Configure(reconfigured, "sw3", reconfigured.Device("sw3").(*vswitch.Switch).Config()); err != nil {
		t.Fatalf("Configure no-op: %v", err)
	}
	snapAfterNoOp := reconfigured.Snapshot()
	for dev, d := range snap0.Devices {
		for p, info := range d.Roles {
			got := snapAfterNoOp.Devices[dev].Roles[p]
			if got.Role != info.Role || got.State != info.State {
				t.Errorf("role changed after no-op configure: %s:%s was (%v,%v), got (%v,%v)",
					dev, p, info.Role, info.State, got.Role, got.State)
			}
		}
	}

	baseline.Run(200)
	reconfigured.Run(200)
	if baseline.Fingerprint() != reconfigured.Fingerprint() {
		t.Errorf("no-op Configure changed the fingerprint:\n got  %s\n want %s",
			reconfigured.Fingerprint(), baseline.Fingerprint())
	}
	// The fingerprint carries no topology change count, so compare it on its
	// own.
	for _, name := range []string{"sw1", "sw2", "sw3"} {
		want, _ := baseline.Device(name).(*vswitch.Switch).TopologyChanges()
		got, _ := reconfigured.Device(name).(*vswitch.Switch).TopologyChanges()
		if got != want {
			t.Errorf("%s topology changes after no-op Configure = %d, want %d", name, got, want)
		}
	}

	// Lower sw3 bridge priority to 4096.
	sw3Cfg := fab.Device("sw3").(*vswitch.Switch).Config()
	sw3Cfg.STP.Priority = 4096
	if err := assembly.Configure(fab, "sw3", sw3Cfg); err != nil {
		t.Fatalf("Configure lower priority: %v", err)
	}

	res, err := fab.RunScenario(fabric.Scenario{Name: "converge", Budget: 300, Window: 3})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if res.Stop != fabric.StopConverged {
		t.Fatalf("res.Stop = %v, want StopConverged", res.Stop)
	}

	finalSnap := fab.Snapshot()
	for pName, info := range finalSnap.Devices["sw3"].Roles {
		if info.Role != bpdu.RoleDesignated {
			t.Errorf("new root sw3 port %s role = %v, want Designated", pName, info.Role)
		}
	}
	sw1P3 := finalSnap.Devices["sw1"].Roles["1/1/3"]
	if sw1P3.Role != bpdu.RoleRoot {
		t.Errorf("sw1 port 1/1/3 role = %v, want Root", sw1P3.Role)
	}
}

func setPortAdmin(t *testing.T, cfg *vswitch.Config, name string, admin port.LinkState) {
	t.Helper()
	b := port.NewBuilder()
	for _, p := range cfg.Ports.Ports() {
		if p.Name == name {
			p.AdminStatus = admin
		}
		b.Add(p)
	}
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}
	cfg.Ports = tbl
}

func trunkLinkOper(fab *fabric.Fabric) (port.LinkState, port.LinkState, bool) {
	for _, l := range fab.Links() {
		if l.Cable.A.Node == "sw1" && l.Cable.B.Node == "sw2" && l.A.Port == "1/1/24" {
			return l.A.Oper, l.B.Oper, true
		}
	}
	return port.Unknown, port.Unknown, false
}

func TestConfigureForksStayIndependent(t *testing.T) {
	fab, _ := newTwoSwitchFabric(t)

	fork := fab.Fork()
	spec0 := assembly.Spec(fab)
	cfg0 := fab.Device("sw1").(*vswitch.Switch).Config()
	links0 := fab.Links()

	// Configure the fork with a VLAN rename and a link-changing edit: bring
	// 1/1/24 administratively down.
	sw1CfgFork := fork.Device("sw1").(*vswitch.Switch).Config()
	sw1CfgFork.Bridge.VLAN.Table[10] = "fork-vlan"
	setPortAdmin(t, &sw1CfgFork, "1/1/24", port.Down)
	if err := assembly.Configure(fork, "sw1", sw1CfgFork); err != nil {
		t.Fatalf("fork.Configure: %v", err)
	}

	if a, b, ok := trunkLinkOper(fork); !ok || a != port.Down || b != port.Down {
		t.Errorf("fork trunk link oper = (%v, %v), want (Down, Down)", a, b)
	}

	// Source remains unchanged, links and port table included.
	if !assembly.Spec(fab).Equal(spec0) {
		t.Error("source Spec changed after fork Configure")
	}
	if fab.Device("sw1").(*vswitch.Switch).Config().Bridge.VLAN.Table[10] != cfg0.Bridge.VLAN.Table[10] {
		t.Error("source switch config changed after fork Configure")
	}
	if !reflect.DeepEqual(fab.Links(), links0) {
		t.Error("source Links changed after fork Configure")
	}
	if p, ok := fab.Device("sw1").(*vswitch.Switch).Ports().Port("1/1/24"); !ok || p.OperStatus != port.Up {
		t.Errorf("source sw1 1/1/24 after fork Configure = (%v, found %v), want Up", p.OperStatus, ok)
	}

	// Configure the source with a link-changing edit of its own that differs
	// from the fork's state: bring 1/1/1 administratively down. The fork's
	// links and port table stay exactly as they were.
	forkLinks := fork.Links()
	forkPorts := fork.Device("sw1").(*vswitch.Switch).Ports().Ports()
	sw1CfgSource := fab.Device("sw1").(*vswitch.Switch).Config()
	sw1CfgSource.Bridge.VLAN.Table[10] = "source-vlan"
	setPortAdmin(t, &sw1CfgSource, "1/1/1", port.Down)
	if err := assembly.Configure(fab, "sw1", sw1CfgSource); err != nil {
		t.Fatalf("fab.Configure: %v", err)
	}

	if fork.Device("sw1").(*vswitch.Switch).Config().Bridge.VLAN.Table[10] != "fork-vlan" {
		t.Errorf("fork switch config changed after source Configure: got %q, want 'fork-vlan'",
			fork.Device("sw1").(*vswitch.Switch).Config().Bridge.VLAN.Table[10])
	}
	if !reflect.DeepEqual(fork.Links(), forkLinks) {
		t.Error("fork Links changed after source Configure")
	}
	if !reflect.DeepEqual(fork.Device("sw1").(*vswitch.Switch).Ports().Ports(), forkPorts) {
		t.Error("fork sw1 port table changed after source Configure")
	}
	if p, ok := fab.Device("sw1").(*vswitch.Switch).Ports().Port("1/1/1"); !ok || p.OperStatus != port.Down {
		t.Errorf("source sw1 1/1/1 after source Configure = (%v, found %v), want Down", p.OperStatus, ok)
	}
}
