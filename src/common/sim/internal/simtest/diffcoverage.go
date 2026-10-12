package simtest

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// diffCoveredPackages is the literal a person edits deliberately: every package at
// src/common/sim/device/vswitch, src/common/sim/port, src/common/sim/assembly, or directly
// under src/common/sim/layer/ that owns a diff.go. AssertEveryDiffPackageIsCovered walks
// those locations and fails when it finds a diff.go whose package path is absent here, so
// a new capability package stops the suite rather than shipping without a coverage gate
// of its own. The walk is what grows; this literal is what a person edits deliberately.
var diffCoveredPackages = []string{
	"src/common/sim/device/vswitch",
	"src/common/sim/layer/bridge",
	"src/common/sim/layer/filter",
	"src/common/sim/layer/lag",
	"src/common/sim/layer/loopprotect",
	"src/common/sim/layer/mcast",
	"src/common/sim/layer/phy",
	"src/common/sim/port",
	"src/common/sim/layer/routing",
	"src/common/sim/layer/stp",
	"src/common/sim/layer/traffic",
	"src/common/sim/assembly",
}

// AssertEveryDiffPackageIsCovered walks src/common/sim/device/vswitch,
// src/common/sim/port, src/common/sim/assembly, and the immediate subdirectories of
// src/common/sim/layer/ for a file named diff.go, and fails on every path it finds that
// is absent from diffCoveredPackages, and on every entry in diffCoveredPackages the walk
// did not find. Go builds one test binary per package, so no individual package's
// diff_coverage_test.go can see whether a sibling package's exists; this enumeration is
// what proves the full set ran.
func AssertEveryDiffPackageIsCovered(t *testing.T) {
	t.Helper()

	root := repoRoot(t)
	found := diffGoPackagePaths(t, root,
		[]string{"src/common/sim/device/vswitch", "src/common/sim/port", "src/common/sim/assembly"},
		[]string{"src/common/sim/layer"})
	assertCoverage(t, found, diffCoveredPackages)
}

// diffGoPackagePaths returns, relative to root, the sorted list of package paths that
// contain a file named diff.go: each directory in packageDirs itself, and each immediate
// subdirectory of every directory in parentDirs. Parameterized on root so a test can
// point it at a temporary tree instead of the repository.
func diffGoPackagePaths(t *testing.T, root string, packageDirs, parentDirs []string) []string {
	t.Helper()

	var found []string
	for _, rel := range packageDirs {
		if hasDiffGo(filepath.Join(root, rel)) {
			found = append(found, filepath.ToSlash(rel))
		}
	}
	for _, rel := range parentDirs {
		parent := filepath.Join(root, rel)
		entries, err := os.ReadDir(parent)
		if err != nil {
			t.Fatalf("read %s: %v", parent, err)
		}
		for _, e := range entries {
			if e.IsDir() && hasDiffGo(filepath.Join(parent, e.Name())) {
				found = append(found, filepath.ToSlash(filepath.Join(rel, e.Name())))
			}
		}
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
// deliberately, and either side drifting from the other is the defect this checks for.
// The pure return value lets a test check the result directly without a nested subtest
// whose failure would also fail the outer test.
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
	// this file lives at src/common/sim/internal/simtest/diffcoverage.go
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

// hasUnexportedField reports whether t declares any field reflection cannot set
// individually, forcing deepCopyValue to copy the struct whole.
func hasUnexportedField(t reflect.Type) bool {
	for i := 0; i < t.NumField(); i++ {
		if !t.Field(i).IsExported() {
			return true
		}
	}
	return false
}

// deepCopyValue returns an independent copy of v: pointers, maps, and slices get fresh
// backing storage (recursively), and atomic leaf types and every basic kind copy by
// value. A struct with any unexported field copies whole in one Set: reflection cannot
// set unexported fields one by one, so rebuilding only the exported ones would silently
// zero the rest: port.Table would come back empty, and the diff under test would then
// report removals unrelated to whichever leaf was perturbed, passing every leaf
// vacuously. After the whole copy, each exported reference field is re-copied for its
// own storage: the walk does descend into exported fields, so an exported pointer,
// slice, or map left aliased to the seed would be mutated through when a leaf beneath
// it is perturbed. Unexported fields keep the whole-copy value.
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
		if hasUnexportedField(t) {
			cp.Set(v)
			for i := 0; i < t.NumField(); i++ {
				if t.Field(i).IsExported() {
					cp.Field(i).Set(deepCopyValue(v.Field(i)))
				}
			}
			return cp
		}
		for i := 0; i < t.NumField(); i++ {
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
		return errs.Msgf("diffcoverage: no mutation strategy for leaf kind %s (type %s)", v.Kind(), t)
	}
	return nil
}

func mutateArrayLeaf(v reflect.Value) error {
	if v.Len() == 0 {
		return errs.Msgf("diffcoverage: no mutation strategy for zero-length array type %s", v.Type())
	}
	last := v.Index(v.Len() - 1)
	if last.Kind() != reflect.Uint8 {
		return errs.Msgf("diffcoverage: no mutation strategy for array element kind %s (type %s)", last.Kind(), v.Type())
	}
	last.SetUint((last.Uint() + 1) % 256)
	return nil
}

type mappedEntry struct {
	Path, Producer, Setup, Before, After string
	Reason                               string
}

type changeRecord struct {
	Layer, Kind, Key, Field string
	From, To                *[2]string
}

type diffMapping struct {
	owner       string
	seed        reflect.Value
	normalize   func(reflect.Value) reflect.Value
	diff        func(reflect.Value, reflect.Value) []trace.Change
	entries     []mappedEntry
	expectation func(string, *int) ([]changeRecord, error)
}

func newDiffMapping[C any](owner string, zero C, fixture string, normalize func(C) C, diff func(C, C) []trace.Change, entries []mappedEntry, expectation func(string, *int) ([]changeRecord, error)) (diffMapping, error) {
	seed := reflect.New(reflect.TypeOf(zero)).Elem()
	if err := decodeMappedValue(seed, fixture); err != nil {
		return diffMapping{}, errs.Wrapf(err, "%s seed", owner)
	}
	return diffMapping{owner, seed, func(v reflect.Value) reflect.Value { return reflect.ValueOf(normalize(v.Interface().(C))) }, func(a, b reflect.Value) []trace.Change { return diff(a.Interface().(C), b.Interface().(C)) }, entries, expectation}, nil
}

func diffMappingFor(owner string) (diffMapping, error) {
	switch owner {
	case "bridge":
		return bridgeDiffMapping()
	case "phy":
		return phyDiffMapping()
	case "loopprotect":
		return loopprotectDiffMapping()
	case "mcast":
		return mcastDiffMapping()
	case "lag":
		return lagDiffMapping()
	case "stp":
		return stpDiffMapping()
	case "filter":
		return filterDiffMapping()
	case "routing":
		return routingDiffMapping()
	case "traffic":
		return trafficDiffMapping()
	case "port":
		return portDiffMapping()
	case "vswitch":
		return vswitchDiffMapping()
	case "assembly":
		return assemblyDiffMapping()
	default:
		return diffMapping{}, errs.Msgf("unknown diff owner %s", owner)
	}
}

// AssertMappedDiffCoverage checks every submitted mutation and its independent sequence.
func AssertMappedDiffCoverage(t *testing.T, owner string) {
	t.Helper()
	m, err := diffMappingFor(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := mappedSeedProblems(m); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{"bridge": 20, "phy": 23, "loopprotect": 6, "mcast": 9, "lag": 18, "stp": 38, "filter": 23, "routing": 19, "traffic": 17, "port": 7, "vswitch": 190, "assembly": 257}
	if len(m.entries) != counts[owner] {
		t.Fatalf("registered %d paths, want %d", len(m.entries), counts[owner])
	}
	hits := 0
	for _, e := range m.entries {
		t.Run(e.Path, func(t *testing.T) {
			if err := checkMappedLeaf(m, e, &hits); err != nil {
				t.Error(err)
			}
		})
	}
	if hits != counts[owner] {
		t.Errorf("concrete expectations reached %d, want %d", hits, counts[owner])
	}
	if owner == "assembly" {
		assertInactiveFaultCoverage(t, m)
	}
}

func decodeMappedValue(v reflect.Value, raw string) error {
	var value any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return errs.Wrap(err, "decode fixture")
	}
	cp := reflect.New(v.Type()).Elem()
	if err := assignMappedValue(cp, value); err != nil {
		return err
	}
	v.Set(cp)
	return nil
}

func assignMappedValue(v reflect.Value, value any) error {
	if value == nil {
		v.SetZero()
		return nil
	}
	if v.Kind() == reflect.Pointer {
		v.Set(reflect.New(v.Type().Elem()))
		return assignMappedValue(v.Elem(), value)
	}
	if s, ok := value.(string); ok {
		if handled, err := assignMappedSpecialString(v, s); handled {
			return err
		}
	}
	if v.Type() == reflect.TypeOf(port.Table{}) {
		return assignMappedPortTable(v, value)
	}
	switch v.Kind() {
	case reflect.Struct:
		return assignMappedStruct(v, value)
	case reflect.Map:
		return assignMappedMap(v, value)
	case reflect.Slice:
		return assignMappedSlice(v, value)
	case reflect.String:
		return assignMappedString(v, value)
	case reflect.Bool:
		return assignMappedBool(v, value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return assignMappedInt(v, value)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return assignMappedUint(v, value)
	case reflect.Float32, reflect.Float64:
		return assignMappedFloat(v, value)
	default:
		return errs.Msgf("unsupported mapped leaf %s", v.Type())
	}
}

func assignMappedStruct(v reflect.Value, value any) error {
	fields, ok := value.(map[string]any)
	if !ok {
		return errs.Msgf("expected object for %s", v.Type())
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		f := v.FieldByName(name)
		if !f.IsValid() {
			return errs.Msgf("unknown fixture member %s.%s", v.Type(), name)
		}
		if err := assignMappedValue(f, fields[name]); err != nil {
			return errs.Wrapf(err, "member %s", name)
		}
	}
	return nil
}

func assignMappedMap(v reflect.Value, value any) error {
	fields, ok := value.(map[string]any)
	if !ok {
		return errs.Msgf("expected map for %s", v.Type())
	}
	v.Set(reflect.MakeMap(v.Type()))
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		key, err := mappedKey(v.Type().Key(), name)
		if err != nil {
			return err
		}
		val := reflect.New(v.Type().Elem()).Elem()
		if err := assignMappedValue(val, fields[name]); err != nil {
			return err
		}
		v.SetMapIndex(key, val)
	}
	return nil
}

func assignMappedSlice(v reflect.Value, value any) error {
	values, ok := value.([]any)
	if !ok {
		return errs.Msgf("expected list for %s", v.Type())
	}
	v.Set(reflect.MakeSlice(v.Type(), len(values), len(values)))
	for i, item := range values {
		if err := assignMappedValue(v.Index(i), item); err != nil {
			return err
		}
	}
	return nil
}

func assignMappedString(v reflect.Value, value any) error {
	s, ok := value.(string)
	if !ok {
		return errs.Msgf("expected string for %s", v.Type())
	}
	v.SetString(s)
	return nil
}

func assignMappedBool(v reflect.Value, value any) error {
	b, ok := value.(bool)
	if !ok {
		return errs.Msgf("expected bool for %s", v.Type())
	}
	v.SetBool(b)
	return nil
}

func assignMappedInt(v reflect.Value, value any) error {
	n, ok := value.(json.Number)
	if !ok {
		return errs.Msgf("expected integer for %s", v.Type())
	}
	i, err := n.Int64()
	if err != nil {
		return err
	}
	v.SetInt(i)
	return nil
}

func assignMappedUint(v reflect.Value, value any) error {
	n, ok := value.(json.Number)
	if !ok {
		return errs.Msgf("expected unsigned integer for %s", v.Type())
	}
	i, err := strconv.ParseUint(string(n), 10, 64)
	if err != nil {
		return err
	}
	v.SetUint(i)
	return nil
}

func assignMappedFloat(v reflect.Value, value any) error {
	n, ok := value.(json.Number)
	if !ok {
		return errs.Msgf("expected number for %s", v.Type())
	}
	f, err := n.Float64()
	if err != nil {
		return err
	}
	v.SetFloat(f)
	return nil
}

func mappedKey(t reflect.Type, text string) (reflect.Value, error) {
	v := reflect.New(t).Elem()
	var value any = text
	if t.Kind() != reflect.String && t != reflect.TypeOf(netip.Addr{}) {
		value = json.Number(text)
	}
	return v, assignMappedValue(v, value)
}

// mappedPathTokens keeps periods inside collection keys distinct from member separators.
func mappedPathTokens(path string) []string {
	var tokens []string
	for len(path) > 0 {
		if path[0] == '.' {
			path = path[1:]
			continue
		}
		if path[0] == '[' {
			i := strings.IndexByte(path, ']')
			tokens = append(tokens, path[:i+1])
			path = path[i+1:]
			continue
		}
		i := strings.IndexAny(path, ".[")
		if i < 0 {
			i = len(path)
		}
		tokens = append(tokens, path[:i])
		path = path[i:]
	}
	return tokens
}

func editMappedPath(v reflect.Value, tokens []string, raw string) error {
	if len(tokens) > 0 && tokens[0] == "@present" {
		tokens = tokens[1:]
	}
	if len(tokens) == 0 {
		return decodeMappedValue(v, raw)
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return errs.Msgf("required seed branch %s is nil", v.Type())
		}
		return editMappedPath(v.Elem(), tokens, raw)
	}
	token := tokens[0]
	if v.Type() == reflect.TypeOf(port.Table{}) {
		if token != "Ports()" {
			return errs.Msgf("opaque table requires Ports() adapter")
		}
		ports := v.Interface().(port.Table).Ports()
		name := strings.Trim(tokens[1], "[]")
		found := false
		for i := range ports {
			if ports[i].Name == name {
				found = true
				if err := editMappedPath(reflect.ValueOf(&ports[i]).Elem(), tokens[2:], raw); err != nil {
					return err
				}
				break
			}
		}
		if !found {
			return errs.Msgf("required table port %s absent", name)
		}
		b := port.NewBuilder()
		for _, p := range ports {
			b.Add(p)
		}
		table, err := b.Build()
		if err != nil {
			return err
		}
		v.Set(reflect.ValueOf(table))
		return nil
	}
	switch v.Kind() {
	case reflect.Struct:
		f := v.FieldByName(token)
		if !f.IsValid() {
			return errs.Msgf("unknown mapped member %s.%s", v.Type(), token)
		}
		return editMappedPath(f, tokens[1:], raw)
	case reflect.Map:
		key, err := mappedKey(v.Type().Key(), strings.Trim(token, "[]"))
		if err != nil {
			return err
		}
		val := v.MapIndex(key)
		if !val.IsValid() {
			return errs.Msgf("required map key %s absent", token)
		}
		if len(tokens) == 2 && tokens[1] == "@key" {
			newKey := reflect.New(v.Type().Key()).Elem()
			if err := decodeMappedValue(newKey, raw); err != nil {
				return err
			}
			if reflect.DeepEqual(key.Interface(), newKey.Interface()) {
				return errs.Msg("map key mutation did not change value")
			}
			v.SetMapIndex(newKey, deepCopyValue(val))
			v.SetMapIndex(key, reflect.Value{})
			return nil
		}
		cp := reflect.New(val.Type()).Elem()
		cp.Set(deepCopyValue(val))
		if err := editMappedPath(cp, tokens[1:], raw); err != nil {
			return err
		}
		v.SetMapIndex(key, cp)
		return nil
	case reflect.Slice:
		i, err := strconv.Atoi(strings.Trim(token, "[]"))
		if err != nil {
			return err
		}
		if i >= v.Len() {
			return errs.Msgf("required slice member %s absent", token)
		}
		return editMappedPath(v.Index(i), tokens[1:], raw)
	default:
		return errs.Msgf("unsupported mapped navigation through %s", v.Type())
	}
}

func prepareMappedInputs(m diffMapping, e mappedEntry) (reflect.Value, reflect.Value, error) {
	a := deepCopyValue(m.seed)
	prefix := m.owner + ".Config."
	tokens := mappedPathTokens(strings.TrimPrefix(e.Path, prefix))
	switch e.Setup {
	case "sequence":
		if err := editMappedPath(a, mappedPathTokens("Cables[0].Cable.Fault.Kind"), `"LoseSequence"`); err != nil {
			return a, a, err
		}
	case "fault-none":
		if err := editMappedPath(a, mappedPathTokens("Cables[0].Cable.Fault.Kind"), `"None"`); err != nil {
			return a, a, err
		}
	case "switch-key":
		if err := editMappedPath(a, mappedPathTokens("Switches[sw1]"), `{"MAC":"02:00:00:00:00:01"}`); err != nil {
			return a, a, err
		}
	case "port-wrapper":
		p := ""
		if m.owner == "assembly" {
			p = "Switches[sw1]."
		}
		if err := editMappedPath(a, mappedPathTokens(p+"Ports"), `[{"Name":"1/1/1","IfIndex":1,"Kind":"Other","AdminStatus":"Up","OperStatus":"Up","MTU":1500,"LagParent":"lag1"},{"Name":"lag1","Kind":"Lag","AdminStatus":"Up","OperStatus":"Up"}]`); err != nil {
			return a, a, err
		}
		if err := editMappedPath(a, mappedPathTokens(p+"LAG.LAGs[lag1].Members"), `{"1/1/1":{"Priority":128,"Key":1},"1/1/2":{"Priority":128,"Key":1}}`); err != nil {
			return a, a, err
		}
	}
	if !strings.HasSuffix(e.Path, ".@key") {
		if err := editMappedPath(a, tokens, e.Before); err != nil {
			return a, a, err
		}
	}
	b := deepCopyValue(a)
	if err := editMappedPath(b, tokens, e.After); err != nil {
		return a, b, err
	}
	if reflect.DeepEqual(a.Interface(), b.Interface()) {
		return a, b, errs.Msgf("mutation of %s did not change submitted input", e.Path)
	}
	return a, b, nil
}

func checkMappedLeaf(m diffMapping, e mappedEntry, hits *int) error {
	a, b, err := prepareMappedInputs(m, e)
	if err != nil {
		return errs.Wrapf(err, "%s", e.Path)
	}
	want, err := m.expectation(e.Path, hits)
	if err != nil {
		return err
	}
	got := m.diff(m.normalize(a), m.normalize(b))
	if len(want) == 0 {
		if e.Reason == "" {
			return errs.Msgf("empty expectation for %s has no disposition reason", e.Path)
		}
		return matchMappedChanges(got, want)
	}
	return matchMappedChanges(got, want)
}

func matchMappedChanges(got []trace.Change, want []changeRecord) error {
	if len(got) != len(want) {
		return errs.Msgf("diff records %d, want %d", len(got), len(want))
	}
	for i, expected := range want {
		actual := got[i]
		if string(actual.Layer) != expected.Layer || actual.Subject.Kind != expected.Kind || actual.Subject.Key != expected.Key || actual.Field != expected.Field || !matchesMappedFact(actual.From, expected.From) || !matchesMappedFact(actual.To, expected.To) {
			return errs.Msgf("diff record %d = (%s, %s, %q, %q, %v, %v), want %+v", i, actual.Layer, actual.Subject.Kind, actual.Subject.Key, actual.Field, actual.From, actual.To, expected)
		}
	}
	return nil
}

func matchesMappedFact(got trace.Fact, want *[2]string) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return got.TypeID() == want[0] && got.Canonical() == want[1]
}

func structuralMappedPaths(t reflect.Type, path string, out *[]string) {
	if t == reflect.TypeOf(port.Table{}) {
		structuralMappedPaths(reflect.TypeOf(port.Port{}), path+".Ports()[*]", out)
		return
	}
	if atomicLeafTypes[t] || t == reflect.TypeOf(netaddr.MAC{}) {
		*out = append(*out, path)
		return
	}
	switch t.Kind() {
	case reflect.Pointer:
		*out = append(*out, path+".@present")
		structuralMappedPaths(t.Elem(), path, out)
	case reflect.Map:
		*out = append(*out, path+"[*].@key")
		structuralMappedPaths(t.Elem(), path+"[*]", out)
	case reflect.Slice:
		structuralMappedPaths(t.Elem(), path+"[*]", out)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).IsExported() {
				structuralMappedPaths(t.Field(i).Type, path+"."+t.Field(i).Name, out)
			}
		}
	default:
		*out = append(*out, path)
	}
}

func structuralMappedPath(path string) string {
	tokens := mappedPathTokens(path)
	var b strings.Builder
	for _, token := range tokens {
		if strings.HasPrefix(token, "[") {
			b.WriteString("[*]")
		} else {
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(token)
		}
	}
	return b.String()
}

func mappedSeedProblems(m diffMapping) error {
	var structural []string
	structuralMappedPaths(m.seed.Type(), m.owner+".Config", &structural)
	registered := make(map[string]bool)
	for _, e := range m.entries {
		p := structuralMappedPath(e.Path)
		if registered[p] {
			return errs.Msgf("duplicate structural mapping %s", p)
		}
		registered[p] = true
	}
	for _, path := range structural {
		if !registered[path] {
			return errs.Msgf("unmapped structural path %s", path)
		}
	}
	for path := range registered {
		if !slices.Contains(structural, path) {
			return errs.Msgf("mapping names absent structural path %s", path)
		}
	}
	// No slot-local override runs until the complete common input is checked.
	for _, e := range m.entries {
		if strings.HasSuffix(e.Path, ".@key") {
			continue
		}
		tokens := mappedPathTokens(strings.TrimPrefix(e.Path, m.owner+".Config."))
		if err := probeMappedPath(m.seed, tokens); err != nil {
			return errs.Wrapf(err, "%s", e.Path)
		}
	}
	return nil
}

func probeMappedPath(v reflect.Value, tokens []string) error {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return errs.Msgf("required seed branch %s is nil", v.Type())
		}
		return probeMappedPath(v.Elem(), tokens)
	}
	if len(tokens) == 0 || tokens[0] == "@present" {
		return nil
	}
	token := tokens[0]
	if v.Type() == reflect.TypeOf(port.Table{}) {
		for _, p := range v.Interface().(port.Table).Ports() {
			if p.Name == strings.Trim(tokens[1], "[]") {
				return probeMappedPath(reflect.ValueOf(p), tokens[2:])
			}
		}
		return errs.Msgf("required opaque port %s absent", tokens[1])
	}
	switch v.Kind() {
	case reflect.Struct:
		f := v.FieldByName(token)
		if !f.IsValid() {
			return errs.Msgf("unknown member %s", token)
		}
		return probeMappedPath(f, tokens[1:])
	case reflect.Map:
		key, err := mappedKey(v.Type().Key(), strings.Trim(token, "[]"))
		if err != nil {
			return err
		}
		value := v.MapIndex(key)
		if !value.IsValid() {
			return errs.Msgf("required map seed %s absent", token)
		}
		return probeMappedPath(value, tokens[1:])
	case reflect.Slice:
		i, err := strconv.Atoi(strings.Trim(token, "[]"))
		if err != nil {
			return err
		}
		if i >= v.Len() {
			return errs.Msgf("required slice seed %s absent", token)
		}
		return probeMappedPath(v.Index(i), tokens[1:])
	default:
		return errs.Msgf("unsupported seed navigation %s", v.Type())
	}
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
		return nil, seedVal, errs.Msgf("the walk found no perturbable leaf in %T; a config with nothing to check would "+
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

// checkLeaf compares a submitted scalar mutation against independently supplied facts.
// Its returned error lets fixture tests assert failure without a failing nested test.
func checkLeaf[C any](seed C, seedVal reflect.Value, l coverageLeaf, normalize func(C) C, diff func(a, b C) []trace.Change, want []changeRecord) error {
	perturbedRoot := deepCopyValue(seedVal)
	leafVal, commit := navigateForMutation(perturbedRoot, l.path)
	origLeafVal := navigateReadOnly(seedVal, l.path)
	origCopy := reflect.New(origLeafVal.Type()).Elem()
	origCopy.Set(origLeafVal)

	if err := mutateLeaf(leafVal); err != nil {
		return errs.Wrapf(err, "perturbing %s", l.displayPath)
	}
	commit()

	if reflect.DeepEqual(origCopy.Interface(), leafVal.Interface()) {
		return errs.Msgf("perturbing %s produced the same value (%v); the mutation strategy for %s needs a different value here",
			l.displayPath, leafVal.Interface(), leafVal.Type())
	}

	perturbed, ok := perturbedRoot.Interface().(C)
	if !ok {
		return errs.Msgf("perturbed copy is %T, want %T", perturbedRoot.Interface(), seed)
	}

	return matchMappedChanges(diff(normalize(seed), normalize(perturbed)), want)
}

// mustBuildPortTable builds a Table from p and its fixed companions. It panics if a
// perturbation violates Table.Validate. The mutation strategies preserve that invariant
// by toggling enums toward zero and retaining a companion LAG port for lag_parent. The
// panic uses the proven handling answer: TestDiffCoversEveryConfigField in
// src/common/sim/port/diff_coverage_test.go exercises every mutation on each
// test run.
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
		return errs.Wrapf(err, "perturbing %s", l.displayPath)
	}
	commit()

	if reflect.DeepEqual(origCopy.Interface(), leafVal.Interface()) {
		return errs.Msgf("perturbing %s produced the same value (%v); the mutation strategy for %s needs a different value here",
			l.displayPath, leafVal.Interface(), leafVal.Type())
	}

	perturbed, ok := perturbedRoot.Interface().(C)
	if !ok {
		return errs.Msgf("perturbed copy is %T, want %T", perturbedRoot.Interface(), seed)
	}

	if keyFn(seed) == keyFn(perturbed) {
		return errs.Msgf("retention key reported no change after perturbing %s", l.displayPath)
	}
	return nil
}
