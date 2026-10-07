package bridge_test

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestValidationRules(t *testing.T) {
	builder := port.NewBuilder()
	builder.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	builder.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	builder.Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	tests := []struct {
		name          string
		cfg           bridge.Config
		wantAttrKey   string
		wantAttrValue any
	}{
		{
			name: "switchport naming absent port",
			cfg: bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/99": {Tagged: []vlan.ID{10}},
					},
				},
			},
			wantAttrKey:   "port",
			wantAttrValue: "1/1/99",
		},
		{
			name: "switchport naming LAG member",
			cfg: bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/2": {Tagged: []vlan.ID{10}},
					},
				},
			},
			wantAttrKey:   "member",
			wantAttrValue: "1/1/2",
		},
		{
			name: "VLAN in both tagged and untagged sets",
			cfg: bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {
							Tagged:   []vlan.ID{10},
							Untagged: []vlan.ID{10},
						},
					},
				},
			},
			wantAttrKey:   "vlan",
			wantAttrValue: vlan.ID(10),
		},
		{
			name: "VLAN table ID outside valid range",
			cfg: bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{0: "invalid"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Tagged: []vlan.ID{10}},
					},
				},
			},
			wantAttrKey:   "vlan",
			wantAttrValue: vlan.ID(0),
		},
		{
			name: "PVID outside valid range",
			cfg: bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: mustVLAN(0)},
					},
				},
			},
			wantAttrKey:   "vlan",
			wantAttrValue: vlan.ID(0),
		},
		{
			name: "switchport with no VLAN table",
			cfg: bridge.Config{
				VLAN: &bridge.VLAN{
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Tagged: []vlan.ID{10}},
					},
				},
			},
			wantAttrKey:   "port",
			wantAttrValue: "1/1/1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate(layer.Env{Ports: ports})
			if err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			attrs := errs.Attributes(err)
			val, ok := attrs[tc.wantAttrKey]
			if !ok {
				t.Fatalf("missing attribute %q in error attributes %v", tc.wantAttrKey, attrs)
			}
			if val != tc.wantAttrValue {
				t.Errorf("attribute %q = %v (%T), want %v (%T)", tc.wantAttrKey, val, val, tc.wantAttrValue, tc.wantAttrValue)
			}
		})
	}
}

func TestValidateRejectsVLANReferencesAbsentFromTable(t *testing.T) {
	t.Parallel()

	ports := buildTestPorts(t, 1)
	missing := vlan.ID(20)
	cases := []struct {
		name      string
		cfg       bridge.Config
		wantField string
	}{
		{
			name: "PVID",
			cfg: bridge.Config{VLAN: &bridge.VLAN{
				Table:       map[vlan.ID]string{10: "ten"},
				Switchports: map[string]bridge.Switchport{"1/1/1": {PVID: &missing}},
			}},
			wantField: "vlan.switchports.1/1/1.pvid",
		},
		{
			name: "tagged VLAN",
			cfg: bridge.Config{VLAN: &bridge.VLAN{
				Table:       map[vlan.ID]string{10: "ten"},
				Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{missing}}},
			}},
			wantField: "vlan.switchports.1/1/1.tagged.20",
		},
		{
			name: "untagged VLAN",
			cfg: bridge.Config{VLAN: &bridge.VLAN{
				Table:       map[vlan.ID]string{10: "ten"},
				Switchports: map[string]bridge.Switchport{"1/1/1": {Untagged: []vlan.ID{missing}}},
			}},
			wantField: "vlan.switchports.1/1/1.untagged.20",
		},
		{
			name: "tunnel VLAN",
			cfg: bridge.Config{VLAN: &bridge.VLAN{
				Table:       map[vlan.ID]string{10: "ten"},
				Switchports: map[string]bridge.Switchport{"1/1/1": {Tunnel: &bridge.Tunnel{VID: missing}}},
			}},
			wantField: "vlan.switchports.1/1/1.tunnel.vid",
		},
		{
			name: "flood VLAN",
			cfg: bridge.Config{
				FloodVLANs: []vlan.ID{missing},
				VLAN:       &bridge.VLAN{Table: map[vlan.ID]string{10: "ten"}},
			},
			wantField: "flood_vlans.20",
		},
		{
			name:      "flood VLAN without VLAN awareness",
			cfg:       bridge.Config{FloodVLANs: []vlan.ID{10}},
			wantField: "flood_vlans.10",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate(layer.Env{Ports: ports})
			if err == nil {
				t.Fatal("Validate() = nil, want error")
			}
			if got := errs.Attributes(err)["field"]; got != tc.wantField {
				t.Errorf("field = %v, want %q", got, tc.wantField)
			}
		})
	}
}

func TestNegativeAgingTimeIsInvalid(t *testing.T) {
	t.Parallel()

	cfg := bridge.Config{AgingTime: -time.Second}
	if got := cfg.Normalize(layer.Env{}).AgingTime; got != -time.Second {
		t.Errorf("Normalize().AgingTime = %s, want -1s", got)
	}
	err := cfg.Validate(layer.Env{Ports: buildTestPorts(t, 1)})
	if err == nil {
		t.Fatal("Validate() = nil, want error")
	}
	if got := errs.Attributes(err)["field"]; got != "aging_time" {
		t.Errorf("field = %v, want aging_time", got)
	}
}

func TestValidateRefusesNegativeMaxEntries(t *testing.T) {
	ports := buildTestPorts(t, 2)

	t.Run("with nil VLAN", func(t *testing.T) {
		cfg := bridge.Config{MaxEntries: -1}
		err := cfg.Validate(layer.Env{Ports: ports})
		if err == nil {
			t.Fatal("Validate with nil VLAN and MaxEntries -1 succeeded, want error")
		}
		if attrs := errs.Attributes(err); attrs["max_entries"] != -1 {
			t.Errorf("error attrs = %+v, want max_entries = -1", attrs)
		}
	})

	t.Run("with VLAN table", func(t *testing.T) {
		cfg := bridge.Config{
			MaxEntries: -1,
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "ten"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {Untagged: []vlan.ID{10}},
				},
			},
		}
		err := cfg.Validate(layer.Env{Ports: ports})
		if err == nil {
			t.Fatal("Validate with VLAN table and MaxEntries -1 succeeded, want error")
		}
		if attrs := errs.Attributes(err); attrs["max_entries"] != -1 {
			t.Errorf("error attrs = %+v, want max_entries = -1", attrs)
		}
	})
}

func TestValidateFloodVLANsAndProtectedPorts(t *testing.T) {
	ports := buildTestPorts(t, 4)

	tableVLAN := &bridge.VLAN{
		Table: map[vlan.ID]string{
			10: "ten",
			20: "twenty",
		},
		Switchports: map[string]bridge.Switchport{
			"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			"1/1/3": {Tagged: []vlan.ID{10, 20}},
			"1/1/4": {},
		},
	}

	cases := []struct {
		name string
		vlan *bridge.VLAN
	}{
		{name: "with nil VLAN", vlan: nil},
		{name: "with VLAN table", vlan: tableVLAN},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgFlood := bridge.Config{
				FloodVLANs: []vlan.ID{0},
				VLAN:       tc.vlan,
			}
			if err := cfgFlood.Validate(layer.Env{Ports: ports}); err == nil {
				t.Error("Validate with FloodVLANs {0} succeeded, want error")
			}

			cfgProt := bridge.Config{
				ProtectedPorts: []string{"1/1/9"},
				VLAN:           tc.vlan,
			}
			if err := cfgProt.Validate(layer.Env{Ports: ports}); err == nil {
				t.Error("Validate with ProtectedPorts {1/1/9} succeeded, want error")
			}
		})
	}

	t.Run("protected port naming a LAG member is refused", func(t *testing.T) {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
		tbl, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		cfg := bridge.Config{ProtectedPorts: []string{"1/1/2"}}
		if err := cfg.Validate(layer.Env{Ports: tbl}); err == nil {
			t.Error("Validate with protected port naming LAG member succeeded, want error")
		}
	})
}

func TestValidateTunnelSwitchportAndPriorityTags(t *testing.T) {
	ports := buildTestPorts(t, 4)

	baseVLAN := func(sw bridge.Switchport) *bridge.VLAN {
		return &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": sw,
			},
		}
	}

	t.Run("tunnel with PVID is refused", func(t *testing.T) {
		cfg := bridge.Config{
			VLAN: baseVLAN(bridge.Switchport{
				Tunnel: &bridge.Tunnel{VID: 10},
				PVID:   mustVLAN(10),
			}),
		}
		if err := cfg.Validate(layer.Env{Ports: ports}); err == nil {
			t.Error("Validate with tunnel and PVID succeeded, want error")
		}
	})

	t.Run("tunnel with tagged VLANs is refused", func(t *testing.T) {
		cfg := bridge.Config{
			VLAN: baseVLAN(bridge.Switchport{
				Tunnel: &bridge.Tunnel{VID: 10},
				Tagged: []vlan.ID{10},
			}),
		}
		if err := cfg.Validate(layer.Env{Ports: ports}); err == nil {
			t.Error("Validate with tunnel and Tagged succeeded, want error")
		}
	})

	t.Run("tunnel with untagged VLANs is refused", func(t *testing.T) {
		cfg := bridge.Config{
			VLAN: baseVLAN(bridge.Switchport{
				Tunnel:   &bridge.Tunnel{VID: 10},
				Untagged: []vlan.ID{10},
			}),
		}
		if err := cfg.Validate(layer.Env{Ports: ports}); err == nil {
			t.Error("Validate with tunnel and Untagged succeeded, want error")
		}
	})

	t.Run("tunnel with invalid VID is refused", func(t *testing.T) {
		cfg := bridge.Config{
			VLAN: baseVLAN(bridge.Switchport{
				Tunnel: &bridge.Tunnel{VID: 0},
			}),
		}
		if err := cfg.Validate(layer.Env{Ports: ports}); err == nil {
			t.Error("Validate with tunnel VID 0 succeeded, want error")
		}
	})

	t.Run("tunnel with invalid customer VID is refused", func(t *testing.T) {
		cfg := bridge.Config{
			VLAN: baseVLAN(bridge.Switchport{
				Tunnel: &bridge.Tunnel{VID: 10, CustomerVIDs: []vlan.ID{0}},
			}),
		}
		if err := cfg.Validate(layer.Env{Ports: ports}); err == nil {
			t.Error("Validate with customer VID 0 succeeded, want error")
		}
	})

	t.Run("unknown PriorityTags is refused", func(t *testing.T) {
		cfg := bridge.Config{
			VLAN: baseVLAN(bridge.Switchport{
				PriorityTags: "Sometimes",
			}),
		}
		if err := cfg.Validate(layer.Env{Ports: ports}); err == nil {
			t.Error("Validate with PriorityTags 'Sometimes' succeeded, want error")
		}
	})
}

func TestSwitchportCloneWithTunnel(t *testing.T) {
	orig := bridge.Switchport{
		Tunnel: &bridge.Tunnel{
			VID:          10,
			CustomerVIDs: []vlan.ID{100, 200},
			TPID:         0x88A8,
		},
		PriorityTags: bridge.PriorityTagsAlways,
	}

	cloned := orig.Clone()
	orig.Tunnel.VID = 20
	orig.Tunnel.CustomerVIDs[0] = 300

	if cloned.Tunnel.VID != 10 {
		t.Errorf("cloned.Tunnel.VID = %d, want 10", cloned.Tunnel.VID)
	}
	if cloned.Tunnel.CustomerVIDs[0] != 100 {
		t.Errorf("cloned.Tunnel.CustomerVIDs[0] = %d, want 100", cloned.Tunnel.CustomerVIDs[0])
	}
	if cloned.PriorityTags != bridge.PriorityTagsAlways {
		t.Errorf("cloned.PriorityTags = %s, want %s", cloned.PriorityTags, bridge.PriorityTagsAlways)
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	cfg := bridge.Config{
		FloodVLANs:     []vlan.ID{20, 10, 20},
		ProtectedPorts: []string{"1/1/2", "1/1/1", "1/1/2"},
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {
					Tagged:   []vlan.ID{30, 20, 20},
					Untagged: []vlan.ID{10, 10},
					Tunnel: &bridge.Tunnel{
						VID:          100,
						CustomerVIDs: []vlan.ID{200, 100, 200},
					},
				},
			},
		},
	}

	norm := cfg.Normalize(layer.Env{})
	if norm.AgingTime != bridge.DefaultAgingTime {
		t.Errorf("AgingTime: got %v, want %v", norm.AgingTime, bridge.DefaultAgingTime)
	}
	wantFlood := []vlan.ID{10, 20}
	if !slices.Equal(norm.FloodVLANs, wantFlood) {
		t.Errorf("FloodVLANs: got %v, want %v", norm.FloodVLANs, wantFlood)
	}
	wantProt := []string{"1/1/1", "1/1/2"}
	if !slices.Equal(norm.ProtectedPorts, wantProt) {
		t.Errorf("ProtectedPorts: got %v, want %v", norm.ProtectedPorts, wantProt)
	}

	sw := norm.VLAN.Switchports["1/1/1"]
	if sw.Admission != bridge.All {
		t.Errorf("Admission: got %v, want %v", sw.Admission, bridge.All)
	}
	if sw.PriorityTags != bridge.PriorityTagsNever {
		t.Errorf("PriorityTags: got %v, want %v", sw.PriorityTags, bridge.PriorityTagsNever)
	}
	wantTagged := []vlan.ID{20, 30}
	if !slices.Equal(sw.Tagged, wantTagged) {
		t.Errorf("Tagged: got %v, want %v", sw.Tagged, wantTagged)
	}
	wantUntagged := []vlan.ID{10}
	if !slices.Equal(sw.Untagged, wantUntagged) {
		t.Errorf("Untagged: got %v, want %v", sw.Untagged, wantUntagged)
	}
	if sw.Tunnel.TPID != 0x88A8 {
		t.Errorf("Tunnel TPID: got 0x%04x, want 0x%04x", sw.Tunnel.TPID, 0x88A8)
	}
	wantCust := []vlan.ID{100, 200}
	if !slices.Equal(sw.Tunnel.CustomerVIDs, wantCust) {
		t.Errorf("CustomerVIDs: got %v, want %v", sw.Tunnel.CustomerVIDs, wantCust)
	}
}

func TestValidateEnumDomains(t *testing.T) {
	t.Parallel()

	builder := port.NewBuilder()
	builder.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	cfg := bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {Admission: "InvalidAdmission"},
			},
		},
	}
	if err := cfg.Validate(layer.Env{Ports: ports}); err == nil {
		t.Fatal("Validate() succeeded for invalid Admission, want error")
	}
}

func TestValidateVLANTableUsesAscendingOrder(t *testing.T) {
	cfg := bridge.Config{VLAN: &bridge.VLAN{Table: map[vlan.ID]string{0: "zero", 5000: "too-high"}}}
	for i := range 100 {
		err := cfg.Validate(layer.Env{})
		if err == nil {
			t.Fatal("Validate accepted invalid VLAN table")
		}
		if got := errs.Attributes(err)["field"]; got != "vlan.table.0" {
			t.Fatalf("Validate call %d field = %v, want vlan.table.0", i, got)
		}
	}
}
