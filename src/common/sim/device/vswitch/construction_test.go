package vswitch_test

import (
	"net/netip"
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestNewRejectsZeroStaticNeighborMACAtRoutingField(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "routed", AdminStatus: port.Up, OperStatus: port.Up}))
	cfg := vswitch.Config{
		Ports: ports,
		Routing: &routing.Config{VRFs: map[string]routing.VRF{
			routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"routed": {Port: "routed", Prefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}},
				},
				Neighbors: []routing.Neighbor{{Interface: "routed", Addr: netip.MustParseAddr("192.0.2.2")}},
			},
		}},
	}

	_, err := vswitch.New(cfg)
	if err == nil {
		t.Fatal("vswitch.New accepted a zero static neighbor MAC")
	}
	if got, want := errs.Attributes(err)["field"], "vrfs.default.neighbors.routed/192.0.2.2.mac"; got != want {
		t.Errorf("field = %v, want %q", got, want)
	}
}

func TestCapabilitiesFollowConfiguration(t *testing.T) {
	portsOnly := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	t.Run("ports only", func(t *testing.T) {
		cfg := vswitch.Config{Ports: portsOnly}
		if caps := cfg.Capabilities(); len(caps) != 0 {
			t.Errorf("got capabilities %v, want empty set", caps)
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("got validation error %v, want nil", err)
		}
		sw := mustSwitch(t, cfg)
		if sw.Entries() != nil {
			t.Errorf("got entries %v on hub, want nil", sw.Entries())
		}
	})

	t.Run("relay only", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports:  portsOnly,
			Bridge: &bridge.Config{},
		}
		want := []trace.Layer{bridge.LayerName}
		if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
			t.Errorf("got capabilities %v, want %v", caps, want)
		}
	})

	t.Run("all capabilities", func(t *testing.T) {
		tbl := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
			Add(port.Port{Name: "lag1", Kind: port.LAG}))

		cfg := vswitch.Config{
			Ports:   tbl,
			Traffic: &traffic.Config{},
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{10}},
					},
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
					Ports: map[string]phy.PsePort{
						"1/1/1": {Group: "g1", Enabled: true, MaxClass: 4},
					},
				},
			},
		}

		want := []trace.Layer{
			phy.LayerName,
			lag.LayerName,
			phy.LayerNamePoE,
			bridge.LayerName,
			traffic.LayerName,
			bridge.LayerNameVLAN,
		}
		if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
			t.Errorf("got capabilities %v, want %v", caps, want)
		}
	})
}

func TestCapabilitiesSTP(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	want := []trace.Layer{bridge.LayerName, stp.LayerName}
	if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
		t.Errorf("got capabilities %v, want %v", caps, want)
	}
}

func TestValidateRefusesSTPWithoutBridge(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	cfg := vswitch.Config{
		Ports: tbl,
		STP: &stp.Config{
			Priority: 32768,
			Address:  netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() = nil, want error when STP is configured without bridge")
	}
}

func TestCapabilitiesWithRouting(t *testing.T) {
	portsOnly := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	t.Run("relay routing vlan", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: portsOnly,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{10}},
					},
				},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		want := []trace.Layer{bridge.LayerName, routing.LayerName, bridge.LayerNameVLAN}
		if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
			t.Errorf("got capabilities %v, want %v", caps, want)
		}
	})

	t.Run("routing alone for router", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: portsOnly,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
							"1/1/2": {Port: "1/1/2", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						},
					},
				},
			},
		}
		want := []trace.Layer{routing.LayerName}
		if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
			t.Errorf("got capabilities %v, want %v", caps, want)
		}
	})
}

func TestCrossLayerValidation(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	t.Run("routed VLAN interface requires bridge VLAN", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for missing bridge VLAN")
		}
		attrs := errs.Attributes(err)
		if attrs["vrf"] != "default" || attrs["interface"] != "vlan10" || attrs["vlan"] != vlan.ID(10) {
			t.Errorf("got attrs %v, want vrf=default, interface=vlan10, vlan=10", attrs)
		}
	})

	t.Run("routed port refused on a relay without VLAN", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/2": {Port: "1/1/2", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for a routed port on a relay that cannot filter it")
		}
		if attrs := errs.Attributes(err); attrs["port"] != "1/1/2" {
			t.Errorf("got attrs %v, want port=1/1/2", attrs)
		}
	})

	t.Run("routed VLAN interface requires VLAN in table", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{20: "vlan20"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{20}},
					},
				},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for VLAN missing from table")
		}
		attrs := errs.Attributes(err)
		if attrs["vrf"] != "default" || attrs["interface"] != "vlan10" || attrs["vlan"] != vlan.ID(10) {
			t.Errorf("got attrs %v, want vrf=default, interface=vlan10, vlan=10", attrs)
		}
	})

	t.Run("routed port cannot appear in switchports", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{10}},
					},
				},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for routed port in switchports")
		}
		attrs := errs.Attributes(err)
		if attrs["vrf"] != "default" || attrs["interface"] != "1/1/1" || attrs["port"] != "1/1/1" {
			t.Errorf("got attrs %v, want vrf=default, interface=1/1/1, port=1/1/1", attrs)
		}
	})

	t.Run("routed port cannot appear in STP ports", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/2": {Untagged: []vlan.ID{10}},
					},
				},
			},
			STP: &stp.Config{
				Priority: 32768,
				Ports:    map[string]stp.Port{"1/1/1": {Priority: 128}},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for routed port in STP ports")
		}
		attrs := errs.Attributes(err)
		if attrs["vrf"] != "default" || attrs["interface"] != "1/1/1" || attrs["port"] != "1/1/1" {
			t.Errorf("got attrs %v, want vrf=default, interface=1/1/1, port=1/1/1", attrs)
		}
	})

	t.Run("router without bridge must route every non-LAG port", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for unrouted port on router")
		}
		attrs := errs.Attributes(err)
		if attrs["port"] != "1/1/2" {
			t.Errorf("got attrs %v, want port=1/1/2", attrs)
		}
	})

	t.Run("router without bridge routes all non-LAG ports successfully", func(t *testing.T) {
		lagPorts := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1"}).
			Add(port.Port{Name: "lag1", Kind: port.LAG}))

		cfg := vswitch.Config{
			Ports: lagPorts,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
							"lag1":  {Port: "lag1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						},
					},
				},
			},
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate failed: %v", err)
		}
	})
}

func TestBaseMACAssignment(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	t.Run("all zero MACs assigned base address", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{10}},
					},
				},
			},
			STP: &stp.Config{
				Priority: 32768,
				Ports:    map[string]stp.Port{"1/1/1": {Priority: 128}},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		sw := mustSwitch(t, cfg)
		wantMAC := netaddr.Local(1)
		if sw.Config().MAC != wantMAC {
			t.Errorf("sw.Config().MAC = %v, want %v", sw.Config().MAC, wantMAC)
		}
		if iface := sw.Config().Routing.VRFs["default"].Interfaces["vlan10"]; iface.MAC != wantMAC {
			t.Errorf("routed interface MAC = %v, want %v", iface.MAC, wantMAC)
		}
		if sw.Config().STP.Address != wantMAC {
			t.Errorf("STP config address = %v, want %v", sw.Config().STP.Address, wantMAC)
		}
		if sw.BridgeID().Address != wantMAC {
			t.Errorf("sw.BridgeID().Address = %v, want %v", sw.BridgeID().Address, wantMAC)
		}
	})

	t.Run("explicit interface MAC preserved beside assigned base", func(t *testing.T) {
		explicitMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
		cfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{10}},
						"1/1/2": {Untagged: []vlan.ID{20}},
					},
				},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, MAC: explicitMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
							"vlan20": {VLAN: 20, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						},
					},
				},
			},
		}
		sw := mustSwitch(t, cfg)
		wantBase := netaddr.Local(1)
		if sw.Config().MAC != wantBase {
			t.Errorf("sw.Config().MAC = %v, want %v", sw.Config().MAC, wantBase)
		}
		if iface10 := sw.Config().Routing.VRFs["default"].Interfaces["vlan10"]; iface10.MAC != explicitMAC {
			t.Errorf("vlan10 MAC = %v, want %v", iface10.MAC, explicitMAC)
		}
		if iface20 := sw.Config().Routing.VRFs["default"].Interfaces["vlan20"]; iface20.MAC != wantBase {
			t.Errorf("vlan20 MAC = %v, want %v", iface20.MAC, wantBase)
		}
	})

	t.Run("derived switch keeps base MAC", func(t *testing.T) {
		cfg := vswitch.Config{
			MAC:   netaddr.Local(42),
			Ports: ports,
		}
		cur := mustSwitch(t, cfg)
		nextCfg := vswitch.Config{
			Ports: ports,
		}
		target := cur.Spec()
		target.Config = nextCfg
		target.Config.MAC = cur.Config().MAC
		nextSw, err := vswitch.Derive(cur, target)
		if err != nil {
			t.Fatalf("Derive failed: %v", err)
		}
		if nextSw.Config().MAC != netaddr.Local(42) {
			t.Errorf("nextSw.Config().MAC = %v, want %v", nextSw.Config().MAC, netaddr.Local(42))
		}
	})
}
