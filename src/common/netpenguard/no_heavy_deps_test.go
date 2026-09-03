// Package netpenguard checks Go source imports for the netpen module's heavy
// dependencies. Only src/edge/netpen may import charm.land/ or
// github.com/gopacket/ packages; dot-directories and testdata are not scanned.
package netpenguard

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// forbiddenImports are the heavy dependency path prefixes that must not appear
// in the main module. They may appear inside src/edge/netpen (the quarantined
// nested module) and nowhere else.
var forbiddenImports = []string{
	"charm.land/",
	"github.com/gopacket/",
}

// exemptDirs are directory paths (relative to the repo root) where forbidden
// imports are allowed. src/edge/netpen is the quarantined nested module.
var exemptDirs = []string{
	filepath.Join("src", "edge", "netpen"),
}

// TestNoHeavyDepsOutsideNetpen walks the repo from its root and asserts that
// no .go file outside src/edge/netpen imports a forbidden heavy dependency. The
// walk skips dot-directories and testdata.
func TestNoHeavyDepsOutsideNetpen(t *testing.T) {
	root := repoRoot(t)
	leaks, err := scanHeavyDeps(root)
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	for _, l := range leaks {
		t.Errorf("heavy dependency leak in %s: %s", l.path, l.reason)
	}
}

type dependencyLeak struct {
	path   string
	reason string
}

func scanHeavyDeps(root string) ([]dependencyLeak, error) {
	var leaks []dependencyLeak

	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			name := d.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "testdata") {
				return filepath.SkipDir
			}

			rel, _ := filepath.Rel(root, path)
			for _, exempt := range exemptDirs {
				if rel == exempt || strings.HasPrefix(rel, exempt+string(filepath.Separator)) {
					return filepath.SkipDir
				}
			}

			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Parse without comments so doc-comment mentions of the
		// forbidden packages do not register as code leaks.
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(root, path)

		for _, imp := range f.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}

			for _, forbidden := range forbiddenImports {
				if strings.Contains(importPath, forbidden) {
					leaks = append(leaks, dependencyLeak{rel, "import " + imp.Path.Value})
				}
			}
		}

		return nil
	})
	return leaks, err
}

// repoRoot finds the repository root by walking up from the working directory
// until it finds a go.mod declaring the main module path. It mirrors the
// repoRoot pattern from src/common/errs/code_test.go.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting working directory: %v", err)
	}

	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module go.aledante.io/FlowSeer\n") {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found above the netpenguard package")
		}

		dir = parent
	}
}
