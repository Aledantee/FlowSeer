package main

import (
	"bytes"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/imports"
	"mvdan.cc/gofumpt/format"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// updateGolden refreshes the committed golden file under
// testdata/golden/fakemib/mib.go. Run with
//
//	go test ./src/protocol/snmp/cmd/mibgen -run TestEmit_FakeMIB_Golden -update-golden
//
// after intentional emitter changes. The flag is unbound by default so
// the test asserts equality against the committed file.
var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/golden/* with the current emitter output")

// goldenPkgPrefix is the import-path prefix the golden packages are
// rendered under, so a cross-package reference between them resolves to
// the committed golden directory.
const goldenPkgPrefix = "go.aledante.io/FlowSeer/src/protocol/snmp/cmd/mibgen/testdata/golden"

// loadFakeMIB resolves FAKE-MIB and FAKE-KEYS-MIB from the testdata mibs
// directory and returns FAKE-MIB with the set holding both.
//
// There is nothing to clean up: a load holds no process-wide state, so
// two tests may hold their own sets and neither can observe the other.
func loadFakeMIB(t *testing.T) (*smi.Module, *smi.ModuleSet) {
	t.Helper()
	mibDir, err := filepath.Abs("testdata/mibs")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	// SNMPv2-TC / SNMPv2-SMI live under spec/mib/ietf in the repo; add
	// that search path too so the IMPORTS resolve.
	ietfDir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "spec", "mib", "ietf"))
	if err != nil {
		t.Fatalf("abs ietf: %v", err)
	}
	if _, err := os.Stat(ietfDir); err != nil {
		t.Skipf("spec/mib/ietf not available: %v", err)
	}

	set, err := smi.Load([]string{"FAKE-MIB", "FAKE-KEYS-MIB"}, smi.Options{SearchPaths: []string{mibDir, ietfDir}})
	if err != nil {
		t.Fatalf("load FAKE-MIB: %v", err)
	}
	mod, ok := set.Module("FAKE-MIB")
	if !ok {
		t.Fatal("FAKE-MIB missing from the resolved set")
	}

	return mod, set
}

// TestEmit_FakeMIB_Golden renders FAKE-MIB and FAKE-KEYS-MIB through the
// emitter, both configured so the cross-package key reference resolves,
// and compares each against its committed golden under testdata/golden/.
// Use -update-golden after any intentional emitter change.
func TestEmit_FakeMIB_Golden(t *testing.T) {
	_, set := loadFakeMIB(t)

	for _, name := range []string{"FAKE-MIB", "FAKE-KEYS-MIB"} {
		cm := fakeModules[name]
		mod, ok := set.Module(name)
		if !ok {
			t.Fatalf("%s missing from the resolved set", name)
		}
		got, degraded, err := renderModule(mod, set, cm, fakeModules, goldenPkgPrefix)
		if err != nil {
			t.Fatalf("renderModule %s: %v", name, err)
		}
		if len(degraded) != 0 {
			t.Errorf("%s: degraded references = %v; want none", name, degraded)
		}

		goldenPath := filepath.Join("testdata", "golden", cm.Package, "mib.go")
		if *updateGolden {
			if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
				t.Fatalf("write golden: %v", err)
			}
			t.Logf("updated %s (%d bytes)", goldenPath, len(got))
			continue
		}

		want, err := os.ReadFile(goldenPath)
		if err != nil {
			t.Fatalf("read golden (rerun with -update-golden if first time): %v", err)
		}
		if string(want) != string(got) {
			t.Errorf("golden mismatch: %s; rerun with -update-golden after auditing the diff", goldenPath)
			// Write the candidate next to the golden so devs can diff
			// without re-running with the update flag.
			candidatePath := goldenPath + ".got"
			_ = os.WriteFile(candidatePath, got, 0o644)
			t.Logf("wrote candidate to %s", candidatePath)
		}
	}
}

// TestEmit_GeneratedParses ensures the emitted source is a syntactically
// valid Go file. It does not run the type checker against the host
// package (that would require building common/snmp from a tmp module
// — too heavy for the unit suite).
func TestEmit_GeneratedParses(t *testing.T) {
	mod, set := loadFakeMIB(t)

	cm := Module{Name: "FAKE-MIB", Package: "fakemib"}
	out, _, err := renderModule(mod, set, cm, nil, goldenPkgPrefix)
	if err != nil {
		t.Fatalf("renderModule: %v", err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "fakemib_mib.go", out, parser.AllErrors|parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v\n--- emitted ---\n%s", err, string(out))
	}
	if f.Doc == nil || !strings.HasPrefix(f.Doc.Text(), "Package fakemib ") {
		t.Error("generated package has no package doc comment")
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() && d.Doc == nil {
				t.Errorf("exported function %s has no doc comment", d.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() && s.Doc == nil && d.Doc == nil {
						t.Errorf("exported type %s has no doc comment", s.Name)
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.IsExported() && s.Doc == nil && d.Doc == nil {
							t.Errorf("exported value %s has no doc comment", name)
						}
					}
				}
			}
		}
	}
	for _, imp := range f.Imports {
		if imp.Path.Value == `"go.aledante.io/ae"` {
			t.Error("generated errors must use the repository errs package")
		}
	}
}

// TestEmit_FakeMIB_UsesIdiomaticGeneratedShapes pins the code-style-sensitive
// shapes that are otherwise easy to obscure in a large golden diff.
func TestEmit_FakeMIB_UsesIdiomaticGeneratedShapes(t *testing.T) {
	mod, set := loadFakeMIB(t)

	cm := Module{Name: "FAKE-MIB", Package: "fakemib"}
	out, _, err := renderModule(mod, set, cm, nil, goldenPkgPrefix)
	if err != nil {
		t.Fatalf("renderModule: %v", err)
	}
	src := string(out)

	wantFragments := []string{
		"return snmp.DecodeMacAddress(vbs[0])",
		"snmp.KindCounter32, snmp.DecodeUint32, snmp.RawCounter32, 0)",
		"var FakeRef = snmp.NewFusedTableColumn[int32](snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 1, 3, 1, 7), snmp.KindInteger32, snmp.DecodeInt32, snmp.RawInteger32, 5)",
		"return snmp.DecodeColumn(rv, FakeSoloLastChange, &row.FakeSoloLastChange, row.observed[:])",
		"return snmp.DecodeColumn(rv, FakeMode, &row.FakeMode, row.observed[:])",
		"return snmp.ColumnObserved(r.observed[:], fakeSoloTableColumns, col)",
		"return snmp.EnumString(int32(v), \"FakeModeValue\", fakeModeValueValues, fakeModeValueNames)",
	}
	for _, want := range wantFragments {
		if !strings.Contains(src, want) {
			t.Errorf("emitted source missing fragment %q", want)
		}
	}
	if got := strings.Count(src, "if colID == 2 {"); got != 4 {
		t.Errorf("one-column watch branches = %d, want 4", got)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fakemib_mib.go", out, parser.AllErrors|parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v\n--- emitted ---\n%s", err, src)
	}

	docs := make(map[string]string)
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Doc != nil {
				docs[d.Name.Name] = d.Doc.Text()
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				doc := d.Doc
				if value.Doc != nil {
					doc = value.Doc
				}
				if doc == nil {
					continue
				}
				for _, name := range value.Names {
					docs[name.Name] = doc.Text()
				}
			}
		}
	}
	wantDocs := map[string]string{
		"FakeLegacyMACGet": "Deprecated: fakeLegacyMac is STATUS obsolete in FAKE-MIB.",
		"FakeDeprecated":   "Deprecated: fakeDeprecated is STATUS deprecated in FAKE-MIB.",
		"FakeSoloTable":    "Deprecated: fakeSoloTable is STATUS deprecated in FAKE-MIB.",
	}
	for name, want := range wantDocs {
		if !strings.Contains(docs[name], want) {
			t.Errorf("%s doc = %q, want paragraph %q", name, docs[name], want)
		}
	}
	if strings.Contains(docs["FakeMAC"], "Deprecated:") {
		t.Errorf("FakeMAC doc = %q, want no deprecation paragraph", docs["FakeMAC"])
	}

	checkName := func(kind, name string) {
		if strings.Contains(name, "_") {
			t.Errorf("generated %s identifier %q contains an underscore", kind, name)
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncDecl:
			checkName("function", n.Name.Name)
		case *ast.TypeSpec:
			checkName("type", n.Name.Name)
		case *ast.ValueSpec:
			for _, name := range n.Names {
				checkName("value", name.Name)
			}
		case *ast.StructType:
			for _, field := range n.Fields.List {
				for _, name := range field.Names {
					checkName("field", name.Name)
				}
			}
		}

		return true
	})
}

// TestEmit_GeneratedFormatting ensures the emitter returns source that already
// satisfies the repository's gofumpt and goimports gates. Generated files must
// not require a caller-side formatting pass after renderModule returns.
func TestEmit_GeneratedFormatting(t *testing.T) {
	mod, set := loadFakeMIB(t)

	cm := Module{Name: "FAKE-MIB", Package: "fakemib"}
	out, _, err := renderModule(mod, set, cm, nil, goldenPkgPrefix)
	if err != nil {
		t.Fatalf("renderModule: %v", err)
	}

	fumpt, err := format.Source(out, format.Options{
		LangVersion: "go1.26",
		ModulePath:  "go.aledante.io/FlowSeer",
	})
	if err != nil {
		t.Fatalf("gofumpt generated source: %v", err)
	}
	if !bytes.Equal(out, fumpt) {
		t.Error("generated source is not gofumpt-clean")
	}

	withImports, err := imports.Process("fakemib_mib.go", out, nil)
	if err != nil {
		t.Fatalf("goimports generated source: %v", err)
	}
	if !bytes.Equal(out, withImports) {
		t.Error("generated source is not goimports-clean")
	}
}

// TestEmit_FakeMIB_HasExpectedSymbols asserts the most important
// surface of the generated package without locking it to byte-exact
// shape. The golden test handles byte-exactness; this test protects
// against accidental removal of a public identifier.
func TestEmit_FakeMIB_HasExpectedSymbols(t *testing.T) {
	mod, set := loadFakeMIB(t)

	cm := Module{Name: "FAKE-MIB", Package: "fakemib"}
	out, _, err := renderModule(mod, set, cm, nil, goldenPkgPrefix)
	if err != nil {
		t.Fatalf("renderModule: %v", err)
	}
	src := string(out)

	wantFragments := []string{
		"package fakemib",
		"Code generated by mibgen",
		"func FakeScalarGet",
		"func FakeStatusGet",
		"type FakeStatusValue int32",
		"FakeStatusValueUp",
		"FakeStatusValueDown",
		"FakeStatusValueTesting",
		"var FakeName =",
		"var FakeMAC =",
		"var FakeOctets =",
		"var FakeLastChange =",
		"type FakeTableRow struct",
		"type FakeTableWalker struct",
		"snmp.TableWalker[FakeTableRow]",
		"snmp.Table[FakeTableRow, *FakeTableWalker]",
		"var FakeTable = fakeTableT{",
		// Module-prefixed dispatch map: FAKE-MIB → fAKEMIBOIDDispatch
		// (camelCase upper-cases letters following hyphens, then the
		// first rune is lower-cased to keep the symbol package-private).
		// Renamed in the D5 refactor so two generated files dropped into
		// the same Go package directory don't collide on a shared name.
		"fAKEMIBOIDDispatch = map[string]snmp.AnyColumn",
		"func OIDDispatch()",
		// snmp.MustOID call expressions replaced the old per-package
		// mustParseOID helper. MustOID (not NewOID) is the codegen-facing
		// companion: a malformed generated OID panics at package init.
		"snmp.MustOID(",
		// Per-package ColumnTiers map + accessor, gated on the
		// module containing at least one Watch-eligible table. FAKE-MIB
		// has fakeLastChange as a structural per-row indicator → gate
		// open, map present.
		"fAKEMIBColumnTiers = map[string]snmp.Tier",
		"snmp.TierCounter",
		"snmp.TierIndicator",
		"snmp.TierState",
		"func ColumnTier(col snmp.AnyColumn) snmp.Tier",
		// Per-table ChangeIndicator var (per-row form for
		// fakeTable's fakeLastChange).
		"var FakeTableIndicator =",
		"snmp.MustChangeIndicator",
		"snmp.NewPerRowIndicator",
		// Per-table Watch machinery — decode/equal helpers,
		// typed wrapper, Watch method.
		"func decodeFakeTableRow(",
		"func equalFakeTableRow(",
		"func mergeFakeTableRow(",
		"type FakeTableWatcher struct",
		"func (tw *FakeTableWatcher) Iter()",
		"func (tw *FakeTableWatcher) Err()",
		"func (tw *FakeTableWatcher) Close()",
		"func (tw *FakeTableWatcher) Fallback()",
		"func (tw *FakeTableWatcher) LastTickErr()",
		"func (tw *FakeTableWatcher) TableRoot()",
		"func (fakeTableT) Watch(",
		"snmp.NewWatcher[FakeTableRow]",
		// equal helper uses the type-appropriate comparator
		// (bytes.Equal for the MacAddress []byte field).
		"bytes.Equal(a.FakeMAC, b.FakeMAC)",
		// Per-column observation: the row carries one bit per column
		// and answers by column identity, so a mapper can tell a
		// reported zero from a column the agent never answered.
		"func (r FakeTableRow) Observed(col snmp.AnyColumn) bool",
		"return snmp.ColumnObserved(r.observed[:], fakeTableColumns, col)",
		// BITS decodes to a set of positions, with one named constant
		// per bit the MIB names.
		"FakeCapabilitiesAlpha snmp.BitPos = 0",
		"FakeCapabilitiesGamma snmp.BitPos = 2",
		"snmp.KindOctetString, snmp.DecodeBitSet, 4)",
		"var FakeFlags = snmp.NewTableColumn[snmp.BitSet]",
		"a.FakeFlags.Equal(b.FakeFlags)",
	}
	notWantFragments := []string{
		// Removed in the D5 refactor; ensure the helper does not creep
		// back in.
		"func mustParseOID",
		// A BITS type must not be resolved through the enum path: an
		// int32 constant per bit can carry neither the wire OCTET
		// STRING nor a two-bits-set answer.
		"type FakeCapabilities int32",
		"func (tw *FakeTableWalker) Iter()",
		"func (tw *FakeTableWalker) Err()",
		"func (tw *FakeTableWalker) Close()",
		"func (fakeTableT) Walk(",
		"func (fakeTableT) WalkWithOptions(",
		"case FakeName.Key():",
	}
	for _, w := range wantFragments {
		if !strings.Contains(src, w) {
			t.Errorf("emitted source missing fragment %q\n--- src ---\n%s", w, src)
		}
	}
	for _, w := range notWantFragments {
		if strings.Contains(src, w) {
			t.Errorf("emitted source contains forbidden fragment %q\n--- src ---\n%s", w, src)
		}
	}
}

// TestEmit_TableBoundAndFusedConstructors asserts that accessible columns are
// emitted with bound ordinals and typed fast-path raw decoders where applicable.
func TestEmit_TableBoundAndFusedConstructors(t *testing.T) {
	mod, set := loadFakeMIB(t)
	cm := Module{Name: "FAKE-MIB", Package: "fakemib"}
	out, _, err := renderModule(mod, set, cm, fakeModules, goldenPkgPrefix)
	if err != nil {
		t.Fatalf("renderModule: %v", err)
	}
	src := string(out)

	wantFragments(t, src,
		"var FakeName = snmp.NewTableColumn[string](snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 1, 3, 1, 2), snmp.KindOctetString, snmp.DecodeDisplayString, 0)",
		"var FakeMAC = snmp.NewTableColumn[net.HardwareAddr](snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 1, 3, 1, 3), snmp.KindOctetString, snmp.DecodeMacAddress, 1)",
		"var FakeOctets = snmp.NewFusedTableColumn[uint64](snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 1, 3, 1, 4), snmp.KindCounter64, snmp.DecodeUint64, snmp.RawCounter64, 2)",
		"var FakeLastChange = snmp.NewFusedTableColumn[uint32](snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 1, 3, 1, 5), snmp.KindTimeTicks, snmp.DecodeUint32, snmp.RawTimeTicks, 3)",
		"var FakeRef = snmp.NewFusedTableColumn[fakekeysmib.FakeKeyIndex](snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 1, 3, 1, 7), snmp.KindInteger32, func(vb snmp.VarBind) (fakekeysmib.FakeKeyIndex, error)",
		"snmp.RawInteger32As[fakekeysmib.FakeKeyIndex], 5)",
		"var FakeMode = snmp.NewFusedTableColumn[FakeModeValue](snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 1, 3, 1, 8), snmp.KindInteger32, func(vb snmp.VarBind) (FakeModeValue, error)",
		"snmp.RawInteger32As[FakeModeValue], 6)",
	)
}

// TestEmit_TableNumericOrdinalDecoder asserts that the ordinal decoder switch
// emits one snmp.DecodeColumn call per accessible column.
func TestEmit_TableNumericOrdinalDecoder(t *testing.T) {
	mod, set := loadFakeMIB(t)
	cm := Module{Name: "FAKE-MIB", Package: "fakemib"}
	out, _, err := renderModule(mod, set, cm, fakeModules, goldenPkgPrefix)
	if err != nil {
		t.Fatalf("renderModule: %v", err)
	}
	src := string(out)

	wantFragments(t, src,
		"case 0:\n\t\t\t\treturn snmp.DecodeColumn(rv, FakeName, &row.FakeName, row.observed[:])",
		"case 1:\n\t\t\t\treturn snmp.DecodeColumn(rv, FakeMAC, &row.FakeMAC, row.observed[:])",
		"case 2:\n\t\t\t\treturn snmp.DecodeColumn(rv, FakeOctets, &row.FakeOctets, row.observed[:])",
		"case 3:\n\t\t\t\treturn snmp.DecodeColumn(rv, FakeLastChange, &row.FakeLastChange, row.observed[:])",
		"case 4:\n\t\t\t\treturn snmp.DecodeColumn(rv, FakeFlags, &row.FakeFlags, row.observed[:])",
		"case 5:\n\t\t\t\treturn snmp.DecodeColumn(rv, FakeRef, &row.FakeRef, row.observed[:])",
		"case 6:\n\t\t\t\treturn snmp.DecodeColumn(rv, FakeMode, &row.FakeMode, row.observed[:])",
	)
}

// TestEmit_TableOneCallColumnObserved asserts that Observed delegates directly
// to snmp.ColumnObserved against the package-level column slice.
func TestEmit_TableOneCallColumnObserved(t *testing.T) {
	mod, set := loadFakeMIB(t)
	cm := Module{Name: "FAKE-MIB", Package: "fakemib"}
	out, _, err := renderModule(mod, set, cm, fakeModules, goldenPkgPrefix)
	if err != nil {
		t.Fatalf("renderModule: %v", err)
	}
	src := string(out)

	wantFragments(t, src,
		"func (r FakeTableRow) Observed(col snmp.AnyColumn) bool {\n\treturn snmp.ColumnObserved(r.observed[:], fakeTableColumns, col)\n}",
	)
}

// TestEmit_TableEmbeddedRuntimeTypes asserts that the singleton embeds
// snmp.Table and the named walker embeds snmp.TableWalker.
func TestEmit_TableEmbeddedRuntimeTypes(t *testing.T) {
	mod, set := loadFakeMIB(t)
	cm := Module{Name: "FAKE-MIB", Package: "fakemib"}
	out, _, err := renderModule(mod, set, cm, fakeModules, goldenPkgPrefix)
	if err != nil {
		t.Fatalf("renderModule: %v", err)
	}
	src := string(out)

	wantFragments(t, src,
		"type FakeTableWalker struct {\n\tsnmp.TableWalker[FakeTableRow]\n}",
		"type fakeTableT struct {\n\tsnmp.Table[FakeTableRow, *FakeTableWalker]\n}",
		"var FakeTable = fakeTableT{Table: snmp.NewTable(\"fakeTable\", fakeTableColumns,",
		"func(tw snmp.TableWalker[FakeTableRow]) *FakeTableWalker {\n\treturn &FakeTableWalker{TableWalker: tw}\n})}",
	)
}

// TestEmit_EnumStaticArraysAndHelper asserts that each enum generates static
// value and name slices and a one-call String implementation.
func TestEmit_EnumStaticArraysAndHelper(t *testing.T) {
	mod, set := loadFakeMIB(t)
	cm := Module{Name: "FAKE-MIB", Package: "fakemib"}
	out, _, err := renderModule(mod, set, cm, fakeModules, goldenPkgPrefix)
	if err != nil {
		t.Fatalf("renderModule: %v", err)
	}
	src := string(out)

	wantFragments(t, src,
		"fakeStatusValueValues = []int32{1, 2, 3}",
		"fakeStatusValueNames = []string{\"up\", \"down\", \"testing\"}",
		"func (v FakeStatusValue) String() string {\n\treturn snmp.EnumString(int32(v), \"FakeStatusValue\", fakeStatusValueValues, fakeStatusValueNames)\n}",
		"fakeModeValueValues = []int32{1}",
		"fakeModeValueNames = []string{\"only\"}",
		"func (v FakeModeValue) String() string {\n\treturn snmp.EnumString(int32(v), \"FakeModeValue\", fakeModeValueValues, fakeModeValueNames)\n}",
	)
}

// TestEmit_RejectsRemovedInlineShapes verifies that generated output does not
// declare per-table lifecycle methods, inline decode arms, or enum switches.
func TestEmit_RejectsRemovedInlineShapes(t *testing.T) {
	mod, set := loadFakeMIB(t)
	cm := Module{Name: "FAKE-MIB", Package: "fakemib"}
	out, _, err := renderModule(mod, set, cm, fakeModules, goldenPkgPrefix)
	if err != nil {
		t.Fatalf("renderModule: %v", err)
	}
	src := string(out)

	rejectFragments(t, src,
		"func (tw *FakeTableWalker) Iter()",
		"func (tw *FakeTableWalker) Err()",
		"func (tw *FakeTableWalker) Close()",
		"func (fakeTableT) Walk(",
		"func (fakeTableT) WalkWithOptions(",
		"if col.Key() == FakeSoloLastChange.Key()",
		"if tw.cols[cell.Column].Key() == FakeSoloLastChange.Key()",
		"if v == FakeModeValueOnly",
		"switch v {",
	)
}
