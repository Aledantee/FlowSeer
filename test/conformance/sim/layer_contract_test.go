// Package conformance enforces layer architecture contracts across
// packages under src/common/sim/layer.
package conformance

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestLayerContract enforces that all layer packages under src/common/sim/layer/
// satisfy the universal and stateful layer architecture contracts.
func TestLayerContract(t *testing.T) {
	root := repoRoot(t)
	layerRoot := filepath.Join(root, "src", "common", "sim", "layer")

	entries, err := os.ReadDir(layerRoot)
	if err != nil {
		t.Fatalf("reading layer directory: %v", err)
	}

	scanned := 0
	fset := token.NewFileSet()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirPath := filepath.Join(layerRoot, entry.Name())
		findings, parsed := checkLayerDir(fset, root, dirPath)
		for _, finding := range findings {
			t.Errorf("%s: %s", entry.Name(), finding)
		}
		if parsed > 0 {
			scanned++
		}
	}

	if scanned == 0 {
		t.Fatal("scanned no layer packages; the gate checked nothing")
	}
	t.Logf("scanned %d layer packages", scanned)
}

// TestLayerContractReportsViolations asserts that checkLayerDir detects each
// category of layer contract violation across fixture packages under testdata/.
func TestLayerContractReportsViolations(t *testing.T) {
	root := repoRoot(t)
	testdataRoot := filepath.Join(root, "test", "conformance", "sim", "testdata")

	tests := []struct {
		name         string
		fixtureDir   string
		wantFindings []string
	}{
		{
			name:         "compliant layer produces no violations",
			fixtureDir:   "valid",
			wantFindings: nil,
		},
		{
			name:       "missing const LayerName is reported",
			fixtureDir: "missing_layer_name",
			wantFindings: []string{
				"test/conformance/sim/testdata/missing_layer_name: missing const LayerName",
			},
		},
		{
			name:       "missing Config type is reported",
			fixtureDir: "missing_config",
			wantFindings: []string{
				"test/conformance/sim/testdata/missing_config: missing Config type",
				"test/conformance/sim/testdata/missing_config: missing Config.Normalize(layer.Env) method",
				"test/conformance/sim/testdata/missing_config: missing Config.Validate(layer.Env) method",
				"test/conformance/sim/testdata/missing_config: missing Config.Clone method",
				"test/conformance/sim/testdata/missing_config: missing Diff function",
			},
		},
		{
			name:       "missing Normalize method is reported",
			fixtureDir: "missing_normalize",
			wantFindings: []string{
				"test/conformance/sim/testdata/missing_normalize: missing Config.Normalize(layer.Env) method",
			},
		},
		{
			name:       "missing Validate method is reported",
			fixtureDir: "missing_validate",
			wantFindings: []string{
				"test/conformance/sim/testdata/missing_validate: missing Config.Validate(layer.Env) method",
			},
		},
		{
			name:       "missing Config.Clone method is reported",
			fixtureDir: "missing_clone",
			wantFindings: []string{
				"test/conformance/sim/testdata/missing_clone: missing Config.Clone method",
			},
		},
		{
			name:       "missing Diff function is reported",
			fixtureDir: "missing_diff",
			wantFindings: []string{
				"test/conformance/sim/testdata/missing_diff: missing Diff function",
			},
		},
		{
			name:       "stateful layer missing New constructor is reported",
			fixtureDir: "missing_new",
			wantFindings: []string{
				"test/conformance/sim/testdata/missing_new: missing New(cfg, layer.Env) constructor",
			},
		},
		{
			name:       "stateful layer missing Layer.Clone method is reported",
			fixtureDir: "missing_layer_clone",
			wantFindings: []string{
				"test/conformance/sim/testdata/missing_layer_clone: missing (*Layer).Clone method",
			},
		},
		{
			name:       "stateful layer missing RetentionKey function is reported",
			fixtureDir: "missing_retention_key",
			wantFindings: []string{
				"test/conformance/sim/testdata/missing_retention_key: missing RetentionKey(cfg, layer.Env) function",
			},
		},
		{
			name:       "forbidden sibling layer import is reported",
			fixtureDir: "import_sibling",
			wantFindings: []string{
				"test/conformance/sim/testdata/import_sibling/fixture.go:5: forbidden sibling import of go.aledante.io/FlowSeer/src/common/sim/layer/bridge",
			},
		},
		{
			name:       "forbidden sim/device import is reported",
			fixtureDir: "import_device",
			wantFindings: []string{
				"test/conformance/sim/testdata/import_device/fixture.go:4: forbidden import of go.aledante.io/FlowSeer/src/common/sim/device/vswitch",
			},
		},
		{
			name:       "forbidden sim/fabric import is reported",
			fixtureDir: "import_fabric",
			wantFindings: []string{
				"test/conformance/sim/testdata/import_fabric/fixture.go:4: forbidden import of go.aledante.io/FlowSeer/src/common/sim/fabric",
			},
		},
		{
			name:       "exported fact type is reported",
			fixtureDir: "exported_fact",
			wantFindings: []string{
				"test/conformance/sim/testdata/exported_fact/fixture.go:10: exported fact type FooFact",
			},
		},
		{
			name:       "forbidden Layer.Wake method is reported",
			fixtureDir: "wake_method",
			wantFindings: []string{
				"test/conformance/sim/testdata/wake_method/fixture.go:38: forbidden Layer method Wake",
			},
		},
		{
			name:       "forbidden Layer.Age method is reported",
			fixtureDir: "age_method",
			wantFindings: []string{
				"test/conformance/sim/testdata/age_method/fixture.go:38: forbidden Layer method Age",
			},
		},
		{
			name:       "wrong Normalize parameter list is reported",
			fixtureDir: "wrong_normalize",
			wantFindings: []string{
				"test/conformance/sim/testdata/wrong_normalize: missing Config.Normalize(layer.Env) method",
			},
		},
		{
			name:       "wrong Validate parameter list is reported",
			fixtureDir: "wrong_validate",
			wantFindings: []string{
				"test/conformance/sim/testdata/wrong_validate: missing Config.Validate(layer.Env) method",
			},
		},
		{
			name:       "wrong New parameter list is reported",
			fixtureDir: "wrong_new_params",
			wantFindings: []string{
				"test/conformance/sim/testdata/wrong_new_params: missing New(cfg, layer.Env) constructor",
			},
		},
		{
			name:       "wrong New results is reported",
			fixtureDir: "wrong_new_results",
			wantFindings: []string{
				"test/conformance/sim/testdata/wrong_new_results: missing New(cfg, layer.Env) constructor",
			},
		},
		{
			name:       "wrong RetentionKey parameter list is reported",
			fixtureDir: "wrong_retention_key",
			wantFindings: []string{
				"test/conformance/sim/testdata/wrong_retention_key: missing RetentionKey(cfg, layer.Env) function",
			},
		},
		{
			name:       "wrong Advance signature is reported",
			fixtureDir: "wrong_advance",
			wantFindings: []string{
				"test/conformance/sim/testdata/wrong_advance/fixture.go:44: invalid (*Layer).Advance signature, want Advance(time.Time) layer.Effects",
			},
		},
	}

	fset := token.NewFileSet()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dirPath := filepath.Join(testdataRoot, tc.fixtureDir)
			findings, _ := checkLayerDir(fset, root, dirPath)
			if !slices.Equal(findings, tc.wantFindings) {
				t.Fatalf("findings mismatch:\ngot:  %v\nwant: %v", findings, tc.wantFindings)
			}
		})
	}
}

// checkLayerDir inspects a single layer package directory and its subdirectories,
// returning all conformance violations found and the count of parsed Go files.
func checkLayerDir(fset *token.FileSet, root, dirPath string) ([]string, int) {
	relDir, err := filepath.Rel(root, dirPath)
	if err != nil {
		relDir = dirPath
	}
	relDir = filepath.ToSlash(relDir)

	var (
		findings []string
		parsed   int
		topFiles []*ast.File
	)

	pkgDirName := filepath.Base(dirPath)

	err = filepath.WalkDir(dirPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		file, err := goparser.ParseFile(fset, path, nil, 0)
		if err != nil {
			findings = append(findings, fmt.Sprintf("parsing file %s: %v", path, err))
			return nil
		}
		parsed++

		// Forbidden imports in non-test files: layers cannot import siblings, device, or fabric.
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			switch {
			case importPath == "go.aledante.io/FlowSeer/src/common/sim/device" || strings.HasPrefix(importPath, "go.aledante.io/FlowSeer/src/common/sim/device/"):
				findings = append(findings, fmt.Sprintf("%s: forbidden import of %s", filePos(fset, root, imp.Pos()), importPath))
			case importPath == "go.aledante.io/FlowSeer/src/common/sim/fabric" || strings.HasPrefix(importPath, "go.aledante.io/FlowSeer/src/common/sim/fabric/"):
				findings = append(findings, fmt.Sprintf("%s: forbidden import of %s", filePos(fset, root, imp.Pos()), importPath))
			case strings.HasPrefix(importPath, "go.aledante.io/FlowSeer/src/common/sim/layer/"):
				sub := strings.TrimPrefix(importPath, "go.aledante.io/FlowSeer/src/common/sim/layer/")
				if sub != "" && sub != pkgDirName && !strings.HasPrefix(sub, pkgDirName+"/") {
					findings = append(findings, fmt.Sprintf("%s: forbidden sibling import of %s", filePos(fset, root, imp.Pos()), importPath))
				}
			}
		}

		// Step fact types must remain unexported within their declaring layer package.
		for _, decl := range file.Decls {
			if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.TYPE {
				for _, spec := range gd.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						if ts.Name.IsExported() && strings.HasSuffix(ts.Name.Name, "Fact") {
							findings = append(findings, fmt.Sprintf("%s: exported fact type %s", filePos(fset, root, ts.Pos()), ts.Name.Name))
						}
					}
				}
			}
		}

		if filepath.Dir(path) == dirPath {
			topFiles = append(topFiles, file)
		}
		return nil
	})
	if err != nil {
		return []string{fmt.Sprintf("walking directory %s: %v", dirPath, err)}, 0
	}

	if len(topFiles) == 0 {
		findings = append(findings, fmt.Sprintf("%s: holds no Go files", relDir))
		return findings, parsed
	}

	var (
		hasLayerName         bool
		hasConfig            bool
		hasNormalize         bool
		hasValidate          bool
		hasConfigClone       bool
		hasDiff              bool
		declaresLayer        bool
		declaresNew          bool
		declaresRetentionKey bool
		hasNew               bool
		hasLayerClone        bool
		hasRetentionKey      bool
	)

	for _, file := range topFiles {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok == token.CONST {
					for _, spec := range d.Specs {
						if vs, ok := spec.(*ast.ValueSpec); ok {
							for _, name := range vs.Names {
								if name.Name == "LayerName" {
									hasLayerName = true
								}
							}
						}
					}
				}
				if d.Tok == token.TYPE {
					for _, spec := range d.Specs {
						if ts, ok := spec.(*ast.TypeSpec); ok {
							if ts.Name.Name == "Config" {
								hasConfig = true
							}
							if ts.Name.Name == "Layer" {
								declaresLayer = true
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil {
					// Package-level functions.
					switch d.Name.Name {
					case "Diff":
						if takesTwoConfigParams(d.Type.Params) && returnsChanges(d.Type.Results) {
							hasDiff = true
						}
					case "New":
						declaresNew = true
						if takesConfigAndEnvParams(d.Type.Params) && returnsLayerAndError(d.Type.Results) {
							hasNew = true
						}
					case "RetentionKey":
						declaresRetentionKey = true
						if takesConfigAndEnvParams(d.Type.Params) && returnsString(d.Type.Results) {
							hasRetentionKey = true
						}
					}
				} else if len(d.Recv.List) > 0 {
					// Methods.
					recvType := receiverTypeName(d.Recv.List[0].Type)
					if recvType == "Config" {
						switch d.Name.Name {
						case "Normalize":
							if takesOnlyEnvParam(d.Type.Params) && returnsConfig(d.Type.Results) {
								hasNormalize = true
							}
						case "Validate":
							if takesOnlyEnvParam(d.Type.Params) && returnsError(d.Type.Results) {
								hasValidate = true
							}
						case "Clone":
							if paramCount(d.Type.Params) == 0 && returnsConfig(d.Type.Results) {
								hasConfigClone = true
							}
						}
					}
					if recvType == "Layer" {
						switch d.Name.Name {
						case "Clone":
							if paramCount(d.Type.Params) == 0 && returnsLayer(d.Type.Results) {
								hasLayerClone = true
							}
						case "Advance":
							if !takesTimeParam(d.Type.Params) || !returnsEffects(d.Type.Results) {
								findings = append(findings, fmt.Sprintf("%s: invalid (*Layer).Advance signature, want Advance(time.Time) layer.Effects", filePos(fset, root, d.Pos())))
							}
						case "Wake", "Age":
							findings = append(findings, fmt.Sprintf("%s: forbidden Layer method %s", filePos(fset, root, d.Pos()), d.Name.Name))
						}
					}
				}
			}
		}
	}

	if !hasLayerName {
		findings = append(findings, fmt.Sprintf("%s: missing const LayerName", relDir))
	}
	if !hasConfig {
		findings = append(findings, fmt.Sprintf("%s: missing Config type", relDir))
	}
	if !hasNormalize {
		findings = append(findings, fmt.Sprintf("%s: missing Config.Normalize(layer.Env) method", relDir))
	}
	if !hasValidate {
		findings = append(findings, fmt.Sprintf("%s: missing Config.Validate(layer.Env) method", relDir))
	}
	if !hasConfigClone {
		findings = append(findings, fmt.Sprintf("%s: missing Config.Clone method", relDir))
	}
	if !hasDiff {
		findings = append(findings, fmt.Sprintf("%s: missing Diff function", relDir))
	}

	isStateful := declaresLayer || declaresNew || declaresRetentionKey
	if isStateful {
		if !hasNew {
			findings = append(findings, fmt.Sprintf("%s: missing New(cfg, layer.Env) constructor", relDir))
		}
		if !hasLayerClone {
			findings = append(findings, fmt.Sprintf("%s: missing (*Layer).Clone method", relDir))
		}
		if !hasRetentionKey {
			findings = append(findings, fmt.Sprintf("%s: missing RetentionKey(cfg, layer.Env) function", relDir))
		}
	}

	return findings, parsed
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return ""
}

func isConfigType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == "Config"
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok && id.Name == "Config" {
			return true
		}
	}
	return false
}

func isEnvType(expr ast.Expr) bool {
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "layer" && sel.Sel.Name == "Env" {
			return true
		}
	}
	return false
}

func isTimeType(expr ast.Expr) bool {
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "time" && sel.Sel.Name == "Time" {
			return true
		}
	}
	return false
}

func isEffectsType(expr ast.Expr) bool {
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "layer" && sel.Sel.Name == "Effects" {
			return true
		}
	}
	return false
}

func paramTypes(fields *ast.FieldList) []ast.Expr {
	if fields == nil {
		return nil
	}
	var types []ast.Expr
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			types = append(types, field.Type)
		} else {
			for range field.Names {
				types = append(types, field.Type)
			}
		}
	}
	return types
}

func paramCount(fields *ast.FieldList) int {
	if fields == nil {
		return 0
	}
	n := 0
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			n++
		} else {
			n += len(field.Names)
		}
	}
	return n
}

func takesTwoConfigParams(params *ast.FieldList) bool {
	types := paramTypes(params)
	return len(types) == 2 && isConfigType(types[0]) && isConfigType(types[1])
}

func takesOnlyEnvParam(params *ast.FieldList) bool {
	types := paramTypes(params)
	return len(types) == 1 && isEnvType(types[0])
}

func takesConfigAndEnvParams(params *ast.FieldList) bool {
	types := paramTypes(params)
	return len(types) == 2 && isConfigType(types[0]) && isEnvType(types[1])
}

func takesTimeParam(params *ast.FieldList) bool {
	types := paramTypes(params)
	return len(types) == 1 && isTimeType(types[0])
}

func returnsConfig(results *ast.FieldList) bool {
	types := paramTypes(results)
	if len(types) != 1 {
		return false
	}
	id, ok := types[0].(*ast.Ident)
	return ok && id.Name == "Config"
}

func returnsError(results *ast.FieldList) bool {
	types := paramTypes(results)
	if len(types) != 1 {
		return false
	}
	id, ok := types[0].(*ast.Ident)
	return ok && id.Name == "error"
}

func returnsChanges(results *ast.FieldList) bool {
	types := paramTypes(results)
	if len(types) != 1 {
		return false
	}
	slice, ok := types[0].(*ast.ArrayType)
	if !ok || slice.Len != nil {
		return false
	}
	sel, ok := slice.Elt.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "trace" && sel.Sel.Name == "Change"
}

func returnsLayer(results *ast.FieldList) bool {
	types := paramTypes(results)
	if len(types) != 1 {
		return false
	}
	star, ok := types[0].(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	return ok && id.Name == "Layer"
}

func returnsLayerAndError(results *ast.FieldList) bool {
	types := paramTypes(results)
	if len(types) != 2 {
		return false
	}
	star, ok := types[0].(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	if !ok || id.Name != "Layer" {
		return false
	}
	errId, ok := types[1].(*ast.Ident)
	return ok && errId.Name == "error"
}

func returnsString(results *ast.FieldList) bool {
	types := paramTypes(results)
	if len(types) != 1 {
		return false
	}
	id, ok := types[0].(*ast.Ident)
	return ok && id.Name == "string"
}

func returnsEffects(results *ast.FieldList) bool {
	types := paramTypes(results)
	return len(types) == 1 && isEffectsType(types[0])
}

func filePos(fset *token.FileSet, root string, pos token.Pos) string {
	position := fset.Position(pos)
	rel, err := filepath.Rel(root, position.Filename)
	if err == nil {
		return fmt.Sprintf("%s:%d", filepath.ToSlash(rel), position.Line)
	}
	return fmt.Sprintf("%s:%d", filepath.ToSlash(position.Filename), position.Line)
}

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
			t.Fatal("repo root not found above the conformance package")
		}

		dir = parent
	}
}
