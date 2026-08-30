package frame

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/smi/internal/diag"
	"go.aledante.io/FlowSeer/src/common/smi/internal/lex"
)

var updateHistograms = flag.Bool("update-heads", false,
	"rewrite the committed per-vendor head histograms from the current corpus")

// corpusRoot is the vendor MIB corpus, relative to this package.
const corpusRoot = "../../../../../spec/mib"

// depthHeadroom is how far the depth cap has to sit above what the
// corpus actually nests. A cap set near the observed maximum would fail
// the next vendor MIB with one more level of SIZE constraint, so the
// sweep insists on a factor rather than a margin.
const depthHeadroom = 8

// stats is what one vendor directory's files added up to. Everything in
// it is deterministic, because it is rendered into a committed file and
// reviewed as a diff.
type stats struct {
	files        int
	modules      int
	frames       int
	maxDepth     int
	pairedFiles  int
	kinds        [len(kindNames)]int
	fatal        map[errs.Code]int
	unrecognized map[string]int
}

func newStats() *stats {
	return &stats{fatal: map[errs.Code]int{}, unrecognized: map[string]int{}}
}

func (s *stats) add(other *stats) {
	s.files += other.files
	s.modules += other.modules
	s.frames += other.frames
	s.pairedFiles += other.pairedFiles
	s.maxDepth = max(s.maxDepth, other.maxDepth)
	for i, n := range other.kinds {
		s.kinds[i] += n
	}
	for code, n := range other.fatal {
		s.fatal[code] += n
	}
	for text, n := range other.unrecognized {
		s.unrecognized[text] += n
	}
}

// render writes the histogram in the committed form: fixed lines first,
// then one line per kind in kind order including the kinds that drew
// nothing, then the fatal codes and the unrecognized head spellings
// sorted by name. Zero-count kinds stay in so a vendor's file keeps the
// same shape as its content changes.
func (s *stats) render() string {
	var b strings.Builder

	fmt.Fprintf(&b, "files %d\n", s.files)
	fmt.Fprintf(&b, "modules %d\n", s.modules)
	fmt.Fprintf(&b, "frames %d\n", s.frames)
	fmt.Fprintf(&b, "max-depth %d\n", s.maxDepth)
	fmt.Fprintf(&b, "paired-comment-files %d\n", s.pairedFiles)

	for k, name := range kindNames {
		fmt.Fprintf(&b, "kind %s %d\n", name, s.kinds[k])
	}

	for _, code := range sortedKeys(s.fatal) {
		fmt.Fprintf(&b, "fatal %s %d\n", code, s.fatal[code])
	}
	for _, text := range sortedKeys(s.unrecognized) {
		fmt.Fprintf(&b, "unrecognized %q %d\n", text, s.unrecognized[text])
	}

	return b.String()
}

func sortedKeys[K ~string, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)

	return out
}

type corpusFile struct {
	path   string
	vendor string
	size   int64
}

// TestSweepCorpus frames every MIB in spec/mib with the framer alone.
//
// The sweep is the evidence behind the head taxonomy. A head form nobody
// thought of shows up here as an unrecognized frame, in a committed
// histogram whose diff a reviewer has to look at, rather than as a
// missing declaration two passes later. It also measures what the corpus
// actually nests, which is what the depth cap is checked against.
func TestSweepCorpus(t *testing.T) {
	files := corpusFiles(t)

	start := time.Now()
	perFile := sweep(t, files)
	t.Logf("framed %d files (%.1f MB) in %v across %d workers",
		len(files), float64(totalSize(files))/(1<<20), time.Since(start), runtime.GOMAXPROCS(0))

	byVendor := map[string]*stats{}
	overall := newStats()
	for i, s := range perFile {
		vendor := files[i].vendor
		if byVendor[vendor] == nil {
			byVendor[vendor] = newStats()
		}
		byVendor[vendor].add(s)
		overall.add(s)
	}

	t.Logf("%d modules, %d frames, %d unrecognized, deepest nesting %d",
		overall.modules, overall.frames, overall.kinds[KindUnrecognized], overall.maxDepth)

	for vendor, s := range byVendor {
		checkHistogram(t, vendor, s)
	}

	if want := MaxDepth / depthHeadroom; overall.maxDepth > want {
		t.Errorf("corpus nests %d deep, which leaves the cap of %d less than %dx headroom",
			overall.maxDepth, MaxDepth, depthHeadroom)
	}
}

func checkHistogram(t *testing.T, vendor string, s *stats) {
	t.Helper()

	path := filepath.Join("testdata", "heads", vendor+".histogram")
	got := s.render()

	if *updateHistograms {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}

		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v\nRerun with -update-heads and review the diff.", path, err)
	}
	if got != string(want) {
		t.Errorf("%s is stale.\n got:\n%s\nwant:\n%s\n"+
			"Rerun with -update-heads and review the diff: a change here is a change in what the head forms cover.",
			path, got, want)
	}
}

// sweep frames every file, dispatching the largest first through a
// bounded pool. Longest-first matters because the corpus's size
// distribution is long-tailed: a single 8 MB Cisco MIB started last would
// hold the whole sweep open behind it. Parallelism stops at the file
// boundary — a MIB is small enough that splitting one across goroutines
// costs more in coordination than it returns.
func sweep(t *testing.T, files []corpusFile) []*stats {
	t.Helper()

	out := make([]*stats, len(files))
	failures := make([]string, len(files))

	work := make(chan int)
	var wg sync.WaitGroup

	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				s, err := frameOne(files[i])
				if err != nil {
					failures[i] = err.Error()
				}
				out[i] = s
			}
		}()
	}

	for i := range files {
		work <- i
	}
	close(work)
	wg.Wait()

	for i, msg := range failures {
		if msg != "" {
			t.Errorf("%s: %s", files[i].path, msg)
		}
	}

	return out
}

func frameOne(cf corpusFile) (*stats, error) {
	src, err := os.ReadFile(cf.path)
	if err != nil {
		return newStats(), err
	}

	f := Cut(src, Options{File: cf.path})
	if err := checkTiling(len(src), f); err != nil {
		return newStats(), err
	}

	s := newStats()
	s.files = 1
	s.modules = len(f.Modules)
	s.maxDepth = f.MaxObservedDepth()
	if f.Comments == lex.CommentPaired {
		s.pairedFiles = 1
	}

	for _, fr := range f.Frames() {
		s.frames++
		s.kinds[fr.Kind]++
		if fr.Kind == KindUnrecognized {
			s.unrecognized[fr.Name]++
		}
	}

	for _, d := range f.Diagnostics {
		if d.Severity() == diag.SeverityFatal {
			s.fatal[d.Code()]++
		}
	}

	return s, nil
}

// corpusFiles lists the MIB sources, largest first. Only the corpus's own
// README is left out; everything else in the tree is input, including the
// files that turn out not to be MIBs at all, since refusing those
// cleanly is part of what the sweep is checking.
func corpusFiles(t *testing.T) []corpusFile {
	t.Helper()

	if _, err := os.Stat(corpusRoot); errors.Is(err, fs.ErrNotExist) {
		t.Skipf("no MIB corpus at %s", corpusRoot)
	}

	var files []corpusFile
	err := filepath.WalkDir(corpusRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".md") {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(corpusRoot, path)
		if err != nil {
			return err
		}

		files = append(files, corpusFile{
			path:   path,
			vendor: strings.Split(filepath.ToSlash(rel), "/")[0],
			size:   info.Size(),
		})

		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", corpusRoot, err)
	}
	if len(files) == 0 {
		t.Skipf("no MIB sources under %s", corpusRoot)
	}

	slices.SortFunc(files, func(a, b corpusFile) int {
		if a.size != b.size {
			return cmp.Compare(b.size, a.size)
		}

		return cmp.Compare(a.path, b.path)
	})

	return files
}

func totalSize(files []corpusFile) int64 {
	var n int64
	for _, f := range files {
		n += f.size
	}

	return n
}
