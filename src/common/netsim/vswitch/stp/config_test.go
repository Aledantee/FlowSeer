package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
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
