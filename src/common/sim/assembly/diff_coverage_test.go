package assembly_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/assembly"
	reflectormodel "go.aledante.io/FlowSeer/src/common/sim/device/reflector"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// TestDiffCoversEveryConfigField includes delegated switch and raw assumption inputs.
// Normalize/Diff fixtures do not require construction validity.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "assembly")
}

func TestDiffQuotesReflectorMemberKeys(t *testing.T) {
	a := assembly.Config{Reflectors: map[string]reflectormodel.Config{
		"r1": {
			Address:     netaddr.MAC{2, 0, 0, 0, 0, 1},
			Ports:       map[string]phy.Ethernet{"p.1": {Setting: &phy.Setting{SpeedBPS: 100_000_000}}},
			Attachments: map[string]reflectormodel.Attachment{"edge.1": {Port: "p.1", Addresses: []netip.Prefix{netip.MustParsePrefix("198.51.100.1/24")}}},
		},
	}}
	b := a.Clone()
	b.Reflectors["r1"].Ports["p.1"].Setting.SpeedBPS = 1_000_000_000
	attachment := b.Reflectors["r1"].Attachments["edge.1"]
	attachment.Addresses = []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}
	b.Reflectors["r1"].Attachments["edge.1"] = attachment
	want := []simtest.ChangeExpectation{
		{Layer: "ethernet", Subject: trace.Subject{Kind: "reflector", Key: "r1"}, Field: `ports["p.1"].speed_bps`, From: &simtest.FactExpectation{TypeID: "phy.speed_bps", Canonical: "100000000"}, To: &simtest.FactExpectation{TypeID: "phy.speed_bps", Canonical: "1000000000"}},
		{Layer: "fabric", Subject: trace.Subject{Kind: "reflector", Key: "r1"}, Field: `attachments["edge.1"].addresses["198.51.100.1/24"]`, From: &simtest.FactExpectation{TypeID: "fabric.prefix", Canonical: "198.51.100.1/24"}},
		{Layer: "fabric", Subject: trace.Subject{Kind: "reflector", Key: "r1"}, Field: `attachments["edge.1"].addresses["192.0.2.1/24"]`, To: &simtest.FactExpectation{TypeID: "fabric.prefix", Canonical: "192.0.2.1/24"}},
	}
	got := assembly.Diff(a, b)
	if len(got) != 3 {
		t.Fatalf("records = %d, want 3: %+v", len(got), got)
	}
	for i, expected := range want {
		if !expected.Matches(got[i]) {
			t.Errorf("record[%d] = %+v, want %+v", i, got[i], expected)
		}
	}
}

func TestDiffDelegatesCorrespondence(t *testing.T) {
	cases := 0
	t.Run("ranges", func(t *testing.T) { diffDelegatesRangeCorrespondence(t, &cases) })
	t.Run("vlans", func(t *testing.T) { diffDelegatesVLANOccurrenceCorrespondence(t, &cases) })
	t.Run("prefixes", func(t *testing.T) { diffDelegatesPrefixOccurrenceCorrespondence(t, &cases) })
	if cases != 28 {
		t.Errorf("concrete correspondence cases = %d, want 28", cases)
	}
}

func diffDelegatesRangeCorrespondence(t *testing.T, casesReached *int) {
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
					got := assembly.Diff(assembly.Config{Switches: map[string]vswitch.Config{"sw1": a}}, assembly.Config{Switches: map[string]vswitch.Config{"sw1": b}})
					hits++
					if len(got) != len(tc.want) {
						t.Fatalf("normalized=%v: records = %d, want %d: %+v", normalized, len(got), len(tc.want), got)
					}
					for i, bound := range tc.want {
						want := simtest.ChangeExpectation{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"sw1"/"\"ingress\"/\"rule1\""`}, Field: field + bound.field, From: bound.from, To: bound.to}
						if !want.Matches(got[i]) {
							t.Errorf("record[%d] = %+v, want %+v", i, got[i], want)
						}
					}
					if !normalized {
						*casesReached++
					}
				}
			})
		}
	}
	if hits != 28 {
		t.Errorf("concrete range expectations = %d, want 28", hits)
	}
}

func diffDelegatesVLANOccurrenceCorrespondence(t *testing.T, casesReached *int) {
	remove10 := simtest.ChangeExpectation{Layer: "stp", Subject: trace.Subject{Kind: "mst_instance", Key: `"sw1"/"1"`}, Field: `vlans["10"]`, From: &simtest.FactExpectation{TypeID: "stp.mst.vlan_id", Canonical: "10"}}
	add10 := simtest.ChangeExpectation{Layer: "stp", Subject: trace.Subject{Kind: "mst_instance", Key: `"sw1"/"1"`}, Field: `vlans["10"]`, To: &simtest.FactExpectation{TypeID: "stp.mst.vlan_id", Canonical: "10"}}
	add20 := simtest.ChangeExpectation{Layer: "stp", Subject: trace.Subject{Kind: "mst_instance", Key: `"sw1"/"1"`}, Field: `vlans["20"]`, To: &simtest.FactExpectation{TypeID: "stp.mst.vlan_id", Canonical: "20"}}
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
				got := assembly.Diff(assembly.Config{Switches: map[string]vswitch.Config{"sw1": a}}, assembly.Config{Switches: map[string]vswitch.Config{"sw1": b}})
				hits++
				if len(got) != len(tc.want) {
					t.Fatalf("normalized=%v: Diff records = %d, want %d: %+v", normalized, len(got), len(tc.want), got)
				}
				for i, want := range tc.want {
					if !want.Matches(got[i]) {
						t.Errorf("normalized=%v: Diff[%d] = %+v, want %+v", normalized, i, got[i], want)
					}
				}
				if !normalized {
					*casesReached++
				}
			}
		})
	}
	if hits != 14 {
		t.Errorf("concrete occurrence expectations = %d, want 14", hits)
	}
}

func diffDelegatesPrefixOccurrenceCorrespondence(t *testing.T, casesReached *int) {
	aPrefix := netip.MustParsePrefix("192.0.2.1/24")
	bPrefix := netip.MustParsePrefix("198.51.100.1/24")
	removeA := simtest.ChangeExpectation{Layer: "routing", Subject: trace.Subject{Kind: "interface", Key: `"sw1"/"\"default\"/\"vlan10\""`}, Field: `prefixes["192.0.2.1/24"]`, From: &simtest.FactExpectation{TypeID: "routing.prefix", Canonical: "192.0.2.1/24"}}
	addA := simtest.ChangeExpectation{Layer: "routing", Subject: trace.Subject{Kind: "interface", Key: `"sw1"/"\"default\"/\"vlan10\""`}, Field: `prefixes["192.0.2.1/24"]`, To: &simtest.FactExpectation{TypeID: "routing.prefix", Canonical: "192.0.2.1/24"}}
	addB := simtest.ChangeExpectation{Layer: "routing", Subject: trace.Subject{Kind: "interface", Key: `"sw1"/"\"default\"/\"vlan10\""`}, Field: `prefixes["198.51.100.1/24"]`, To: &simtest.FactExpectation{TypeID: "routing.prefix", Canonical: "198.51.100.1/24"}}
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
				got := assembly.Diff(assembly.Config{Switches: map[string]vswitch.Config{"sw1": a}}, assembly.Config{Switches: map[string]vswitch.Config{"sw1": b}})
				hits++
				if len(got) != len(tc.want) {
					t.Fatalf("normalized=%v: Diff records = %d, want %d: %+v", normalized, len(got), len(tc.want), got)
				}
				for i, want := range tc.want {
					if !want.Matches(got[i]) {
						t.Errorf("normalized=%v: Diff[%d] = %+v, want %+v", normalized, i, got[i], want)
					}
				}
				if !normalized {
					*casesReached++
				}
			}
		})
	}
	if hits != 14 {
		t.Errorf("concrete occurrence expectations = %d, want 14", hits)
	}
}
