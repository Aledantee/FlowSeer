package vswitch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceSizeLimits(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Count(string(source), "\n")
		if len(source) > 0 && source[len(source)-1] != '\n' {
			lines++
		}
		if lines > 1200 {
			t.Errorf("%s: %d lines, limit 1200", name, lines)
		}
		positions := token.NewFileSet()
		file, err := parser.ParseFile(positions, name, source, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			length := positions.Position(fn.End()).Line - positions.Position(fn.Pos()).Line + 1
			if length > 150 && (fn.Doc == nil || !strings.Contains(fn.Doc.Text(), "one unit")) {
				t.Errorf("%s:%d: %s has %d lines, limit 150 without a reason to keep one unit", name, positions.Position(fn.Pos()).Line, fn.Name.Name, length)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no production source checked")
	}
}
