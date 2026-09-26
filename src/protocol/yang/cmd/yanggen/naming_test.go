package main

import (
	"regexp"
	"slices"
	"testing"
)

// TestNameScopeClash forces a clash between "a-b" + "c" and "a" + "b-c"
// (both join to "ABC"), asserting the second claim gets the X-hex suffix
// and that the result is identical on repeated runs.
func TestNameScopeClash(t *testing.T) {
	s := newNameScope()
	first := s.claim("ABC", "/a-b/c")
	if first != "ABC" {
		t.Fatalf("first claim got %q, want %q", first, "ABC")
	}

	second := s.claim("ABC", "/a/b-c")
	hexPattern := regexp.MustCompile(`^ABCX[0-9a-f]{6}$`)
	if !hexPattern.MatchString(second) {
		t.Errorf("second claim got %q, want ABCX<6-hex-digits>", second)
	}

	// Repeated run with a fresh scope must yield the identical result.
	s2 := newNameScope()
	s2.claim("ABC", "/a-b/c")
	second2 := s2.claim("ABC", "/a/b-c")
	if second2 != second {
		t.Errorf("repeated run got %q, want %q", second2, second)
	}
}

// TestFairGrowthRequirement5 verifies Requirement 5: two nodes whose own names
// clash ("config" under two different parents) receive distinct names grown by
// one ancestor segment each ("InterfaceConfig" and "SubinterfaceConfig").
func TestFairGrowthRequirement5(t *testing.T) {
	node1 := &claimantEntity{
		id:         "node1",
		candidates: candidateSuffixes([]string{"Interface", "Config"}),
		getSymbols: func(base string) []claimSpec {
			return []claimSpec{
				{wanted: base, kind: kindStruct, depth: 2, tieBreak: "/interface/config", discriminator: "shape1"},
				{wanted: base + "Schema", kind: kindSchema, depth: 2, tieBreak: "/interface/config", discriminator: "schema:shape1"},
			}
		},
	}
	node2 := &claimantEntity{
		id:         "node2",
		candidates: candidateSuffixes([]string{"Subinterface", "Config"}),
		getSymbols: func(base string) []claimSpec {
			return []claimSpec{
				{wanted: base, kind: kindStruct, depth: 2, tieBreak: "/subinterface/config", discriminator: "shape2"},
				{wanted: base + "Schema", kind: kindSchema, depth: 2, tieBreak: "/subinterface/config", discriminator: "schema:shape2"},
			}
		},
	}

	scope := resolveFairGrowth([]*claimantEntity{node1, node2})

	got1 := scope.byPath["shape1"]
	if got1 != "InterfaceConfig" {
		t.Errorf("node1 struct = %q, want %q", got1, "InterfaceConfig")
	}
	got2 := scope.byPath["shape2"]
	if got2 != "SubinterfaceConfig" {
		t.Errorf("node2 struct = %q, want %q", got2, "SubinterfaceConfig")
	}
	got1Schema := scope.byPath["schema:shape1"]
	if got1Schema != "InterfaceConfigSchema" {
		t.Errorf("node1 schema = %q, want %q", got1Schema, "InterfaceConfigSchema")
	}
	got2Schema := scope.byPath["schema:shape2"]
	if got2Schema != "SubinterfaceConfigSchema" {
		t.Errorf("node2 schema = %q, want %q", got2Schema, "SubinterfaceConfigSchema")
	}
}

// TestFairGrowthReversedOrderByteIdentity asserts that resolving entities in
// forward order vs reverse order yields the exact same byte-identical assigned names.
func TestFairGrowthReversedOrderByteIdentity(t *testing.T) {
	makeEntities := func() (*claimantEntity, *claimantEntity) {
		n1 := &claimantEntity{
			id:         "node1",
			candidates: candidateSuffixes([]string{"Interface", "Config"}),
			getSymbols: func(base string) []claimSpec {
				return []claimSpec{
					{wanted: base, kind: kindStruct, depth: 2, tieBreak: "/interface/config", discriminator: "shape1"},
					{wanted: base + "Schema", kind: kindSchema, depth: 2, tieBreak: "/interface/config", discriminator: "schema:shape1"},
				}
			},
		}
		n2 := &claimantEntity{
			id:         "node2",
			candidates: candidateSuffixes([]string{"Subinterface", "Config"}),
			getSymbols: func(base string) []claimSpec {
				return []claimSpec{
					{wanted: base, kind: kindStruct, depth: 2, tieBreak: "/subinterface/config", discriminator: "shape2"},
					{wanted: base + "Schema", kind: kindSchema, depth: 2, tieBreak: "/subinterface/config", discriminator: "schema:shape2"},
				}
			},
		}
		return n1, n2
	}

	fwd1, fwd2 := makeEntities()
	fwdScope := resolveFairGrowth([]*claimantEntity{fwd1, fwd2})

	rev1, rev2 := makeEntities()
	revScope := resolveFairGrowth([]*claimantEntity{rev2, rev1})

	for _, key := range []string{"shape1", "shape2", "schema:shape1", "schema:shape2"} {
		fwd := fwdScope.byPath[key]
		rev := revScope.byPath[key]
		if fwd != rev {
			t.Errorf("key %q: forward got %q, reverse got %q", key, fwd, rev)
		}
	}
}

// TestFairGrowthHashFallback asserts that when candidate growth exhausts segments,
// the clash falls back to an X<hash> suffix deterministically, and the result is
// identical in reversed order.
func TestFairGrowthHashFallback(t *testing.T) {
	makeEntities := func() (*claimantEntity, *claimantEntity) {
		n1 := &claimantEntity{
			id:         "nodeA",
			candidates: candidateSuffixes([]string{"State"}),
			getSymbols: func(base string) []claimSpec {
				return []claimSpec{
					{wanted: base, kind: kindStruct, depth: 1, tieBreak: "/alarm-state", discriminator: "shapeA"},
				}
			},
		}
		n2 := &claimantEntity{
			id:         "nodeB",
			candidates: candidateSuffixes([]string{"State"}),
			getSymbols: func(base string) []claimSpec {
				return []claimSpec{
					{wanted: base, kind: kindStruct, depth: 1, tieBreak: "/alarmState", discriminator: "shapeB"},
				}
			},
		}
		return n1, n2
	}

	fwdA, fwdB := makeEntities()
	fwdScope := resolveFairGrowth([]*claimantEntity{fwdA, fwdB})

	revA, revB := makeEntities()
	revScope := resolveFairGrowth([]*claimantEntity{revB, revA})

	wantPat := regexp.MustCompile(`^StateX[0-9a-f]{6}$`)
	if fwdScope.byPath["shapeA"] != "State" {
		t.Errorf("fwd shapeA = %q, want State", fwdScope.byPath["shapeA"])
	}
	if !wantPat.MatchString(fwdScope.byPath["shapeB"]) {
		t.Errorf("fwd shapeB = %q, want StateX<hex>", fwdScope.byPath["shapeB"])
	}

	if revScope.byPath["shapeA"] != fwdScope.byPath["shapeA"] {
		t.Errorf("rev shapeA = %q, want %q", revScope.byPath["shapeA"], fwdScope.byPath["shapeA"])
	}
	if revScope.byPath["shapeB"] != fwdScope.byPath["shapeB"] {
		t.Errorf("rev shapeB = %q, want %q", revScope.byPath["shapeB"], fwdScope.byPath["shapeB"])
	}
}

func TestCandidateAndCommonSuffix(t *testing.T) {
	cands := candidateSuffixes([]string{"A", "B", "C"})
	wantCands := []string{"C", "BC", "ABC"}
	if !slices.Equal(cands, wantCands) {
		t.Errorf("candidateSuffixes = %v, want %v", cands, wantCands)
	}

	common := longestCommonSuffix([][]string{
		{"A", "B", "Item"},
		{"X", "Y", "Item"},
	})
	if !slices.Equal(common, []string{"Item"}) {
		t.Errorf("longestCommonSuffix = %v, want [Item]", common)
	}

	common2 := longestCommonSuffix([][]string{
		{"Interface", "Subinterface", "Config"},
		{"Other", "Subinterface", "Config"},
	})
	if !slices.Equal(common2, []string{"Subinterface", "Config"}) {
		t.Errorf("longestCommonSuffix = %v, want [Subinterface Config]", common2)
	}
}
