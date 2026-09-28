package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRootTree writes a root go.mod into a temporary directory and
// returns the output directory yanggen would use beneath it.
func newRootTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	rootMod := "module go.aledante.io/FlowSeer\n\ngo 1.27\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(rootMod), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(root, "generated", "go", "yang")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return outDir
}

func TestGoModGolden(t *testing.T) {
	m, err := resolveOutputModule(newRootTree(t))
	if err != nil {
		t.Fatalf("resolveOutputModule: %v", err)
	}
	got := m.render()
	goldenPath := filepath.Join("testdata", "golden", "go.mod.golden")
	if *updateGolden {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (rerun with -update-golden if first time): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("go.mod mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestResolveOutputModuleWithoutRoot(t *testing.T) {
	if _, err := resolveOutputModule(t.TempDir()); err == nil {
		t.Fatal("resolveOutputModule without a root go.mod succeeded, want an error")
	}
}

// writeCheckInputs puts a lockfile matching the fixture and the given
// go.mod under outDir, so -check fails on the go.mod alone.
func writeCheckInputs(t *testing.T, outDir string, goMod []byte) {
	t.Helper()
	cfg, err := LoadConfig("testdata/fixture.yaml")
	if err != nil {
		t.Fatal(err)
	}
	sets, err := LoadVendors(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteLockfile(BuildLockfile(sets), outDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "go.mod"), goMod, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunCheckGoMod(t *testing.T) {
	// A tidied go.mod carries indirect requirements the generator did
	// not write; -check accepts them.
	tidied := "module go.aledante.io/FlowSeer/generated/go/yang\n\ngo 1.27\n\n" +
		"require go.aledante.io/FlowSeer v0.0.0-00010101000000-000000000000\n\n" +
		"require google.golang.org/protobuf v1.36.12 // indirect\n\n" +
		"replace go.aledante.io/FlowSeer => ../../..\n"
	tests := []struct {
		name     string
		goMod    string
		wantCode int
		wantErr  string
	}{
		{name: "tidied", goMod: tidied, wantCode: 0},
		{
			name:     "wrong go directive",
			goMod:    strings.Replace(tidied, "go 1.27", "go 1.26", 1),
			wantCode: 1,
			wantErr:  "go directive is not 1.27",
		},
		{
			name:     "replace removed",
			goMod:    strings.Replace(tidied, "replace go.aledante.io/FlowSeer => ../../..\n", "", 1),
			wantCode: 1,
			wantErr:  "no replace go.aledante.io/FlowSeer => ../../..",
		},
		{
			name:     "module path changed",
			goMod:    strings.Replace(tidied, "generated/go/yang", "generated/yang", 1),
			wantCode: 1,
			wantErr:  "module path is not go.aledante.io/FlowSeer/generated/go/yang",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outDir := newRootTree(t)
			writeCheckInputs(t, outDir, []byte(tt.goMod))
			var stdout, stderr bytes.Buffer
			code := run([]string{"-config", "testdata/fixture.yaml", "-out", outDir, "-check"}, &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("run -check = %d, want %d; stderr: %s", code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantErr)
			}
		})
	}
}
