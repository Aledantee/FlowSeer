package loopprotect_test

import (
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func testPortTable(t *testing.T) port.Table {
	t.Helper()

	tbl, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
		Add(port.Port{Name: "lag1", Kind: port.Lag}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, LagParent: "lag1"}).
		Build()
	if err != nil {
		t.Fatalf("port.Builder.Build: %v", err)
	}

	return tbl
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tbl := testPortTable(t)

	tests := []struct {
		name    string
		cfg     loopprotect.Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: loopprotect.Config{
				Ports: map[string]loopprotect.Port{
					"1/1/1": {Action: loopprotect.Block},
				},
			},
		},
		{
			name: "unknown port",
			cfg: loopprotect.Config{
				Ports: map[string]loopprotect.Port{
					"nope": {Action: loopprotect.Block},
				},
			},
			wantErr: true,
		},
		{
			name: "lag member port",
			cfg: loopprotect.Config{
				Ports: map[string]loopprotect.Port{
					"1/1/3": {Action: loopprotect.Block},
				},
			},
			wantErr: true,
		},
		{
			name: "negative interval",
			cfg: loopprotect.Config{
				Interval: -time.Second,
				Ports: map[string]loopprotect.Port{
					"1/1/1": {Action: loopprotect.Block},
				},
			},
			wantErr: true,
		},
		{
			name: "negative recovery duration",
			cfg: loopprotect.Config{
				Ports: map[string]loopprotect.Port{
					"1/1/1": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Duration: -time.Second}},
				},
			},
			wantErr: true,
		},
		{
			name: "unknown action",
			cfg: loopprotect.Config{
				Ports: map[string]loopprotect.Port{
					"1/1/1": {Action: "Bogus"},
				},
			},
			wantErr: true,
		},
		{
			name: "unknown recovery mode",
			cfg: loopprotect.Config{
				Ports: map[string]loopprotect.Port{
					"1/1/1": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: "Bogus"}},
				},
			},
			wantErr: true,
		},
		{
			name: "loop cleared with disable",
			cfg: loopprotect.Config{
				Ports: map[string]loopprotect.Port{
					"1/1/1": {Action: loopprotect.Disable, Recovery: loopprotect.Recovery{Mode: loopprotect.LoopCleared}},
				},
			},
			wantErr: true,
		},
		{
			name: "manual with disable is fine",
			cfg: loopprotect.Config{
				Ports: map[string]loopprotect.Port{
					"1/1/1": {Action: loopprotect.Disable, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual}},
				},
			},
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

func TestValidateRefusesPortNameOverProbeLimit(t *testing.T) {
	t.Parallel()

	// Evidence that Validate refuses a port name Encode could never encode:
	// the probe payload's port-name length field is a single octet, so a
	// name over 255 octets would silently wrap and make every probe that
	// port sends undecodable.
	longName := strings.Repeat("x", 256)
	tbl, err := port.NewBuilder().
		Add(port.Port{Name: longName, Kind: port.Physical}).
		Build()
	if err != nil {
		t.Fatalf("port.Builder.Build: %v", err)
	}

	cfg := loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			longName: {Action: loopprotect.Block},
		},
	}
	if err := cfg.Validate(tbl); err == nil {
		t.Errorf("Validate() = nil, want error for a %d-octet port name (probe limit is 255)", len(longName))
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	cfg := loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block},
			"1/1/2": {Action: loopprotect.NoLearn},
			"1/1/3": {Action: loopprotect.Disable},
		},
	}

	got := cfg.Normalize()

	if got.Interval != loopprotect.DefaultInterval {
		t.Errorf("Interval = %s, want %s", got.Interval, loopprotect.DefaultInterval)
	}

	wantDuration := 3 * loopprotect.DefaultInterval
	for name, wantMode := range map[string]loopprotect.RecoveryMode{
		"1/1/1": loopprotect.LoopCleared,
		"1/1/2": loopprotect.LoopCleared,
		"1/1/3": loopprotect.Manual,
	} {
		p := got.Ports[name]
		if p.Recovery.Mode != wantMode {
			t.Errorf("Ports[%q].Recovery.Mode = %s, want %s", name, p.Recovery.Mode, wantMode)
		}
		if p.Recovery.Duration != wantDuration {
			t.Errorf("Ports[%q].Recovery.Duration = %s, want %s", name, p.Recovery.Duration, wantDuration)
		}
	}
}

func TestNormalizeExplicitValuesSurvive(t *testing.T) {
	t.Parallel()

	cfg := loopprotect.Config{
		Interval: 10 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {
				Action:   loopprotect.Block,
				Recovery: loopprotect.Recovery{Mode: loopprotect.Timer, Duration: 20 * time.Second},
			},
		},
	}

	got := cfg.Normalize()

	if got.Interval != 10*time.Second {
		t.Errorf("Interval = %s, want 10s", got.Interval)
	}
	p := got.Ports["1/1/1"]
	if p.Recovery.Mode != loopprotect.Timer {
		t.Errorf("Recovery.Mode = %s, want Timer", p.Recovery.Mode)
	}
	if p.Recovery.Duration != 20*time.Second {
		t.Errorf("Recovery.Duration = %s, want 20s", p.Recovery.Duration)
	}
}

func TestClone(t *testing.T) {
	t.Parallel()

	cfg := loopprotect.Config{
		Interval: 10 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{10, 20}},
		},
	}

	cloned := cfg.Clone()
	cloned.Ports["1/1/1"] = loopprotect.Port{Action: loopprotect.Disable}

	if cfg.Ports["1/1/1"].Action != loopprotect.Block {
		t.Errorf("Clone mutated the original: Action = %s, want Block", cfg.Ports["1/1/1"].Action)
	}

	clonedVLANs := cfg.Clone().Ports["1/1/1"].VLANs
	clonedVLANs[0] = 99
	if cfg.Ports["1/1/1"].VLANs[0] != 10 {
		t.Errorf("Clone shared the VLANs slice: VLANs[0] = %d, want 10", cfg.Ports["1/1/1"].VLANs[0])
	}
}

func TestDiffActionChangeYieldsOneChange(t *testing.T) {
	t.Parallel()

	a := loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual, Duration: time.Minute}},
		},
	}
	b := loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Disable, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual, Duration: time.Minute}},
		},
	}

	changes := loopprotect.Diff(a, b)

	if len(changes) != 1 {
		t.Fatalf("Diff() = %d changes, want 1: %+v", len(changes), changes)
	}

	c := changes[0]
	if c.Field != "action" {
		t.Errorf("Field = %q, want %q", c.Field, "action")
	}
	if c.Layer != loopprotect.LayerLoopProtect {
		t.Errorf("Layer = %q, want %q", c.Layer, loopprotect.LayerLoopProtect)
	}
	if c.Subject.Kind != "port" || c.Subject.Key != "1/1/1" {
		t.Errorf("Subject = %+v, want port/1/1/1", c.Subject)
	}
	if c.From.Canonical() != string(loopprotect.Block) {
		t.Errorf("From = %s, want %s", c.From.Canonical(), loopprotect.Block)
	}
	if c.To.Canonical() != string(loopprotect.Disable) {
		t.Errorf("To = %s, want %s", c.To.Canonical(), loopprotect.Disable)
	}
}

func TestDiffNoChange(t *testing.T) {
	t.Parallel()

	cfg := loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block},
		},
	}

	if changes := loopprotect.Diff(cfg, cfg); len(changes) != 0 {
		t.Errorf("Diff(cfg, cfg) = %d changes, want 0: %+v", len(changes), changes)
	}
}

func TestDiffIntervalChange(t *testing.T) {
	t.Parallel()

	a := loopprotect.Config{Interval: 5 * time.Second}
	b := loopprotect.Config{Interval: 10 * time.Second}

	changes := loopprotect.Diff(a, b)
	if len(changes) != 1 {
		t.Fatalf("Diff() = %d changes, want 1: %+v", len(changes), changes)
	}
	if changes[0].Field != "interval" {
		t.Errorf("Field = %q, want %q", changes[0].Field, "interval")
	}
}

func TestDiffAddedAndRemovedPorts(t *testing.T) {
	t.Parallel()

	a := loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block},
		},
	}
	b := loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"1/1/2": {Action: loopprotect.NoLearn},
		},
	}

	changes := loopprotect.Diff(a, b)
	if len(changes) != 2 {
		t.Fatalf("Diff() = %d changes, want 2: %+v", len(changes), changes)
	}

	byKey := map[string]struct {
		hasFrom bool
		hasTo   bool
	}{}
	for _, c := range changes {
		byKey[c.Subject.Key] = struct {
			hasFrom bool
			hasTo   bool
		}{hasFrom: c.From != nil, hasTo: c.To != nil}
	}

	removed, ok := byKey["1/1/1"]
	if !ok || !removed.hasFrom || removed.hasTo {
		t.Errorf("removed port change = %+v, want From set and To nil", removed)
	}
	added, ok := byKey["1/1/2"]
	if !ok || added.hasFrom || !added.hasTo {
		t.Errorf("added port change = %+v, want From nil and To set", added)
	}
}
