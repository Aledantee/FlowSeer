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

// TestNoGosmiIdentifierLeaks pins the rule that no gosmi identifier
// appears in any code under common/snmp/.
//
// mibgen read MIBs through gosmi until the parser under common/smi
// replaced it, and gosmi is now retired from the main module entirely.
// Re-adding it here is a two-line change that compiles: an import plus a
// `go mod tidy`, and the panics and the vendored MIB patches the removal
// bought are back. The exemption set is deliberately empty — nothing
// under common/snmp/ has a reason to reach for the retired library, and
// the one place that still does is the differential module next door
// under common/smi/, which its own guard exempts.
//
// The check parses with go/parser rather than grepping, so an import
// path or a qualified reference registers and a doc comment does not.
// Prose here is free to name gosmi when it explains why something is
// shaped the way it is.
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
