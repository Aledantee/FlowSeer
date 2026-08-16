package snmp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNoGosnmpIdentifierLeaks pins the rule that no gosnmp identifier
// appears in any code under common/snmp/ outside of an explicit
// exemption set.
//
// The check uses go/parser so it is precise: it inspects import paths
// and qualified identifier references (gosnmp.X), but not doc comments
// or unrelated string literals. Doc comments may reference gosnmp
// issues for context — the contract is that no compiled symbol carries
// the dependency.
//
// The test file itself is exempted because the rejection logic mentions
// the string "gosnmp" by necessity.
//
// With the gosnmp backend deleted and the engine flattened into package
// snmp, no production or test code under common/snmp/ may carry a gosnmp
// import — the exemption set is empty. The sole exception added later is
// the standalone benchmark module (common/snmp/bench/), which depends on
// gosnmp purely as a performance comparand and lives in its own
// go.module; the WalkDir below descends into it despite the module
// boundary, so it is exempted here.
func TestNoGosnmpIdentifierLeaks(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	root := filepath.Dir(here)

	const forbidden = "gosnmp"
	const selfFile = "no_gosnmp_test.go"

	// String-literal exemption: the conformance corpus cites the
	// gosnmp issue numbers each pinned quirk encodes (e.g. "gosnmp #241")
	// as DATA in provenance fields — that institutional-memory citation is
	// the whole point of the corpus, not a code dependency. Import and
	// identifier checks still run on this file, so a real gosnmp dependency
	// sneaked in here is still caught; only the belt-and-suspenders
	// string-literal scan is skipped.
	stringLitExempt := map[string]bool{"conformance_corpus_test.go": true}

	// The standalone benchmark module (common/snmp/bench/) depends on
	// gosnmp purely as a performance comparand and has its own go.module;
	// WalkDir descends into it regardless of the module boundary, so it
	// is the sole exemption. Everything else under common/snmp/ must be
	// gosnmp-free.
	exemptDirs := []string{"bench"}

	type leak struct {
		path   string
		reason string
	}
	var leaks []leak

	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(root, path)
			for _, exempt := range exemptDirs {
				if rel == exempt || strings.HasPrefix(rel, exempt+string(filepath.Separator)) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Base(path) == selfFile {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Parse without comments so doc-comment mentions of gosnmp do
		// not register as code leaks.
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		// Imports
		for _, imp := range f.Imports {
			lit := imp.Path.Value
			if strings.Contains(strings.ToLower(lit), forbidden) {
				leaks = append(leaks, leak{rel, "import " + lit})
			}
		}
		// Identifiers
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			if strings.Contains(strings.ToLower(id.Name), forbidden) {
				leaks = append(leaks, leak{rel, "identifier " + id.Name})
			}
			return true
		})
		// String literals — catch sneaky path injection too. Skipped for
		// the conformance corpus, whose provenance data legitimately cites
		// gosnmp issue numbers (see stringLitExempt above).
		if !stringLitExempt[filepath.Base(path)] {
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if strings.Contains(strings.ToLower(lit.Value), forbidden) {
					leaks = append(leaks, leak{rel, "string literal " + lit.Value})
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	if len(leaks) > 0 {
		for _, l := range leaks {
			t.Errorf("gosnmp identifier leak in %s: %s", l.path, l.reason)
		}
	}
}
