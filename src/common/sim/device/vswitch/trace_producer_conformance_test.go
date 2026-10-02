package vswitch_test

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

func TestProductionTraceStepsCarrySemanticFacts(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	root := filepath.Dir(thisFile)
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && entry.Name() == "netmodel" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if isTraceStepType(literal.Type) {
				auditStepLiteral(t, fset, root, path, literal)
			}
			if isTraceStepSliceType(literal.Type) {
				for _, element := range literal.Elts {
					step, ok := element.(*ast.CompositeLit)
					if ok && step.Type == nil {
						auditStepLiteral(t, fset, root, path, step)
					}
				}
			}
			return true
		})

		return nil
	})
	if err != nil {
		t.Fatalf("audit production trace steps: %v", err)
	}
}

func auditStepLiteral(t *testing.T, fset *token.FileSet, root, path string, literal *ast.CompositeLit) {
	t.Helper()
	if stepLiteralHasFacts(literal) {
		return
	}
	pos := fset.Position(literal.Pos())
	t.Errorf("production trace step at %s:%d has no Inputs or Outputs", filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator))), pos.Line)
}

func isTraceStepType(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Step" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == "trace"
}

func isTraceStepSliceType(expr ast.Expr) bool {
	array, ok := expr.(*ast.ArrayType)
	return ok && isTraceStepType(array.Elt)
}

func stepLiteralHasFacts(literal *ast.CompositeLit) bool {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		name, ok := field.Key.(*ast.Ident)
		if ok && (name.Name == "Inputs" || name.Name == "Outputs") && factExpressionCanCarryValues(field.Value) {
			return true
		}
	}
	return false
}

func factExpressionCanCarryValues(expr ast.Expr) bool {
	if ident, ok := expr.(*ast.Ident); ok && ident.Name == "nil" {
		return false
	}
	if literal, ok := expr.(*ast.CompositeLit); ok && len(literal.Elts) == 0 {
		return false
	}

	return true
}
