package proto_test

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
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

// deviceLayer is the first index at which a package carries identity — refs,
// tenancy, lifecycle. Nothing under net/ may reach it.
const deviceLayer = 5

// protocolLayer holds the per-protocol packages. A layer package models a
// function; a protocol package models one wire protocol's view of it. The
// dependency runs protocol → layer, never back.
const protocolLayer = 3

// violation is one import that breaks the layering, named by the rule it
// breaks so the failure says which of the three it is.
type violation struct {
	file   string
	imp    string
	reason string
}

func TestProtoImportLayering(t *testing.T) {
	production, fixtures := scanOwnedProtos(t)

	t.Run("production files respect the layering", func(t *testing.T) {
		for _, f := range production {
			for _, v := range f.violations(t) {
				t.Errorf("%s imports %s: %s", v.file, v.imp, v.reason)
			}
		}
	})

	// Without this pass the production pass could be green because the rules
	// never fire at all. Each fixture is a worked violation, and an empty
	// fixture set means the proof went missing.
	t.Run("every fixture is caught", func(t *testing.T) {
		if len(fixtures) == 0 {
			t.Fatalf("got no fixtures under %s, want at least one negative fixture", ownedRoot(t))
		}
		for _, f := range fixtures {
			if got := f.violations(t); len(got) == 0 {
				t.Errorf("got no violation for fixture %s, want at least one", f.rel)
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

func (f protoFile) violations(t *testing.T) []violation {
	t.Helper()
	from := layerOf(t, f.rel, f.pkgPath)
	var out []violation
	for _, imp := range f.imports {
		to := layerOf(t, f.rel, strings.TrimPrefix(path.Dir(imp), "flowseer/"))
		switch {
		case strings.HasPrefix(f.pkgPath, "net/") && to >= deviceLayer:
			out = append(out, violation{f.rel, imp, "a net/ package may not import an entity package"})
		case from < protocolLayer && to == protocolLayer:
			out = append(out, violation{f.rel, imp, "a layer package may not import a protocol package"})
		case to > from:
			out = append(out, violation{f.rel, imp, "imports flow upward only"})
		}
	}
	return out
}

// layerOf resolves a package path relative to flowseer/ to its layer index.
// An unplaced package fails the test rather than being skipped: a new package
// has to be positioned in the table before anything can import it.
func layerOf(t *testing.T, file, pkgPath string) int {
	t.Helper()
	best, bestLen := -1, -1
	for _, l := range layers {
		for _, p := range l.prefixes {
			if (pkgPath == p || strings.HasPrefix(pkgPath, p+"/")) && len(p) > bestLen {
				best, bestLen = l.index, len(p)
			}
		}
	}
	if best < 0 {
		t.Fatalf("%s: package path %q is not in the layering table; place it there first", file, pkgPath)
	}
	return best
}

var importRe = regexp.MustCompile(`(?m)^\s*import\s+(?:option\s+)?"([^"]+)"\s*;`)

// scanOwnedProtos reads every .proto under spec/proto/flowseer/, splitting
// them into production files and negative fixtures.
func scanOwnedProtos(t *testing.T) (production, fixtures []protoFile) {
	t.Helper()
	root := ownedRoot(t)
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
		rel, rerr := filepath.Rel(filepath.Dir(root), p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)

		f := protoFile{
			rel:     rel,
			pkgPath: strings.TrimPrefix(path.Dir(rel), "flowseer/"),
		}
		for _, m := range importRe.FindAllStringSubmatch(stripComments(string(src)), -1) {
			if strings.HasPrefix(m[1], "flowseer/") {
				f.imports = append(f.imports, m[1])
			}
		}
		if strings.Contains(rel, "/_test_fixtures/") {
			// A fixture's own directory is not a package; judge it by the
			// layer its parent package sits in.
			f.pkgPath = strings.TrimSuffix(f.pkgPath, "/_test_fixtures")
			fixtures = append(fixtures, f)
			return nil
		}
		production = append(production, f)
		return nil
	})
	if err != nil {
		t.Fatalf("scanning %s: %v", root, err)
	}
	return production, fixtures
}

// ownedRoot locates spec/proto/flowseer/ from this file's own path, so the
// test does not depend on the working directory it is run from.
func ownedRoot(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Join(filepath.Dir(here), "flowseer")
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
