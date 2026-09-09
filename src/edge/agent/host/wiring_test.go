package host

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

func TestProductionLaneQueueCapacity(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "host.go", nil, 0)
	if err != nil {
		t.Fatalf("parse host.go: %v", err)
	}

	capacity := 0
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		selector, ok := literal.Type.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Config" {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || pkg.Name != "access" {
			return true
		}
		for _, element := range literal.Elts {
			field, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			name, ok := field.Key.(*ast.Ident)
			if !ok || name.Name != "QueueCapacity" {
				continue
			}
			value, ok := field.Value.(*ast.BasicLit)
			if !ok {
				return false
			}
			capacity, _ = strconv.Atoi(value.Value)
			return false
		}
		return false
	})

	if capacity != 4 {
		t.Errorf("production lane QueueCapacity = %d, want 4", capacity)
	}
}
