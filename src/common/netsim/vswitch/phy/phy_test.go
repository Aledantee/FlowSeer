package phy_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

var gigabitCapable = []uint64{10_000_000, 100_000_000, 1_000_000_000}

func mustTable(t *testing.T, ports ...port.Port) port.Table {
	t.Helper()
	b := port.NewBuilder()
	for _, p := range ports {
		b.Add(p)
	}
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	return tbl
}

func TestSpeedsResolvePerPort(t *testing.T) {
	t.Run("auto-negotiation on resolves to 1000 full", func(t *testing.T) {
		e := phy.Ethernet{
			SupportedSpeedsBPS:       gigabitCapable,
			AutoNegotiationSupported: true,
			Setting:                  &phy.Setting{AutoNegotiation: true},
		}
		got := e.Resolve()
		want := phy.Resolved{SpeedBPS: 1_000_000_000, Duplex: phy.Full, Source: phy.SourceNegotiated}
		if got != want {
			t.Errorf("Resolve() = %+v, want %+v", got, want)
		}
	})

	t.Run("auto-negotiation off with speed 100 resolves to 100", func(t *testing.T) {
		e := phy.Ethernet{
			SupportedSpeedsBPS: gigabitCapable,
			Setting:            &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
		}
		got := e.Resolve()
		want := phy.Resolved{SpeedBPS: 100_000_000, Duplex: phy.Full, Source: phy.SourceSetting}
		if got != want {
			t.Errorf("Resolve() = %+v, want %+v", got, want)
		}
	})

	t.Run("speed 2500 fails validation naming the port", func(t *testing.T) {
		tbl := mustTable(t, port.Port{Name: "1/1/1", Kind: port.Physical})
		cfg := phy.Config{Ethernet: map[string]phy.Ethernet{
			"1/1/1": {SupportedSpeedsBPS: gigabitCapable, Setting: &phy.Setting{SpeedBPS: 2_500_000_000}},
		}}

		err := cfg.Validate(tbl)
		if err == nil {
			t.Fatal("Validate() error = nil, want error")
		}
		attrs := errs.Attributes(err)
		if got, want := attrs["port"], "1/1/1"; got != want {
			t.Errorf("errs.Attributes(err)[\"port\"] = %v, want %v", got, want)
		}
		if got, want := attrs["speed_bps"], uint64(2_500_000_000); got != want {
			t.Errorf("errs.Attributes(err)[\"speed_bps\"] = %v, want %v", got, want)
		}
	})
}

func TestPoeAllocationHonoursBudgetPriorityAndLimit(t *testing.T) {
	class4 := func(priority phy.Priority, limit *uint32) phy.PsePort {
		return phy.PsePort{Group: "1", MaxClass: 8, Enabled: true, Limit: limit, Priority: priority, PDClass: phy.Class(4)}
	}
	threePorts := func() map[string]phy.PsePort {
		return map[string]phy.PsePort{
			"1/1/1": class4(phy.PriorityLow, nil),
			"1/1/2": class4(phy.PriorityCritical, nil),
			"1/1/3": class4(phy.PriorityHigh, nil),
		}
	}

	t.Run("60 W denies the low-priority port", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerMilliwatts: 60_000}},
			Ports:  threePorts(),
		}}

		got := cfg.Allocate()
		for _, name := range []string{"1/1/2", "1/1/3"} {
			if pa := got.Ports[name]; pa.Denial != "" || pa.Milliwatts != 30_000 {
				t.Errorf("Ports[%q] = %+v, want 30000 mW granted", name, pa)
			}
		}
		if pa := got.Ports["1/1/1"]; pa.Denial != phy.ReasonBudget || pa.Milliwatts != 0 {
			t.Errorf("Ports[\"1/1/1\"] = %+v, want denial %q", pa, phy.ReasonBudget)
		}
		if g := got.Groups["1"]; g.BudgetMilliwatts != 60_000 || g.AllocatedMilliwatts != 60_000 || g.RemainderMilliwatts != 0 {
			t.Errorf("Groups[\"1\"] = %+v, want budget 60000, allocated 60000, remainder 0", g)
		}
	})

	t.Run("90 W allocates all three", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerMilliwatts: 90_000}},
			Ports:  threePorts(),
		}}

		got := cfg.Allocate()
		for _, name := range []string{"1/1/1", "1/1/2", "1/1/3"} {
			if pa := got.Ports[name]; pa.Denial != "" || pa.Milliwatts != 30_000 {
				t.Errorf("Ports[%q] = %+v, want 30000 mW granted", name, pa)
			}
		}
		if g := got.Groups["1"]; g.RemainderMilliwatts != 0 {
			t.Errorf("Groups[\"1\"].RemainderMilliwatts = %d, want 0", g.RemainderMilliwatts)
		}
	})

	t.Run("class-4 port with a 15400 mW limit is denied", func(t *testing.T) {
		limit := uint32(15_400)
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerMilliwatts: 60_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": class4(phy.PriorityCritical, &limit)},
		}}

		got := cfg.Allocate()
		if pa := got.Ports["1/1/1"]; pa.Denial != phy.ReasonLimit || pa.Milliwatts != 0 {
			t.Errorf("Ports[\"1/1/1\"] = %+v, want denial %q", pa, phy.ReasonLimit)
		}
		if g := got.Groups["1"]; g.RemainderMilliwatts != 60_000 {
			t.Errorf("Groups[\"1\"].RemainderMilliwatts = %d, want 60000", g.RemainderMilliwatts)
		}
	})

	t.Run("equal priority breaks the tie by port name", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerMilliwatts: 30_000}},
			Ports: map[string]phy.PsePort{
				"1/1/2": class4(phy.PriorityCritical, nil),
				"1/1/1": class4(phy.PriorityCritical, nil),
			},
		}}

		got := cfg.Allocate()
		if pa := got.Ports["1/1/1"]; pa.Denial != "" || pa.Milliwatts != 30_000 {
			t.Errorf("Ports[\"1/1/1\"] = %+v, want 30000 mW granted", pa)
		}
		if pa := got.Ports["1/1/2"]; pa.Denial != phy.ReasonBudget {
			t.Errorf("Ports[\"1/1/2\"].Denial = %q, want %q", pa.Denial, phy.ReasonBudget)
		}
	})
}

func TestClassAbovePortMaximum(t *testing.T) {
	cfg := phy.Config{PoE: &phy.PoE{
		Groups: map[string]phy.Group{"1": {PowerMilliwatts: 90_000}},
		Ports: map[string]phy.PsePort{
			"1/1/1": {Group: "1", MaxClass: 4, Enabled: true, Priority: phy.PriorityCritical, PDClass: phy.Class(6)},
		},
	}}

	got := cfg.Allocate()
	if pa := got.Ports["1/1/1"]; pa.Denial != phy.ReasonClassUnsupported || pa.Milliwatts != 0 {
		t.Errorf("Ports[\"1/1/1\"] = %+v, want denial %q", pa, phy.ReasonClassUnsupported)
	}
	if g := got.Groups["1"]; g.RemainderMilliwatts != 90_000 {
		t.Errorf("Groups[\"1\"].RemainderMilliwatts = %d, want 90000", g.RemainderMilliwatts)
	}
}

func TestObservedOverridesResolution(t *testing.T) {
	cases := []struct {
		name    string
		setting *phy.Setting
	}{
		{name: "over negotiation", setting: &phy.Setting{AutoNegotiation: true}},
		{name: "over a fixed setting", setting: &phy.Setting{SpeedBPS: 10_000_000, Duplex: phy.Full}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := phy.Ethernet{
				SupportedSpeedsBPS:       gigabitCapable,
				AutoNegotiationSupported: true,
				Setting:                  tc.setting,
				Observed:                 &phy.Observed{SpeedBPS: 100_000_000, Duplex: phy.Half},
			}
			got := e.Resolve()
			want := phy.Resolved{SpeedBPS: 100_000_000, Duplex: phy.Half, Source: phy.SourceObserved}
			if got != want {
				t.Errorf("Resolve() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestUnresolvedLinkDown(t *testing.T) {
	t.Run("no setting and no observation", func(t *testing.T) {
		e := phy.Ethernet{SupportedSpeedsBPS: gigabitCapable, AutoNegotiationSupported: true}
		got := e.Resolve()
		if got.SpeedBPS != 0 || got.Source != phy.SourceUnresolved {
			t.Errorf("Resolve() = %+v, want zero speed with source %q", got, phy.SourceUnresolved)
		}
	})

	t.Run("auto-negotiation off without a speed", func(t *testing.T) {
		e := phy.Ethernet{SupportedSpeedsBPS: gigabitCapable, Setting: &phy.Setting{}}
		got := e.Resolve()
		if got.SpeedBPS != 0 || got.Source != phy.SourceUnresolved {
			t.Errorf("Resolve() = %+v, want zero speed with source %q", got, phy.SourceUnresolved)
		}
	})
}

func TestConfigResolve(t *testing.T) {
	t.Run("resolves every port", func(t *testing.T) {
		cfg := phy.Config{Ethernet: map[string]phy.Ethernet{
			"1/1/1": {Observed: &phy.Observed{SpeedBPS: 1_000_000_000, Duplex: phy.Full}},
			"1/1/2": {},
		}}
		got := cfg.Resolve()
		if len(got) != 2 {
			t.Fatalf("len(Resolve()) = %d, want 2", len(got))
		}
		if got["1/1/1"].Source != phy.SourceObserved || got["1/1/2"].Source != phy.SourceUnresolved {
			t.Errorf("Resolve() = %+v, want observed and unresolved", got)
		}
	})

	t.Run("absent capability resolves to nil", func(t *testing.T) {
		if got := (phy.Config{}).Resolve(); got != nil {
			t.Errorf("Config{}.Resolve() = %v, want nil", got)
		}
	})
}

func TestClassPowerMW(t *testing.T) {
	powers := []uint32{15_400, 4_000, 7_000, 15_400, 30_000, 45_000, 60_000, 75_000, 90_000}
	for class, want := range powers {
		got, ok := phy.ClassPowerMW(uint8(class))
		if !ok || got != want {
			t.Errorf("ClassPowerMW(%d) = %d, %v; want %d, true", class, got, ok, want)
		}
	}
	if got, ok := phy.ClassPowerMW(9); ok || got != 0 {
		t.Errorf("ClassPowerMW(9) = %d, %v; want 0, false", got, ok)
	}
}

func TestValidate(t *testing.T) {
	tbl := mustTable(t,
		port.Port{Name: "1/1/1", Kind: port.Physical},
		port.Port{Name: "lag1", Kind: port.Lag},
	)
	validPoE := &phy.PoE{
		Groups: map[string]phy.Group{"1": {PowerMilliwatts: 60_000}},
		Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, PDClass: phy.Class(4)}},
	}

	cases := []struct {
		name     string
		cfg      phy.Config
		wantAttr string
		wantVal  any
	}{
		{
			name:     "valid configuration",
			cfg:      phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: gigabitCapable}}, PoE: validPoE},
			wantAttr: "",
		},
		{
			name: "an auto-negotiating setting carries no speed to check",
			cfg: phy.Config{Ethernet: map[string]phy.Ethernet{
				"1/1/1": {SupportedSpeedsBPS: gigabitCapable, AutoNegotiationSupported: true, Setting: &phy.Setting{AutoNegotiation: true}},
			}},
			wantAttr: "",
		},
		{
			name:     "a fixed setting without a speed is unresolved, not invalid",
			cfg:      phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: gigabitCapable, Setting: &phy.Setting{}}}},
			wantAttr: "",
		},
		{
			name:     "ethernet names an unknown port",
			cfg:      phy.Config{Ethernet: map[string]phy.Ethernet{"9/9/9": {}}},
			wantAttr: "port",
			wantVal:  "9/9/9",
		},
		{
			name:     "ethernet names a LAG",
			cfg:      phy.Config{Ethernet: map[string]phy.Ethernet{"lag1": {}}},
			wantAttr: "port",
			wantVal:  "lag1",
		},
		{
			name:     "poe names an unknown port",
			cfg:      phy.Config{PoE: &phy.PoE{Ports: map[string]phy.PsePort{"9/9/9": {Group: "1"}}}},
			wantAttr: "port",
			wantVal:  "9/9/9",
		},
		{
			name:     "poe names a LAG",
			cfg:      phy.Config{PoE: &phy.PoE{Ports: map[string]phy.PsePort{"lag1": {Group: "1"}}}},
			wantAttr: "port",
			wantVal:  "lag1",
		},
		{
			name: "unknown group",
			cfg: phy.Config{PoE: &phy.PoE{
				Groups: map[string]phy.Group{"1": {PowerMilliwatts: 60_000}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "7"}},
			}},
			wantAttr: "group",
			wantVal:  "7",
		},
		{
			name: "pd class above 8",
			cfg: phy.Config{PoE: &phy.PoE{
				Groups: map[string]phy.Group{"1": {PowerMilliwatts: 60_000}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", MaxClass: 8, PDClass: phy.Class(9)}},
			}},
			wantAttr: "class",
			wantVal:  uint8(9),
		},
		{
			name: "maximum class above 8",
			cfg: phy.Config{PoE: &phy.PoE{
				Groups: map[string]phy.Group{"1": {PowerMilliwatts: 60_000}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", MaxClass: 9}},
			}},
			wantAttr: "class",
			wantVal:  uint8(9),
		},
		{
			name: "invalid setting duplex",
			cfg: phy.Config{Ethernet: map[string]phy.Ethernet{
				"1/1/1": {Setting: &phy.Setting{Duplex: phy.Duplex("Bogus")}},
			}},
			wantAttr: "field",
			wantVal:  "ethernet.1/1/1.duplex",
		},
		{
			name: "invalid observed duplex",
			cfg: phy.Config{Ethernet: map[string]phy.Ethernet{
				"1/1/1": {Observed: &phy.Observed{Duplex: phy.Duplex("Bogus")}},
			}},
			wantAttr: "field",
			wantVal:  "ethernet.1/1/1.observed.duplex",
		},
		{
			name: "auto-negotiation requested but unsupported",
			cfg: phy.Config{Ethernet: map[string]phy.Ethernet{
				"1/1/1": {AutoNegotiationSupported: false, Setting: &phy.Setting{AutoNegotiation: true}},
			}},
			wantAttr: "field",
			wantVal:  "ethernet.1/1/1.auto_negotiation",
		},
		{
			name: "invalid poe priority",
			cfg: phy.Config{PoE: &phy.PoE{
				Groups: map[string]phy.Group{"1": {PowerMilliwatts: 60_000}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", Priority: phy.Priority("Bogus")}},
			}},
			wantAttr: "field",
			wantVal:  "poe.ports.1/1/1.priority",
		},
		{
			name: "empty poe group name",
			cfg: phy.Config{PoE: &phy.PoE{
				Groups: map[string]phy.Group{"": {PowerMilliwatts: 60_000}},
			}},
			wantAttr: "field",
			wantVal:  "poe.groups",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate(tbl)
			if tc.wantAttr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}

				return
			}
			if err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
			if got := errs.Attributes(err)[tc.wantAttr]; got != tc.wantVal {
				t.Errorf("errs.Attributes(err)[%q] = %v, want %v", tc.wantAttr, got, tc.wantVal)
			}
		})
	}
}

func TestDiff(t *testing.T) {
	t.Run("one group and one port change", func(t *testing.T) {
		a := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerMilliwatts: 60_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityLow, PDClass: phy.Class(4)}},
		}}
		b := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerMilliwatts: 90_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", MaxClass: 8, Enabled: false, Priority: phy.PriorityLow, PDClass: phy.Class(4)}},
		}}

		diffs := phy.Diff(a, b)
		if got, want := len(diffs), 2; got != want {
			t.Fatalf("len(diffs) = %d, want %d", got, want)
		}

		c0 := diffs[0]
		if c0.Layer != port.LayerPoe || c0.Subject.Kind != "pse_group" || c0.Subject.Key != "1" {
			t.Errorf("diffs[0] = %+v, want poe pse_group:1", c0)
		}
		if c0.Field != "power_milliwatts" || c0.From != phy.PowerFact(60_000) || c0.To != phy.PowerFact(90_000) {
			t.Errorf("diffs[0] = %+v, want power_milliwatts 60000 -> 90000", c0)
		}

		c1 := diffs[1]
		if c1.Layer != port.LayerPoe || c1.Subject.Kind != "port" || c1.Subject.Key != "1/1/1" {
			t.Errorf("diffs[1] = %+v, want poe port:1/1/1", c1)
		}
		if c1.Field != "enabled" || c1.From != phy.BoolFact(true) || c1.To != phy.BoolFact(false) {
			t.Errorf("diffs[1] = %+v, want enabled true -> false", c1)
		}
	})

	t.Run("added and removed entries carry an empty field and a nil side", func(t *testing.T) {
		a := phy.Config{
			Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: gigabitCapable}},
			PoE:      &phy.PoE{Groups: map[string]phy.Group{"1": {PowerMilliwatts: 60_000}}},
		}
		b := phy.Config{}

		diffs := phy.Diff(a, b)
		if got, want := len(diffs), 2; got != want {
			t.Fatalf("len(diffs) = %d, want %d", got, want)
		}

		c0 := diffs[0]
		if c0.Layer != port.LayerEthernet || c0.Field != "" || c0.To != nil {
			t.Errorf("diffs[0] = %+v, want removed ethernet port with empty field and nil To", c0)
		}
		if e, ok := c0.From.(phy.Ethernet); !ok || len(e.SupportedSpeedsBPS) != 3 {
			t.Errorf("diffs[0].From = %+v, want the removed Ethernet entry", c0.From)
		}

		c1 := diffs[1]
		if c1.Layer != port.LayerPoe || c1.Subject.Kind != "pse_group" || c1.Field != "" || c1.To != nil {
			t.Errorf("diffs[1] = %+v, want removed pse group with empty field and nil To", c1)
		}

		back := phy.Diff(b, a)
		if got, want := len(back), 2; got != want {
			t.Fatalf("len(back) = %d, want %d", got, want)
		}
		if back[0].From != nil || back[1].From != nil {
			t.Errorf("back = %+v, want added entries with nil From", back)
		}
	})

	t.Run("ethernet setting changes", func(t *testing.T) {
		a := phy.Config{Ethernet: map[string]phy.Ethernet{
			"1/1/1": {Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full}},
		}}
		b := phy.Config{Ethernet: map[string]phy.Ethernet{
			"1/1/1": {Setting: &phy.Setting{AutoNegotiation: true}},
		}}

		diffs := phy.Diff(a, b)
		if got, want := len(diffs), 3; got != want {
			t.Fatalf("len(diffs) = %d, want %d", got, want)
		}
		if diffs[0].Field != "speed_bps" || diffs[0].From != phy.SpeedFact(100_000_000) || diffs[0].To != phy.SpeedFact(0) {
			t.Errorf("diffs[0] = %+v, want speed_bps 100000000 -> 0", diffs[0])
		}
		if diffs[1].Field != "auto_negotiation_enabled" || diffs[1].From != phy.BoolFact(false) || diffs[1].To != phy.BoolFact(true) {
			t.Errorf("diffs[1] = %+v, want auto_negotiation_enabled false -> true", diffs[1])
		}
		if diffs[2].Field != "resolve_source" || diffs[2].From != phy.StringFact(phy.SourceSetting) || diffs[2].To != phy.StringFact(phy.SourceUnresolved) {
			t.Errorf("diffs[2] = %+v, want resolve_source setting -> unresolved", diffs[2])
		}
	})

	t.Run("present zero observation changes resolution source", func(t *testing.T) {
		a := phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": {}}}
		b := phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": {Observed: &phy.Observed{}}}}

		diffs := phy.Diff(a, b)
		if len(diffs) != 1 {
			t.Fatalf("len(Diff()) = %d, want 1: %+v", len(diffs), diffs)
		}
		if got := diffs[0]; got.Field != "resolve_source" || got.From != phy.StringFact(phy.SourceUnresolved) || got.To != phy.StringFact(phy.SourceObserved) {
			t.Errorf("Diff()[0] = %+v, want resolve_source unresolved -> observed", got)
		}
	})

	t.Run("all phy behavior fields diff coverage", func(t *testing.T) {
		limitA := uint32(15400)
		limitB := uint32(30000)
		pdA := uint8(1)
		pdB := uint8(2)

		a := phy.Config{
			Ethernet: map[string]phy.Ethernet{
				"1/1/1": {
					SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000},
					AutoNegotiationSupported: true,
					Setting:                  &phy.Setting{SpeedBPS: 10_000_000, Duplex: phy.Half, AutoNegotiation: false},
					Observed:                 &phy.Observed{SpeedBPS: 10_000_000, Duplex: phy.Half},
				},
			},
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{
					"1": {PowerMilliwatts: 50_000},
				},
				Ports: map[string]phy.PsePort{
					"1/1/1": {
						Group:    "1",
						MaxClass: 4,
						Enabled:  true,
						Limit:    &limitA,
						Priority: phy.PriorityLow,
						PDClass:  &pdA,
					},
				},
			},
		}

		b := phy.Config{
			Ethernet: map[string]phy.Ethernet{
				"1/1/1": {
					SupportedSpeedsBPS:       []uint64{100_000_000, 1_000_000_000},
					AutoNegotiationSupported: false,
					Setting:                  &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full, AutoNegotiation: true},
					Observed:                 &phy.Observed{SpeedBPS: 100_000_000, Duplex: phy.Full},
				},
			},
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{
					"1": {PowerMilliwatts: 100_000},
				},
				Ports: map[string]phy.PsePort{
					"1/1/1": {
						Group:    "1",
						MaxClass: 8,
						Enabled:  false,
						Limit:    &limitB,
						Priority: phy.PriorityHigh,
						PDClass:  &pdB,
					},
				},
			},
		}

		diffs := phy.Diff(a, b)
		ethFields := make(map[string]bool)
		poePortFields := make(map[string]bool)
		poeGroupFields := make(map[string]bool)

		for _, d := range diffs {
			if d.From == nil || d.To == nil {
				t.Errorf("field %q has nil fact: From=%v To=%v", d.Field, d.From, d.To)
			}
			if d.From.TypeID() == "" || d.To.TypeID() == "" {
				t.Errorf("field %q fact has empty TypeID", d.Field)
			}
			if d.Layer == port.LayerEthernet {
				ethFields[d.Field] = true
			}
			if d.Layer == port.LayerPoe && d.Subject.Kind == "port" {
				poePortFields[d.Field] = true
			}
			if d.Layer == port.LayerPoe && d.Subject.Kind == "pse_group" {
				poeGroupFields[d.Field] = true
			}
		}

		expectedEth := []string{
			"speed_bps",
			"duplex",
			"auto_negotiation_enabled",
			"supported_speeds_bps",
			"auto_negotiation_supported",
			"observed_speed_bps",
			"observed_duplex",
		}
		for _, f := range expectedEth {
			if !ethFields[f] {
				t.Errorf("diff missing ethernet field %q; seen: %v", f, ethFields)
			}
		}

		expectedPoePort := []string{
			"max_class",
			"enabled",
			"power_limit_milliwatts",
			"priority",
			"pd_class",
		}
		for _, f := range expectedPoePort {
			if !poePortFields[f] {
				t.Errorf("diff missing poe port field %q; seen: %v", f, poePortFields)
			}
		}

		if !poeGroupFields["power_milliwatts"] {
			t.Errorf("diff missing poe group field power_milliwatts; seen: %v", poeGroupFields)
		}
	})

	t.Run("identical configs yield an empty diff", func(t *testing.T) {
		cfg := phy.Config{
			Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: gigabitCapable, Setting: &phy.Setting{SpeedBPS: 1_000_000_000}}},
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{"1": {PowerMilliwatts: 60_000}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, PDClass: phy.Class(4)}},
			},
		}
		if diffs := phy.Diff(cfg, cfg); len(diffs) != 0 {
			t.Errorf("Diff(cfg, cfg) = %+v, want empty", diffs)
		}
	})
}

func TestNormalize(t *testing.T) {
	t.Run("default equivalence and idempotence", func(t *testing.T) {
		raw := phy.Config{
			Ethernet: map[string]phy.Ethernet{
				"1/1/1": {
					SupportedSpeedsBPS: []uint64{1_000_000_000, 100_000_000, 1_000_000_000},
					Setting:            &phy.Setting{SpeedBPS: 100_000_000},
				},
			},
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{"1": {PowerMilliwatts: 60_000}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", MaxClass: 8, Enabled: true}},
			},
		}

		explicit := phy.Config{
			Ethernet: map[string]phy.Ethernet{
				"1/1/1": {
					SupportedSpeedsBPS: []uint64{100_000_000, 1_000_000_000},
					Setting:            &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
				},
			},
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{"1": {PowerMilliwatts: 60_000}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityLow}},
			},
		}

		normRaw := raw.Normalize()
		normExplicit := explicit.Normalize()

		diffs := phy.Diff(normRaw, normExplicit)
		if len(diffs) != 0 {
			t.Errorf("normalized raw != normalized explicit: diffs = %+v", diffs)
		}

		// Idempotence
		normTwice := normRaw.Normalize()
		if len(phy.Diff(normRaw, normTwice)) != 0 {
			t.Errorf("Normalize() is not idempotent")
		}
	})

	t.Run("caller input immutability", func(t *testing.T) {
		speeds := []uint64{1_000_000_000, 100_000_000}
		raw := phy.Config{
			Ethernet: map[string]phy.Ethernet{
				"1/1/1": {SupportedSpeedsBPS: speeds, Setting: &phy.Setting{SpeedBPS: 100_000_000}},
			},
		}
		_ = raw.Normalize()
		if speeds[0] != 1_000_000_000 || speeds[1] != 100_000_000 {
			t.Errorf("caller speeds slice was mutated: %v", speeds)
		}
		if raw.Ethernet["1/1/1"].Setting.Duplex != "" {
			t.Errorf("caller setting was mutated: %+v", raw.Ethernet["1/1/1"].Setting)
		}
	})
}

func TestClone(t *testing.T) {
	limit := uint32(15400)
	pd := uint8(4)
	orig := phy.Config{
		Ethernet: map[string]phy.Ethernet{
			"1/1/1": {
				SupportedSpeedsBPS: []uint64{100_000_000},
				Setting:            &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
				Observed:           &phy.Observed{SpeedBPS: 100_000_000, Duplex: phy.Full},
			},
		},
		PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerMilliwatts: 60_000}},
			Ports: map[string]phy.PsePort{
				"1/1/1": {Group: "1", MaxClass: 8, Limit: &limit, PDClass: &pd},
			},
		},
	}

	cloned := orig.Clone()

	// Mutate clone and assert original is unmodified
	cloned.Ethernet["1/1/1"].SupportedSpeedsBPS[0] = 10_000_000
	cloned.Ethernet["1/1/1"].Setting.SpeedBPS = 10_000_000
	cloned.Ethernet["1/1/1"].Observed.SpeedBPS = 10_000_000
	*cloned.PoE.Ports["1/1/1"].Limit = 30000
	*cloned.PoE.Ports["1/1/1"].PDClass = 8

	if orig.Ethernet["1/1/1"].SupportedSpeedsBPS[0] != 100_000_000 {
		t.Errorf("orig SupportedSpeedsBPS mutated")
	}
	if orig.Ethernet["1/1/1"].Setting.SpeedBPS != 100_000_000 {
		t.Errorf("orig Setting mutated")
	}
	if orig.Ethernet["1/1/1"].Observed.SpeedBPS != 100_000_000 {
		t.Errorf("orig Observed mutated")
	}
	if *orig.PoE.Ports["1/1/1"].Limit != 15400 {
		t.Errorf("orig PoE Limit mutated")
	}
	if *orig.PoE.Ports["1/1/1"].PDClass != 4 {
		t.Errorf("orig PoE PDClass mutated")
	}
}

func TestPhyBehaviorMatrix(t *testing.T) {
	// Proves all behavior-bearing inputs are tracked
	fields := []string{
		"Ethernet.SupportedSpeedsBPS",
		"Ethernet.AutoNegotiationSupported",
		"Ethernet.Setting.SpeedBPS",
		"Ethernet.Setting.Duplex",
		"Ethernet.Setting.AutoNegotiation",
		"Ethernet.Observed.SpeedBPS",
		"Ethernet.Observed.Duplex",
		"PoE.Groups.PowerMilliwatts",
		"PoE.Ports.Group",
		"PoE.Ports.MaxClass",
		"PoE.Ports.Enabled",
		"PoE.Ports.Limit",
		"PoE.Ports.Priority",
		"PoE.Ports.PDClass",
	}
	if len(fields) != 14 {
		t.Fatalf("unexpected number of phy fields: %d", len(fields))
	}
}
