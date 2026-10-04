package phy_test

import (
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/port"
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
			AutoNegotiationSupported: phy.CapabilitySupported,
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
		want := phy.Resolved{SpeedBPS: 100_000_000, Duplex: phy.Full, Source: "setting"}
		if got != want {
			t.Errorf("Resolve() = %+v, want %+v", got, want)
		}
	})

	t.Run("fixed setting with observation of speed 0 resolves from setting", func(t *testing.T) {
		e := phy.Ethernet{
			SupportedSpeedsBPS: gigabitCapable,
			Setting:            &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
			Observed:           &phy.Observed{SpeedBPS: 0, Duplex: phy.Full},
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

		err := cfg.Validate(layer.Env{Ports: tbl})
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
	class4 := func(priority phy.Priority, limit *uint64) phy.PsePort {
		return phy.PsePort{Group: "1", MaxClass: 8, Enabled: true, Limit: limit, Priority: priority, PD: phy.PDAttached, PDClass: phy.Class(4)}
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
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
			Ports:  threePorts(),
		}}

		got := cfg.Allocate()
		for _, name := range []string{"1/1/2", "1/1/3"} {
			if pa := got.Ports[name]; pa.Denial != "" || pa.MaxNanowatts != 30_000_000_000 || pa.State != phy.PowerDelivered {
				t.Errorf("Ports[%q] = %+v, want 30 W granted", name, pa)
			}
		}
		if pa := got.Ports["1/1/1"]; pa.Denial != phy.ReasonBudget || pa.MaxNanowatts != 0 || pa.State != phy.PowerDenied {
			t.Errorf("Ports[\"1/1/1\"] = %+v, want denial %q", pa, phy.ReasonBudget)
		}
		if g := got.Groups["1"]; g.BudgetNanowatts != 60_000_000_000 || g.AllocatedNanowatts != 60_000_000_000 || g.RemainderMinNanowatts != 0 || g.RemainderMaxNanowatts != 0 {
			t.Errorf("Groups[\"1\"] = %+v, want budget 60000, allocated 60000, remainder 0", g)
		}
	})

	t.Run("90 W allocates all three", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 90_000_000_000}},
			Ports:  threePorts(),
		}}

		got := cfg.Allocate()
		for _, name := range []string{"1/1/1", "1/1/2", "1/1/3"} {
			if pa := got.Ports[name]; pa.Denial != "" || pa.MaxNanowatts != 30_000_000_000 || pa.State != phy.PowerDelivered {
				t.Errorf("Ports[%q] = %+v, want 30 W granted", name, pa)
			}
		}
		if g := got.Groups["1"]; g.RemainderMinNanowatts != 0 || g.RemainderMaxNanowatts != 0 {
			t.Errorf("Groups[\"1\"] remainders = (%d, %d), want (0, 0)", g.RemainderMinNanowatts, g.RemainderMaxNanowatts)
		}
	})

	t.Run("class-4 port with a 15.4 W limit is denied", func(t *testing.T) {
		limit := uint64(15_400_000_000)
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": class4(phy.PriorityCritical, &limit)},
		}}

		got := cfg.Allocate()
		if pa := got.Ports["1/1/1"]; pa.Denial != "limit" || pa.MaxNanowatts != 0 || pa.State != phy.PowerDenied {
			t.Errorf("Ports[\"1/1/1\"] = %+v, want denial %q", pa, "limit")
		}
		if g := got.Groups["1"]; g.RemainderMinNanowatts != 60_000_000_000 || g.RemainderMaxNanowatts != 60_000_000_000 {
			t.Errorf("Groups[\"1\"] remainders = (%d, %d), want 60000000000", g.RemainderMinNanowatts, g.RemainderMaxNanowatts)
		}
	})

	t.Run("equal priority breaks the tie by port name", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 30_000_000_000}},
			Ports: map[string]phy.PsePort{
				"1/1/2": class4(phy.PriorityCritical, nil),
				"1/1/1": class4(phy.PriorityCritical, nil),
			},
		}}

		got := cfg.Allocate()
		if pa := got.Ports["1/1/1"]; pa.Denial != "" || pa.MaxNanowatts != 30_000_000_000 || pa.State != phy.PowerDelivered {
			t.Errorf("Ports[\"1/1/1\"] = %+v, want 30 W granted", pa)
		}
		if pa := got.Ports["1/1/2"]; pa.Denial != phy.ReasonBudget || pa.State != phy.PowerDenied {
			t.Errorf("Ports[\"1/1/2\"].Denial = %q, want %q", pa.Denial, phy.ReasonBudget)
		}
	})
}

func TestClassAbovePortMaximum(t *testing.T) {
	cfg := phy.Config{PoE: &phy.PoE{
		Groups: map[string]phy.Group{"1": {PowerNanowatts: 90_000_000_000}},
		Ports: map[string]phy.PsePort{
			"1/1/1": {Group: "1", MaxClass: 4, Enabled: true, Priority: phy.PriorityCritical, PD: phy.PDAttached, PDClass: phy.Class(6)},
		},
	}}

	got := cfg.Allocate()
	if pa := got.Ports["1/1/1"]; pa.Denial != "class-unsupported" || pa.MaxNanowatts != 0 || pa.State != phy.PowerDenied {
		t.Errorf("Ports[\"1/1/1\"] = %+v, want denial %q", pa, "class-unsupported")
	}
	if g := got.Groups["1"]; g.RemainderMinNanowatts != 90_000_000_000 || g.RemainderMaxNanowatts != 90_000_000_000 {
		t.Errorf("Groups[\"1\"] remainders = (%d, %d), want 90000000000", g.RemainderMinNanowatts, g.RemainderMaxNanowatts)
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
				AutoNegotiationSupported: phy.CapabilitySupported,
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
		e := phy.Ethernet{SupportedSpeedsBPS: gigabitCapable, AutoNegotiationSupported: phy.CapabilitySupported}
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

	t.Run("zero observation without a setting is unresolved", func(t *testing.T) {
		e := phy.Ethernet{Observed: &phy.Observed{SpeedBPS: 0, Duplex: phy.Full}}
		got := e.Resolve()
		want := phy.Resolved{Source: phy.SourceUnresolved}
		if got != want {
			t.Errorf("Resolve() = %+v, want %+v", got, want)
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

func TestValidate(t *testing.T) {
	tbl := mustTable(t,
		port.Port{Name: "1/1/1", Kind: port.Physical},
		port.Port{Name: "lag1", Kind: port.LAG},
	)
	validPoE := &phy.PoE{
		Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
		Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, PD: phy.PDAttached, PDClass: phy.Class(4)}},
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
			name: "fixed speed with unreported supported speeds is accepted",
			cfg: phy.Config{Ethernet: map[string]phy.Ethernet{
				"1/1/1": {Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full}},
			}},
			wantAttr: "",
		},
		{
			name: "an auto-negotiating setting carries no speed to check",
			cfg: phy.Config{Ethernet: map[string]phy.Ethernet{
				"1/1/1": {SupportedSpeedsBPS: gigabitCapable, AutoNegotiationSupported: phy.CapabilitySupported, Setting: &phy.Setting{AutoNegotiation: true}},
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
				Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "7"}},
			}},
			wantAttr: "group",
			wantVal:  "7",
		},
		{
			name: "pd class above 8",
			cfg: phy.Config{PoE: &phy.PoE{
				Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", MaxClass: 8, PD: phy.PDAttached, PDClass: phy.Class(9)}},
			}},
			wantAttr: "class",
			wantVal:  uint8(9),
		},
		{
			name: "maximum class above 8",
			cfg: phy.Config{PoE: &phy.PoE{
				Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
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
			name: "invalid auto-negotiation capability",
			cfg: phy.Config{Ethernet: map[string]phy.Ethernet{
				"1/1/1": {AutoNegotiationSupported: phy.Capability("Bogus")},
			}},
			wantAttr: "field",
			wantVal:  "ethernet.1/1/1.auto_negotiation_supported",
		},
		{
			name: "auto-negotiation requested but unsupported",
			cfg: phy.Config{Ethernet: map[string]phy.Ethernet{
				"1/1/1": {AutoNegotiationSupported: phy.CapabilityUnsupported, Setting: &phy.Setting{AutoNegotiation: true}},
			}},
			wantAttr: "field",
			wantVal:  "ethernet.1/1/1.auto_negotiation",
		},
		{
			name: "invalid poe pd state",
			cfg: phy.Config{PoE: &phy.PoE{
				Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", PD: phy.PDState("Bogus")}},
			}},
			wantAttr: "field",
			wantVal:  "poe.ports.1/1/1.pd",
		},
		{
			name: "pd class declared without attached pd",
			cfg: phy.Config{PoE: &phy.PoE{
				Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", PDClass: phy.Class(4)}},
			}},
			wantAttr: "field",
			wantVal:  "poe.ports.1/1/1.pd_class",
		},
		{
			name: "invalid poe priority",
			cfg: phy.Config{PoE: &phy.PoE{
				Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", Priority: phy.Priority("Bogus")}},
			}},
			wantAttr: "field",
			wantVal:  "poe.ports.1/1/1.priority",
		},
		{
			name: "empty poe group name",
			cfg: phy.Config{PoE: &phy.PoE{
				Groups: map[string]phy.Group{"": {PowerNanowatts: 60_000_000_000}},
			}},
			wantAttr: "field",
			wantVal:  "poe.groups",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate(layer.Env{Ports: tbl})
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
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityLow, PDClass: phy.Class(4)}},
		}}
		b := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 90_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", MaxClass: 8, Enabled: false, Priority: phy.PriorityLow, PDClass: phy.Class(4)}},
		}}

		diffs := phy.Diff(a, b)
		if got, want := len(diffs), 2; got != want {
			t.Fatalf("len(diffs) = %d, want %d", got, want)
		}

		c0 := diffs[0]
		if c0.Layer != phy.LayerNamePoE || c0.Subject.Kind != "pse_group" || c0.Subject.Key != "1" {
			t.Errorf("diffs[0] = %+v, want poe pse_group:1", c0)
		}
		if c0.Field != "power_nanowatts" || c0.From.TypeID() != "phy.power_nw" || c0.To.TypeID() != "phy.power_nw" || c0.From.Canonical() != "60000000000" || c0.To.Canonical() != "90000000000" {
			t.Errorf("diffs[0] = %+v, want power_nanowatts 60000000000 -> 90000000000", c0)
		}

		c1 := diffs[1]
		if c1.Layer != phy.LayerNamePoE || c1.Subject.Kind != "port" || c1.Subject.Key != "1/1/1" {
			t.Errorf("diffs[1] = %+v, want poe port:1/1/1", c1)
		}
		if c1.Field != "enabled" || c1.From.TypeID() != "phy.bool" || c1.To.TypeID() != "phy.bool" || c1.From.Canonical() != "true" || c1.To.Canonical() != "false" {
			t.Errorf("diffs[1] = %+v, want enabled true -> false", c1)
		}
	})

	t.Run("added and removed entries carry an empty field and a nil side", func(t *testing.T) {
		a := phy.Config{
			Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: gigabitCapable}},
			PoE:      &phy.PoE{Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}}},
		}
		b := phy.Config{}

		diffs := phy.Diff(a, b)
		if got, want := len(diffs), 2; got != want {
			t.Fatalf("len(diffs) = %d, want %d", got, want)
		}

		c0 := diffs[0]
		if c0.Layer != phy.LayerName || c0.Field != "" || c0.To != nil {
			t.Errorf("diffs[0] = %+v, want removed ethernet port with empty field and nil To", c0)
		}
		if c0.From == nil || c0.From.TypeID() != "phy.ethernet" || !strings.Contains(c0.From.Canonical(), "1000000000") {
			t.Errorf("diffs[0].From = %+v, want the removed Ethernet entry", c0.From)
		}

		c1 := diffs[1]
		if c1.Layer != phy.LayerNamePoE || c1.Subject.Kind != "pse_group" || c1.Field != "" || c1.To != nil {
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
			"1/1/1": {Setting: &phy.Setting{AutoNegotiation: true, Duplex: phy.Full}},
		}}

		diffs := phy.Diff(a, b)
		if got, want := len(diffs), 2; got != want {
			t.Fatalf("len(diffs) = %d, want %d", got, want)
		}
		if diffs[0].Field != "speed_bps" || diffs[0].From.TypeID() != "phy.speed_bps" || diffs[0].To.TypeID() != "phy.speed_bps" || diffs[0].From.Canonical() != "100000000" || diffs[0].To.Canonical() != "0" {
			t.Errorf("diffs[0] = %+v, want speed_bps 100000000 -> 0", diffs[0])
		}
		if diffs[1].Field != "auto_negotiation_enabled" || diffs[1].From.TypeID() != "phy.bool" || diffs[1].To.TypeID() != "phy.bool" || diffs[1].From.Canonical() != "false" || diffs[1].To.Canonical() != "true" {
			t.Errorf("diffs[1] = %+v, want auto_negotiation_enabled false -> true", diffs[1])
		}
	})

	t.Run("all phy behavior fields diff coverage", func(t *testing.T) {
		limitA := uint64(15_400_000_000)
		limitB := uint64(30_000_000_000)
		pdA := uint8(1)
		pdB := uint8(2)

		a := phy.Config{
			Ethernet: map[string]phy.Ethernet{
				"1/1/1": {
					SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000},
					AutoNegotiationSupported: phy.CapabilitySupported,
					Setting:                  &phy.Setting{SpeedBPS: 10_000_000, Duplex: phy.Half, AutoNegotiation: false},
					Observed:                 &phy.Observed{SpeedBPS: 10_000_000, Duplex: phy.Half},
				},
			},
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{
					"1": {PowerNanowatts: 50_000_000_000},
				},
				Ports: map[string]phy.PsePort{
					"1/1/1": {
						Group:    "1",
						MaxClass: 4,
						Enabled:  true,
						Limit:    &limitA,
						Priority: phy.PriorityLow,
						PD:       phy.PDAttached,
						PDClass:  &pdA,
					},
				},
			},
		}

		b := phy.Config{
			Ethernet: map[string]phy.Ethernet{
				"1/1/1": {
					SupportedSpeedsBPS:       []uint64{100_000_000, 1_000_000_000},
					AutoNegotiationSupported: phy.CapabilityUnsupported,
					Setting:                  &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full, AutoNegotiation: true},
					Observed:                 &phy.Observed{SpeedBPS: 100_000_000, Duplex: phy.Full},
				},
			},
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{
					"1": {PowerNanowatts: 100_000_000_000},
				},
				Ports: map[string]phy.PsePort{
					"1/1/1": {
						Group:    "1",
						MaxClass: 8,
						Enabled:  false,
						Limit:    &limitB,
						Priority: phy.PriorityHigh,
						PD:       phy.PDAbsent,
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
			if d.Layer == phy.LayerName {
				ethFields[d.Field] = true
			}
			if d.Layer == phy.LayerNamePoE && d.Subject.Kind == "port" {
				poePortFields[d.Field] = true
			}
			if d.Layer == phy.LayerNamePoE && d.Subject.Kind == "pse_group" {
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
			"power_limit_nanowatts",
			"priority",
			"pd",
			"pd_class",
		}
		for _, f := range expectedPoePort {
			if !poePortFields[f] {
				t.Errorf("diff missing poe port field %q; seen: %v", f, poePortFields)
			}
		}

		if !poeGroupFields["power_nanowatts"] {
			t.Errorf("diff missing poe group field power_nanowatts; seen: %v", poeGroupFields)
		}
	})

	t.Run("identical configs yield an empty diff", func(t *testing.T) {
		cfg := phy.Config{
			Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: gigabitCapable, Setting: &phy.Setting{SpeedBPS: 1_000_000_000}}},
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
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
				Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", MaxClass: 8, Enabled: true}},
			},
		}

		explicit := phy.Config{
			Ethernet: map[string]phy.Ethernet{
				"1/1/1": {
					SupportedSpeedsBPS: []uint64{100_000_000, 1_000_000_000},
					Setting:            &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Unknown},
				},
			},
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityLow}},
			},
		}

		normRaw := raw.Normalize(layer.Env{})
		normExplicit := explicit.Normalize(layer.Env{})

		diffs := phy.Diff(normRaw, normExplicit)
		if len(diffs) != 0 {
			t.Errorf("normalized raw != normalized explicit: diffs = %+v", diffs)
		}

		// Idempotence
		normTwice := normRaw.Normalize(layer.Env{})
		if len(phy.Diff(normRaw, normTwice)) != 0 {
			t.Errorf("Normalize() is not idempotent")
		}
	})

	t.Run("empty duplex defaults to Unknown in setting and observed", func(t *testing.T) {
		raw := phy.Config{
			Ethernet: map[string]phy.Ethernet{
				"1/1/1": {
					Setting:  &phy.Setting{SpeedBPS: 1_000_000_000, AutoNegotiation: true},
					Observed: &phy.Observed{SpeedBPS: 1_000_000_000},
				},
			},
		}
		norm := raw.Normalize(layer.Env{})
		e := norm.Ethernet["1/1/1"]
		if e.Setting.Duplex != phy.Unknown {
			t.Errorf("Setting.Duplex = %q, want %q", e.Setting.Duplex, phy.Unknown)
		}
		if e.Observed.Duplex != phy.Unknown {
			t.Errorf("Observed.Duplex = %q, want %q", e.Observed.Duplex, phy.Unknown)
		}
	})

	t.Run("caller input immutability", func(t *testing.T) {
		speeds := []uint64{1_000_000_000, 100_000_000}
		raw := phy.Config{
			Ethernet: map[string]phy.Ethernet{
				"1/1/1": {SupportedSpeedsBPS: speeds, Setting: &phy.Setting{SpeedBPS: 100_000_000}},
			},
		}
		_ = raw.Normalize(layer.Env{})
		if speeds[0] != 1_000_000_000 || speeds[1] != 100_000_000 {
			t.Errorf("caller speeds slice was mutated: %v", speeds)
		}
		if raw.Ethernet["1/1/1"].Setting.Duplex != "" {
			t.Errorf("caller setting was mutated: %+v", raw.Ethernet["1/1/1"].Setting)
		}
	})
}

func TestClone(t *testing.T) {
	limit := uint64(15_400_000_000)
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
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
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
	*cloned.PoE.Ports["1/1/1"].Limit = 30_000_000_000
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
	if *orig.PoE.Ports["1/1/1"].Limit != 15_400_000_000 {
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
		"PoE.Groups.PowerNanowatts",
		"PoE.Ports.Group",
		"PoE.Ports.MaxClass",
		"PoE.Ports.Enabled",
		"PoE.Ports.Limit",
		"PoE.Ports.Priority",
		"PoE.Ports.PD",
		"PoE.Ports.PDClass",
	}
	if len(fields) != 15 {
		t.Fatalf("unexpected number of phy fields: %d", len(fields))
	}
}

func TestPoeAllocateTruthTable(t *testing.T) {
	limit15k := uint64(15_000_000_000)

	t.Run("row 1: disabled and absent yields PowerNoDevice 0..0", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", Enabled: false, PD: phy.PDAbsent}},
		}}
		got := cfg.Allocate().Ports["1/1/1"]
		want := phy.PortAllocation{State: phy.PowerNoDevice, MinNanowatts: 0, MaxNanowatts: 0}
		if got != want {
			t.Errorf("Allocate() port = %+v, want %+v", got, want)
		}
	})

	t.Run("row 2: disabled and attached yields PowerDenied disabled 0..0", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", Enabled: false, PD: phy.PDAttached, PDClass: phy.Class(4)}},
		}}
		got := cfg.Allocate().Ports["1/1/1"]
		want := phy.PortAllocation{State: phy.PowerDenied, Denial: phy.ReasonDisabled, MinNanowatts: 0, MaxNanowatts: 0}
		if got != want {
			t.Errorf("Allocate() port = %+v, want %+v", got, want)
		}
	})

	t.Run("row 3: disabled and unknown yields PowerDenied disabled 0..0", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", Enabled: false, PD: phy.PDUnknown}},
		}}
		got := cfg.Allocate().Ports["1/1/1"]
		want := phy.PortAllocation{State: phy.PowerDenied, Denial: phy.ReasonDisabled, MinNanowatts: 0, MaxNanowatts: 0}
		if got != want {
			t.Errorf("Allocate() port = %+v, want %+v", got, want)
		}
	})

	t.Run("row 4: enabled and absent yields PowerNoDevice 0..0", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", Enabled: true, PD: phy.PDAbsent}},
		}}
		got := cfg.Allocate().Ports["1/1/1"]
		want := phy.PortAllocation{State: phy.PowerNoDevice, MinNanowatts: 0, MaxNanowatts: 0}
		if got != want {
			t.Errorf("Allocate() port = %+v, want %+v", got, want)
		}
	})

	t.Run("row 5: enabled, attached, class above maxClass yields PowerDenied class-unsupported", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", Enabled: true, MaxClass: 3, PD: phy.PDAttached, PDClass: phy.Class(4)}},
		}}
		got := cfg.Allocate().Ports["1/1/1"]
		want := phy.PortAllocation{State: phy.PowerDenied, Denial: "class-unsupported", MinNanowatts: 0, MaxNanowatts: 0}
		if got != want {
			t.Errorf("Allocate() port = %+v, want %+v", got, want)
		}
	})

	t.Run("row 6: enabled, attached, P above limit yields PowerDenied limit", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", Enabled: true, MaxClass: 8, Limit: &limit15k, PD: phy.PDAttached, PDClass: phy.Class(4)}},
		}}
		got := cfg.Allocate().Ports["1/1/1"]
		want := phy.PortAllocation{State: phy.PowerDenied, Denial: "limit", MinNanowatts: 0, MaxNanowatts: 0}
		if got != want {
			t.Errorf("Allocate() port = %+v, want %+v", got, want)
		}
	})

	t.Run("row 7: enabled, attached, P <= remMin yields PowerDelivered P..P", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 60_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", Enabled: true, MaxClass: 8, PD: phy.PDAttached, PDClass: phy.Class(4)}},
		}}
		alloc := cfg.Allocate()
		got := alloc.Ports["1/1/1"]
		want := phy.PortAllocation{State: phy.PowerDelivered, MinNanowatts: 30_000_000_000, MaxNanowatts: 30_000_000_000}
		if got != want {
			t.Errorf("Allocate() port = %+v, want %+v", got, want)
		}
		if g := alloc.Groups["1"]; g.AllocatedNanowatts != 30_000_000_000 || g.RemainderMinNanowatts != 30_000_000_000 || g.RemainderMaxNanowatts != 30_000_000_000 {
			t.Errorf("Allocate() group = %+v, want allocated 30000 remainders (30000, 30000)", g)
		}
	})

	t.Run("row 8: enabled, attached, P > remMax yields PowerDenied budget", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 15_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", Enabled: true, MaxClass: 8, PD: phy.PDAttached, PDClass: phy.Class(4)}},
		}}
		got := cfg.Allocate().Ports["1/1/1"]
		want := phy.PortAllocation{State: phy.PowerDenied, Denial: phy.ReasonBudget, MinNanowatts: 0, MaxNanowatts: 0}
		if got != want {
			t.Errorf("Allocate() port = %+v, want %+v", got, want)
		}
	})

	t.Run("row 9: enabled, attached, remMin < P <= remMax yields PowerUnknown 0..P", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 30_000_000_000}},
			Ports: map[string]phy.PsePort{
				"1/1/1": {Group: "1", Enabled: true, Priority: phy.PriorityHigh, MaxClass: 4, PD: phy.PDUnknown},
				"1/1/2": {Group: "1", Enabled: true, Priority: phy.PriorityLow, MaxClass: 8, PD: phy.PDAttached, PDClass: phy.Class(4)},
			},
		}}
		got := cfg.Allocate().Ports["1/1/2"]
		want := phy.PortAllocation{State: phy.PowerUnknown, MinNanowatts: 0, MaxNanowatts: 30_000_000_000}
		if got != want {
			t.Errorf("Allocate() port = %+v, want %+v", got, want)
		}
	})

	t.Run("row 10: enabled, attached with unknown class yields PowerUnknown 0..D", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 50_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", Enabled: true, MaxClass: 4, PD: phy.PDAttached}},
		}}
		got := cfg.Allocate().Ports["1/1/1"]
		want := phy.PortAllocation{State: phy.PowerUnknown, MinNanowatts: 0, MaxNanowatts: 30_000_000_000}
		if got != want {
			t.Errorf("Allocate() port = %+v, want %+v", got, want)
		}
	})

	t.Run("row 11: enabled, PD unknown yields PowerUnknown 0..D", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 50_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", Enabled: true, MaxClass: 4, PD: phy.PDUnknown}},
		}}
		got := cfg.Allocate().Ports["1/1/1"]
		want := phy.PortAllocation{State: phy.PowerUnknown, MinNanowatts: 0, MaxNanowatts: 30_000_000_000}
		if got != want {
			t.Errorf("Allocate() port = %+v, want %+v", got, want)
		}
	})

	t.Run("row 12: unknown device class power exceeding budget clamps to budget", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 20_000_000_000}},
			Ports: map[string]phy.PsePort{
				"1/1/1": {Group: "1", Enabled: true, MaxClass: 4, PD: phy.PDUnknown},
			},
		}}
		alloc := cfg.Allocate()
		got := alloc.Ports["1/1/1"]
		want := phy.PortAllocation{State: phy.PowerUnknown, MinNanowatts: 0, MaxNanowatts: 20_000_000_000}
		if got != want {
			t.Errorf("Allocate() port = %+v, want %+v", got, want)
		}
		if g := alloc.Groups["1"]; g.RemainderMinNanowatts != 0 || g.RemainderMaxNanowatts != 20_000_000_000 {
			t.Errorf("Allocate() group = %+v, want remMin 0, remMax 20W", g)
		}
	})

	t.Run("acceptance example: 30W budget with critical unknown, low class 3, low class 1", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 30_000_000_000}},
			Ports: map[string]phy.PsePort{
				"1/1/1": {Group: "1", Priority: phy.PriorityCritical, Enabled: true, MaxClass: 4, PD: phy.PDUnknown},
				"1/1/2": {Group: "1", Priority: phy.PriorityLow, Enabled: true, MaxClass: 8, PD: phy.PDAttached, PDClass: phy.Class(3)},
				"1/1/3": {Group: "1", Priority: phy.PriorityLow, Enabled: true, MaxClass: 8, PD: phy.PDAttached, PDClass: phy.Class(1)},
			},
		}}
		alloc := cfg.Allocate()
		p1 := alloc.Ports["1/1/1"]
		if p1.State != phy.PowerUnknown || p1.MinNanowatts != 0 || p1.MaxNanowatts != 30_000_000_000 {
			t.Errorf("port 1/1/1 = %+v, want PowerUnknown 0..30000", p1)
		}
		p2 := alloc.Ports["1/1/2"]
		if p2.State != phy.PowerUnknown || p2.MinNanowatts != 0 || p2.MaxNanowatts != 15_400_000_000 {
			t.Errorf("port 1/1/2 = %+v, want PowerUnknown 0..15400", p2)
		}
		p3 := alloc.Ports["1/1/3"]
		if p3.State != phy.PowerUnknown || p3.MinNanowatts != 0 || p3.MaxNanowatts != 4_000_000_000 {
			t.Errorf("port 1/1/3 = %+v, want PowerUnknown 0..4000", p3)
		}
		if g := alloc.Groups["1"]; g.RemainderMinNanowatts != 0 || g.RemainderMaxNanowatts != 30_000_000_000 || g.AllocatedNanowatts != 0 {
			t.Errorf("group = %+v, want allocated 0 remainders (0, 30000)", g)
		}
	})

	t.Run("remMin subtraction saturates at zero without underflow", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 10_000_000_000}},
			Ports: map[string]phy.PsePort{
				"1/1/1": {Group: "1", Priority: phy.PriorityHigh, Enabled: true, MaxClass: 4, PD: phy.PDUnknown},
				"1/1/2": {Group: "1", Priority: phy.PriorityLow, Enabled: true, MaxClass: 4, PD: phy.PDUnknown},
			},
		}}
		alloc := cfg.Allocate()
		p1 := alloc.Ports["1/1/1"]
		if p1.State != phy.PowerUnknown || p1.MinNanowatts != 0 || p1.MaxNanowatts != 10_000_000_000 {
			t.Errorf("port 1/1/1 = %+v, want PowerUnknown 0..10000", p1)
		}
		p2 := alloc.Ports["1/1/2"]
		if p2.State != phy.PowerUnknown || p2.MinNanowatts != 0 || p2.MaxNanowatts != 10_000_000_000 {
			t.Errorf("port 1/1/2 = %+v, want PowerUnknown 0..10000", p2)
		}
		if g := alloc.Groups["1"]; g.RemainderMinNanowatts != 0 || g.RemainderMaxNanowatts != 10_000_000_000 {
			t.Errorf("group = %+v, want remMin 0, remMax 10000", g)
		}
	})

	t.Run("D is capped by port limit", func(t *testing.T) {
		lim := uint64(15_400_000_000)
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 50_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", Enabled: true, MaxClass: 4, Limit: &lim, PD: phy.PDUnknown}},
		}}
		got := cfg.Allocate().Ports["1/1/1"]
		want := phy.PortAllocation{State: phy.PowerUnknown, MinNanowatts: 0, MaxNanowatts: 15_400_000_000}
		if got != want {
			t.Errorf("Allocate() port = %+v, want %+v", got, want)
		}
	})

	t.Run("D is zero when no class fits limit", func(t *testing.T) {
		lim := uint64(1_000_000_000)
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 50_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", Enabled: true, MaxClass: 4, Limit: &lim, PD: phy.PDUnknown}},
		}}
		got := cfg.Allocate().Ports["1/1/1"]
		want := phy.PortAllocation{State: phy.PowerUnknown, MinNanowatts: 0, MaxNanowatts: 0}
		if got != want {
			t.Errorf("Allocate() port = %+v, want %+v", got, want)
		}
	})

	t.Run("D is maximum class power rather than lookup at maxClass", func(t *testing.T) {
		cfg := phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerNanowatts: 50_000_000_000}},
			Ports:  map[string]phy.PsePort{"1/1/1": {Group: "1", Enabled: true, MaxClass: 2, PD: phy.PDUnknown}},
		}}
		// Classes <= 2 have powers: class 0 (15400), class 1 (4000), class 2 (7000). Max is 15400, not class 2's 7000.
		got := cfg.Allocate().Ports["1/1/1"]
		want := phy.PortAllocation{State: phy.PowerUnknown, MinNanowatts: 0, MaxNanowatts: 15_400_000_000}
		if got != want {
			t.Errorf("Allocate() port = %+v, want %+v", got, want)
		}
	})
}

func TestUnknownDemandUsesRemainingMaximum(t *testing.T) {
	tests := []struct {
		name string
		pd   phy.PDState
	}{
		{name: "attached device with unknown class", pd: phy.PDAttached},
		{name: "unknown device state", pd: phy.PDUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := phy.Config{PoE: &phy.PoE{
				Groups: map[string]phy.Group{"1": {PowerNanowatts: 50_000_000_000}},
				Ports: map[string]phy.PsePort{
					"1/1/1": {Group: "1", Priority: phy.PriorityCritical, Enabled: true, MaxClass: 4, PD: phy.PDAttached, PDClass: phy.Class(4)},
					"1/1/2": {Group: "1", Priority: phy.PriorityLow, Enabled: true, MaxClass: 4, PD: tc.pd},
				},
			}}
			alloc := cfg.Allocate()
			if got, want := alloc.Ports["1/1/1"], (phy.PortAllocation{State: phy.PowerDelivered, MinNanowatts: 30_000_000_000, MaxNanowatts: 30_000_000_000}); got != want {
				t.Errorf("first port = %+v, want %+v", got, want)
			}
			if got, want := alloc.Ports["1/1/2"], (phy.PortAllocation{State: phy.PowerUnknown, MaxNanowatts: 20_000_000_000}); got != want {
				t.Errorf("unknown demand = %+v, want %+v", got, want)
			}
			if got, want := alloc.Groups["1"], (phy.GroupAllocation{
				BudgetNanowatts:       50_000_000_000,
				AllocatedNanowatts:    30_000_000_000,
				RemainderMinNanowatts: 0,
				RemainderMaxNanowatts: 20_000_000_000,
			}); got != want {
				t.Errorf("group = %+v, want %+v", got, want)
			}
		})
	}
}

func TestDisabledUnknownDoesNotChargeBudget(t *testing.T) {
	cfg := phy.Config{PoE: &phy.PoE{
		Groups: map[string]phy.Group{"1": {PowerNanowatts: 30_000_000_000}},
		Ports: map[string]phy.PsePort{
			"1/1/1": {Group: "1", Priority: phy.PriorityCritical, Enabled: false, MaxClass: 4, PD: phy.PDUnknown},
			"1/1/2": {Group: "1", Priority: phy.PriorityLow, Enabled: true, MaxClass: 4, PD: phy.PDAttached, PDClass: phy.Class(4)},
		},
	}}
	alloc := cfg.Allocate()
	if got, want := alloc.Ports["1/1/1"], (phy.PortAllocation{State: phy.PowerDenied, Denial: phy.ReasonDisabled}); got != want {
		t.Errorf("disabled port = %+v, want %+v", got, want)
	}
	if got, want := alloc.Ports["1/1/2"], (phy.PortAllocation{State: phy.PowerDelivered, MinNanowatts: 30_000_000_000, MaxNanowatts: 30_000_000_000}); got != want {
		t.Errorf("enabled port = %+v, want %+v", got, want)
	}
	if got, want := alloc.Groups["1"], (phy.GroupAllocation{
		BudgetNanowatts:       30_000_000_000,
		AllocatedNanowatts:    30_000_000_000,
		RemainderMinNanowatts: 0,
		RemainderMaxNanowatts: 0,
	}); got != want {
		t.Errorf("group = %+v, want %+v", got, want)
	}
}
