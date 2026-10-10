package filter_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/tcp"
	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestDiffReportsRuleAndBindingChanges(t *testing.T) {
	protoTCP := uint8(6)
	protoUDP := uint8(17)

	cfgA := filter.Config{
		Sets: map[string]filter.RuleSet{
			"set1": {
				Stateful: true,
				Default:  filter.Drop,
				Rules: []filter.Rule{
					{
						Name:   "rule-stay",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							DstPorts: []filter.PortRange{{Start: 80, End: 80}},
						},
					},
					{
						Name:   "rule-to-remove",
						Action: filter.Drop,
						Match: filter.Match{
							Protocol: &protoUDP,
							DstPorts: []filter.PortRange{{Start: 5353, End: 5353}},
						},
					},
					{
						Name:   "rule-to-change",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							DstPorts: []filter.PortRange{{Start: 8080, End: 8080}},
						},
					},
				},
			},
		},
		Bindings: []filter.Binding{
			{Interface: "vlan10", Direction: filter.In, Set: "set1"},
			{Interface: "vlan20", Direction: filter.Out, Set: "set1"},
		},
	}

	cfgB := filter.Config{
		Sets: map[string]filter.RuleSet{
			"set1": {
				Stateful: true,
				Default:  filter.Drop,
				Rules: []filter.Rule{
					{
						Name:   "rule-stay",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							DstPorts: []filter.PortRange{{Start: 80, End: 80}},
						},
					},
					{
						Name:   "rule-to-change",
						Action: filter.Reject,
						Match: filter.Match{
							Protocol: &protoTCP,
							DstPorts: []filter.PortRange{{Start: 8080, End: 8080}},
						},
					},
					{
						Name:   "rule-to-add",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							DstPorts: []filter.PortRange{{Start: 443, End: 443}},
						},
					},
				},
			},
			"set2": {
				Stateful: false,
				Default:  filter.Accept,
			},
		},
		Bindings: []filter.Binding{
			{Interface: "vlan10", Direction: filter.In, Set: "set1"},
			{Interface: "vlan20", Direction: filter.Out, Set: "set2"}, // changed set
			{Interface: "vlan30", Direction: filter.In, Set: "set2"},  // added binding
		},
	}

	changes := filter.Diff(cfgA, cfgB)

	// Changes expected:
	// 1. Rule removed: rule-to-remove
	// 2. Rule changed: rule-to-change
	// 3. Rule added: rule-to-add
	// 4. Set added: set2
	// 5. Binding changed: vlan20/out from set1 to set2
	// 6. Binding added: vlan30/in set2
	var foundRemovedRule, foundChangedRule, foundAddedRule bool
	var foundChangedBinding, foundAddedBinding bool

	for _, c := range changes {
		if c.Subject.Kind == "rule" {
			switch c.Subject.Key {
			case `"set1"/"rule-to-remove"`:
				if c.From != nil && c.To == nil {
					foundRemovedRule = true
				}
			case `"set1"/"rule-to-change"`:
				if c.From != nil && c.To != nil {
					foundChangedRule = true
				}
			case `"set1"/"rule-to-add"`:
				if c.From == nil && c.To != nil {
					foundAddedRule = true
				}
			}
		}
		if c.Subject.Kind == "binding" {
			switch c.Subject.Key {
			case `"vlan20"/"out"`:
				if c.From != nil && c.To != nil && c.Field == "set" {
					foundChangedBinding = true
				}
			case `"vlan30"/"in"`:
				if c.From == nil && c.To != nil {
					foundAddedBinding = true
				}
			}
		}
	}

	if !foundRemovedRule {
		t.Errorf("Diff missing rule removal change; got changes: %+v", changes)
	}
	if !foundChangedRule {
		t.Errorf("Diff missing rule modification change; got changes: %+v", changes)
	}
	if !foundAddedRule {
		t.Errorf("Diff missing rule addition change; got changes: %+v", changes)
	}
	if !foundChangedBinding {
		t.Errorf("Diff missing binding change; got changes: %+v", changes)
	}
	if !foundAddedBinding {
		t.Errorf("Diff missing binding addition; got changes: %+v", changes)
	}
}

func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "filter")
}

func TestDiffRangeCorrespondence(t *testing.T) {
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
		{"insert", []filter.PortRange{{100, 200}, {300, 400}}, []filter.PortRange{{50, 250}, {100, 200}, {300, 400}}, []bound{{"[0].start", fact("100"), fact("50")}, {"[0].end", fact("200"), fact("250")}, {"[1].start", fact("300"), fact("100")}, {"[1].end", fact("400"), fact("200")}, {"[2].start", nil, fact("300")}, {"[2].end", nil, fact("400")}}},
		{"delete", []filter.PortRange{{100, 200}, {300, 400}}, []filter.PortRange{{300, 400}}, []bound{{"[0].start", fact("100"), fact("300")}, {"[0].end", fact("200"), fact("400")}, {"[1].start", fact("300"), nil}, {"[1].end", fact("400"), nil}}},
		{"move", []filter.PortRange{{100, 200}, {300, 400}}, []filter.PortRange{{500, 600}, {300, 400}}, []bound{{"[0].start", fact("100"), fact("300")}, {"[0].end", fact("200"), fact("400")}, {"[1].start", fact("300"), fact("500")}, {"[1].end", fact("400"), fact("600")}}},
		{"reorder", []filter.PortRange{{100, 200}, {300, 400}}, []filter.PortRange{{300, 400}, {100, 200}}, []bound{}},
		{"duplicate", []filter.PortRange{{100, 200}, {300, 400}}, []filter.PortRange{{100, 200}, {100, 200}, {300, 400}}, []bound{}},
		{"one_bound", []filter.PortRange{{100, 200}, {300, 400}}, []filter.PortRange{{101, 200}, {300, 400}}, []bound{{"[0].start", fact("100"), fact("101")}}},
		{"both_bounds", []filter.PortRange{{100, 200}, {300, 400}}, []filter.PortRange{{101, 201}, {300, 400}}, []bound{{"[0].start", fact("100"), fact("101")}, {"[0].end", fact("200"), fact("201")}}},
	}
	hits := 0
	for _, field := range []string{"match.src_ports", "match.dst_ports"} {
		for _, tc := range cases {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				for _, normalized := range []bool{false, true} {
					config := func(ranges []filter.PortRange) filter.Config {
						match := filter.Match{SrcPorts: ranges}
						if field == "match.dst_ports" {
							match = filter.Match{DstPorts: ranges}
						}
						return filter.Config{Sets: map[string]filter.RuleSet{"s": {Default: filter.Drop, Rules: []filter.Rule{{Name: "r", Action: filter.Accept, Match: match}}}}}
					}
					a, b := config(tc.a), config(tc.b)
					if normalized {
						a, b = a.Normalize(layer.Env{}), b.Normalize(layer.Env{})
					}
					got := filter.Diff(a, b)
					hits++
					if len(got) != len(tc.want) {
						t.Fatalf("normalized=%v: records = %d, want %d: %+v", normalized, len(got), len(tc.want), got)
					}
					for i, bound := range tc.want {
						want := simtest.ChangeExpectation{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"s"/"r"`}, Field: field + bound.field, From: bound.from, To: bound.to}
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

func TestDiffPrefixRemovalPrecedesAddition(t *testing.T) {
	config := func(prefix string) filter.Config {
		return filter.Config{Sets: map[string]filter.RuleSet{"s": {Default: filter.Drop, Rules: []filter.Rule{{Name: "r", Action: filter.Accept, Match: filter.Match{Src: []netip.Prefix{netip.MustParsePrefix(prefix)}}}}}}}
	}
	got := filter.Diff(config("198.51.100.0/24"), config("192.0.2.0/24"))
	want := []simtest.ChangeExpectation{
		{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"s"/"r"`}, Field: `match.src["198.51.100.0/24"]`, From: &simtest.FactExpectation{TypeID: "filter.prefix", Canonical: "198.51.100.0/24"}},
		{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"s"/"r"`}, Field: `match.src["192.0.2.0/24"]`, To: &simtest.FactExpectation{TypeID: "filter.prefix", Canonical: "192.0.2.0/24"}},
	}
	if len(got) != 2 {
		t.Fatalf("records = %d, want 2", len(got))
	}
	for i, expected := range want {
		if !expected.Matches(got[i]) {
			t.Errorf("record[%d] = %+v, want %+v", i, got[i], expected)
		}
	}
}

func TestDiffDuplicateBindingWinner(t *testing.T) {
	a := filter.Config{Bindings: []filter.Binding{{Interface: "vlan10", Direction: filter.In, Set: "a"}, {Interface: "vlan10", Direction: filter.In, Set: "z"}}}
	hits := 0
	for _, tc := range []struct {
		name, kept string
		want       []simtest.ChangeExpectation
	}{
		{"remove_overwritten", "z", []simtest.ChangeExpectation{}},
		{"remove_winner", "a", []simtest.ChangeExpectation{{Layer: "filter", Subject: trace.Subject{Kind: "binding", Key: `"vlan10"/"in"`}, Field: "set", From: &simtest.FactExpectation{TypeID: "filter.binding", Canonical: `set="z"`}, To: &simtest.FactExpectation{TypeID: "filter.binding", Canonical: `set="a"`}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := filter.Config{Bindings: []filter.Binding{{Interface: "vlan10", Direction: filter.In, Set: tc.kept}}}
			got := filter.Diff(a, b)
			hits++
			if len(got) != len(tc.want) {
				t.Fatalf("records = %d, want %d", len(got), len(tc.want))
			}
			for i, want := range tc.want {
				if !want.Matches(got[i]) {
					t.Errorf("record[%d] = %+v, want %+v", i, got[i], want)
				}
			}
		})
	}
	if hits != 2 {
		t.Errorf("concrete binding expectations = %d, want 2", hits)
	}
}

func TestDiffRetainedRuleMemberOrder(t *testing.T) {
	a := filter.Config{Sets: map[string]filter.RuleSet{"s": {Default: filter.Drop, Rules: []filter.Rule{{Name: "r", Action: filter.Accept, Match: filter.Match{
		Protocol: new(uint8(6)),
		Src:      []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")},
		Dst:      []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
		SrcPorts: []filter.PortRange{{Start: 100, End: 200}},
		DstPorts: []filter.PortRange{{Start: 300, End: 400}},
		ICMP:     &filter.ICMPMatch{Type: 8, Code: new(uint8(0))},
		TCPFlags: &filter.FlagMatch{Mask: tcp.SYN | tcp.ACK, Value: tcp.SYN},
	}}}}}}
	b := filter.Config{Sets: map[string]filter.RuleSet{"s": {Default: filter.Drop, Rules: []filter.Rule{{Name: "r", Action: filter.Drop}}}}}
	want := []simtest.ChangeExpectation{
		{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"s"/"r"`}, Field: "action", From: &simtest.FactExpectation{TypeID: "filter.action", Canonical: "accept"}, To: &simtest.FactExpectation{TypeID: "filter.action", Canonical: "drop"}},
		{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"s"/"r"`}, Field: "match.protocol", From: &simtest.FactExpectation{TypeID: "filter.protocol", Canonical: "6"}},
		{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"s"/"r"`}, Field: "match.src[\"198.51.100.0/24\"]", From: &simtest.FactExpectation{TypeID: "filter.prefix", Canonical: "198.51.100.0/24"}},
		{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"s"/"r"`}, Field: "match.dst[\"192.0.2.0/24\"]", From: &simtest.FactExpectation{TypeID: "filter.prefix", Canonical: "192.0.2.0/24"}},
		{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"s"/"r"`}, Field: "match.src_ports[0].start", From: &simtest.FactExpectation{TypeID: "filter.port", Canonical: "100"}},
		{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"s"/"r"`}, Field: "match.src_ports[0].end", From: &simtest.FactExpectation{TypeID: "filter.port", Canonical: "200"}},
		{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"s"/"r"`}, Field: "match.dst_ports[0].start", From: &simtest.FactExpectation{TypeID: "filter.port", Canonical: "300"}},
		{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"s"/"r"`}, Field: "match.dst_ports[0].end", From: &simtest.FactExpectation{TypeID: "filter.port", Canonical: "400"}},
		{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"s"/"r"`}, Field: "match.icmp.type", From: &simtest.FactExpectation{TypeID: "filter.icmp_type", Canonical: "8"}},
		{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"s"/"r"`}, Field: "match.icmp.code", From: &simtest.FactExpectation{TypeID: "filter.icmp_code", Canonical: "0"}},
		{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"s"/"r"`}, Field: "match.tcp_flags.mask", From: &simtest.FactExpectation{TypeID: "filter.tcp_flags", Canonical: "18"}},
		{Layer: "filter", Subject: trace.Subject{Kind: "rule", Key: `"s"/"r"`}, Field: "match.tcp_flags.value", From: &simtest.FactExpectation{TypeID: "filter.tcp_flags", Canonical: "2"}},
	}
	got := filter.Diff(a, b)
	if len(got) != 12 {
		t.Fatalf("records = %d, want 12", len(got))
	}
	for i, expected := range want {
		if !expected.Matches(got[i]) {
			t.Errorf("record[%d] = %+v, want %+v", i, got[i], expected)
		}
	}
}

// TestRetentionKeyCoversEveryConfigField verifies that every exported filter.Config field
// affects filter.RetentionKey.
func TestRetentionKeyCoversEveryConfigField(t *testing.T) {
	protoTCP := uint8(6)
	icmpCode := uint8(0)

	seed := filter.Config{
		Sets: map[string]filter.RuleSet{
			"set1": {
				Stateful: true,
				Default:  filter.Drop,
				Rules: []filter.Rule{
					{
						Name:   "rule1",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							Src:      []netip.Prefix{netip.MustParsePrefix("10.0.1.0/24")},
							Dst:      []netip.Prefix{netip.MustParsePrefix("10.0.2.0/24")},
							SrcPorts: []filter.PortRange{{Start: 1000, End: 2000}},
							DstPorts: []filter.PortRange{{Start: 80, End: 80}},
							ICMP:     &filter.ICMPMatch{Type: 8, Code: &icmpCode},
							TCPFlags: &filter.FlagMatch{Mask: tcp.SYN | tcp.ACK, Value: tcp.SYN},
						},
					},
				},
			},
		},
		Bindings: []filter.Binding{
			{Interface: "vlan10", Direction: filter.In, Set: "set1"},
		},
	}

	keyFn := func(c filter.Config) string {
		return filter.RetentionKey(c, layer.Env{NodeID: "node-1"})
	}
	simtest.AssertRetentionKeyCoversConfig(t, seed, keyFn, nil)

	key1 := filter.RetentionKey(seed, layer.Env{NodeID: "node-1"})
	key2 := filter.RetentionKey(seed, layer.Env{NodeID: "node-2"})
	if key1 == key2 {
		t.Errorf("RetentionKey produced same key for different node IDs: %q", key1)
	}
}
