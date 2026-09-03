package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig materializes a config in a temp dir with a modules/
// directory present, so path-existence validation passes unless a
// test wants otherwise.
func writeConfig(t *testing.T, yaml string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "yanggen.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	return path, err
}

func TestLoadConfigValid(t *testing.T) {
	path, err := writeConfig(t, `
vendors:
  - name: acme
    paths: [modules]
    skip:
      - module: broken-mod
        reason: "goyang cannot parse it"
      - pattern: "*-deviation"
        reason: "platform bundles excluded"
    package_overrides:
      - module: colliding-mod
        package: collide2
`)
	if err != nil {
		t.Fatalf("LoadConfig(%s): %v", path, err)
	}
}

func TestLoadConfigRejections(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string // substring of the ConfigError issue
	}{
		{
			name: "unknown key",
			yaml: "vendors:\n  - name: acme\n    paths: [modules]\n    frobnicate: true\n",
			want: "frobnicate",
		},
		{
			name: "missing search path",
			yaml: "vendors:\n  - name: acme\n    paths: [no-such-dir]\n",
			want: "does not exist on disk",
		},
		{
			name: "no vendors",
			yaml: "vendors: []\n",
			want: "vendors is empty",
		},
		{
			name: "duplicate vendor",
			yaml: "vendors:\n  - name: acme\n    paths: [modules]\n  - name: acme\n    paths: [modules]\n",
			want: "duplicate vendor name",
		},
		{
			name: "bad vendor name",
			yaml: "vendors:\n  - name: Acme_Corp\n    paths: [modules]\n",
			want: "lowercase",
		},
		{
			name: "skip without reason",
			yaml: "vendors:\n  - name: acme\n    paths: [modules]\n    skip:\n      - module: foo\n",
			want: "reason is required",
		},
		{
			name: "skip with module and pattern",
			yaml: "vendors:\n  - name: acme\n    paths: [modules]\n    skip:\n      - module: foo\n        pattern: \"foo*\"\n        reason: x\n",
			want: "mutually exclusive",
		},
		{
			name: "duplicate package override name",
			yaml: "vendors:\n  - name: acme\n    paths: [modules]\n    package_overrides:\n      - {module: a, package: pkg}\n      - {module: b, package: pkg}\n",
			want: `package override name "pkg" used twice`,
		},
		{
			name: "invalid package name",
			yaml: "vendors:\n  - name: acme\n    paths: [modules]\n    package_overrides:\n      - {module: a, package: Not-Go}\n",
			want: "not a lowercase Go identifier",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := writeConfig(t, tc.yaml)
			if err == nil {
				t.Fatal("LoadConfig succeeded, want a validation error")
			}
			var ce *ConfigError
			if !errors.As(err, &ce) {
				t.Fatalf("error is %T (%v), want *ConfigError", err, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadConfigResolvesRelativePaths(t *testing.T) {
	path, err := writeConfig(t, "vendors:\n  - name: acme\n    paths: [modules]\n")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Vendors[0].Paths[0]
	if !filepath.IsAbs(got) {
		t.Errorf("resolved path %q is not absolute", got)
	}
	if want := filepath.Join(filepath.Dir(path), "modules"); got != want {
		t.Errorf("resolved path = %q, want %q", got, want)
	}
}
