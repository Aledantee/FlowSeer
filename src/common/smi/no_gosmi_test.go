package smi_test

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

// TestNoGosmiIdentifierLeaks pins the rule that no gosmi identifier
// appears in any code under common/smi/ outside the differential module.
//
// gosmi is the library this parser replaced, and it survives only as the
// comparand the differential test measures against. That module has its
// own go.module so the dependency stays out of the main graph, but a
// module boundary stops nothing: a file added here that imports gosmi
// compiles fine and quietly pulls the retired library back into the
// parser. Nothing but a test notices, and by the time somebody does the
// dependency has been re-tidied into go.mod.
//
// The check parses with go/parser rather than grepping, so an import
// path or a qualified reference registers and a doc comment does not.
// Prose is free to name gosmi — several comments in this package explain
// why a defect of its is not reproduced — and the contract is only that
// no compiled symbol carries it.
//
// The test file itself is exempted, because the rejection logic spells
// the forbidden string out by necessity.
func TestNoGosmiIdentifierLeaks(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	root := filepath.Dir(here)

	const forbidden = "gosmi"
	const selfFile = "no_gosmi_test.go"

	// The differential module (common/smi/differential/) depends on gosmi
	// as the comparand the parser is measured against and lives in its
	// own go.module; WalkDir descends into it regardless of the module
	// boundary, so it is the sole exemption.
	const exemptDir = "differential"

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
			if rel, _ := filepath.Rel(root, path); rel == exemptDir {
				return filepath.SkipDir
			}

			return nil
		}
		if filepath.Base(path) == selfFile || !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Parsed without comments, so a doc-comment mention of gosmi does
		// not register as a code leak.
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)

		for _, imp := range f.Imports {
			if strings.Contains(strings.ToLower(imp.Path.Value), forbidden) {
				leaks = append(leaks, leak{rel, "import " + imp.Path.Value})
			}
		}

		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.Ident:
				if strings.Contains(strings.ToLower(n.Name), forbidden) {
					leaks = append(leaks, leak{rel, "identifier " + n.Name})
				}
			case *ast.BasicLit:
				// String literals too, which catches an import path
				// reached indirectly — a go:generate line's argument, or a
				// path handed to a loader.
				if n.Kind == token.STRING && strings.Contains(strings.ToLower(n.Value), forbidden) {
					leaks = append(leaks, leak{rel, "string literal " + n.Value})
				}
			}

			return true
		})

		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	for _, l := range leaks {
		t.Errorf("gosmi identifier leak in %s: %s", l.path, l.reason)
	}
}
