package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunVerifyFixture(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-config", "testdata/fixture.yaml", "-verify"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run -verify = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "OK: resolved 6 modules") {
		t.Errorf("stdout = %q, want the resolved-module count", stdout.String())
	}
	if !strings.Contains(stdout.String(), "vendor fixture: 6 module(s), 0 skipped, 2 recovered") {
		t.Errorf("stdout = %q, want 2 recovered", stdout.String())
	}
}

func TestRunCheckWithoutLockfileFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-config", "testdata/fixture.yaml", "-out", t.TempDir(), "-check"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run -check without lockfile = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "no lockfile") {
		t.Errorf("stderr = %q, want a no-lockfile diagnostic", stderr.String())
	}
}

func TestRunBadFlagsExitTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-definitely-not-a-flag"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run with bad flag = %d, want 2", code)
	}
}

func TestRunMissingConfigExitOne(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-config", "testdata/no-such.yaml", "-verify"}, &stdout, &stderr); code != 1 {
		t.Fatalf("run with missing config = %d, want 1", code)
	}
}

func TestRunVerifyRejectsImportPackageCycle(t *testing.T) {
	dir := t.TempDir()
	writeYangModule(t, dir, "cyc-a.yang", `module cyc-a {
  yang-version 1.1;
  namespace "urn:flowseer:cyc-a";
  prefix a;
  import cyc-b { prefix b; }
  container a-root;
  augment "/b:b-root" { container from-a; }
}`)
	writeYangModule(t, dir, "cyc-b.yang", `module cyc-b {
  yang-version 1.1;
  namespace "urn:flowseer:cyc-b";
  prefix b;
  import cyc-a { prefix a; }
  container b-root;
  augment "/a:a-root" { container from-b; }
}`)
	config := filepath.Join(t.TempDir(), "cycle.yaml")
	source := fmt.Sprintf("vendors:\n  - name: cycle\n    paths:\n      - %q\n", dir)
	if err := os.WriteFile(config, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"-config", config, "-verify"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run -verify on cyclic tree = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "cyca -> cycb -> cyca") {
		t.Errorf("stderr = %q, want the package cycle", stderr.String())
	}
}

// TestRunVerifyRealTrees is the end-to-end load gate: all three vendored
// trees load with the committed manifest, modulo recorded skips. It
// costs tens of seconds, so -short skips it.
func TestRunVerifyRealTrees(t *testing.T) {
	if testing.Short() {
		t.Skip("full vendored-tree load skipped in -short mode")
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"-config", "yanggen.yaml", "-verify"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run -verify on real trees = %d, stderr: %s", code, stderr.String())
	}
	for vendor, count := range map[string]string{"cisco-iosxe": "113", "ruckus-icx": "1", "aruba-cx": "0"} {
		found := false
		for _, line := range strings.Split(stdout.String(), "\n") {
			if strings.HasPrefix(line, "vendor "+vendor+":") {
				found = true
				if !strings.HasSuffix(line, ", "+count+" recovered") {
					t.Errorf("vendor line = %q, want %s recovered", line, count)
				}
			}
		}
		if !found {
			t.Errorf("verify output missing vendor %s", vendor)
		}
	}
}
