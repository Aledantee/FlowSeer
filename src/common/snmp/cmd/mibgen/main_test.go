package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempConfig drops a YAML file under t.TempDir() and returns its
// absolute path. The MIB search path embedded in the YAML is computed
// from the test working directory so it resolves to the real
// spec/mib/ietf regardless of where the test binary runs from.
func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mibgen.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

// TestRun_Verify_HappyPath: writes a tiny config that points at the
// real spec/mib/ietf and runs -verify; expects exit 0 and the load
// summary on stdout.
func TestRun_Verify_HappyPath(t *testing.T) {
	mibDir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "spec", "mib", "ietf"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, statErr := os.Stat(mibDir); statErr != nil {
		t.Skipf("spec/mib/ietf not available: %v", statErr)
	}

	yaml := "" +
		"search_paths:\n" +
		"  - " + mibDir + "\n" +
		"modules:\n" +
		"  - { name: SNMPv2-SMI, package: snmpv2smi }\n"
	cfgPath := writeTempConfig(t, yaml)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-config", cfgPath, "-verify"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d; want 0\nstdout=%q\nstderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "OK: loaded 1 modules") {
		t.Errorf("stdout = %q; want it to contain 'OK: loaded 1 modules'", stdout.String())
	}
}

// TestRun_DefaultEmit: with no mode flag, the run path now emits a
// full Go binding for each module. We use a fresh tmpdir as -out so
// the test never touches the committed bindings tree.
func TestRun_DefaultEmit(t *testing.T) {
	mibDir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "spec", "mib", "ietf"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, statErr := os.Stat(mibDir); statErr != nil {
		t.Skipf("spec/mib/ietf not available: %v", statErr)
	}

	yaml := "" +
		"search_paths:\n" +
		"  - " + mibDir + "\n" +
		"modules:\n" +
		"  - { name: SNMPv2-SMI, package: snmpv2smi }\n"
	cfgPath := writeTempConfig(t, yaml)
	out := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := run([]string{"-config", cfgPath, "-out", out}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d; want 0\nstdout=%q\nstderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "OK: emitted 1 module") {
		t.Errorf("stdout = %q; want 'OK: emitted 1 module'", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(out, "snmpv2smi", "mib.go")); err != nil {
		t.Errorf("expected emitted mib.go: %v", err)
	}
}

// TestRun_CheckMatches: regenerate into an out dir, then -check against
// that same dir; expect exit 0.
func TestRun_CheckMatches(t *testing.T) {
	mibDir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "spec", "mib", "ietf"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, statErr := os.Stat(mibDir); statErr != nil {
		t.Skipf("spec/mib/ietf not available: %v", statErr)
	}

	yaml := "" +
		"search_paths:\n" +
		"  - " + mibDir + "\n" +
		"modules:\n" +
		"  - { name: SNMPv2-SMI, package: snmpv2smi }\n"
	cfgPath := writeTempConfig(t, yaml)
	out := t.TempDir()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-config", cfgPath, "-out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("emit failed: stderr=%q", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	if code := run([]string{"-config", cfgPath, "-out", out, "-check"}, &stdout, &stderr); code != 0 {
		t.Fatalf("check exit = %d; want 0\nstdout=%q\nstderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "match committed output") {
		t.Errorf("stdout = %q; want 'match committed output'", stdout.String())
	}
}

// TestRun_UpdateWrites: -update writes the bindings into -out, same as
// the default mode; the verb exists so callers can be explicit.
func TestRun_UpdateWrites(t *testing.T) {
	mibDir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "spec", "mib", "ietf"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, statErr := os.Stat(mibDir); statErr != nil {
		t.Skipf("spec/mib/ietf not available: %v", statErr)
	}

	yaml := "" +
		"search_paths:\n" +
		"  - " + mibDir + "\n" +
		"modules:\n" +
		"  - { name: SNMPv2-SMI, package: snmpv2smi }\n"
	cfgPath := writeTempConfig(t, yaml)
	out := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := run([]string{"-config", cfgPath, "-out", out, "-update"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d; want 0\nstdout=%q\nstderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "regenerated") {
		t.Errorf("stdout = %q; want it to mention 'regenerated'", stdout.String())
	}
}

// TestRun_BadConfig: a non-existent config path exits 1 with stderr
// diagnostic.
func TestRun_BadConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-config", filepath.Join(t.TempDir(), "missing.yaml"), "-verify"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d; want 1", code)
	}
	if stderr.Len() == 0 {
		t.Errorf("stderr empty; want diagnostic")
	}
}

// TestRun_BadFlag: an unknown flag exits 2 (flag.Parse error path).
func TestRun_BadFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--definitely-not-a-flag"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d; want 2\nstderr=%q", code, stderr.String())
	}
}

// TestRun_HelpFlag: -h exits 0.
func TestRun_HelpFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-h"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d; want 0 for -h\nstderr=%q", code, stderr.String())
	}
	// Help text goes to stderr by our flag.FlagSet output configuration.
	if !strings.Contains(stderr.String(), "Usage: mibgen") {
		t.Errorf("stderr = %q; want usage banner", stderr.String())
	}
}

// TestRun_LoadCycle: a config whose depends_on edges form a cycle
// passes validateConfig and fails in topoSort, so run prints the cycle
// diagnostic and exits 1 like any other load failure.
func TestRun_LoadCycle(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-config", filepath.Join("testdata", "configs", "cycle.yaml"), "-verify"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d; want 1\nstderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "dependency cycle in modules") {
		t.Errorf("stderr = %q; want the cycle diagnostic", stderr.String())
	}
}
