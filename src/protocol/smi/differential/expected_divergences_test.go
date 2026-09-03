package differential_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi/differential"
)

func TestBitsNumberingDivergencePreservesMembers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ours   string
		gosmi  string
		accept bool
	}{
		{name: "discarded numbers", ours: "alpha=0 beta=7", gosmi: "alpha=0 beta=0", accept: true},
		{name: "missing member", ours: "alpha=7", gosmi: "alpha=0 beta=0"},
		{name: "extra member", ours: "alpha=0 beta=7 gamma=9", gosmi: "alpha=0 beta=0"},
		{name: "renamed member", ours: "alpha=0 gamma=7", gosmi: "alpha=0 beta=0"},
		{name: "missing members", gosmi: "alpha=0 beta=0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, accepted := classify(differential.Divergence{
				Module: "ACME-MIB", Subject: "Capabilities", Field: differential.FieldMembers,
				Ours: tc.ours, Gosmi: tc.gosmi,
			})
			if accepted != tc.accept {
				t.Errorf("accepted = %v (rule %q), want %v", accepted, name, tc.accept)
			}
		})
	}
}
