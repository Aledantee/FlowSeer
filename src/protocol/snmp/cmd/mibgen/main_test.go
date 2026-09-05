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

// seedBaseline runs -refresh-baseline so a temporary config has the
// record the generation paths now insist on. A refreshed group arrives
// pending, which the always-on gate permits — that is the state a branch
// mid-triage is in, and it is the state these CLI tests want.
func seedBaseline(t *testing.T, cfgPath string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-config", cfgPath, "-refresh-baseline"}, &stdout, &stderr); code != 0 {
		t.Fatalf("seed baseline: exit %d, stderr=%q", code, stderr.String())
	}
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
	seedBaseline(t, cfgPath)
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
	seedBaseline(t, cfgPath)
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
	seedBaseline(t, cfgPath)
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

// TestRun_MissingBaselineRefusesToEmit keeps a deleted or mistyped
// baseline path from rendering with the gate silently doing nothing.
func TestRun_MissingBaselineRefusesToEmit(t *testing.T) {
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
	code := run([]string{"-config", cfgPath, "-out", t.TempDir()}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d; want 1\nstderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "-refresh-baseline") {
		t.Errorf("stderr = %q; want it to say how to create the baseline", stderr.String())
	}
}

// TestRun_RefreshBaselineWritesAReviewableFile covers the refresh flag
// end to end: it writes the file the gate then reads, and the file names
// what the module actually raised.
func TestRun_RefreshBaselineWritesAReviewableFile(t *testing.T) {
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
	if code := run([]string{"-config", cfgPath, "-refresh-baseline"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d; want 0\nstderr=%q", code, stderr.String())
	}

	body, err := os.ReadFile(filepath.Join(filepath.Dir(cfgPath), defaultBaselineName))
	if err != nil {
		t.Fatalf("read refreshed baseline: %v", err)
	}
	for _, want := range []string{"module: SNMPv2-SMI", "smi/range-outside-base-type", "Counter32", "status: pending"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("refreshed baseline is missing %q:\n%s", want, body)
		}
	}
}

// TestRun_ReportsDegradedReferences: FAKE-MIB references a keyed
// convention from FAKE-KEYS-MIB, which this config leaves out. The run
// still succeeds and names the reference it emitted in its base type.
func TestRun_ReportsDegradedReferences(t *testing.T) {
	ietfDir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "spec", "mib", "ietf"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, statErr := os.Stat(ietfDir); statErr != nil {
		t.Skipf("spec/mib/ietf not available: %v", statErr)
	}
	fakeDir, err := filepath.Abs(filepath.Join("testdata", "mibs"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	yaml := "" +
		"search_paths:\n" +
		"  - " + fakeDir + "\n" +
		"  - " + ietfDir + "\n" +
		"modules:\n" +
		"  - { name: FAKE-MIB, package: fakemib }\n"
	cfgPath := writeTempConfig(t, yaml)
	seedBaseline(t, cfgPath)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-config", cfgPath, "-out", t.TempDir()}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d; want 0\nstdout=%q\nstderr=%q", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"OK: emitted 1 module", "fakeRef", "FakeKeyIndex", "FAKE-KEYS-MIB"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout = %q; want it to contain %q", stdout.String(), want)
		}
	}
}
