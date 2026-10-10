package routing_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// TestDiffCoversEveryConfigField verifies that every exported routing.Config field reaches
// routing.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "routing")
}

func TestDiffPrefixOccurrenceCorrespondence(t *testing.T) {
	aPrefix := netip.MustParsePrefix("192.0.2.1/24")
	bPrefix := netip.MustParsePrefix("198.51.100.1/24")
	removeA := simtest.ChangeExpectation{Layer: "routing", Subject: trace.Subject{Kind: "interface", Key: `"default"/"vlan10"`}, Field: `prefixes["192.0.2.1/24"]`, From: &simtest.FactExpectation{TypeID: "routing.prefix", Canonical: "192.0.2.1/24"}}
	addA := simtest.ChangeExpectation{Layer: "routing", Subject: trace.Subject{Kind: "interface", Key: `"default"/"vlan10"`}, Field: `prefixes["192.0.2.1/24"]`, To: &simtest.FactExpectation{TypeID: "routing.prefix", Canonical: "192.0.2.1/24"}}
	addB := simtest.ChangeExpectation{Layer: "routing", Subject: trace.Subject{Kind: "interface", Key: `"default"/"vlan10"`}, Field: `prefixes["198.51.100.1/24"]`, To: &simtest.FactExpectation{TypeID: "routing.prefix", Canonical: "198.51.100.1/24"}}
	cases := []struct {
		name string
		a, b []netip.Prefix
		want []simtest.ChangeExpectation
	}{
		{"insert", []netip.Prefix{aPrefix, aPrefix}, []netip.Prefix{aPrefix, aPrefix, aPrefix}, []simtest.ChangeExpectation{addA}},
		{"delete", []netip.Prefix{aPrefix, aPrefix}, []netip.Prefix{aPrefix}, []simtest.ChangeExpectation{removeA}},
		{"replace_both", []netip.Prefix{aPrefix, aPrefix}, []netip.Prefix{bPrefix, bPrefix}, []simtest.ChangeExpectation{removeA, removeA, addB, addB}},
		{"reorder", []netip.Prefix{aPrefix, bPrefix}, []netip.Prefix{bPrefix, aPrefix}, []simtest.ChangeExpectation{}},
		{"deduplicate", []netip.Prefix{aPrefix, aPrefix, bPrefix}, []netip.Prefix{aPrefix, bPrefix}, []simtest.ChangeExpectation{removeA}},
		{"remove_two", []netip.Prefix{aPrefix, aPrefix, aPrefix}, []netip.Prefix{aPrefix}, []simtest.ChangeExpectation{removeA, removeA}},
		{"add_two", []netip.Prefix{aPrefix}, []netip.Prefix{aPrefix, aPrefix, aPrefix}, []simtest.ChangeExpectation{addA, addA}},
	}
	hits := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, normalized := range []bool{false, true} {
				a := routing.Config{VRFs: map[string]routing.VRF{"default": {Interfaces: map[string]routing.Interface{"vlan10": {Prefixes: tc.a}}}}}
				b := routing.Config{VRFs: map[string]routing.VRF{"default": {Interfaces: map[string]routing.Interface{"vlan10": {Prefixes: tc.b}}}}}
				if normalized {
					a, b = a.Normalize(layer.Env{}), b.Normalize(layer.Env{})
				}
				got := routing.Diff(a, b)
				hits++
				if len(got) != len(tc.want) {
					t.Fatalf("normalized=%v: Diff records = %d, want %d: %+v", normalized, len(got), len(tc.want), got)
				}
				for i, want := range tc.want {
					if !want.Matches(got[i]) {
						t.Errorf("normalized=%v: Diff[%d] = %+v, want %+v", normalized, i, got[i], want)
					}
				}
			}
		})
	}
	if hits != 14 {
		t.Errorf("concrete occurrence expectations = %d, want 14", hits)
	}
}
