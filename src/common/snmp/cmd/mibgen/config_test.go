package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadConfig_Minimal parses the committed minimal.yaml fixture and
// checks the post-resolution struct shape.
func TestLoadConfig_Minimal(t *testing.T) {
	path := filepath.Join("testdata", "configs", "minimal.yaml")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.SearchPaths) != 1 {
		t.Fatalf("SearchPaths: want 1 entry, got %v", cfg.SearchPaths)
	}
	if !filepath.IsAbs(cfg.SearchPaths[0]) {
		t.Errorf("SearchPaths[0] not absolute: %q", cfg.SearchPaths[0])
	}
	if !strings.HasSuffix(filepath.ToSlash(cfg.SearchPaths[0]), "testdata/configs/mibs") {
		t.Errorf("SearchPaths[0] = %q; want it anchored at the YAML's directory", cfg.SearchPaths[0])
	}
	if len(cfg.Modules) != 1 {
		t.Fatalf("Modules: want 1 entry, got %d", len(cfg.Modules))
	}
	m := cfg.Modules[0]
	if m.Name != "SNMPv2-SMI" || m.Package != "snmpv2smi" {
		t.Errorf("Modules[0] = %+v; want SNMPv2-SMI / snmpv2smi", m)
	}
	if len(m.DependsOn) != 0 || len(m.Overrides) != 0 {
		t.Errorf("Modules[0] should have no deps/overrides, got %+v", m)
	}
}

// TestLoadConfig_FullSchema loads the committed default mibgen.yaml and
// confirms the bundled module set is sixteen modules with unique
// names/packages.
func TestLoadConfig_FullSchema(t *testing.T) {
	cfg, err := LoadConfig("mibgen.yaml")
	if err != nil {
		t.Fatalf("LoadConfig(mibgen.yaml): %v", err)
	}
	if got, want := len(cfg.Modules), 16; got != want {
		t.Fatalf("mibgen.yaml modules: got %d, want %d", got, want)
	}
	seen := make(map[string]bool, len(cfg.Modules))
	for _, m := range cfg.Modules {
		if m.Name == "" || m.Package == "" {
			t.Errorf("blank name/package in %+v", m)
		}
		if seen[m.Name] {
			t.Errorf("duplicate name in mibgen.yaml: %s", m.Name)
		}
		seen[m.Name] = true
	}
	// Spot-check the first and last entries to confirm ordering survives.
	if cfg.Modules[0].Name != "SNMPv2-SMI" {
		t.Errorf("first module = %q; want SNMPv2-SMI", cfg.Modules[0].Name)
	}
	if cfg.Modules[len(cfg.Modules)-1].Name != "MIKROTIK-MIB" {
		t.Errorf("last module = %q; want MIKROTIK-MIB", cfg.Modules[len(cfg.Modules)-1].Name)
	}
}

// TestLoadConfig_EmptyConfig: an empty YAML document is treated as a
// fully-empty Config, which fails validation because search_paths is
// required. (Per the field documentation.)
func TestLoadConfig_EmptyConfig(t *testing.T) {
	_, err := LoadConfigBytes([]byte(""), "empty.yaml")
	if err == nil {
		t.Fatalf("LoadConfigBytes(empty) succeeded; want validation error")
	}
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("err is %T (%v); want *ConfigError", err, err)
	}
	if !strings.Contains(ce.Issue, "search_paths") {
		t.Errorf("err issue %q does not mention search_paths", ce.Issue)
	}
}

// TestLoadConfig_DuplicateModuleName ensures the diagnostic names the
// duplicate.
func TestLoadConfig_DuplicateModuleName(t *testing.T) {
	src := []byte(`
search_paths: [./mibs]
modules:
  - { name: A-MIB, package: amib }
  - { name: A-MIB, package: amib2 }
`)
	_, err := LoadConfigBytes(src, "dup-name.yaml")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "A-MIB") || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("err %q should mention A-MIB and duplicate", err.Error())
	}
}

// TestLoadConfig_DuplicatePackage ensures the diagnostic names the
// duplicate package.
func TestLoadConfig_DuplicatePackage(t *testing.T) {
	src := []byte(`
search_paths: [./mibs]
modules:
  - { name: A-MIB, package: shared }
  - { name: B-MIB, package: shared }
`)
	_, err := LoadConfigBytes(src, "dup-pkg.yaml")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "shared") {
		t.Errorf("err %q should mention the duplicated package name", err.Error())
	}
}

// TestLoadConfig_OverrideMissingGoType: an override without go_type is
// rejected; the diagnostic names the offending OID.
func TestLoadConfig_OverrideMissingGoType(t *testing.T) {
	src := []byte(`
search_paths: [./mibs]
modules:
  - name: A-MIB
    package: amib
    overrides:
      - oid: "1.3.6.1.2.1.2.2.1.7"
        go_type: ""
`)
	_, err := LoadConfigBytes(src, "no-gotype.yaml")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "1.3.6.1.2.1.2.2.1.7") {
		t.Errorf("err %q should reference the OID", err.Error())
	}
	if !strings.Contains(err.Error(), "go_type") {
		t.Errorf("err %q should mention go_type", err.Error())
	}
}

// TestLoadConfig_OverrideMalformedOID: a non-parseable OID is rejected;
// the diagnostic includes the failing OID and a parse-error fragment.
func TestLoadConfig_OverrideMalformedOID(t *testing.T) {
	src := []byte(`
search_paths: [./mibs]
modules:
  - name: A-MIB
    package: amib
    overrides:
      - oid: "1.3..bogus"
        go_type: IfAdminStatus
`)
	_, err := LoadConfigBytes(src, "bad-oid.yaml")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "1.3..bogus") {
		t.Errorf("err %q should include the malformed OID", err.Error())
	}
	if !strings.Contains(strings.ToLower(err.Error()), "oid") {
		t.Errorf("err %q should mention OID parsing", err.Error())
	}
}

// TestLoadConfig_UnknownYAMLKey verifies strict decoding rejects typos.
func TestLoadConfig_UnknownYAMLKey(t *testing.T) {
	src := []byte(`
search_paths: [./mibs]
modules:
  - name: A-MIB
    package: amib
    surprise: "extra key"
`)
	_, err := LoadConfigBytes(src, "unknown-key.yaml")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "surprise") {
		t.Errorf("err %q should mention the unknown key 'surprise'", err.Error())
	}
}

// TestLoadConfig_DependsOnUnknownModule: a depends_on entry that does
// not match any module name is rejected; the diagnostic names both
// modules.
func TestLoadConfig_DependsOnUnknownModule(t *testing.T) {
	src := []byte(`
search_paths: [./mibs]
modules:
  - name: VENDOR-MIB
    package: vendormib
    depends_on: [DOES-NOT-EXIST]
`)
	_, err := LoadConfigBytes(src, "missing-dep.yaml")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "VENDOR-MIB") {
		t.Errorf("err %q should name the dependent module", err.Error())
	}
	if !strings.Contains(err.Error(), "DOES-NOT-EXIST") {
		t.Errorf("err %q should name the missing target", err.Error())
	}
}

// TestLoadConfig_MissingSearchPath: a resolved search path that does
// not exist on disk is rejected; the diagnostic names the offending
// path and wraps os.ErrNotExist so callers can errors.Is it.
func TestLoadConfig_MissingSearchPath(t *testing.T) {
	src := []byte(`
search_paths: ["./this-directory-does-not-exist"]
modules:
  - { name: A-MIB, package: amib }
`)
	_, err := LoadConfigBytes(src, "missing-search-path.yaml")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("err is %T (%v); want *ConfigError", err, err)
	}
	if !strings.Contains(ce.Issue, "this-directory-does-not-exist") {
		t.Errorf("err issue %q should name the missing path", ce.Issue)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err %v does not wrap os.ErrNotExist", err)
	}
}

// TestLoadConfig_FileNotFound wraps os.ErrNotExist for errors.Is.
func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "no-such-file.yaml"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err %v does not wrap os.ErrNotExist", err)
	}
}

// TestLoadConfig_RelativeSearchPathResolution checks that relative
// search_paths in the YAML resolve against the YAML directory, not the
// caller's CWD. (We change CWD before calling LoadConfig to make the
// guarantee non-trivial.)
func TestLoadConfig_RelativeSearchPathResolution(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("testdata", "configs", "minimal.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	wantDir, err := filepath.Abs(filepath.Join("testdata", "configs", "mibs"))
	if err != nil {
		t.Fatal(err)
	}

	tmp := t.TempDir()
	oldwd, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	cfg, err := LoadConfig(abs)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.SearchPaths[0] != wantDir {
		t.Errorf("SearchPaths[0] = %q; want %q (resolved relative to YAML dir, not cwd %q)", cfg.SearchPaths[0], wantDir, tmp)
	}
}

// --- IndicatorDecl validation ----------------------------------

func TestLoadConfig_IndicatorScalarValid(t *testing.T) {
	src := []byte(`
search_paths: [mibs]
modules:
  - name: A-MIB
    package: amib
    indicators:
      - scalar_oid: "1.3.6.1.2.1.31.1.1.5"
        covers_tables:
          - "1.3.6.1.2.1.2.2"
`)
	cfg, err := LoadConfigBytes(src, mustTestConfigPath(t))
	if err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
	if len(cfg.Modules[0].Indicators) != 1 {
		t.Fatalf("want 1 indicator, got %d", len(cfg.Modules[0].Indicators))
	}
}

func TestLoadConfig_IndicatorPerRowOverrideValid(t *testing.T) {
	src := []byte(`
search_paths: [mibs]
modules:
  - name: A-MIB
    package: amib
    indicators:
      - column_oid: "1.3.6.1.2.1.2.2.1.9"
        table_oid:  "1.3.6.1.2.1.2.2"
`)
	if _, err := LoadConfigBytes(src, mustTestConfigPath(t)); err != nil {
		t.Errorf("expected ok, got %v", err)
	}
}

func TestLoadConfig_IndicatorScalarAndColumnMutuallyExclusive(t *testing.T) {
	src := []byte(`
search_paths: [mibs]
modules:
  - name: A-MIB
    package: amib
    indicators:
      - scalar_oid:    "1.3.6.1.2.1.31.1.1.5"
        column_oid:   "1.3.6.1.2.1.2.2.1.9"
        covers_tables: ["1.3.6.1.2.1.2.2"]
`)
	_, err := LoadConfigBytes(src, mustTestConfigPath(t))
	if err == nil {
		t.Fatal("expected mutually-exclusive error, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err %v does not mention mutual exclusion", err)
	}
}

func TestLoadConfig_IndicatorNeitherScalarNorColumn(t *testing.T) {
	src := []byte(`
search_paths: [mibs]
modules:
  - name: A-MIB
    package: amib
    indicators:
      - covers_tables: ["1.3.6.1.2.1.2.2"]
`)
	_, err := LoadConfigBytes(src, mustTestConfigPath(t))
	if err == nil {
		t.Fatal("expected error for missing scalar_oid/column_oid, got nil")
	}
}

func TestLoadConfig_IndicatorScalarMissingCoversTables(t *testing.T) {
	src := []byte(`
search_paths: [mibs]
modules:
  - name: A-MIB
    package: amib
    indicators:
      - scalar_oid: "1.3.6.1.2.1.31.1.1.5"
`)
	_, err := LoadConfigBytes(src, mustTestConfigPath(t))
	if err == nil {
		t.Fatal("expected error for missing covers_tables, got nil")
	}
	if !strings.Contains(err.Error(), "at least one covered table") {
		t.Errorf("err %v missing expected phrase", err)
	}
}

func TestLoadConfig_IndicatorEmptyCoversTablesEntry(t *testing.T) {
	src := []byte(`
search_paths: [mibs]
modules:
  - name: A-MIB
    package: amib
    indicators:
      - scalar_oid: "1.3.6.1.2.1.31.1.1.5"
        covers_tables:
          - ""
`)
	_, err := LoadConfigBytes(src, mustTestConfigPath(t))
	if err == nil {
		t.Fatal("expected error for empty covers_tables entry, got nil")
	}
}

func TestLoadConfig_IndicatorMalformedOID(t *testing.T) {
	src := []byte(`
search_paths: [mibs]
modules:
  - name: A-MIB
    package: amib
    indicators:
      - scalar_oid: "not-an-oid"
        covers_tables: ["1.3.6.1.2.1.2.2"]
`)
	_, err := LoadConfigBytes(src, mustTestConfigPath(t))
	if err == nil {
		t.Fatal("expected malformed-oid error, got nil")
	}
}

func TestLoadConfig_IndicatorPerRowMissingTableOID(t *testing.T) {
	src := []byte(`
search_paths: [mibs]
modules:
  - name: A-MIB
    package: amib
    indicators:
      - column_oid: "1.3.6.1.2.1.2.2.1.9"
`)
	_, err := LoadConfigBytes(src, mustTestConfigPath(t))
	if err == nil {
		t.Fatal("expected error for missing table_oid, got nil")
	}
}

func TestLoadConfig_IndicatorUnknownKeyRejected(t *testing.T) {
	// Strict YAML decoding catches typos: `indicator` (singular)
	// instead of `indicators`.
	src := []byte(`
search_paths: [mibs]
modules:
  - name: A-MIB
    package: amib
    indicator:
      - scalar_oid: "1.3.6.1.2.1.31.1.1.5"
        covers_tables: ["1.3.6.1.2.1.2.2"]
`)
	_, err := LoadConfigBytes(src, mustTestConfigPath(t))
	if err == nil {
		t.Fatal("expected unknown-field error, got nil")
	}
}

// mustTestConfigPath returns the absolute path to the testdata/configs
// directory, so LoadConfigBytes can resolve a relative
// `search_paths: [mibs]` entry to the on-disk fixture
// directory regardless of the test's working directory.
func mustTestConfigPath(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "configs", "minimal.yaml"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	return abs
}
