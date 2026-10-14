package conformance

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type guardFixture struct {
	stateful   bool
	configOnly bool
	remove     string
	add        string
	importPath string
}

// guardFixtureDir keeps declarations from the gate's valid fixtures. Imports
// are supplied separately so a seam probe has exactly one dependency.
func guardFixtureDir(t *testing.T, fixture guardFixture) (string, string) {
	t.Helper()
	name := "valid_stateless"
	if fixture.stateful {
		name = "valid"
	}
	path := filepath.Join(repoRoot(t), filepath.FromSlash(fixtureRoot), name, "fixture.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, path, source, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	out.WriteString("package fixture\n")
	if fixture.importPath != "" {
		fmt.Fprintf(&out, "import _ %q\n", fixture.importPath)
	}
	removed := 0
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			continue
		}
		lines := declarationLines(&ast.File{Decls: []ast.Decl{decl}})
		if slices.Contains(lines, fixture.remove) {
			removed++
			continue
		}
		if fixture.configOnly && !slices.Contains(lines, "type Config") {
			continue
		}
		out.Write(source[fset.Position(decl.Pos()).Offset:fset.Position(decl.End()).Offset])
		out.WriteString("\n")
	}
	if fixture.remove != "" && removed != 1 {
		t.Fatalf("removed %d declarations for %q, want 1", removed, fixture.remove)
	}
	out.WriteString(fixture.add)
	root := t.TempDir()
	dir := filepath.Join(root, "valid")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(out.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, dir
}

func fixtureFindings(t *testing.T, fixture guardFixture, guards []guard) []string {
	t.Helper()
	root, dir := guardFixtureDir(t, fixture)
	got, err := checkDir(token.NewFileSet(), root, dir, guards, false)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func namedGuard(t *testing.T, guards []guard, name string) guard {
	t.Helper()
	var found []guard
	for _, g := range guards {
		if g.name == name {
			found = append(found, g)
		}
	}
	if len(found) != 1 {
		t.Fatalf("found %d rows for %q, want 1", len(found), name)
	}
	return found[0]
}

func replacedGuard(t *testing.T, guards []guard, name string, change func(*guard)) []guard {
	t.Helper()
	result := slices.Clone(guards)
	namedGuard(t, result, name)
	for i := range result {
		if result[i].name == name {
			change(&result[i])
		}
	}
	return result
}

// TestGuardDeclarationSemantics pins signatures independently of guard literals.
func TestGuardDeclarationSemantics(t *testing.T) {
	rows := []struct {
		name, declaration, signature, wrong string
		statefulOnly                        bool
	}{
		{"layer_name", "const LayerName", "const LayerName", "var LayerName = 0", false},
		{"normalize", "func Config.Normalize", "func (Config) Normalize(layer.Env) (Config)", "func (Config) Normalize() {}", false},
		{"validate", "func Config.Validate", "func (Config) Validate(layer.Env) (error)", "func (Config) Validate() {}", false},
		{"config_clone", "func Config.Clone", "func (Config) Clone() (Config)", "func (Config) Clone() {}", false},
		{"diff", "func Diff", "func Diff(Config, Config) ([]trace.Change)", "func Diff() {}", false},
		{"new", "func New", "func New(Config, layer.Env) (*Layer, error)", "func New() {}", true},
		{"layer_clone", "func Layer.Clone", "func (*Layer) Clone() (*Layer)", "func (*Layer) Clone() {}", true},
		{"retention_key", "func RetentionKey", "func RetentionKey(Config, layer.Env) (string)", "func RetentionKey(Config, layer.Env) int { return 0 }", true},
	}
	hits := 0
	for _, row := range rows {
		for _, stateful := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stateful=%t", row.name, stateful), func(t *testing.T) {
				base := guardFixture{stateful: stateful}
				if got := fixtureFindings(t, base, layerGuards); len(got) != 0 {
					t.Fatalf("valid fixture findings %q, want none", got)
				}
				hits++
				if row.statefulOnly && !stateful {
					return
				}
				base.remove, base.add = row.declaration, row.wrong
				want := []string{fmt.Sprintf("valid: missing %q", row.signature)}
				if got := fixtureFindings(t, base, layerGuards); !slices.Equal(got, want) {
					t.Errorf("wrong declaration findings %q, want %q", got, want)
				}
				hits++
			})
		}
	}
	mutant := replacedGuard(t, layerGuards, "retention_key", func(g *guard) {
		g.literal = "func RetentionKey(Config, layer.Env) (int)"
	})
	if got := fixtureFindings(t, guardFixture{stateful: true}, mutant); !slices.Equal(got, []string{`valid: missing "func RetentionKey(Config, layer.Env) (int)"`}) {
		t.Errorf("mutated signature findings %q, want independently required signature refusal", got)
	}
	hits++
	if hits != 30 {
		t.Errorf("expectation hits %d, want 30", hits)
	}
}

// TestGuardClassificationSemantics isolates each classifier from the others.
func TestGuardClassificationSemantics(t *testing.T) {
	classifiers := []struct{ name, declaration string }{
		{"stateful_layer", "type Layer struct{}"},
		{"stateful_new", "func New() {}"},
		{"stateful_retention_key", "func RetentionKey(Config, layer.Env) string { return \"key\" }"},
	}
	hits := 0
	for _, row := range classifiers {
		t.Run(row.name, func(t *testing.T) {
			guards := []guard{namedGuard(t, layerGuards, row.name), {kind: requireLineIfStateful, literal: "func (Config) Clone() (Config)"}}
			fixture := guardFixture{configOnly: true}
			if got := fixtureFindings(t, fixture, guards); len(got) != 0 {
				t.Errorf("Config-only findings %q, want none", got)
			}
			fixture.add = row.declaration
			want := []string{`valid: missing "func (Config) Clone() (Config)"`}
			if got := fixtureFindings(t, fixture, guards); !slices.Equal(got, want) {
				t.Errorf("sole classifier findings %q, want %q", got, want)
			}
			mutant := replacedGuard(t, guards, row.name, func(g *guard) { g.literal += "Changed" })
			if got := fixtureFindings(t, fixture, mutant); len(got) != 0 {
				t.Errorf("mutated classifier findings %q, want none", got)
			}
			hits += 3
		})
	}
	for _, stateful := range []bool{false, true} {
		t.Run(fmt.Sprintf("advance/stateful=%t", stateful), func(t *testing.T) {
			fixture := guardFixture{stateful: stateful}
			if stateful {
				fixture.remove = "func Layer.Advance"
			}
			if got := fixtureFindings(t, fixture, layerGuards); len(got) != 0 {
				t.Errorf("absent Advance findings %q, want none", got)
			}
			fixture.add = "func (*Layer) Advance(time.Time) layer.Effects { return layer.Effects{} }"
			if got := fixtureFindings(t, fixture, layerGuards); len(got) != 0 {
				t.Errorf("valid Advance findings %q, want none", got)
			}
			fixture.add = "func (*Layer) Advance() {}"
			want := []string{`valid: missing "func (*Layer) Advance(time.Time) (layer.Effects)"`}
			if got := fixtureFindings(t, fixture, layerGuards); !slices.Equal(got, want) {
				t.Errorf("wrong Advance findings %q, want %q", got, want)
			}
			mutant := replacedGuard(t, layerGuards, "advance", func(g *guard) { g.when = "func Layer.AdvanceChanged" })
			if got := fixtureFindings(t, fixture, mutant); len(got) != 0 {
				t.Errorf("mutated Advance condition findings %q, want none", got)
			}
			hits += 4
		})
	}
	if hits != 17 {
		t.Errorf("expectation hits %d, want 17", hits)
	}
}

// TestGuardImportSemantics pins roots, slash boundaries and same-layer imports.
func TestGuardImportSemantics(t *testing.T) {
	const sim = "go.aledante.io/FlowSeer/src/common/sim"
	rows := []struct {
		name, root  string
		guards      []guard
		rootRefused bool
	}{
		{"import_device", sim + "/device", layerGuards, true},
		{"import_fabric", sim + "/fabric", layerGuards, true},
		{"import_sibling", sim + "/layer", layerGuards, false},
		{"device_imports_layer", sim + "/layer", deviceGuards, true},
		{"device_imports_fabric", sim + "/fabric", deviceGuards, true},
		{"fabric_imports_layer", sim + "/layer", fabricGuards, true},
		{"fabric_imports_device_kind", sim + "/device", fabricGuards, false},
	}
	hits := 0
	for _, row := range rows {
		paths := []struct {
			path    string
			refused bool
		}{
			{row.root, row.rootRefused},
			{row.root + "/other", true},
			{row.root + "/other/sub", true},
			{row.root + "x", false},
			{row.root + "x/sub", false},
		}
		if row.name == "import_sibling" {
			paths = append(paths, struct {
				path    string
				refused bool
			}{row.root + "/valid", false}, struct {
				path    string
				refused bool
			}{row.root + "/valid/sub", false}, struct {
				path    string
				refused bool
			}{row.root + "/validx", true})
		}
		for _, stateful := range []bool{false, true} {
			for _, probe := range paths {
				t.Run(fmt.Sprintf("%s/stateful=%t/%s", row.name, stateful, probe.path), func(t *testing.T) {
					got := fixtureFindings(t, guardFixture{stateful: stateful, importPath: probe.path}, row.guards)
					var want []string
					if probe.refused {
						want = []string{fmt.Sprintf("valid/fixture.go:2: imports %q", probe.path)}
					}
					if !slices.Equal(got, want) {
						t.Errorf("findings %q, want %q", got, want)
					}
					hits++
				})
			}
		}
	}
	mutations := []struct {
		name, path string
		refuses    func(string, string) bool
	}{
		{"root equality", sim + "/device", func(_, path string) bool { return strings.HasPrefix(path, sim+"/device/") }},
		{"slash boundary", sim + "/devicex", func(_, path string) bool { return strings.HasPrefix(path, sim+"/device") }},
		{"changed root", sim + "/device/other", importsBelow(sim + "/device/vswitch")},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			fixture := guardFixture{importPath: mutation.path}
			mutant := replacedGuard(t, layerGuards, "import_device", func(g *guard) { g.refuses = mutation.refuses })
			original := fixtureFindings(t, fixture, layerGuards)
			if got := fixtureFindings(t, fixture, mutant); slices.Equal(got, original) {
				t.Errorf("mutated import findings %q equal original %q", got, original)
			}
			hits++
		})
	}
	if hits != 79 {
		t.Errorf("expectation hits %d, want 79", hits)
	}
}

// TestGuardFactSemantics applies refusal rows to stateful and stateless packages.
func TestGuardFactSemantics(t *testing.T) {
	rows := []struct{ name, source, finding string }{
		{"exported_fact", "type FooFact string", "declares type \"FooFact\""},
		{"private_fact", "type fooFact string", ""},
		{"exported_other", "type FooFacts string", ""},
		{"wake", "func (*Layer) Wake() {}", "valid: declares \"func Layer.Wake\""},
		{"age", "func (*Layer) Age() {}", "valid: declares \"func Layer.Age\""},
		{"other_receiver", "func (Config) Wake() {}", ""},
	}
	hits := 0
	for _, stateful := range []bool{false, true} {
		for _, row := range rows {
			t.Run(fmt.Sprintf("%s/stateful=%t", row.name, stateful), func(t *testing.T) {
				got := fixtureFindings(t, guardFixture{stateful: stateful, add: row.source}, layerGuards)
				if row.finding == "" {
					if len(got) != 0 {
						t.Errorf("findings %q, want none", got)
					}
				} else if len(got) != 1 || !strings.HasSuffix(got[0], row.finding) {
					t.Errorf("findings %q, want one ending in %q", got, row.finding)
				}
				hits++
			})
		}
	}
	for _, stateful := range []bool{false, true} {
		fixture := guardFixture{stateful: stateful, add: "type FooFact string"}
		root, dir := guardFixtureDir(t, fixture)
		facts, err := readLayerDir(token.NewFileSet(), root, dir, false)
		if err != nil {
			t.Fatal(err)
		}
		mutant := replacedGuard(t, layerGuards, "exported_fact", func(g *guard) {
			original := g.refuses
			g.refuses = func(name, value string) bool {
				return facts.lines["type Layer"] && original(name, value)
			}
		})
		got, err := checkDir(token.NewFileSet(), root, dir, mutant, false)
		if err != nil {
			t.Fatal(err)
		}
		if stateful {
			if len(got) != 1 || !strings.HasSuffix(got[0], `declares type "FooFact"`) {
				t.Errorf("narrowed stateful fact findings %q, want FooFact refusal", got)
			}
		} else if len(got) != 0 {
			t.Errorf("narrowed stateless fact findings %q, want none", got)
		}
		hits++
	}
	if hits != 14 {
		t.Errorf("expectation hits %d, want 14", hits)
	}
}
