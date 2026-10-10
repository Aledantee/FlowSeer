package simtest

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// TestEveryDiffPackageIsCovered runs AssertEveryDiffPackageIsCovered against the real
// repository tree, so a package that gains a diff.go with no entry in
// diffCoveredPackages fails here rather than shipping silently uncovered.
func TestEveryDiffPackageIsCovered(t *testing.T) {
	AssertEveryDiffPackageIsCovered(t)
}

func TestMappedTPIDEffectiveAndActiveControls(t *testing.T) {
	cases := []struct {
		owner, path, key, before, after, from, to string
	}{
		{"bridge", "bridge.Config.VLAN.Switchports[1/1/1].Tunnel.TPID", "1/1/1", `0`, `33024`, "34984", "33024"},
		{"bridge", "bridge.Config.VLAN.Switchports[1/1/1].Tunnel.TPID", "1/1/1", `34984`, `34985`, "34984", "34985"},
		{"vswitch", "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tunnel.TPID", "1/1/1", `0`, `33024`, "34984", "33024"},
		{"vswitch", "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tunnel.TPID", "1/1/1", `34984`, `34985`, "34984", "34985"},
		{"assembly", "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Tunnel.TPID", `"sw1"/"1/1/1"`, `0`, `33024`, "34984", "33024"},
		{"assembly", "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Tunnel.TPID", `"sw1"/"1/1/1"`, `34984`, `34985`, "34984", "34985"},
	}
	hits := 0
	for _, tc := range cases {
		t.Run(tc.path+"/"+tc.before+"_to_"+tc.after, func(t *testing.T) {
			m, err := diffMappingFor(tc.owner)
			if err != nil {
				t.Fatal(err)
			}
			entry := mappedEntry{Path: tc.path, Producer: "bridge", Before: tc.before, After: tc.after}
			m.expectation = func(path string, hits *int) ([]changeRecord, error) {
				if path != tc.path {
					return nil, errs.Msgf("unknown TPID control %s", path)
				}
				*hits++
				return []changeRecord{{Layer: "vlan", Kind: "port", Key: tc.key, Field: "tunnel.tpid", From: &[2]string{"bridge.tpid", tc.from}, To: &[2]string{"bridge.tpid", tc.to}}}, nil
			}
			if err := checkMappedLeaf(m, entry, &hits); err != nil {
				t.Error(err)
			}
		})
	}
	if hits != 6 {
		t.Errorf("TPID effective/active expectations reached %d, want 6", hits)
	}
}

func TestMappedOptionalFilterAggregateSequences(t *testing.T) {
	cases := []struct {
		owner, path, populated string
		removal, addition      []changeRecord
	}{
		{"filter", "filter.Config.Sets[set1].Rules[0].Match.ICMP.@present", `{"Type":8,"Code":0}`, []changeRecord{{"filter", "rule", "\"set1\"/\"rule1\"", "match.icmp.type", &[2]string{"filter.icmp_type", "8"}, nil}, {"filter", "rule", "\"set1\"/\"rule1\"", "match.icmp.code", &[2]string{"filter.icmp_code", "0"}, nil}}, []changeRecord{{"filter", "rule", "\"set1\"/\"rule1\"", "match.icmp.type", nil, &[2]string{"filter.icmp_type", "8"}}, {"filter", "rule", "\"set1\"/\"rule1\"", "match.icmp.code", nil, &[2]string{"filter.icmp_code", "0"}}}},
		{"filter", "filter.Config.Sets[set1].Rules[0].Match.TCPFlags.@present", `{"Mask":2,"Value":2}`, []changeRecord{{"filter", "rule", "\"set1\"/\"rule1\"", "match.tcp_flags.mask", &[2]string{"filter.tcp_flags", "2"}, nil}, {"filter", "rule", "\"set1\"/\"rule1\"", "match.tcp_flags.value", &[2]string{"filter.tcp_flags", "2"}, nil}}, []changeRecord{{"filter", "rule", "\"set1\"/\"rule1\"", "match.tcp_flags.mask", nil, &[2]string{"filter.tcp_flags", "2"}}, {"filter", "rule", "\"set1\"/\"rule1\"", "match.tcp_flags.value", nil, &[2]string{"filter.tcp_flags", "2"}}}},
		{"vswitch", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.ICMP.@present", `{"Type":8,"Code":0}`, []changeRecord{{"filter", "rule", "\"ingress\"/\"rule1\"", "match.icmp.type", &[2]string{"filter.icmp_type", "8"}, nil}, {"filter", "rule", "\"ingress\"/\"rule1\"", "match.icmp.code", &[2]string{"filter.icmp_code", "0"}, nil}}, []changeRecord{{"filter", "rule", "\"ingress\"/\"rule1\"", "match.icmp.type", nil, &[2]string{"filter.icmp_type", "8"}}, {"filter", "rule", "\"ingress\"/\"rule1\"", "match.icmp.code", nil, &[2]string{"filter.icmp_code", "0"}}}},
		{"vswitch", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.TCPFlags.@present", `{"Mask":2,"Value":2}`, []changeRecord{{"filter", "rule", "\"ingress\"/\"rule1\"", "match.tcp_flags.mask", &[2]string{"filter.tcp_flags", "2"}, nil}, {"filter", "rule", "\"ingress\"/\"rule1\"", "match.tcp_flags.value", &[2]string{"filter.tcp_flags", "2"}, nil}}, []changeRecord{{"filter", "rule", "\"ingress\"/\"rule1\"", "match.tcp_flags.mask", nil, &[2]string{"filter.tcp_flags", "2"}}, {"filter", "rule", "\"ingress\"/\"rule1\"", "match.tcp_flags.value", nil, &[2]string{"filter.tcp_flags", "2"}}}},
		{"assembly", "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.ICMP.@present", `{"Type":8,"Code":0}`, []changeRecord{{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.icmp.type", &[2]string{"filter.icmp_type", "8"}, nil}, {"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.icmp.code", &[2]string{"filter.icmp_code", "0"}, nil}}, []changeRecord{{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.icmp.type", nil, &[2]string{"filter.icmp_type", "8"}}, {"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.icmp.code", nil, &[2]string{"filter.icmp_code", "0"}}}},
		{"assembly", "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.TCPFlags.@present", `{"Mask":2,"Value":2}`, []changeRecord{{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.tcp_flags.mask", &[2]string{"filter.tcp_flags", "2"}, nil}, {"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.tcp_flags.value", &[2]string{"filter.tcp_flags", "2"}, nil}}, []changeRecord{{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.tcp_flags.mask", nil, &[2]string{"filter.tcp_flags", "2"}}, {"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.tcp_flags.value", nil, &[2]string{"filter.tcp_flags", "2"}}}},
	}
	removalHits, additionHits, exactHits := 0, 0, 0
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			m, err := diffMappingFor(tc.owner)
			if err != nil {
				t.Fatal(err)
			}
			var entry mappedEntry
			for _, e := range m.entries {
				if e.Path == tc.path {
					entry = e
				}
			}
			removal, err := m.expectation(tc.path, &removalHits)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(removal, tc.removal) {
				t.Errorf("removal sequence = %+v, want %+v", removal, tc.removal)
			}
			additions, err := optionalFilterAdditionExpectation(tc.path, &additionHits)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(additions, tc.addition) {
				t.Errorf("addition sequence = %+v, want %+v", additions, tc.addition)
			}
			hits := 0
			if err := checkMappedLeaf(m, entry, &hits); err != nil {
				t.Fatal(err)
			}
			reverse := entry
			reverse.Before = "null"
			reverse.After = tc.populated
			m.expectation = optionalFilterAdditionExpectation
			if err := checkMappedLeaf(m, reverse, &hits); err != nil {
				t.Fatal(err)
			}
			for _, sequence := range [][]changeRecord{tc.removal, tc.addition} {
				var actual []trace.Change
				for _, r := range sequence {
					var from, to trace.Fact
					if r.From != nil {
						from = optionalFilterFixtureFact(*r.From)
					}
					if r.To != nil {
						to = optionalFilterFixtureFact(*r.To)
					}
					actual = append(actual, trace.Change{Layer: trace.Layer(r.Layer), Subject: trace.Subject{Kind: r.Kind, Key: r.Key}, Field: r.Field, From: from, To: to})
				}
				if err := matchMappedChanges(actual, sequence); err != nil {
					t.Fatal(err)
				}
				exactHits++
				if err := matchMappedChanges(actual[:1], sequence); err == nil {
					t.Error("optional member lost under exact fixture matcher")
				}
			}
		})
	}
	if removalHits != 6 || additionHits != 6 {
		t.Errorf("optional removal/addition expectations %d/%d, want 6/6", removalHits, additionHits)
	}
	if exactHits != 12 {
		t.Errorf("optional exact fixture expectations %d, want 12", exactHits)
	}
}

type optionalFilterFixtureFact [2]string

func (f optionalFilterFixtureFact) TypeID() string    { return f[0] }
func (f optionalFilterFixtureFact) Canonical() string { return f[1] }

func TestMappedInactiveFaultControls(t *testing.T) {
	m, err := assemblyDiffMapping()
	if err != nil {
		t.Fatal(err)
	}
	assertInactiveFaultCoverage(t, m)
}

func TestCheckLeafExactFixtures(t *testing.T) {
	type fixture struct {
		Scalar   int
		Nested   map[string][]int
		Noop     int
		Interval int
		Identity string
		Ports    port.Table
	}
	table := mustBuildPortTable(port.Port{Name: "fixed", Kind: port.Physical}, nil)
	seed := fixture{Scalar: 1, Nested: map[string][]int{"key.with/slash": {2}}, Noop: 1, Interval: 1, Identity: "old", Ports: table}
	leaves, root, err := leavesOrEmpty(seed)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]coverageLeaf{}
	for _, l := range leaves {
		byPath[l.matchPath] = l
	}
	normalize := func(c fixture) fixture { c.Noop = 0; return c }
	diff := func(a, b fixture) []trace.Change {
		// The opaque table must survive the copy used for every submitted scalar.
		if !reflect.DeepEqual(a.Ports.Ports(), b.Ports.Ports()) {
			return []trace.Change{{Field: "table-damaged"}}
		}
		change := func(field string, from, to int) trace.Change {
			return trace.Change{Field: field, From: diffFixtureFact(strconv.Itoa(from)), To: diffFixtureFact(strconv.Itoa(to))}
		}
		switch {
		case a.Scalar != b.Scalar:
			return []trace.Change{change("scalar", a.Scalar, b.Scalar)}
		case a.Nested["key.with/slash"][0] != b.Nested["key.with/slash"][0]:
			return []trace.Change{change("nested", 2, 3)}
		case a.Interval != b.Interval:
			return []trace.Change{change("interval", 1, 2), change("derived", 3, 6)}
		case a.Identity != b.Identity:
			return []trace.Change{{Subject: trace.Subject{Kind: "item", Key: ""}, To: diffFixtureFact("")}, {Subject: trace.Subject{Kind: "item", Key: "old"}, From: diffFixtureFact("old")}}
		default:
			return nil
		}
	}
	cases := []struct {
		path string
		want []changeRecord
	}{
		{".Scalar", []changeRecord{{Field: "scalar", From: &[2]string{"diffcoverage.fixture", "1"}, To: &[2]string{"diffcoverage.fixture", "2"}}}},
		{".Nested.*.*", []changeRecord{{Field: "nested", From: &[2]string{"diffcoverage.fixture", "2"}, To: &[2]string{"diffcoverage.fixture", "3"}}}},
		{".Noop", nil},
		{".Interval", []changeRecord{{Field: "interval", From: &[2]string{"diffcoverage.fixture", "1"}, To: &[2]string{"diffcoverage.fixture", "2"}}, {Field: "derived", From: &[2]string{"diffcoverage.fixture", "3"}, To: &[2]string{"diffcoverage.fixture", "6"}}}},
		{".Identity", []changeRecord{{Kind: "item", Key: "", To: &[2]string{"diffcoverage.fixture", ""}}, {Kind: "item", Key: "old", From: &[2]string{"diffcoverage.fixture", "old"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if err := checkLeaf(seed, root, byPath[tc.path], normalize, diff, tc.want); err != nil {
				t.Fatal(err)
			}
			if len(tc.want) > 0 {
				unrelated := func(_, _ fixture) []trace.Change {
					return []trace.Change{{Field: "unrelated", To: diffFixtureFact("irrelevant")}}
				}
				if err := checkLeaf(seed, root, byPath[tc.path], normalize, unrelated, tc.want); err == nil {
					t.Error("unrelated result admitted")
				}
			}
		})
	}
	unsupported := struct{ Value complex128 }{Value: 1}
	unsupportedLeaves, value, err := leavesOrEmpty(unsupported)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkLeaf(unsupported, value, unsupportedLeaves[0], func(c struct{ Value complex128 }) struct{ Value complex128 } { return c }, func(_, _ struct{ Value complex128 }) []trace.Change { return nil }, nil); err == nil {
		t.Error("unsupported leaf accepted")
	}
}

func TestMappedDiffManifest(t *testing.T) {
	registered, hits, empty := 0, 0, 0
	for _, owner := range []string{"bridge", "phy", "loopprotect", "mcast", "lag", "stp", "filter", "routing", "traffic", "port", "vswitch", "assembly"} {
		m, err := diffMappingFor(owner)
		if err != nil {
			t.Fatal(err)
		}
		if err := mappedSeedProblems(m); err != nil {
			t.Fatal(err)
		}
		registered += len(m.entries)
		for _, e := range m.entries {
			t.Run(e.Path, func(t *testing.T) {
				if err := checkMappedLeaf(m, e, &hits); err != nil {
					t.Error(err)
				}
				n := 0
				want, err := m.expectation(e.Path, &n)
				if err != nil {
					t.Fatal(err)
				}
				if len(want) == 0 {
					empty++
				}
			})
		}
	}
	if registered != 627 {
		t.Errorf("registered %d, want 627", registered)
	}
	if hits != 627 {
		t.Errorf("concrete expectations reached %d, want 627", hits)
	}
	if empty != 8 {
		t.Errorf("primary empty expectations %d, want 8", empty)
	}
}

func TestMappedChangesExactSequence(t *testing.T) {
	fact := diffFixtureFact("one")
	correct := []trace.Change{{Layer: "fixture", Subject: trace.Subject{Kind: "port", Key: "p1"}, Field: "value", From: fact, To: diffFixtureFact("two")}, {Layer: "fixture", Subject: trace.Subject{Kind: "port", Key: "p2"}, Field: "value", To: fact}}
	want := []changeRecord{{"fixture", "port", "p1", "value", &[2]string{"diffcoverage.fixture", "one"}, &[2]string{"diffcoverage.fixture", "two"}}, {"fixture", "port", "p2", "value", nil, &[2]string{"diffcoverage.fixture", "one"}}}
	cases := []struct {
		name   string
		mutate func([]trace.Change) []trace.Change
	}{
		{"layer", func(c []trace.Change) []trace.Change { c[0].Layer = "wrong"; return c }},
		{"kind", func(c []trace.Change) []trace.Change { c[0].Subject.Kind = "wrong"; return c }},
		{"key", func(c []trace.Change) []trace.Change { c[0].Subject.Key = "wrong"; return c }},
		{"field", func(c []trace.Change) []trace.Change { c[0].Field = "wrong"; return c }},
		{"fact", func(c []trace.Change) []trace.Change { c[0].To = diffFixtureFact("wrong"); return c }},
		{"typed_fact", func(c []trace.Change) []trace.Change { c[0].To = diffWrongTypeFact("two"); return c }},
		{"missing_with_unrelated", func(c []trace.Change) []trace.Change {
			c[0] = trace.Change{Layer: "fixture", Field: "unrelated"}
			return c
		}},
		{"duplicate", func(c []trace.Change) []trace.Change { return append(c, c[0]) }},
		{"reordered", func(c []trace.Change) []trace.Change { c[0], c[1] = c[1], c[0]; return c }},
		{"half_pair", func(c []trace.Change) []trace.Change { return c[:1] }},
	}
	if err := matchMappedChanges(correct, want); err != nil {
		t.Fatalf("correct sequence: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := matchMappedChanges(tc.mutate(slices.Clone(correct)), want); err == nil {
				t.Error("incorrect sequence accepted")
			}
		})
	}
	repeated := []trace.Change{correct[1], correct[1]}
	if err := matchMappedChanges(repeated, []changeRecord{want[1], want[1]}); err != nil {
		t.Error(err)
	}
	if err := matchMappedChanges(append(repeated, correct[1]), []changeRecord{want[1], want[1]}); err == nil {
		t.Error("unexpected occurrence accepted")
	}
	if err := matchMappedChanges(correct, nil); err == nil {
		t.Error("nonempty normalization no-op accepted")
	}
}

func TestMappedSeedAndMutationGuards(t *testing.T) {
	m, err := vswitchDiffMapping()
	if err != nil {
		t.Fatal(err)
	}
	if err := mappedSeedProblems(m); err != nil {
		t.Fatal(err)
	}
	broken := m
	broken.seed = deepCopyValue(m.seed)
	broken.seed.FieldByName("Filter").SetZero()
	if err := mappedSeedProblems(broken); err == nil {
		t.Error("nil required filter branch accepted")
	}
	type fixture struct {
		Value int
		Added int
	}
	var structural []string
	structuralMappedPaths(reflect.TypeOf(fixture{}), "fixture.Config", &structural)
	extra := diffMapping{owner: "fixture", seed: reflect.ValueOf(fixture{Value: 1, Added: 1}), entries: []mappedEntry{{Path: "fixture.Config.Value"}}}
	if err := mappedSeedProblems(extra); err == nil {
		t.Error("new exported member accepted without mapping")
	}
	type nested struct{ Values map[string][]*int }
	seed := nested{Values: map[string][]*int{"k.with/dot": {new(1)}}}
	root := deepCopyValue(reflect.ValueOf(seed))
	if err := editMappedPath(root, mappedPathTokens("Values[k.with/dot][0]"), `2`); err != nil {
		t.Fatal(err)
	}
	if got := *root.Interface().(nested).Values["k.with/dot"][0]; got != 2 {
		t.Errorf("written nested value %d, want 2", got)
	}
	if got := *seed.Values["k.with/dot"][0]; got != 1 {
		t.Errorf("seed changed to %d, want 1", got)
	}
	unsupported := reflect.ValueOf(new(complex128)).Elem()
	if err := decodeMappedValue(unsupported, `2`); err == nil {
		t.Error("unsupported mutation accepted")
	}
	assemblyMapping, err := assemblyDiffMapping()
	if err != nil {
		t.Fatal(err)
	}
	switchKeyHits := 0
	for _, entry := range assemblyMapping.entries {
		if entry.Setup != "switch-key" {
			continue
		}
		a, b, err := prepareMappedInputs(assemblyMapping, entry)
		if err != nil {
			t.Fatal(err)
		}
		before := a.FieldByName("Switches").MapIndex(reflect.ValueOf("sw1")).Interface().(vswitch.Config)
		after := b.FieldByName("Switches").MapIndex(reflect.ValueOf("sw2")).Interface().(vswitch.Config)
		want := vswitch.Config{MAC: netaddr.MAC{2, 0, 0, 0, 0, 1}}
		switchKeyHits++
		if !reflect.DeepEqual(before, want) || !reflect.DeepEqual(after, want) {
			t.Errorf("switch-key inputs = %+v / %+v, want MAC-only %+v", before, after, want)
		}
	}
	if switchKeyHits != 1 {
		t.Errorf("switch-key concrete expectations reached %d, want 1", switchKeyHits)
	}
}

func TestMappedExactSequenceRejectsMissingProducer(t *testing.T) {
	m, err := vswitchDiffMapping()
	if err != nil {
		t.Fatal(err)
	}
	boundaryHits := 0
	for _, entry := range m.entries {
		if entry.Path != "vswitch.Config.Bridge.AgingTime" && entry.Path != "vswitch.Config.Bridge.@present" {
			continue
		}
		t.Run(entry.Path, func(t *testing.T) {
			boundaryHits++
			m.diff = func(_, _ reflect.Value) []trace.Change {
				return []trace.Change{{Layer: "relay", Subject: trace.Subject{Kind: "capability", Key: "relay"}, Field: "presence", To: diffFixtureFact("allowed")}, {Layer: "vlan", Subject: trace.Subject{Kind: "capability", Key: "vlan"}, Field: "presence", To: diffFixtureFact("allowed")}}
			}
			hits := 0
			if err := checkMappedLeaf(m, entry, &hits); err == nil {
				t.Error("exact matcher admitted missing bridge arm")
			}
		})
	}
	if boundaryHits != 2 {
		t.Errorf("producer boundary expectations reached %d, want 2", boundaryHits)
	}
}

type diffFixtureFact string

type diffWrongTypeFact string

func (f diffWrongTypeFact) TypeID() string    { return "diffcoverage.wrong_type" }
func (f diffWrongTypeFact) Canonical() string { return string(f) }

func (f diffFixtureFact) TypeID() string    { return "diffcoverage.fixture" }
func (f diffFixtureFact) Canonical() string { return string(f) }

// TestCheckLeafCatchesAnOmittedField proves the per-leaf check reports a problem for
// exactly the field a fixture's Diff never compares, and reports none for the field it
// does. checkLeaf is a pure function precisely so this test can call it directly instead
// of through a nested subtest, whose failure would fail this test along with it.
func TestCheckLeafCatchesAnOmittedField(t *testing.T) {
	type fixtureConfig struct {
		Covered   int
		Uncovered int
	}
	normalize := func(c fixtureConfig) fixtureConfig { return c }
	diff := func(a, b fixtureConfig) []trace.Change {
		var changes []trace.Change
		if a.Covered != b.Covered {
			changes = append(changes, trace.Change{Field: "covered", From: diffFixtureFact("a"), To: diffFixtureFact("b")})
		}
		// Uncovered has no arm: the defect this gate exists to catch.
		return changes
	}
	seed := fixtureConfig{Covered: 1, Uncovered: 2}

	leaves, seedVal, err := leavesOrEmpty(seed)
	if err != nil {
		t.Fatalf("leavesOrEmpty: %v", err)
	}
	byPath := make(map[string]coverageLeaf, len(leaves))
	for _, l := range leaves {
		byPath[l.matchPath] = l
	}

	if err := checkLeaf(seed, seedVal, byPath[".Covered"], normalize, diff, []changeRecord{{Field: "covered", From: &[2]string{"diffcoverage.fixture", "a"}, To: &[2]string{"diffcoverage.fixture", "b"}}}); err != nil {
		t.Errorf("checkLeaf(.Covered) = %v, want nil: Diff does compare this field", err)
	}
	if err := checkLeaf(seed, seedVal, byPath[".Uncovered"], normalize, diff, []changeRecord{{Field: "covered", From: &[2]string{"diffcoverage.fixture", "a"}, To: &[2]string{"diffcoverage.fixture", "b"}}}); err == nil {
		t.Error("checkLeaf(.Uncovered) = nil, want an error naming the field Diff never compares")
	}
}

// TestCheckLeafFailsForAVswitchLeafWithNoDiffArm extends the omitted-field evidence to
// the port-bearing vswitch.Config the gate actually walks. The seed carries a populated
// port.Table, whose unexported fields a field-by-field deep copy would zero; the fixture
// Diff is the real vswitch.Diff with its mac arm removed. Perturbing .MAC must fail the
// check, because with the arm gone nothing reports the MAC change; a deep copy that
// emptied the perturbed table would instead inject spurious port.Diff removals and pass
// this leaf, and every other vswitch leaf, vacuously.
func TestCheckLeafFailsForAVswitchLeafWithNoDiffArm(t *testing.T) {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}
	seed := vswitch.Config{
		MAC:   netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
		Ports: ports,
	}

	leaves, seedVal, err := leavesOrEmpty(seed)
	if err != nil {
		t.Fatalf("leavesOrEmpty: %v", err)
	}
	byPath := make(map[string]coverageLeaf, len(leaves))
	for _, l := range leaves {
		byPath[l.matchPath] = l
	}
	macLeaf, ok := byPath[".MAC"]
	if !ok {
		t.Fatalf("the walk found no .MAC leaf in vswitch.Config")
	}

	// vswitch.Diff minus its mac arm: the defect this gate exists to catch.
	diffWithoutMacArm := func(a, b vswitch.Config) []trace.Change {
		var kept []trace.Change
		for _, c := range vswitch.Diff(a, b) {
			if c.Field != "mac" {
				kept = append(kept, c)
			}
		}
		return kept
	}

	if err := checkLeaf(seed, seedVal, macLeaf, vswitch.Config.Normalize, diffWithoutMacArm, []changeRecord{{Layer: "port", Kind: "device", Field: "mac", From: &[2]string{"vswitch.mac", "02:00:00:00:00:01"}, To: &[2]string{"vswitch.mac", "02:00:00:00:00:02"}}}); err == nil {
		t.Error("checkLeaf(.MAC) with vswitch.Diff's mac arm removed = nil, want an error: " +
			"only the mac arm can report this leaf, so the gate must fail when the arm is absent")
	}
	if err := checkLeaf(seed, seedVal, macLeaf, vswitch.Config.Normalize, vswitch.Diff, []changeRecord{{Layer: "port", Kind: "device", Field: "mac", From: &[2]string{"vswitch.mac", "02:00:00:00:00:01"}, To: &[2]string{"vswitch.mac", "02:00:00:00:00:02"}}}); err != nil {
		t.Errorf("checkLeaf(.MAC) with the full vswitch.Diff = %v, want nil: the mac arm covers this leaf", err)
	}
}

// TestCheckLeafRetentionCatchesAnOmittedField proves the per-leaf retention check reports
// a problem for exactly the field a fixture's key function never includes, and reports
// none for the field it does.
func TestCheckLeafRetentionCatchesAnOmittedField(t *testing.T) {
	type fixtureConfig struct {
		Covered   int
		Uncovered int
	}
	keyFn := func(c fixtureConfig) string {
		return strconv.Itoa(c.Covered)
	}
	seed := fixtureConfig{Covered: 1, Uncovered: 2}

	leaves, seedVal, err := leavesOrEmpty(seed)
	if err != nil {
		t.Fatalf("leavesOrEmpty: %v", err)
	}
	byPath := make(map[string]coverageLeaf, len(leaves))
	for _, l := range leaves {
		byPath[l.matchPath] = l
	}

	if err := checkLeafRetention(seed, seedVal, byPath[".Covered"], keyFn); err != nil {
		t.Errorf("checkLeafRetention(.Covered) = %v, want nil", err)
	}
	if err := checkLeafRetention(seed, seedVal, byPath[".Uncovered"], keyFn); err == nil {
		t.Error("checkLeafRetention(.Uncovered) = nil, want an error naming the omitted field")
	}
}

// TestLeavesOrEmptyFailsRatherThanPassingVacuously covers the case a check that plants a
// value proves nothing unless the plant is known to differ from what was there: a
// fixture whose only field is struct{}, a single-valued type, has no leaf the walker can
// perturb, so leavesOrEmpty must report an error rather than an empty, uncomplaining set.
func TestLeavesOrEmptyFailsRatherThanPassingVacuously(t *testing.T) {
	type onlyField struct {
		Nothing struct{}
	}

	if _, _, err := leavesOrEmpty(onlyField{}); err == nil {
		t.Error("leavesOrEmpty(onlyField{}) = nil error, want an error: there is no leaf to perturb, so a gate built on it would pass vacuously")
	}
}

// TestCoverageProblemsCatchesAnUncoveredPackage builds a fixture directory tree with a
// diff.go the covered list does not name, and asserts coverageProblems reports it.
func TestCoverageProblemsCatchesAnUncoveredPackage(t *testing.T) {
	root := t.TempDir()
	newcapDir := filepath.Join(root, "layer", "newcap")
	if err := os.MkdirAll(newcapDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", newcapDir, err)
	}
	if err := os.WriteFile(filepath.Join(newcapDir, "diff.go"), []byte("package newcap\n"), 0o644); err != nil {
		t.Fatalf("write diff.go: %v", err)
	}
	fabricDir := filepath.Join(root, "fabric")
	if err := os.MkdirAll(fabricDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", fabricDir, err)
	}

	found := diffGoPackagePaths(t, root, []string{"device/vswitch", "fabric"}, []string{"layer"})

	if problems := coverageProblems(found, []string{"layer/newcap"}); len(problems) != 0 {
		t.Errorf("coverageProblems with the new package covered = %v, want none", problems)
	}
	if problems := coverageProblems(found, []string{"fabric"}); len(problems) == 0 {
		t.Error("coverageProblems with the new package omitted = none, want a problem naming layer/newcap")
	}
}

func TestMappedNestedCapabilityProducerPartitions(t *testing.T) {
	cases := []struct {
		owner, path string
		records     int
	}{
		{"vswitch", "vswitch.Config.Bridge.VLAN.@present", 4},
		{"vswitch", "vswitch.Config.Phy.PoE.@present", 3},
		{"assembly", "assembly.Config.Switches[sw1].Bridge.VLAN.@present", 4},
		{"assembly", "assembly.Config.Switches[sw1].Phy.PoE.@present", 3},
	}
	hits := 0
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			m, err := diffMappingFor(tc.owner)
			if err != nil {
				t.Fatal(err)
			}
			var entry mappedEntry
			for _, e := range m.entries {
				if e.Path == tc.path {
					entry = e
				}
			}
			if entry.Path == "" {
				t.Fatal("missing declared nested lifecycle slot")
			}
			original := m.diff
			for _, variant := range []string{"baseline", "missing", "wrong", "capability-key", "order", "capability-field", "exact-sequence"} {
				t.Run(variant, func(t *testing.T) {
					m.diff = func(a, b reflect.Value) []trace.Change {
						got := original(a, b)
						if len(got) != tc.records {
							t.Fatalf("fixture records = %d, want %d", len(got), tc.records)
						}
						switch variant {
						case "missing":
							got = append(got[:1], got[2:]...)
						case "wrong":
							got[1].Field = "wrong-delegated-field"
						case "capability-key":
							got[0].Subject.Key = "wrong-capability"
						case "capability-field":
							got[0].Field = ""
						case "order":
							got[1], got[2] = got[2], got[1]
						}
						return got
					}
					n := 0
					err := checkMappedLeaf(m, entry, &n)
					hits += n
					if variant == "baseline" || variant == "exact-sequence" {
						if err != nil {
							t.Error(err)
						}
					} else if err == nil {
						t.Errorf("%s admitted", variant)
					}
				})
			}
		})
	}
	if hits != 28 {
		t.Errorf("concrete partition expectations = %d, want 28", hits)
	}
}
