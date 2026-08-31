// Package differential measures FlowSeer's SMI parser against gosmi over
// the corpus in spec/mib.
//
// It answers two questions the parser's own test suite cannot. The first
// is how much of the corpus the predecessor could read at all: every
// corpus file is put through gosmi and lands in one of three classes —
// loaded, failed to load, or panicked — and the counts are reported per
// vendor. Those are the before-numbers any later claim about resolution
// rate is read against. The second is whether the two implementations
// agree about the modules gosmi does load, compared over the projection
// [Fields] names and with the divergences in expected_divergences.go
// allowed for.
//
// The panic class is not a defensive flourish. gosmi v0.4.4 panics
// rather than returning an error on DEFVAL { { } }, which RFC 2578 §7.9
// spells out as the way to say no bits are set, and the corpus carries
// that construct. An unrecovered panic ends the test binary instead of
// one file, so every gosmi call in this package runs under
// [LoadWithGosmi]'s recovery.
//
// gosmi keeps its module universe in package-level state, so a load is
// not safe for concurrent use and this package runs its corpus pass on
// one goroutine. That is also why every load gets a fresh Init/Exit
// pair: a file must not see the modules the previous file pulled in, or
// two vendors that ship a module under the same name would silently
// share one definition.
package differential

import (
	"cmp"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/sleepinggenius2/gosmi"
)

// Outcome is what became of one corpus file put through gosmi.
type Outcome uint8

// The outcome classes. Panicked is kept apart from Failed rather than
// folded into it because a returned error costs one file and a panic
// costs the process that asked.
const (
	OutcomeLoaded Outcome = iota
	OutcomeFailed
	OutcomePanicked
)

var outcomeNames = [...]string{
	OutcomeLoaded:   "loaded",
	OutcomeFailed:   "failed",
	OutcomePanicked: "panicked",
}

// String returns the outcome's name as the census reports it.
func (o Outcome) String() string {
	if int(o) >= len(outcomeNames) {
		return "outcome(" + fmt.Sprint(uint8(o)) + ")"
	}

	return outcomeNames[o]
}

// CorpusFile is one MIB source and the vendor authority that ships it.
type CorpusFile struct {
	Path   string
	Vendor string
	Size   int64
}

// LCOSFamilyDir and LCOSRetained name the one directory the corpus pass
// collapses, and the member it keeps.
//
// LANCOM ships every LCOS release as a full OID dump, so the directory
// is fifteen near-copies of its newest member and roughly half the
// corpus by bytes. The parser's own corpus harness collapses it the same
// way, and the census only means anything if both sides counted the same
// files.
const (
	LCOSFamilyDir = "lancom/lcos"
	LCOSRetained  = "LC-UNIFIED-LCOS-10-94-REL-OIDS.mib"
)

// CorpusFiles lists every MIB source under root, minus the corpus's own
// README and minus the LCOS releases [LCOSRetained] stands in for. The
// result is sorted by path, so a census does not depend on directory
// order.
func CorpusFiles(root string) ([]CorpusFile, error) {
	var files []CorpusFile

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".md") {
			return err
		}

		slash := filepath.ToSlash(path)
		if strings.Contains(slash, "/"+LCOSFamilyDir+"/") && filepath.Base(path) != LCOSRetained {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		files = append(files, CorpusFile{
			Path:   path,
			Vendor: strings.Split(filepath.ToSlash(rel), "/")[0],
			Size:   info.Size(),
		})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}

	slices.SortFunc(files, func(a, b CorpusFile) int { return cmp.Compare(a.Path, b.Path) })

	return files, nil
}

// CorpusDirs lists every directory under root, which is what a file's
// IMPORTS are followed through. The whole corpus is on the search path
// rather than the file's own vendor directory because vendors import
// each other's modules and the IETF's constantly, and a census that
// counted unfound imports as gosmi's failures would be measuring the
// search path.
func CorpusDirs(root string) ([]string, error) {
	var dirs []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, path)
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}
	slices.Sort(dirs)

	return dirs, nil
}

// gosmiState serializes access to gosmi's package-level module
// universe. Nothing here is concurrent by design; the mutex is what
// makes that a guarantee rather than a convention.
var gosmiState sync.Mutex

// LoadWithGosmi puts one MIB file through gosmi and reports which of the
// three outcome classes it landed in, with the module's projection when
// it loaded and a one-line detail when it did not.
//
// The file is loaded by base name with its own directory at the head of
// the search path, because gosmi resolves a name against its path list
// and two vendors ship files under the same base name. searchPaths
// follows so IMPORTS resolve across the corpus.
//
// The projection is read here rather than handed back as a live
// SmiModule. gosmi's nodes and types are reached by walking pointers
// into the module universe the deferred Exit below tears down, and
// walking them costs another trip through the code that panics, so both
// have to happen inside the recovery and before the teardown.
//
// A panic anywhere in that is caught and returned as
// [OutcomePanicked]. The module universe is rebuilt from nothing around
// every call, so a file that panicked halfway through a build cannot
// leave its wreckage where the next file will read it.
//
// LoadWithGosmi is not safe for concurrent use: it takes a
// package-level lock for the whole call, because what it manipulates is
// gosmi's package-level state.
func LoadWithGosmi(path string, searchPaths []string) (p *Projection, outcome Outcome, detail string) {
	gosmiState.Lock()
	defer gosmiState.Unlock()

	defer func() {
		if r := recover(); r != nil {
			p, outcome, detail = nil, OutcomePanicked, fmt.Sprintf("%v", r)
		}
		gosmi.Exit()
	}()

	gosmi.Init()

	dirs := append([]string{filepath.Dir(path)}, searchPaths...)
	gosmi.SetPath(strings.Join(dirs, string(os.PathListSeparator)))

	name, err := gosmi.LoadModule(filepath.Base(path))
	if err != nil {
		return nil, OutcomeFailed, err.Error()
	}

	mod, err := gosmi.GetModule(name)
	if err != nil {
		return nil, OutcomeFailed, err.Error()
	}

	projected := ProjectGosmi(&mod)

	return &projected, OutcomeLoaded, ""
}

// PanicDetail is the recovered value of a panicking load, trimmed to the
// message gosmi carried. Callers use it to group panics by cause; the
// stack is not part of it, since the stack is the same for every file
// that trips the same construct.
func PanicDetail(detail string) string {
	if i := strings.IndexByte(detail, '\n'); i >= 0 {
		return detail[:i]
	}

	return detail
}

// VendorCensus counts one vendor's corpus files by outcome.
type VendorCensus struct {
	Loaded   int
	Failed   int
	Panicked int
}

// Files returns how many files the vendor contributed.
func (v VendorCensus) Files() int { return v.Loaded + v.Failed + v.Panicked }

// Add folds other into v.
func (v *VendorCensus) Add(other VendorCensus) {
	v.Loaded += other.Loaded
	v.Failed += other.Failed
	v.Panicked += other.Panicked
}

// Record counts one file's outcome.
func (v *VendorCensus) Record(o Outcome) {
	switch o {
	case OutcomeLoaded:
		v.Loaded++
	case OutcomeFailed:
		v.Failed++
	case OutcomePanicked:
		v.Panicked++
	}
}

// Census is the per-vendor outcome count for a whole corpus pass.
type Census map[string]*VendorCensus

// Record counts one file.
func (c Census) Record(vendor string, o Outcome) {
	if c[vendor] == nil {
		c[vendor] = &VendorCensus{}
	}
	c[vendor].Record(o)
}

// Total returns the counts across every vendor.
func (c Census) Total() VendorCensus {
	var total VendorCensus
	for _, v := range c {
		total.Add(*v)
	}

	return total
}

// Render writes the census in its committed form: one line per vendor in
// name order, then a total line. The counts are what a later resolution
// rate is read against, so they are a fixture rather than a log line.
func (c Census) Render() string {
	var b strings.Builder

	for _, vendor := range slices.Sorted(maps.Keys(c)) {
		v := c[vendor]
		fmt.Fprintf(&b, "vendor %s loaded %d failed %d panicked %d\n", vendor, v.Loaded, v.Failed, v.Panicked)
	}

	total := c.Total()
	fmt.Fprintf(&b, "total loaded %d failed %d panicked %d\n", total.Loaded, total.Failed, total.Panicked)

	return b.String()
}

// CompareOrUpdate diffs got against the fixture at path, or rewrites the
// fixture when update is set. It returns an empty string when they
// match, and otherwise a message naming the first line that moved.
//
// A mismatch also leaves got beside the fixture as "<path>.got", so a
// reviewer diffs two files rather than reading a wall of test output and
// can promote the new one once the change is understood.
func CompareOrUpdate(path, got string, update bool) (string, error) {
	if update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			return "", fmt.Errorf("writing %s: %w", path, err)
		}

		return "", nil
	}

	want, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s (run with -update-census to create it): %w", path, err)
	}
	if string(want) == got {
		return "", nil
	}

	gotPath := path + ".got"
	if err := os.WriteFile(gotPath, []byte(got), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", gotPath, err)
	}

	return fmt.Sprintf("%s no longer matches; current output written to %s\n%s",
		path, gotPath, firstDifference(string(want), got)), nil
}

// firstDifference returns the first line the two texts disagree on, in
// got/want order, so a mismatch says which number moved.
func firstDifference(want, got string) string {
	w := strings.Split(want, "\n")
	g := strings.Split(got, "\n")

	for i := range max(len(w), len(g)) {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return fmt.Sprintf("line %d: got %q, want %q", i+1, gl, wl)
		}
	}

	return ""
}
