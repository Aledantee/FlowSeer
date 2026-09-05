package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// fakeVendorModules is the configuration the identity golden renders
// under: two naming-node-only vendor modules, listed out of name order so
// the deduplication tiebreak is the module name and not the config order.
var fakeVendorModules = []Module{
	{Name: "FAKE-VENDOR-B-MIB", Package: "fakevendorb"},
	{Name: "FAKE-VENDOR-A-MIB", Package: "fakevendora"},
}

// loadFakeVendors resolves both fake vendor modules from testdata/mibs.
func loadFakeVendors(t *testing.T) (*Config, *smi.ModuleSet) {
	t.Helper()
	mibDir, err := filepath.Abs("testdata/mibs")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	ietfDir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "spec", "mib", "ietf"))
	if err != nil {
		t.Fatalf("abs ietf: %v", err)
	}
	if _, err := os.Stat(ietfDir); err != nil {
		t.Skipf("spec/mib/ietf not available: %v", err)
	}

	names := make([]string, 0, len(fakeVendorModules))
	for _, m := range fakeVendorModules {
		names = append(names, m.Name)
	}
	set, err := smi.Load(names, smi.Options{SearchPaths: []string{mibDir, ietfDir}})
	if err != nil {
		t.Fatalf("load fake vendor modules: %v", err)
	}

	return &Config{Modules: fakeVendorModules}, set
}

// TestEmit_Identity_Golden renders the identity package from both fake
// vendor modules and compares it against testdata/golden/sysobjectid.
// Use -update-golden after any intentional emitter change.
func TestEmit_Identity_Golden(t *testing.T) {
	cfg, set := loadFakeVendors(t)

	got, _, err := renderIdentity(cfg, set, goldenPkgPrefix)
	if err != nil {
		t.Fatalf("renderIdentity: %v", err)
	}

	goldenPath := filepath.Join("testdata", "golden", identityPackage, "mib.go")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated %s (%d bytes)", goldenPath, len(got))
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (rerun with -update-golden if first time): %v", err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("golden mismatch: %s; rerun with -update-golden after auditing the diff", goldenPath)
		_ = os.WriteFile(goldenPath+".got", got, 0o644)
	}
}

// TestIdentity_CollectsEnterpriseNamingNodes pins the table's contents:
// every naming node under enterprises from both modules in OID order,
// one entry per OID attributed to the module first in name order, and
// nothing from outside the enterprises subtree.
func TestIdentity_CollectsEnterpriseNamingNodes(t *testing.T) {
	cfg, set := loadFakeVendors(t)

	entries := collectIdentity(cfg, set)

	want := []struct{ oid, name, module string }{
		{"1.3.6.1.4.1.99996.1", "fakeSharedByA", "FAKE-VENDOR-A-MIB"},
		{"1.3.6.1.4.1.99997", "fakeVendorB", "FAKE-VENDOR-B-MIB"},
		{"1.3.6.1.4.1.99997.1", "fakeVendorBProducts", "FAKE-VENDOR-B-MIB"},
		{"1.3.6.1.4.1.99997.1.1", "fakeVendorBAccessPoint", "FAKE-VENDOR-B-MIB"},
		{"1.3.6.1.4.1.99998", "fakeVendorA", "FAKE-VENDOR-A-MIB"},
		{"1.3.6.1.4.1.99998.1", "fakeVendorAProducts", "FAKE-VENDOR-A-MIB"},
		{"1.3.6.1.4.1.99998.1.1", "fakeVendorASwitchFamily", "FAKE-VENDOR-A-MIB"},
		{"1.3.6.1.4.1.99998.1.1.1", "fakeVendorASwitch24", "FAKE-VENDOR-A-MIB"},
		{"1.3.6.1.4.1.99998.1.1.2", "fakeVendorASwitch48", "FAKE-VENDOR-A-MIB"},
		{"1.3.6.1.4.1.99998.1.2", "fakeVendorARouterFamily", "FAKE-VENDOR-A-MIB"},
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i, w := range want {
		e := entries[i]
		if e.OID.String() != w.oid || e.Name != w.name || e.Module != w.module {
			t.Errorf("entry %d = %s %s from %s; want %s %s from %s",
				i, e.OID, e.Name, e.Module, w.oid, w.name, w.module)
		}
	}
	for _, e := range entries {
		if e.Name == "fakeVendorAExperimental" || e.Name == "fakeSharedByB" {
			t.Errorf("entry %s must not be in the table", e.Name)
		}
	}
}

// TestIdentity_DescriptionsAreTrimmed pins the first-paragraph rule and
// the 240-character cap at a word boundary.
func TestIdentity_DescriptionsAreTrimmed(t *testing.T) {
	cfg, set := loadFakeVendors(t)

	byName := map[string]identityEntry{}
	for _, e := range collectIdentity(cfg, set) {
		byName[e.Name] = e
	}

	products := byName["fakeVendorAProducts"].Description
	if len(products) > identityDescriptionCap {
		t.Errorf("fakeVendorAProducts description is %d chars, cap is %d", len(products), identityDescriptionCap)
	}
	if !strings.HasPrefix(products, "Registration point for every Fake Vendor A product. A sysObjectID under this node") {
		t.Errorf("fakeVendorAProducts description not collapsed to one line: %q", products)
	}
	if strings.Contains(products, "second paragraph") {
		t.Errorf("fakeVendorAProducts description carries the second paragraph: %q", products)
	}
	if strings.HasSuffix(products, " ") || strings.Contains(products, "  ") {
		t.Errorf("fakeVendorAProducts description has stray whitespace: %q", products)
	}

	if got := byName["fakeVendorASwitch24"].Description; got != "The 24-port switch." {
		t.Errorf("fakeVendorASwitch24 description = %q; want the first paragraph only", got)
	}
	if got := byName["fakeVendorASwitch48"].Description; got != "" {
		t.Errorf("plain OID assignment description = %q; want empty", got)
	}
}

func TestTrimIdentityDescription(t *testing.T) {
	long := strings.Repeat("word ", 60) + "tail"
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"whitespace", "  \n\t ", ""},
		{"collapses", "a  b\n   c", "a b c"},
		{"first paragraph", "first\n   line\n\n   second", "first line"},
		{"blank line with spaces", "first\n   \n second", "first"},
		{"cap at word boundary", long, strings.TrimSpace(strings.Repeat("word ", 48))},
		{"exactly at cap", strings.Repeat("x", identityDescriptionCap), strings.Repeat("x", identityDescriptionCap)},
		{"one long word", strings.Repeat("x", identityDescriptionCap+1), strings.Repeat("x", identityDescriptionCap)},
	}
	for _, c := range cases {
		if got := trimIdentityDescription(c.in); got != c.want {
			t.Errorf("%s: trimIdentityDescription(%q) = %q; want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestIdentity_ImportsOnlySNMP asserts the rendered package depends on
// the SNMP library alone, so it can never pull a per-module package in.
func TestIdentity_ImportsOnlySNMP(t *testing.T) {
	cfg, set := loadFakeVendors(t)

	src, report, err := renderIdentity(cfg, set, goldenPkgPrefix)
	if err != nil {
		t.Fatalf("renderIdentity: %v", err)
	}
	if report.Nodes != 10 || report.Modules != 2 {
		t.Errorf("report = %+v; want 10 nodes from 2 modules", report)
	}

	f, err := parser.ParseFile(token.NewFileSet(), "mib.go", src, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, src)
	}
	if len(f.Imports) != 1 || f.Imports[0].Path.Value != `"`+snmpImport+`"` {
		var paths []string
		for _, imp := range f.Imports {
			paths = append(paths, imp.Path.Value)
		}
		t.Errorf("imports = %v; want only %q", paths, snmpImport)
	}
	if f.Name.Name != identityPackage {
		t.Errorf("package = %s; want %s", f.Name.Name, identityPackage)
	}
}

// TestRun_CheckReportsIdentityDrift: -check compares the identity package
// too, so an edit to it fails the run like a per-module drift does.
func TestRun_CheckReportsIdentityDrift(t *testing.T) {
	mibDir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "spec", "mib", "ietf"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, statErr := os.Stat(mibDir); statErr != nil {
		t.Skipf("spec/mib/ietf not available: %v", statErr)
	}
	fakeDir, err := filepath.Abs("testdata/mibs")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	yaml := "" +
		"search_paths:\n" +
		"  - " + mibDir + "\n" +
		"  - " + fakeDir + "\n" +
		"modules:\n" +
		"  - { name: FAKE-VENDOR-A-MIB, package: fakevendora }\n"
	cfgPath := writeTempConfig(t, yaml)
	seedBaseline(t, cfgPath)
	out := t.TempDir()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-config", cfgPath, "-out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("emit failed: stderr=%q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "OK: identity table with 7 naming nodes from 1 module(s)") {
		t.Errorf("stdout = %q; want the identity count line", stdout.String())
	}

	identityPath := filepath.Join(out, identityPackage, "mib.go")
	if err := os.WriteFile(identityPath, []byte("package sysobjectid\n"), 0o644); err != nil {
		t.Fatalf("overwrite identity package: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-config", cfgPath, "-out", out, "-check"}, &stdout, &stderr); code != 1 {
		t.Fatalf("check exit = %d; want 1\nstdout=%q\nstderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), identityPackage) {
		t.Errorf("stderr = %q; want it to name the identity package", stderr.String())
	}
}

// TestRun_RealConfigEmitsIdentity runs the repository config into a
// temporary directory: MIKROTIK-MIB sits under enterprises 14988, so the
// identity table is non-empty and its source parses.
func TestRun_RealConfigEmitsIdentity(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	cfgPath := filepath.Join(root, "mibgen.yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Skipf("repository config not available: %v", err)
	}
	out := t.TempDir()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-config", cfgPath, "-out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("emit failed: exit %d\nstdout=%q\nstderr=%q", code, stdout.String(), stderr.String())
	}
	src, err := os.ReadFile(filepath.Join(out, identityPackage, "mib.go"))
	if err != nil {
		t.Fatalf("read identity package: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "mib.go", src, parser.AllErrors); err != nil {
		t.Fatalf("parse identity package: %v", err)
	}
	if !strings.Contains(string(src), "snmp.MustOID(1, 3, 6, 1, 4, 1, 14988)") {
		t.Error("identity package does not carry the MikroTik enterprise node")
	}
}
