package fabric_test

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func makeTwoSwitchConfigs(t *testing.T) (fabric.Config, netaddr.MAC, netaddr.MAC) {
	t.Helper()

	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable1, err := b1.Build()
	if err != nil {
		t.Fatalf("build sw1 ports: %v", err)
	}

	b2 := port.NewBuilder()
	b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable2, err := b2.Build()
	if err != nil {
		t.Fatalf("build sw2 ports: %v", err)
	}

	vid10 := vlan.ID(10)
	newBridgeCfg := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{
					10: "VLAN10",
				},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {
						PVID:     &vid10,
						Untagged: []vlan.ID{10},
					},
					"1/1/24": {
						Tagged: []vlan.ID{10},
					},
				},
			},
		}
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  pTable1,
				Bridge: newBridgeCfg(),
			},
			"sw2": {
				Ports:  pTable2,
				Bridge: newBridgeCfg(),
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/24"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/24"},
				LengthMeters: 300,
				Medium:       fabric.MultimodeFiber,
			},
			{
				A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				B: fabric.Endpoint{Node: "h2"},
			},
		},
	}

	return cfg, macH1, macH2
}

func TestCompareEqualFabricsReturnsSameTrue(t *testing.T) {
	cfgA, macH1, macH2 := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	fabA, err := fabric.New(cfgA)
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.New(cfgB)
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	scenario := []fabric.Injection{
		{
			At:     now,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("payload"),
			},
		},
	}

	cmp, err := fabric.Compare(fabA, fabB, scenario, 10)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !cmp.Same {
		t.Errorf("Compare returned Same: false for equal fabrics, want true")
	}
	if cmp.Steps[0] != 2 || cmp.Steps[1] != 2 {
		t.Errorf("Steps = %v, want [2, 2]", cmp.Steps)
	}
	if len(cmp.Current) != 1 || len(cmp.Expected) != 1 {
		t.Fatalf("Journeys length mismatch: cur=%d, exp=%d", len(cmp.Current), len(cmp.Expected))
	}
	if len(cmp.Current[0].Deliveries) != 1 || len(cmp.Expected[0].Deliveries) != 1 {
		t.Fatalf("Deliveries mismatch: cur=%d, exp=%d", len(cmp.Current[0].Deliveries), len(cmp.Expected[0].Deliveries))
	}
	if cmp.Current[0].Deliveries[0].Host != "h2" || cmp.Expected[0].Deliveries[0].Host != "h2" {
		t.Errorf("Delivery host mismatch: cur=%s, exp=%s", cmp.Current[0].Deliveries[0].Host, cmp.Expected[0].Deliveries[0].Host)
	}
}

func TestCompareAndDiffDetectVlanAndCableFaultChange(t *testing.T) {
	curCfg, macH1, macH2 := makeTwoSwitchConfigs(t)
	expCfg := curCfg.Clone()

	// Expected configuration moves sw2:1/1/1 to VLAN 20 and cuts the inter-switch cable.
	vid20 := vlan.ID(20)
	sw2Cfg := expCfg.Switches["sw2"]
	sw2Cfg.Bridge.VLAN.Table[20] = "VLAN20"
	sw2Cfg.Bridge.VLAN.Switchports["1/1/1"] = bridge.Switchport{
		PVID:     &vid20,
		Untagged: []vlan.ID{20},
	}
	expCfg.Switches["sw2"] = sw2Cfg

	for i := range expCfg.Cables {
		if expCfg.Cables[i].A.Node == "sw1" && expCfg.Cables[i].B.Node == "sw2" {
			expCfg.Cables[i].Fault = fabric.Fault{Kind: fabric.FaultCut}
		}
	}

	curFab, err := fabric.New(curCfg)
	if err != nil {
		t.Fatalf("New curFab: %v", err)
	}
	expFab, err := fabric.New(expCfg)
	if err != nil {
		t.Fatalf("New expFab: %v", err)
	}

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	scenario := []fabric.Injection{
		{
			At:     now,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test-payload"),
			},
		},
	}

	cmp, err := fabric.Compare(curFab, expFab, scenario, 10)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if cmp.Same {
		t.Errorf("Compare returned Same: true, want false")
	}

	if len(cmp.Current) != 1 || len(cmp.Expected) != 1 {
		t.Fatalf("Journeys missing: cur=%d, exp=%d", len(cmp.Current), len(cmp.Expected))
	}
	if len(cmp.Current[0].Deliveries) != 1 {
		t.Errorf("Current deliveries = %d, want 1", len(cmp.Current[0].Deliveries))
	}
	if len(cmp.Expected[0].Deliveries) != 0 {
		t.Errorf("Expected deliveries = %d, want 0", len(cmp.Expected[0].Deliveries))
	}

	changes := fabric.Diff(curCfg, expCfg)

	var (
		foundSwitchportChange bool
		foundCableFaultChange bool
	)

	for _, ch := range changes {
		if ch.Subject.Kind == "port" && ch.Subject.Key == "sw2/1/1/1" {
			if ch.Field == "pvid" {
				foundSwitchportChange = true
			}
		}
		if ch.Subject.Kind == "cable" && ch.Subject.Key == "sw1:1/1/24-sw2:1/1/24" {
			if ch.Field == "fault" && ch.Layer == fabric.Layer {
				foundCableFaultChange = true
				fromFault, okFrom := ch.From.(fabric.Fault)
				toFault, okTo := ch.To.(fabric.Fault)
				if !okFrom || fromFault.Kind != fabric.FaultNone && fromFault.Kind != "" {
					t.Errorf("cable fault From = %+v, want empty or FaultNone", ch.From)
				}
				if !okTo || toFault.Kind != fabric.FaultCut {
					t.Errorf("cable fault To = %+v, want FaultCut", ch.To)
				}
			}
		}
	}

	if !foundSwitchportChange {
		t.Errorf("Diff missing switchport change under sw2/1/1/1: %v", changes)
	}
	if !foundCableFaultChange {
		t.Errorf("Diff missing cable fault change sw1:1/1/24-sw2:1/1/24: %v", changes)
	}
}

func TestDeriveRetainsAdmittedDynamicEntries(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)

	macA := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x0a}
	macB := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x0b}
	macC := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x0c}

	curCfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: ports,
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{
							10: "vlan10",
							20: "vlan20",
						},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
							"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
							"1/1/3": {PVID: &vid10, Untagged: []vlan.ID{10}},
						},
					},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macB},
			"h2": {Address: macA},
			"h3": {Address: macC},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
			{A: fabric.Endpoint{Node: "h3"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}},
		},
	}

	curFab, err := fabric.New(curCfg)
	if err != nil {
		t.Fatalf("New curFab: %v", err)
	}

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	// Inject frame from h2 to learn (10, macA) on 1/1/2.
	_, err = curFab.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "h2"},
		Frame: ethernet.Frame{
			Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			Src:       macA,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("frame A"),
		},
	})
	if err != nil {
		t.Fatalf("Inject h2: %v", err)
	}

	// Inject frame from h1 to learn (10, macB) on 1/1/1.
	_, err = curFab.Inject(fabric.Injection{
		At:     now.Add(time.Second),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			Src:       macB,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("frame B"),
		},
	})
	if err != nil {
		t.Fatalf("Inject h1: %v", err)
	}

	curFab.Run(10)

	entries := curFab.Switch("sw1").Entries()
	if len(entries) != 2 {
		t.Fatalf("sw1 learned entries = %d, want 2", len(entries))
	}

	// In the expected config, 1/1/2 moves to VLAN 20.
	expCfg := curCfg.Clone()
	expSw := expCfg.Switches["sw1"]
	expSw.Bridge.VLAN.Switchports["1/1/2"] = bridge.Switchport{
		PVID:     &vid20,
		Untagged: []vlan.ID{20},
	}
	expCfg.Switches["sw1"] = expSw

	derivedFab, err := fabric.Derive(curFab, expCfg)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	derivedEntries := derivedFab.Switch("sw1").Entries()
	if len(derivedEntries) != 1 {
		t.Fatalf("derived sw1 entries = %d, want 1: %v", len(derivedEntries), derivedEntries)
	}
	if derivedEntries[0].FID != 10 || derivedEntries[0].MAC != macB || derivedEntries[0].Port != "1/1/1" {
		t.Errorf("derived entry mismatch: got %v, want (10, %s) -> 1/1/1", derivedEntries[0], macB)
	}
}

func TestDeriveBuildsFreshSwitchWhenNoNamesake(t *testing.T) {
	curCfg, _, _ := makeTwoSwitchConfigs(t)
	curFab, err := fabric.New(curCfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	expCfg := curCfg.Clone()
	b3 := port.NewBuilder()
	b3.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable3, err := b3.Build()
	if err != nil {
		t.Fatalf("build sw3 ports: %v", err)
	}

	vid10 := vlan.ID(10)
	expCfg.Switches["sw3"] = vswitch.Config{
		Ports: pTable3,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
				},
			},
		},
	}
	expCfg.Hosts["h3"] = fabric.Host{Address: netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x03}}
	expCfg.Cables = append(expCfg.Cables, fabric.Cable{
		A: fabric.Endpoint{Node: "h3"},
		B: fabric.Endpoint{Node: "sw3", Port: "1/1/1"},
	})

	derived, err := fabric.Derive(curFab, expCfg)
	if err != nil {
		t.Fatalf("Derive with new switch: %v", err)
	}
	if derived.Switch("sw3") == nil {
		t.Errorf("derived switch sw3 is nil, want fresh switch")
	}

	// From nil cur
	fromNil, err := fabric.Derive(nil, expCfg)
	if err != nil {
		t.Fatalf("Derive from nil: %v", err)
	}
	if fromNil.Switch("sw3") == nil {
		t.Errorf("derive from nil sw3 is nil, want fresh switch")
	}
}

func TestDiffAllSubjectKinds(t *testing.T) {
	curCfg, macH1, _ := makeTwoSwitchConfigs(t)

	t.Run("equal configs return empty diff", func(t *testing.T) {
		changes := fabric.Diff(curCfg, curCfg.Clone())
		if len(changes) != 0 {
			t.Errorf("Diff returned %d changes for equal configs, want 0", len(changes))
		}
	})

	t.Run("cable modifications and added/removed cables", func(t *testing.T) {
		expCfg := curCfg.Clone()
		// Modify first cable length and top speed
		expCfg.Cables[0].LengthMeters = 50
		expCfg.Cables[0].TopSpeedBPS = 100_000_000

		changes := fabric.Diff(curCfg, expCfg)
		var foundLen, foundSpeed bool
		for _, ch := range changes {
			if ch.Subject.Kind == "cable" && ch.Subject.Key == "h1:-sw1:1/1/1" {
				if ch.Field == "length" && ch.From == float64(0) && ch.To == float64(50) {
					foundLen = true
				}
				if ch.Field == "top_speed" && ch.From == uint64(0) && ch.To == uint64(100_000_000) {
					foundSpeed = true
				}
			}
		}
		if !foundLen || !foundSpeed {
			t.Errorf("missing cable length or top_speed change: %v", changes)
		}
	})

	t.Run("unset medium diffs as twisted pair", func(t *testing.T) {
		cfgA := curCfg.Clone()
		cfgA.Cables[0].Medium = ""

		cfgB := cfgA.Clone()
		cfgB.Cables[0].Medium = fabric.TwistedPair

		for _, ch := range fabric.Diff(cfgA, cfgB) {
			if ch.Subject.Kind == "cable" && ch.Field == "medium" {
				t.Errorf("Diff reported medium %v -> %v for two cables that mean twisted pair", ch.From, ch.To)
			}
		}
	})

	t.Run("cable medium and delay modifications", func(t *testing.T) {
		cfgA := curCfg.Clone()
		cfgA.Cables[0].Medium = fabric.TwistedPair
		cfgA.Cables[0].Delay = nil

		cfgB := cfgA.Clone()
		d := 1 * time.Microsecond
		cfgB.Cables[0].Medium = fabric.SinglemodeFiber
		cfgB.Cables[0].Delay = &d

		changes := fabric.Diff(cfgA, cfgB)
		var foundMedium, foundDelay bool
		for _, ch := range changes {
			if ch.Subject.Kind == "cable" && ch.Subject.Key == "h1:-sw1:1/1/1" {
				if ch.Field == "medium" && ch.From == fabric.TwistedPair && ch.To == fabric.SinglemodeFiber {
					foundMedium = true
				}
				if ch.Field == "delay" && ch.From == nil && ch.To == 1*time.Microsecond {
					foundDelay = true
				}
			}
		}
		if !foundMedium {
			t.Errorf("missing cable medium change: %v", changes)
		}
		if !foundDelay {
			t.Errorf("missing cable delay change: %v", changes)
		}
	})

	t.Run("host moved port, address changed, vlan changed", func(t *testing.T) {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		tbl, _ := b.Build()

		vid10 := vlan.ID(10)
		vid20 := vlan.ID(20)

		cfgA := fabric.Config{
			Switches: map[string]vswitch.Config{
				"sw1": {Ports: tbl},
			},
			Hosts: map[string]fabric.Host{
				"h1": {Address: macH1, VLAN: &vid10},
			},
			Cables: []fabric.Cable{
				{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			},
		}

		macH1New := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}
		cfgB := fabric.Config{
			Switches: map[string]vswitch.Config{
				"sw1": {Ports: tbl},
			},
			Hosts: map[string]fabric.Host{
				"h1": {Address: macH1New, VLAN: &vid20},
			},
			Cables: []fabric.Cable{
				{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
			},
		}

		changes := fabric.Diff(cfgA, cfgB)

		var foundPort, foundAddr, foundVLAN bool
		for _, ch := range changes {
			if ch.Subject.Kind == "host" && ch.Subject.Key == "h1" {
				if ch.Field == "port" && ch.From == (fabric.Endpoint{Node: "sw1", Port: "1/1/1"}) &&
					ch.To == (fabric.Endpoint{Node: "sw1", Port: "1/1/2"}) {
					foundPort = true
				}
				if ch.Field == "address" && ch.From == macH1 && ch.To == macH1New {
					foundAddr = true
				}
				if ch.Field == "vlan" && ch.From == vid10 && ch.To == vid20 {
					foundVLAN = true
				}
			}
		}

		if !foundPort {
			t.Errorf("missing host port move change: %v", changes)
		}
		if !foundAddr {
			t.Errorf("missing host address change: %v", changes)
		}
		if !foundVLAN {
			t.Errorf("missing host vlan change: %v", changes)
		}
	})

	t.Run("switch and host added and removed", func(t *testing.T) {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		tbl, _ := b.Build()

		cfgA := fabric.Config{
			Switches: map[string]vswitch.Config{
				"sw1": {Ports: tbl},
			},
			Hosts: map[string]fabric.Host{
				"h1": {Address: macH1},
			},
			Cables: []fabric.Cable{
				{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			},
		}

		cfgB := fabric.Config{
			Switches: map[string]vswitch.Config{
				"sw2": {Ports: tbl},
			},
			Hosts: map[string]fabric.Host{
				"h2": {Address: macH1},
			},
			Cables: []fabric.Cable{
				{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
			},
		}

		changes := fabric.Diff(cfgA, cfgB)

		var (
			sw1Removed, sw2Added bool
			h1Removed, h2Added   bool
		)

		for _, ch := range changes {
			if ch.Subject.Kind == "switch" {
				if ch.Subject.Key == "sw1" && ch.Field == "" && ch.To == nil {
					sw1Removed = true
				}
				if ch.Subject.Key == "sw2" && ch.Field == "" && ch.From == nil {
					sw2Added = true
				}
			}
			if ch.Subject.Kind == "host" {
				if ch.Subject.Key == "h1" && ch.Field == "" && ch.To == nil {
					h1Removed = true
				}
				if ch.Subject.Key == "h2" && ch.Field == "" && ch.From == nil {
					h2Added = true
				}
			}
		}

		if !sw1Removed || !sw2Added {
			t.Errorf("switch add/remove diff failed: %v", changes)
		}
		if !h1Removed || !h2Added {
			t.Errorf("host add/remove diff failed: %v", changes)
		}
	})

	t.Run("host IP stack diff reports gateway and neighbor changes", func(t *testing.T) {
		gw1 := netip.MustParseAddr("10.0.10.1")
		gw2 := netip.MustParseAddr("10.0.10.254")
		nbr1 := netip.MustParseAddr("10.0.10.2")
		nbr2 := netip.MustParseAddr("10.0.10.3")
		mac1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
		mac2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x22}

		cfgA := fabric.Config{
			Hosts: map[string]fabric.Host{
				"h1": {
					Address: macH1,
					IP: &fabric.HostIP{
						Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.10/24")},
						Gateway:   gw1,
						Neighbors: map[netip.Addr]netaddr.MAC{
							nbr1: mac1,
						},
					},
				},
			},
		}

		cfgB := fabric.Config{
			Hosts: map[string]fabric.Host{
				"h1": {
					Address: macH1,
					IP: &fabric.HostIP{
						Addresses: []netip.Prefix{
							netip.MustParsePrefix("10.0.10.10/24"),
							netip.MustParsePrefix("fd00::10/64"),
						},
						Gateway: gw2,
						Neighbors: map[netip.Addr]netaddr.MAC{
							nbr1: mac1,
							nbr2: mac2,
						},
					},
				},
			},
		}

		changes := fabric.Diff(cfgA, cfgB)

		var foundAddrs, foundGW, foundNbr bool
		for _, ch := range changes {
			if ch.Subject.Kind == "host" && ch.Subject.Key == "h1" {
				if ch.Field == "addresses" {
					foundAddrs = true
					if !slices.Equal(ch.From.([]string), []string{"10.0.10.10/24"}) ||
						!slices.Equal(ch.To.([]string), []string{"10.0.10.10/24", "fd00::10/64"}) {
						t.Errorf("addresses diff = %v -> %v", ch.From, ch.To)
					}
				}
				if ch.Field == "gateway" {
					foundGW = true
					if ch.From != gw1 || ch.To != gw2 {
						t.Errorf("gateway diff = %v -> %v, want %v -> %v", ch.From, ch.To, gw1, gw2)
					}
				}
				if ch.Field == "neighbors.10.0.10.3" {
					foundNbr = true
					if ch.From != nil || ch.To != mac2 {
						t.Errorf("neighbor diff = %v -> %v, want nil -> %v", ch.From, ch.To, mac2)
					}
				}
			}
		}

		if !foundAddrs {
			t.Errorf("missing addresses diff: %v", changes)
		}
		if !foundGW {
			t.Errorf("missing gateway diff: %v", changes)
		}
		if !foundNbr {
			t.Errorf("missing neighbor diff: %v", changes)
		}
	})
}
