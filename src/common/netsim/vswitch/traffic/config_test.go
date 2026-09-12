package traffic_test

import (
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

func trafficPortTable(t *testing.T) port.Table {
	t.Helper()
	table, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/24", Kind: port.Physical}).
		Add(port.Port{Name: "lag1", Kind: port.Lag}).
		Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	return table
}

func vlanPtr(id vlan.ID) *vlan.ID {
	return &id
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	ports := trafficPortTable(t)
	valid := traffic.Config{
		Mirrors:  []traffic.Mirror{{Name: "m1", SelectAll: true, OutputPort: "1/1/4"}},
		Policers: map[string]traffic.Policer{"1/1/1": {RateBPS: 1_000_000, BurstOctets: 10_000}},
		Queues:   map[string]traffic.PortQueues{"1/1/24": {MaxRateBPS: map[vlan.PCP]uint64{0: 100_000_000}}},
	}
	if err := valid.Validate(ports); err != nil {
		t.Fatalf("Validate failed for valid configuration: %v", err)
	}

	tests := []struct {
		name string
		cfg  traffic.Config
	}{
		{
			name: "both mirror outputs",
			cfg: traffic.Config{Mirrors: []traffic.Mirror{{
				Name: "m1", OutputPort: "1/1/4", OutputVLAN: vlanPtr(99),
			}}},
		},
		{
			name: "neither mirror output",
			cfg:  traffic.Config{Mirrors: []traffic.Mirror{{Name: "m1"}}},
		},
		{
			name: "empty mirror name",
			cfg:  traffic.Config{Mirrors: []traffic.Mirror{{OutputPort: "1/1/4"}}},
		},
		{
			name: "duplicate mirror name",
			cfg: traffic.Config{Mirrors: []traffic.Mirror{
				{Name: "m1", OutputPort: "1/1/4"},
				{Name: "m1", OutputVLAN: vlanPtr(99)},
			}},
		},
		{
			name: "unknown selector port",
			cfg: traffic.Config{Mirrors: []traffic.Mirror{{
				Name: "m1", SelectSrcPorts: []string{"missing"}, OutputPort: "1/1/4",
			}}},
		},
		{
			name: "unknown output port",
			cfg:  traffic.Config{Mirrors: []traffic.Mirror{{Name: "m1", OutputPort: "missing"}}},
		},
		{
			name: "LAG output port",
			cfg:  traffic.Config{Mirrors: []traffic.Mirror{{Name: "m1", OutputPort: "lag1"}}},
		},
		{
			name: "LAG member output port",
			cfg:  traffic.Config{Mirrors: []traffic.Mirror{{Name: "m1", OutputPort: "1/1/2"}}},
		},
		{
			name: "output port selected by another mirror",
			cfg: traffic.Config{Mirrors: []traffic.Mirror{
				{Name: "m1", OutputPort: "1/1/4"},
				{Name: "m2", SelectSrcPorts: []string{"1/1/4"}, OutputVLAN: vlanPtr(99)},
			}},
		},
		{
			name: "invalid selected VLAN",
			cfg: traffic.Config{Mirrors: []traffic.Mirror{{
				Name: "m1", SelectVLANs: []vlan.ID{4095}, OutputPort: "1/1/4",
			}}},
		},
		{
			name: "invalid output VLAN",
			cfg:  traffic.Config{Mirrors: []traffic.Mirror{{Name: "m1", OutputVLAN: vlanPtr(0)}}},
		},
		{
			name: "negative snap length",
			cfg:  traffic.Config{Mirrors: []traffic.Mirror{{Name: "m1", OutputPort: "1/1/4", SnapLen: -1}}},
		},
		{
			name: "snap length below tagged header",
			cfg:  traffic.Config{Mirrors: []traffic.Mirror{{Name: "m1", OutputPort: "1/1/4", SnapLen: 17}}},
		},
		{
			name: "unknown policer port",
			cfg:  traffic.Config{Policers: map[string]traffic.Policer{"missing": {}}},
		},
		{
			name: "rate without burst",
			cfg:  traffic.Config{Policers: map[string]traffic.Policer{"1/1/1": {RateBPS: 1_000_000}}},
		},
		{
			name: "unknown queue port",
			cfg:  traffic.Config{Queues: map[string]traffic.PortQueues{"missing": {}}},
		},
		{
			name: "zero queue max rate",
			cfg: traffic.Config{Queues: map[string]traffic.PortQueues{
				"1/1/24": {MaxRateBPS: map[vlan.PCP]uint64{0: 0}},
			}},
		},
		{
			name: "invalid queue PCP",
			cfg: traffic.Config{Queues: map[string]traffic.PortQueues{
				"1/1/24": {MaxRateBPS: map[vlan.PCP]uint64{8: 1}},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.cfg.Validate(ports); err == nil {
				t.Fatal("Validate succeeded, want error")
			}
		})
	}
}

func TestConfigCloneIsIndependent(t *testing.T) {
	t.Parallel()

	original := traffic.Config{
		Mirrors: []traffic.Mirror{{
			Name:           "m1",
			SelectSrcPorts: []string{"1/1/1"},
			SelectDstPorts: []string{"1/1/24"},
			SelectVLANs:    []vlan.ID{10},
			OutputVLAN:     vlanPtr(99),
		}},
		Policers: map[string]traffic.Policer{"1/1/1": {RateBPS: 1}},
		Queues: map[string]traffic.PortQueues{
			"1/1/24": {MaxRateBPS: map[vlan.PCP]uint64{7: 2}},
		},
	}
	clone := original.Clone()
	clone.Mirrors[0].SelectSrcPorts[0] = "changed"
	clone.Mirrors[0].SelectDstPorts[0] = "changed"
	clone.Mirrors[0].SelectVLANs[0] = 20
	*clone.Mirrors[0].OutputVLAN = 100
	clone.Policers["1/1/1"] = traffic.Policer{RateBPS: 3}
	clone.Queues["1/1/24"].MaxRateBPS[7] = 4

	if original.Mirrors[0].SelectSrcPorts[0] != "1/1/1" || original.Mirrors[0].SelectDstPorts[0] != "1/1/24" {
		t.Errorf("original mirror selectors changed: %+v", original.Mirrors[0])
	}
	if original.Mirrors[0].SelectVLANs[0] != 10 || *original.Mirrors[0].OutputVLAN != 99 {
		t.Errorf("original mirror VLANs changed: %+v", original.Mirrors[0])
	}
	if original.Policers["1/1/1"].RateBPS != 1 {
		t.Errorf("original policer rate = %d, want 1", original.Policers["1/1/1"].RateBPS)
	}
	if original.Queues["1/1/24"].MaxRateBPS[7] != 2 {
		t.Errorf("original queue rate = %d, want 2", original.Queues["1/1/24"].MaxRateBPS[7])
	}
}

func TestConfigLookups(t *testing.T) {
	t.Parallel()

	cfg := traffic.Config{
		Mirrors: []traffic.Mirror{
			{Name: "m1", OutputPort: "1/1/24"},
			{Name: "m2", OutputPort: "1/1/4"},
			{Name: "m3", OutputPort: "1/1/24"},
			{Name: "m4", OutputVLAN: vlanPtr(99)},
		},
		Queues: map[string]traffic.PortQueues{
			"1/1/24": {MaxRateBPS: map[vlan.PCP]uint64{7: 100_000_000}},
		},
	}
	if got, want := cfg.OutputPorts(), []string{"1/1/24", "1/1/4"}; !slices.Equal(got, want) {
		t.Errorf("OutputPorts = %v, want %v", got, want)
	}
	if got, ok := cfg.MaxRate("1/1/24", 7); !ok || got != 100_000_000 {
		t.Errorf("MaxRate = %d, %t, want 100000000, true", got, ok)
	}
	if _, ok := cfg.MaxRate("1/1/24", 0); ok {
		t.Error("MaxRate reported an absent PCP")
	}
}

func TestDiffReportsFieldsAndIgnoresSetOrder(t *testing.T) {
	t.Parallel()

	a := traffic.Config{
		Mirrors: []traffic.Mirror{{
			Name: "m1", SelectSrcPorts: []string{"1/1/24", "1/1/1"}, SelectVLANs: []vlan.ID{20, 10},
			OutputPort: "1/1/4", SnapLen: 64,
		}},
		Policers: map[string]traffic.Policer{"1/1/1": {RateBPS: 1_000_000, BurstOctets: 10_000}},
	}
	b := a.Clone()
	b.Mirrors[0].SelectSrcPorts = []string{"1/1/1", "1/1/24"}
	b.Mirrors[0].SelectVLANs = []vlan.ID{10, 20}
	b.Mirrors[0].SnapLen = 128
	b.Policers["1/1/1"] = traffic.Policer{RateBPS: 2_000_000, BurstOctets: 10_000}

	changes := traffic.Diff(a, b)
	if len(changes) != 2 {
		t.Fatalf("Diff returned %d changes, want 2: %+v", len(changes), changes)
	}
	assertChange(t, changes, trace.Subject{Kind: "mirror", Key: "m1"}, "snap_len", traffic.SnapLenFact(64), traffic.SnapLenFact(128))
	assertChange(t, changes, trace.Subject{Kind: "port", Key: "1/1/1"}, "rate", traffic.RateFact(1_000_000), traffic.RateFact(2_000_000))
}

func assertChange(t *testing.T, changes []trace.Change, subject trace.Subject, field string, from, to trace.Fact) {
	t.Helper()
	for _, change := range changes {
		if change.Layer == traffic.Layer && change.Subject == subject && change.Field == field && trace.CompareFact(change.From, from) == 0 && trace.CompareFact(change.To, to) == 0 {
			return
		}
	}
	t.Errorf("change %v %q from %v to %v not found in %+v", subject, field, from, to, changes)
}
