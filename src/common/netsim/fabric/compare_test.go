package fabric_test

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
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

func TestCompareEqualFabricsReturnsEquivalent(t *testing.T) {
	cfgA, macH1, macH2 := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	fabA, err := fabric.New(statedPhysical(cfgA))
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.New(statedPhysical(cfgB))
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

	cmp := fabric.Compare(fabA, fabB, scenario, 10)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}
	if cmp.Disposition != analysis.Equivalent {
		t.Errorf("Compare returned disposition: %v, want Equivalent", cmp.Disposition)
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

func TestCompareDetectsMirrorCopyDeliveryDifference(t *testing.T) {
	base, macs := newTrafficTopology(t, nil)
	currentCfg := base.Config()
	expectedCfg := base.Config()
	sw1 := expectedCfg.Switches["sw1"]
	sw1.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{{
		Name: "span", SelectSrcPorts: []string{"1/1/1"}, OutputPort: "1/1/4",
	}}}
	expectedCfg.Switches["sw1"] = sw1
	current, err := fabric.New(statedPhysical(currentCfg))
	if err != nil {
		t.Fatalf("New current: %v", err)
	}
	expected, err := fabric.New(statedPhysical(expectedCfg))
	if err != nil {
		t.Fatalf("New expected: %v", err)
	}
	scenario := []fabric.Injection{{
		At:     time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Src: macs["h1"], Dst: macs["h2"], Payload: make([]byte, 46)},
	}}

	comparison := fabric.Compare(current, expected, scenario, 100)
	if comparison.Err != nil {
		t.Fatalf("Compare: %v", comparison.Err)
	}
	if comparison.Disposition != analysis.Different {
		t.Errorf("Disposition = %v, want %v", comparison.Disposition, analysis.Different)
	}
	if comparison.Difference.Observable != "multiplicity" {
		t.Errorf("Difference.Observable = %q, want %q", comparison.Difference.Observable, "multiplicity")
	}
	if len(comparison.Current) != 1 || len(comparison.Expected) != 2 {
		t.Errorf("journey counts = %d/%d, want 1/2", len(comparison.Current), len(comparison.Expected))
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
	for _, swName := range []string{"sw1", "sw2"} {
		swCfg := expCfg.Switches[swName]
		b := port.NewBuilder()
		for _, p := range swCfg.Ports.Ports() {
			if p.Name == "1/1/24" {
				p.OperStatus = port.Down
			}
			b.Add(p)
		}
		swCfg.Ports, _ = b.Build()
		expCfg.Switches[swName] = swCfg
	}

	curFab, err := fabric.New(statedPhysical(curCfg))
	if err != nil {
		t.Fatalf("New curFab: %v", err)
	}
	expFab, err := fabric.New(statedPhysical(expCfg))
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

	cmp := fabric.Compare(curFab, expFab, scenario, 10)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}
	if cmp.Disposition != analysis.Different {
		t.Errorf("Compare disposition: %v, want Different", cmp.Disposition)
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
		if ch.Subject.Kind == "port" && ch.Subject.Key == "sw2/1%2F1%2F1" {
			if ch.Field == "pvid" {
				foundSwitchportChange = true
			}
		}
		if ch.Subject.Kind == "cable" && ch.Subject.Key == "sw1:1/1/24-sw2:1/1/24" {
			if ch.Field == "fault" && ch.Layer == fabric.Layer {
				foundCableFaultChange = true
				if ch.From.TypeID() != "fabric.fault" || !strings.Contains(ch.From.Canonical(), string(fabric.FaultNone)) {
					t.Errorf("cable fault From = %v:%q, want FaultNone snapshot", ch.From.TypeID(), ch.From.Canonical())
				}
				if ch.To.TypeID() != "fabric.fault" || !strings.Contains(ch.To.Canonical(), string(fabric.FaultCut)) {
					t.Errorf("cable fault To = %v:%q, want FaultCut snapshot", ch.To.TypeID(), ch.To.Canonical())
				}
			}
		}
	}

	if !foundSwitchportChange {
		t.Errorf("Diff missing switchport change under sw2/1%%2F1%%2F1: %v", changes)
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

	curFab, err := fabric.New(statedPhysical(curCfg))
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

	derivedFab, err := fabric.Derive(curFab, constructionSpec(statedPhysical(expCfg)))
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
	curFab, err := fabric.New(statedPhysical(curCfg))
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

	derived, err := fabric.Derive(curFab, constructionSpec(statedPhysical(expCfg)))
	if err != nil {
		t.Fatalf("Derive with new switch: %v", err)
	}
	if derived.Switch("sw3") == nil {
		t.Errorf("derived switch sw3 is nil, want fresh switch")
	}

	// From nil cur
	fromNil, err := fabric.Derive(nil, constructionSpec(statedPhysical(expCfg)))
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
				if ch.Field == "length" && ch.From == fabric.LengthFact(0) && ch.To == fabric.LengthFact(50) {
					foundLen = true
				}
				if ch.Field == "top_speed" && ch.From == fabric.TopSpeedFact(0) && ch.To == fabric.TopSpeedFact(100_000_000) {
					foundSpeed = true
				}
			}
		}
		if !foundLen || !foundSpeed {
			t.Errorf("missing cable length or top_speed change: %v", changes)
		}
	})

	t.Run("unspecified medium diffs against twisted pair", func(t *testing.T) {
		cfgA := curCfg.Clone()
		cfgA.Cables[0].Medium = fabric.MediumUnspecified

		cfgB := cfgA.Clone()
		cfgB.Cables[0].Medium = fabric.TwistedPair

		from, to := changedFieldFacts(t, "medium", cfgA, cfgB)
		if from != fabric.MediumUnspecified || to != fabric.TwistedPair {
			t.Errorf("medium change = %v -> %v, want Unspecified -> TwistedPair", from, to)
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
				if ch.Field == "delay" && ch.From == nil && ch.To == fabric.DelayFact(1*time.Microsecond) {
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
				if ch.Field == "address" && ch.From == fabric.MACFact(macH1) && ch.To == fabric.MACFact(macH1New) {
					foundAddr = true
				}
				if ch.Field == "vlan" && ch.From == fabric.VLANFact(vid10) && ch.To == fabric.VLANFact(vid20) {
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
					if !trace.EqualFact(ch.From, fabric.PrefixesFact([]string{"10.0.10.10/24"})) ||
						!trace.EqualFact(ch.To, fabric.PrefixesFact([]string{"10.0.10.10/24", "fd00::10/64"})) {
						t.Errorf("addresses diff = %v -> %v", ch.From, ch.To)
					}
				}
				if ch.Field == "gateway" {
					foundGW = true
					if ch.From != fabric.GatewayFact(gw1) || ch.To != fabric.GatewayFact(gw2) {
						t.Errorf("gateway diff = %v -> %v, want %v -> %v", ch.From, ch.To, gw1, gw2)
					}
				}
				if ch.Field == "neighbors.10.0.10.3" {
					foundNbr = true
					if ch.From != nil || ch.To != fabric.MACFact(mac2) {
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

func TestDiffDetectsReversedDirectionalFaults(t *testing.T) {
	for _, kind := range []fabric.FaultKind{fabric.FaultDeadAToB, fabric.FaultDeadBToA} {
		t.Run(string(kind), func(t *testing.T) {
			cfgA, _, _ := makeTwoSwitchConfigs(t)
			cfgA.Cables[1].Fault = fabric.Fault{Kind: kind}
			cfgB := cfgA.Clone()
			cfgB.Cables[1].A, cfgB.Cables[1].B = cfgB.Cables[1].B, cfgB.Cables[1].A

			changes := fabric.Diff(cfgA, cfgB)
			var faultChange *trace.Change
			for i := range changes {
				if changes[i].Subject.Kind == "cable" && changes[i].Field == "fault" {
					faultChange = &changes[i]
					break
				}
			}
			if faultChange == nil {
				t.Fatal("Diff missed behavior change from reversing a directional cable fault")
			}
			if trace.EqualFact(faultChange.From, faultChange.To) {
				t.Errorf("fault change has equal facts: %v -> %v", faultChange.From, faultChange.To)
			}
			wantFrom, wantTo := kind, fabric.FaultDeadAToB
			if kind == fabric.FaultDeadAToB {
				wantTo = fabric.FaultDeadBToA
			}
			if !strings.Contains(faultChange.From.Canonical(), string(wantFrom)) ||
				!strings.Contains(faultChange.To.Canonical(), string(wantTo)) {
				t.Errorf("fault change = %q -> %q, want %q -> %q", faultChange.From.Canonical(), faultChange.To.Canonical(), wantFrom, wantTo)
			}

			reverseFrom, reverseTo := changedFieldFacts(t, "fault", cfgB, cfgA)
			if !strings.Contains(reverseFrom.Canonical(), string(wantTo)) ||
				!strings.Contains(reverseTo.Canonical(), string(wantFrom)) {
				t.Errorf("reverse fault change = %q -> %q, want %q -> %q", reverseFrom.Canonical(), reverseTo.Canonical(), wantTo, wantFrom)
			}

			equivalent := cfgA.Clone()
			equivalent.Cables[1].A, equivalent.Cables[1].B = equivalent.Cables[1].B, equivalent.Cables[1].A
			if kind == fabric.FaultDeadAToB {
				equivalent.Cables[1].Fault.Kind = fabric.FaultDeadBToA
			} else {
				equivalent.Cables[1].Fault.Kind = fabric.FaultDeadAToB
			}
			if changes := fabric.Diff(cfgA, equivalent); len(changes) != 0 {
				t.Errorf("equivalent reversed cable diff = %v, want no changes", changes)
			}
		})
	}
}

func TestDiffUsesLosslessImmutableSnapshotFacts(t *testing.T) {
	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)
	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	gateway := netip.MustParseAddr("10.0.10.1")
	host := fabric.Host{
		Address: mac,
		VLAN:    &vid10,
		IP: &fabric.HostIP{
			Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.10/24")},
			Gateway:   gateway,
			Neighbors: map[netip.Addr]netaddr.MAC{
				gateway: {0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
			},
		},
	}

	hostCfgA := fabric.Config{Hosts: map[string]fabric.Host{"h1": host}}
	hostCfgB := hostCfgA.Clone()
	hostB := hostCfgB.Hosts["h1"]
	hostB.VLAN = &vid20
	hostCfgB.Hosts["h1"] = hostB
	hostFactA := addedFact(t, "host", hostCfgA)
	hostFactB := addedFact(t, "host", hostCfgB)
	if trace.EqualFact(hostFactA, hostFactB) {
		t.Error("host snapshot facts collide when VLAN behavior differs")
	}
	if got := reflect.TypeOf(hostFactA); got == reflect.TypeOf(fabric.Host{}) {
		t.Errorf("host diff fact has mutable config type %v", got)
	}
	hostVariants := []struct {
		name   string
		mutate func(*fabric.Host)
	}{
		{name: "address", mutate: func(h *fabric.Host) { h.Address[5]++ }},
		{name: "IP presence", mutate: func(h *fabric.Host) { h.IP = nil }},
		{name: "IP address", mutate: func(h *fabric.Host) {
			h.IP.Addresses[0] = netip.MustParsePrefix("10.0.20.10/24")
		}},
		{name: "gateway", mutate: func(h *fabric.Host) {
			h.IP.Gateway = netip.MustParseAddr("10.0.10.254")
		}},
		{name: "neighbor", mutate: func(h *fabric.Host) {
			h.IP.Neighbors[gateway] = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}
		}},
	}
	for _, variant := range hostVariants {
		t.Run("host "+variant.name, func(t *testing.T) {
			changed := host.Clone()
			variant.mutate(&changed)
			fact := addedFact(t, "host", fabric.Config{Hosts: map[string]fabric.Host{"h1": changed}})
			if trace.EqualFact(hostFactA, fact) {
				t.Errorf("host snapshot facts collide when %s differs", variant.name)
			}
		})
	}

	delay := time.Nanosecond
	cableA := fabric.Cable{
		A: fabric.Endpoint{Node: "left", Port: "one"},
		B: fabric.Endpoint{Node: "right", Port: "two"},
	}
	cableB := cableA.Clone()
	cableB.Delay = &delay
	cableB.Fault = fabric.Fault{Kind: fabric.FaultCut}
	cableCfgA := fabric.Config{Cables: []fabric.Cable{cableA}}
	cableCfgB := fabric.Config{Cables: []fabric.Cable{cableB}}
	cableFactA := addedFact(t, "cable", cableCfgA)
	cableFactB := addedFact(t, "cable", cableCfgB)
	if got := reflect.TypeOf(cableFactA); got == reflect.TypeOf(fabric.Cable{}) {
		t.Errorf("cable diff fact has mutable config type %v", got)
	}
	cableVariants := []struct {
		name   string
		mutate func(*fabric.Cable)
	}{
		{name: "endpoint node", mutate: func(c *fabric.Cable) { c.A.Node = "left:one" }},
		{name: "endpoint port", mutate: func(c *fabric.Cable) { c.A.Port = "one:two" }},
		{name: "length", mutate: func(c *fabric.Cable) { c.LengthMeters = 1.5 }},
		{name: "top speed", mutate: func(c *fabric.Cable) { c.TopSpeedBPS = 1_000_000_000 }},
		{name: "fault", mutate: func(c *fabric.Cable) { c.Fault = fabric.Fault{Kind: fabric.FaultCut} }},
		{name: "medium", mutate: func(c *fabric.Cable) { c.Medium = fabric.SinglemodeFiber }},
		{name: "delay", mutate: func(c *fabric.Cable) { c.Delay = &delay }},
	}
	for _, variant := range cableVariants {
		t.Run("cable "+variant.name, func(t *testing.T) {
			changed := cableA.Clone()
			variant.mutate(&changed)
			fact := addedFact(t, "cable", fabric.Config{Cables: []fabric.Cable{changed}})
			if trace.EqualFact(cableFactA, fact) {
				t.Errorf("cable snapshot facts collide when %s differs", variant.name)
			}
		})
	}

	faultCfgA := fabric.Config{Cables: []fabric.Cable{{
		A:     cableA.A,
		B:     cableA.B,
		Fault: fabric.Fault{Kind: fabric.FaultLoseSequence, Sequence: []uint{1}},
	}}}
	faultCfgB := faultCfgA.Clone()
	faultCfgB.Cables[0].Fault.Sequence[0] = 2
	faultFactA, faultFactB := changedFieldFacts(t, "fault", faultCfgA, faultCfgB)
	if trace.EqualFact(faultFactA, faultFactB) {
		t.Error("fault snapshot facts collide when loss sequence differs")
	}
	if got := reflect.TypeOf(faultFactA); got == reflect.TypeOf(fabric.Fault{}) {
		t.Errorf("fault diff fact has mutable config type %v", got)
	}

	wantHost := hostFactA.Canonical()
	wantCable := cableFactB.Canonical()
	wantFault := faultFactA.Canonical()
	*hostCfgA.Hosts["h1"].VLAN = 30
	hostCfgA.Hosts["h1"].IP.Addresses[0] = netip.MustParsePrefix("10.0.30.10/24")
	hostCfgA.Hosts["h1"].IP.Neighbors[gateway] = netaddr.MAC{}
	*cableCfgB.Cables[0].Delay = 2 * time.Nanosecond
	cableCfgB.Cables[0].Fault.Kind = fabric.FaultNone
	faultCfgA.Cables[0].Fault.Sequence[0] = 99
	if got := hostFactA.Canonical(); got != wantHost {
		t.Errorf("host fact changed after source mutation: got %q, want %q", got, wantHost)
	}
	if got := cableFactB.Canonical(); got != wantCable {
		t.Errorf("cable fact changed after source mutation: got %q, want %q", got, wantCable)
	}
	if got := faultFactA.Canonical(); got != wantFault {
		t.Errorf("fault fact changed after source mutation: got %q, want %q", got, wantFault)
	}
}

func addedFact(t *testing.T, kind string, cfg fabric.Config) trace.Fact {
	t.Helper()

	for _, change := range fabric.Diff(fabric.Config{}, cfg) {
		if change.Subject.Kind == kind && change.Field == "" && change.To != nil {
			return change.To
		}
	}
	t.Fatalf("missing added %s fact", kind)

	return nil
}

func changedFieldFacts(t *testing.T, field string, a, b fabric.Config) (trace.Fact, trace.Fact) {
	t.Helper()

	for _, change := range fabric.Diff(a, b) {
		if change.Field == field {
			return change.From, change.To
		}
	}
	t.Fatalf("missing %s change", field)

	return nil, nil
}

func TestCompareDetectsPathDifference(t *testing.T) {
	cfgA, macH1, macH2 := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	// Change cable medium between sw1 and sw2 in cfgB.
	cfgB.Cables[1].Medium = fabric.SinglemodeFiber

	fabA, err := fabric.New(statedPhysical(cfgA))
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.New(statedPhysical(cfgB))
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	scenario := []fabric.Injection{
		{
			At:     time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test"),
			},
		},
	}

	cmp := fabric.Compare(fabA, fabB, scenario, 10)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}
	if cmp.Disposition != analysis.Different {
		t.Errorf("Disposition = %v, want %v", cmp.Disposition, analysis.Different)
	}
	if cmp.Difference.Observable != "path" {
		t.Errorf("Difference.Observable = %q, want %q", cmp.Difference.Observable, "path")
	}
}

func TestCompareDetectsTimingDifference(t *testing.T) {
	cfgA, macH1, macH2 := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	delayA := 1 * time.Microsecond
	delayB := 5 * time.Microsecond
	cfgA.Cables[1].Delay = &delayA
	cfgB.Cables[1].Delay = &delayB

	fabA, err := fabric.New(statedPhysical(cfgA))
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.New(statedPhysical(cfgB))
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	scenario := []fabric.Injection{
		{
			At:     time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test"),
			},
		},
	}

	cmp := fabric.Compare(fabA, fabB, scenario, 10)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}
	if cmp.Disposition != analysis.Different {
		t.Errorf("Disposition = %v, want %v", cmp.Disposition, analysis.Different)
	}
	if cmp.Difference.Observable != "timing" {
		t.Errorf("Difference.Observable = %q, want %q", cmp.Difference.Observable, "timing")
	}
}

func TestCompareDetectsDropLocationDifference(t *testing.T) {
	base, macs := newTrafficTopology(t, nil)
	cfgA := base.Config()
	cfgB := base.Config()

	// In both configs: cut cable to h4 (on 1/1/2) and cable to h3 (on 1/1/4).
	for i := range cfgA.Cables {
		if cfgA.Cables[i].A.Port == "1/1/2" || cfgA.Cables[i].B.Port == "1/1/2" {
			cfgA.Cables[i].Fault = fabric.Fault{Kind: fabric.FaultCut}
			cfgB.Cables[i].Fault = fabric.Fault{Kind: fabric.FaultCut}
		}
		if cfgA.Cables[i].A.Port == "1/1/4" || cfgA.Cables[i].B.Port == "1/1/4" {
			cfgA.Cables[i].Fault = fabric.Fault{Kind: fabric.FaultCut}
			cfgB.Cables[i].Fault = fabric.Fault{Kind: fabric.FaultCut}
		}
	}
	for _, cfg := range []*fabric.Config{&cfgA, &cfgB} {
		swCfg := cfg.Switches["sw1"]
		b := port.NewBuilder()
		for _, p := range swCfg.Ports.Ports() {
			if p.Name == "1/1/2" || p.Name == "1/1/4" {
				p.OperStatus = port.Down
			}
			b.Add(p)
		}
		swCfg.Ports, _ = b.Build()
		cfg.Switches["sw1"] = swCfg
	}

	targetMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}

	specA := constructionSpec(statedPhysical(cfgA))
	sw1A := specA.Switches["sw1"]
	sw1A.Seeds = []bridge.Seed{{MAC: targetMAC, FID: 10, Port: "1/1/2", Lifetime: bridge.Static}}
	specA.Switches["sw1"] = sw1A

	specB := constructionSpec(statedPhysical(cfgB))
	sw1B := specB.Switches["sw1"]
	sw1B.Seeds = []bridge.Seed{{MAC: targetMAC, FID: 10, Port: "1/1/4", Lifetime: bridge.Static}}
	specB.Switches["sw1"] = sw1B

	fabA, err := fabric.NewWithSpec(specA)
	if err != nil {
		t.Fatalf("NewWithSpec fabA: %v", err)
	}
	fabB, err := fabric.NewWithSpec(specB)
	if err != nil {
		t.Fatalf("NewWithSpec fabB: %v", err)
	}

	scenario := []fabric.Injection{
		{
			At:     time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       targetMAC,
				Src:       macs["h1"],
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test"),
			},
		},
	}

	cmp := fabric.Compare(fabA, fabB, scenario, 10)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}
	if cmp.Disposition != analysis.Different {
		t.Errorf("Disposition = %v, want %v", cmp.Disposition, analysis.Different)
	}
	if cmp.Difference.Observable != "drop location" {
		t.Errorf("Difference.Observable = %q, want %q", cmp.Difference.Observable, "drop location")
	}
	if cmp.Difference.Current != "sw1/1/1/2" || cmp.Difference.Expected != "sw1/1/1/4" {
		t.Errorf("Difference current/expected = %q/%q, want sw1/1/1/2/sw1/1/1/4", cmp.Difference.Current, cmp.Difference.Expected)
	}
}

func TestCompareDetectsJourneyTerminalDifference(t *testing.T) {
	cfgA, macH1, macH2 := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	// Cut cable to h2 in cfgB so frame drops instead of delivering.
	for i := range cfgB.Cables {
		if cfgB.Cables[i].A.Node == "h2" || cfgB.Cables[i].B.Node == "h2" {
			cfgB.Cables[i].Fault = fabric.Fault{Kind: fabric.FaultCut}
		}
	}
	sw2Ports := cfgB.Switches["sw2"].Ports.Ports()
	b := port.NewBuilder()
	for _, p := range sw2Ports {
		if p.Name == "1/1/1" {
			p.OperStatus = port.Down
		}
		b.Add(p)
	}
	sw2Cfg := cfgB.Switches["sw2"]
	sw2Cfg.Ports, _ = b.Build()
	cfgB.Switches["sw2"] = sw2Cfg

	fabA, err := fabric.New(statedPhysical(cfgA))
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.New(statedPhysical(cfgB))
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	scenario := []fabric.Injection{
		{
			At:     time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test"),
			},
		},
	}

	cmp := fabric.Compare(fabA, fabB, scenario, 10)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}
	if cmp.Disposition != analysis.Different {
		t.Errorf("Disposition = %v, want %v", cmp.Disposition, analysis.Different)
	}
	if cmp.Difference.Observable != "journey terminal" {
		t.Errorf("Difference.Observable = %q, want %q", cmp.Difference.Observable, "journey terminal")
	}
}

func TestCompareDetectsFinalStateDifference(t *testing.T) {
	cfgA, macH1, _ := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	specA := constructionSpec(statedPhysical(cfgA))
	specB := constructionSpec(statedPhysical(cfgB))

	// In cfgB, sw2 has an extra static seed.
	sw2B := specB.Switches["sw2"]
	sw2B.Seeds = []bridge.Seed{{
		MAC:      netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x88},
		FID:      10,
		Port:     "1/1/1",
		Lifetime: bridge.Static,
	}}
	specB.Switches["sw2"] = sw2B

	fabA, err := fabric.NewWithSpec(specA)
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.NewWithSpec(specB)
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	// Scenario frame stays between h1 and sw1.
	scenario := []fabric.Injection{
		{
			At:     time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH1,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test"),
			},
		},
	}

	cmp := fabric.Compare(fabA, fabB, scenario, 10)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}
	if cmp.Disposition != analysis.Different {
		t.Errorf("Disposition = %v, want %v", cmp.Disposition, analysis.Different)
	}
	if cmp.Difference.Observable != "final state" {
		t.Errorf("Difference.Observable = %q, want %q", cmp.Difference.Observable, "final state")
	}
}

func TestCompareDetectsStatusDifference(t *testing.T) {
	cfgA, macH1, macH2 := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	specA := constructionSpec(statedPhysical(cfgA))
	specB := constructionSpec(statedPhysical(cfgB))

	// sw1 in specB carries an issue with status Incomplete on 1/1/1.
	sw1B := specB.Switches["sw1"]
	sw1B.Metadata = analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{
		{Code: "test.issue", Status: analysis.Incomplete, Scope: analysis.PortScope("sw1", "1/1/1"), Message: "test issue"},
	}, analysis.EvidenceCatalog{}, nil)
	specB.Switches["sw1"] = sw1B

	fabA, err := fabric.NewWithSpec(specA)
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.NewWithSpec(specB)
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	scenario := []fabric.Injection{
		{
			At:     time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test"),
			},
		},
	}

	cmp := fabric.Compare(fabA, fabB, scenario, 10)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}
	if cmp.Disposition != analysis.Inconclusive {
		t.Errorf("Disposition = %v, want %v", cmp.Disposition, analysis.Inconclusive)
	}
	if cmp.Difference.Observable != "status" {
		t.Errorf("Difference.Observable = %q, want %q", cmp.Difference.Observable, "status")
	}
}

func TestCompareDetectsIssuesDifference(t *testing.T) {
	cfgA, macH1, macH2 := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	specA := constructionSpec(statedPhysical(cfgA))
	sw1A := specA.Switches["sw1"]
	sw1A.Metadata = analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{
		{Code: "test.issue.a", Status: analysis.Incomplete, Scope: analysis.PortScope("sw1", "1/1/1"), Message: "issue a"},
	}, analysis.EvidenceCatalog{}, nil)
	specA.Switches["sw1"] = sw1A

	specB := constructionSpec(statedPhysical(cfgB))
	sw1B := specB.Switches["sw1"]
	sw1B.Metadata = analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{
		{Code: "test.issue.b", Status: analysis.Incomplete, Scope: analysis.PortScope("sw1", "1/1/1"), Message: "issue b"},
	}, analysis.EvidenceCatalog{}, nil)
	specB.Switches["sw1"] = sw1B

	fabA, err := fabric.NewWithSpec(specA)
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.NewWithSpec(specB)
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	scenario := []fabric.Injection{
		{
			At:     time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test"),
			},
		},
	}

	cmp := fabric.Compare(fabA, fabB, scenario, 10)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}
	if cmp.Disposition != analysis.Inconclusive {
		t.Errorf("Disposition = %v, want %v", cmp.Disposition, analysis.Inconclusive)
	}
	if cmp.Difference.Observable != "issues" {
		t.Errorf("Difference.Observable = %q, want %q", cmp.Difference.Observable, "issues")
	}
}

func TestCompareNonConsuming(t *testing.T) {
	cfgA, macH1, macH2 := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	fabA, err := fabric.New(statedPhysical(cfgA))
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.New(statedPhysical(cfgB))
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}
	twinA, err := fabric.New(statedPhysical(cfgA))
	if err != nil {
		t.Fatalf("New twinA: %v", err)
	}

	snapABefore := fabA.Snapshot()
	snapBBefore := fabB.Snapshot()

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	scenario := []fabric.Injection{
		{
			At:     now,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test"),
			},
		},
	}

	cmp := fabric.Compare(fabA, fabB, scenario, 10)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}

	// 1. Both inputs unchanged after Compare.
	snapAAfter := fabA.Snapshot()
	snapBAfter := fabB.Snapshot()
	if snapAAfter.Clock != snapABefore.Clock {
		t.Errorf("fabA clock changed: before=%v, after=%v", snapABefore.Clock, snapAAfter.Clock)
	}
	if snapBAfter.Clock != snapBBefore.Clock {
		t.Errorf("fabB clock changed: before=%v, after=%v", snapBBefore.Clock, snapBAfter.Clock)
	}
	if len(fabA.Report()) != 0 || len(fabB.Report()) != 0 {
		t.Errorf("fabA or fabB has recorded journeys: len(fabA)=%d, len(fabB)=%d", len(fabA.Report()), len(fabB.Report()))
	}

	// 2. Re-run of fabA matches a never-compared twin.
	_, err = fabA.Inject(scenario[0])
	if err != nil {
		t.Fatalf("Inject fabA: %v", err)
	}
	resA := fabA.Run(10)

	_, err = twinA.Inject(scenario[0])
	if err != nil {
		t.Fatalf("Inject twinA: %v", err)
	}
	resTwin := twinA.Run(10)

	if resA.Steps != resTwin.Steps {
		t.Errorf("Steps mismatch: fabA=%d, twinA=%d", resA.Steps, resTwin.Steps)
	}
	if len(fabA.Report()) != len(twinA.Report()) {
		t.Errorf("Report length mismatch: fabA=%d, twinA=%d", len(fabA.Report()), len(twinA.Report()))
	}
}

func TestCompareActivePeriodicProtocolRepeatability(t *testing.T) {
	cfgA, macH1, macH2 := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)
	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	cfgA.Start = t0
	cfgB.Start = t0

	// Configure STP on both switches.
	sw1STP := &stp.Config{
		Priority: 4096,
		Address:  netaddr.MAC{0, 0, 0, 0, 1, 1},
		Ports: map[string]stp.Port{
			"1/1/1":  {},
			"1/1/24": {},
		},
	}
	sw2STP := &stp.Config{
		Priority: 8192,
		Address:  netaddr.MAC{0, 0, 0, 0, 1, 2},
		Ports: map[string]stp.Port{
			"1/1/1":  {},
			"1/1/24": {},
		},
	}
	s1A := cfgA.Switches["sw1"]
	s1A.STP = sw1STP
	cfgA.Switches["sw1"] = s1A
	s2A := cfgA.Switches["sw2"]
	s2A.STP = sw2STP
	cfgA.Switches["sw2"] = s2A

	s1B := cfgB.Switches["sw1"]
	s1B.STP = sw1STP
	cfgB.Switches["sw1"] = s1B
	s2B := cfgB.Switches["sw2"]
	s2B.STP = sw2STP
	cfgB.Switches["sw2"] = s2B

	fabA, err := fabric.New(statedPhysical(cfgA))
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.New(statedPhysical(cfgB))
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	scenario := []fabric.Injection{
		{
			At:     time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test"),
			},
		},
	}

	cmp1 := fabric.Compare(fabA, fabB, scenario, 10)
	cmp2 := fabric.Compare(fabA, fabB, scenario, 10)

	if cmp1.Disposition != cmp2.Disposition {
		t.Errorf("Disposition mismatch: %v vs %v", cmp1.Disposition, cmp2.Disposition)
	}
	if cmp1.Steps != cmp2.Steps {
		t.Errorf("Steps mismatch: %v vs %v", cmp1.Steps, cmp2.Steps)
	}
	if cmp1.Difference != cmp2.Difference {
		t.Errorf("Difference mismatch: %v vs %v", cmp1.Difference, cmp2.Difference)
	}
}

func TestCompareMidRunInputsOrdinalPairing(t *testing.T) {
	cfgA, macH1, macH2 := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	fabA, err := fabric.New(statedPhysical(cfgA))
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.New(statedPhysical(cfgB))
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	// Inject 2 pre-scenario frames into fabA so both hosts are learned.
	_, err = fabA.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       macH2,
			Src:       macH1,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("pre-run A 1"),
		},
	})
	if err != nil {
		t.Fatalf("Inject fabA: %v", err)
	}
	_, err = fabA.Inject(fabric.Injection{
		At:     now.Add(time.Millisecond),
		Origin: fabric.Endpoint{Node: "h2"},
		Frame: ethernet.Frame{
			Dst:       macH1,
			Src:       macH2,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("pre-run A 2"),
		},
	})
	if err != nil {
		t.Fatalf("Inject fabA: %v", err)
	}
	fabA.Run(10)

	// Inject 3 pre-scenario frames into fabB in a different order/timing.
	_, err = fabB.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "h2"},
		Frame: ethernet.Frame{
			Dst:       macH1,
			Src:       macH2,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("pre-run B 1"),
		},
	})
	if err != nil {
		t.Fatalf("Inject fabB 1: %v", err)
	}
	_, err = fabB.Inject(fabric.Injection{
		At:     now.Add(2 * time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       macH2,
			Src:       macH1,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("pre-run B 2"),
		},
	})
	if err != nil {
		t.Fatalf("Inject fabB 2: %v", err)
	}
	_, err = fabB.Inject(fabric.Injection{
		At:     now.Add(4 * time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       macH2,
			Src:       macH1,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("pre-run B 3"),
		},
	})
	if err != nil {
		t.Fatalf("Inject fabB 3: %v", err)
	}
	fabB.Run(10)

	// Now run Compare with a fresh scenario.
	scenario := []fabric.Injection{
		{
			At:     now.Add(time.Second),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("scenario frame"),
			},
		},
	}

	cmp := fabric.Compare(fabA, fabB, scenario, 10)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}
	if cmp.Disposition != analysis.Equivalent {
		t.Errorf("Disposition = %v, want Equivalent", cmp.Disposition)
	}
	// Verify pre-scenario journeys are excluded!
	if len(cmp.Current) != 1 {
		t.Errorf("Current journeys = %d, want 1 (pre-scenario journeys must be excluded)", len(cmp.Current))
	}
	if len(cmp.Expected) != 1 {
		t.Errorf("Expected journeys = %d, want 1 (pre-scenario journeys must be excluded)", len(cmp.Expected))
	}
}

func TestCompareEquivalentComplete(t *testing.T) {
	cfgA, macH1, macH2 := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	fabA, err := fabric.New(statedPhysical(cfgA))
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.New(statedPhysical(cfgB))
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	scenario := []fabric.Injection{
		{
			At:     time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test"),
			},
		},
	}

	cmp := fabric.Compare(fabA, fabB, scenario, 10)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}
	if cmp.Disposition != analysis.Equivalent {
		t.Errorf("Disposition = %v, want Equivalent", cmp.Disposition)
	}
	if cmp.Difference != (fabric.Difference{}) {
		t.Errorf("Difference = %v, want empty", cmp.Difference)
	}
}

func TestCompareInconclusiveIncomplete(t *testing.T) {
	cfgA, macH1, macH2 := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	fabA, err := fabric.New(statedPhysical(cfgA))
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.New(statedPhysical(cfgB))
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	scenario := []fabric.Injection{
		{
			At:     time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test"),
			},
		},
	}

	// Budget of 1 step is exhausted before frame arrives at h2.
	cmp := fabric.Compare(fabA, fabB, scenario, 1)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}
	if cmp.Disposition != analysis.Inconclusive {
		t.Errorf("Disposition = %v, want Inconclusive", cmp.Disposition)
	}
	if cmp.Difference != (fabric.Difference{}) {
		t.Errorf("Difference = %v, want empty", cmp.Difference)
	}
}

func TestCompareResultImmutability(t *testing.T) {
	cfgA, macH1, macH2 := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	fabA, err := fabric.New(statedPhysical(cfgA))
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.New(statedPhysical(cfgB))
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	scenario := []fabric.Injection{
		{
			At:     time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test"),
			},
		},
	}

	cmp := fabric.Compare(fabA, fabB, scenario, 10)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}

	curCount := len(cmp.Current)
	expCount := len(cmp.Expected)
	steps := cmp.Steps
	disposition := cmp.Disposition

	// Step fabA and fabB further.
	fabA.Run(5)
	fabB.Run(5)

	if len(cmp.Current) != curCount || len(cmp.Expected) != expCount {
		t.Error("Comparison journeys count changed after external stepping")
	}
	if cmp.Steps != steps {
		t.Errorf("Comparison steps changed: got %v, want %v", cmp.Steps, steps)
	}
	if cmp.Disposition != disposition {
		t.Errorf("Comparison disposition changed: got %v, want %v", cmp.Disposition, disposition)
	}
}

func TestCompareAsymmetricCompletionInconclusive(t *testing.T) {
	cfgA, macH1, macH2 := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	fabA, err := fabric.New(statedPhysical(cfgA))
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.New(statedPhysical(cfgB))
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	// Pre-inject background frames into fabB only so its queue has pending work requiring more steps.
	for i := 0; i < 5; i++ {
		_, err = fabB.Inject(fabric.Injection{
			At:     now,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH1,
				Src:       macH2,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("background"),
			},
		})
		if err != nil {
			t.Fatalf("Inject background: %v", err)
		}
	}

	scenario := []fabric.Injection{
		{
			At:     now,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("scenario frame"),
			},
		},
	}

	// Budget of 2 steps: fabA completes (StopQueueDrained, Complete)
	// fabB stops at budget (StopBudget, Exhausted) with identical scenario journey.
	cmp := fabric.Compare(fabA, fabB, scenario, 2)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}
	if cmp.Disposition != analysis.Inconclusive {
		t.Fatalf("Disposition = %v, want Inconclusive (Difference = %v)", cmp.Disposition, cmp.Difference)
	}
}

func TestComparePacketOriginImmutability(t *testing.T) {
	cfgA, _, _ := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)
	fabA, err := fabric.New(statedPhysical(cfgA))
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.New(statedPhysical(cfgB))
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	origPayload := []byte("original packet payload")
	pkt := &fabric.Packet{
		To:       netip.MustParseAddr("10.0.10.2"),
		Protocol: 17,
		Payload:  append([]byte(nil), origPayload...),
	}
	scenario := []fabric.Injection{
		{
			At:     now,
			Origin: fabric.Endpoint{Node: "h1"},
			Packet: pkt,
		},
	}

	cmp := fabric.Compare(fabA, fabB, scenario, 10)
	// Mutate caller's packet pointer and payload
	pkt.Protocol = 6
	pkt.Payload[0] = 'X'

	for idx, replay := range cmp.Replay {
		replayPkt := replay.Scenario.Actions[0].Inject.Packet
		if replayPkt.Protocol != 17 {
			t.Errorf("Replay[%d] Packet.Protocol was mutated: got %d, want 17", idx, replayPkt.Protocol)
		}
		if string(replayPkt.Payload) != string(origPayload) {
			t.Errorf("Replay[%d] Packet.Payload was mutated: got %q, want %q", idx, replayPkt.Payload, origPayload)
		}
	}
}

func TestCompareNonEmptyQueueExcludesPreScenarioDescendants(t *testing.T) {
	trafficCfg := &traffic.Config{
		Mirrors: []traffic.Mirror{{
			Name:           "span",
			SelectSrcPorts: []string{"1/1/1"},
			OutputPort:     "1/1/4",
		}},
	}
	fabA, macs := newTrafficTopology(t, trafficCfg)
	fabB, _ := newTrafficTopology(t, trafficCfg)

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	// Inject a pre-scenario frame into fabA only, scheduled at now.
	// It sits in the arrival queue of fabA.
	_, err := fabA.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       macs["h2"],
			Src:       macs["h1"],
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("pre-scenario"),
		},
	})
	if err != nil {
		t.Fatalf("Inject pre-scenario: %v", err)
	}

	// Scenario frame injected on h3 at now.Add(time.Second)
	scenario := []fabric.Injection{
		{
			At:     now.Add(time.Second),
			Origin: fabric.Endpoint{Node: "h3"},
			Frame: ethernet.Frame{
				Dst:       macs["h4"],
				Src:       macs["h3"],
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("scenario frame"),
			},
		},
	}

	cmp := fabric.Compare(fabA, fabB, scenario, 10)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}
	if len(cmp.Current) != 1 {
		t.Errorf("Current journeys = %d, want 1 (pre-scenario descendants must be excluded)", len(cmp.Current))
	}
	if len(cmp.Expected) != 1 {
		t.Errorf("Expected journeys = %d, want 1", len(cmp.Expected))
	}
}

func TestCompareMultiDeviceDivergenceDeterminism(t *testing.T) {
	cfgA, macH1, _ := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	specA := constructionSpec(statedPhysical(cfgA))
	specB := constructionSpec(statedPhysical(cfgB))

	// In specB, both sw1 and sw2 differ from specA by having an extra static seed.
	sw1B := specB.Switches["sw1"]
	sw1B.Seeds = []bridge.Seed{{
		MAC:      netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77},
		FID:      10,
		Port:     "1/1/1",
		Lifetime: bridge.Static,
	}}
	specB.Switches["sw1"] = sw1B

	sw2B := specB.Switches["sw2"]
	sw2B.Seeds = []bridge.Seed{{
		MAC:      netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x88},
		FID:      10,
		Port:     "1/1/1",
		Lifetime: bridge.Static,
	}}
	specB.Switches["sw2"] = sw2B

	fabA, err := fabric.NewWithSpec(specA)
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.NewWithSpec(specB)
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	scenario := []fabric.Injection{
		{
			At:     time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH1,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test"),
			},
		},
	}

	// Because both sw1 and sw2 differ in FDB entries, sorted order must deterministically report sw1 first.
	// Without sorting, Go map iteration order flakes between sw1 and sw2 across runs.
	observed := make(map[string]bool)
	for i := 0; i < 50; i++ {
		cmp := fabric.Compare(fabA, fabB, scenario, 10)
		if cmp.Err != nil {
			t.Fatalf("Compare: %v", cmp.Err)
		}
		if cmp.Disposition != analysis.Different {
			t.Fatalf("Disposition = %v, want Different", cmp.Disposition)
		}
		firstDev := strings.Split(cmp.Difference.Current, ":")[0]
		observed[firstDev] = true
		if firstDev != "sw1" {
			t.Fatalf("run %d reported first differing device %q, want stable sorted \"sw1\"", i, firstDev)
		}
	}
}

func TestCompareReplayReproducesBothCurrentAndCandidate(t *testing.T) {
	cfgA, macH1, macH2 := makeTwoSwitchConfigs(t)
	cfgB, _, _ := makeTwoSwitchConfigs(t)

	// Cut cable to h2 in cfgB so frame drops instead of delivering.
	for i := range cfgB.Cables {
		if cfgB.Cables[i].A.Node == "h2" || cfgB.Cables[i].B.Node == "h2" {
			cfgB.Cables[i].Fault = fabric.Fault{Kind: fabric.FaultCut}
		}
	}
	sw2Ports := cfgB.Switches["sw2"].Ports.Ports()
	b := port.NewBuilder()
	for _, p := range sw2Ports {
		if p.Name == "1/1/1" {
			p.OperStatus = port.Down
		}
		b.Add(p)
	}
	sw2Cfg := cfgB.Switches["sw2"]
	sw2Cfg.Ports, _ = b.Build()
	cfgB.Switches["sw2"] = sw2Cfg

	fabA, err := fabric.New(statedPhysical(cfgA))
	if err != nil {
		t.Fatalf("New fabA: %v", err)
	}
	fabB, err := fabric.New(statedPhysical(cfgB))
	if err != nil {
		t.Fatalf("New fabB: %v", err)
	}

	scenario := []fabric.Injection{
		{
			At:     time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test replay"),
			},
		},
	}

	cmp := fabric.Compare(fabA, fabB, scenario, 10)
	if cmp.Err != nil {
		t.Fatalf("Compare: %v", cmp.Err)
	}
	if cmp.Disposition != analysis.Different {
		t.Fatalf("Disposition = %v, want Different", cmp.Disposition)
	}

	resA, err := fabric.Replay(cmp.Replay[0])
	if err != nil {
		t.Fatalf("Replay current: %v", err)
	}
	resB, err := fabric.Replay(cmp.Replay[1])
	if err != nil {
		t.Fatalf("Replay candidate: %v", err)
	}

	if resA.Status != analysis.Complete {
		t.Errorf("Replay current Status = %v, want Complete", resA.Status)
	}
	if resB.Status != analysis.Complete {
		t.Errorf("Replay candidate Status = %v, want Complete", resB.Status)
	}

	repFabA, err := fabric.NewWithSpec(cmp.Replay[0].Spec)
	if err != nil {
		t.Fatalf("NewWithSpec current: %v", err)
	}
	if _, err := repFabA.RunScenario(cmp.Replay[0].Scenario); err != nil {
		t.Fatalf("RunScenario current: %v", err)
	}
	jA := repFabA.Report()
	if len(jA) != 1 || jA[0].State != fabric.JourneyDelivered {
		t.Errorf("Replayed current journeys = %v, want 1 delivered journey", jA)
	}

	repFabB, err := fabric.NewWithSpec(cmp.Replay[1].Spec)
	if err != nil {
		t.Fatalf("NewWithSpec candidate: %v", err)
	}
	if _, err := repFabB.RunScenario(cmp.Replay[1].Scenario); err != nil {
		t.Fatalf("RunScenario candidate: %v", err)
	}
	jB := repFabB.Report()
	if len(jB) != 1 || jB[0].State != fabric.JourneyDropped {
		t.Errorf("Replayed candidate journeys = %v, want 1 dropped journey", jB)
	}
}
