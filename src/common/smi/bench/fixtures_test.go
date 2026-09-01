package bench

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

// corpusFixture is one real MIB the suite measures against, pinned by
// path and content.
//
// The hash is the point of the type. Everything else here is a
// convenience; the hash is what stops a MIB re-sync from moving the
// baseline underneath a gate that then keeps passing and means nothing.
type corpusFixture struct {
	// name is what the benchmark sub-name reports, and so what a
	// regression is attributed to.
	name string

	// path is repository-relative.
	path string

	// sha256 is the hex digest of the file the committed baseline was
	// measured on.
	sha256 string

	// shape says which end of the size distribution this file covers,
	// so a later reader knows what would be lost by dropping it.
	shape string
}

// corpusFixtures are the two extremes of the repository's MIB tree.
//
// Parsers do not scale evenly along one axis. Per-declaration overhead
// dominates a file of many tiny declarations, and per-declaration
// buffers and copies dominate a file holding one enormous one, and an
// optimization for either can be a regression for the other. A single
// median-sized MIB reports neither, so both ends are pinned and a
// benchmark that takes a fixture runs against both.
var corpusFixtures = []corpusFixture{
	{
		name:   "lancom-oids",
		path:   "spec/mib/lancom/lcos/LC-UNIFIED-LCOS-10-94-REL-OIDS.mib",
		sha256: "f7e5f14c5483b90cc36c0427ddeb22ba43e8794844019bf16c4e8b2c54f6476a",
		shape:  "many small declarations",
	},
	{
		name:   "huawei-tc",
		path:   "spec/mib/huawei/HUAWEI-TC-MIB",
		sha256: "57f2e1750c35fa48cd96138517adabfc5f4b7f4c66859428af4d799ac9c96f95",
		shape:  "few enormous declarations",
	},
}

// repoRoot returns the repository root. The bench module sits four
// levels down and is always run from its own directory, so the root is
// a fixed relative walk rather than a search.
func repoRoot(tb testing.TB) string {
	tb.Helper()

	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		tb.Fatalf("resolving repository root: %v", err)
	}

	return root
}

// fixtureCache holds each fixture's bytes for the process's lifetime.
// A benchmark run touches the same few megabytes dozens of times and
// re-reading them would put the page cache in the measurement.
var (
	fixtureMu    sync.Mutex
	fixtureCache = map[string][]byte{}
)

// readFixture returns the pinned fixture's bytes, failing the caller if
// the file on disk is not the one the baseline was measured on.
func readFixture(tb testing.TB, f corpusFixture) []byte {
	tb.Helper()

	fixtureMu.Lock()
	defer fixtureMu.Unlock()

	if src, ok := fixtureCache[f.path]; ok {
		return src
	}

	path := filepath.Join(repoRoot(tb), filepath.FromSlash(f.path))
	src, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("pinned fixture %s is missing: %v\n"+
			"A MIB re-sync moved or removed it. Re-pin corpusFixtures and re-capture "+
			"testdata/baseline-micro.txt in the same commit.", f.path, err)
	}

	sum := sha256.Sum256(src)
	if got := hex.EncodeToString(sum[:]); got != f.sha256 {
		tb.Fatalf("pinned fixture %s changed content:\n  have %s\n  want %s\n"+
			"A MIB re-sync rewrote it, so the committed baseline no longer describes this "+
			"file. Update the sha256 in corpusFixtures and re-capture "+
			"testdata/baseline-micro.txt in the same commit.", f.path, got, f.sha256)
	}

	fixtureCache[f.path] = src

	return src
}

// fixturePath returns a pinned fixture's absolute path, after checking
// its content, for the benchmarks that go through the file-taking entry
// points rather than the byte-taking ones.
func fixturePath(tb testing.TB, f corpusFixture) string {
	tb.Helper()
	readFixture(tb, f)

	return filepath.Join(repoRoot(tb), filepath.FromSlash(f.path))
}

// mibgenConfig is the slice of mibgen.yaml the end-to-end benchmark
// needs: which modules go generate loads, and where they are found.
//
// It is read rather than copied because the number of configured
// modules is the thing being measured. A hardcoded list would keep
// reporting sixteen modules' cost after somebody added a seventeenth.
type mibgenConfig struct {
	SearchPaths []string `yaml:"search_paths"`
	Modules     []struct {
		Name string `yaml:"name"`
	} `yaml:"modules"`
}

// loadMibgenConfig reads the repository's mibgen.yaml and returns the
// module names and absolute search paths.
func loadMibgenConfig(tb testing.TB) ([]string, []string) {
	tb.Helper()

	root := repoRoot(tb)
	raw, err := os.ReadFile(filepath.Join(root, "mibgen.yaml"))
	if err != nil {
		tb.Fatalf("reading mibgen.yaml: %v", err)
	}

	var cfg mibgenConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		tb.Fatalf("parsing mibgen.yaml: %v", err)
	}

	names := make([]string, 0, len(cfg.Modules))
	for _, m := range cfg.Modules {
		names = append(names, m.Name)
	}

	paths := make([]string, 0, len(cfg.SearchPaths))
	for _, p := range cfg.SearchPaths {
		paths = append(paths, filepath.Join(root, filepath.FromSlash(p)))
	}

	if len(names) == 0 || len(paths) == 0 {
		tb.Fatalf("mibgen.yaml has %d modules and %d search paths; both must be non-empty",
			len(names), len(paths))
	}

	return names, paths
}

// TestCorpusFixturePins checks that both pinned fixtures are present and
// unchanged, and that they still sit at opposite ends of the size
// distribution.
//
// The size assertion is deliberately loose. Its job is not to pin a
// number the SHA already pins, but to catch a future re-pin that swaps
// one of these for a file of the same middling shape as the other, which
// would leave the suite passing while measuring one axis twice.
func TestCorpusFixturePins(t *testing.T) {
	sizes := map[string]int{}
	for _, f := range corpusFixtures {
		src := readFixture(t, f)
		sizes[f.name] = len(src)
		t.Logf("%s (%s): %d bytes", f.name, f.shape, len(src))
	}

	if len(corpusFixtures) != 2 {
		t.Fatalf("expected the two extremes, got %d fixtures", len(corpusFixtures))
	}

	small, large := corpusFixtures[0], corpusFixtures[1]
	perDecl := func(f corpusFixture) float64 {
		frames, largest := 0, 0
		for _, m := range cutFixture(t, f).Modules {
			frames += len(m.Frames)
			for _, fr := range m.Frames {
				largest = max(largest, fr.Span.Len())
			}
		}
		if frames == 0 {
			t.Fatalf("%s framed into no declarations", f.name)
		}
		t.Logf("%s: %d declarations, %.0f B mean, largest %d B",
			f.name, frames, float64(sizes[f.name])/float64(frames), largest)

		return float64(sizes[f.name]) / float64(frames)
	}

	if a, b := perDecl(small), perDecl(large); a*10 > b {
		t.Errorf("%s averages %.0f B per declaration and %s averages %.0f B; "+
			"the two fixtures must stay an order of magnitude apart or the suite "+
			"measures one shape twice", small.name, a, large.name, b)
	}
}

// TestMibgenConfigReadable checks the end-to-end benchmark's input is
// there, since a benchmark that skips is a benchmark that silently stops
// gating.
func TestMibgenConfigReadable(t *testing.T) {
	names, paths := loadMibgenConfig(t)
	t.Logf("mibgen.yaml configures %d modules over %d search paths", len(names), len(paths))

	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("search path %s: %v", p, err)
		}
	}
	for _, n := range names {
		if strings.TrimSpace(n) == "" {
			t.Error("mibgen.yaml lists a module with no name")
		}
	}
}
