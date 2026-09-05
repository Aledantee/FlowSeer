package main

import (
	"crypto/sha256"
	"encoding/hex"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// renderConfigured renders one module from the repository's real
// mibgen.yaml. Tests use it where the thing under test is how the
// shipped configuration resolves, not how the emitter handles a
// hand-built fixture.
func renderConfigured(t *testing.T, name string) string {
	t.Helper()
	root := repoRoot(t)
	if _, err := os.Stat(filepath.Join(root, "spec", "mib")); err != nil {
		t.Skipf("spec/mib not available: %v", err)
	}

	cfg, err := LoadConfig(filepath.Join(root, "mibgen.yaml"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	set, err := LoadModules(cfg)
	if err != nil {
		t.Fatalf("load modules: %v", err)
	}

	cfgByName := make(map[string]Module, len(cfg.Modules))
	for _, m := range cfg.Modules {
		cfgByName[m.Name] = m
	}
	cm, ok := cfgByName[name]
	if !ok {
		t.Fatalf("module %q is not configured", name)
	}
	mod, ok := set.Module(name)
	if !ok {
		t.Fatalf("module %q missing from the resolved set", name)
	}

	out, _, err := renderModule(mod, set, cm, cfgByName, defaultPkgPrefix)
	if err != nil {
		t.Fatalf("renderModule %s: %v", name, err)
	}

	return string(out)
}

// kindOf renders the snmp.Kind a type resolves to, as the emitted
// source spells it.
func kindOf(t *testing.T, ec *emitCtx, ty *smi.Type) string {
	t.Helper()
	r := naturalResolved(ec, "someObject", ty)

	return r.Kind.GoString()
}

// baseTypeCtx builds an emitCtx whose module imports every SMI
// application type, so a resolution test is not answering the
// IMPORTS question by accident.
func baseTypeCtx() *emitCtx {
	mod := &smi.Module{
		Name: "TEST-MIB",
		Imports: []smi.Import{{
			Module: "SNMPv2-SMI",
			Symbols: []string{
				"Integer32", "Unsigned32", "Counter32", "Counter64",
				"Gauge32", "TimeTicks", "IpAddress", "Opaque",
			},
		}},
	}

	return newEmitCtx(mod, &smi.ModuleSet{}, Module{Name: "TEST-MIB", Package: "testmib"}, nil, "")
}

// TestResolve_ApplicationTypeWireKinds pins the wire Kind of every SMI
// application type. They share integer widths with each other and with
// Integer32, so the Kind is the only thing telling a decoder which
// VarBind variant an agent will send.
func TestResolve_ApplicationTypeWireKinds(t *testing.T) {
	ec := baseTypeCtx()
	for _, tc := range []struct {
		base smi.BaseType
		want string
	}{
		{smi.BaseCounter32, "KindCounter32"},
		{smi.BaseCounter64, "KindCounter64"},
		{smi.BaseGauge32, "KindGauge32"},
		{smi.BaseTimeTicks, "KindTimeTicks"},
		{smi.BaseIPAddress, "KindIPAddress"},
		{smi.BaseOpaque, "KindOpaque"},
		{smi.BaseInteger32, "KindInteger32"},
		{smi.BaseUnsigned32, "KindUinteger32"},
	} {
		t.Run(tc.base.String(), func(t *testing.T) {
			for _, name := range []string{"", "NamedConvention"} {
				ec.visible[name] = true
				got := kindOf(t, ec, &smi.Type{Name: name, Base: tc.base})
				if !strings.Contains(got, tc.want) {
					t.Errorf("%v named %q resolved to %s; want %s", tc.base, name, got, tc.want)
				}
			}
		})
	}
}

// TimeStamp has TimeTicks wire encoding but retains its convention's
// indicator tier; preserving the wire kind must not erase that meaning.
func TestResolve_TimeStampRetainsWireKindAndIndicatorTier(t *testing.T) {
	ec := baseTypeCtx()

	raw := kindOf(t, ec, &smi.Type{Base: smi.BaseTimeTicks})
	if !strings.Contains(raw, "KindTimeTicks") {
		t.Errorf("raw TimeTicks resolved to %s; want KindTimeTicks", raw)
	}

	ec.visible["TimeStamp"] = true
	ty := &smi.Type{Name: "TimeStamp", Base: smi.BaseTimeTicks}
	r := naturalResolved(ec, "changedAt", ty)
	if named := r.Kind.GoString(); !strings.Contains(named, "KindTimeTicks") {
		t.Errorf("TimeStamp resolved to %s; want KindTimeTicks", named)
	}
	if tier := classifyTier(&smi.Node{Name: "changedAt", Type: ty}, r.Variant); tier != "snmp.TierIndicator" {
		t.Errorf("TimeStamp tier = %s, want snmp.TierIndicator", tier)
	}
}

// TestResolve_TypeNotImportedIsNotAvailable pins that the emitter holds
// a module to its IMPORTS clause. MIKROTIK-MIB writes SYNTAX Unsigned32
// without importing it, and the binding falls back to octets rather
// than to a definition the author never claimed.
// TestResolve_ImportsGateNamesNotBaseTypes pins where the IMPORTS check
// stops. A name has to be imported, because the import is what says
// which module's definition is meant and guessing binds to the wrong
// one. A base type names no module -- RFC 2578 §7.1 defines it -- so a
// module that writes one without importing it has said what it means and
// only failed to say where it came from. Withholding the wire type there
// does not refuse the declaration, it emits a byte string for a number.
func TestResolve_ImportsGateNamesNotBaseTypes(t *testing.T) {
	mod := &smi.Module{Name: "TEST-MIB"}
	ec := newEmitCtx(mod, &smi.ModuleSet{}, Module{Name: "TEST-MIB", Package: "testmib"}, nil, "")

	got := kindOf(t, ec, &smi.Type{Base: smi.BaseUnsigned32})
	if !strings.Contains(got, "KindUinteger32") {
		t.Errorf("un-imported Unsigned32 resolved to %s; want its own wire kind", got)
	}

	got = kindOf(t, ec, &smi.Type{Name: "SomebodyElsesTC", Base: smi.BaseUnsigned32})
	if !strings.Contains(got, "KindOctetString") {
		t.Errorf("un-imported named type resolved to %s; want the octet-string fallback", got)
	}
}

// TestEnumKey_InlineEnumKeysByDeclaringObject pins that an enumeration
// written inline gets one Go type per declaring object, while a named
// type gets one however many objects name it.
func TestEnumKey_InlineEnumKeysByDeclaringObject(t *testing.T) {
	inline := &smi.Type{Base: smi.BaseInteger, Members: []smi.Member{{Name: "up", Number: 1}}}
	if got := enumKey("fakeStatus", inline); got != "node:fakeStatus" {
		t.Errorf("inline enum key = %q; want node:fakeStatus", got)
	}
	if got := enumKey("otherObject", inline); got != "node:otherObject" {
		t.Errorf("inline enum on a second object keyed as %q; want its own key", got)
	}

	named := &smi.Type{Name: "IfOperStatus", Base: smi.BaseInteger, Members: inline.Members}
	if got := enumKey("ifOperStatus", named); got != "type:IfOperStatus" {
		t.Errorf("named enum key = %q; want type:IfOperStatus", got)
	}
}

// TestEmit_InlineEnumTypeName pins the Go type an inline enumeration
// gets: the declaring object's name plus "Value", which is what tells a
// reader it was not a convention somebody named.
func TestEmit_InlineEnumTypeName(t *testing.T) {
	mod, set := loadFakeMIB(t)
	cm := Module{Name: "FAKE-MIB", Package: "fakemib"}
	out, _, err := renderModule(mod, set, cm, nil, defaultPkgPrefix)
	if err != nil {
		t.Fatalf("renderModule: %v", err)
	}
	if !strings.Contains(string(out), "type FakeStatusValue int32") {
		t.Error("inline enum on fakeStatus did not emit FakeStatusValue")
	}
}

// TestEmit_CrossModuleEnumQualifiesToItsHomeModule pins that IF-MIB's
// ifType renders as IANAifType-MIB's enum rather than as a bare int32
// or as a same-named type from somewhere else in the loaded set.
func TestEmit_CrossModuleEnumQualifiesToItsHomeModule(t *testing.T) {
	src := renderConfigured(t, "IF-MIB")
	if !strings.Contains(src, "ianaiftype.IANAifType") {
		t.Error("ifType did not qualify to the ianaiftype package")
	}
}

// TestEmit_HeaderCarriesSourcePathAndDigest pins the provenance the
// header exists for: which file the module resolved to, and what that
// file contained when the bindings were written.
func TestEmit_HeaderCarriesSourcePathAndDigest(t *testing.T) {
	root := repoRoot(t)
	src := renderConfigured(t, "IF-MIB")

	// The header prefers a path relative to the working directory and
	// falls back to the absolute one when the source sits above it,
	// which is what the test binary's directory sees.
	path := filepath.Join("spec", "mib", "ietf", "IF-MIB")
	if !strings.Contains(src, "// Source path:   "+filepath.Join(root, path)) {
		t.Errorf("header does not name %s", path)
	}

	b, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatalf("read source MIB: %v", err)
	}
	sum := sha256.Sum256(b)
	if !strings.Contains(src, "// Source SHA-256: "+hex.EncodeToString(sum[:])) {
		t.Error("header does not carry the source digest")
	}
}

// TestEmit_DescriptionWithGoLiteralHazards renders a module whose
// DESCRIPTION carries a double quote, a backslash and a newline, and
// checks the result is still a Go file. MIB text reaches the emitted
// source both as comments and as string literals, and either one would
// break on unescaped content.
func TestEmit_DescriptionWithGoLiteralHazards(t *testing.T) {
	mibDir, err := filepath.Abs("testdata/mibs")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	ietfDir, err := filepath.Abs(filepath.Join(repoRoot(t), "spec", "mib", "ietf"))
	if err != nil {
		t.Fatalf("abs ietf: %v", err)
	}
	if _, err := os.Stat(ietfDir); err != nil {
		t.Skipf("spec/mib/ietf not available: %v", err)
	}

	set, err := smi.Load([]string{"ESCAPE-MIB"}, smi.Options{SearchPaths: []string{mibDir, ietfDir}})
	if err != nil {
		t.Fatalf("load ESCAPE-MIB: %v", err)
	}
	mod, ok := set.Module("ESCAPE-MIB")
	if !ok {
		t.Fatal("ESCAPE-MIB missing from the resolved set")
	}

	cm := Module{Name: "ESCAPE-MIB", Package: "escapemib"}
	out, _, err := renderModule(mod, set, cm, nil, defaultPkgPrefix)
	if err != nil {
		t.Fatalf("renderModule: %v", err)
	}

	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "escapemib_mib.go", out, parser.AllErrors|parser.ParseComments); err != nil {
		t.Fatalf("emitted source does not parse: %v\n--- emitted ---\n%s", err, out)
	}

	// The hazards have to be in the file for the parse above to prove
	// anything. The description reaches a comment, where a backslash is
	// ordinary text, and the member names reach string literals.
	src := string(out)
	if !strings.Contains(src, `C:\temp\x`) {
		t.Errorf("the backslash-bearing description did not reach the emitted source:\n%s", src)
	}
	if !strings.Contains(src, `return "with-hyphen"`) {
		t.Errorf("the enum member did not reach a string literal:\n%s", src)
	}
}

// TestCheckSourceNames_RejectsNameOutsideTheDescriptorSet pins the
// refusal: a name camelCase would silently drop a character from does
// not become a Go identifier, it fails the build.
func TestCheckSourceNames_RejectsNameOutsideTheDescriptorSet(t *testing.T) {
	mod := &smi.Module{
		Name:  "TEST-MIB",
		Nodes: []*smi.Node{{Name: "ifµSpeed"}},
	}
	err := checkSourceNames(mod)
	if err == nil {
		t.Fatal("expected a refusal for a name outside the descriptor set")
	}
	if !strings.Contains(err.Error(), "if") {
		t.Errorf("refusal does not name the offending object: %v", err)
	}

	ok := &smi.Module{
		Name:  "TEST-MIB",
		Nodes: []*smi.Node{{Name: "ifSpeed"}, {Name: "if-gsn_2"}},
	}
	if err := checkSourceNames(ok); err != nil {
		t.Errorf("descriptor-set names refused: %v", err)
	}
}

// TestRefuseUnresolved_FailsRatherThanEmittingPartially pins the
// refusal a partial package would otherwise hide: a declaration that
// lost its SYNTAX still has a name and an OID, so it would emit an
// accessor that compiles and decodes the wrong thing.
func TestRefuseUnresolved_FailsRatherThanEmittingPartially(t *testing.T) {
	mod := &smi.Module{
		Name:  "TEST-MIB",
		Nodes: []*smi.Node{{Name: "fine"}, {Name: "broken", Unresolved: true}},
	}
	err := refuseUnresolved(mod, nil)
	if err == nil {
		t.Fatal("expected a refusal for an unresolved declaration")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("refusal does not name the unresolved declaration: %v", err)
	}

	if err := refuseUnresolved(&smi.Module{Name: "TEST-MIB", Nodes: []*smi.Node{{Name: "fine"}}}, nil); err != nil {
		t.Errorf("whole module refused: %v", err)
	}
}
