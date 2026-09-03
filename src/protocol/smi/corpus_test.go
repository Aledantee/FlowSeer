package smi_test

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// The corpus harness is the evidence behind the leniency claim: a
// malformed vendor MIB costs one declaration rather than the file. A
// hand-written fixture suite cannot make that claim, because the shapes
// that break a parser are the ones nobody thought to write down. The
// vendored corpus is a stress bar rather than an adoption target — its
// job is to exercise the deviation space at a scale fixtures do not
// reach — so what it produces is committed as a per-vendor snapshot and
// reviewed as a diff.
//
// Every file is measured on its own. The load follows the file's IMPORTS
// through the corpus, because a module read without the textual
// conventions it imports leaves every object unresolved and the
// measurement then says more about the search path than about the MIB.
// What the file itself declared is what gets counted: modules, nodes and
// diagnostics are attributed by the file they came from, so an imported
// module never lands in the importer's vendor.
//
// A worker owns one file's whole load and drops it before taking the
// next, so what the run holds at any moment is one import closure and a
// handful of counters, not a corpus.

var updateCorpus = flag.Bool("update-corpus", false,
	"rewrite the committed per-vendor corpus snapshots from the current corpus")

// corpusRoot is the vendor MIB corpus, relative to this package.
const corpusRoot = "../../../spec/mib"

// corpusSnapshotDir holds one committed snapshot per vendor directory.
const corpusSnapshotDir = "testdata/corpus"

// lcosFamilyDir is the one directory the default run collapses. LANCOM
// ships every LCOS release as a full OID dump, so the directory is
// fifteen near-copies of its newest member and roughly half the corpus
// by bytes. Reading all of it on every `go test` buys nothing the newest
// member does not already say, which
// TestCorpusDeduplicationHoldsAcrossTheLCOSFamily is there to keep true.
const lcosFamilyDir = "lancom/lcos"

// lcosRetained is the family member the deduplicated corpus keeps: the
// newest release LANCOM ships here.
const lcosRetained = "LC-UNIFIED-LCOS-10-94-REL-OIDS.mib"

// poolSizeSampleStride is how much of the corpus the pool-size check
// reads: one file in eight, plus one from every vendor so that a vendor
// shipping fewer than eight files is not skipped.
const poolSizeSampleStride = 8

// corpusFileBudget is how long one file may take before the run is
// treated as hung rather than slow. The largest file in the corpus
// frames, parses and resolves in tens of milliseconds, so a file over
// this budget is not a file that got unlucky.
const corpusFileBudget = 30 * time.Second

// corpusFile is one MIB source and the vendor authority it belongs to.
type corpusFile struct {
	path   string
	vendor string
	size   int64
}

// corpusCodeKey identifies one row of a snapshot's diagnostic
// histogram. Severity is part of the key rather than a property of the
// code so that a regrading shows up as a moved row instead of a silent
// change of meaning under a row that still reads the same.
type corpusCodeKey struct {
	code     errs.Code
	severity smi.Severity
}

// corpusStats is what one vendor's files added up to. Everything in it
// is a count, so folding two of them together is order-independent and
// the snapshot does not depend on which worker read which file.
type corpusStats struct {
	files       int
	bytes       int64
	modules     int
	nodes       int
	unresolved  int
	fatalFiles  int
	diagnostics int
	bySeverity  map[smi.Severity]int
	byCode      map[corpusCodeKey]int
}

func newCorpusStats() *corpusStats {
	return &corpusStats{
		bySeverity: map[smi.Severity]int{},
		byCode:     map[corpusCodeKey]int{},
	}
}

func (s *corpusStats) add(other *corpusStats) {
	s.files += other.files
	s.bytes += other.bytes
	s.modules += other.modules
	s.nodes += other.nodes
	s.unresolved += other.unresolved
	s.fatalFiles += other.fatalFiles
	s.diagnostics += other.diagnostics
	for severity, n := range other.bySeverity {
		s.bySeverity[severity] += n
	}
	for key, n := range other.byCode {
		s.byCode[key] += n
	}
}

// render writes the snapshot in its committed form: the totals first,
// then every severity level including the ones that drew nothing, then
// one line per diagnostic code and severity sorted by code. The
// zero-count severity lines stay in so a vendor's snapshot keeps its
// shape as its content changes, which is what makes the diff readable.
//
// File bytes are deliberately absent: a re-sync that reformats a MIB
// without changing what it declares should not show up as a snapshot
// change.
func (s *corpusStats) render() string {
	var b strings.Builder

	fmt.Fprintf(&b, "files %d\n", s.files)
	fmt.Fprintf(&b, "modules %d\n", s.modules)
	fmt.Fprintf(&b, "declarations %d\n", s.nodes)
	fmt.Fprintf(&b, "unresolved-declarations %d\n", s.unresolved)
	fmt.Fprintf(&b, "files-with-fatal %d\n", s.fatalFiles)

	for _, severity := range smi.Severities() {
		fmt.Fprintf(&b, "severity %s %d\n", severity, s.bySeverity[severity])
	}

	keys := make([]corpusCodeKey, 0, len(s.byCode))
	for key := range s.byCode {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b corpusCodeKey) int {
		if a.code != b.code {
			return cmp.Compare(a.code, b.code)
		}

		return cmp.Compare(a.severity, b.severity)
	})

	for _, key := range keys {
		fmt.Fprintf(&b, "diagnostic %s %s %d\n", key.code, key.severity, s.byCode[key])
	}

	return b.String()
}

// codes returns the set of diagnostic codes the stats saw, which is what
// the deduplication check compares across the LCOS family. Counts are
// left out on purpose: two releases of the same MIB differ in how many
// objects they declare, so comparing counts would report a difference
// that says nothing about whether reading both is worth the seconds.
func (s *corpusStats) codes() []string {
	seen := map[string]bool{}
	for key := range s.byCode {
		seen[string(key.code)] = true
	}

	out := make([]string, 0, len(seen))
	for code := range seen {
		out = append(out, code)
	}
	slices.Sort(out)

	return out
}

// TestCorpusSnapshot loads every file in the deduplicated corpus and
// compares the per-vendor diagnostic histogram against the committed
// fixture.
//
// A regression here is a change in what the parser makes of real vendor
// MIBs, which is the property this package exists for, so it arrives as
// a reviewable diff rather than a number in a log line.
func TestCorpusSnapshot(t *testing.T) {
	files := deduplicated(corpusFiles(t))
	if testing.Short() {
		files = oneFilePerVendor(files)
	}

	start := time.Now()
	byVendor, overall := scanCorpus(t, files, 0)
	t.Logf("loaded %d files (%.1f MB) in %v across %d workers",
		overall.files, float64(overall.bytes)/(1<<20), time.Since(start), runtime.GOMAXPROCS(0))
	t.Logf("%d modules, %d declarations, %d unresolved, %d diagnostics, %d files with a fatal",
		overall.modules, overall.nodes, overall.unresolved, overall.diagnostics, overall.fatalFiles)
	for _, severity := range smi.Severities() {
		t.Logf("severity %s: %d", severity, overall.bySeverity[severity])
	}

	if testing.Short() {
		t.Logf("short mode read one file per vendor, so the snapshots are not comparable and were not checked")

		return
	}

	for _, vendor := range slices.Sorted(maps.Keys(byVendor)) {
		msg, err := compareOrUpdate(corpusSnapshotDir, vendor, byVendor[vendor].render(), *updateCorpus)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if msg != "" {
			t.Error(msg)
		}
	}

	checkSnapshotsCoverTheCorpus(t, byVendor)
}

// checkSnapshotsCoverTheCorpus fails on a committed snapshot no vendor
// directory produces any more, which is how a vendor removed from the
// corpus stops leaving a fixture behind that nothing ever checks again.
func checkSnapshotsCoverTheCorpus(t *testing.T, byVendor map[string]*corpusStats) {
	t.Helper()

	entries, err := os.ReadDir(corpusSnapshotDir)
	if err != nil {
		t.Fatalf("reading %s: %v", corpusSnapshotDir, err)
	}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".snapshot") {
			continue
		}
		vendor := strings.TrimSuffix(name, ".snapshot")
		if byVendor[vendor] == nil {
			t.Errorf("%s/%s has no vendor directory under %s any more; delete it",
				corpusSnapshotDir, name, corpusRoot)
		}
	}
}

// TestCorpusPoolSizeIsInvisible reads a slice of the corpus twice, once
// single-threaded and once on every core, with the file order shuffled
// between the runs, and requires byte-identical snapshots.
//
// The snapshot is the thing a reviewer trusts, so it has to be a
// property of the corpus rather than of the machine that read it.
//
// A sample rather than the corpus, because the single-threaded pass is
// the slowest thing in the default run and what could make the two runs
// disagree is how the counters fold together, which a couple of hundred
// files across every vendor exercises as well as sixteen hundred would.
func TestCorpusPoolSizeIsInvisible(t *testing.T) {
	if testing.Short() {
		t.Skip("reads its slice of the corpus twice")
	}

	all := deduplicated(corpusFiles(t))
	files := oneFilePerVendor(all)
	picked := map[string]bool{}
	for _, cf := range files {
		picked[cf.path] = true
	}
	for i := 0; i < len(all); i += poolSizeSampleStride {
		if !picked[all[i].path] {
			files = append(files, all[i])
		}
	}

	serial, _ := scanCorpus(t, files, 1)

	if len(serial) != len(vendorsOf(all)) {
		t.Errorf("the sample spans %d vendors of %d", len(serial), len(vendorsOf(all)))
	}

	shuffled := slices.Clone(files)
	r := rand.New(rand.NewPCG(0x5EED, 0xC0FFEE))
	r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	parallel, _ := scanCorpus(t, shuffled, runtime.NumCPU())

	for _, vendor := range slices.Sorted(maps.Keys(serial)) {
		if parallel[vendor] == nil {
			t.Errorf("%s: the parallel run saw no files", vendor)

			continue
		}
		if got, want := parallel[vendor].render(), serial[vendor].render(); got != want {
			t.Errorf("%s: the pool size reached the snapshot\n one worker:\n%s\n%d workers:\n%s",
				vendor, want, runtime.NumCPU(), got)
		}
	}
}

// TestCorpusSnapshotNamesTheVendorAndSeverity injects a diagnostic the
// committed snapshot does not carry and requires the failure to say
// which vendor grew it and how badly it is graded.
//
// A snapshot mismatch that only says "files differ" costs the reviewer
// the whole file to find the one line that moved, so the message is
// itself part of what this harness promises.
func TestCorpusSnapshotNamesTheVendorAndSeverity(t *testing.T) {
	dir := t.TempDir()

	base := newCorpusStats()
	base.files = 2
	base.modules = 2
	base.byCode[corpusCodeKey{code: smi.ErrCodeMissingClause, severity: smi.SeverityError}] = 1
	base.bySeverity[smi.SeverityError] = 1

	if _, err := compareOrUpdate(dir, "acme", base.render(), true); err != nil {
		t.Fatalf("writing the baseline: %v", err)
	}

	regressed := newCorpusStats()
	regressed.add(base)
	regressed.byCode[corpusCodeKey{code: smi.ErrCodeUnexpectedToken, severity: smi.SeverityError}] = 3
	regressed.bySeverity[smi.SeverityError] += 3

	msg, err := compareOrUpdate(dir, "acme", regressed.render(), false)
	if err != nil {
		t.Fatalf("comparing: %v", err)
	}
	if msg == "" {
		t.Fatal("an injected diagnostic left the snapshot comparison green")
	}

	for _, want := range []string{"acme", string(smi.ErrCodeUnexpectedToken), smi.SeverityError.String()} {
		if !strings.Contains(msg, want) {
			t.Errorf("the mismatch does not name %q:\n%s", want, msg)
		}
	}
}

// TestCorpusSnapshotWritesTheGotFile requires a mismatch to leave the
// current output beside the fixture. A reviewer diffs two files rather
// than reading a wall of test output, and can promote the new one once
// the change is understood.
func TestCorpusSnapshotWritesTheGotFile(t *testing.T) {
	dir := t.TempDir()

	stale := "files 1\n"
	if err := os.WriteFile(filepath.Join(dir, "acme.snapshot"), []byte(stale), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	current := "files 2\n"
	msg, err := compareOrUpdate(dir, "acme", current, false)
	if err != nil {
		t.Fatalf("comparing: %v", err)
	}
	if msg == "" {
		t.Fatal("a stale fixture compared equal")
	}

	got, err := os.ReadFile(filepath.Join(dir, "acme.snapshot.got"))
	if err != nil {
		t.Fatalf("reading the .got file: %v", err)
	}
	if string(got) != current {
		t.Errorf("the .got file holds %q, want %q", got, current)
	}
	if !strings.Contains(msg, "acme.snapshot.got") {
		t.Errorf("the mismatch does not point at the .got file:\n%s", msg)
	}
}

// TestCorpusSnapshotRefreshRewritesTheFixture pins the refresh flag's
// half of the contract: with it set, the fixture becomes the current
// output and the comparison that follows is clean, so the reviewable
// diff is the one git shows.
func TestCorpusSnapshotRefreshRewritesTheFixture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acme.snapshot")

	if err := os.WriteFile(path, []byte("files 1\n"), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	current := "files 2\ndiagnostic smi.unexpected-token error 3\n"
	if msg, err := compareOrUpdate(dir, "acme", current, true); err != nil || msg != "" {
		t.Fatalf("refresh returned (%q, %v)", msg, err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the refreshed fixture: %v", err)
	}
	if string(written) != current {
		t.Errorf("the refreshed fixture holds %q, want %q", written, current)
	}

	if msg, err := compareOrUpdate(dir, "acme", current, false); err != nil || msg != "" {
		t.Errorf("the refreshed fixture still mismatches: (%q, %v)", msg, err)
	}
}

// TestCorpusDeduplicationHoldsAcrossTheLCOSFamily checks the assumption
// the default run rests on: that the LCOS releases the default run drops
// are the same MIB at different sizes, not different MIBs.
//
// It compares diagnostic code sets rather than counts, because two
// releases legitimately declare different numbers of objects and only a
// new kind of fault means the dropped file was carrying something the
// retained one is not. A version that diverges belongs back in the
// default run, so this fails and names it.
func TestCorpusDeduplicationHoldsAcrossTheLCOSFamily(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the whole LCOS family")
	}

	family := familyFiles(corpusFiles(t))
	if len(family) < 2 {
		t.Skipf("no LCOS family under %s/%s", corpusRoot, lcosFamilyDir)
	}

	byFile := map[string][]string{}
	for _, cf := range family {
		stats, _ := scanCorpus(t, []corpusFile{cf}, 1)
		byFile[filepath.Base(cf.path)] = stats[cf.vendor].codes()
	}

	retained, ok := byFile[lcosRetained]
	if !ok {
		t.Fatalf("the retained member %s is not in the family", lcosRetained)
	}

	for _, name := range slices.Sorted(maps.Keys(byFile)) {
		if name == lcosRetained {
			continue
		}
		if !slices.Equal(byFile[name], retained) {
			t.Errorf("%s raises a different set of diagnostic codes than the retained %s, "+
				"so dropping it from the default run loses coverage — keep it\n dropped: %v\nretained: %v",
				name, lcosRetained, byFile[name], retained)
		}
	}
}

// TestCorpusShortModeReadsOneFilePerVendor pins the cheapest tier's
// contract. Short mode exists so that `go test -short ./...` still
// touches every vendor authority's spelling habits without reading a
// hundred and seventy megabytes.
func TestCorpusShortModeReadsOneFilePerVendor(t *testing.T) {
	files := deduplicated(corpusFiles(t))
	picked := oneFilePerVendor(files)

	vendors := map[string]int{}
	for _, cf := range files {
		vendors[cf.vendor]++
	}

	if len(picked) != len(vendors) {
		t.Errorf("short mode picked %d files for %d vendors", len(picked), len(vendors))
	}
	if len(picked) >= len(files) {
		t.Errorf("short mode picked %d of %d files, which skips nothing", len(picked), len(files))
	}

	seen := map[string]bool{}
	for _, cf := range picked {
		if seen[cf.vendor] {
			t.Errorf("%s was picked twice", cf.vendor)
		}
		seen[cf.vendor] = true
	}
	for vendor := range vendors {
		if !seen[vendor] {
			t.Errorf("short mode skips %s entirely", vendor)
		}
	}
}

// compareOrUpdate writes the vendor's snapshot when update is set, and
// otherwise compares it against the committed one. It returns the
// failure message, empty when they agree, rather than failing itself, so
// that the tests covering the mismatch path can assert on what a
// reviewer is told.
func compareOrUpdate(dir, vendor, got string, update bool) (string, error) {
	path := filepath.Join(dir, vendor+".snapshot")

	if update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			return "", fmt.Errorf("writing %s: %w", path, err)
		}
		_ = os.Remove(path + ".got")

		return "", nil
	}

	want, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w (rerun with -update-corpus and review the diff)", path, err)
	}
	if got == string(want) {
		return "", nil
	}

	gotPath := path + ".got"
	if err := os.WriteFile(gotPath, []byte(got), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", gotPath, err)
	}

	return fmt.Sprintf("%s: the corpus snapshot moved. Diff %s against %s, and rerun with "+
		"-update-corpus once the change is understood.\n%s",
		vendor, gotPath, path, snapshotDiff(string(want), got)), nil
}

// snapshotDiff renders the lines that differ, prefixed the way a patch
// prefixes them. Every line carries its code and its severity, so the
// diff alone says what changed and how badly it is graded.
func snapshotDiff(want, got string) string {
	inWant := map[string]bool{}
	for _, line := range strings.Split(want, "\n") {
		inWant[line] = true
	}
	inGot := map[string]bool{}
	for _, line := range strings.Split(got, "\n") {
		inGot[line] = true
	}

	var b strings.Builder
	for _, line := range strings.Split(want, "\n") {
		if line != "" && !inGot[line] {
			fmt.Fprintf(&b, "  - %s\n", line)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		if line != "" && !inWant[line] {
			fmt.Fprintf(&b, "  + %s\n", line)
		}
	}

	return b.String()
}

// scanCorpus loads every file through a bounded pool and folds the
// result into per-vendor counters, returning them and their sum.
//
// Each worker reads, parses and discards one file before taking the
// next, and keeps only counters between files. Nothing here holds a
// module set, a source buffer or a per-file record: peak memory is
// bounded by the largest file times the pool size rather than by how
// many files the corpus holds, which is the only way a hundred and
// seventy megabytes of MIB fits in a test.
//
// Files are dispatched largest first, because the corpus's size
// distribution is long-tailed and a six-megabyte LANCOM dump started
// last would hold the whole run open behind it.
func scanCorpus(t *testing.T, files []corpusFile, workers int) (map[string]*corpusStats, *corpusStats) {
	t.Helper()

	searchPaths := corpusSearchPaths(t)

	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}

	ordered := slices.Clone(files)
	slices.SortFunc(ordered, func(a, b corpusFile) int {
		if a.size != b.size {
			return cmp.Compare(b.size, a.size)
		}

		return cmp.Compare(a.path, b.path)
	})

	work := make(chan corpusFile)
	perWorker := make([][]*vendorStats, workers)
	failures := make([][]string, workers)

	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			byVendor := map[string]*corpusStats{}
			for cf := range work {
				start := time.Now()
				stats, problem := scanFile(cf, searchPaths)
				elapsed := time.Since(start)

				if problem != "" {
					failures[w] = append(failures[w], fmt.Sprintf("%s: %s", cf.path, problem))
				}
				if elapsed > corpusFileBudget {
					failures[w] = append(failures[w],
						fmt.Sprintf("%s: took %v, which is a hang rather than a slow file", cf.path, elapsed))
				}
				if byVendor[cf.vendor] == nil {
					byVendor[cf.vendor] = newCorpusStats()
				}
				byVendor[cf.vendor].add(stats)
			}

			for vendor, stats := range byVendor {
				perWorker[w] = append(perWorker[w], &vendorStats{vendor: vendor, stats: stats})
			}
		}()
	}

	for _, cf := range ordered {
		work <- cf
	}
	close(work)
	wg.Wait()

	byVendor := map[string]*corpusStats{}
	overall := newCorpusStats()
	for _, part := range perWorker {
		for _, vs := range part {
			if byVendor[vs.vendor] == nil {
				byVendor[vs.vendor] = newCorpusStats()
			}
			byVendor[vs.vendor].add(vs.stats)
			overall.add(vs.stats)
		}
	}

	for _, part := range failures {
		for _, msg := range part {
			t.Error(msg)
		}
	}

	return byVendor, overall
}

// vendorStats is one worker's counters for one vendor, on its way to the
// merge.
type vendorStats struct {
	vendor string
	stats  *corpusStats
}

// scanFile loads one file and counts what came back. The returned string
// is empty unless the file did something no MIB is allowed to do to this
// parser: panic, fail to read, or come back with neither a module nor a
// diagnostic explaining why.
//
// The panic is recovered rather than left to crash the run because a
// panic in a worker goroutine takes the process down without saying
// which file caused it, and which file caused it is the whole finding.
func scanFile(cf corpusFile, searchPaths []string) (stats *corpusStats, problem string) {
	stats = newCorpusStats()
	stats.files = 1
	stats.bytes = cf.size

	defer func() {
		if r := recover(); r != nil {
			problem = fmt.Sprintf("panic: %v\n%s", r, debug.Stack())
		}
	}()

	set, err := smi.LoadFiles([]string{cf.path}, smi.Options{Workers: 1, SearchPaths: searchPaths})
	if err != nil {
		return stats, err.Error()
	}

	for _, m := range set.Modules() {
		if m.File != cf.path {
			continue
		}
		stats.modules++
		stats.nodes += len(m.Nodes)
		for _, n := range m.Nodes {
			if n.Unresolved {
				stats.unresolved++
			}
		}
	}

	for _, d := range set.Diagnostics() {
		if d.Position().File != cf.path {
			continue
		}
		stats.diagnostics++
		stats.bySeverity[d.Severity()]++
		stats.byCode[corpusCodeKey{code: d.Code(), severity: d.Severity()}]++
		if d.Severity() == smi.SeverityFatal {
			stats.fatalFiles = 1
		}
	}

	if stats.modules == 0 && stats.diagnostics == 0 {
		return stats, "yielded neither a module nor a diagnostic saying why"
	}

	return stats, ""
}

// corpusFiles lists every MIB source under spec/mib. Only the corpus's
// own README is left out; the files that turn out not to be MIBs at all
// stay in, since refusing those cleanly is part of what is being
// measured.
func corpusFiles(t *testing.T) []corpusFile {
	t.Helper()

	if _, err := os.Stat(corpusRoot); errors.Is(err, fs.ErrNotExist) {
		t.Skipf("no MIB corpus at %s", corpusRoot)
	}

	var files []corpusFile
	for _, rel := range trackedCorpusPaths(t) {
		path := filepath.Join(corpusRoot, rel)

		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}

		files = append(files, corpusFile{
			path:   path,
			vendor: strings.Split(filepath.ToSlash(rel), "/")[0],
			size:   info.Size(),
		})
	}
	if len(files) == 0 {
		t.Skipf("no MIB sources under %s", corpusRoot)
	}

	slices.SortFunc(files, func(a, b corpusFile) int { return cmp.Compare(a.path, b.path) })

	return files
}

// trackedCorpusPaths returns the corpus files git tracks, relative to
// corpusRoot and without the corpus README.
//
// The corpus is the vendored tree, and vendored means committed. Reading
// the directory instead would fold in whatever a developer happens to
// have left under spec/mib -- a scratch copy of one vendor's whole MIB
// archive is the case that prompted this -- and the snapshots committed
// beside this test would then describe that developer's disk. A golden
// that moves with untracked files is not a golden.
func trackedCorpusPaths(t *testing.T) []string {
	t.Helper()

	out, err := exec.Command("git", "-C", corpusRoot, "ls-files", "-z", ".").Output()
	if err != nil {
		t.Skipf("listing tracked files under %s: %v", corpusRoot, err)
	}

	var paths []string
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" || strings.HasSuffix(rel, ".md") {
			continue
		}

		paths = append(paths, rel)
	}

	return paths
}

// corpusSearchPaths is every directory under spec/mib, which is what a
// file's IMPORTS are followed through. The whole corpus is on the path
// rather than the file's own vendor directory, because vendors import
// each other's modules and the IETF's constantly, and a module found is
// worth more to the measurement than a tidy boundary.
func corpusSearchPaths(t *testing.T) []string {
	t.Helper()

	// Derived from the tracked files rather than read off the directory,
	// for the reason trackedCorpusPaths gives: a scratch tree under
	// spec/mib would otherwise join the search path, and an import that
	// resolves against an untracked copy resolves differently for every
	// checkout. That moves the snapshot without any code changing.
	seen := map[string]bool{corpusRoot: true}
	dirs := []string{corpusRoot}
	for _, rel := range trackedCorpusPaths(t) {
		for dir := filepath.Dir(filepath.Join(corpusRoot, rel)); !seen[dir]; dir = filepath.Dir(dir) {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	slices.Sort(dirs)

	return dirs
}

// deduplicated drops the LCOS releases the default run does not read.
func deduplicated(files []corpusFile) []corpusFile {
	out := make([]corpusFile, 0, len(files))
	for _, cf := range files {
		if inLCOSFamily(cf) && filepath.Base(cf.path) != lcosRetained {
			continue
		}
		out = append(out, cf)
	}

	return out
}

// familyFiles returns the LCOS releases, retained and dropped alike.
func familyFiles(files []corpusFile) []corpusFile {
	var out []corpusFile
	for _, cf := range files {
		if inLCOSFamily(cf) {
			out = append(out, cf)
		}
	}

	return out
}

func inLCOSFamily(cf corpusFile) bool {
	return strings.Contains(filepath.ToSlash(cf.path), "/"+lcosFamilyDir+"/")
}

// vendorsOf returns the vendor authorities the files belong to.
func vendorsOf(files []corpusFile) map[string]bool {
	out := map[string]bool{}
	for _, cf := range files {
		out[cf.vendor] = true
	}

	return out
}

// oneFilePerVendor picks the largest file each vendor ships, which is
// the one most likely to carry a spelling the parser has not seen.
func oneFilePerVendor(files []corpusFile) []corpusFile {
	best := map[string]corpusFile{}
	for _, cf := range files {
		cur, ok := best[cf.vendor]
		if !ok || cf.size > cur.size || (cf.size == cur.size && cf.path < cur.path) {
			best[cf.vendor] = cf
		}
	}

	out := make([]corpusFile, 0, len(best))
	for _, vendor := range slices.Sorted(maps.Keys(best)) {
		out = append(out, best[vendor])
	}

	return out
}
