package netsimtest

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// TestEveryDiffPackageIsCovered is R9's other half: AssertEveryDiffPackageIsCovered run
// against the real repository tree, so a package that gains a diff.go with no entry in
// diffCoveredPackages fails here rather than shipping silently uncovered.
func TestEveryDiffPackageIsCovered(t *testing.T) {
	AssertEveryDiffPackageIsCovered(t)
}

type diffFixtureFact string

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

	if err := checkLeaf(seed, seedVal, byPath[".Covered"], normalize, diff); err != nil {
		t.Errorf("checkLeaf(.Covered) = %v, want nil: Diff does compare this field", err)
	}
	if err := checkLeaf(seed, seedVal, byPath[".Uncovered"], normalize, diff); err == nil {
		t.Error("checkLeaf(.Uncovered) = nil, want an error naming the field Diff never compares")
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
	vswitchDir := filepath.Join(root, "vswitch")
	newcapDir := filepath.Join(vswitchDir, "newcap")
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

	found := diffGoPackagePaths(t, root, "vswitch", "fabric")

	if problems := coverageProblems(found, []string{"vswitch/newcap"}); len(problems) != 0 {
		t.Errorf("coverageProblems with the new package covered = %v, want none", problems)
	}
	if problems := coverageProblems(found, []string{"vswitch"}); len(problems) == 0 {
		t.Error("coverageProblems with the new package omitted = none, want a problem naming vswitch/newcap")
	}
}
