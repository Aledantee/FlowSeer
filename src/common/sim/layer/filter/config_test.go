package filter_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/tcp"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
)

func TestConfigValidation(t *testing.T) {
	protoTCP := uint8(6)

	tests := []struct {
		name    string
		cfg     filter.Config
		wantErr bool
	}{
		{
			name: "valid configuration",
			cfg: filter.Config{
				Sets: map[string]filter.RuleSet{
					"s1": {
						Default: filter.Drop,
						Rules: []filter.Rule{
							{
								Name:   "r1",
								Action: filter.Accept,
								Match: filter.Match{
									Protocol: &protoTCP,
									Src:      []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
									SrcPorts: []filter.PortRange{{Start: 1, End: 100}},
									TCPFlags: &filter.FlagMatch{Mask: tcp.SYN, Value: tcp.SYN},
								},
							},
						},
					},
				},
				Bindings: []filter.Binding{
					{Interface: "vlan10", Direction: filter.In, Set: "s1"},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid default action",
			cfg: filter.Config{
				Sets: map[string]filter.RuleSet{
					"s1": {Default: "invalid-action"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid rule action",
			cfg: filter.Config{
				Sets: map[string]filter.RuleSet{
					"s1": {
						Default: filter.Drop,
						Rules:   []filter.Rule{{Action: "invalid-action"}},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid port range",
			cfg: filter.Config{
				Sets: map[string]filter.RuleSet{
					"s1": {
						Default: filter.Drop,
						Rules: []filter.Rule{
							{
								Action: filter.Accept,
								Match:  filter.Match{SrcPorts: []filter.PortRange{{Start: 200, End: 100}}},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "tcp flags value outside mask",
			cfg: filter.Config{
				Sets: map[string]filter.RuleSet{
					"s1": {
						Default: filter.Drop,
						Rules: []filter.Rule{
							{
								Action: filter.Accept,
								Match:  filter.Match{TCPFlags: &filter.FlagMatch{Mask: tcp.SYN, Value: tcp.ACK}},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "empty binding interface",
			cfg: filter.Config{
				Sets: map[string]filter.RuleSet{
					"s1": {Default: filter.Drop},
				},
				Bindings: []filter.Binding{
					{Interface: "", Direction: filter.In, Set: "s1"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid binding direction",
			cfg: filter.Config{
				Sets: map[string]filter.RuleSet{
					"s1": {Default: filter.Drop},
				},
				Bindings: []filter.Binding{
					{Interface: "vlan10", Direction: "both", Set: "s1"},
				},
			},
			wantErr: true,
		},
		{
			name: "binding references unknown set",
			cfg: filter.Config{
				Sets: map[string]filter.RuleSet{
					"s1": {Default: filter.Drop},
				},
				Bindings: []filter.Binding{
					{Interface: "vlan10", Direction: filter.In, Set: "unknown-set"},
				},
			},
			wantErr: true,
		},
		{
			name: "duplicate binding for same interface and direction",
			cfg: filter.Config{
				Sets: map[string]filter.RuleSet{
					"s1": {Default: filter.Drop},
					"s2": {Default: filter.Accept},
				},
				Bindings: []filter.Binding{
					{Interface: "vlan10", Direction: filter.In, Set: "s1"},
					{Interface: "vlan10", Direction: filter.In, Set: "s2"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate(layer.Env{})
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
