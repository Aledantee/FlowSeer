package netmodel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestLoaderLayout(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var load *ast.FuncDecl
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Count(strings.TrimSuffix(string(source), "\n"), "\n") + 1
		if lines > 1200 {
			t.Errorf("%s has %d lines, limit 1200", name, lines)
		}
		file, err := parser.ParseFile(fset, name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch function := node.(type) {
			case *ast.FuncDecl:
				lines := fset.Position(function.End()).Line - fset.Position(function.Pos()).Line + 1
				if lines > 150 {
					t.Errorf("%s:%s has %d lines, limit 150", name, function.Name.Name, lines)
				}
				if function.Name.Name == "Load" {
					load = function
				}
			case *ast.FuncLit:
				lines := fset.Position(function.End()).Line - fset.Position(function.Pos()).Line + 1
				if lines > 150 {
					t.Errorf("%s:%d function literal has %d lines, limit 150", name, fset.Position(function.Pos()).Line, lines)
				}
			}
			return true
		})
	}
	if load == nil {
		t.Fatal("Load declaration is missing")
	}
	var calls []string
	ast.Inspect(load.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch function := call.Fun.(type) {
		case *ast.Ident:
			calls = append(calls, function.Name)
		case *ast.SelectorExpr:
			calls = append(calls, function.Sel.Name)
		}
		return true
	})
	want := []string{
		"validateInterfaceNames", "newLoadContext", "requestedCapabilities", "loadPorts",
		"prepareOwnership", "summarizeFacets", "selectCapabilities", "reportMissingSTP",
		"reportUnwantedBridge", "reportUnwantedPHY", "reportUnwantedSTP", "reportUnwantedRouting",
		"reportUnwantedFilter", "reportNoSwitchport", "loadPHY", "loadBridge", "loadSTP",
		"loadLAG", "loadRouting", "applyRoutingVLANs", "loadFilter", "Normalize", "Validate",
		"loadFDB", "finishLoad",
	}
	last := -1
	for _, name := range want {
		index := slices.Index(calls, name)
		if index <= last {
			t.Errorf("Load must call %s after the preceding coordinator step, calls %v", name, calls)
			continue
		}
		last = index
	}
}
