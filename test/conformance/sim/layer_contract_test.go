// Package conformance enforces layer architecture contracts across
// packages under src/common/sim/layer.
package conformance

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLayerContract enforces that all layer packages under src/common/sim/layer/
// satisfy the layer architecture contract (R1, R2, R3).
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
		findings := checkLayerDir(fset, root, dirPath)
		for _, finding := range findings {
			t.Errorf("%s: %s", entry.Name(), finding)
		}
		scanned++
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
		name        string
		fixtureDir  string
		wantFinding string
	}{
		{
			name:        "compliant layer produces no violations",
			fixtureDir:  "valid",
			wantFinding: "",
		},
		{
			name:        "missing const LayerName is reported",
			fixtureDir:  "missing_layer_name",
			wantFinding: "missing const LayerName",
		},
		{
			name:        "missing Config type is reported",
			fixtureDir:  "missing_config",
			wantFinding: "missing Config type",
		},
		{
			name:        "missing Normalize method is reported",
			fixtureDir:  "missing_normalize",
			wantFinding: "missing Config.Normalize(layer.Env) method",
		},
		{
			name:        "missing Validate method is reported",
			fixtureDir:  "missing_validate",
			wantFinding: "missing Config.Validate(layer.Env) method",
		},
		{
			name:        "missing Config.Clone method is reported",
			fixtureDir:  "missing_clone",
			wantFinding: "missing Config.Clone method",
		},
		{
			name:        "missing Diff function is reported",
			fixtureDir:  "missing_diff",
			wantFinding: "missing Diff function",
		},
		{
			name:        "stateful layer missing New constructor is reported",
			fixtureDir:  "missing_new",
			wantFinding: "missing New(cfg, layer.Env) constructor",
		},
		{
			name:        "stateful layer missing Layer.Clone method is reported",
			fixtureDir:  "missing_layer_clone",
			wantFinding: "missing (*Layer).Clone method",
		},
		{
			name:        "stateful layer missing RetentionKey function is reported",
			fixtureDir:  "missing_retention_key",
			wantFinding: "missing RetentionKey(cfg, layer.Env) function",
		},
		{
			name:        "forbidden sibling layer import is reported",
			fixtureDir:  "import_sibling",
			wantFinding: "forbidden sibling import of go.aledante.io/FlowSeer/src/common/sim/layer/bridge",
		},
		{
			name:        "forbidden sim/device import is reported",
			fixtureDir:  "import_device",
			wantFinding: "forbidden import of go.aledante.io/FlowSeer/src/common/sim/device/vswitch",
		},
		{
			name:        "forbidden sim/fabric import is reported",
			fixtureDir:  "import_fabric",
			wantFinding: "forbidden import of go.aledante.io/FlowSeer/src/common/sim/fabric",
		},
		{
			name:        "exported fact type is reported",
			fixtureDir:  "exported_fact",
			wantFinding: "exported fact type FooFact",
		},
		{
			name:        "forbidden Layer.Wake method is reported",
			fixtureDir:  "wake_method",
			wantFinding: "forbidden Layer method Wake",
		},
		{
			name:        "forbidden Layer.Age method is reported",
			fixtureDir:  "age_method",
			wantFinding: "forbidden Layer method Age",
		},
	}

	fset := token.NewFileSet()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dirPath := filepath.Join(testdataRoot, tc.fixtureDir)
			findings := checkLayerDir(fset, root, dirPath)
			if tc.wantFinding == "" {
				if len(findings) > 0 {
					t.Fatalf("expected no findings, got: %v", findings)
				}
				return
			}
			matched := false
			for _, f := range findings {
				if strings.Contains(f, tc.wantFinding) {
					matched = true
					break
				}
			}
			if !matched {
				t.Fatalf("expected finding containing %q, got findings: %v", tc.wantFinding, findings)
			}
		})
	}
}

// checkLayerDir inspects a single layer package directory and returns all
// conformance violations found.
func checkLayerDir(fset *token.FileSet, root, dirPath string) []string {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return []string{fmt.Sprintf("reading directory %s: %v", dirPath, err)}
	}

	var files []*ast.File
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		filePath := filepath.Join(dirPath, entry.Name())
		file, err := goparser.ParseFile(fset, filePath, nil, 0)
		if err != nil {
			return []string{fmt.Sprintf("parsing file %s: %v", filePath, err)}
		}
		files = append(files, file)
	}

	if len(files) == 0 {
		return nil
	}

	var findings []string
	pkgDirName := filepath.Base(dirPath)

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

	for _, file := range files {
		// Rule R2: Forbidden imports in non-test files.
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			switch {
			case importPath == "go.aledante.io/FlowSeer/src/common/sim/device" || strings.HasPrefix(importPath, "go.aledante.io/FlowSeer/src/common/sim/device/"):
				findings = append(findings, fmt.Sprintf("%s: forbidden import of %s", filePos(fset, root, imp.Pos()), importPath))
			case importPath == "go.aledante.io/FlowSeer/src/common/sim/fabric" || strings.HasPrefix(importPath, "go.aledante.io/FlowSeer/src/common/sim/fabric/"):
				findings = append(findings, fmt.Sprintf("%s: forbidden import of %s", filePos(fset, root, imp.Pos()), importPath))
			case strings.HasPrefix(importPath, "go.aledante.io/FlowSeer/src/common/sim/layer/"):
				sub := strings.TrimPrefix(importPath, "go.aledante.io/FlowSeer/src/common/sim/layer/")
				if sub != "" && sub != pkgDirName && sub != file.Name.Name {
					findings = append(findings, fmt.Sprintf("%s: forbidden sibling import of %s", filePos(fset, root, imp.Pos()), importPath))
				}
			}
		}

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
							// Rule R3: absence of exported *Fact types.
							if ts.Name.IsExported() && strings.HasSuffix(ts.Name.Name, "Fact") {
								findings = append(findings, fmt.Sprintf("%s: exported fact type %s", filePos(fset, root, ts.Pos()), ts.Name.Name))
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil {
					// Package-level functions.
					switch d.Name.Name {
					case "Diff":
						if takesConfigParams(d.Type.Params) {
							hasDiff = true
						}
					case "New":
						declaresNew = true
						if takesConfigAndEnv(d.Type.Params) && returnsLayerAndError(d.Type.Results) {
							hasNew = true
						}
					case "RetentionKey":
						declaresRetentionKey = true
						if takesConfigAndEnv(d.Type.Params) && returnsString(d.Type.Results) {
							hasRetentionKey = true
						}
					}
				} else if len(d.Recv.List) > 0 {
					// Methods.
					recvType := receiverTypeName(d.Recv.List[0].Type)
					if recvType == "Config" {
						switch d.Name.Name {
						case "Normalize":
							if takesEnvParam(d.Type.Params) && returnsConfig(d.Type.Results) {
								hasNormalize = true
							}
						case "Validate":
							if takesEnvParam(d.Type.Params) && returnsError(d.Type.Results) {
								hasValidate = true
							}
						case "Clone":
							if d.Type.Params.NumFields() == 0 && returnsConfig(d.Type.Results) {
								hasConfigClone = true
							}
						}
					}
					if recvType == "Layer" {
						switch d.Name.Name {
						case "Clone":
							if d.Type.Params.NumFields() == 0 && returnsLayer(d.Type.Results) {
								hasLayerClone = true
							}
						case "Wake", "Age":
							findings = append(findings, fmt.Sprintf("%s: forbidden Layer method %s", filePos(fset, root, d.Pos()), d.Name.Name))
						}
					}
				}
			}
		}
	}

	relDir, err := filepath.Rel(root, dirPath)
	if err != nil {
		relDir = dirPath
	}
	relDir = filepath.ToSlash(relDir)

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

	return findings
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
	switch t := expr.(type) {
	case *ast.SelectorExpr:
		if id, ok := t.X.(*ast.Ident); ok && id.Name == "layer" && t.Sel.Name == "Env" {
			return true
		}
	case *ast.Ident:
		return t.Name == "Env"
	}
	return false
}

func takesConfigParams(params *ast.FieldList) bool {
	if params == nil {
		return false
	}
	count := 0
	for _, field := range params.List {
		if isConfigType(field.Type) {
			if len(field.Names) == 0 {
				count++
			} else {
				count += len(field.Names)
			}
		}
	}
	return count >= 2
}

func takesEnvParam(params *ast.FieldList) bool {
	if params == nil {
		return false
	}
	for _, field := range params.List {
		if isEnvType(field.Type) {
			return true
		}
	}
	return false
}

func takesConfigAndEnv(params *ast.FieldList) bool {
	if params == nil {
		return false
	}
	hasConfig := false
	hasEnv := false
	for _, field := range params.List {
		if isConfigType(field.Type) {
			hasConfig = true
		}
		if isEnvType(field.Type) {
			hasEnv = true
		}
	}
	return hasConfig && hasEnv
}

func returnsConfig(results *ast.FieldList) bool {
	if results == nil || len(results.List) == 0 {
		return false
	}
	switch t := results.List[0].Type.(type) {
	case *ast.Ident:
		return t.Name == "Config"
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok && id.Name == "Config" {
			return true
		}
	}
	return false
}

func returnsError(results *ast.FieldList) bool {
	if results == nil || len(results.List) == 0 {
		return false
	}
	if id, ok := results.List[0].Type.(*ast.Ident); ok && id.Name == "error" {
		return true
	}
	return false
}

func returnsLayer(results *ast.FieldList) bool {
	if results == nil || len(results.List) == 0 {
		return false
	}
	switch t := results.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok && id.Name == "Layer" {
			return true
		}
	case *ast.Ident:
		return t.Name == "Layer"
	}
	return false
}

func returnsLayerAndError(results *ast.FieldList) bool {
	if results == nil || len(results.List) < 2 {
		return false
	}
	hasLayer := false
	switch t := results.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok && id.Name == "Layer" {
			hasLayer = true
		}
	case *ast.Ident:
		return t.Name == "Layer"
	}
	hasError := false
	if id, ok := results.List[1].Type.(*ast.Ident); ok && id.Name == "error" {
		hasError = true
	}
	return hasLayer && hasError
}

func returnsString(results *ast.FieldList) bool {
	if results == nil || len(results.List) == 0 {
		return false
	}
	if id, ok := results.List[0].Type.(*ast.Ident); ok && id.Name == "string" {
		return true
	}
	return false
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
