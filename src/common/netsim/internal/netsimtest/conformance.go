package netsimtest

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
)

// Group identifies an invariant conformance group tested across the netsim packages.
type Group string

const (
	// GroupResultCanonicality verifies that simulation evaluation produces canonical results
	// regardless of configuration map insertion order or evaluation order.
	GroupResultCanonicality Group = "result-canonicality"

	// GroupOrderingDeterminism verifies that processing orders (e.g., ports, VLANs) are
	// strictly deterministic across randomized permutations.
	GroupOrderingDeterminism Group = "ordering-determinism"

	// GroupTraceFactStability verifies that causal trace facts and evidence trees are
	// strictly stable across identical re-runs.
	GroupTraceFactStability Group = "trace-fact-stability"

	// GroupCloneIsolation verifies that deep copies and forks of simulation entities maintain
	// complete memory isolation and do not leak mutations.
	GroupCloneIsolation Group = "clone-isolation"

	// GroupDeriveInvalidation verifies that configuration mutations or derived states
	// properly invalidate cached lookup tables.
	GroupDeriveInvalidation Group = "derive-invalidation"

	// GroupRunLifecycle verifies simulation execution bounds, step budget depletion,
	// and terminal run lifecycle states.
	GroupRunLifecycle Group = "run-lifecycle"

	// GroupExactBehavioralComparison verifies that behavioral comparison reports Equivalent
	// for identical behaviors and Different when observables diverge.
	GroupExactBehavioralComparison Group = "exact-behavioral-comparison"

	// GroupDiagnosticTraceEquality verifies that diagnostic metadata differences do not
	// alter behavioral equivalence determinations.
	GroupDiagnosticTraceEquality Group = "diagnostic-trace-equality"

	// GroupSearchCoverageAccounting verifies that bounded search accounts for domain
	// coverage, candidate remainder, and budget constraints accurately.
	GroupSearchCoverageAccounting Group = "search-coverage-accounting"
)

// AllGroups lists every declared conformance invariant group.
var AllGroups = []Group{
	GroupResultCanonicality,
	GroupOrderingDeterminism,
	GroupTraceFactStability,
	GroupCloneIsolation,
	GroupDeriveInvalidation,
	GroupRunLifecycle,
	GroupExactBehavioralComparison,
	GroupDiagnosticTraceEquality,
	GroupSearchCoverageAccounting,
}

var conformanceGroupMarkerRe = regexp.MustCompile(`(?i)(?:conformance\s+group|covers\s+conformance\s+group):\s*([a-z0-9\-]+)`)

// NetsimRootDir returns the absolute path to the src/common/netsim directory.
func NetsimRootDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// CollectClaims scans all conformance_test.go files under the specified root directory
// and maps each conformance Group to the relative package paths claiming it.
func CollectClaims(root string) (map[Group][]string, error) {
	claims := make(map[Group][]string)
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, "conformance_test.go") {
			return nil
		}

		relDir, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}

		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}

		for _, cg := range f.Comments {
			for _, m := range conformanceGroupMarkerRe.FindAllStringSubmatch(cg.Text(), -1) {
				g := Group(strings.TrimSpace(m[1]))
				if !slices.Contains(claims[g], relDir) {
					claims[g] = append(claims[g], relDir)
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	for g := range claims {
		sort.Strings(claims[g])
	}

	return claims, nil
}

// ValidateClaims asserts that every group in declared is claimed by exactly one package,
// and that no undeclared groups are claimed.
func ValidateClaims(declared []Group, claims map[Group][]string) error {
	declaredSet := make(map[Group]bool, len(declared))
	for _, g := range declared {
		declaredSet[g] = true
	}

	for g, pkgs := range claims {
		if !declaredSet[g] {
			return fmt.Errorf("unknown conformance group claimed: %q by %v", g, pkgs)
		}
	}

	for _, g := range declared {
		pkgs := claims[g]
		if len(pkgs) == 0 {
			return fmt.Errorf("unclaimed conformance group: %q (must be claimed by exactly one package)", g)
		}
		if len(pkgs) > 1 {
			return fmt.Errorf("conformance group %q claimed by multiple packages: %v", g, pkgs)
		}
	}

	return nil
}

// AssertAllGroupsClaimed asserts that all declared invariant groups are claimed by
// exactly one package under the netsim root directory.
func AssertAllGroupsClaimed(t *testing.T) {
	t.Helper()
	root := NetsimRootDir()
	claims, err := CollectClaims(root)
	if err != nil {
		t.Fatalf("CollectClaims failed: %v", err)
	}
	if err := ValidateClaims(AllGroups, claims); err != nil {
		t.Fatalf("conformance claims validation failed: %v", err)
	}
}

// PermuteOrder returns a copy of items ordered deterministically according to the given seed.
func PermuteOrder[T any](items []T, seed int64) []T {
	if len(items) <= 1 {
		return slices.Clone(items)
	}
	out := slices.Clone(items)
	s := seed
	for i := len(out) - 1; i > 0; i-- {
		s = (s*1103515245 + 12345) & 0x7fffffff
		j := s % int64(i+1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}
