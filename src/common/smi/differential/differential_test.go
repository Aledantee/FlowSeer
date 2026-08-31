package differential_test

import (
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"go.aledante.io/FlowSeer/src/common/smi"
	"go.aledante.io/FlowSeer/src/common/smi/differential"
)

var updateCensus = flag.Bool("update-census", false,
	"rewrite the committed corpus outcome census from the current corpus")

const (
	// repoRoot is the FlowSeer checkout, relative to this package.
	repoRoot = "../../../.."

	// corpusRoot is the vendor MIB corpus.
	corpusRoot = repoRoot + "/spec/mib"

	// censusFixture holds the committed per-vendor outcome counts.
	censusFixture = "testdata/census.txt"

	// mibgenConfig lists the modules the generator is configured for,
	// which is the set the byte-identical-output claim rests on.
	mibgenConfig = repoRoot + "/mibgen.yaml"
)

// TestGosmiCorpusCensus puts every corpus file through gosmi and commits
// the per-vendor loaded, failed and panicked counts.
//
// The counts are the reason this module exists. Nothing else in the
// repository can say how much of the corpus the predecessor could read,
// and the panic count in particular is not a performance note: a panic
// takes the process that asked, so every one of those files is one the
// generator could not have been pointed at without first patching the
// MIB.
func TestGosmiCorpusCensus(t *testing.T) {
	pass := corpusPass(t)
	total := pass.census.Total()

	t.Logf("put %d files through gosmi in %v", total.Files(), pass.elapsed)
	t.Logf("loaded %d, failed %d, panicked %d", total.Loaded, total.Failed, total.Panicked)
	for _, vendor := range slices.Sorted(maps.Keys(pass.census)) {
		v := pass.census[vendor]
		t.Logf("%s: loaded %d, failed %d, panicked %d", vendor, v.Loaded, v.Failed, v.Panicked)
	}
	for _, cause := range slices.Sorted(maps.Keys(pass.panics)) {
		t.Logf("panic at %s: %d files", cause, pass.panics[cause])
	}

	if testing.Short() {
		t.Log("short mode read one file per vendor, so the census is not comparable and was not checked")

		return
	}

	msg, err := differential.CompareOrUpdate(censusFixture, pass.census.Render(), *updateCensus)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if msg != "" {
		t.Error(msg)
	}
}

// TestCensusCoversEveryVendor pins the per-vendor half of the report: a
// vendor that ships MIBs but draws no line has been dropped from the
// walk, and the census would then read as an improvement.
func TestCensusCoversEveryVendor(t *testing.T) {
	pass := corpusPass(t)

	entries, err := os.ReadDir(corpusRoot)
	if err != nil {
		t.Fatalf("reading %s: %v", corpusRoot, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		v := pass.census[e.Name()]
		if v == nil {
			t.Errorf("%s ships MIBs but the census has no line for it", e.Name())

			continue
		}
		if v.Files() == 0 {
			t.Errorf("%s: the census counted no files", e.Name())
		}
	}
}

// TestProjectionAgreesWithGosmi requires every module gosmi loads to
// compare equal over the projection, apart from the divergences
// [differential.ExpectedDivergences] records.
//
// A divergence outside that set is not evidence against either side on
// its own. It is adjudicated against the RFC clause text and lands as
// either a new expected divergence with its citation or a parser bug;
// neither implementation is authoritative by default.
func TestProjectionAgreesWithGosmi(t *testing.T) {
	pass := corpusPass(t)

	t.Logf("projection: %v", differential.Fields())
	for _, clause := range slices.Sorted(maps.Keys(differential.Excluded)) {
		t.Logf("outside the projection, %s: %s", clause, differential.Excluded[clause])
	}

	byRule := map[string]int{}
	var unexpected []differential.Divergence
	for _, d := range pass.divergences {
		if name, ok := classify(d); ok {
			byRule[name]++

			continue
		}
		unexpected = append(unexpected, d)
	}

	for _, rule := range differential.ExpectedDivergences() {
		t.Logf("%s at fault, %s: %d occurrences", rule.Fault, rule.Name, byRule[rule.Name])
	}
	for _, key := range slices.Sorted(maps.Keys(differential.AdjudicatedInstances)) {
		t.Logf("source at fault, %s: %s (%d occurrences)",
			key, differential.AdjudicatedInstances[key], byRule[key])
	}
	t.Logf("compared %d modules, %d divergences, %d unexpected", pass.compared, len(pass.divergences), len(unexpected))
	for _, f := range differential.Fields() {
		if n := pass.shortfall[f]; n > 0 {
			t.Logf("gosmi supplied no %s in %d places the parser did", f, n)
		}
	}

	if len(pass.missing) > 0 {
		t.Errorf("gosmi loaded %d modules the parser did not produce: %v",
			len(pass.missing), first(pass.missing, 10))
	}

	// Grouped by field rather than listed flat, because an unexpected
	// divergence is adjudicated one clause at a time: what a reviewer
	// needs first is which clause moved and a handful of instances to
	// read the RFC against, not the first forty rows of one module.
	differential.SortDivergences(unexpected)
	byField := map[differential.Field][]differential.Divergence{}
	for _, d := range unexpected {
		byField[d.Field] = append(byField[d.Field], d)
	}
	for _, f := range differential.Fields() {
		ds := byField[f]
		if len(ds) == 0 {
			continue
		}
		t.Errorf("%d unexpected divergences in %s, for example:", len(ds), f)
		for _, d := range first(ds, unexpectedSamplesPerField) {
			t.Errorf("  %v", d)
		}
	}
}

// unexpectedSamplesPerField is how many instances of an unadjudicated
// clause a failure prints. Enough to see whether they are one shape or
// several, and not so many that one clause buries the next.
const unexpectedSamplesPerField = 6

// TestEveryExpectedDivergenceStillOccurs fails on a recorded divergence
// the corpus no longer produces, so the list cannot rot into a
// description of a dependency nobody is running any more.
func TestEveryExpectedDivergenceStillOccurs(t *testing.T) {
	if testing.Short() {
		t.Skip("a rule can legitimately draw nothing from one file per vendor")
	}

	pass := corpusPass(t)

	seen := map[string]bool{}
	for _, d := range pass.divergences {
		if name, ok := classify(d); ok {
			seen[name] = true
		}
	}

	for _, rule := range differential.ExpectedDivergences() {
		if !seen[rule.Name] {
			t.Errorf("expected divergence %q no longer occurs anywhere in the corpus; "+
				"either it was fixed or the rule never described anything. Reason on file: %s",
				rule.Name, rule.Reason)
		}
	}

	for _, key := range slices.Sorted(maps.Keys(differential.AdjudicatedInstances)) {
		if !seen[key] {
			t.Errorf("adjudicated instance %q no longer diverges; "+
				"either it was fixed or the entry never described anything. Reading on file: %s",
				key, differential.AdjudicatedInstances[key])
		}
	}
}

// TestUnexpectedDivergenceNamesTheModuleAndField injects a model change
// outside the expected set and requires the comparison to report it and
// to say which module and which field moved.
//
// A comparison that only says "the models differ" costs a reviewer the
// whole corpus to find the one declaration that moved, so the message is
// itself part of what this harness promises.
func TestUnexpectedDivergenceNamesTheModuleAndField(t *testing.T) {
	ours := differential.Projection{
		Module: "ACME-MIB",
		Nodes: map[string]differential.Subject{
			"acmeUptime": {OID: "1.3.6.1.4.1.9999.1", Kind: "scalar", Access: "read-only", Status: "current"},
		},
		Types: map[string]differential.Subject{},
	}
	theirs := differential.Projection{
		Module: "ACME-MIB",
		Nodes: map[string]differential.Subject{
			"acmeUptime": {OID: "1.3.6.1.4.1.9999.2", Kind: "scalar", Access: "read-only", Status: "current"},
		},
		Types: map[string]differential.Subject{},
	}

	got, shortfall := differential.Compare(ours, theirs)
	if len(shortfall) != 0 {
		t.Fatalf("an injected OID change was counted as a shortfall: %v", shortfall)
	}
	if len(got) != 1 {
		t.Fatalf("got %d divergences, want 1: %v", len(got), got)
	}
	if _, ok := classify(got[0]); ok {
		t.Fatalf("an injected OID change matched an expected divergence: %v", got[0])
	}

	msg := got[0].String()
	for _, want := range []string{"ACME-MIB", "acmeUptime", string(differential.FieldOID)} {
		if !strings.Contains(msg, want) {
			t.Errorf("the divergence does not name %q: %s", want, msg)
		}
	}
}

// TestPanickingModuleIsRecordedAndTheRunContinues pins the property the
// whole census rests on: gosmi panics on DEFVAL { { } }, which RFC 2578
// §7.9 spells out as the way to say no bits are set, and that panic has
// to cost one file rather than the process.
func TestPanickingModuleIsRecordedAndTheRunContinues(t *testing.T) {
	dirs, err := differential.CorpusDirs(corpusRoot)
	if err != nil {
		t.Skipf("no MIB corpus at %s: %v", corpusRoot, err)
	}

	panicking := filepath.Join(corpusRoot, "ieee", "lldp.mib")
	if _, err := os.Stat(panicking); err != nil {
		t.Skipf("%s is not in the corpus: %v", panicking, err)
	}

	_, outcome, detail := differential.LoadWithGosmi(panicking, dirs)
	if outcome != differential.OutcomePanicked {
		t.Fatalf("%s came back %s, want panicked (detail %q)", panicking, outcome, detail)
	}

	after := filepath.Join(corpusRoot, "ietf", "SNMPv2-MIB")
	if _, err := os.Stat(after); err != nil {
		t.Skipf("%s is not in the corpus: %v", after, err)
	}
	if _, outcome, detail := differential.LoadWithGosmi(after, dirs); outcome != differential.OutcomeLoaded {
		t.Fatalf("the load after a panic came back %s (detail %q); the panic left gosmi's state broken",
			outcome, detail)
	}
}

// TestConfiguredModulesHaveNoBitsNumberingGap checks the assumption the
// generator's current output rests on.
//
// The generator compensates for gosmi's discarded BITS numbers by
// assigning positions from declaration order. That is right for every
// BITS type it renders today only because all of them number
// consecutively from zero. A gap anywhere in the configured modules
// would mean the committed output is already wrong, and that a cutover
// producing byte-identical output would be preserving the error rather
// than proving the replacement.
func TestConfiguredModulesHaveNoBitsNumberingGap(t *testing.T) {
	modules, searchPaths := mibgenModules(t)

	set, err := smi.Load(modules, smi.Options{SearchPaths: searchPaths})
	if err != nil {
		t.Fatalf("loading the configured modules: %v", err)
	}

	checked := 0
	for _, name := range modules {
		mod, ok := set.Module(name)
		if !ok {
			t.Errorf("%s is configured but did not resolve", name)

			continue
		}

		for _, ty := range mod.Types {
			if ty.Base != smi.BaseBits {
				continue
			}
			checked++
			if gap := numberingGap(ty.Members); gap != "" {
				t.Errorf("%s.%s: %s. The generator assigns BITS positions from declaration order, "+
					"so the committed output for this type is wrong and byte-identical output would "+
					"preserve the error", name, ty.Name, gap)
			}
		}

		for _, n := range mod.Nodes {
			if n.Type == nil || n.Type.Base != smi.BaseBits || n.Type.Name != "" {
				continue
			}
			checked++
			if gap := numberingGap(n.Type.Members); gap != "" {
				t.Errorf("%s.%s: %s. The generator assigns BITS positions from declaration order, "+
					"so the committed output for this object is wrong and byte-identical output "+
					"would preserve the error", name, n.Name, gap)
			}
		}
	}

	t.Logf("checked %d BITS types across %d configured modules", checked, len(modules))
	if checked == 0 {
		t.Error("no BITS type was checked, so the check proves nothing")
	}
}

// numberingGap returns an empty string when the members number
// consecutively from zero, and otherwise says where the run breaks.
func numberingGap(members []smi.Member) string {
	numbers := make([]int64, 0, len(members))
	for _, m := range members {
		numbers = append(numbers, m.Number)
	}
	slices.Sort(numbers)

	for i, n := range numbers {
		if n != int64(i) {
			return fmt.Sprintf("bit numbering is %v, which is not 0..%d", numbers, len(numbers)-1)
		}
	}

	return ""
}

// TestParserPackageGraphDoesNotReachGosmi requires the parser and
// everything it imports to be free of gosmi.
//
// The check is deliberately scoped to src/common/smi/... rather than to
// the whole main module, and the scope is the honest one for today: the
// generator under src/common/snmp/cmd/mibgen still renders through gosmi
// and still carries the import. What this test can assert now is that
// the replacement itself never took the dependency on, so the generator
// is the only thing left holding it.
func TestParserPackageGraphDoesNotReachGosmi(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "./src/common/smi/...")
	cmd.Dir = repoRoot

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps in %s: %v", repoRoot, err)
	}

	for _, pkg := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.Contains(pkg, "gosmi") {
			t.Errorf("the parser's package graph reaches %s", pkg)
		}
	}
}

// classify returns the name of the expected divergence d matches.
func classify(d differential.Divergence) (string, bool) {
	for _, rule := range differential.ExpectedDivergences() {
		if rule.Match(d) {
			return rule.Name, true
		}
	}
	if key := differential.InstanceKey(d); differential.AdjudicatedInstances[key] != "" {
		return key, true
	}

	return "", false
}

func first[T any](xs []T, n int) []T {
	if len(xs) <= n {
		return xs
	}

	return xs[:n]
}

// pass is one walk of the corpus through both implementations: the
// outcome census, what the panicking files panicked on, and every
// divergence over the projection.
//
// One walk rather than one per test, because gosmi re-reads a module's
// whole import closure on every load and the walk is minutes rather than
// seconds.
type pass struct {
	census      differential.Census
	panics      map[string]int
	divergences []differential.Divergence
	shortfall   map[differential.Field]int
	missing     []string
	compared    int
	elapsed     time.Duration
}

var corpusOnce = sync.OnceValues(runPass)

func corpusPass(t *testing.T) *pass {
	t.Helper()

	p, err := corpusOnce()
	if err != nil {
		t.Skipf("%v", err)
	}

	return p
}

// runPass walks the corpus once. It is single-goroutine because gosmi
// keeps its module universe in package-level state.
func runPass() (*pass, error) {
	files, err := differential.CorpusFiles(corpusRoot)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no MIB sources under %s", corpusRoot)
	}
	dirs, err := differential.CorpusDirs(corpusRoot)
	if err != nil {
		return nil, err
	}
	if testing.Short() {
		files = largestPerVendor(files)
	}

	restore, err := silenceStdout()
	if err != nil {
		return nil, err
	}
	defer restore()

	p := &pass{
		census:    differential.Census{},
		panics:    map[string]int{},
		shortfall: map[differential.Field]int{},
	}
	start := time.Now()

	for _, cf := range files {
		theirs, outcome, detail := differential.LoadWithGosmi(cf.Path, dirs)
		p.census.Record(cf.Vendor, outcome)
		if outcome == differential.OutcomePanicked {
			p.panics[differential.PanicDetail(detail)]++
		}
		if outcome != differential.OutcomeLoaded {
			continue
		}

		set, err := smi.LoadFiles([]string{cf.Path}, smi.Options{Workers: 1, SearchPaths: dirs})
		if err != nil {
			return nil, fmt.Errorf("the parser could not read %s: %w", cf.Path, err)
		}
		ours, ok := set.Module(theirs.Module)
		if !ok {
			p.missing = append(p.missing, theirs.Module+" ("+cf.Path+")")

			continue
		}

		p.compared++
		ds, shortfall := differential.Compare(differential.ProjectSMI(ours), *theirs)
		p.divergences = append(p.divergences, ds...)
		for f, n := range shortfall {
			p.shortfall[f] += n
		}
	}

	p.elapsed = time.Since(start)

	return p, nil
}

// silenceStdout redirects os.Stdout to the null device for the duration
// of a corpus walk.
//
// gosmi prints the error of every module it cannot load straight to
// standard output rather than only returning it, so a walk would
// otherwise bury the test's own output under a line per unreadable file.
// The returned function restores the previous stdout, which the test
// framework needs back before it flushes.
func silenceStdout() (func(), error) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", os.DevNull, err)
	}

	saved := os.Stdout
	os.Stdout = devnull

	return func() {
		os.Stdout = saved
		_ = devnull.Close()
	}, nil
}

// largestPerVendor picks the largest file each vendor ships, which is
// the one most likely to carry a spelling neither implementation has
// seen.
func largestPerVendor(files []differential.CorpusFile) []differential.CorpusFile {
	best := map[string]differential.CorpusFile{}
	for _, cf := range files {
		cur, ok := best[cf.Vendor]
		if !ok || cf.Size > cur.Size || (cf.Size == cur.Size && cf.Path < cur.Path) {
			best[cf.Vendor] = cf
		}
	}

	out := make([]differential.CorpusFile, 0, len(best))
	for _, vendor := range slices.Sorted(maps.Keys(best)) {
		out = append(out, best[vendor])
	}

	return out
}

// mibgenModules reads the generator's configuration and returns the
// module names it is configured for together with its search paths,
// resolved from this package.
func mibgenModules(t *testing.T) ([]string, []string) {
	t.Helper()

	raw, err := os.ReadFile(mibgenConfig)
	if err != nil {
		t.Skipf("no generator configuration at %s: %v", mibgenConfig, err)
	}

	var cfg struct {
		SearchPaths []string `yaml:"search_paths"`
		Modules     []struct {
			Name string `yaml:"name"`
		} `yaml:"modules"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parsing %s: %v", mibgenConfig, err)
	}

	modules := make([]string, 0, len(cfg.Modules))
	for _, m := range cfg.Modules {
		modules = append(modules, m.Name)
	}

	paths := make([]string, 0, len(cfg.SearchPaths))
	for _, p := range cfg.SearchPaths {
		paths = append(paths, filepath.Join(repoRoot, p))
	}

	return modules, paths
}
