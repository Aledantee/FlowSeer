package proto_test

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
)

// layers is the single declaration of FlowSeer's proto import layering. An
// import may point at an equal or lower index, never a higher one; the
// architecture record cites this table rather than restating it.
//
// Prefixes are package paths relative to flowseer/, matched longest-first, so
// net/protocol covers every protocol package under it.
var layers = []struct {
	index    int
	prefixes []string
}{
	{0, []string{"net/addr"}},
	{1, []string{"net/phy", "net/l2", "net/l3"}},
	{2, []string{"net/interface"}},
	{3, []string{"net/protocol"}},
	{4, []string{"net/wlan"}},
	{5, []string{"device"}},
	{6, []string{"inventory"}},
	{7, []string{"integration"}},
	{8, []string{"service"}},
	{9, []string{"event"}},
}

// The two boundaries below are read out of the table rather than written a
// second time. Inserting a package renumbers the table, and a duplicated
// index would go on enforcing the old boundary without failing.
var (
	// deviceLayer is the first index at which a package carries identity —
	// refs, tenancy, lifecycle. Nothing under net/ may reach it.
	deviceLayer = layerIndexOf("device")

	// protocolLayer holds the per-protocol packages. A layer package models a
	// function; a protocol package models one wire protocol's view of it. The
	// dependency runs protocol → layer, never back.
	protocolLayer = layerIndexOf("net/protocol")
)

// The reason an import was rejected. Fixtures assert on these, so a fixture
// caught by the wrong rule fails instead of counting as proof.
const (
	reasonEntity   = "a net/ package may not import an entity package"
	reasonProtocol = "a layer package may not import a protocol package"
	reasonUpward   = "imports flow upward only"
	reasonUnplaced = "package is not in the layering table; place it there first"
)

// wantFixtureReason maps each negative fixture to the rule it exists to prove.
// Every rule in violations below needs an entry here, or that rule ships
// unexercised and could be inverted without failing anything.
var wantFixtureReason = map[string]string{
	"imports_device.proto":   reasonEntity,
	"imports_protocol.proto": reasonProtocol,
	"imports_upward.proto":   reasonUpward,
}

// violation is one import that breaks the layering, named by the rule it
// breaks so the failure says which of the rules it is.
type violation struct {
	file   string
	imp    string
	reason string
}

func TestProtoImportLayering(t *testing.T) {
	production, fixtures, fixtureDirs := scanOwnedProtos(t)

	t.Run("both layer boundaries resolve", func(t *testing.T) {
		if deviceLayer < 0 {
			t.Errorf("got %d for the device layer, want it declared in the layers table", deviceLayer)
		}
		if protocolLayer < 0 {
			t.Errorf("got %d for the protocol layer, want it declared in the layers table", protocolLayer)
		}
	})

	t.Run("production files respect the layering", func(t *testing.T) {
		for _, f := range production {
			for _, v := range f.violations() {
				t.Errorf("%s imports %s: %s", v.file, v.imp, v.reason)
			}
		}
	})

	// Without this pass the production pass could be green because the rules
	// never fire at all. Each fixture is a worked violation of one named rule,
	// and an empty fixture set means the proof went missing.
	t.Run("every fixture is caught by the rule it proves", func(t *testing.T) {
		if len(fixtures) == 0 {
			t.Fatalf("got no fixtures under %s, want one per layering rule", ownedRoot(t))
		}
		seen := map[string]bool{}
		for _, f := range fixtures {
			base := path.Base(f.rel)
			want, known := wantFixtureReason[base]
			if !known {
				t.Errorf("got fixture %s with no expected rule, want an entry in wantFixtureReason", f.rel)
				continue
			}
			seen[base] = true
			got := f.violations()
			if len(got) == 0 {
				t.Errorf("got no violation for fixture %s, want %q", f.rel, want)
				continue
			}
			if got[0].reason != want {
				t.Errorf("got %q for fixture %s, want %q", got[0].reason, f.rel, want)
			}
		}
		for base := range wantFixtureReason {
			if !seen[base] {
				t.Errorf("got no fixture named %s, want one proving %q", base, wantFixtureReason[base])
			}
		}
	})

	// The hook skips buf lint under _test_fixtures, so an unexcluded fixture
	// directory no longer announces itself on save. It has to fail here
	// instead, before it breaks the whole module at generate time.
	t.Run("every fixture directory is excluded in buf.yaml", func(t *testing.T) {
		cfg, err := os.ReadFile(filepath.Join(repoRoot(t), "buf.yaml"))
		if err != nil {
			t.Fatalf("reading buf.yaml: %v", err)
		}
		for _, dir := range fixtureDirs {
			if !strings.Contains(string(cfg), dir) {
				t.Errorf("got no buf.yaml exclude for %s, want one; buf lint and buf generate would otherwise pick the fixtures up", dir)
			}
		}
	})
}

// protoFile is one .proto source with its package path (the directory
// relative to flowseer/) and the flowseer imports it declares.
type protoFile struct {
	rel     string // path relative to spec/proto, for failure messages
	pkgPath string
	imports []string
}

func (f protoFile) violations() []violation {
	from, ok := layerOf(f.pkgPath)
	if !ok {
		return []violation{{f.rel, "", reasonUnplaced}}
	}
	var out []violation
	for _, imp := range f.imports {
		to, ok := layerOf(strings.TrimPrefix(path.Dir(imp), "flowseer/"))
		if !ok {
			out = append(out, violation{f.rel, imp, reasonUnplaced})
			continue
		}
		// The ascending rule at the bottom already catches both cases above
		// it; they run first so the message names the specific boundary that
		// was crossed rather than the general one. Reordering the table would
		// change which message an author sees, never whether the import is
		// caught.
		switch {
		case strings.HasPrefix(f.pkgPath, "net/") && to >= deviceLayer:
			out = append(out, violation{f.rel, imp, reasonEntity})
		case from < protocolLayer && to == protocolLayer:
			out = append(out, violation{f.rel, imp, reasonProtocol})
		case to > from:
			out = append(out, violation{f.rel, imp, reasonUpward})
		}
	}
	return out
}

// layerOf resolves a package path relative to flowseer/ to its layer index.
// An unplaced package is a violation rather than a skip, so a new package has
// to be positioned in the table before anything can import it — and reporting
// it as a violation rather than aborting keeps the remaining files checked.
func layerOf(pkgPath string) (int, bool) {
	best, bestLen := 0, -1
	for _, l := range layers {
		for _, p := range l.prefixes {
			if (pkgPath == p || strings.HasPrefix(pkgPath, p+"/")) && len(p) > bestLen {
				best, bestLen = l.index, len(p)
			}
		}
	}
	return best, bestLen >= 0
}

// layerIndexOf returns the index of the layer that declares prefix, or -1.
func layerIndexOf(prefix string) int {
	for _, l := range layers {
		if slices.Contains(l.prefixes, prefix) {
			return l.index
		}
	}
	return -1
}

// The modifier group covers `public` and `weak` as well as `option`: both are
// forbidden by the style guide (and `weak` is not even edition-2024 grammar),
// but a guard that stops seeing an import the moment someone writes a banned
// keyword fails open, which is the one thing it must not do.
var importRe = regexp.MustCompile(`(?m)^\s*import\s+(?:(?:option|public|weak)\s+)?"([^"]+)"\s*;`)

// scanOwnedProtos reads every .proto under spec/proto/flowseer/, splitting
// them into production files and negative fixtures, and reports the fixture
// directories it found.
func scanOwnedProtos(t *testing.T) (production, fixtures []protoFile, fixtureDirs []string) {
	t.Helper()
	root := ownedRoot(t)
	dirs := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".proto") {
			return nil
		}
		src, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(repoRoot(t), p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)

		f := protoFile{
			rel:     rel,
			pkgPath: strings.TrimPrefix(path.Dir(rel), "spec/proto/flowseer/"),
		}
		for _, m := range importRe.FindAllStringSubmatch(stripComments(string(src)), -1) {
			if strings.HasPrefix(m[1], "flowseer/") {
				f.imports = append(f.imports, m[1])
			}
		}
		if i := strings.Index(f.pkgPath, "_test_fixtures"); i >= 0 {
			dirs[path.Join("spec/proto/flowseer", f.pkgPath[:i]+"_test_fixtures")] = true
			// A fixture's own directory is not a package; judge it by the
			// layer its parent package sits in.
			f.pkgPath = strings.TrimSuffix(f.pkgPath[:i], "/")
			fixtures = append(fixtures, f)
			return nil
		}
		production = append(production, f)
		return nil
	})
	if err != nil {
		t.Fatalf("scanning %s: %v", root, err)
	}
	for d := range dirs {
		fixtureDirs = append(fixtureDirs, d)
	}
	sort.Strings(fixtureDirs)
	return production, fixtures, fixtureDirs
}

// repoRoot locates the checkout from this file's own path, so the test does
// not depend on the working directory it is run from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(here)))
}

func ownedRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "spec", "proto", "flowseer")
}

// stripComments blanks // and /* */ comments so a commented-out or discussed
// import path is not read as a real one.
func stripComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				i = len(src)
				break
			}
			// Keep the newlines so line-anchored matching still lines up.
			b.WriteString(strings.Repeat("\n", strings.Count(src[i:i+2+end+2], "\n")))
			i += 2 + end + 2
		default:
			b.WriteByte(src[i])
			i++
		}
	}
	return b.String()
}
