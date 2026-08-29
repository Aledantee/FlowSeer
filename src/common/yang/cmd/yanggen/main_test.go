package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVerifyFixture(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-config", "testdata/fixture.yaml", "-verify"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run -verify = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "OK: resolved 4 modules") {
		t.Errorf("stdout = %q, want the resolved-module count", stdout.String())
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
	for _, vendor := range []string{"cisco-iosxe", "ruckus-icx", "aruba-cx"} {
		if !strings.Contains(stdout.String(), "vendor "+vendor+":") {
			t.Errorf("verify output missing vendor %s", vendor)
		}
	}
}
