package netsimtest

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// diffCoveredPackages is the literal a person edits deliberately: every package under
// src/common/netsim/vswitch/, src/common/netsim/vswitch/*/, and src/common/netsim/fabric/
// that owns a diff.go. AssertEveryDiffPackageIsCovered walks those three locations and
// fails when it finds a diff.go whose package path is absent here, so a new capability
// package stops the suite rather than shipping without a coverage gate of its own. The
// walk is what grows; this literal is what a person edits deliberately.
var diffCoveredPackages = []string{
	"src/common/netsim/vswitch",
	"src/common/netsim/vswitch/bridge",
	"src/common/netsim/vswitch/lag",
	"src/common/netsim/vswitch/loopprotect",
	"src/common/netsim/vswitch/mcast",
	"src/common/netsim/vswitch/phy",
	"src/common/netsim/vswitch/port",
	"src/common/netsim/vswitch/routing",
	"src/common/netsim/vswitch/stp",
	"src/common/netsim/vswitch/traffic",
	"src/common/netsim/fabric",
}

// AssertEveryDiffPackageIsCovered walks src/common/netsim/vswitch/, its direct
// subdirectories, and src/common/netsim/fabric/ for a file named diff.go, and fails on
// every path it finds that is absent from diffCoveredPackages, and on every entry in
// diffCoveredPackages the walk did not find. Go builds one test binary per package, so no
// individual package's diff_coverage_test.go can see whether a sibling package's exists;
// this enumeration is what proves the full set ran.
func AssertEveryDiffPackageIsCovered(t *testing.T) {
	t.Helper()

	root := repoRoot(t)
	found := diffGoPackagePaths(t, root, "src/common/netsim/vswitch", "src/common/netsim/fabric")
	assertCoverage(t, found, diffCoveredPackages)
}

// diffGoPackagePaths returns, relative to root, the sorted list of package paths under
// vswitchRelDir (itself and its immediate subdirectories) and fabricRelDir that contain a
// file named diff.go. Parameterized on root so a test can point it at a temporary tree
// instead of the repository.
func diffGoPackagePaths(t *testing.T, root, vswitchRelDir, fabricRelDir string) []string {
	t.Helper()

	var found []string
	vswitchDir := filepath.Join(root, vswitchRelDir)
	if hasDiffGo(vswitchDir) {
		found = append(found, filepath.ToSlash(vswitchRelDir))
	}
	entries, err := os.ReadDir(vswitchDir)
	if err != nil {
		t.Fatalf("read %s: %v", vswitchDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(vswitchDir, e.Name())
		if hasDiffGo(dir) {
			found = append(found, filepath.ToSlash(filepath.Join(vswitchRelDir, e.Name())))
		}
	}
	fabricDir := filepath.Join(root, fabricRelDir)
	if hasDiffGo(fabricDir) {
		found = append(found, filepath.ToSlash(fabricRelDir))
	}
	slices.Sort(found)

	return found
}

// assertCoverage fails on every problem coverageProblems reports.
func assertCoverage(t *testing.T, found, covered []string) {
	t.Helper()
	for _, msg := range coverageProblems(found, covered) {
		t.Error(msg)
	}
}

// coverageProblems names every path in found absent from covered, and every path in
// covered the walk did not find: the walk is what grows, covered is what a person edits
// deliberately, and either side drifting from the other is the defect this checks for. A
// pure function, not a *testing.T-driven assertion, so a test can check its result
// directly instead of through a nested subtest whose failure would fail the outer test.
func coverageProblems(found, covered []string) []string {
	var problems []string
	coveredSet := make(map[string]bool, len(covered))
	for _, p := range covered {
		coveredSet[p] = true
	}
	for _, path := range found {
		if !coveredSet[path] {
			problems = append(problems, fmt.Sprintf("package %s has a diff.go with no entry in the covered list", path))
		}
	}
	for _, path := range covered {
		if !slices.Contains(found, path) {
			problems = append(problems, fmt.Sprintf("the covered list names %s, which the walk found no diff.go in", path))
		}
	}
	return problems
}

func hasDiffGo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "diff.go"))
	return err == nil
}

// repoRoot locates the module root from this source file's own path, the pattern
// src/common/service/test/integration/testenv/otel.go already uses to find fixtures
// relative to the module rather than the test binary's working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate diffcoverage.go source")
	}
	// this file lives at src/common/netsim/internal/netsimtest/diffcoverage.go
	return filepath.Join(filepath.Dir(source), "..", "..", "..", "..", "..")
}

// atomicLeafTypes names struct types the walker treats as one indivisible leaf value
// rather than a container to recurse into: each holds unexported internals that
// reflection cannot set field by field, and each is a value type safe to copy whole.
var atomicLeafTypes = map[reflect.Type]bool{
	reflect.TypeOf(time.Time{}):    true,
	reflect.TypeOf(netip.Addr{}):   true,
	reflect.TypeOf(netip.Prefix{}): true,
}

// stepKind names one hop in a path from a seeded value's root down to one leaf.
type stepKind int

const (
	stepField stepKind = iota
	stepMapKey
	stepSliceIndex
)

type step struct {
	kind  stepKind
	index int // struct field index, or slice index
	key   reflect.Value
}

// coverageLeaf is one scalar field reachable from a seeded value: the path to
// re-navigate a fresh copy of the root, the path exemptions match on (map values and
// slice elements collapse to "*", since a runtime key is not a structural fact), and the
// same path with real keys for failure messages.
type coverageLeaf struct {
	path        []step
	matchPath   string
	displayPath string
}

// discoverLeaves appends every exported scalar field reachable from v to *out, recursing
// into structs, non-nil pointers, map values, and slice elements. v is read-only here;
// it need not be addressable.
func discoverLeaves(v reflect.Value, path []step, matchPath, displayPath string, out *[]coverageLeaf) {
	if atomicLeafTypes[v.Type()] {
		*out = append(*out, coverageLeaf{path: path, matchPath: matchPath, displayPath: displayPath})
		return
	}

	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		discoverLeaves(v.Elem(), path, matchPath, displayPath, out)
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			discoverLeaves(v.Field(i), clonePath(path, step{kind: stepField, index: i}), matchPath+"."+f.Name, displayPath+"."+f.Name, out)
		}
	case reflect.Map:
		keys := v.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int {
			return strings.Compare(fmt.Sprint(a.Interface()), fmt.Sprint(b.Interface()))
		})
		for _, k := range keys {
			discoverLeaves(v.MapIndex(k), clonePath(path, step{kind: stepMapKey, key: k}),
				matchPath+".*", fmt.Sprintf("%s[%v]", displayPath, k.Interface()), out)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			discoverLeaves(v.Index(i), clonePath(path, step{kind: stepSliceIndex, index: i}),
				matchPath+".*", fmt.Sprintf("%s[%d]", displayPath, i), out)
		}
	default:
		// bool, string, every int/uint/float kind, and array (netaddr.MAC's [6]byte
		// among them): a single value the mutator below knows how to perturb.
		*out = append(*out, coverageLeaf{path: path, matchPath: matchPath, displayPath: displayPath})
	}
}

func clonePath(path []step, next step) []step {
	cp := make([]step, len(path)+1)
	copy(cp, path)
	cp[len(path)] = next
	return cp
}

// deepCopyValue returns an independent copy of v: pointers, maps, and slices get fresh
// backing storage (recursively), atomic leaf types and every basic kind copy by value,
// and unexported struct fields are left at their zero value, since a Config type in this
// tree exports every field a caller can set.
func deepCopyValue(v reflect.Value) reflect.Value {
	t := v.Type()
	if atomicLeafTypes[t] {
		return v
	}

	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		cp := reflect.New(t.Elem())
		cp.Elem().Set(deepCopyValue(v.Elem()))
		return cp
	case reflect.Struct:
		cp := reflect.New(t).Elem()
		for i := 0; i < t.NumField(); i++ {
			if !t.Field(i).IsExported() {
				continue
			}
			cp.Field(i).Set(deepCopyValue(v.Field(i)))
		}
		return cp
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		cp := reflect.MakeMapWithSize(t, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			cp.SetMapIndex(iter.Key(), deepCopyValue(iter.Value()))
		}
		return cp
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		cp := reflect.MakeSlice(t, v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			cp.Index(i).Set(deepCopyValue(v.Index(i)))
		}
		return cp
	default:
		return v
	}
}

// navigateForMutation walks root (addressable) along path and returns the leaf's
// settable value and a commit function. root's own struct/pointer chain stays settable
// throughout, but a map's values are not addressable in place: each map-key step copies
// the value out to a temporary, continues navigation inside it, and defers a
// SetMapIndex writing it back. commit runs those writes innermost first, after the
// caller has mutated the returned leaf value.
func navigateForMutation(root reflect.Value, path []step) (reflect.Value, func()) {
	v := root
	var commits []func()
	for _, s := range path {
		for v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		switch s.kind {
		case stepField:
			v = v.Field(s.index)
		case stepSliceIndex:
			v = v.Index(s.index)
		case stepMapKey:
			mapV, key := v, s.key
			tmp := reflect.New(mapV.Type().Elem()).Elem()
			tmp.Set(mapV.MapIndex(key))
			commits = append(commits, func() { mapV.SetMapIndex(key, tmp) })
			v = tmp
		}
	}
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	return v, func() {
		for i := len(commits) - 1; i >= 0; i-- {
			commits[i]()
		}
	}
}

// navigateReadOnly walks root along path without needing it addressable, for reading the
// pristine seed's leaf value to compare a mutation against.
func navigateReadOnly(root reflect.Value, path []step) reflect.Value {
	v := root
	for _, s := range path {
		for v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		switch s.kind {
		case stepField:
			v = v.Field(s.index)
		case stepSliceIndex:
			v = v.Index(s.index)
		case stepMapKey:
			v = v.MapIndex(s.key)
		}
	}
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	return v
}

// mutateLeaf sets v (settable) to a value of its own type guaranteed different from
// whatever it held before, or panics naming the type when no strategy covers it: a
// perturbation the walker cannot compute is a gap in the walker, not a leaf to skip.
// mutateLeaf sets v (settable) to a value of its own type guaranteed different from
// whatever it held before, or returns an error naming the type when no strategy covers
// it: a perturbation the walker cannot compute is a gap in the walker, not a leaf to
// skip.
func mutateLeaf(v reflect.Value) error {
	t := v.Type()
	switch {
	case t == reflect.TypeOf(time.Time{}):
		v.Set(reflect.ValueOf(v.Interface().(time.Time).Add(time.Second)))
		return nil
	case t == reflect.TypeOf(netip.Addr{}):
		v.Set(reflect.ValueOf(alternateAddr(v.Interface().(netip.Addr))))
		return nil
	case t == reflect.TypeOf(netip.Prefix{}):
		v.Set(reflect.ValueOf(alternatePrefix(v.Interface().(netip.Prefix))))
		return nil
	}

	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.String:
		// The zero value is valid (often the default) for every closed string enum in
		// this tree, so toggling toward it is the one mutation guaranteed to pass a
		// package's own construction-time validation in either direction.
		if v.String() == "" {
			v.SetString("diffcoverage-perturbed")
		} else {
			v.SetString("")
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(v.Uint() + 1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(v.Float() + 1)
	case reflect.Array:
		return mutateArrayLeaf(v)
	default:
		return fmt.Errorf("diffcoverage: no mutation strategy for leaf kind %s (type %s)", v.Kind(), t)
	}
	return nil
}

func mutateArrayLeaf(v reflect.Value) error {
	if v.Len() == 0 {
		return fmt.Errorf("diffcoverage: no mutation strategy for zero-length array type %s", v.Type())
	}
	last := v.Index(v.Len() - 1)
	if last.Kind() != reflect.Uint8 {
		return fmt.Errorf("diffcoverage: no mutation strategy for array element kind %s (type %s)", last.Kind(), v.Type())
	}
	last.SetUint((last.Uint() + 1) % 256)
	return nil
}

func alternateAddr(orig netip.Addr) netip.Addr {
	alt := netip.MustParseAddr("192.0.2.9")
	if orig == alt {
		return netip.MustParseAddr("192.0.2.10")
	}
	return alt
}

func alternatePrefix(orig netip.Prefix) netip.Prefix {
	alt := netip.MustParsePrefix("192.0.2.0/25")
	if orig == alt {
		return netip.MustParsePrefix("192.0.2.128/25")
	}
	return alt
}

// leavesOrEmpty walks seed and reports an error, rather than an empty leaf set, when the
// walk finds nothing to perturb: a config with no leaf would otherwise pass any coverage
// gate vacuously, having checked nothing. A pure function so a test can call it directly
// instead of through a nested subtest whose failure would fail the outer test.
func leavesOrEmpty[C any](seed C) ([]coverageLeaf, reflect.Value, error) {
	seedVal := reflect.ValueOf(seed)
	var leaves []coverageLeaf
	discoverLeaves(seedVal, nil, "", "", &leaves)
	if len(leaves) == 0 {
		return nil, seedVal, fmt.Errorf("the walk found no perturbable leaf in %T; a config with nothing to check would "+
			"pass this gate vacuously rather than proving anything", seed)
	}
	return leaves, seedVal, nil
}

// exemptionProblems names every exemption with no reason, and every exemption naming a
// path absent from found: a stale exemption reads as coverage that is not there.
func exemptionProblems(found []coverageLeaf, exemptions map[string]string) []string {
	present := make(map[string]bool, len(found))
	for _, l := range found {
		present[l.matchPath] = true
	}
	var problems []string
	for path, reason := range exemptions {
		if reason == "" {
			problems = append(problems, fmt.Sprintf("exemption %q has no reason", path))
		}
		if !present[path] {
			problems = append(problems, fmt.Sprintf("exemption %q names a field the walk did not find; it no longer exists or was renamed", path))
		}
	}
	return problems
}

// checkLeaf perturbs a fresh deep copy of seed at l alone, normalizes both sides through
// normalize, and reports an error unless diff detects the change — or reports a
// mutation-strategy error first if the perturbation did not actually produce a different
// value. A pure function, not a *testing.T-driven assertion, so a test can check its
// result directly instead of through a nested subtest whose failure would fail the outer
// test.
func checkLeaf[C any](seed C, seedVal reflect.Value, l coverageLeaf, normalize func(C) C, diff func(a, b C) []trace.Change) error {
	perturbedRoot := deepCopyValue(seedVal)
	leafVal, commit := navigateForMutation(perturbedRoot, l.path)
	origLeafVal := navigateReadOnly(seedVal, l.path)
	origCopy := reflect.New(origLeafVal.Type()).Elem()
	origCopy.Set(origLeafVal)

	if err := mutateLeaf(leafVal); err != nil {
		return fmt.Errorf("perturbing %s: %w", l.displayPath, err)
	}
	commit()

	if reflect.DeepEqual(origCopy.Interface(), leafVal.Interface()) {
		return fmt.Errorf("perturbing %s produced the same value (%v); the mutation strategy for %s needs a different value here",
			l.displayPath, leafVal.Interface(), leafVal.Type())
	}

	perturbed, ok := perturbedRoot.Interface().(C)
	if !ok {
		return fmt.Errorf("perturbed copy is %T, want %T", perturbedRoot.Interface(), seed)
	}

	if changes := diff(normalize(seed), normalize(perturbed)); len(changes) == 0 {
		return fmt.Errorf("diff reported no change after perturbing %s", l.displayPath)
	}
	return nil
}

// AssertDiffCoversConfig walks seed by reflection and, for every leaf not named in
// exemptions, perturbs a fresh deep copy's value at that leaf alone, normalizes both
// sides through normalize, and fails unless diff reports at least one change. It fails
// outright, rather than skipping, a leaf whose perturbation did not actually change the
// value: a check that plants a value proves nothing unless the plant is known to differ
// from what was there. Every key in exemptions must name a leaf the walk actually found,
// and carry a non-empty reason, or the exemption itself fails.
func AssertDiffCoversConfig[C any](t *testing.T, seed C, normalize func(C) C, diff func(a, b C) []trace.Change, exemptions map[string]string) {
	t.Helper()

	leaves, seedVal, err := leavesOrEmpty(seed)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range exemptionProblems(leaves, exemptions) {
		t.Error(msg)
	}

	for _, l := range leaves {
		l := l
		if _, exempt := exemptions[l.matchPath]; exempt {
			continue
		}
		t.Run(l.displayPath, func(t *testing.T) {
			t.Helper()
			if err := checkLeaf(seed, seedVal, l, normalize, diff); err != nil {
				t.Error(err)
			}
		})
	}
}

// AssertDiffCoversPort is port's variant of AssertDiffCoversConfig: port.Diff takes a
// Table whose fields are unexported and built only through NewBuilder, so this walks the
// exported port.Port instead and builds each side's single-port Table through the
// builder, alongside companions (a paired LAG port for a member's lag_parent leaf, for
// example) that stay fixed across every perturbation.
func AssertDiffCoversPort(t *testing.T, seed port.Port, companions []port.Port, exemptions map[string]string) {
	t.Helper()

	leaves, seedVal, err := leavesOrEmpty(seed)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range exemptionProblems(leaves, exemptions) {
		t.Error(msg)
	}

	diff := func(a, b port.Port) []trace.Change {
		return port.Diff(mustBuildPortTable(a, companions), mustBuildPortTable(b, companions))
	}
	normalize := func(p port.Port) port.Port { return p }

	for _, l := range leaves {
		l := l
		if _, exempt := exemptions[l.matchPath]; exempt {
			continue
		}
		t.Run(l.displayPath, func(t *testing.T) {
			t.Helper()
			if err := checkLeaf(seed, seedVal, l, normalize, diff); err != nil {
				t.Error(err)
			}
		})
	}
}

// mustBuildPortTable builds a Table from p and its fixed companions. Build failing here
// means a perturbation broke Table.Validate, which the mutation strategies in this file
// are meant to avoid (toggling an enum toward its zero value, keeping a companion LAG
// port for lag_parent); it is a bug in this file's fixtures, not a condition a caller can
// recover from.
func mustBuildPortTable(p port.Port, companions []port.Port) port.Table {
	b := port.NewBuilder()
	b.Add(p)
	for _, c := range companions {
		b.Add(c)
	}
	t, err := b.Build()
	if err != nil {
		panic(fmt.Sprintf("diffcoverage: build port table: %v", err))
	}
	return t
}

// AssertRetentionKeyCoversConfig walks seed by reflection and, for every leaf not named in
// exemptions, perturbs a fresh deep copy's value at that leaf alone, and asserts keyFn reports
// a different retention key. It fails outright, rather than skipping, a leaf whose perturbation
// did not actually change the value.
func AssertRetentionKeyCoversConfig[C any](t *testing.T, seed C, keyFn func(C) string, exemptions map[string]string) {
	t.Helper()

	leaves, seedVal, err := leavesOrEmpty(seed)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range exemptionProblems(leaves, exemptions) {
		t.Error(msg)
	}

	for _, l := range leaves {
		l := l
		if _, exempt := exemptions[l.matchPath]; exempt {
			continue
		}
		t.Run(l.displayPath, func(t *testing.T) {
			t.Helper()
			if err := checkLeafRetention(seed, seedVal, l, keyFn); err != nil {
				t.Error(err)
			}
		})
	}
}

func checkLeafRetention[C any](seed C, seedVal reflect.Value, l coverageLeaf, keyFn func(C) string) error {
	perturbedRoot := deepCopyValue(seedVal)
	leafVal, commit := navigateForMutation(perturbedRoot, l.path)
	origLeafVal := navigateReadOnly(seedVal, l.path)
	origCopy := reflect.New(origLeafVal.Type()).Elem()
	origCopy.Set(origLeafVal)

	if err := mutateLeaf(leafVal); err != nil {
		return fmt.Errorf("perturbing %s: %w", l.displayPath, err)
	}
	commit()

	if reflect.DeepEqual(origCopy.Interface(), leafVal.Interface()) {
		return fmt.Errorf("perturbing %s produced the same value (%v); the mutation strategy for %s needs a different value here",
			l.displayPath, leafVal.Interface(), leafVal.Type())
	}

	perturbed, ok := perturbedRoot.Interface().(C)
	if !ok {
		return fmt.Errorf("perturbed copy is %T, want %T", perturbedRoot.Interface(), seed)
	}

	if keyFn(seed) == keyFn(perturbed) {
		return fmt.Errorf("retention key reported no change after perturbing %s", l.displayPath)
	}
	return nil
}
