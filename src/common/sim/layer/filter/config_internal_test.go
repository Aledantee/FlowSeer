package filter

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

func TestConfigNormalizeAndClone(t *testing.T) {
	cfg := Config{
		Sets: map[string]RuleSet{
			"s1": {
				Stateful: true,
				Default:  Accept,
				Rules: []Rule{
					{
						Name:   "r1",
						Action: Accept,
						Match: Match{
							Src: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
						},
					},
				},
			},
		},
		Bindings: []Binding{
			{Interface: "vlan20", Direction: In, Set: "s1"},
			{Interface: "vlan10", Direction: Out, Set: "s1"},
			{Interface: "vlan10", Direction: In, Set: "s1"},
		},
	}

	norm := cfg.Normalize(layer.Env{})
	if !norm.equal(cfg) {
		t.Errorf("norm.equal(cfg) = false, want true")
	}

	// Verify bindings were sorted by interface then direction
	if norm.Bindings[0].Interface != "vlan10" || norm.Bindings[0].Direction != In {
		t.Errorf("unexpected first binding: %+v", norm.Bindings[0])
	}
	if norm.Bindings[1].Interface != "vlan10" || norm.Bindings[1].Direction != Out {
		t.Errorf("unexpected second binding: %+v", norm.Bindings[1])
	}
	if norm.Bindings[2].Interface != "vlan20" || norm.Bindings[2].Direction != In {
		t.Errorf("unexpected third binding: %+v", norm.Bindings[2])
	}

	cloned := cfg.Clone()
	if !cloned.equal(cfg) {
		t.Errorf("cloned.equal(cfg) = false, want true")
	}
}
