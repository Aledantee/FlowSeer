package vswitch_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// TestDiffCoversEveryConfigField includes all capability branches and the opaque
// port adapter, because delegation must preserve each producer's complete sequence.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "vswitch")
}

func TestDiffDelegatesRangeCorrespondence(t *testing.T) {
	type bound struct {
		field    string
		from, to *simtest.FactExpectation
	}
	fact := func(value string) *simtest.FactExpectation {
		return &simtest.FactExpectation{TypeID: "filter.port", Canonical: value}
	}
	cases := []struct {
		name string
		a, b []filter.PortRange
		want []bound
	}{
		{"insert", []filter.PortRange{{Start: 100, End: 200}, {Start: 300, End: 400}}, []filter.PortRange{{Start: 50, End: 250}, {Start: 100, End: 200}, {Start: 300, End: 400}}, []bound{{"[0].start", fact("100"), fact("50")}, {"[0].end", fact("200"), fact("250")}, {"[1].start", fact("300"), fact("100")}, {"[1].end", fact("400"), fact("200")}, {"[2].start", nil, fact("300")}, {"[2].end", nil, fact("400")}}},
		{"delete", []filter.PortRange{{Start: 100, End: 200}, {Start: 300, End: 400}}, []filter.PortRange{{Start: 300, End: 400}}, []bound{{"[0].start", fact("100"), fact("300")}, {"[0].end", fact("200"), fact("400")}, {"[1].start", fact("300"), nil}, {"[1].end", fact("400"), nil}}},
		{"move", []filter.PortRange{{Start: 100, End: 200}, {Start: 300, End: 400}}, []filter.PortRange{{Start: 500, End: 600}, {Start: 300, End: 400}}, []bound{{"[0].start", fact("100"), fact("300")}, {"[0].end", fact("200"), fact("400")}, {"[1].start", fact("300"), fact("500")}, {"[1].end", fact("400"), fact("600")}}},
		{"reorder", []filter.PortRange{{Start: 100, End: 200}, {Start: 300, End: 400}}, []filter.PortRange{{Start: 300, End: 400}, {Start: 100, End: 200}}, []bound{}},
		{"duplicate", []filter.PortRange{{Start: 100, End: 200}, {Start: 300, End: 400}}, []filter.PortRange{{Start: 100, End: 200}, {Start: 100, End: 200}, {Start: 300, End: 400}}, []bound{}},
		{"one_bound", []filter.PortRange{{Start: 100, End: 200}, {Start: 300, End: 400}}, []filter.PortRange{{Start: 101, End: 200}, {Start: 300, End: 400}}, []bound{{"[0].start", fact("100"), fact("101")}}},
		{"both_bounds", []filter.PortRange{{Start: 100, End: 200}, {Start: 300, End: 400}}, []filter.PortRange{{Start: 101, End: 201}, {Start: 300, End: 400}}, []bound{{"[0].start", fact("100"), fact("101")}, {"[0].end", fact("200"), fact("201")}}},
	}
	hits := 0
	for _, field := range []string{"match.src_ports", "match.dst_ports"} {
		for _, tc := range cases {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				for _, normalized := range []bool{false, true} {
					config := func(ranges []filter.PortRange) vswitch.Config {
						match := filter.Match{SrcPorts: ranges}
						if field == "match.dst_ports" {
							match = filter.Match{DstPorts: ranges}
						}
						return vswitch.Config{MAC: netaddr.MAC{2, 0, 0, 0, 0, 1}, Filter: &filter.Config{Sets: map[string]filter.RuleSet{"ingress": {Default: filter.Drop, Rules: []filter.Rule{{Name: "rule1", Action: filter.Accept, Match: match}}}}}}
					}
					a, b := config(tc.a), config(tc.b)
					if normalized {
						a, b = a.Normalize(), b.Normalize()
					}
					got := vswitch.Diff(a, b)
					hits++
					if len(got) != len(tc.want) {
						t.Fatalf("normalized=%v: records = %d, want %d: %+v", normalized, len(got), len(tc.want), got)
					}
					for i, bound := range tc.want {
						want := simtest.ChangeExpectation{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"ingress"/"rule1"`}, Field: field + bound.field, From: bound.from, To: bound.to}
						if !want.Matches(got[i]) {
							t.Errorf("record[%d] = %+v, want %+v", i, got[i], want)
						}
					}
				}
			})
		}
	}
	if hits != 28 {
		t.Errorf("concrete range expectations = %d, want 28", hits)
	}
}

func TestDiffDelegatesVLANOccurrenceCorrespondence(t *testing.T) {
	remove10 := simtest.ChangeExpectation{Layer: "stp", Subject: trace.Subject{Kind: "mst_instance", Key: "1"}, Field: `vlans["10"]`, From: &simtest.FactExpectation{TypeID: "stp.mst.vlan_id", Canonical: "10"}}
	add10 := simtest.ChangeExpectation{Layer: "stp", Subject: trace.Subject{Kind: "mst_instance", Key: "1"}, Field: `vlans["10"]`, To: &simtest.FactExpectation{TypeID: "stp.mst.vlan_id", Canonical: "10"}}
	add20 := simtest.ChangeExpectation{Layer: "stp", Subject: trace.Subject{Kind: "mst_instance", Key: "1"}, Field: `vlans["20"]`, To: &simtest.FactExpectation{TypeID: "stp.mst.vlan_id", Canonical: "20"}}
	cases := []struct {
		name string
		a, b []vlan.ID
		want []simtest.ChangeExpectation
	}{
		{"insert", []vlan.ID{10, 10}, []vlan.ID{10, 10, 10}, []simtest.ChangeExpectation{add10}},
		{"delete", []vlan.ID{10, 10}, []vlan.ID{10}, []simtest.ChangeExpectation{remove10}},
		{"replace_both", []vlan.ID{10, 10}, []vlan.ID{20, 20}, []simtest.ChangeExpectation{remove10, remove10, add20, add20}},
		{"reorder", []vlan.ID{10, 20}, []vlan.ID{20, 10}, []simtest.ChangeExpectation{}},
		{"deduplicate", []vlan.ID{10, 10, 20}, []vlan.ID{10, 20}, []simtest.ChangeExpectation{remove10}},
		{"remove_two", []vlan.ID{10, 10, 10}, []vlan.ID{10}, []simtest.ChangeExpectation{remove10, remove10}},
		{"add_two", []vlan.ID{10}, []vlan.ID{10, 10, 10}, []simtest.ChangeExpectation{add10, add10}},
	}
	hits := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, normalized := range []bool{false, true} {
				a := vswitch.Config{MAC: netaddr.MAC{2, 0, 0, 0, 0, 1}, STP: &stp.Config{MST: &stp.MST{Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: tc.a}}}}}
				b := vswitch.Config{MAC: netaddr.MAC{2, 0, 0, 0, 0, 1}, STP: &stp.Config{MST: &stp.MST{Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: tc.b}}}}}
				if normalized {
					a, b = a.Normalize(), b.Normalize()
				}
				got := vswitch.Diff(a, b)
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

func TestDiffDelegatesPrefixOccurrenceCorrespondence(t *testing.T) {
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
				a := vswitch.Config{MAC: netaddr.MAC{2, 0, 0, 0, 0, 1}, Routing: &routing.Config{VRFs: map[string]routing.VRF{"default": {Interfaces: map[string]routing.Interface{"vlan10": {Prefixes: tc.a}}}}}}
				b := vswitch.Config{MAC: netaddr.MAC{2, 0, 0, 0, 0, 1}, Routing: &routing.Config{VRFs: map[string]routing.VRF{"default": {Interfaces: map[string]routing.Interface{"vlan10": {Prefixes: tc.b}}}}}}
				if normalized {
					a, b = a.Normalize(), b.Normalize()
				}
				got := vswitch.Diff(a, b)
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
