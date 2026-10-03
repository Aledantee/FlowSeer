// Package conformance enforces layer architecture contracts across
// packages under src/common/sim/layer.
//
// The contract is a table of guards, layerGuards. The gate writes the
// declarations of a layer package as lines of text, one name line and one
// signature line for a function, and each guard tests one literal against
// those lines or against the package's imports and type names. A guard
// arrives with a fixture directory under testdata/ (and a second stateless
// fixture for each of the five member rows), and
// TestLayerContractGuardsRefuseTheirFixtures requires each fixture to produce
// the findings listed for it, and none once the row is dropped.
package conformance

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	simImportPath     = "go.aledante.io/FlowSeer/src/common/sim"
	layerImportPrefix = simImportPath + "/layer/"
	fixtureRoot       = "test/conformance/sim/testdata"
)

type guardKind int

const (
	// requireLine reports a package without the literal line.
	requireLine guardKind = iota
	// requireLineIfStateful reports a stateful package without the literal line.
	requireLineIfStateful
	// requireLineIfPresent reports a package that holds the when line and
	// lacks the literal line.
	requireLineIfPresent
	// refuseLine reports a package that holds the literal line.
	refuseLine
	// marksStateful makes the package stateful when it holds the literal
	// line. It reports nothing itself.
	marksStateful
	// refuseImport reports each import of a non-test file outside
	// testdata under the directory for which refuses returns true.
	refuseImport
	// refuseTypeName reports each type of a non-test file outside
	// testdata under the directory for which refuses returns true.
	refuseTypeName
)

// guard is one row of the contract. Its name is the fixture directory the
// gate refuses, with a second stateless fixture for each member row.
type guard struct {
	name    string
	kind    guardKind
	literal string
	when    string
	// refuses receives the directory's name and an import path or a type
	// name.
	refuses func(dirName, value string) bool
}

// layerGuards lists what the gate enforces. A guard arrives with its fixture.
var layerGuards = []guard{
	{name: "layer_name", kind: requireLine, literal: "const LayerName"},
	{name: "normalize", kind: requireLine, literal: "func (Config) Normalize(layer.Env) (Config)"},
	{name: "validate", kind: requireLine, literal: "func (Config) Validate(layer.Env) (error)"},
	{name: "config_clone", kind: requireLine, literal: "func (Config) Clone() (Config)"},
	{name: "diff", kind: requireLine, literal: "func Diff(Config, Config) ([]trace.Change)"},
	{name: "stateful_layer", kind: marksStateful, literal: "type Layer"},
	{name: "stateful_new", kind: marksStateful, literal: "func New"},
	{name: "stateful_retention_key", kind: marksStateful, literal: "func RetentionKey"},
	{name: "new", kind: requireLineIfStateful, literal: "func New(Config, layer.Env) (*Layer, error)"},
	{name: "layer_clone", kind: requireLineIfStateful, literal: "func (*Layer) Clone() (*Layer)"},
	{name: "retention_key", kind: requireLineIfStateful, literal: "func RetentionKey(Config, layer.Env) (string)"},
	{name: "advance", kind: requireLineIfPresent, when: "func Layer.Advance", literal: "func (*Layer) Advance(time.Time) (layer.Effects)"},
	{name: "wake", kind: refuseLine, literal: "func Layer.Wake"},
	{name: "age", kind: refuseLine, literal: "func Layer.Age"},
	{name: "exported_fact", kind: refuseTypeName, refuses: exportedFactType},
	{name: "import_sibling", kind: refuseImport, refuses: importsSiblingLayer},
	{name: "import_device", kind: refuseImport, refuses: importsBelow(simImportPath + "/device")},
	{name: "import_fabric", kind: refuseImport, refuses: importsBelow(simImportPath + "/fabric")},
}

func exportedFactType(_, name string) bool {
	return ast.IsExported(name) && strings.HasSuffix(name, "Fact")
}

// importsSiblingLayer refuses an import below sim/layer/ whose first path
// element is not the importing directory's name. The layer package itself,
// sim/layer, is not below sim/layer/.
func importsSiblingLayer(dirName, path string) bool {
	rest, ok := strings.CutPrefix(path, layerImportPrefix)
	if !ok {
		return false
	}
	first, _, _ := strings.Cut(rest, "/")

	return first != dirName
}

func importsBelow(root string) func(dirName, path string) bool {
	return func(_, path string) bool {
		return path == root || strings.HasPrefix(path, root+"/")
	}
}

// TestLayerContract enforces layerGuards on every package directory under
// src/common/sim/layer/.
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
		findings, err := checkLayerDir(fset, root, filepath.Join(layerRoot, entry.Name()), layerGuards)
		if err != nil {
			t.Fatalf("checking %s: %v", entry.Name(), err)
		}
		for _, finding := range findings {
			t.Error(finding)
		}
		scanned++
	}

	if scanned == 0 {
		t.Fatal("scanned no layer packages; the gate checked nothing")
	}
	t.Logf("scanned %d layer packages", scanned)
}

// refusedFindings is what the full guard list reports for each row's
// fixture. A stateful_ row reports no finding of its own: its fixture is a
// stateless package made stateful, so the findings are the members the
// stateful rows require and the fixture lacks.
var refusedFindings = map[string][]string{
	"layer_name": {
		`test/conformance/sim/testdata/layer_name: missing "const LayerName"`,
	},
	"layer_name_stateless": {
		`test/conformance/sim/testdata/layer_name_stateless: missing "const LayerName"`,
	},
	"normalize": {
		`test/conformance/sim/testdata/normalize: missing "func (Config) Normalize(layer.Env) (Config)"`,
	},
	"normalize_stateless": {
		`test/conformance/sim/testdata/normalize_stateless: missing "func (Config) Normalize(layer.Env) (Config)"`,
	},
	"validate": {
		`test/conformance/sim/testdata/validate: missing "func (Config) Validate(layer.Env) (error)"`,
	},
	"validate_stateless": {
		`test/conformance/sim/testdata/validate_stateless: missing "func (Config) Validate(layer.Env) (error)"`,
	},
	"config_clone": {
		`test/conformance/sim/testdata/config_clone: missing "func (Config) Clone() (Config)"`,
	},
	"config_clone_stateless": {
		`test/conformance/sim/testdata/config_clone_stateless: missing "func (Config) Clone() (Config)"`,
	},
	"diff": {
		`test/conformance/sim/testdata/diff: missing "func Diff(Config, Config) ([]trace.Change)"`,
	},
	"diff_stateless": {
		`test/conformance/sim/testdata/diff_stateless: missing "func Diff(Config, Config) ([]trace.Change)"`,
	},
	"stateful_layer": {
		`test/conformance/sim/testdata/stateful_layer: missing "func New(Config, layer.Env) (*Layer, error)"`,
		`test/conformance/sim/testdata/stateful_layer: missing "func (*Layer) Clone() (*Layer)"`,
		`test/conformance/sim/testdata/stateful_layer: missing "func RetentionKey(Config, layer.Env) (string)"`,
	},
	"stateful_new": {
		`test/conformance/sim/testdata/stateful_new: missing "func New(Config, layer.Env) (*Layer, error)"`,
		`test/conformance/sim/testdata/stateful_new: missing "func (*Layer) Clone() (*Layer)"`,
		`test/conformance/sim/testdata/stateful_new: missing "func RetentionKey(Config, layer.Env) (string)"`,
	},
	"stateful_retention_key": {
		`test/conformance/sim/testdata/stateful_retention_key: missing "func New(Config, layer.Env) (*Layer, error)"`,
		`test/conformance/sim/testdata/stateful_retention_key: missing "func (*Layer) Clone() (*Layer)"`,
	},
	"new": {
		`test/conformance/sim/testdata/new: missing "func New(Config, layer.Env) (*Layer, error)"`,
	},
	"layer_clone": {
		`test/conformance/sim/testdata/layer_clone: missing "func (*Layer) Clone() (*Layer)"`,
	},
	"retention_key": {
		`test/conformance/sim/testdata/retention_key: missing "func RetentionKey(Config, layer.Env) (string)"`,
	},
	"advance": {
		`test/conformance/sim/testdata/advance: missing "func (*Layer) Advance(time.Time) (layer.Effects)"`,
	},
	"wake": {
		`test/conformance/sim/testdata/wake: declares "func Layer.Wake"`,
	},
	"age": {
		`test/conformance/sim/testdata/age: declares "func Layer.Age"`,
	},
	"exported_fact": {
		`test/conformance/sim/testdata/exported_fact/fixture.go:42: declares type "FooFact"`,
		`test/conformance/sim/testdata/exported_fact/sub/x.go:3: declares type "FooFact"`,
	},
	"import_sibling": {
		`test/conformance/sim/testdata/import_sibling/sub/x.go:4: imports "go.aledante.io/FlowSeer/src/common/sim/layer/bridge"`,
	},
	"import_device": {
		`test/conformance/sim/testdata/import_device/fixture.go:4: imports "go.aledante.io/FlowSeer/src/common/sim/device/vswitch"`,
	},
	"import_fabric": {
		`test/conformance/sim/testdata/import_fabric/fixture.go:4: imports "go.aledante.io/FlowSeer/src/common/sim/fabric"`,
	},
}

// fixtureDirs returns the fixture directories that hold guard name. The five
// member rows are held by both a stateful and a stateless fixture.
func fixtureDirs(name string) []string {
	switch name {
	case "layer_name", "normalize", "validate", "config_clone", "diff":
		return []string{name, name + "_stateless"}
	default:
		return []string{name}
	}
}

// TestLayerContractGuardsRefuseTheirFixtures runs the whole list over each
// row's fixtures and requires the findings listed for it, and none once the
// row is dropped.
func TestLayerContractGuardsRefuseTheirFixtures(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	named := map[string]bool{"valid": true, "valid_stateless": true}
	for _, g := range layerGuards {
		for _, name := range fixtureDirs(g.name) {
			if named[name] {
				t.Errorf("row %q repeats a fixture directory %q", g.name, name)
			}
			named[name] = true

			t.Run(name, func(t *testing.T) {
				want, ok := refusedFindings[name]
				if !ok {
					t.Fatalf("refusedFindings lists nothing for row %q", name)
				}
				if g.kind != marksStateful && len(want) == 0 {
					t.Fatalf("refusedFindings lists no findings for row %q", name)
				}
				dir := filepath.Join(root, filepath.FromSlash(fixtureRoot), name)

				got, err := checkLayerDir(fset, root, dir, layerGuards)
				if err != nil {
					t.Fatalf("checking fixture: %v", err)
				}
				if !slices.Equal(got, want) {
					t.Errorf("findings with every row:\ngot  %q\nwant %q", got, want)
				}

				without := slices.DeleteFunc(slices.Clone(layerGuards), func(other guard) bool {
					return other.name == g.name
				})
				got, err = checkLayerDir(fset, root, dir, without)
				if err != nil {
					t.Fatalf("checking fixture without row %q: %v", g.name, err)
				}
				if len(got) != 0 {
					t.Errorf("findings without row %q, want none: %q", g.name, got)
				}
			})
		}
	}

	for name := range refusedFindings {
		if !named[name] {
			t.Errorf("refusedFindings names %q, which no row does", name)
		}
	}

	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(fixtureRoot)))
	if err != nil {
		t.Fatalf("reading %s: %v", fixtureRoot, err)
	}
	for _, entry := range entries {
		if entry.IsDir() && !named[entry.Name()] {
			t.Errorf("fixture directory %s/%s is named by no row", fixtureRoot, entry.Name())
		}
	}
}

// TestLayerContractPassesValidFixtures holds what the gate lets through.
// valid_stateless is a package with the five members every layer has, as phy
// is, and valid is a whole stateful layer with the source honest packages hold.
// Its imports of sim/devicex and sim/fabricx begin with a refused root and
// are not below it.
func TestLayerContractPassesValidFixtures(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	for _, name := range []string{"valid", "valid_stateless"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(root, filepath.FromSlash(fixtureRoot), name)
			got, err := checkLayerDir(fset, root, dir, layerGuards)
			if err != nil {
				t.Fatalf("checking fixture: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("findings, want none: %q", got)
			}
		})
	}
}

// TestLayerContractStopsOnUnparsableFile writes the broken file under
// t.TempDir(), since gofumpt exits 2 on a Go file that does not parse and the
// verifier runs it over every changed file.
func TestLayerContractStopsOnUnparsableFile(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte("package broken\n\nfunc (\n"), 0o600); err != nil {
		t.Fatalf("writing broken file: %v", err)
	}

	findings, err := checkLayerDir(token.NewFileSet(), root, dir, layerGuards)
	if err == nil {
		t.Fatalf("got findings %q and no error, want an error", findings)
	}
	if !strings.Contains(err.Error(), "broken.go") {
		t.Errorf("error %q does not name the file", err)
	}
}

// TestLayerContractReadsEmptyReceiverList holds that func () New() {} is
// read as a plain function. The package comment of go/parser/parser.go says
// the parser accepts a larger language than is syntactically permitted by
// the Go spec, so go/parser accepts the declaration and returns a non-nil
// receiver list with no entry.
func TestLayerContractReadsEmptyReceiverList(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "stray")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("creating directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stray.go"), []byte("package stray\n\nfunc () New() {}\n"), 0o600); err != nil {
		t.Fatalf("writing stray file: %v", err)
	}

	got, err := checkLayerDir(token.NewFileSet(), root, dir, layerGuards)
	if err != nil {
		t.Fatalf("checking directory: %v", err)
	}

	want := []string{
		`stray: missing "const LayerName"`,
		`stray: missing "func (Config) Normalize(layer.Env) (Config)"`,
		`stray: missing "func (Config) Validate(layer.Env) (error)"`,
		`stray: missing "func (Config) Clone() (Config)"`,
		`stray: missing "func Diff(Config, Config) ([]trace.Change)"`,
		`stray: missing "func New(Config, layer.Env) (*Layer, error)"`,
		`stray: missing "func (*Layer) Clone() (*Layer)"`,
		`stray: missing "func RetentionKey(Config, layer.Env) (string)"`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("findings:\ngot  %q\nwant %q", got, want)
	}
}

// TestLayerContractRefusesBackquotedImport holds that import paths written
// as backquoted raw string literals are read with strconv.Unquote and refused.
func TestLayerContractRefusesBackquotedImport(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "stray")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("creating directory: %v", err)
	}
	src := "package stray\n\nimport _ `go.aledante.io/FlowSeer/src/common/sim/device/vswitch`\n"
	if err := os.WriteFile(filepath.Join(dir, "stray.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing stray file: %v", err)
	}

	got, err := checkLayerDir(token.NewFileSet(), root, dir, layerGuards)
	if err != nil {
		t.Fatalf("checking directory: %v", err)
	}

	wantFinding := `stray/stray.go:3: imports "go.aledante.io/FlowSeer/src/common/sim/device/vswitch"`
	if !slices.Contains(got, wantFinding) {
		t.Errorf("findings %q do not include %q", got, wantFinding)
	}
}

// layerFacts is what checkLayerDir reads from one layer package directory.
type layerFacts struct {
	dirName string
	// lines holds a name line and, for a function, a signature line for
	// each declaration of the non-test files in the directory itself.
	lines map[string]bool
	// imports and types come from every non-test file outside testdata
	// under the directory, subdirectories included.
	imports []sourceValue
	types   []sourceValue
}

type sourceValue struct {
	pos   string
	value string
}

// checkLayerDir reads the layer package directory at dir by path and returns
// what guards report. A file that does not parse is an error.
func checkLayerDir(fset *token.FileSet, root, dir string, guards []guard) ([]string, error) {
	facts, err := readLayerDir(fset, root, dir)
	if err != nil {
		return nil, err
	}

	relDir := relativePath(root, dir)
	stateful := false
	for _, g := range guards {
		if g.kind == marksStateful && facts.lines[g.literal] {
			stateful = true
		}
	}

	var findings []string
	for _, g := range guards {
		switch g.kind {
		case requireLine:
			if !facts.lines[g.literal] {
				findings = append(findings, fmt.Sprintf("%s: missing %q", relDir, g.literal))
			}
		case requireLineIfStateful:
			if stateful && !facts.lines[g.literal] {
				findings = append(findings, fmt.Sprintf("%s: missing %q", relDir, g.literal))
			}
		case requireLineIfPresent:
			if facts.lines[g.when] && !facts.lines[g.literal] {
				findings = append(findings, fmt.Sprintf("%s: missing %q", relDir, g.literal))
			}
		case refuseLine:
			if facts.lines[g.literal] {
				findings = append(findings, fmt.Sprintf("%s: declares %q", relDir, g.literal))
			}
		case refuseImport:
			for _, imp := range facts.imports {
				if g.refuses(facts.dirName, imp.value) {
					findings = append(findings, fmt.Sprintf("%s: imports %q", imp.pos, imp.value))
				}
			}
		case refuseTypeName:
			for _, typ := range facts.types {
				if g.refuses(facts.dirName, typ.value) {
					findings = append(findings, fmt.Sprintf("%s: declares type %q", typ.pos, typ.value))
				}
			}
		case marksStateful:
		}
	}

	return findings, nil
}

func readLayerDir(fset *token.FileSet, root, dir string) (layerFacts, error) {
	facts := layerFacts{dirName: filepath.Base(dir), lines: map[string]bool{}}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
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

		file, err := goparser.ParseFile(fset, path, nil, goparser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", relativePath(root, path), err)
		}

		for _, imp := range file.Imports {
			val, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return fmt.Errorf("reading import path %s in %s: %w", imp.Path.Value, relativePath(root, path), err)
			}
			facts.imports = append(facts.imports, sourceValue{
				pos:   filePos(fset, root, imp.Pos()),
				value: val,
			})
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				if typ, ok := spec.(*ast.TypeSpec); ok {
					facts.types = append(facts.types, sourceValue{
						pos:   filePos(fset, root, typ.Pos()),
						value: typ.Name.Name,
					})
				}
			}
		}

		if filepath.Dir(path) == dir {
			for _, line := range declarationLines(file) {
				facts.lines[line] = true
			}
		}

		return nil
	})
	if err != nil {
		return layerFacts{}, fmt.Errorf("reading %s: %w", relativePath(root, dir), err)
	}

	return facts, nil
}

// declarationLines writes each declaration of file as text: `const LayerName`,
// `type Layer`, `func New`, and `func Layer.Wake` for a method, whose pointer
// star is dropped. A function also gets a signature line of its receiver
// type, one type per parameter, and its results in parentheses whatever their
// count: `func (*Layer) Clone() (*Layer)`. Parameter names do not appear.
func declarationLines(file *ast.File) []string {
	var lines []string
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			lines = append(lines, genDeclLines(d)...)
		case *ast.FuncDecl:
			lines = append(lines, funcLines(d)...)
		}
	}

	return lines
}

func genDeclLines(decl *ast.GenDecl) []string {
	var keyword string
	switch decl.Tok {
	case token.CONST:
		keyword = "const"
	case token.VAR:
		keyword = "var"
	case token.TYPE:
		keyword = "type"
	}

	var lines []string
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.ValueSpec:
			for _, name := range s.Names {
				lines = append(lines, keyword+" "+name.Name)
			}
		case *ast.TypeSpec:
			lines = append(lines, keyword+" "+s.Name.Name)
		}
	}

	return lines
}

func funcLines(fn *ast.FuncDecl) []string {
	name := "func " + fn.Name.Name
	signature := "func "
	if fn.Recv.NumFields() > 0 {
		recv := types.ExprString(fn.Recv.List[0].Type)
		name = "func " + strings.TrimPrefix(recv, "*") + "." + fn.Name.Name
		signature += "(" + recv + ") "
	}
	signature += fmt.Sprintf("%s(%s) (%s)", fn.Name.Name, typeList(fn.Type.Params), typeList(fn.Type.Results))

	return []string{name, signature}
}

// typeList prints one type per parameter or result, so `a, b Config` and
// an unnamed `Config` read alike.
func typeList(fields *ast.FieldList) string {
	if fields == nil {
		return ""
	}

	var list []string
	for _, field := range fields.List {
		for range max(len(field.Names), 1) {
			list = append(list, types.ExprString(field.Type))
		}
	}

	return strings.Join(list, ", ")
}

func relativePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}

	return filepath.ToSlash(rel)
}

func filePos(fset *token.FileSet, root string, pos token.Pos) string {
	position := fset.Position(pos)

	return fmt.Sprintf("%s:%d", relativePath(root, position.Filename), position.Line)
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
