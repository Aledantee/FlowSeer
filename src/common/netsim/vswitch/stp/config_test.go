package stp_test

import (
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

func TestDefaultPathCost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		speedBPS uint64
		want     uint32
	}{
		{name: "100 Gbps", speedBPS: 100_000_000_000, want: 200},
		{name: "400 Gbps", speedBPS: 400_000_000_000, want: 200},
		{name: "40 Gbps", speedBPS: 40_000_000_000, want: 2_000},
		{name: "10 Gbps", speedBPS: 10_000_000_000, want: 2_000},
		{name: "2.5 Gbps", speedBPS: 2_500_000_000, want: 20_000},
		{name: "1 Gbps", speedBPS: 1_000_000_000, want: 20_000},
		{name: "100 Mbps", speedBPS: 100_000_000, want: 200_000},
		{name: "10 Mbps", speedBPS: 10_000_000, want: 2_000_000},
		{name: "unknown speed", speedBPS: 0, want: 20_000},
		{name: "sub-10 Mbps", speedBPS: 1_000_000, want: 20_000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := stp.DefaultPathCost(tc.speedBPS)
			if got != tc.want {
				t.Errorf("DefaultPathCost(%d) = %d, want %d", tc.speedBPS, got, tc.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	validMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	tbl, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
		Add(port.Port{Name: "lag1", Kind: port.Lag}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, LagParent: "lag1"}).
		Build()
	if err != nil {
		t.Fatalf("port.Builder.Build: %v", err)
	}

	tests := []struct {
		name    string
		cfg     stp.Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: stp.Config{
				Priority: 32768,
				Address:  validMAC,
				Ports: map[string]stp.Port{
					"1/1/1": {Priority: 128},
					"lag1":  {Priority: 128},
				},
			},
			wantErr: false,
		},
		{
			name: "zero address accepted",
			cfg: stp.Config{
				Priority: 32768,
				Address:  netaddr.MAC{},
				Ports: map[string]stp.Port{
					"1/1/1": {Priority: 128},
				},
			},
			wantErr: false,
		},
		{
			name: "group address rejected",
			cfg: stp.Config{
				Priority: 32768,
				Address:  netaddr.MAC{0x01, 0, 0, 0, 0, 1},
				Ports:    map[string]stp.Port{"1/1/1": {Priority: 128}},
			},
			wantErr: true,
		},
		{
			name: "non-multiple of 4096 priority rejected",
			cfg: stp.Config{
				Priority: 32767,
				Address:  validMAC,
				Ports: map[string]stp.Port{
					"1/1/1": {Priority: 128},
				},
			},
			wantErr: true,
		},
		{
			name: "hello time above ten seconds rejected",
			cfg: stp.Config{
				Priority:  32768,
				Address:   validMAC,
				HelloTime: 11 * time.Second,
				Ports:     map[string]stp.Port{"1/1/1": {}},
			},
			wantErr: true,
		},
		{
			name: "max age below six seconds rejected",
			cfg: stp.Config{
				Priority: 32768,
				Address:  validMAC,
				MaxAge:   5 * time.Second,
				Ports:    map[string]stp.Port{"1/1/1": {}},
			},
			wantErr: true,
		},
		{
			name: "forward delay above thirty seconds rejected",
			cfg: stp.Config{
				Priority:     32768,
				Address:      validMAC,
				ForwardDelay: 31 * time.Second,
				Ports:        map[string]stp.Port{"1/1/1": {}},
			},
			wantErr: true,
		},
		{
			name: "port absent from table rejected",
			cfg: stp.Config{
				Priority: 32768,
				Address:  validMAC,
				Ports: map[string]stp.Port{
					"1/1/99": {Priority: 128},
				},
			},
			wantErr: true,
		},
		{
			name: "lag member port rejected",
			cfg: stp.Config{
				Priority: 32768,
				Address:  validMAC,
				Ports: map[string]stp.Port{
					"1/1/3": {Priority: 128},
				},
			},
			wantErr: true,
		},
		{
			name: "tx hold count above ten rejected",
			cfg: stp.Config{
				Priority:    32768,
				Address:     validMAC,
				TxHoldCount: 11,
				Ports: map[string]stp.Port{
					"1/1/1": {Priority: 128},
				},
			},
			wantErr: true,
		},
		{
			name: "tx hold count ten accepted",
			cfg: stp.Config{
				Priority:    32768,
				Address:     validMAC,
				TxHoldCount: 10,
				Ports: map[string]stp.Port{
					"1/1/1": {Priority: 128},
				},
			},
			wantErr: false,
		},
		{
			name: "tx hold count zero accepted",
			cfg: stp.Config{
				Priority:    32768,
				Address:     validMAC,
				TxHoldCount: 0,
				Ports: map[string]stp.Port{
					"1/1/1": {Priority: 128},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid point to point rejected",
			cfg: stp.Config{
				Priority: 32768,
				Address:  validMAC,
				Ports: map[string]stp.Port{
					"1/1/1": {PointToPoint: "InvalidMode"},
				},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.Validate(tbl)
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestNewRejectsPathCostAboveMaximum(t *testing.T) {
	t.Parallel()

	tbl, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Build()
	if err != nil {
		t.Fatalf("port.Builder.Build: %v", err)
	}

	cfg := stp.Config{
		Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Ports: map[string]stp.Port{
			"1/1/1": {PathCost: 200_000_001},
		},
	}
	_, err = stp.New(cfg, tbl)
	if err == nil {
		t.Fatal("New() error = nil, want path cost rejection")
	}
	if got, want := errs.Attributes(err)["field"], "ports.1/1/1.path_cost"; got != want {
		t.Errorf("field = %v, want %q", got, want)
	}

	cfg.Ports["1/1/1"] = stp.Port{PathCost: stp.MaxPathCost}
	if _, err := stp.New(cfg, tbl); err != nil {
		t.Errorf("New() at maximum path cost: %v", err)
	}
}

func TestValidateUsesEffectiveTimerRelations(t *testing.T) {
	t.Parallel()

	tbl, err := port.NewBuilder().Build()
	if err != nil {
		t.Fatalf("port.Builder.Build: %v", err)
	}
	validMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	for _, test := range []struct {
		name    string
		cfg     stp.Config
		wantErr bool
	}{
		{name: "all defaults", cfg: stp.Config{Address: validMAC}},
		{
			name: "both equality boundaries",
			cfg:  stp.Config{Address: validMAC, HelloTime: 2 * time.Second, MaxAge: 6 * time.Second, ForwardDelay: 4 * time.Second},
		},
		{
			name:    "minimum max age violated",
			cfg:     stp.Config{Address: validMAC, HelloTime: 3 * time.Second, MaxAge: 6 * time.Second, ForwardDelay: 4 * time.Second},
			wantErr: true,
		},
		{
			name:    "maximum max age violated",
			cfg:     stp.Config{Address: validMAC, HelloTime: time.Second, MaxAge: 7 * time.Second, ForwardDelay: 4 * time.Second},
			wantErr: true,
		},
		{
			name:    "explicit hello with default max age",
			cfg:     stp.Config{Address: validMAC, HelloTime: 10 * time.Second},
			wantErr: true,
		},
		{
			name:    "explicit max age with default forward delay",
			cfg:     stp.Config{Address: validMAC, MaxAge: 40 * time.Second},
			wantErr: true,
		},
		{
			name: "explicit max and forward with default hello at equality",
			cfg:  stp.Config{Address: validMAC, MaxAge: 20 * time.Second, ForwardDelay: 11 * time.Second},
		},
		{
			name: "explicit hello and max with default forward delay at equality",
			cfg:  stp.Config{Address: validMAC, HelloTime: 2 * time.Second, MaxAge: 6 * time.Second},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.cfg.Validate(tbl)
			if (err != nil) != test.wantErr {
				t.Errorf("Validate() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestClone(t *testing.T) {
	t.Parallel()

	cfg := stp.Config{
		Priority: 32768,
		Ports: map[string]stp.Port{
			"1/1/1": {Priority: 128},
		},
	}
	cloned := cfg.Clone()
	cloned.Ports["1/1/1"] = stp.Port{Priority: 64}

	if cfg.Ports["1/1/1"].Priority != 128 {
		t.Errorf("original port priority modified: got %d, want 128", cfg.Ports["1/1/1"].Priority)
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	cfg := stp.Config{
		Ports: map[string]stp.Port{
			"1/1/1": {},
		},
	}
	norm := cfg.Normalize()

	if norm.Priority != stp.DefaultBridgePriority {
		t.Errorf("Priority: got %d, want %d", norm.Priority, stp.DefaultBridgePriority)
	}
	if norm.HelloTime != stp.DefaultHelloTime {
		t.Errorf("HelloTime: got %v, want %v", norm.HelloTime, stp.DefaultHelloTime)
	}
	if norm.MaxAge != stp.DefaultMaxAge {
		t.Errorf("MaxAge: got %v, want %v", norm.MaxAge, stp.DefaultMaxAge)
	}
	if norm.ForwardDelay != stp.DefaultForwardDelay {
		t.Errorf("ForwardDelay: got %v, want %v", norm.ForwardDelay, stp.DefaultForwardDelay)
	}
	if norm.TxHoldCount != stp.DefaultTxHoldCount {
		t.Errorf("TxHoldCount: got %d, want %d", norm.TxHoldCount, stp.DefaultTxHoldCount)
	}
	p := norm.Ports["1/1/1"]
	if p.Priority != stp.DefaultPortPriority {
		t.Errorf("port Priority: got %d, want %d", p.Priority, stp.DefaultPortPriority)
	}
	if p.PointToPoint != stp.PointToPointAuto {
		t.Errorf("port PointToPoint: got %v, want %v", p.PointToPoint, stp.PointToPointAuto)
	}
}

func TestNormalizePreservesExplicitZeroPriorities(t *testing.T) {
	cfg := stp.Config{
		Priority:        0,
		PriorityPresent: true,
		Ports: map[string]stp.Port{
			"1/1/1": {Priority: 0, PriorityPresent: true},
		},
	}

	norm := cfg.Normalize()
	if norm.Priority != 0 {
		t.Errorf("bridge priority = %d, want explicit zero", norm.Priority)
	}
	if got := norm.Ports["1/1/1"].Priority; got != 0 {
		t.Errorf("port priority = %d, want explicit zero", got)
	}
}

func TestPortFactCanonicalUsesEffectiveElectionSettings(t *testing.T) {
	t.Parallel()

	defaulted := stp.Port{}
	explicitDefaults := stp.Port{
		Priority:        stp.DefaultPortPriority,
		PriorityPresent: true,
		PointToPoint:    stp.PointToPointAuto,
	}
	explicitZero := stp.Port{
		Priority:        0,
		PriorityPresent: true,
	}

	if !trace.EqualFact(defaulted, explicitDefaults) {
		t.Errorf("defaulted fact = %q, want effective defaults %q", defaulted.Canonical(), explicitDefaults.Canonical())
	}
	if trace.EqualFact(explicitZero, defaulted) {
		t.Errorf("explicit-zero fact = %q, want distinct from omitted priority", explicitZero.Canonical())
	}
	if !trace.EqualFact(stp.PointToPointMode(""), stp.PointToPointAuto) {
		t.Errorf(
			"unspecified point-to-point fact = %q, want %q",
			stp.PointToPointMode("").Canonical(),
			stp.PointToPointAuto.Canonical(),
		)
	}
}

func TestBridgeID(t *testing.T) {
	t.Parallel()

	mac1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	mac2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	b1 := stp.BridgeID{Priority: 4096, Address: mac1}
	b2 := stp.BridgeID{Priority: 8192, Address: mac1}
	b3 := stp.BridgeID{Priority: 4096, Address: mac2}

	if !b1.Less(b2) {
		t.Error("b1 should be Less than b2 by priority")
	}
	if b2.Less(b1) {
		t.Error("b2 should not be Less than b1")
	}
	if !b1.Less(b3) {
		t.Error("b1 should be Less than b3 by MAC address")
	}
	if b3.Less(b1) {
		t.Error("b3 should not be Less than b1")
	}

	if got := b1.String(); got != "4096/00:11:22:33:44:01" {
		t.Errorf("b1.String() = %q, want %q", got, "4096/00:11:22:33:44:01")
	}
}

func TestDiff(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	a := stp.Config{
		Priority:     32768,
		Address:      mac,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
		Ports: map[string]stp.Port{
			"1/1/1": {Priority: 128, PathCost: 20000, AdminEdge: false, PointToPoint: stp.PointToPointAuto},
			"1/1/2": {Priority: 128, PathCost: 20000},
		},
	}

	b := stp.Config{
		Priority:     4096,
		Address:      mac,
		HelloTime:    1 * time.Second,
		MaxAge:       10 * time.Second,
		ForwardDelay: 7 * time.Second,
		TxHoldCount:  4,
		Ports: map[string]stp.Port{
			"1/1/1": {Priority: 64, PathCost: 2000, AdminEdge: true, AutoEdge: true, PointToPoint: stp.PointToPointForceTrue},
			"1/1/3": {Priority: 128, PathCost: 20000},
		},
	}

	changes := stp.Diff(a, b)

	findChange := func(kind, key, field string) (any, any, bool) {
		for _, c := range changes {
			if c.Subject.Kind == kind && c.Subject.Key == key && c.Field == field {
				return c.From, c.To, true
			}
		}
		return nil, nil, false
	}

	if from, to, ok := findChange("bridge", "", "priority"); !ok || from != stp.PriorityFact(32768) || to != stp.PriorityFact(4096) {
		t.Errorf("priority change: got (%v, %v, %v), want (32768, 4096, true)", from, to, ok)
	}
	if from, to, ok := findChange("bridge", "", "hello_time"); !ok || from != stp.DurationFact(2*time.Second) || to != stp.DurationFact(1*time.Second) {
		t.Errorf("hello_time change: got (%v, %v, %v)", from, to, ok)
	}
	if from, to, ok := findChange("bridge", "", "max_age"); !ok || from != stp.DurationFact(20*time.Second) || to != stp.DurationFact(10*time.Second) {
		t.Errorf("max_age change: got (%v, %v, %v)", from, to, ok)
	}
	if from, to, ok := findChange("bridge", "", "forward_delay"); !ok || from != stp.DurationFact(15*time.Second) || to != stp.DurationFact(7*time.Second) {
		t.Errorf("forward_delay change: got (%v, %v, %v)", from, to, ok)
	}
	if from, to, ok := findChange("bridge", "", "tx_hold_count"); !ok || from != stp.TxHoldCountFact(6) || to != stp.TxHoldCountFact(4) {
		t.Errorf("tx_hold_count change: got (%v, %v, %v), want (6, 4, true): the default is what an unset count means", from, to, ok)
	}

	otherMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x66}
	moved := b
	moved.Address = otherMAC
	if from, to, ok := findAddress(stp.Diff(b, moved)); !ok || from != stp.MACFact(mac) || to != stp.MACFact(otherMAC) {
		t.Errorf("address change: got (%v, %v, %v), want (%v, %v, true)", from, to, ok, mac, otherMAC)
	}

	if from, to, ok := findChange("port", "1/1/1", "priority"); !ok || from != stp.PortPriorityFact(128) || to != stp.PortPriorityFact(64) {
		t.Errorf("port priority change: got (%v, %v, %v)", from, to, ok)
	}
	if from, to, ok := findChange("port", "1/1/1", "admin_path_cost"); !ok || from != stp.PathCostFact(20000) || to != stp.PathCostFact(2000) {
		t.Errorf("port admin_path_cost change: got (%v, %v, %v)", from, to, ok)
	}
	if from, to, ok := findChange("port", "1/1/1", "admin_edge"); !ok || from != stp.BoolFact(false) || to != stp.BoolFact(true) {
		t.Errorf("port admin_edge change: got (%v, %v, %v)", from, to, ok)
	}
	if from, to, ok := findChange("port", "1/1/1", "admin_point_to_point"); !ok || from != stp.PointToPointAuto || to != stp.PointToPointForceTrue {
		t.Errorf("port admin_point_to_point change: got (%v, %v, %v)", from, to, ok)
	}
	if from, to, ok := findChange("port", "1/1/1", "auto_edge"); !ok || from != stp.BoolFact(false) || to != stp.BoolFact(true) {
		t.Errorf("port auto_edge change: got (%v, %v, %v)", from, to, ok)
	}

	// 1/1/2 removed
	if from, to, ok := findChange("port", "1/1/2", ""); !ok || from == nil || to != nil {
		t.Errorf("port 1/1/2 removed: got (%v, %v, %v)", from, to, ok)
	}
	// 1/1/3 added
	if from, to, ok := findChange("port", "1/1/3", ""); !ok || from != nil || to == nil {
		t.Errorf("port 1/1/3 added: got (%v, %v, %v)", from, to, ok)
	}
}

func findAddress(changes []trace.Change) (any, any, bool) {
	for _, c := range changes {
		if c.Subject.Kind == "bridge" && c.Field == "address" {
			return c.From, c.To, true
		}
	}
	return nil, nil, false
}

func TestValidateRefusesContradictoryGuardCombinations(t *testing.T) {
	t.Parallel()

	tbl, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Build()
	if err != nil {
		t.Fatalf("port.Builder.Build: %v", err)
	}

	tests := []struct {
		name string
		p    stp.Port
	}{
		{name: "loop guard with restricted role", p: stp.Port{LoopGuard: true, RestrictedRole: true}},
		{name: "loop guard with admin edge", p: stp.Port{LoopGuard: true, AdminEdge: true}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := stp.Config{
				Priority: 32768,
				Ports:    map[string]stp.Port{"1/1/1": tc.p},
			}
			err := cfg.Validate(tbl)
			if err == nil {
				t.Fatalf("Validate() = nil, want a rejection")
			}
			if got, want := errs.Attributes(err)["field"], "ports.1/1/1.loop_guard"; got != want {
				t.Errorf("field attribute = %v, want %q", got, want)
			}
		})
	}
}

func TestValidateAcceptsEachGuardAlone(t *testing.T) {
	t.Parallel()

	tbl, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Build()
	if err != nil {
		t.Fatalf("port.Builder.Build: %v", err)
	}

	guards := map[string]stp.Port{
		"bpdu guard":      {BPDUGuard: true},
		"restricted role": {RestrictedRole: true},
		"restricted tcn":  {RestrictedTCN: true},
		"loop guard":      {LoopGuard: true},
	}

	for name, p := range guards {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := stp.Config{
				Priority: 32768,
				Ports:    map[string]stp.Port{"1/1/1": p},
			}
			if err := cfg.Validate(tbl); err != nil {
				t.Errorf("Validate() = %v, want acceptance", err)
			}
		})
	}
}

func TestNormalizeLeavesGuardsUntouched(t *testing.T) {
	t.Parallel()

	// Every guard defaults to off, so an unset guard has nothing to fill in and
	// a set one has nothing to override.
	cfg := stp.Config{
		Ports: map[string]stp.Port{
			"1/1/1": {},
			"1/1/2": {BPDUGuard: true, RestrictedRole: true, RestrictedTCN: true},
		},
	}

	norm := cfg.Normalize()

	if p := norm.Ports["1/1/1"]; p.BPDUGuard || p.RestrictedRole || p.RestrictedTCN || p.LoopGuard {
		t.Errorf("unset guards normalized to %+v, want all off", p)
	}
	if p := norm.Ports["1/1/2"]; !p.BPDUGuard || !p.RestrictedRole || !p.RestrictedTCN || p.LoopGuard {
		t.Errorf("set guards normalized to %+v, want the three set ones kept and loop guard off", p)
	}
}

func TestCloneCopiesGuards(t *testing.T) {
	t.Parallel()

	cfg := stp.Config{
		Ports: map[string]stp.Port{"1/1/1": {LoopGuard: true}},
	}
	cloned := cfg.Clone()
	cloned.Ports["1/1/1"] = stp.Port{}

	if !cfg.Ports["1/1/1"].LoopGuard {
		t.Error("original loop guard cleared through the clone")
	}
}

func TestPortFactCanonicalDistinguishesGuards(t *testing.T) {
	t.Parallel()

	plain := stp.Port{}
	guards := map[string]stp.Port{
		"bpdu guard":      {BPDUGuard: true},
		"restricted role": {RestrictedRole: true},
		"restricted tcn":  {RestrictedTCN: true},
		"loop guard":      {LoopGuard: true},
	}

	for name, p := range guards {
		if trace.EqualFact(p, plain) {
			t.Errorf("%s fact = %q, want distinct from an unguarded port", name, p.Canonical())
		}
	}
}

func TestDiffReportsOneChangePerGuardField(t *testing.T) {
	t.Parallel()

	base := stp.Config{
		Priority: 32768,
		Ports:    map[string]stp.Port{"1/1/1": {}},
	}
	guarded := stp.Config{
		Priority: 32768,
		Ports:    map[string]stp.Port{"1/1/1": {LoopGuard: true}},
	}

	changes := stp.Diff(base, guarded)
	if len(changes) != 1 {
		t.Fatalf("Diff() = %d changes, want exactly one: %+v", len(changes), changes)
	}
	c := changes[0]
	if c.Subject.Kind != "port" || c.Subject.Key != "1/1/1" || c.Field != "loop_guard" {
		t.Errorf("change subject/field = %v/%q, want port/1/1/1 loop_guard", c.Subject, c.Field)
	}
	if c.From != stp.BoolFact(false) || c.To != stp.BoolFact(true) {
		t.Errorf("change = (%v, %v), want (false, true)", c.From, c.To)
	}

	all := stp.Config{
		Priority: 32768,
		Ports: map[string]stp.Port{
			"1/1/1": {BPDUGuard: true, RestrictedRole: true, RestrictedTCN: true},
		},
	}
	fields := map[string]bool{}
	for _, c := range stp.Diff(base, all) {
		fields[c.Field] = true
	}
	for _, want := range []string{"bpdu_guard", "restricted_role", "restricted_tcn"} {
		if !fields[want] {
			t.Errorf("Diff() reported no change on field %q", want)
		}
	}
	if fields["loop_guard"] {
		t.Error("Diff() reported a loop_guard change where neither side sets it")
	}
}

func TestNormalizeLeavesConfigWithoutMSTUnchanged(t *testing.T) {
	t.Parallel()

	cfg := stp.Config{
		Priority:        32768,
		PriorityPresent: true,
		HelloTime:       stp.DefaultHelloTime,
		MaxAge:          stp.DefaultMaxAge,
		ForwardDelay:    stp.DefaultForwardDelay,
		TxHoldCount:     stp.DefaultTxHoldCount,
		Ports: map[string]stp.Port{
			"1/1/1": {Priority: stp.DefaultPortPriority, PriorityPresent: true, PointToPoint: stp.PointToPointAuto},
		},
	}

	norm := cfg.Normalize()
	if norm.MST != nil {
		t.Fatalf("MST = %+v, want nil", norm.MST)
	}
	if !reflect.DeepEqual(cfg, norm) {
		t.Errorf("Normalize() changed a config that already held effective defaults: got %+v, want %+v", norm, cfg)
	}
}

func TestDiffMSTRegionRevision(t *testing.T) {
	t.Parallel()

	a := stp.Config{MST: &stp.MST{Name: "region-1", Revision: 1}}
	b := stp.Config{MST: &stp.MST{Name: "region-1", Revision: 2}}

	changes := stp.Diff(a, b)

	var found int
	for _, c := range changes {
		if c.Field == "mst.revision" {
			found++
			if c.From != stp.MSTRevisionFact(1) || c.To != stp.MSTRevisionFact(2) {
				t.Errorf("mst.revision change = (%v, %v), want (1, 2)", c.From, c.To)
			}
		}
	}
	if found != 1 {
		t.Fatalf("Diff() reported %d changes on mst.revision, want exactly 1: %+v", found, changes)
	}
}

func TestDiffMSTVLANMoveBetweenInstances(t *testing.T) {
	t.Parallel()

	a := stp.Config{
		MST: &stp.MST{
			Instances: map[stp.MSTID]stp.Instance{
				1: {VLANs: []vlan.ID{10}},
				2: {VLANs: []vlan.ID{20}},
			},
		},
	}
	b := stp.Config{
		MST: &stp.MST{
			Instances: map[stp.MSTID]stp.Instance{
				1: {VLANs: []vlan.ID{10, 20}},
				2: {VLANs: []vlan.ID{}},
			},
		},
	}

	changes := stp.Diff(a, b)

	vlanFields := map[string]int{}
	for _, c := range changes {
		if c.Field == "vlans" {
			vlanFields[c.Subject.Key]++
		}
	}
	if vlanFields["1"] != 1 {
		t.Errorf("instance 1 vlans changes = %d, want 1", vlanFields["1"])
	}
	if vlanFields["2"] != 1 {
		t.Errorf("instance 2 vlans changes = %d, want 1", vlanFields["2"])
	}
}

func TestDiffPVSTTreePriority(t *testing.T) {
	t.Parallel()

	a := stp.Config{PVST: &stp.PVST{Trees: map[vlan.ID]stp.Tree{
		1:  {},
		10: {Priority: 4096, PriorityPresent: true},
	}}}
	b := stp.Config{PVST: &stp.PVST{Trees: map[vlan.ID]stp.Tree{
		1:  {},
		10: {Priority: 61440, PriorityPresent: true},
	}}}

	changes := stp.Diff(a, b)

	var found int
	for _, c := range changes {
		if c.Subject.Kind == "pvst_tree" && c.Subject.Key == "10" && c.Field == "priority" {
			found++
			if c.From != stp.PriorityFact(4096) || c.To != stp.PriorityFact(61440) {
				t.Errorf("pvst tree 10 priority change = (%v, %v), want (4096, 61440)", c.From, c.To)
			}
		}
	}
	if found != 1 {
		t.Fatalf("Diff() reported %d changes on pvst tree 10 priority, want exactly 1: %+v", found, changes)
	}
}

func TestDiffPVSTPathCostMovesPorts(t *testing.T) {
	t.Parallel()

	a := stp.Config{
		Ports: map[string]stp.Port{"l1": {}, "l2": {}},
		PVST: &stp.PVST{Trees: map[vlan.ID]stp.Tree{
			1:  {},
			10: {Ports: map[string]stp.InstancePort{"l1": {PathCost: 2_000_000}}},
		}},
	}
	b := stp.Config{
		Ports: map[string]stp.Port{"l1": {}, "l2": {}},
		PVST: &stp.PVST{Trees: map[vlan.ID]stp.Tree{
			1:  {},
			10: {Ports: map[string]stp.InstancePort{"l2": {PathCost: 2_000_000}}},
		}},
	}

	changes := stp.Diff(a, b)
	if len(changes) == 0 {
		t.Fatal("Diff() reported no changes when a PVST path cost moved from one port to another")
	}
}

func TestDiffPVSTRemovedEntirely(t *testing.T) {
	t.Parallel()

	a := stp.Config{PVST: &stp.PVST{Trees: map[vlan.ID]stp.Tree{1: {}, 10: {}}}}
	b := stp.Config{}

	changes := stp.Diff(a, b)

	var found int
	for _, c := range changes {
		if c.Field == "pvst" {
			found++
			if c.From != stp.BoolFact(true) || c.To != stp.BoolFact(false) {
				t.Errorf("pvst change = (%v, %v), want (true, false)", c.From, c.To)
			}
		}
	}
	if found != 1 {
		t.Fatalf("Diff() reported %d changes on pvst removal, want exactly 1: %+v", found, changes)
	}
}

func TestValidateRefusesMSTAndPVSTTogether(t *testing.T) {
	t.Parallel()

	tbl, err := port.NewBuilder().Build()
	if err != nil {
		t.Fatalf("port.Builder.Build: %v", err)
	}

	cfg := stp.Config{
		Priority: 32768,
		MST:      &stp.MST{},
		PVST:     &stp.PVST{},
	}

	err = cfg.Validate(tbl)
	if err == nil {
		t.Fatal("Validate() = nil, want rejection of MST and PVST both set")
	}
	if got, want := errs.Attributes(err)["field"], "pvst"; got != want {
		t.Errorf("field attribute = %v, want %q", got, want)
	}
}

func TestPVSTValidate(t *testing.T) {
	t.Parallel()

	tbl, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
		Build()
	if err != nil {
		t.Fatalf("port.Builder.Build: %v", err)
	}
	// "1/1/2" is in the port table but never added as an STP port, so a tree
	// may reference it in the port table check but not the STP-port-set check.
	stpPorts := map[string]stp.Port{"1/1/1": {}}

	tests := []struct {
		name      string
		pvst      stp.PVST
		wantErr   bool
		wantField string
	}{
		{
			name: "valid trees",
			pvst: stp.PVST{
				Trees: map[vlan.ID]stp.Tree{
					1: {Priority: 4096, Ports: map[string]stp.InstancePort{"1/1/1": {}}},
				},
			},
			wantErr: false,
		},
		{
			name: "VID zero rejected",
			pvst: stp.PVST{
				Trees: map[vlan.ID]stp.Tree{0: {}},
			},
			wantErr:   true,
			wantField: "pvst.trees.0",
		},
		{
			name: "VID above 4094 rejected",
			pvst: stp.PVST{
				Trees: map[vlan.ID]stp.Tree{4095: {}},
			},
			wantErr:   true,
			wantField: "pvst.trees.4095",
		},
		{
			name: "priority not a multiple of 4096 rejected",
			pvst: stp.PVST{
				Trees: map[vlan.ID]stp.Tree{1: {Priority: 100}},
			},
			wantErr:   true,
			wantField: "pvst.trees.1.priority",
		},
		{
			name: "unknown tree port rejected",
			pvst: stp.PVST{
				Trees: map[vlan.ID]stp.Tree{
					1: {Priority: 4096, Ports: map[string]stp.InstancePort{"1/1/99": {}}},
				},
			},
			wantErr:   true,
			wantField: "pvst.trees.1.ports.1/1/99",
		},
		{
			name: "tree port absent from the STP port set rejected",
			pvst: stp.PVST{
				Trees: map[vlan.ID]stp.Tree{
					1: {Priority: 4096, Ports: map[string]stp.InstancePort{"1/1/2": {}}},
				},
			},
			wantErr:   true,
			wantField: "pvst.trees.1.ports.1/1/2",
		},
		{
			name: "tree port path cost over maximum rejected",
			pvst: stp.PVST{
				Trees: map[vlan.ID]stp.Tree{
					1: {
						Priority: 4096,
						Ports:    map[string]stp.InstancePort{"1/1/1": {PathCost: stp.MaxPathCost + 1}},
					},
				},
			},
			wantErr:   true,
			wantField: "pvst.trees.1.ports.1/1/1.path_cost",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.pvst.Validate(tbl, stpPorts)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, tc.wantErr)
			}
			if tc.wantErr {
				if got := errs.Attributes(err)["field"]; got != tc.wantField {
					t.Errorf("field attribute = %v, want %q", got, tc.wantField)
				}
			}
		})
	}
}

func TestPVSTNormalizeInsertsDefaultVLAN1(t *testing.T) {
	t.Parallel()

	empty := stp.PVST{}
	norm := empty.Normalize(stp.DefaultBridgePriority)

	tree, ok := norm.Trees[1]
	if !ok {
		t.Fatal("Normalize() on empty Trees did not insert a VLAN 1 tree")
	}
	if got := tree.Priority; got != stp.DefaultBridgePriority {
		t.Errorf("VLAN 1 tree priority = %d, want default %d", got, stp.DefaultBridgePriority)
	}
	if !tree.PriorityPresent {
		t.Error("VLAN 1 tree PriorityPresent = false, want true after normalization")
	}

	// A non-empty Trees map that still omits VLAN 1 must also get the
	// default tree inserted: newLayer builds VLAN 1's tree unconditionally
	// in PVST mode, so Normalize must name every tree the layer runs.
	nonEmpty := stp.PVST{Trees: map[vlan.ID]stp.Tree{10: {Priority: 4096, PriorityPresent: true}}}
	norm = nonEmpty.Normalize(stp.DefaultBridgePriority)
	if len(norm.Trees) != 2 {
		t.Fatalf("Normalize() on Trees missing VLAN 1 = %d trees, want 2 (VLAN 1 inserted alongside VLAN 10)", len(norm.Trees))
	}
	vlan1, ok := norm.Trees[1]
	if !ok {
		t.Fatal("Normalize() did not insert a VLAN 1 tree although Trees held only VLAN 10")
	}
	if got := vlan1.Priority; got != stp.DefaultBridgePriority {
		t.Errorf("inserted VLAN 1 tree priority = %d, want default %d", got, stp.DefaultBridgePriority)
	}
}

func TestPVSTNormalizeLeavesExplicitVLAN1Alone(t *testing.T) {
	t.Parallel()

	p := stp.PVST{Trees: map[vlan.ID]stp.Tree{1: {Priority: 4096, PriorityPresent: true}}}
	norm := p.Normalize(stp.DefaultBridgePriority)

	if got := norm.Trees[1].Priority; got != 4096 {
		t.Errorf("VLAN 1 tree priority = %d, want the explicitly configured 4096", got)
	}
}

func TestPVSTClone(t *testing.T) {
	t.Parallel()

	p := stp.PVST{
		Trees: map[vlan.ID]stp.Tree{
			1: {Ports: map[string]stp.InstancePort{"1/1/1": {PathCost: 100}}},
		},
	}

	cloned := p.Clone()
	cloned.Trees[2] = stp.Tree{Priority: 4096}
	clonedTree := cloned.Trees[1]
	clonedTree.Ports["1/1/1"] = stp.InstancePort{PathCost: 200}
	cloned.Trees[1] = clonedTree

	if _, ok := p.Trees[2]; ok {
		t.Error("original Trees map modified: VLAN 2 tree added through the clone")
	}
	if p.Trees[1].Ports["1/1/1"].PathCost != 100 {
		t.Errorf("original tree port modified: got %d, want 100", p.Trees[1].Ports["1/1/1"].PathCost)
	}
}

func TestPVSTCanonicalIncludesTrees(t *testing.T) {
	t.Parallel()

	p := stp.PVST{
		Trees: map[vlan.ID]stp.Tree{
			1:  {Priority: 4096, Ports: map[string]stp.InstancePort{"1/1/1": {PathCost: 100}}},
			10: {Priority: 8192},
		},
	}

	want := `trees=[1:{priority=4096,ports=[1/1/1:{priority=128,path_cost=100}]} 10:{priority=8192,ports=[]}]`
	if got := p.Canonical(); got != want {
		t.Errorf("Canonical() = %q, want %q", got, want)
	}
}

func TestDiffMSTInstancePortCost(t *testing.T) {
	t.Parallel()

	a := stp.Config{
		MST: &stp.MST{
			Instances: map[stp.MSTID]stp.Instance{
				1: {Ports: map[string]stp.InstancePort{"1/1/1": {PathCost: 100}}},
			},
		},
	}
	b := stp.Config{
		MST: &stp.MST{
			Instances: map[stp.MSTID]stp.Instance{
				1: {Ports: map[string]stp.InstancePort{"1/1/1": {PathCost: 200}}},
			},
		},
	}

	changes := stp.Diff(a, b)

	for _, c := range changes {
		if c.Subject.Kind == "mst_instance_port" && c.Field == "path_cost" {
			if c.From != stp.PathCostFact(100) || c.To != stp.PathCostFact(200) {
				t.Errorf("path_cost change = (%v, %v), want (100, 200)", c.From, c.To)
			}
			if !trace.EqualFact(c.From, stp.PathCostFact(100)) || trace.EqualFact(c.From, c.To) {
				t.Errorf("canonical facts did not change: from %q, to %q", c.From.Canonical(), c.To.Canonical())
			}
			return
		}
	}
	t.Fatalf("Diff() reported no mst_instance_port path_cost change: %+v", changes)
}
