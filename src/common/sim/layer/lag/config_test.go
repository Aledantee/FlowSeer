package lag_test

import (
	"strconv"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func mustMAC(t *testing.T, s string) netaddr.MAC {
	t.Helper()
	m, err := netaddr.Parse(s)
	if err != nil {
		t.Fatalf("parse MAC %q: %v", s, err)
	}

	return m
}

func lagPortTable(t *testing.T) port.Table {
	t.Helper()
	tbl, err := port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.LAG}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical}).
		Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	return tbl
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tbl := lagPortTable(t)

	t.Run("valid configuration passes", func(t *testing.T) {
		t.Parallel()
		cfg := lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {
					Mode:    lag.ActiveBackup,
					Primary: "1/1/1",
					Members: map[string]lag.Member{
						"1/1/1": {Priority: 32768},
						"1/1/2": {Priority: 32768},
					},
				},
			},
		}
		if err := cfg.Validate(layer.Env{Ports: tbl}); err != nil {
			t.Fatalf("Validate failed for valid config: %v", err)
		}
	})

	t.Run("absent LAG port accepted", func(t *testing.T) {
		t.Parallel()
		cfg := lag.Config{}
		if err := cfg.Validate(layer.Env{Ports: tbl}); err != nil {
			t.Fatalf("Validate failed for empty config: %v", err)
		}
	})

	t.Run("refuses non-LAG port as LAG", func(t *testing.T) {
		t.Parallel()
		cfg := lag.Config{
			LAGs: map[string]lag.LAG{
				"1/1/3": {},
			},
		}
		if err := cfg.Validate(layer.Env{Ports: tbl}); err == nil {
			t.Fatal("Validate succeeded for physical port as LAG, want error")
		}
	})

	t.Run("refuses primary naming non-member", func(t *testing.T) {
		t.Parallel()
		cfg := lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {
					Primary: "1/1/3",
				},
			},
		}
		if err := cfg.Validate(layer.Env{Ports: tbl}); err == nil {
			t.Fatal("Validate succeeded with Primary naming non-member, want error")
		}
	})

	t.Run("refuses member key on port that is not a member", func(t *testing.T) {
		t.Parallel()
		cfg := lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {
					Members: map[string]lag.Member{
						"1/1/3": {Key: 1},
					},
				},
			},
		}
		if err := cfg.Validate(layer.Env{Ports: tbl}); err == nil {
			t.Fatal("Validate succeeded with member key on non-member, want error")
		}
	})

	t.Run("refuses min-links exceeding member count", func(t *testing.T) {
		t.Parallel()
		cfg := lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {
					MinLinks: 3,
				},
			},
		}
		if err := cfg.Validate(layer.Env{Ports: tbl}); err == nil {
			t.Fatal("Validate succeeded with MinLinks 3 on 2 members, want error")
		}
	})

	t.Run("refuses negative delays", func(t *testing.T) {
		t.Parallel()
		cfg := lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {
					UpDelay: -1 * time.Second,
				},
			},
		}
		if err := cfg.Validate(layer.Env{Ports: tbl}); err == nil {
			t.Fatal("Validate succeeded with negative UpDelay, want error")
		}

		cfg2 := lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {
					DownDelay: -1 * time.Second,
				},
			},
		}
		if err := cfg2.Validate(layer.Env{Ports: tbl}); err == nil {
			t.Fatal("Validate succeeded with negative DownDelay, want error")
		}
	})

	t.Run("refuses negative rebalance interval", func(t *testing.T) {
		t.Parallel()
		interval := -1 * time.Second
		cfg := lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {RebalanceInterval: &interval},
			},
		}
		err := cfg.Validate(layer.Env{Ports: tbl})
		if err == nil {
			t.Fatal("Validate succeeded with negative RebalanceInterval, want error")
		}
		if got := errs.Attributes(err)["field"]; got != "lags.lag1.rebalance_interval" {
			t.Errorf("field = %v, want lags.lag1.rebalance_interval", got)
		}
	})

	t.Run("refuses unknown modes", func(t *testing.T) {
		t.Parallel()
		cfg := lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {
					Mode: "UnknownMode",
				},
			},
		}
		if err := cfg.Validate(layer.Env{Ports: tbl}); err == nil {
			t.Fatal("Validate succeeded with unknown mode, want error")
		}

		cfg2 := lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {
					LACP: lag.LACPConfig{Mode: "UnknownLACPMode"},
				},
			},
		}
		if err := cfg2.Validate(layer.Env{Ports: tbl}); err == nil {
			t.Fatal("Validate succeeded with unknown LACP mode, want error")
		}
	})

	t.Run("refuses group LACP system ID", func(t *testing.T) {
		t.Parallel()
		cfg := lag.Config{LAGs: map[string]lag.LAG{
			"lag1": {
				Mode: lag.ActiveBackup,
				LACP: lag.LACPConfig{Mode: lag.Off, SystemID: netaddr.MAC{0x01, 0, 0, 0, 0, 1}},
			},
		}}
		err := cfg.Validate(layer.Env{Ports: tbl})
		if err == nil {
			t.Fatal("Validate() = nil, want error")
		}
		if got := errs.Attributes(err)["field"]; got != "lags.lag1.lacp.system_id" {
			t.Errorf("field = %v, want lags.lag1.lacp.system_id", got)
		}
	})
}

func TestDefaults(t *testing.T) {
	t.Parallel()

	tbl, err := port.NewBuilder().
		Add(port.Port{Name: "lag2", Kind: port.LAG}).
		Add(port.Port{Name: "lag1", Kind: port.LAG}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1"}).
		Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	sysMAC := mustMAC(t, "02:00:00:00:00:aa")
	cfg := lag.Config{}.Normalize(layer.Env{Ports: tbl, MAC: sysMAC})

	l1, ok := cfg.LAGs["lag1"]
	if !ok {
		t.Fatal("lag1 not found in defaulted config")
	}
	if l1.LACP.Key != 1 {
		t.Errorf("lag1 key = %d, want 1", l1.LACP.Key)
	}
	// Normalize leaves Primary unset so active-backup keeps the last active member.
	if l1.Primary != "" {
		t.Errorf("lag1 primary = %q, want unset", l1.Primary)
	}
	if l1.RebalanceInterval == nil || *l1.RebalanceInterval != 10*time.Second {
		t.Errorf("lag1 rebalance interval = %v, want %v", l1.RebalanceInterval, 10*time.Second)
	}
	if l1.LACP.SystemPriority != 32768 {
		t.Errorf("lag1 system priority = %d, want 32768", l1.LACP.SystemPriority)
	}
	if l1.LACP.SystemID != sysMAC {
		t.Errorf("lag1 system ID = %v, want %v", l1.LACP.SystemID, sysMAC)
	}
	m1 := l1.Members["1/1/1"]
	if m1.Priority != 32768 {
		t.Errorf("member 1/1/1 priority = %d, want 32768", m1.Priority)
	}
	if m1.Key != 1 {
		t.Errorf("member 1/1/1 key = %d, want 1", m1.Key)
	}

	l2, ok := cfg.LAGs["lag2"]
	if !ok {
		t.Fatal("lag2 not found in defaulted config")
	}
	if l2.LACP.Key != 2 {
		t.Errorf("lag2 key = %d, want 2", l2.LACP.Key)
	}
}

func TestDiff(t *testing.T) {
	t.Parallel()

	a := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode: lag.ActiveBackup,
				Members: map[string]lag.Member{
					"1/1/1": {Priority: 32768},
				},
			},
		},
	}

	b := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode: lag.BalanceTCP,
				Members: map[string]lag.Member{
					"1/1/1": {Priority: 100},
				},
			},
		},
	}

	changes := lag.Diff(a, b)

	findChange := func(kind, key, field string) (any, any, bool) {
		for _, c := range changes {
			if c.Subject.Kind == kind && c.Subject.Key == key && c.Field == field {
				return c.From, c.To, true
			}
		}

		return nil, nil, false
	}

	if from, to, ok := findChange("lag", "lag1", "mode"); !ok || from != lag.ActiveBackup || to != lag.BalanceTCP {
		t.Errorf("mode change: got (%v, %v, %v), want (%v, %v, true)", from, to, ok, lag.ActiveBackup, lag.BalanceTCP)
	}

	memberKey := trace.CompositeKey("lag1", "1/1/1")
	from, to, ok := findChange("port", memberKey, "priority")
	fromFact, okFrom := from.(trace.Fact)
	toFact, okTo := to.(trace.Fact)
	if !ok || !okFrom || !okTo || fromFact.Canonical() != strconv.Itoa(32768) || toFact.Canonical() != "100" {
		t.Errorf("priority change: got (%v, %v, %v), want (32768, 100, true)", from, to, ok)
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	cfg := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Members: map[string]lag.Member{
					"1/1/1": {},
				},
			},
		},
	}
	norm := cfg.Normalize(layer.Env{Ports: lagPortTable(t), MAC: mustMAC(t, "02:00:00:00:00:aa")})
	l := norm.LAGs["lag1"]
	if l.Mode != lag.ActiveBackup {
		t.Errorf("Mode: got %v, want %v", l.Mode, lag.ActiveBackup)
	}
	if l.LACP.Mode != lag.Off {
		t.Errorf("LACP.Mode: got %v, want %v", l.LACP.Mode, lag.Off)
	}
	if l.LACP.SystemPriority != 32768 {
		t.Errorf("LACP.SystemPriority: got %d, want 32768", l.LACP.SystemPriority)
	}
	m := l.Members["1/1/1"]
	if m.Priority != 32768 {
		t.Errorf("Member Priority: got %d, want 32768", m.Priority)
	}
}

// TestRebalanceIntervalNormalization is evidence for the four normalization
// cases named in the decisions: nil fills the default, zero disables the
// signal and stays zero, a value under one second is raised to it, and a
// value at or above one second is kept.
func TestRebalanceIntervalNormalization(t *testing.T) {
	t.Parallel()

	tbl := lagPortTable(t)
	mac := mustMAC(t, "02:00:00:00:00:aa")

	normalize := func(interval *time.Duration) *time.Duration {
		cfg := lag.Config{LAGs: map[string]lag.LAG{"lag1": {RebalanceInterval: interval}}}
		return cfg.Normalize(layer.Env{Ports: tbl, MAC: mac}).LAGs["lag1"].RebalanceInterval
	}

	zero := time.Duration(0)
	below := 500 * time.Millisecond
	above := 20 * time.Second

	cases := []struct {
		name string
		in   *time.Duration
		want time.Duration
	}{
		{name: "nil fills the default", in: nil, want: 10 * time.Second},
		{name: "zero disables and stays zero", in: &zero, want: 0},
		{name: "below the minimum is raised to it", in: &below, want: time.Second},
		{name: "at or above the minimum is kept", in: &above, want: above},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := normalize(tc.in)
			if got == nil || *got != tc.want {
				t.Errorf("RebalanceInterval = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDiffCoversRebalanceInterval(t *testing.T) {
	t.Parallel()

	zero := time.Duration(0)
	five := 5 * time.Second

	// Nil and an explicit zero are both "unset or disabled" in Canonical form
	// but distinguished as Set/unset; only a differing explicit value changes.
	unset := lag.Config{LAGs: map[string]lag.LAG{"lag1": {}}}
	explicitZero := lag.Config{LAGs: map[string]lag.LAG{"lag1": {RebalanceInterval: &zero}}}
	explicitFive := lag.Config{LAGs: map[string]lag.LAG{"lag1": {RebalanceInterval: &five}}}

	if changes := lag.Diff(explicitZero, explicitZero); len(changes) != 0 {
		t.Errorf("Diff(explicitZero, explicitZero) = %+v, want no changes", changes)
	}

	changes := lag.Diff(unset, explicitFive)
	found := false
	for _, c := range changes {
		if c.Subject.Kind == "lag" && c.Subject.Key == "lag1" && c.Field == "rebalance_interval" {
			found = true
		}
	}
	if !found {
		t.Errorf("Diff(unset, explicitFive) = %+v, want a rebalance_interval change", changes)
	}
}

// TestVSwitchStoresEffectiveLAGConfiguration proves a package outside lag and
// mcast can name every exported Config field and construct a value: it builds
// the effective LAG configuration through vswitch.Config alone and checks it
// against lag.Config{}.Defaults directly.
func TestVSwitchStoresEffectiveLAGConfiguration(t *testing.T) {
	t.Parallel()

	ports := lagPortTable(t)
	systemID := mustMAC(t, "02:00:00:00:00:aa")
	omitted := vswitch.Config{MAC: systemID, Ports: ports}
	effectiveLAG := lag.Config{}.Normalize(layer.Env{Ports: ports, MAC: systemID})
	explicit := vswitch.Config{MAC: systemID, Ports: ports, LAG: &effectiveLAG}

	omittedNorm := omitted.Normalize()
	if omittedNorm.LAG == nil {
		t.Fatal("Normalize().LAG = nil, want effective LAG configuration")
	}
	if changes := vswitch.Diff(omittedNorm, explicit.Normalize()); len(changes) != 0 {
		t.Errorf("Diff(omitted, explicit) = %+v, want no changes", changes)
	}

	sw, err := vswitch.New(omitted)
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}
	if changes := vswitch.Diff(sw.Config(), explicit.Normalize()); len(changes) != 0 {
		t.Errorf("Diff(Switch.Config(), explicit) = %+v, want no changes", changes)
	}
	if changes := vswitch.Diff(sw.Spec().Config, explicit.Normalize()); len(changes) != 0 {
		t.Errorf("Diff(Switch.Spec().Config, explicit) = %+v, want no changes", changes)
	}
}

func TestDiffOmittedModeMatchesActiveBackup(t *testing.T) {
	t.Parallel()

	a := lag.Config{LAGs: map[string]lag.LAG{"lag1": {Mode: ""}}}
	b := lag.Config{LAGs: map[string]lag.LAG{"lag1": {Mode: lag.ActiveBackup}}}
	if diffs := lag.Diff(a, b); len(diffs) != 0 {
		t.Fatalf("Diff reported changes between omitted mode and ActiveBackup: %v", diffs)
	}
}

func TestLAGSnapshotFactIsLosslessAndImmutable(t *testing.T) {
	t.Parallel()

	membersA := map[string]lag.Member{"1/1/1": {Priority: 1}}
	membersB := map[string]lag.Member{"1/1/2": {Priority: 1}}
	a := lag.Config{LAGs: map[string]lag.LAG{"lag1": {Mode: lag.ActiveBackup, LACP: lag.LACPConfig{Mode: lag.Off}, Members: membersA}}}
	b := lag.Config{LAGs: map[string]lag.LAG{"lag1": {Mode: lag.ActiveBackup, LACP: lag.LACPConfig{Mode: lag.Off}, Members: membersB}}}

	factA := lag.Diff(lag.Config{}, a)[0].To
	factB := lag.Diff(lag.Config{}, b)[0].To
	if factA.TypeID() != "lag.lag" {
		t.Errorf("TypeID() = %q, want lag.lag", factA.TypeID())
	}
	if factA.Canonical() == factB.Canonical() {
		t.Errorf("different LAGs share canonical form %q", factA.Canonical())
	}
	before := factA.Canonical()
	membersA["1/1/3"] = lag.Member{Priority: 1}
	if got := factA.Canonical(); got != before {
		t.Errorf("fact changed after source mutation: got %q, want %q", got, before)
	}
}
