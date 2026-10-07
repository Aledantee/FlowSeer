package vswitch_test

import (
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestConfigEqualAndDiffNormalizeCompleteConfiguration(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.LAG}).
		Add(port.Port{Name: "member", Kind: port.Physical, LagParent: "lag1"}))
	raw := vswitch.Config{Ports: ports}
	explicit := raw.Normalize()

	if !raw.Equal(explicit) {
		t.Fatal("raw configuration and its normalized form compare unequal")
	}
	if got := vswitch.Diff(raw, explicit); len(got) != 0 {
		t.Errorf("Diff(raw, raw.Normalize()) = %+v, want none", got)
	}
}

func TestDiffOfAHubAndAHubIsEmpty(t *testing.T) {
	b := port.NewBuilder()
	b.Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical})
	cfg := vswitch.Config{Ports: mustTable(t, b)}
	if changes := vswitch.Diff(cfg, cfg); len(changes) != 0 {
		t.Errorf("Diff = %+v, want none", changes)
	}
}

func TestDiffFieldChangesAcrossLayers(t *testing.T) {
	curCfg, expCfg, _, _ := makeComparePair(t)

	t.Run("port field changes across layers", func(t *testing.T) {
		changes := vswitch.Diff(curCfg, expCfg)
		if len(changes) != 2 {
			t.Fatalf("got %d changes, want 2: %v", len(changes), changes)
		}

		pvidChange := changes[0]
		if pvidChange.Subject.Key != "1/1/2" || pvidChange.Field != "pvid" ||
			trace.CompareFact(pvidChange.From, bridge.PVIDFact(10)) != 0 || trace.CompareFact(pvidChange.To, bridge.PVIDFact(20)) != 0 {
			t.Errorf("unexpected pvid change: %+v", pvidChange)
		}

		untaggedChange := changes[1]
		if untaggedChange.Subject.Key != "1/1/2" || untaggedChange.Field != "untagged_vlan_ids" ||
			trace.CompareFact(untaggedChange.From, bridge.VLANsFact([]vlan.ID{10})) != 0 ||
			trace.CompareFact(untaggedChange.To, bridge.VLANsFact([]vlan.ID{20})) != 0 {
			t.Errorf("unexpected untagged_vlan_ids change: %+v", untaggedChange)
		}
	})

	t.Run("added port", func(t *testing.T) {
		morePorts := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

		addedPortCfg := curCfg
		addedPortCfg.Ports = morePorts

		changes := vswitch.Diff(curCfg, addedPortCfg)
		if len(changes) != 1 {
			t.Fatalf("got %d changes, want 1", len(changes))
		}
		ch := changes[0]
		if ch.Subject.Kind != "port" || ch.Subject.Key != "1/1/4" || ch.Field != "" || ch.From != nil || ch.To == nil {
			t.Errorf("unexpected port addition change: %+v", ch)
		}
	})

	t.Run("pse group budget change", func(t *testing.T) {
		cfgA := vswitch.Config{
			Phy: &phy.Config{
				PoE: &phy.PoE{
					Groups: map[string]phy.Group{
						"g1": {PowerNanowatts: 60_000_000_000},
					},
				},
			},
		}
		cfgB := vswitch.Config{
			Phy: &phy.Config{
				PoE: &phy.PoE{
					Groups: map[string]phy.Group{
						"g1": {PowerNanowatts: 90_000_000_000},
					},
				},
			},
		}

		changes := vswitch.Diff(cfgA, cfgB)
		if len(changes) != 1 {
			t.Fatalf("got %d changes, want 1", len(changes))
		}
		ch := changes[0]
		if ch.Subject.Kind != "pse_group" || ch.Subject.Key != "g1" || ch.Field != "power_nanowatts" ||
			ch.From.TypeID() != "phy.power_nw" || ch.From.Canonical() != "60000000000" ||
			ch.To.TypeID() != "phy.power_nw" || ch.To.Canonical() != "90000000000" {
			t.Errorf("unexpected pse_group change: %+v", ch)
		}
	})

	t.Run("bridge gains VLAN capability", func(t *testing.T) {
		cfgA := vswitch.Config{
			Bridge: &bridge.Config{},
		}
		cfgB := vswitch.Config{
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
				},
			},
		}

		changes := vswitch.Diff(cfgA, cfgB)
		foundCap := false
		for _, ch := range changes {
			if ch.Subject.Kind == "capability" && ch.Subject.Key == "vlan" {
				foundCap = true
				if ch.From != nil || ch.To.TypeID() != "vswitch.layer" || ch.To.Canonical() != string(bridge.LayerNameVLAN) {
					t.Errorf("unexpected capability change payload: %+v", ch)
				}
			}
		}
		if !foundCap {
			t.Errorf("got changes %+v, want capability change for vlan", changes)
		}
	})

	t.Run("traffic mirror changes", func(t *testing.T) {
		cfgA := vswitch.Config{Traffic: &traffic.Config{Mirrors: []traffic.Mirror{{
			Name:       "m1",
			SelectAll:  true,
			OutputPort: "1/1/4",
			SnapLen:    64,
		}}}}
		cfgB := cfgA.Clone()
		cfgB.Traffic.Mirrors[0].SnapLen = 128

		changes := vswitch.Diff(cfgA, cfgB)
		if len(changes) != 1 {
			t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
		}
		ch := changes[0]
		if ch.Layer != traffic.LayerName || ch.Subject.Kind != "mirror" || ch.Subject.Key != "m1" ||
			ch.Field != "snap_len" ||
			ch.From.TypeID() != "traffic.snap_len" || ch.From.Canonical() != "64" ||
			ch.To.TypeID() != "traffic.snap_len" || ch.To.Canonical() != "128" {
			t.Errorf("unexpected mirror change: %+v", ch)
		}
	})
}

func TestDiffEqualConfigsEmpty(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	pvid10 := vlan.ID(10)

	cases := []struct {
		name string
		cfg  vswitch.Config
	}{
		{
			name: "ports only",
			cfg:  vswitch.Config{Ports: tbl},
		},
		{
			name: "bridge without vlan",
			cfg: vswitch.Config{
				Ports:  tbl,
				Bridge: &bridge.Config{AgingTime: 300 * time.Second},
			},
		},
		{
			name: "bridge with vlan",
			cfg: vswitch.Config{
				Ports: tbl,
				Bridge: &bridge.Config{
					AgingTime: 300 * time.Second,
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "vlan10"},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {PVID: &pvid10, Untagged: []vlan.ID{10}},
						},
					},
				},
			},
		},
		{
			name: "full config",
			cfg: vswitch.Config{
				Ports: tbl,
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "vlan10"},
					},
				},
				Phy: &phy.Config{
					Ethernet: map[string]phy.Ethernet{
						"1/1/1": {SupportedSpeedsBPS: []uint64{1_000_000_000}},
					},
					PoE: &phy.PoE{
						Groups: map[string]phy.Group{
							"g1": {PowerNanowatts: 60_000_000_000},
						},
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changes := vswitch.Diff(tc.cfg, tc.cfg)
			if len(changes) != 0 {
				t.Errorf("got %d changes for equal configs, want 0: %v", len(changes), changes)
			}
		})
	}
}

func TestDiffSTPPriorityChange(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	mac := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	cfgA := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  mac,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}
	cfgB := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 4096,
			Address:  mac,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	changes := vswitch.Diff(cfgA, cfgB)
	found := false
	for _, ch := range changes {
		if ch.Layer == stp.LayerName && ch.Subject.Kind == "bridge" && ch.Field == "priority" {
			found = true
			if ch.From.TypeID() != "stp.priority" || ch.From.Canonical() != "32768" ||
				ch.To.TypeID() != "stp.priority" || ch.To.Canonical() != "4096" {
				t.Errorf("priority change = %+v, want 32768 -> 4096", ch)
			}
		}
	}
	if !found {
		t.Fatalf("Diff() did not find stp priority change: %+v", changes)
	}
}

// TestDiffLoopProtectCapabilityAndFieldChange proves that vswitch.Diff
// reports loop protection's capability presence change and, for two
// configurations differing by exactly one loop-protection field, exactly
// one loop-protection change.
func TestDiffLoopProtectCapabilityAndFieldChange(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}))

	base := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
	}
	withLoopProtect := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block},
			},
		},
	}

	capChanges := vswitch.Diff(base, withLoopProtect)
	foundCap := false
	for _, ch := range capChanges {
		if ch.Layer == loopprotect.LayerName && ch.Subject.Kind == "capability" {
			foundCap = true
		}
	}
	if !foundCap {
		t.Fatalf("Diff() did not report a loop-protection capability change: %+v", capChanges)
	}

	withNoLearn := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.NoLearn},
			},
		},
	}

	fieldChanges := vswitch.Diff(withLoopProtect, withNoLearn)
	loopProtectChanges := 0
	for _, ch := range fieldChanges {
		if ch.Layer == loopprotect.LayerName && ch.Subject.Kind == "port" {
			loopProtectChanges++
		}
	}
	if loopProtectChanges != 1 {
		t.Errorf("Diff() reported %d loop-protection field changes for one changed field, want 1: %+v", loopProtectChanges, fieldChanges)
	}
}

// TestDiffReportsDeviceMAC is evidence for the device-level mac change.
func TestDiffReportsDeviceMAC(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}))
	cfgA := vswitch.Config{Ports: ports, MAC: netaddr.MAC{0x02, 0, 0, 0, 0, 1}}
	cfgB := vswitch.Config{Ports: ports, MAC: netaddr.MAC{0x02, 0, 0, 0, 0, 2}}

	changes := vswitch.Diff(cfgA, cfgB)
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want one", changes)
	}
	ch := changes[0]
	if ch.Layer != port.LayerName || ch.Subject != (trace.Subject{Kind: "device"}) || ch.Field != "mac" ||
		ch.From.TypeID() != "vswitch.mac" || ch.From.Canonical() != cfgA.MAC.String() ||
		ch.To.TypeID() != "vswitch.mac" || ch.To.Canonical() != cfgB.MAC.String() {
		t.Errorf("change = %+v, want layer port, subject device, field mac, %s to %s", ch, cfgA.MAC, cfgB.MAC)
	}
	if got := vswitch.Diff(cfgA, cfgA); len(got) != 0 {
		t.Errorf("Diff(a, a) = %+v, want none", got)
	}
}

func TestDiffRoutingNeighborChange(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}))

	macA := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	macB := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x88}

	cfgA := vswitch.Config{
		Ports: ports,
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan20": {VLAN: 20, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macA},
					},
				},
			},
		},
	}
	cfgB := cfgA.Clone()
	cfgB.Routing.VRFs["default"] = routing.VRF{
		Interfaces: cfgA.Routing.VRFs["default"].Interfaces,
		Neighbors: []routing.Neighbor{
			{Interface: "vlan20", Addr: ipH2, MAC: macB},
		},
	}

	changes := vswitch.Diff(cfgA, cfgB)
	var foundNeighborChange bool
	for _, c := range changes {
		if c.Layer == routing.LayerName && c.Subject.Kind == "neighbor" && c.Field == "mac" {
			foundNeighborChange = true
			if c.From.TypeID() != "routing.mac" || c.From.Canonical() != macA.String() ||
				c.To.TypeID() != "routing.mac" || c.To.Canonical() != macB.String() {
				t.Errorf("neighbor diff from=%v to=%v, want %v -> %v", c.From, c.To, macA, macB)
			}
		}
	}
	if !foundNeighborChange {
		t.Errorf("Diff changes %+v did not contain neighbor MAC change", changes)
	}
}
