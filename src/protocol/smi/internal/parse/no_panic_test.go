package parse

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestNoPanicOrRecoverInPackage asserts the parser unwinds by the p.fatal flag,
// not by panic: no non-test source file in this package calls the panic or
// recover builtins. The parser recovers from malformed input by design, so a
// panic here would be an unhandled control flow the style guide's Panics
// section bans outside a Must function. It scans the AST rather than the text
// so a "panic" inside a string or comment is not a false positive.
func TestNoPanicOrRecoverInPackage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}

	fset := token.NewFileSet()
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		f, err := goparser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		scanned++

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			if id.Name == "panic" || id.Name == "recover" {
				t.Errorf("%s:%d calls %s; the parser must unwind by p.fatal, not by panic",
					name, fset.Position(id.Pos()).Line, id.Name)
			}

			return true
		})
	}

	if scanned == 0 {
		t.Fatal("scanned no source files; the guard checked nothing")
	}
}
