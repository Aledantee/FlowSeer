package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeManifest writes a manifest plus optional dummy .snmprec files
// into a tempdir and returns the manifest path. snmprecs is a map
// from relative-path → file content; pass nil to skip writing files
// (used by the "missing-file" negative test).
func writeManifest(t *testing.T, manifestYAML string, snmprecs map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifestYAML), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for relPath, content := range snmprecs {
		full := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write snmprec %s: %v", relPath, err)
		}
	}
	return manifestPath
}

// TestLoadManifest_Happy loads a one-entry manifest pointing at a
// real .snmprec file and asserts the entry decodes with all fields.
func TestLoadManifest_Happy(t *testing.T) {
	path := writeManifest(t, `
entries:
  - vendor: nokia
    device: srlinux-24
    captured_on: 2026-05-13
    snmpsim_context: srlinux/baseline
    snmprec: srlinux/baseline.snmprec
    scenarios:
      - dense-row-collector
`, map[string]string{
		"srlinux/baseline.snmprec": "1.3.6.1.2.1.1.1.0|4|FlowSeer Test Agent\n",
	})

	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(m.Entries))
	}
	e := m.Entries[0]
	if e.Vendor != "nokia" || e.Device != "srlinux-24" {
		t.Errorf("vendor/device = %q/%q, want nokia/srlinux-24", e.Vendor, e.Device)
	}
	if e.SnmpsimContext != "srlinux/baseline" {
		t.Errorf("snmpsim_context = %q, want srlinux/baseline", e.SnmpsimContext)
	}
	if len(e.Scenarios) != 1 || e.Scenarios[0] != "dense-row-collector" {
		t.Errorf("scenarios = %v, want [dense-row-collector]", e.Scenarios)
	}
}

// TestLoadManifest_TwoEntries proves that adding a vendor capture
// is data, not code — two entries decode and validate without any
// Go change vs the one-entry case.
func TestLoadManifest_TwoEntries(t *testing.T) {
	path := writeManifest(t, `
entries:
  - vendor: nokia
    device: srlinux-24
    captured_on: 2026-05-13
    snmpsim_context: srlinux/baseline
    snmprec: srlinux/baseline.snmprec
  - vendor: cisco
    device: ios-xe-17
    captured_on: 2026-06-01
    snmpsim_context: cisco/ios-xe
    snmprec: cisco/ios-xe.snmprec
`, map[string]string{
		"srlinux/baseline.snmprec": "1.3.6.1.2.1.1.1.0|4|nokia\n",
		"cisco/ios-xe.snmprec":     "1.3.6.1.2.1.1.1.0|4|cisco\n",
	})

	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(m.Entries))
	}
}

// TestLoadManifest_MissingSnmprecFile asserts the loader rejects a
// manifest entry pointing at a non-existent .snmprec file —
// broken entries fail loudly at load, not as silent test passes.
func TestLoadManifest_MissingSnmprecFile(t *testing.T) {
	path := writeManifest(t, `
entries:
  - vendor: nokia
    device: srlinux-24
    captured_on: 2026-05-13
    snmpsim_context: srlinux/nonexistent
    snmprec: srlinux/nonexistent.snmprec
`, nil)

	_, err := LoadManifest(path)
	if err == nil {
		t.Fatal("expected error for missing snmprec file")
	}
	if !strings.Contains(err.Error(), "nonexistent.snmprec") {
		t.Errorf("error %q does not name the missing file", err)
	}
}

// TestLoadManifest_DuplicateContext asserts the loader rejects two
// entries sharing the same snmpsim_context — the routing key snmpsim
// uses to direct requests, which must be unique.
func TestLoadManifest_DuplicateContext(t *testing.T) {
	// Two distinct files with the same context value — pin the
	// duplicate-context error before it surfaces as a snmpsim_context
	// vs snmprec mismatch error (validation order matters).
	path := writeManifest(t, `
entries:
  - vendor: nokia
    device: srlinux-24
    captured_on: 2026-05-13
    snmpsim_context: a/baseline
    snmprec: a/baseline.snmprec
  - vendor: cisco
    device: ios-xe-17
    captured_on: 2026-06-01
    snmpsim_context: a/baseline
    snmprec: b/baseline.snmprec
`, map[string]string{
		"a/baseline.snmprec": "x\n",
		"b/baseline.snmprec": "x\n",
	})

	_, err := LoadManifest(path)
	if err == nil {
		t.Fatal("expected duplicate-context error")
	}
	if !strings.Contains(err.Error(), "duplicate snmpsim_context") {
		t.Errorf("error %q does not name the duplicate", err)
	}
}

// TestLoadManifest_ContextMismatchesSnmprec asserts the loader
// rejects an entry whose snmpsim_context does not equal the snmprec
// path without its extension. snmpsim's community→context routing is
// filename-based; a mismatched entry would silently dial the wrong
// community at runtime.
func TestLoadManifest_ContextMismatchesSnmprec(t *testing.T) {
	path := writeManifest(t, `
entries:
  - vendor: nokia
    device: srlinux-24
    captured_on: 2026-05-13
    snmpsim_context: wrong-name
    snmprec: srlinux/baseline.snmprec
`, map[string]string{
		"srlinux/baseline.snmprec": "1.3.6.1.2.1.1.1.0|4|x\n",
	})

	_, err := LoadManifest(path)
	if err == nil {
		t.Fatal("expected context-mismatch error")
	}
	if !strings.Contains(err.Error(), "does not match snmprec path") {
		t.Errorf("error %q does not name the mismatch", err)
	}
}

// TestLoadManifest_PathTraversal asserts the loader rejects a
// manifest entry whose snmprec field resolves outside the manifest
// directory after filepath.Join.
func TestLoadManifest_PathTraversal(t *testing.T) {
	path := writeManifest(t, `
entries:
  - vendor: nokia
    device: srlinux-24
    captured_on: 2026-05-13
    snmpsim_context: "../../../etc/passwd"
    snmprec: "../../../etc/passwd.snmprec"
`, nil)

	_, err := LoadManifest(path)
	if err == nil {
		t.Fatal("expected path-traversal error")
	}
	if !strings.Contains(err.Error(), "escapes the manifest directory") {
		t.Errorf("error %q does not name the traversal", err)
	}
}

// TestLoadManifest_MalformedYAML asserts the loader surfaces decode
// errors with a path-anchored message rather than a bare yaml.v3
// stack.
func TestLoadManifest_MalformedYAML(t *testing.T) {
	path := writeManifest(t, "entries: [not valid yaml :::\n", nil)
	_, err := LoadManifest(path)
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !strings.Contains(err.Error(), "manifest") || !strings.Contains(err.Error(), "decode") {
		t.Errorf("error %q does not surface decode failure with manifest path", err)
	}
}

// TestLoadManifest_MissingRequiredFields asserts each required
// scalar field surfaces a named-field error.
func TestLoadManifest_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "missing vendor",
			yaml: "entries:\n  - device: x\n    captured_on: 2026-05-13\n    snmpsim_context: c\n    snmprec: f.snmprec\n",
			want: "empty vendor",
		},
		{
			name: "missing device",
			yaml: "entries:\n  - vendor: v\n    captured_on: 2026-05-13\n    snmpsim_context: c\n    snmprec: f.snmprec\n",
			want: "empty device",
		},
		{
			name: "missing context",
			yaml: "entries:\n  - vendor: v\n    device: d\n    captured_on: 2026-05-13\n    snmprec: f.snmprec\n",
			want: "empty snmpsim_context",
		},
		{
			name: "missing snmprec",
			yaml: "entries:\n  - vendor: v\n    device: d\n    captured_on: 2026-05-13\n    snmpsim_context: c\n",
			want: "empty snmprec",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeManifest(t, tc.yaml, nil)
			_, err := LoadManifest(path)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}
